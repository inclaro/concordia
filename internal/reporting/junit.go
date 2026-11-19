package reporting

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
)

const endCase = "    </testcase>"

type caseCounts struct {
	tests, failures, errors, skipped int
}

type renderedCase struct {
	xml []string
	caseCounts
}

func (c *caseCounts) add(o caseCounts) {
	c.tests += o.tests
	c.failures += o.failures
	c.errors += o.errors
	c.skipped += o.skipped
}

func findingLocation(f *conformance.Finding) string {
	if f.Location == nil {
		return ""
	}

	if f.Location.Line > 0 {
		return fmt.Sprintf(" %s:%d", f.Location.Path, f.Location.Line)
	}

	return " " + f.Location.Path
}

func reasonLines(rule *conformance.RuleResult) []string {
	out := make([]string, 0, len(rule.Reasons))
	for _, r := range rule.Reasons {
		out = append(out, r.Code+": "+r.Message)
	}

	return out
}

func firstOr(list []string, fallback string) string {
	if len(list) == 0 {
		return fallback
	}

	return list[0]
}

// caseOpening is the opening of a testcase element, without its closing `>` or `/>`.
type caseOpening string

func newCaseOpening(classname string, rule *conformance.RuleResult) caseOpening {
	return caseOpening(`    <testcase classname="` + xmlEscape(classname) + `" name="` + xmlEscape(rule.RuleID+" "+rule.Name) + `"`)
}

// suiteOpenTag gives the opening line of a testsuite element with its counts.
func suiteOpenTag(name string, c caseCounts) string {
	return `  <testsuite name="` + xmlEscape(name) + `"` +
		fmt.Sprintf(` tests="%d" failures="%d" errors="%d" skipped="%d">`, c.tests, c.failures, c.errors, c.skipped)
}

func passedCase(open caseOpening) renderedCase {
	return renderedCase{caseCounts: caseCounts{tests: 1}, xml: []string{string(open) + "/>"}}
}

func skippedCase(open caseOpening, reasons []string) renderedCase {
	return renderedCase{caseCounts: caseCounts{tests: 1, skipped: 1}, xml: []string{
		string(open) + ">", `      <skipped message="` + xmlEscape(strings.Join(reasons, "; ")) + `"/>`, endCase,
	}}
}

func erroredCase(open caseOpening, reasons []string, fallback string) renderedCase {
	return renderedCase{caseCounts: caseCounts{tests: 1, errors: 1}, xml: []string{
		string(open) + ">",
		`      <error type="error" message="` + xmlEscape(firstOr(reasons, fallback)) + `">` + xmlEscape(strings.Join(reasons, "\n")) + `</error>`,
		endCase,
	}}
}

// failedCase renders a failure of the given type; the message falls back to the type itself.
func failedCase(open caseOpening, kind string, reasons []string) renderedCase {
	return renderedCase{caseCounts: caseCounts{tests: 1, failures: 1}, xml: []string{
		string(open) + ">",
		`      <failure type="` + kind + `" message="` + xmlEscape(firstOr(reasons, kind)) + `">` + xmlEscape(strings.Join(reasons, "\n")) + `</failure>`,
		endCase,
	}}
}

// noteCase renders a counted case that carries only an output note.
func noteCase(open caseOpening, note string) renderedCase {
	return renderedCase{caseCounts: caseCounts{tests: 1}, xml: []string{
		string(open) + ">", "      <system-out>" + xmlEscape(note) + "</system-out>", endCase,
	}}
}

// keptRuleFindings gives the visible findings of a rule, accepted by keep.
func keptRuleFindings(run *conformance.Run, rule *conformance.RuleResult, keep func(*conformance.Finding) bool) []*conformance.Finding {
	var own []*conformance.Finding

	for i := range run.Findings {
		f := &run.Findings[i]
		if f.RuleID != rule.RuleID || !conformance.Visible(f) {
			continue
		}

		if keep(f) {
			own = append(own, f)
		}
	}

	return own
}

func anyFinding(*conformance.Finding) bool { return true }

func renderCase(run *conformance.Run, rule *conformance.RuleResult) renderedCase {
	open := newCaseOpening(rule.Policy.ID, rule)
	reasons := reasonLines(rule)

	switch rule.Verdict {
	case conformance.VerdictNotApplicable:
		return skippedCase(open, reasons)
	case conformance.VerdictError:
		return erroredCase(open, reasons, "rule evaluation error")
	case conformance.VerdictUnknown:
		return renderUnknown(open, rule, reasons)
	case conformance.VerdictViolation:
		return renderViolation(run, open, keptRuleFindings(run, rule, anyFinding))
	case conformance.VerdictPass:
		return passedCase(open)
	}

	return renderedCase{}
}

func renderUnknown(open caseOpening, rule *conformance.RuleResult, reasons []string) renderedCase {
	onUnknown := rule.Plan.EvidenceRequirements.OnUnknown

	if onUnknown == policy.UnknownFail && conformance.RuleGates(rule, conformance.VerdictUnknown) {
		return failedCase(open, "unknown", reasons)
	}

	return noteCase(open, fmt.Sprintf("unknown (%s): %s", onUnknown, strings.Join(reasons, "\n")))
}

func renderViolation(run *conformance.Run, open caseOpening, own []*conformance.Finding) renderedCase {
	var failing []string

	for _, f := range own {
		if conformance.IsFailing(f, run.Summary.FailOn) {
			failing = append(failing, f.Fingerprint+findingLocation(f)+": "+f.Message)
		}
	}

	if len(failing) > 0 {
		return renderedCase{caseCounts: caseCounts{tests: 1, failures: 1}, xml: []string{
			string(open) + ">",
			fmt.Sprintf(`      <failure type="violation" message="%d active finding(s)">%s</failure>`, len(failing), xmlEscape(strings.Join(failing, "\n"))),
			endCase,
		}}
	}

	accepted := make([]string, 0, len(own))
	for _, f := range own {
		accepted = append(accepted, fmt.Sprintf("%s: %s%s", f.Disposition, f.Fingerprint, findingLocation(f)))
	}

	return noteCase(open, strings.Join(accepted, "\n"))
}

// suiteIncluded tells whether a rule has a testcase in the suite of its policy pack.
func suiteIncluded(run *conformance.Run, rule *conformance.RuleResult) bool {
	if !conformance.RuleVisible(rule) {
		return false
	}

	// With views, each view check has a suite of its own: the rules of the views pack would count them twice.
	if len(run.Views) > 0 && rule.Family == policy.FamilyView {
		return false
	}

	// With required assessments, each has a testcase of its own: REV-APR-001 would count them twice.
	return rule.RuleID != reviewApprovalRule || !hasRequiredAssessments(run)
}

// packSuites groups the rules that have a testcase by `policy@version`.
func packSuites(run *conformance.Run) map[string][]*conformance.RuleResult {
	suites := map[string][]*conformance.RuleResult{}

	for i := range run.Rules {
		rule := &run.Rules[i]
		if !suiteIncluded(run, rule) {
			continue
		}

		key := rule.Policy.ID + "@" + rule.Policy.Version
		suites[key] = append(suites[key], rule)
	}

	return suites
}

// renderPackSuites renders one `<testsuite>` for each policy pack in key order and adds the counts to total.
func renderPackSuites(run *conformance.Run, total *caseCounts) []string {
	suites := packSuites(run)

	keys := make([]string, 0, len(suites))
	for k := range suites {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	var body []string

	for _, key := range keys {
		var sum caseCounts

		cases := make([]renderedCase, 0, len(suites[key]))

		for _, r := range suites[key] {
			c := renderCase(run, r)
			cases = append(cases, c)
			sum.add(c.caseCounts)
		}

		total.add(sum)
		body = append(body, suiteOpenTag(key, sum))

		for _, c := range cases {
			body = append(body, c.xml...)
		}

		body = append(body, "  </testsuite>")
	}

	return body
}

// RenderJUnit renders JUnit XML: one `<testsuite>` for each policy pack, one `<testcase>` for each rule, and one
// `<testsuite>` for each check of each view.
func RenderJUnit(run *conformance.Run) string {
	var total caseCounts

	body := renderPackSuites(run, &total)
	body = append(body, renderViewSuites(run, &total)...)
	body = append(body, renderRequiredSuite(run, &total)...)

	lines := make([]string, 0, len(body)+4)
	lines = append(lines,
		`<?xml version="1.0" encoding="UTF-8"?>`,
		fmt.Sprintf(`<testsuites name="concordia" tests="%d" failures="%d" errors="%d" skipped="%d">`, total.tests, total.failures, total.errors, total.skipped))
	lines = append(lines, body...)
	lines = append(lines, "</testsuites>", "")

	return strings.Join(lines, "\n")
}

// viewRule gives the result of the built-in rule of a view check, or nil when the run does not hold it.
func viewRule(run *conformance.Run, ruleID string) *conformance.RuleResult {
	for i := range run.Rules {
		if run.Rules[i].RuleID == ruleID {
			return &run.Rules[i]
		}
	}

	return nil
}

// viewSuiteName names the suite of one view check so that a CI interface groups the three checks of a view.
func viewSuiteName(view *conformance.ViewResult, check *viewCheck) string {
	return "views/" + view.ID + "/" + check.name
}

// renderViewCase renders the one testcase of a view check. The verdicts map as the ones of a rule do: `pass` is a
// passing case, `violation` a failure, `unknown` follows the `onUnknown` of the rule of the check, `not-applicable`
// is skipped, `error` is an error.
func renderViewCase(run *conformance.Run, rule *conformance.RuleResult, view *conformance.ViewResult, check *viewCheck) renderedCase {
	open := newCaseOpening("views/"+view.ID, rule)
	reasons := check.reasons(view)

	switch check.verdict(view) {
	case conformance.VerdictNotApplicable:
		return skippedCase(open, reasons)
	case conformance.VerdictError:
		return erroredCase(open, reasons, "view check error")
	case conformance.VerdictUnknown:
		return renderUnknown(open, rule, reasons)
	case conformance.VerdictViolation:
		return renderViewViolation(run, open, viewFindings(run, rule, view), reasons)
	case conformance.VerdictPass:
	}

	return passedCase(open)
}

// viewFindings gives the visible findings of a rule that are raised for one view.
func viewFindings(run *conformance.Run, rule *conformance.RuleResult, view *conformance.ViewResult) []*conformance.Finding {
	return keptRuleFindings(run, rule, func(f *conformance.Finding) bool { return viewIDOf(f) == view.ID })
}

// renderViewViolation renders a violated view check: a failure of its own reasons when no finding is raised for the
// view, the violation of its findings otherwise.
func renderViewViolation(run *conformance.Run, open caseOpening, own []*conformance.Finding, reasons []string) renderedCase {
	if len(own) == 0 {
		return failedCase(open, "violation", reasons)
	}

	return renderViolation(run, open, own)
}

// viewCheckRule gives the rule of a view check; a run that does not hold its result gets the conservative
// `onUnknown: fail`.
func viewCheckRule(run *conformance.Run, check *viewCheck) *conformance.RuleResult {
	if rule := viewRule(run, check.ruleID); rule != nil {
		return rule
	}

	rule := &conformance.RuleResult{RuleID: check.ruleID, Name: check.title}
	rule.Plan.EvidenceRequirements.OnUnknown = policy.UnknownFail

	return rule
}

// renderViewSuites renders one `<testsuite>` for each check of each view, in view and check order, and adds the
// counts of the suites to total. A check whose rule is a shadow rule stays out, as a shadow rule does.
func renderViewSuites(run *conformance.Run, total *caseCounts) []string {
	var body []string

	for i := range run.Views {
		view := &run.Views[i]

		for j := range viewChecks {
			check := &viewChecks[j]

			rule := viewCheckRule(run, check)
			if !conformance.RuleVisible(rule) {
				continue
			}

			c := renderViewCase(run, rule, view, check)
			total.add(c.caseCounts)

			body = append(body, suiteOpenTag(viewSuiteName(view, check), c.caseCounts))
			body = append(body, c.xml...)
			body = append(body, "  </testsuite>")
		}
	}

	return body
}
