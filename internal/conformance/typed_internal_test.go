package conformance

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

func shippedProfileRef(t *testing.T) *observation.ProfileRef {
	t.Helper()

	profiles, err := observation.EmbeddedProfiles()
	if err != nil || len(profiles) == 0 {
		t.Fatalf("embedded profiles: %v", err)
	}

	return &observation.ProfileRef{ID: profiles[0].ID, Digest: profiles[0].Digest}
}

func TestCapabilityRegressionsComeFromTheLockProfile(t *testing.T) {
	t.Parallel()

	lock := shippedProfileRef(t)

	// This run has another profile that supports no family: every family that the lock supported regressed.
	caps := &observation.Capabilities{Profile: &observation.ProfileRef{ID: "other", Digest: "sha256:other"}, Families: map[string]observation.FamilyState{}}

	regressions := capabilityRegressions(lock, caps)
	if len(regressions) == 0 {
		t.Fatal("no regression for a run that lost every family")
	}

	for i := range regressions {
		if regressions[i].Was != observation.SupportSupported || regressions[i].Now != observation.SupportUnsupported {
			t.Fatalf("regression = %+v", regressions[i])
		}
	}

	if got := capabilityRegressions(lock, &observation.Capabilities{Profile: lock}); got != nil {
		t.Fatalf("the same profile promises nothing that is lost: %+v", got)
	}

	if got := capabilityRegressions(nil, caps); got != nil {
		t.Fatalf("a lock with no profile promised nothing: %+v", got)
	}

	if got := capabilityRegressions(&observation.ProfileRef{ID: "unknown", Digest: "sha256:x"}, caps); got != nil {
		t.Fatalf("a profile that is not shipped cannot be compared: %+v", got)
	}
}

func precedencesHavePackOrder(got []policy.NamedPrecedence) bool {
	if len(got) != 2 {
		return false
	}

	return got[0].ID == "b" && got[0].Version == 3 && got[1].ID == "c" && got[1].Rank("Z") == 0
}

func typedRuleReportedInFull(out []RuleResult, kept []Finding) bool {
	if len(kept) != 1 {
		return false
	}

	return out[0].ChangedScope == changedScopeFull && out[0].Verdict == VerdictViolation
}

func conflictHeadIsObs021Warning(got []diag.Diagnostic) bool {
	if len(got) != 2 {
		return false
	}

	return got[0].Code == diag.OBS021 && got[0].Severity == diag.SeverityWarning
}

func TestPrecedencesOfKeepsThePackOrderAndTheIDs(t *testing.T) {
	t.Parallel()

	packs := []policy.PolicyPackRef{
		{ID: "a"},
		{ID: "b", Precedence: &policy.Precedence{Version: 3, Order: []string{"X", "Y"}}},
		{ID: "c", Precedence: &policy.Precedence{Version: 1, Order: []string{"Z"}}},
	}

	got := precedencesOf(packs)
	if !precedencesHavePackOrder(got) {
		t.Fatalf("precedences = %+v", got)
	}
}

func TestTypedEnforcementDefaultsToAdvisory(t *testing.T) {
	t.Parallel()

	var cfg EnforcementConfig

	for declared, want := range map[policy.Enforcement]policy.Enforcement{
		"": policy.EnforcementAdvisory, policy.EnforcementMandatory: policy.EnforcementMandatory, policy.EnforcementShadow: policy.EnforcementShadow,
	} {
		plan := &policy.RulePlan{Enforcement: declared, Predicate: policy.Predicate{Family: policy.FamilyTyped}}
		if got := cfg.Resolve(plan, "").Mode; got != want {
			t.Errorf("declared %q = %q, want %q", declared, got, want)
		}
	}
}

func TestTypedRulesAreNotReducibleToChangedFiles(t *testing.T) {
	t.Parallel()

	rules := []RuleResult{{RuleID: "T-1", Family: policy.FamilyTyped, Verdict: VerdictViolation, FindingCount: 1}}
	findings := []Finding{{RuleID: "T-1", Location: &Location{Path: "k8s/prod/other.yaml"}}}

	out, kept := reduceToChanged(rules, findings, map[string]struct{}{"a.go": {}})

	if !typedRuleReportedInFull(out, kept) {
		t.Fatalf("a typed rule is reported in full: %+v %+v", out, kept)
	}

	if got := fullScopeRules(out); len(got) != 1 || got[0] != "T-1" {
		t.Fatalf("full scope rules = %v, want the typed rule (CO-CON-027 names it)", got)
	}
}

func TestConflictDiagnosticNamesTheIdentityAndEveryLocation(t *testing.T) {
	t.Parallel()

	id := observation.ScopedID{Environment: "production", Cluster: "prod-eu", APIGroup: "apps", Kind: "Deployment", Namespace: "payments", Name: "reports"}
	conflicts := []observation.Conflict{
		{ID: id, Certain: true, Locations: []observation.Location{{Path: "k8s/overrides/reports.yaml", Line: 1}, {Path: "k8s/prod/reports.yaml"}}},
		{ID: observation.ScopedID{Environment: "production", APIGroup: "apps", Kind: "Deployment", Name: "gateway"}, Locations: []observation.Location{{Path: "a.yaml"}, {Path: "b.yaml"}}},
	}

	got := conflictDiagnostics(conflicts)
	if !conflictHeadIsObs021Warning(got) {
		t.Fatalf("diagnostics = %+v", got)
	}

	for _, want := range []string{id.String(), "k8s/overrides/reports.yaml:1", "k8s/prod/reports.yaml", "no declaration wins"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q lacks %q", got[0].Message, want)
		}
	}

	if !strings.Contains(got[1].Message, "may be different resources") {
		t.Errorf("an uncertain group must say so: %q", got[1].Message)
	}
}

func TestIdentityConflictsSkipOnlyTheMalformedNode(t *testing.T) {
	t.Parallel()

	resource := func(file, objKey string) observation.ObservedNode {
		return observation.ObservedNode{
			Label: "Resource", FilePath: file, Environment: "production",
			Properties: map[string]string{"obj_key": objKey},
		}
	}

	evidence := &fetched{result: observation.EvidenceQueryResult{Nodes: map[string]observation.ObservedNode{
		"1": resource("k8s/a.yaml", "apps/Deployment/payments/api"),
		"2": resource("k8s/b.yaml", "apps/Deployment/payments/api"),
		"3": resource("k8s/bad.yaml", "nonsense"),
	}}}

	got := identityConflicts(&workspace.Workspace{}, evidence)
	if len(got) != 1 || len(got[0].Locations) != 2 {
		t.Fatalf("a malformed node must not hide the other conflict: %+v", got)
	}
}
