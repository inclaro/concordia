package conformance_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// reviewRule is a rule of the review-currency pack with no mode of its own.
func reviewRule(id string) policy.RulePlan {
	return plan(policy.Predicate{Family: policy.FamilyAssessmentCurrency, AssessmentCurrency: "approval"}, func(p *policy.RulePlan) {
		p.RuleID, p.Family, p.Pack = id, string(policy.FamilyAssessmentCurrency), policy.PackRef{ID: "review-currency", Version: "1.0.0"}
		p.Traversal.EdgeKinds = nil
	})
}

func reviewGuarantees(declared []string, authorities int, anchor bool) *conformance.EnforcementGuarantees {
	g := guarantees(nil, declared, nil)
	g.Authorities, g.TrustAnchor = authorities, anchor

	return g
}

func mandatoryReviewEntries() []workspace.EnforcementEntry {
	return []workspace.EnforcementEntry{{Pack: "review-currency", Mode: workspace.ModeMandatory}}
}

func asDiagError(t *testing.T, err error, code string) *diag.Error {
	t.Helper()

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("error %v is not a diagnostic", err)
	}

	if failure.Diagnostic.Code != code || failure.ExitCode != diag.InvalidInput {
		t.Fatalf("diagnostic = %+v, want %s with exit 2", failure, code)
	}

	return failure
}

// Scenario: Review rules default to advisory.
func TestReviewRulesDefaultToAdvisory(t *testing.T) {
	t.Parallel()

	var cfg conformance.EnforcementConfig

	rule := reviewRule("REV-APR-001")
	if got := cfg.Resolve(&rule, "production"); got.Mode != policy.EnforcementAdvisory || got.Source != conformance.SourceDefault {
		t.Fatalf("Resolve = %+v, want the advisory default", got)
	}

	// A default is not a choice: no guarantee is validated and no authority is needed.
	if err := cfg.ValidateEnforcement([]policy.RulePlan{rule}, reviewGuarantees(nil, 0, false)); err != nil {
		t.Fatalf("an advisory default needs no guarantee: %v", err)
	}
}

func TestReviewRulesAreReportedInTheAssessmentCurrencyArea(t *testing.T) {
	t.Parallel()

	if got := conformance.AreaOfFamily(policy.FamilyAssessmentCurrency); got != conformance.AreaAssessmentCurrency {
		t.Fatalf("area = %s", got)
	}
}

// Scenario: Rollback covers review rules.
func TestRollbackCoversReviewRules(t *testing.T) {
	t.Parallel()

	wsp := rollbackWorkspace()
	wsp.Check.Enforcement = mandatoryReviewEntries()

	cfg, err := conformance.NewEnforcementConfig(wsp, time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(cfg.Rollback.Message, "review rules ran as shadow") {
		t.Errorf("rollback message = %q does not name the review rules", cfg.Rollback.Message)
	}

	rule := reviewRule("REV-APR-001")
	if got := cfg.Resolve(&rule, ""); got.Mode != policy.EnforcementShadow || got.Source != conformance.SourceRollback {
		t.Errorf("review rule = %+v, want shadow by rollback over the mandatory entry", got)
	}

	// A rule that runs as shadow depends on no guarantee and no authority.
	if err := cfg.ValidateEnforcement([]policy.RulePlan{rule}, reviewGuarantees(nil, 0, false)); err != nil {
		t.Fatalf("a rolled back review rule needs no guarantee: %v", err)
	}
}

// Scenario: Mandatory without authority.
func TestMandatoryReviewRuleWithoutAuthorityOrAnchorGivesASM004(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: mandatoryReviewEntries()}
	rules := []policy.RulePlan{reviewRule("REV-APR-001")}
	declared := []string{"production"}

	for name, tc := range map[string]struct {
		want        string
		authorities int
		anchor      bool
	}{
		"no authority":                 {"no authority is configured", 0, true},
		"no trust anchor":              {"no trust anchor is configured", 1, false},
		"neither authority nor anchor": {"no trust anchor is configured", 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			failure := asDiagError(t, cfg.ValidateEnforcement(rules, reviewGuarantees(declared, tc.authorities, tc.anchor)), diag.ASM004)
			if !strings.Contains(failure.Diagnostic.Message, "REV-APR-001") || !strings.Contains(failure.Diagnostic.Message, "production") ||
				!strings.Contains(strings.Join(messagesOf(failure), "\n"), tc.want) {
				t.Errorf("messages = %v", messagesOf(failure))
			}
		})
	}
}

func messagesOf(failure *diag.Error) []string {
	out := make([]string, 0, 1+len(failure.Diagnostic.Cause))
	out = append(out, failure.Diagnostic.Message)

	for i := range failure.Diagnostic.Cause {
		out = append(out, failure.Diagnostic.Cause[i].Message)
	}

	return out
}

// Scenario: Mandatory gate without R0 guarantees.
func TestMandatoryReviewRuleWithAnAbsentGuaranteeGivesPOL018(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: mandatoryReviewEntries()}
	rules := []policy.RulePlan{reviewRule("REV-APR-001")}

	// The workspace declares no environment: environment attribution is absent.
	failure := asDiagError(t, cfg.ValidateEnforcement(rules, reviewGuarantees(nil, 1, true)), diag.POL018)
	mentions(t, failure, "REV-APR-001", "environment attribution missing")

	// An entry for an environment that the workspace does not declare gives the same refusal.
	cfg = conformance.EnforcementConfig{Entries: []workspace.EnforcementEntry{{Pack: "review-currency", Environment: "production", Mode: workspace.ModeMandatory}}}
	failure = asDiagError(t, cfg.ValidateEnforcement(rules, reviewGuarantees([]string{"staging"}, 1, true)), diag.POL018)
	mentions(t, failure, "REV-APR-001", "production", "environment attribution missing")
}

func TestMandatoryReviewRuleWithItsGuaranteesRuns(t *testing.T) {
	t.Parallel()

	cfg := conformance.EnforcementConfig{Entries: mandatoryReviewEntries()}
	rules := []policy.RulePlan{reviewRule("REV-APR-001"), reviewRule("REV-GOV-001")}

	if err := cfg.ValidateEnforcement(rules, reviewGuarantees([]string{"production"}, 1, true)); err != nil {
		t.Fatalf("the guarantees are present: %v", err)
	}

	if got := cfg.Resolve(&rules[0], "production").Mode; got != policy.EnforcementMandatory {
		t.Fatalf("mode = %s", got)
	}
}

func TestAdvisoryReviewEntryNeedsNoAuthority(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{workspace.ModeAdvisory, workspace.ModeShadow} {
		cfg := conformance.EnforcementConfig{Entries: []workspace.EnforcementEntry{{Pack: "review-currency", Mode: mode}}}

		if err := cfg.ValidateEnforcement([]policy.RulePlan{reviewRule("REV-APR-001")}, reviewGuarantees(nil, 0, false)); err != nil {
			t.Errorf("%s: %v", mode, err)
		}
	}
}

func TestReviewCurrencyFamilyIsNotAnEdgeFamily(t *testing.T) {
	t.Parallel()

	if policy.FamilyAssessmentCurrency.EdgeFindings() {
		t.Fatal("assessment-currency findings are not edges")
	}
}

// The rollback documentation names the review rules: the `check.rollback` row and section of the workspace reference
// and the Roll back section of the roll-out guide.
func TestRollbackDocumentationNamesTheReviewRules(t *testing.T) {
	t.Parallel()

	for file, parts := range map[string][]string{
		"../../docs/reference/workspace.md":             {"Runs the typed, decision-governance, and review rules as `shadow`", "`REV-APR-001`", "`assessment-currency`"},
		"../../docs/how-to/roll-out-new-obligations.md": {"every review rule", "`REV-GOV-001`", "`mandatory` is `shadow` too"},
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}

		for _, part := range parts {
			if !strings.Contains(string(data), part) {
				t.Errorf("%s does not say %q", file, part)
			}
		}
	}
}
