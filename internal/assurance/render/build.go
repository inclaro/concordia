package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/yamlparse"
)

// builder collects the documents of one source and turns them into a Result.
type builder struct {
	ctx         docContext
	entries     []entry
	gaps        []assurance.Gap
	redactions  []redact.Record
	diagnostics []diag.Diagnostic
	// documents counts every document read, to apply the resource limit before anything is kept.
	documents int
}

// input is one text of rendered or literal manifests.
type input struct {
	// file names the text in redaction records and diagnostics: a workspace-relative path.
	file string
	text string
	// location maps the `# Source:` comment of a document (Helm) to a repository path; nil when the text has no such
	// comment. A document with no comment is located at fallback.
	location func(source string) string
	fallback string
}

var (
	separatorLine = regexp.MustCompile(`^(---|\.{3})(\s|$)`)
	sourceComment = regexp.MustCompile(`^#\s*Source:\s*(\S.*?)\s*$`)
)

type chunk struct {
	text   string
	source string
}

// splitDocuments cuts a YAML stream at the separator lines and keeps the Helm `# Source:` comment of each part.
func splitDocuments(text string) []chunk {
	var (
		out     []chunk
		current []string
		source  string
	)

	flush := func() {
		if len(current) > 0 {
			out = append(out, chunk{text: strings.Join(current, "\n"), source: source})
		}

		current, source = nil, ""
	}

	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if separatorLine.MatchString(line) {
			flush()

			continue
		}

		if m := sourceComment.FindStringSubmatch(line); m != nil {
			source = m[1]
		}

		current = append(current, line)
	}

	flush()

	return out
}

func isBlankDocument(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			return false
		}
	}

	return true
}

// add redacts the text, then reads its documents. Redaction comes first, so the typed reader never sees a secret
// value. Documents that cannot be read become gaps: nothing is skipped silently.
func (b *builder) add(src input) {
	redacted, records := redact.Default().Redact(src.file, src.text)
	b.redactions = append(b.redactions, records...)

	for i, c := range splitDocuments(redacted) {
		if isBlankDocument(c.text) {
			continue
		}

		location := src.fallback
		if src.location != nil && c.source != "" {
			location = src.location(c.source)
		}

		value, problems := yamlparse.Parse([]byte(c.text))
		if len(problems) > 0 {
			b.gap(GapUnparseableDocument, fmt.Sprintf("document %d: %s", i+1, describeProblem(problems[0])), location)

			continue
		}

		b.addValue(value, location, i+1)
	}
}

func describeProblem(p yamlparse.Problem) string {
	if p.Line > 0 {
		return fmt.Sprintf("line %d: %s", p.Line, p.Message)
	}

	return p.Message
}

func (b *builder) addValue(value any, location string, number int) {
	doc := asMap(value)
	if doc == nil {
		if value != nil {
			b.gap(GapUnparseableDocument, fmt.Sprintf("document %d is not a mapping", number), location)
		}

		return
	}

	// A List holds several objects.
	if items := listItems(doc); items != nil {
		for _, item := range items {
			b.addValue(item, location, number)
		}

		return
	}

	b.documents++

	if !hasKindAndName(doc) {
		b.gap(GapInvalidResource, fmt.Sprintf("document %d has no kind or no metadata.name", number), location)

		return
	}

	b.addResource(doc, location, number)
}

// listItems gives the objects of a `kind: *List` document, or nil when the document is not a list.
func listItems(doc map[string]any) []any {
	items := asList(doc["items"])
	if !strings.HasSuffix(str(doc, "kind"), "List") {
		return nil
	}

	return items
}

func hasKindAndName(doc map[string]any) bool {
	return str(doc, "kind") != "" && str(asMap(doc["metadata"]), "name") != ""
}

// addResource reads one valid resource document and keeps it with its canonical JSON and digest.
func (b *builder) addResource(doc map[string]any, location string, number int) {
	dctx := b.ctx
	dctx.location = location

	res, occs, gaps := readDocument(doc, &dctx)
	b.gaps = append(b.gaps, gaps...)

	canonical, err := canonjson.Marshal(doc)
	if err != nil {
		b.gap(GapUnparseableDocument, fmt.Sprintf("document %d cannot be written as JSON: %v", number, err), location)

		return
	}

	res.Digest = digest.OfBytes(canonical)
	b.entries = append(b.entries, entry{resource: res, occurrences: occs, canonical: canonical})
}

func (b *builder) gap(kind, detail, location string) {
	b.gaps = append(b.gaps, assurance.Gap{Kind: kind, Detail: detail, Path: location})
}

func (b *builder) warn(code, detail, location string) {
	b.diagnostics = append(b.diagnostics, newDiagnostic(code, detail, location))
}

func newDiagnostic(code, detail, location string) diag.Diagnostic {
	var opts []diag.Option
	if location != "" {
		opts = append(opts, diag.WithSource(diag.Source{URI: location}))
	}

	return diag.New(code, diag.Detail(detail), opts...)
}

// finish settles identities, resolves selections, and computes the output digest and the coverage. The caller sets
// the result header (environment, tool, renderer, confinement, input digest) before or after.
func (b *builder) finish(res *Result, limits Limits) {
	if limits.MaxResources > 0 && b.documents > limits.MaxResources {
		b.fail(res, "resource-limit", fmt.Sprintf("the render has %d resources, above the limit of %d", b.documents, limits.MaxResources))

		return
	}

	sortEntries(b.entries)

	// The output digest covers every document, also the ones that conflict: a changed duplicate changes the output.
	hash := sha256.New()
	for i := range b.entries {
		_, _ = fmt.Fprintf(hash, "%s\n%s", &b.entries[i].resource.ID, b.entries[i].canonical)
	}

	res.OutputDigest = digest.Prefix + hex.EncodeToString(hash.Sum(nil))

	kept, conflicts := settleIdentities(b.entries)

	for _, c := range conflicts {
		detail := fmt.Sprintf("%d documents have the identity %s", len(c.Locations), c.ID)
		b.gap(GapDuplicateIdentity, detail, c.Locations[0])
		b.warn(diag.PRV007, detail+" ("+strings.Join(dedupe(c.Locations), ", ")+")", c.Locations[0])
	}

	for i := range kept {
		res.Resources = append(res.Resources, kept[i].resource)
		res.Occurrences = append(res.Occurrences, kept[i].occurrences...)
	}

	sortOccurrences(res.Occurrences)
	res.Selections = computeSelections(res.Resources)

	b.seal(res)
}

// seal copies the collected gaps, redactions, and diagnostics into the result and sets the coverage.
func (b *builder) seal(res *Result) {
	res.Gaps = append([]assurance.Gap(nil), b.gaps...)
	sortGaps(res.Gaps)

	res.Redactions = append([]redact.Record(nil), b.redactions...)
	slices.SortStableFunc(res.Redactions, compareRecords)

	diagnostics := append([]diag.Diagnostic(nil), b.diagnostics...)
	diagnostics = append(diagnostics, gapDiagnostics(res.Gaps)...)
	res.Diagnostics = diag.Sort(dedupeDiagnostics(diagnostics))

	if res.Coverage != assurance.CoverageFailed {
		res.Coverage = coverageOfGaps(res.Gaps)
	}
}

// gapDiagnostics gives a CO-PRV-005 diagnostic for each gap that makes evidence unreadable.
func gapDiagnostics(gaps []assurance.Gap) []diag.Diagnostic {
	var out []diag.Diagnostic

	for _, g := range gaps {
		switch g.Kind {
		case GapUnparseableImage, GapUnsupportedSelector, GapUnparseableDocument, GapInvalidResource:
			out = append(out, newDiagnostic(diag.PRV005, g.Kind+": "+g.Detail, g.Path))
		}
	}

	return out
}

func coverageOfGaps(gaps []assurance.Gap) assurance.Coverage {
	if len(gaps) > 0 {
		return assurance.CoveragePartial
	}

	return assurance.CoverageComplete
}

// fail marks the source as failed: no resource set is kept, so a partial set is never used as complete evidence.
// A kind adds a gap and a detail adds a CO-PRV-005 warning; both may be empty when the findings that caused the
// failure are recorded already.
func (b *builder) fail(res *Result, kind, detail string) {
	res.Coverage = assurance.CoverageFailed
	res.OutputDigest = ""
	res.Resources, res.Occurrences, res.Selections = nil, nil, nil

	if kind != "" {
		b.gaps = append(b.gaps, assurance.Gap{Kind: kind, Detail: detail})
	}

	if detail != "" {
		b.diagnostics = append(b.diagnostics, newDiagnostic(diag.PRV005, fmt.Sprintf("environment %s (%s): %s", res.Environment, res.Path, detail), res.Path))
	}

	b.seal(res)
}

func compareRecords(left, right redact.Record) int {
	if c := canonjson.CompareUTF16(left.File, right.File); c != 0 {
		return c
	}

	if left.Line != right.Line {
		return left.Line - right.Line
	}

	if c := canonjson.CompareUTF16(left.Property, right.Property); c != 0 {
		return c
	}

	return canonjson.CompareUTF16(left.Class, right.Class)
}

func sortGaps(gaps []assurance.Gap) {
	slices.SortStableFunc(gaps, func(a, b assurance.Gap) int {
		if c := canonjson.CompareUTF16(a.Kind, b.Kind); c != 0 {
			return c
		}

		if c := canonjson.CompareUTF16(a.Path, b.Path); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Detail, b.Detail)
	})
}

func dedupe(list []string) []string {
	var out []string

	for _, s := range list {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}

	return out
}

func dedupeDiagnostics(list []diag.Diagnostic) []diag.Diagnostic {
	seen := make(map[string]bool, len(list))

	var out []diag.Diagnostic

	for _, d := range list {
		key := d.Code + "\x00" + d.Message
		if d.Source != nil {
			key += "\x00" + d.Source.URI
		}

		if !seen[key] {
			seen[key] = true

			out = append(out, d)
		}
	}

	return out
}

// helmLocation maps the `# Source:` value of a Helm document (`<chart name>/templates/x.yaml`) to the repository
// path of the chart file. The chart name, the first element, is not a directory of the repository.
func helmLocation(chartPath string) func(string) string {
	return func(source string) string {
		_, rest, ok := strings.Cut(source, "/")
		if !ok || isOutsideChart(rest) {
			return chartPath
		}

		return path.Join(chartPath, rest)
	}
}

// isOutsideChart reports whether the part of a `# Source:` value after the chart name cannot name a chart file.
func isOutsideChart(rest string) bool {
	return rest == "" || strings.HasPrefix(rest, "..")
}

// newResult makes the header of a result.
func newResult(env, cluster, tool, srcPath string) *Result {
	return &Result{
		Environment: env, Cluster: cluster, Tool: tool, Path: srcPath,
		Level: deployment.LevelRenderedOrEvaluated, Confinement: ConfinementNone,
	}
}

// recordFindings turns the findings of the confinement and the pre-scan into gaps and diagnostics. It gives true
// when one of them is blocking: the source is then failed and must not be rendered.
func (b *builder) recordFindings(res *Result, findings []finding) bool {
	blocked := false

	for _, f := range findings {
		b.gaps = append(b.gaps, f.Gap)
		b.warn(findingCode(f.Kind), f.Kind+": "+f.Detail, f.Path)

		blocked = blocked || f.Blocking
	}

	if blocked {
		b.fail(res, "", fmt.Sprintf("environment %s (%s) was not rendered", res.Environment, res.Path))
	}

	return blocked
}

// sourceFailed is recordFindings for findings that are all blocking.
func (b *builder) sourceFailed(res *Result, findings []finding) { b.recordFindings(res, findings) }

// findingCode gives the diagnostic of a finding: embedded commands, remote includes, and ignored requests are
// CO-PRV-006; a source that cannot be read is a failed render.
func findingCode(kind string) string {
	switch kind {
	case GapRemoteBase, GapRemoteChartRepo, GapUnvendoredDependency, GapExecGenerator, GapKRMFunction, GapHelmInflation, GapPostRenderer:
		return diag.PRV006
	default:
		return diag.PRV005
	}
}
