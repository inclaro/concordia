package reporting

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/redact"
)

// PrettyOptions are the options of the pretty reporter.
type PrettyOptions struct {
	// Color enables ANSI color. Status words are always written, color is never the only signal.
	Color bool
	// Verbose also shows baselined and waived findings in full, and the full fingerprint.
	Verbose bool
}

const (
	ansiRed    = "31"
	ansiYellow = "33"
	ansiGreen  = "32"
	ansiBold   = "1"

	// shortRevision is the number of characters of a revision in the changed-scope line.
	shortRevision = 12
)

// paintFunc wraps a text in the ANSI code of a color, or leaves it as it is when the output has no color.
type paintFunc func(code, text string) string

// painter gives the paint function of the output: it adds the ANSI codes when color is on.
func painter(color bool) paintFunc {
	return func(code, text string) string {
		if !color {
			return text
		}

		return "\x1b[" + code + "m" + text + "\x1b[0m"
	}
}

// verdictColor gives the ANSI color of an area verdict.
func verdictColor(verdict conformance.AreaVerdict) string {
	switch conformance.RuleVerdict(verdict) {
	case conformance.VerdictViolation, conformance.VerdictError:
		return ansiRed
	case conformance.VerdictUnknown:
		return ansiYellow
	case conformance.VerdictPass, conformance.VerdictNotApplicable:
		return ansiGreen
	}

	return ansiBold
}

// countOrder is the order of the counts that every area knows; other counts follow, sorted by name.
var countOrder = []string{"violation", "unknown", "error", "pass", "notApplicable", "findings"}

// countsText lists the counts of an area that are not zero: the known names first, then the others by name.
func countsText(counts map[string]int) string {
	names := make([]string, 0, len(counts))

	for name, n := range counts {
		if n > 0 {
			names = append(names, name)
		}
	}

	rank := func(name string) int {
		if i := slices.Index(countOrder, name); i >= 0 {
			return i
		}

		return len(countOrder)
	}

	slices.SortFunc(names, func(a, b string) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), strings.Compare(a, b)) })

	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = fmt.Sprintf("%d %s", counts[name], name)
	}

	return strings.Join(parts, ", ")
}

// areaTitle is the heading text of an area section. The diagram area has its full name here; Area.Title stays the
// short name for the lists of areas.
func areaTitle(area conformance.Area) string {
	if area == conformance.AreaDiagramCurrency {
		return diagramCurrencyTitle
	}

	return area.Title()
}

// areaHeading is the first line of the section of an area: its title, its verdict, and its counts.
func areaHeading(area conformance.Area, res *conformance.AreaResult, paint paintFunc) string {
	if res.Verdict == "" {
		return paint(ansiBold, areaTitle(area))
	}

	line := paint(ansiBold, areaTitle(area)+":") + " " + paint(ansiBold+";"+verdictColor(res.Verdict), string(res.Verdict))
	if text := countsText(res.Counts); text != "" {
		line += " (" + text + ")"
	}

	return line
}

// traceDetailLines is the detail of the deployment trace area: the level line, the counts by status (every
// subject counts, `unknown` and `unresolved` ones too), and the subjects that are not complete, capped with
// "… and N more". The results file always has the full list.
func traceDetailLines(trace *deployment.Trace) []string {
	if !trace.Configured() {
		return nil
	}

	open := sanitized(openSubjectLines(trace))
	lines := make([]string, 0, 2+len(open))
	lines = append(lines, "  "+trace.LevelsSentence(), traceSubjectsLine(trace))

	return append(lines, open...)
}

// traceSubjectsLine counts the subjects of the deployment trace, by status.
func traceSubjectsLine(trace *deployment.Trace) string {
	counts := make([]string, 0, len(deployment.Statuses()))
	for _, status := range deployment.Statuses() {
		counts = append(counts, fmt.Sprintf("%d %s", trace.Summary.ByStatus[status], status))
	}

	return fmt.Sprintf("  Subjects: %d in %d environment(s): %s", trace.Summary.Subjects, trace.Summary.Environments, strings.Join(counts, ", "))
}

// openSubjectLines name the subjects of the deployment trace that are not complete, up to the list limit.
func openSubjectLines(trace *deployment.Trace) []string {
	var open []string

	for _, status := range deployment.Statuses() {
		if status == deployment.StatusComplete {
			continue
		}

		for i := range trace.Environments {
			open = append(open, environmentSubjectLines(&trace.Environments[i], status)...)
		}
	}

	if len(open) > deployment.DefaultListLimit {
		open = append(open[:deployment.DefaultListLimit], fmt.Sprintf("  … and %d more", len(open)-deployment.DefaultListLimit))
	}

	return open
}

// environmentSubjectLines name the subjects of one environment that have a status.
func environmentSubjectLines(env *deployment.EnvironmentTrace, status deployment.Status) []string {
	var lines []string

	for j := range env.Subjects {
		if env.Subjects[j].Status == status {
			lines = append(lines, fmt.Sprintf("  %s: %s in %s", status, env.Subjects[j].ArchitectureID, env.Name))
		}
	}

	return lines
}

// areaHead is the heading of an area section and, for the deployment trace and the views, its detail lines.
func areaHead(run *conformance.Run, area conformance.Area, res *conformance.AreaResult, paint paintFunc) []string {
	head := []string{areaHeading(area, res, paint)}

	switch area {
	case conformance.AreaDeploymentTrace:
		head = append(head, traceDetailLines(&run.DeploymentTrace)...)
	case conformance.AreaDiagramCurrency:
		head = append(head, viewRows(run)...)
	case conformance.AreaADRGovernance:
		head = append(head, decisionRows(run)...)
	case conformance.AreaAssessmentCurrency:
		head = append(head, assessmentRows(run)...)
	case conformance.AreaQualityEvaluation:
		head = append(head, qualityRows(run)...)
	case conformance.AreaConformance:
	}

	return head
}

// notConfiguredLines names the areas that the workspace does not configure. When the deployment trace is one of them,
// a second line says why: no environment is declared.
func notConfiguredLines(run *conformance.Run, titles []string) []string {
	lines := []string{"Not configured: " + strings.Join(titles, ", ")}

	if run.Areas.DeploymentTrace.Verdict == conformance.AreaNotConfigured && run.DeploymentTrace.Status == deployment.StatusNotConfigured {
		lines = append(lines, "No environment declared: the deployment trace and environment-specific assurance need a declared environment.")
	}

	return lines
}

// RenderPretty renders the terminal result. It is a pure function of the run.
//
// Each area that the run evaluated is a section of its own: a heading with the verdict and counts of the area,
// then its rules that do not pass. An area that the workspace does not configure is named in one line. The
// areas that are `unknown` are named again before the summary line, so that a passing `conformance` section
// never hides them (OUT-03).
func RenderPretty(run *conformance.Run, opts PrettyOptions) string {
	paint := painter(opts.Color)
	blocks := groupRuleBlocks(run, opts, paint)

	var (
		lines         []string
		notConfigured []string
		unknownAreas  []string
	)

	for _, area := range conformance.AreaOrder() {
		res := run.Areas.Get(area)
		if res.Verdict == conformance.AreaNotConfigured {
			notConfigured = append(notConfigured, area.Title())

			continue
		}

		if !areaShown(run, area, res, blocks[area]) {
			continue
		}

		if isUnknownArea(area, res) {
			unknownAreas = append(unknownAreas, area.Title())
		}

		lines = separated(lines, areaLines(run, area, res, blocks[area], paint))
	}

	if len(notConfigured) > 0 {
		lines = separated(lines, notConfiguredLines(run, notConfigured))
	}

	lines = separated(lines, footerLines(run, unknownAreas, paint))

	return strings.Join(lines, "\n") + "\n"
}

// separated adds a section to the lines, after an empty line when there are lines already.
func separated(lines, section []string) []string {
	if len(lines) > 0 {
		lines = append(lines, "")
	}

	return append(lines, section...)
}

// areaShown says whether the output has a section for an area: it is configured, or it has rule blocks to show, or it
// is the decision area of a run that has decisions.
func areaShown(run *conformance.Run, area conformance.Area, res *conformance.AreaResult, blocks [][]string) bool {
	if res.Verdict.Configured() || len(blocks) > 0 {
		return true
	}

	return area == conformance.AreaADRGovernance && run.DecisionSummary != nil
}

// isUnknownArea says whether an area is named again before the summary line: its verdict is `unknown`. A passing
// `conformance` section is never hidden by it, so that area shows its own verdict.
func isUnknownArea(area conformance.Area, res *conformance.AreaResult) bool {
	return res.Verdict == conformance.AreaVerdict(conformance.VerdictUnknown) && area != conformance.AreaConformance
}

// areaLines are the section of an area: its heading and detail lines, then each of its rule blocks after an empty line.
func areaLines(run *conformance.Run, area conformance.Area, res *conformance.AreaResult, blocks [][]string, paint paintFunc) []string {
	lines := areaHead(run, area, res, paint)

	for _, block := range blocks {
		lines = append(lines, "")
		lines = append(lines, block...)
	}

	return lines
}

// groupRuleBlocks renders the shown rules and groups the blocks by area.
func groupRuleBlocks(run *conformance.Run, opts PrettyOptions, paint paintFunc) map[conformance.Area][][]string {
	blocks := map[conformance.Area][][]string{}

	for i := range run.Rules {
		rule := &run.Rules[i]
		if block := ruleBlock(run, rule, opts, paint); block != nil {
			area := conformance.AreaOfRule(rule)
			blocks[area] = append(blocks[area], block)
		}
	}

	return blocks
}

// footerLines are the closing lines: changed scope, unknown areas, rollback notice, and the summary line.
func footerLines(run *conformance.Run, unknownAreas []string, paint paintFunc) []string {
	var lines []string

	if ch := run.Run.Changed; ch != nil {
		lines = append(lines, changedScopeLine(ch))
	}

	if len(unknownAreas) > 0 {
		lines = append(lines, paint(ansiYellow, "Unknown area(s): "+strings.Join(unknownAreas, ", ")))
	}

	if areas := violationAreas(run); len(areas) > 0 {
		lines = append(lines, paint(ansiRed, "Violation area(s): "+strings.Join(areas, ", ")))
	}

	if rb := run.Summary.Rollback; rb != nil {
		lines = append(lines, paint(ansiYellow, redact.SanitizeLine("Rollback active: "+rb.Message+". This is not a successful typed, governance, or review check.")))
	}

	return append(lines, paint(summaryColor(run, unknownAreas), summaryLine(run)))
}

// changedScopeLine says how many files changed since which revision.
func changedScopeLine(ch *conformance.RunChanged) string {
	base := ch.Base
	if len(base) > shortRevision {
		base = base[:shortRevision]
	}

	return redact.SanitizeLine(fmt.Sprintf("Changed scope: %d file(s) since %s (%s)", len(ch.Files), base, ch.Ref))
}

// summaryColor is green for a run that is clear on every count, else bold.
func summaryColor(run *conformance.Run, unknownAreas []string) string {
	if run.Summary.ExitCode != 0 || len(unknownAreas) > 0 {
		return ansiBold
	}

	if anyAreaOpen(run) {
		return ansiBold
	}

	return ansiGreen
}

// anyAreaOpen says whether a decision, review, or quality area has something open.
func anyAreaOpen(run *conformance.Run) bool {
	return adrOpen(run) || reviewOpen(run) || qualityOpen(run)
}
