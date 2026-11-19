package conformance

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
)

func controlWith(rules, checks []string, edit func(*policy.ControlPlan)) policy.ControlPlan {
	plan := policy.ControlPlan{
		ControlID: "C-1", Scope: intent.ControlScope{Level: intent.ScopeNode, ID: "billing"},
		RuleIDs: rules, CheckIDs: checks, Applicable: true,
	}
	if edit != nil {
		edit(&plan)
	}

	return plan
}

func ruleVerdicts(verdicts map[string]RuleVerdict) []RuleResult {
	out := make([]RuleResult, 0, len(verdicts))
	for id, verdict := range verdicts {
		out = append(out, RuleResult{RuleID: id, Verdict: verdict})
	}

	return out
}

func checksOf(checks map[string]ExternalCheck) *ExternalEvaluation {
	return &ExternalEvaluation{Checks: checks}
}

// Task 8.5: the verdict of a control is the worst verdict of its rules and its checks.
func TestControlVerdictIsTheWorstOfRulesAndChecks(t *testing.T) {
	t.Parallel()

	pass := ExternalCheck{Verdict: VerdictPass}
	unknownCheck := ExternalCheck{Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied, Detail: "every result is inadmissible"}

	for _, tc := range []struct {
		name   string
		rules  map[string]RuleVerdict
		checks map[string]ExternalCheck
		want   RuleVerdict
	}{
		{"rule passes and check is unknown", map[string]RuleVerdict{"R-1": VerdictPass}, map[string]ExternalCheck{"unit": unknownCheck}, VerdictUnknown},
		{"rule is unknown and check passes", map[string]RuleVerdict{"R-1": VerdictUnknown}, map[string]ExternalCheck{"unit": pass}, VerdictUnknown},
		{"rule violates and check is unknown", map[string]RuleVerdict{"R-1": VerdictViolation}, map[string]ExternalCheck{"unit": unknownCheck}, VerdictViolation},
		{"rule passes and check violates", map[string]RuleVerdict{"R-1": VerdictPass}, map[string]ExternalCheck{"unit": {Verdict: VerdictViolation}}, VerdictViolation},
		{"both pass", map[string]RuleVerdict{"R-1": VerdictPass}, map[string]ExternalCheck{"unit": pass}, VerdictPass},
		{"a check that errors", map[string]RuleVerdict{"R-1": VerdictPass}, map[string]ExternalCheck{"unit": {Verdict: VerdictError, Detail: "result unreadable"}}, VerdictError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, _ := controlResults([]policy.ControlPlan{controlWith([]string{"R-1"}, []string{"unit"}, nil)}, ruleVerdicts(tc.rules), checksOf(tc.checks), "")
			if len(got) != 1 || got[0].Verdict != tc.want {
				t.Fatalf("control = %+v, want %s", got, tc.want)
			}
		})
	}
}

// controlJudgedByChecksOnly reports whether a result for a control with no rules shows the given verdict and no rule or reason entries.
func controlJudgedByChecksOnly(result *ControlResult, want RuleVerdict) bool {
	return result.Verdict == want && len(result.Reasons) == 0 && len(result.RuleIDs) == 0
}

func TestControlWithChecksOnlyIsJudgedByItsChecks(t *testing.T) {
	t.Parallel()

	control := controlWith([]string{}, []string{"sast", "unit"}, nil)

	got, _ := controlResults([]policy.ControlPlan{control}, nil, checksOf(map[string]ExternalCheck{"sast": {Verdict: VerdictPass}, "unit": {Verdict: VerdictPass}}), "")
	if !controlJudgedByChecksOnly(&got[0], VerdictPass) {
		t.Errorf("all checks pass: %+v", got[0])
	}

	got, _ = controlResults([]policy.ControlPlan{control}, nil, checksOf(map[string]ExternalCheck{"sast": {Verdict: VerdictPass}}), "")
	if got[0].Verdict != VerdictUnknown {
		t.Errorf("a check with no result must not fulfil the control: %+v", got[0])
	}
}

// namesCheckWithNoEvidence reports whether the control is unknown with exactly one no-evidence reason that names the check.
func namesCheckWithNoEvidence(result *ControlResult, check string) bool {
	if result.Verdict != VerdictUnknown || len(result.Reasons) != 1 {
		return false
	}

	reason := result.Reasons[0]

	return reason.Code == string(ReasonNoEvidenceSupplied) && strings.Contains(reason.Message, check)
}

func TestCheckWithoutAResultGivesNoEvidenceSuppliedNamingTheCheck(t *testing.T) {
	t.Parallel()

	for name, external := range map[string]*ExternalEvaluation{"no assurance evidence": nil, "checks without the entry": checksOf(map[string]ExternalCheck{})} {
		got, _ := controlResults([]policy.ControlPlan{controlWith([]string{}, []string{"unit-tests"}, nil)}, nil, external, "")

		if !namesCheckWithNoEvidence(&got[0], "unit-tests") {
			t.Errorf("%s: control = %+v", name, got[0])
		}
	}
}

func TestControlResultListsChecksOnlyWhenThereAreSome(t *testing.T) {
	t.Parallel()

	rulesOnly, _ := controlResults([]policy.ControlPlan{controlWith([]string{"R-1"}, nil, nil)}, ruleVerdicts(map[string]RuleVerdict{"R-1": VerdictPass}), nil, "")

	data, err := json.Marshal(rulesOnly[0])
	if err != nil {
		t.Fatal(err)
	}

	if jsonMentionsAny(data, "checkIds", "reasons") || rulesOnly[0].Verdict != VerdictPass {
		t.Errorf("a control with no check keeps its shape: %s", data)
	}

	withChecks, _ := controlResults([]policy.ControlPlan{controlWith(nil, []string{"sast"}, nil)}, nil, checksOf(map[string]ExternalCheck{"sast": {Verdict: VerdictPass}}), "")

	data, err = json.Marshal(withChecks[0])
	if err != nil || !jsonMentionsAll(data, `"checkIds":["sast"]`, `"ruleIds":[]`) {
		t.Errorf("control JSON = %s (%v)", data, err)
	}
}

func jsonMentionsAny(data []byte, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(string(data), fragment) {
			return true
		}
	}

	return false
}

func jsonMentionsAll(data []byte, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(string(data), fragment) {
			return false
		}
	}

	return true
}

func TestControlOfAnotherEngineIsNotApplicable(t *testing.T) {
	t.Parallel()

	control := controlWith(nil, []string{"sast"}, func(p *policy.ControlPlan) { p.Applicable = false })

	got, failing := controlResults([]policy.ControlPlan{control}, nil, nil, policy.UnknownFail)
	if got[0].Verdict != VerdictNotApplicable || failing {
		t.Errorf("control = %+v failing %v", got[0], failing)
	}
}

// controlCoverageReasons returns the coverage-incomplete reasons of a control result.
func controlCoverageReasons(result *ControlResult) []Reason {
	var coverage []Reason

	for _, r := range result.Reasons {
		if r.Code == string(ReasonCoverageIncomplete) {
			coverage = append(coverage, r)
		}
	}

	return coverage
}

// reportsTwoOfFour reports whether there is exactly one coverage reason and it says 2 of 4 checks have a result.
func reportsTwoOfFour(coverage []Reason) bool {
	return len(coverage) == 1 && strings.Contains(coverage[0].Message, "2 of 4")
}

func coverageControlOver(minimum float64) ControlResult {
	half := map[string]ExternalCheck{"a": {Verdict: VerdictPass}, "b": {Verdict: VerdictNotApplicable}, "c": {Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied}, "d": {Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied}}
	withMinimum := func(p *policy.ControlPlan) { p.MinimumPolicyCoverage = &minimum }

	got, _ := controlResults([]policy.ControlPlan{controlWith(nil, []string{"a", "b", "c", "d"}, withMinimum)}, nil, checksOf(half), "")

	return got[0]
}

func TestMinimumPolicyCoverageAppliesToTheShareOfChecksWithAResult(t *testing.T) {
	t.Parallel()

	// The two unknown checks already make the control unknown; the coverage adds the reason that says by how much.
	below := coverageControlOver(0.75)
	coverage := controlCoverageReasons(&below)

	if below.Verdict != VerdictUnknown || !reportsTwoOfFour(coverage) {
		t.Errorf("below the minimum: %+v", below)
	}

	enough := coverageControlOver(0.5)
	if len(controlCoverageReasons(&enough)) != 0 {
		t.Errorf("coverage 0.5 meets the minimum 0.5: %+v", enough)
	}

	// A control with no check has no coverage to judge, whatever the minimum is.
	minimumOne := 1.0
	withMinimum := func(p *policy.ControlPlan) { p.MinimumPolicyCoverage = &minimumOne }

	rulesOnly, _ := controlResults([]policy.ControlPlan{controlWith([]string{"R-1"}, nil, withMinimum)}, ruleVerdicts(map[string]RuleVerdict{"R-1": VerdictPass}), nil, "")
	if rulesOnly[0].Verdict != VerdictPass {
		t.Errorf("rules only: %+v", rulesOnly[0])
	}
}

func TestUnknownDispositionOfAControlDecidesWhetherAnUnknownCheckFailsTheRun(t *testing.T) {
	t.Parallel()

	disposition := func(d policy.UnknownDisposition) func(*policy.ControlPlan) {
		return func(p *policy.ControlPlan) { p.UnknownDisposition = d }
	}

	for _, tc := range []struct {
		edit   func(*policy.ControlPlan)
		checks map[string]ExternalCheck
		name   string
		cli    policy.UnknownDisposition
		want   bool
	}{
		{disposition(policy.UnknownFail), nil, "control fail and no result", "", true},
		{disposition(policy.UnknownWarn), nil, "control warn", "", false},
		{disposition(policy.UnknownIgnore), nil, "control ignore", "", false},
		{nil, nil, "nothing set is advisory", "", false},
		{nil, nil, "--unknown fail", policy.UnknownFail, true},
		{disposition(policy.UnknownFail), nil, "--unknown warn beats the control", policy.UnknownWarn, false},
		{disposition(policy.UnknownFail), map[string]ExternalCheck{"unit": {Verdict: VerdictPass}}, "control fail and a passing check", "", false},
		{disposition(policy.UnknownFail), map[string]ExternalCheck{"unit": {Verdict: VerdictViolation}}, "control fail and a violation", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, failing := controlResults([]policy.ControlPlan{controlWith(nil, []string{"unit"}, tc.edit)}, nil, checksOf(tc.checks), tc.cli)
			if failing != tc.want {
				t.Errorf("fails the run = %v, want %v", failing, tc.want)
			}
		})
	}
}

// A check result that is not valid (an unknown with a reason outside the closed set) is an error, as for a rule.
func TestCheckVerdictWithAnOpenReasonIsAnError(t *testing.T) {
	t.Parallel()

	got := (*ExternalEvaluation)(nil).checkVerdict("x")
	if got.Verdict != VerdictUnknown || got.Reason != ReasonNoEvidenceSupplied {
		t.Fatalf("no evidence: %+v", got)
	}

	bad := checksOf(map[string]ExternalCheck{"x": {Verdict: VerdictUnknown, Reason: "stale"}}).checkVerdict("x")
	if bad.Verdict != VerdictError {
		t.Errorf("open reason: %+v", bad)
	}
}
