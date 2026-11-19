package reporting

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

// reviewOpen says whether the assessment currency area of the run must not be read as a pass: it is `unknown` or
// `violation` (OUT-03). The pretty report names such an area before the summary line, so that a clean `conformance`
// section never shows one overall pass.
func reviewOpen(run *conformance.Run) bool {
	switch conformance.RuleVerdict(run.Areas.AssessmentCurrency.Verdict) {
	case conformance.VerdictUnknown, conformance.VerdictViolation:
		return true
	case conformance.VerdictPass, conformance.VerdictNotApplicable, conformance.VerdictError:
	}

	return false
}

// violationAreas gives the titles of the areas with the verdict `violation` that are not `conformance` (its rules
// show their own failures): the ADR governance and the assessment currency areas.
func violationAreas(run *conformance.Run) []string {
	var out []string

	for _, area := range []conformance.Area{conformance.AreaADRGovernance, conformance.AreaAssessmentCurrency, conformance.AreaQualityEvaluation} {
		if run.Areas.Get(area).Verdict == conformance.AreaVerdict(conformance.VerdictViolation) {
			out = append(out, area.Title())
		}
	}

	return out
}

// assessmentPlace is the file of an assessment version, with its line when known.
func assessmentPlace(e *conformance.AssessmentEntry) string {
	if e.Line > 0 {
		return e.File + ":" + strconv.Itoa(e.Line)
	}

	return e.File
}

// currencyText is the currency of a version with its reasons: `unknown` shows the closed reason, the others the
// short details.
func currencyText(e *conformance.AssessmentEntry) string {
	switch {
	case e.UnknownReason != "":
		return e.Currency + " (" + e.UnknownReason + ")"
	case len(e.Reasons) > 0:
		return e.Currency + " (" + strings.Join(e.Reasons, ", ") + ")"
	}

	return e.Currency
}

// assessmentRow is the row of one assessment version: its currency on the first line, then its outcome, approval,
// and file on a line of its own.
func assessmentRow(entry *conformance.AssessmentEntry) []string {
	approval := "no approval"
	if a := entry.Approval; a != nil {
		approval = "approval " + a.ID + " by " + a.Authority
		if a.Mechanism != "" {
			approval += " (" + a.Mechanism + ")"
		}
	}

	return []string{
		fmt.Sprintf("  %s v%d (%s): %s", entry.ID, entry.Version, entry.Type, currencyText(entry)),
		fmt.Sprintf("    outcome %s; %s; %s", entry.Outcome, approval, assessmentPlace(entry)),
	}
}

// assessmentRows are the rows of the Assessment currency section: one row for each assessment version, each
// trigger below it (the text names the changed dependency, the class, the assessment, and the action `get review`),
// and a note for each decision record that failed verification and each relaxation without a governance disposition.
func assessmentRows(run *conformance.Run) []string {
	report := run.AssessmentCurrency
	if report == nil {
		return nil
	}

	var rows []string

	for i := range report.Assessments {
		e := &report.Assessments[i]
		rows = append(rows, assessmentRow(e)...)

		for j := range e.Triggers {
			rows = append(rows, "    changed: "+e.Triggers[j].ChangeText()+"; next action: "+e.Triggers[j].Action)
		}

		for _, n := range e.NotApplied {
			rows = append(rows, fmt.Sprintf("    not applied: %s (%s): %s", n.Record, n.Kind, n.Reason))
		}
	}

	if run.Governance != nil {
		open := 0

		for _, ch := range run.Governance.Relaxations {
			if ch.Relaxation {
				open++
			}
		}

		if open > 0 {
			rows = append(rows, fmt.Sprintf("  %d relaxation(s) of the review configuration without a governance disposition", open))
		}
	}

	return sanitized(rows)
}

// Keys of the review entries in the SARIF run properties.
const (
	keyVersion = "version"
	keyVerdict = "verdict"
)

// reviewProperty is the assessment currency summary in the SARIF run properties.
func reviewProperty(run *conformance.Run) obj {
	assessments := make([]any, 0, len(run.AssessmentCurrency.Assessments))

	for i := range run.AssessmentCurrency.Assessments {
		e := &run.AssessmentCurrency.Assessments[i]
		assessments = append(assessments, obj{
			keyID: e.ID, keyVersion: e.Version, "file": e.File, "state": e.State, "currency": e.Currency,
		})
	}

	return obj{
		keyVerdict: run.Areas.AssessmentCurrency.Verdict, "evaluatedAt": run.AssessmentCurrency.EvaluatedAt, "assessments": assessments,
	}
}

// reviewApprovalRule is the rule whose verdict is the currency of the required assessments.
const reviewApprovalRule = "REV-APR-001"

// hasRequiredAssessments says whether the run lists required assessments: each of them has a testcase of its own, so the
// rule REV-APR-001 would count them twice.
func hasRequiredAssessments(run *conformance.Run) bool {
	return run.AssessmentCurrency != nil && len(run.AssessmentCurrency.Required) > 0
}

// requiredFinding gives the finding of a required assessment (the rule REV-APR-001 with the entry id as source) that
// the reports show, or nil for an entry that passes.
func requiredFinding(run *conformance.Run, entry *conformance.RequiredEntry) *conformance.Finding {
	for i := range run.Findings {
		f := &run.Findings[i]
		if f.RuleID == reviewApprovalRule && f.Source.ArchitectureID == entry.ID && conformance.Visible(f) {
			return f
		}
	}

	return nil
}

// findingGates says whether a finding that is not a pass blocks: it is live and its mode is `mandatory` (or unset).
func findingGates(f *conformance.Finding) bool {
	return conformance.IsLive(f) && (f.Enforcement == "" || f.Enforcement == policy.EnforcementMandatory)
}

// renderRequiredCase renders the testcase of one required assessment. `current` passes; a violation fails when it
// blocks and is reported otherwise; an `unknown` currency fails when it blocks and the rule says `onUnknown: fail`.
func renderRequiredCase(run *conformance.Run, rule *conformance.RuleResult, entry *conformance.RequiredEntry) renderedCase {
	open := `    <testcase classname="` + xmlEscape("assessments/"+entry.Assessment) + `" name="` + xmlEscape(entry.ID+" "+entry.Assessment) + `"`
	base := caseCounts{tests: 1}

	if entry.Verdict == conformance.VerdictPass {
		return renderedCase{caseCounts: base, xml: []string{open + "/>"}}
	}

	// A finding that this report does not show (for example `shadow` in this environment) is not a pass: skipped.
	finding := requiredFinding(run, entry)
	if finding == nil {
		return renderedCase{caseCounts: caseCounts{tests: 1, skipped: 1}, xml: []string{
			open + ">", `      <skipped message="` + xmlEscape(entry.Currency+" (not shown in this enforcement mode)") + `"/>`, endCase,
		}}
	}

	text := finding.Fingerprint + findingLocation(finding) + ": " + finding.Message

	switch {
	case entry.Verdict == conformance.VerdictViolation && conformance.IsFailing(finding, run.Summary.FailOn):
		base.failures = 1

		return renderedCase{caseCounts: base, xml: []string{
			open + ">", `      <failure type="violation" message="` + xmlEscape(entry.Currency) + `">` + xmlEscape(text) + `</failure>`, endCase,
		}}
	case entry.Verdict == conformance.VerdictUnknown && findingGates(finding) && rule.Plan.EvidenceRequirements.OnUnknown != policy.UnknownWarn &&
		rule.Plan.EvidenceRequirements.OnUnknown != policy.UnknownIgnore:
		base.failures = 1

		return renderedCase{caseCounts: base, xml: []string{
			open + ">", `      <failure type="unknown" message="` + xmlEscape(entry.UnknownReason) + `">` + xmlEscape(text) + `</failure>`, endCase,
		}}
	}

	return renderedCase{caseCounts: base, xml: []string{
		open + ">", "      <system-out>" + xmlEscape(string(finding.Disposition)+": "+text) + "</system-out>", endCase,
	}}
}

// renderRequiredSuite renders one `<testsuite>` with a testcase for each required assessment and adds its counts to
// total. A run whose REV-APR-001 is a shadow rule has none, as a shadow rule has no suite.
func renderRequiredSuite(run *conformance.Run, total *caseCounts) []string {
	if !hasRequiredAssessments(run) {
		return nil
	}

	rule := viewRule(run, reviewApprovalRule)
	if rule == nil || !conformance.RuleVisible(rule) {
		return nil
	}

	var (
		sum   caseCounts
		cases []renderedCase
	)

	for i := range run.AssessmentCurrency.Required {
		c := renderRequiredCase(run, rule, &run.AssessmentCurrency.Required[i])
		cases = append(cases, c)
		sum.add(c.caseCounts)
	}

	total.add(sum)

	body := []string{`  <testsuite name="` + xmlEscape("assessments/"+rule.Policy.ID) + `"` + fmt.Sprintf(` tests="%d" failures="%d" errors="%d" skipped="%d">`,
		sum.tests, sum.failures, sum.errors, sum.skipped)}

	for _, c := range cases {
		body = append(body, c.xml...)
	}

	return append(body, "  </testsuite>")
}
