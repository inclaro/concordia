package reporting

import (
	"fmt"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/redact"
)

func coverageLine(c *conformance.Coverage) string {
	return fmt.Sprintf("Coverage: %s (required %s)", Percent(c.Files.Ratio), Percent(c.Required))
}

func countDisposition(own []*conformance.Finding, d exceptions.Disposition) int {
	n := 0

	for _, f := range own {
		if f.Disposition == d {
			n++
		}
	}

	return n
}

// ruleLines are the lines under the header of a failing rule: why the rule exists, who owns it, the findings grouped
// by their two ends, and how to fix them. The rationale and the remediation come from the policy, so a reader sees
// the consequence of a finding next to the code that causes it.
func ruleLines(run *conformance.Run, rule *conformance.RuleResult, verbose bool) []string {
	if rule.Verdict != conformance.VerdictViolation {
		return nonViolationLines(rule)
	}

	own, shown := ruleFindings(run, rule, verbose)
	lines := ruleIntroLines(rule, shown, verbose)

	for _, group := range groupFindings(rule, shown) {
		lines = append(lines, groupLines(run, rule, &group, verbose)...)
	}

	if rule.Plan.Remediation != "" {
		lines = append(lines, "  Fix: "+rule.Plan.Remediation)
	}

	if !verbose {
		lines = append(lines, hiddenLines(own)...)
	}

	return lines
}

// ruleIntroLines are the lines before the findings of a failing rule: why it matters, who owns it, and its coverage
// when that is short of the requirement (or always with --verbose).
func ruleIntroLines(rule *conformance.RuleResult, shown []*conformance.Finding, verbose bool) []string {
	lines := rationaleLines(rule)

	if owner := ruleOwner(rule, shown); owner != "" {
		lines = append(lines, "  Owner: "+owner)
	}

	if coverageShown(rule, verbose) {
		lines = append(lines, "  "+coverageLine(rule.Coverage))
	}

	return lines
}

// rationaleLines is the line that says why a rule exists, or none when the policy gives no rationale.
func rationaleLines(rule *conformance.RuleResult) []string {
	if rule.Plan.Rationale == "" {
		return nil
	}

	return []string{"  Why it matters: " + rule.Plan.Rationale}
}

// coverageShown says whether the coverage line of a failing rule is shown: always with --verbose, else when the
// coverage is short of the requirement.
func coverageShown(rule *conformance.RuleResult, verbose bool) bool {
	if rule.Coverage == nil {
		return false
	}

	return verbose || rule.Coverage.Files.Ratio < rule.Coverage.Required
}

// ruleFindings are the visible findings of a rule, and those of them that the output shows: the live ones, or all
// with --verbose.
func ruleFindings(run *conformance.Run, rule *conformance.RuleResult, verbose bool) (own, shown []*conformance.Finding) {
	for i := range run.Findings {
		if run.Findings[i].RuleID == rule.RuleID && conformance.Visible(&run.Findings[i]) {
			own = append(own, &run.Findings[i])
		}
	}

	return own, shownFindings(own, verbose)
}

// shownFindings are the findings that the output shows: the live ones, or all with --verbose.
func shownFindings(own []*conformance.Finding, verbose bool) []*conformance.Finding {
	var shown []*conformance.Finding

	for _, f := range own {
		if conformance.IsLive(f) || verbose {
			shown = append(shown, f)
		}
	}

	return shown
}

// groupLines are the heading of one group, its findings aligned on the longest location, and its next action when
// the rule has no remediation that says it.
func groupLines(run *conformance.Run, rule *conformance.RuleResult, group *findingGroup, verbose bool) []string {
	width := 0
	for _, f := range group.findings {
		width = max(width, len([]rune(locationText(f))))
	}

	head := fmt.Sprintf("  %d finding(s)", len(group.findings))
	if group.ends != "" {
		head = fmt.Sprintf("  %s: %d finding(s)", group.ends, len(group.findings))
	}

	lines := []string{head}

	for _, f := range group.findings {
		lines = append(lines, findingLines(run, f, width, verbose)...)
	}

	if rule.Plan.Remediation == "" && group.next != "" {
		lines = append(lines, "    Next action: "+group.next)
	}

	return lines
}

// hiddenLines say how many accepted findings the output does not show.
func hiddenLines(own []*conformance.Finding) []string {
	var lines []string

	if n := countDisposition(own, exceptions.DispositionBaselined); n > 0 {
		lines = append(lines, fmt.Sprintf("  %d baselined finding(s) not shown (use --verbose)", n))
	}

	if n := countDisposition(own, exceptions.DispositionWaived); n > 0 {
		lines = append(lines, fmt.Sprintf("  %d waived finding(s) not shown (use --verbose)", n))
	}

	return lines
}

// ruleOwner is the owner of a rule: the owner that the policy names, else the one owner that all shown findings share.
func ruleOwner(rule *conformance.RuleResult, findings []*conformance.Finding) string {
	if rule.Plan.Owner != "" {
		return rule.Plan.Owner
	}

	owner := ""

	for _, f := range findings {
		if f.Owner == nil {
			return ""
		}

		if owner != "" && owner != *f.Owner {
			return ""
		}

		owner = *f.Owner
	}

	return owner
}

// nonViolationLines are the lines of a rule that has no findings to show (`unknown` or `error`): why the rule exists,
// what Concordia cannot judge and why, and the coverage that it had.
func nonViolationLines(rule *conformance.RuleResult) []string {
	lines := rationaleLines(rule)

	label := "Cannot judge"
	if rule.Verdict == conformance.VerdictError {
		label = "Error"
	}

	for _, r := range rule.Reasons {
		lines = append(lines, fmt.Sprintf("  %s: %s (%s)", label, r.Message, r.Code))
	}

	if rule.Coverage != nil {
		lines = append(lines, "  "+coverageLine(rule.Coverage))
	}

	if rule.Verdict == conformance.VerdictUnknown && rule.Plan.EvidenceRequirements.OnUnknown == policy.UnknownWarn {
		lines = append(lines, "  On unknown: warn")
	}

	return lines
}

func summaryLine(run *conformance.Run) string {
	counts := activeCounts(run)

	parts := []string{fmt.Sprintf("%d active error(s)", counts[policy.SeverityError])}

	for _, s := range []policy.RuleSeverity{policy.SeverityWarning, policy.SeverityInfo} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d active %s(s)", counts[s], s))
		}
	}

	parts = append(parts, fmt.Sprintf("%d baselined", run.Summary.Baselined), fmt.Sprintf("%d waived", run.Summary.Waived),
		fmt.Sprintf("%d expired waiver(s)", run.Summary.ExpiredWaiver), fmt.Sprintf("%d unknown", shownUnknownCount(run)))

	return strings.Join(parts, ", ")
}

// activeCounts counts the active visible findings of a run by severity.
func activeCounts(run *conformance.Run) map[policy.RuleSeverity]int {
	counts := map[policy.RuleSeverity]int{}

	for i := range run.Findings {
		if run.Findings[i].Disposition == exceptions.DispositionActive && conformance.Visible(&run.Findings[i]) {
			counts[run.Findings[i].Severity]++
		}
	}

	return counts
}

// shownUnknownCount counts the rules of a run whose `unknown` verdict the output shows.
func shownUnknownCount(run *conformance.Run) int {
	unknown := 0

	for i := range run.Rules {
		if isShownUnknown(&run.Rules[i]) {
			unknown++
		}
	}

	return unknown
}

// sanitized replaces the control characters in each line. Names, messages, and paths come from
// repository content and provider output: no control sequence of them reaches the terminal.
// The color codes are ours and are added to the lines afterwards.
func sanitized(lines []string) []string {
	for i, line := range lines {
		lines[i] = redact.SanitizeLine(line)
	}

	return lines
}

// ruleBlock renders one rule that the terminal shows: a failing, unknown, or erroring rule. nil for the others.
func ruleBlock(run *conformance.Run, rule *conformance.RuleResult, opts PrettyOptions, paint paintFunc) []string {
	if !ruleShown(rule) {
		return nil
	}

	word, color := verdictWord(rule.Verdict)
	header := paint(ansiBold+";"+color, word) + " " + redact.SanitizeLine(rule.RuleID+" "+rule.Name) + headerNotes(rule)

	return append([]string{header}, sanitized(ruleLines(run, rule, opts.Verbose))...)
}

// ruleShown says whether the terminal shows a rule: a visible rule that fails, is unknown with a reason to show it, or
// errors.
func ruleShown(rule *conformance.RuleResult) bool {
	if !conformance.RuleVisible(rule) {
		return false
	}

	switch rule.Verdict {
	case conformance.VerdictPass, conformance.VerdictNotApplicable:
		return false
	case conformance.VerdictUnknown:
		return isShownUnknown(rule)
	case conformance.VerdictViolation, conformance.VerdictError:
	}

	return true
}

// verdictWord is the status word of a rule block and its ANSI color.
func verdictWord(verdict conformance.RuleVerdict) (word, color string) {
	switch verdict {
	case conformance.VerdictViolation:
		return "FAIL", ansiRed
	case conformance.VerdictUnknown:
		return "UNKNOWN", ansiYellow
	case conformance.VerdictPass, conformance.VerdictNotApplicable, conformance.VerdictError:
	}

	return "ERROR", ansiRed
}

// headerNotes are the notes that follow the name of a rule in its header: an advisory rule does not change the exit
// code, and a full-scope rule ran on the whole workspace.
func headerNotes(rule *conformance.RuleResult) string {
	notes := ""

	if rule.Enforcement == policy.EnforcementAdvisory {
		notes += " (advisory: does not change the exit code)"
	}

	if rule.ChangedScope == "full" {
		notes += " (full scope)"
	}

	return notes
}
