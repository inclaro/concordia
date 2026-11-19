package conformance_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

// qualityRuleShape is the verdict, severity, and unknown disposition of the rule of a quality scenario.
type qualityRuleShape struct {
	verdict   conformance.RuleVerdict
	severity  policy.RuleSeverity
	onUnknown policy.UnknownDisposition
}

// requireUnknownQualityRule stops the test when the rule of a scenario is not unknown with the severity and the
// unknown disposition of its enforcement.
func requireUnknownQualityRule(t *testing.T, rule *conformance.RuleResult, severity policy.RuleSeverity, onUnknown policy.UnknownDisposition) {
	t.Helper()

	got := qualityRuleShape{rule.Verdict, rule.Severity, rule.Plan.EvidenceRequirements.OnUnknown}
	if want := (qualityRuleShape{conformance.VerdictUnknown, severity, onUnknown}); got != want {
		t.Fatalf("rule = %+v, want %+v", got, want)
	}
}

// qualityEntryShape is the part of a quality entry that says what a scenario's verdict is.
type qualityEntryShape struct {
	mode        string
	verdict     conformance.RuleVerdict
	reason      conformance.UnknownReason
	enforcement string
}

// requireQualityEntryShape reports an entry that differs from the shape.
func requireQualityEntryShape(t *testing.T, entry *conformance.QualityEntry, want qualityEntryShape) {
	t.Helper()

	got := qualityEntryShape{entry.Mode, entry.Verdict, entry.Reason, entry.Enforcement}
	if got != want {
		t.Errorf("entry = %+v, want %+v (%+v)", got, want, entry)
	}
}

// qualityLinkShape is the part of an addressedBy link that says what the link reads from the decision summary.
type qualityLinkShape struct {
	adr, canonical, status, state, detail string
	reviewRationale                       bool
}

func shapeOfQualityLink(link *conformance.QualityLink) qualityLinkShape {
	return qualityLinkShape{link.ADR, link.Canonical, link.Status, link.State, link.Detail, link.ReviewRationale}
}

// qualityAreasByName indexes the quality areas of the assurance section by area name.
func qualityAreasByName(run *conformance.Run) map[string]conformance.QualityAreaEntry {
	areas := map[string]conformance.QualityAreaEntry{}
	for i := range run.Assurance.QualityAreas {
		areas[run.Assurance.QualityAreas[i].Area] = run.Assurance.QualityAreas[i]
	}

	return areas
}

// qualityFindingsOf are the findings of the rule of one scenario or area.
func qualityFindingsOf(run *conformance.Run, ruleID string) []*conformance.Finding {
	var out []*conformance.Finding

	for i := range run.Findings {
		if run.Findings[i].RuleID == ruleID {
			out = append(out, &run.Findings[i])
		}
	}

	return out
}

// requireQualityAreaVerdict reports an area result whose verdict differs.
func requireQualityAreaVerdict(t *testing.T, area *conformance.AreaResult, verdict conformance.RuleVerdict) {
	t.Helper()

	if area.Verdict != conformance.AreaVerdict(verdict) {
		t.Errorf("area = %+v, want verdict %s", *area, verdict)
	}
}

// requireQualityConfigError stops the test unless err is the configuration error of a scenario file: exit code 2
// with the code QUA002 and a message that names the identifier.
func requireQualityConfigError(t *testing.T, err error, identifier string) {
	t.Helper()

	var ce *evidence.ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v, want a configuration error", err)
	}

	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("error = %v, want a diagnostic", err)
	}

	if de.Diagnostic.Code != diag.QUA002 || de.ExitCode != diag.InvalidInput {
		t.Errorf("diagnostic = %+v", de)
	}

	if !strings.Contains(de.Error(), identifier) {
		t.Errorf("diagnostic = %+v does not name %s", de, identifier)
	}
}
