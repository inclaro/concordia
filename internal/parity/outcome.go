package parity

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// decimalBase is the radix of jsParseInt.
const decimalBase = 10

// ruleJSONSchema is the rule ID of the JSON Schema findings.
const ruleJSONSchema = "json-schema"

// Finding is one upstream finding without its message and position.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // "error" or "warning"
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// Outcome is the normalized result of one run.
type Outcome struct {
	Findings    []Finding `json:"findings"` // all findings except json-schema, sorted by Rule, Path, Severity
	LoadError   bool      `json:"loadError"`
	HasErrors   bool      `json:"hasErrors"`
	HasWarnings bool      `json:"hasWarnings"`
	JSONSchema  bool      `json:"jsonSchema"` // at least one json-schema error
}

// Expected is the file expected.json of a case: the upstream outcome of each run.
type Expected struct {
	Runs map[string]Outcome `json:"runs"`
	Tag  string             `json:"tag"`
}

type upstreamOutput struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Path     string `json:"path"`
}

// FromUpstream makes an outcome from the JSON output of `calm validate -f json`
// and its exit code. Empty output with exit code 1 is a load error.
func FromUpstream(data []byte, exitCode int) (Outcome, error) {
	if strings.TrimSpace(string(data)) == "" {
		return emptyUpstreamOutcome(exitCode)
	}

	var raw struct {
		JSONSchema []upstreamOutput `json:"jsonSchemaValidationOutputs"`
		Spectral   []upstreamOutput `json:"spectralSchemaValidationOutputs"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return Outcome{}, fmt.Errorf("parse calm output: %w", err)
	}

	out := Outcome{Findings: []Finding{}}

	for _, entry := range slices.Concat(raw.JSONSchema, raw.Spectral) {
		out.addUpstream(entry)
	}

	sortFindings(out.Findings)

	return out, nil
}

// emptyUpstreamOutcome gives the outcome of an empty output: exit code 1 is a load error.
func emptyUpstreamOutcome(exitCode int) (Outcome, error) {
	if exitCode != 1 {
		return Outcome{}, fmt.Errorf("empty output with exit code %d", exitCode)
	}

	return Outcome{LoadError: true, Findings: []Finding{}}, nil
}

// addUpstream takes an upstream finding. A finding that is no error and no warning does not count.
func (o *Outcome) addUpstream(entry upstreamOutput) {
	severity, ok := upstreamSeverity(entry.Severity)
	if !ok {
		return
	}

	o.noteSeverity(severity)
	o.add(Finding{Rule: entry.Code, Severity: string(severity), Path: entry.Path, Message: entry.Message})
}

// upstreamSeverity maps the severity of an upstream finding. It gives false for a severity that does not count.
func upstreamSeverity(severity string) (diag.Severity, bool) {
	switch severity {
	case "error":
		return diag.SeverityError, true
	case "warning":
		return diag.SeverityWarning, true
	default:
		return "", false
	}
}

// noteSeverity sets the flag of the severity. It gives false for the severity info, which does not count.
func (o *Outcome) noteSeverity(severity diag.Severity) bool {
	switch severity {
	case diag.SeverityError:
		o.HasErrors = true
	case diag.SeverityWarning:
		o.HasWarnings = true
	case diag.SeverityInfo:
		return false
	}

	return true
}

// add takes a finding: a json-schema error only sets JSONSchema, any other finding is listed.
func (o *Outcome) add(f Finding) {
	if f.Rule != ruleJSONSchema {
		o.Findings = append(o.Findings, f)

		return
	}

	if f.Severity == string(diag.SeverityError) {
		o.JSONSchema = true
	}
}

// FromConcordia makes an outcome from the diagnostics of `concordia model validate`.
// Only diagnostics with a rule ID take part, except the load errors. docs maps the
// source URI of a run document to its parsed JSON value; the path of a finding in
// such a document gets the unique-id rewrite of the CALM CLI (see RewritePathWithIDs).
func FromConcordia(diags []diag.Diagnostic, docs map[string]any) Outcome {
	if hasLoadError(diags) {
		return Outcome{LoadError: true, Findings: []Finding{}}
	}

	out := Outcome{Findings: []Finding{}}

	for i := range diags {
		diagnostic := &diags[i]
		if diagnostic.Rule == "" || !out.noteSeverity(diagnostic.Severity) {
			continue
		}

		out.add(Finding{
			Rule: diagnostic.Rule, Severity: string(diagnostic.Severity), Path: concordiaPath(diagnostic, docs), Message: diagnostic.Message,
		})
	}

	sortFindings(out.Findings)

	return out
}

// hasLoadError tells if the diagnostics have a load error.
func hasLoadError(diags []diag.Diagnostic) bool {
	for i := range diags {
		if isLoadError(&diags[i]) {
			return true
		}
	}

	return false
}

// isLoadError tells if the diagnostic stops the load of the document: INT001 or INT003 without a rule ID, or
// INT012 for the top-level `$schema`. A `$schema` pattern that cannot be resolved leaves no document to
// validate; the calm CLI fails to load it in the same way when it cannot fetch the pattern.
func isLoadError(d *diag.Diagnostic) bool {
	if d.Rule != "" {
		return false
	}

	switch d.Code {
	case diag.INT001, diag.INT003:
		return true
	case diag.INT012:
		return d.Source != nil && d.Source.Pointer == "/$schema"
	}

	return false
}

// concordiaPath gives the upstream path of a diagnostic, with the unique-id rewrite when its document is in docs.
func concordiaPath(diagnostic *diag.Diagnostic, docs map[string]any) string {
	pointer, uri := "", ""
	if diagnostic.Source != nil {
		pointer, uri = diagnostic.Source.Pointer, diagnostic.Source.URI
	}

	p := PathOfPointer(pointer)
	if doc, ok := docs[uri]; ok {
		p = RewritePathWithIDs(p, doc)
	}

	return p
}

// PathOfPointer gives the upstream path of a JSON Pointer: the unescaped
// segments joined with "/" after a leading "/". The root is "/".
func PathOfPointer(pointer string) string {
	if pointer == "" {
		return "/"
	}

	segments := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i, s := range segments {
		segments[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}

	return "/" + strings.Join(segments, "/")
}

// RewritePathWithIDs ports rewritePathWithIds of the CALM CLI
// (shared/src/commands/validate/validation-enrichment.ts): each array index in
// the path becomes the unique-id of the array item, when the item has a string
// unique-id. A nil document leaves the path unchanged.
func RewritePathWithIDs(p string, doc any) string {
	if p == "" || doc == nil {
		return p
	}

	tokens := strings.Split(p, "/")[1:]
	rewritten := make([]string, 0, len(tokens))
	cursor := doc

	for _, token := range tokens {
		var id string

		id, cursor = rewriteToken(cursor, token)
		rewritten = append(rewritten, id)
	}

	return "/" + strings.Join(rewritten, "/")
}

// rewriteToken gives the rewritten path token and the node below cursor that the token selects.
func rewriteToken(cursor any, token string) (id string, next any) {
	switch node := cursor.(type) {
	case []any:
		item := arrayItem(node, token)

		return uniqueIDOr(item, token), item
	case map[string]any:
		return token, node[token]
	default:
		return token, nil
	}
}

// arrayItem gives the item of list at the index in token, or nil when the token is not an index in range.
func arrayItem(list []any, token string) any {
	index, ok := jsParseInt(token)
	if !ok {
		return nil
	}

	if index < 0 || index >= len(list) {
		return nil
	}

	return list[index]
}

// uniqueIDOr gives the string unique-id of an object item, or fallback.
func uniqueIDOr(item any, fallback string) string {
	if m, isObj := item.(map[string]any); isObj {
		if s, isStr := m["unique-id"].(string); isStr {
			return s
		}
	}

	return fallback
}

// maxSafeDigits is the number of digits of jsParseInt that stay exact in a JavaScript number.
const maxSafeDigits = 15

// jsWhitespace are the leading characters that jsParseInt skips.
const jsWhitespace = " \t\n\r\v\f"

// jsParseInt is JavaScript Number.parseInt(text, 10) for the integer results.
func jsParseInt(text string) (int, bool) {
	sign, rest := splitSign(strings.TrimLeft(text, jsWhitespace))
	n, digits := leadingNumber(rest)

	return sign * n, digits > 0
}

// splitSign gives the sign of a number text (1 or -1) and the text without it.
func splitSign(text string) (sign int, rest string) {
	if text == "" {
		return 1, text
	}

	switch text[0] {
	case '-':
		return -1, text[1:]
	case '+':
		return 1, text[1:]
	default:
		return 1, text
	}
}

// leadingNumber gives the value of the leading decimal digits of text and their number.
func leadingNumber(text string) (value, digits int) {
	for digits < min(len(text), maxSafeDigits) && isDigit(text[digits]) {
		value = value*decimalBase + int(text[digits]-'0')
		digits++
	}

	return value, digits
}

// isDigit tells if b is an ASCII decimal digit.
func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func sortFindings(list []Finding) {
	slices.SortStableFunc(list, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.Rule, b.Rule), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Severity, b.Severity))
	})
}

type findingKey struct{ rule, severity, path string }

// Compare gives one line for each difference between the upstream outcome
// (want) and the Concordia outcome (got).
func Compare(want, got Outcome) []string {
	var out []string

	flag := func(name string, w, g bool) {
		if w != g {
			out = append(out, fmt.Sprintf("%s: calm=%t concordia=%t", name, w, g))
		}
	}

	flag("loadError", want.LoadError, got.LoadError)
	flag("hasErrors", want.HasErrors, got.HasErrors)
	flag("hasWarnings", want.HasWarnings, got.HasWarnings)
	flag("jsonSchema", want.JSONSchema, got.JSONSchema)

	counts := map[findingKey]int{}
	for _, f := range want.Findings {
		counts[findingKey{f.Rule, f.Severity, f.Path}]++
	}

	for _, f := range got.Findings {
		counts[findingKey{f.Rule, f.Severity, f.Path}]--
	}

	keys := make([]findingKey, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, func(a, b findingKey) int {
		return cmp.Or(cmp.Compare(a.rule, b.rule), cmp.Compare(a.path, b.path), cmp.Compare(a.severity, b.severity))
	})

	for _, k := range keys {
		n := counts[k]
		for ; n > 0; n-- {
			out = append(out, fmt.Sprintf("finding %s %s at %s: missing in Concordia", k.rule, k.severity, k.path))
		}

		for ; n < 0; n++ {
			out = append(out, fmt.Sprintf("finding %s %s at %s: extra in Concordia", k.rule, k.severity, k.path))
		}
	}

	return out
}
