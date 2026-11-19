package impact_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/impact"
)

func reviewRule() impact.RuleSetting {
	return impact.RuleSetting{
		RuleID: impact.RuleApproval, Severity: "warning", OnUnknown: "warn", MinimumCoverage: 0.8,
		MinimumConfidence: "syntactic", Scope: []string{"a", "b"}, Other: "sha256:def",
	}
}

func anchorState() impact.GovernanceState {
	return impact.GovernanceState{
		Rules:       []impact.RuleSetting{reviewRule()},
		Enforcement: []impact.EnforcementSetting{{Pack: "review-currency", Mode: "mandatory"}},
		Authorities: []impact.AuthoritySetting{
			{ID: "reviewers", Role: "reviewer", Mechanism: "protected-review", Reviewers: []string{"alice", "bob"}, Paths: []string{"assessments/**"}},
			{ID: "governance", Role: impact.RoleGovernance, Mechanism: "protected-review", Reviewers: []string{"carol"}},
		},
	}
}

func cloneState(s impact.GovernanceState) impact.GovernanceState {
	out := impact.GovernanceState{
		Rules:       slices.Clone(s.Rules),
		Enforcement: slices.Clone(s.Enforcement),
		Authorities: slices.Clone(s.Authorities),
	}

	return out
}

func TestStrictnessPerAxis(t *testing.T) {
	with := func(edit func(*impact.RuleSetting)) impact.RuleSetting {
		r := reviewRule()
		edit(&r)

		return r
	}

	tests := []struct {
		name string
		edit func(*impact.RuleSetting)
		want impact.Comparison
	}{
		{"same", func(*impact.RuleSetting) {}, impact.Equal},
		{"severity raised", func(r *impact.RuleSetting) { r.Severity = "error" }, impact.Stricter},
		{"severity lowered", func(r *impact.RuleSetting) { r.Severity = "info" }, impact.Looser},
		{"unknown value", func(r *impact.RuleSetting) { r.Severity = "fatal" }, impact.Incomparable},
		{"onUnknown fail", func(r *impact.RuleSetting) { r.OnUnknown = "fail" }, impact.Stricter},
		{"onUnknown ignore", func(r *impact.RuleSetting) { r.OnUnknown = "ignore" }, impact.Looser},
		{"coverage raised", func(r *impact.RuleSetting) { r.MinimumCoverage = 0.9 }, impact.Stricter},
		{"coverage lowered", func(r *impact.RuleSetting) { r.MinimumCoverage = 0.5 }, impact.Looser},
		{"confidence resolved", func(r *impact.RuleSetting) { r.MinimumConfidence = "resolved" }, impact.Stricter},
		{"confidence heuristic", func(r *impact.RuleSetting) { r.MinimumConfidence = "heuristic" }, impact.Looser},
		{"confidence misspelled", func(r *impact.RuleSetting) { r.MinimumConfidence = "sure" }, impact.Incomparable},
		{"scope wider", func(r *impact.RuleSetting) { r.Scope = []string{"a", "b", "c"} }, impact.Stricter},
		{"scope narrower", func(r *impact.RuleSetting) { r.Scope = []string{"a"} }, impact.Looser},
		{"scope replaced", func(r *impact.RuleSetting) { r.Scope = []string{"a", "c"} }, impact.Incomparable},
		{"scope order only", func(r *impact.RuleSetting) { r.Scope = []string{"b", "a"} }, impact.Equal},
		{"definition changed", func(r *impact.RuleSetting) { r.Other = "sha256:other" }, impact.Incomparable},
		{
			"severity up and coverage down",
			func(r *impact.RuleSetting) { r.Severity = "error"; r.MinimumCoverage = 0.1 },
			impact.Incomparable,
		},
		{
			"severity and coverage both up",
			func(r *impact.RuleSetting) { r.Severity = "error"; r.MinimumCoverage = 0.95 },
			impact.Stricter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := impact.Strictness(reviewRule(), with(tt.edit)); got != tt.want {
				t.Errorf("Strictness = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestEnforcementModeOrder(t *testing.T) {
	mode := func(m string) impact.EnforcementSetting {
		return impact.EnforcementSetting{Pack: "review-currency", Mode: m}
	}

	order := []string{"shadow", "advisory", "mandatory"}

	for i, from := range order {
		for j, to := range order {
			want := impact.Equal

			switch {
			case j > i:
				want = impact.Stricter
			case j < i:
				want = impact.Looser
			}

			if got := impact.Strictness(mode(from), mode(to)); got != want {
				t.Errorf("%s -> %s = %s, want %s", from, to, got, want)
			}
		}
	}

	if got := impact.Strictness(mode("mandatory"), mode("off")); got != impact.Incomparable {
		t.Errorf("an unknown mode = %s, want incomparable", got)
	}
}

func TestStrictnessOfMismatchedSettingsIsIncomparable(t *testing.T) {
	other := reviewRule()
	other.RuleID = impact.RuleDisposition

	if got := impact.Strictness(reviewRule(), other); got != impact.Incomparable {
		t.Errorf("other rule = %s", got)
	}

	if got := impact.Strictness(reviewRule(), impact.AuthoritySetting{ID: impact.RuleApproval}); got != impact.Incomparable {
		t.Errorf("other kind = %s", got)
	}

	if got := impact.Strictness(nil, reviewRule()); got != impact.Incomparable {
		t.Errorf("nil anchor = %s", got)
	}
}

// Scenario: A31 candidate removes its own authority; a review rule set to advisory.
func TestA31RemovedReviewerAndAdvisoryAreRelaxations(t *testing.T) {
	candidate := cloneState(anchorState())
	candidate.Authorities = candidate.Authorities[1:] // the reviewer entry is gone
	candidate.Enforcement = []impact.EnforcementSetting{{Pack: "review-currency", Mode: "advisory"}}

	result := impact.CheckGovernance(impact.Anchor{Revision: "abc", State: anchorState()}, candidate, nil)

	checkEq(t, "verdict", result.Verdict, impact.GovernanceViolation)

	if len(result.Obligations) != 2 {
		t.Fatalf("result = %+v", result)
	}

	for _, o := range result.Obligations {
		assertOpenRelaxationObligation(t, &o)
	}

	first := result.Obligations[0].Change
	checkEq(t, "first obligation kind", first.Kind, impact.SettingAuthority)
	checkEq(t, "first obligation op", first.Op, impact.ChangeRemoved)
	checkEq(t, "first obligation subject", first.Subject, "reviewers")

	second := result.Obligations[1].Change
	checkEq(t, "second obligation kind", second.Kind, impact.SettingEnforcement)
	checkEq(t, "second obligation comparison", second.Comparison, impact.Looser)
}

// assertOpenRelaxationObligation checks an obligation that a relaxation raised and no record answers.
func assertOpenRelaxationObligation(t *testing.T, o *impact.GovernanceObligation) {
	t.Helper()

	checkEq(t, "obligation rule", o.Rule, "REV-GOV-001")
	checkEq(t, "obligation verdict", o.Verdict, impact.GovernanceViolation)
	checkTrue(t, o.Disposition == nil, "an open obligation has a disposition")
	checkTrue(t, o.Change.Relaxation(), "the obligation's change is not a relaxation")
}

// Scenario: Stricter change. Severity warning to error is a possible invalidator, not a relaxation.
func TestRaisedSeverityIsNotARelaxation(t *testing.T) {
	candidate := cloneState(anchorState())
	candidate.Rules[0].Severity = "error"

	result := impact.CheckGovernance(impact.Anchor{State: anchorState()}, candidate, nil)

	checkEq(t, "verdict", result.Verdict, impact.GovernancePass)
	checkEq(t, "obligations", len(result.Obligations), 0)

	if len(result.Changes) != 1 {
		t.Fatalf("changes = %+v", result.Changes)
	}

	checkEq(t, "comparison", result.Changes[0].Comparison, impact.Stricter)
	checkTrue(t, !result.Changes[0].Relaxation(), "a raised severity is a relaxation")
}

// Scenario: Relaxation kinds. A change that Concordia cannot order counts as a relaxation.
func TestIncomparableChangeIsARelaxationAndEveryKindIsOne(t *testing.T) {
	edits := map[string]func(*impact.GovernanceState){
		"remove a rule":       func(s *impact.GovernanceState) { s.Rules = nil },
		"lower a severity":    func(s *impact.GovernanceState) { s.Rules[0].Severity = "info" },
		"weaken evidence":     func(s *impact.GovernanceState) { s.Rules[0].MinimumCoverage = 0.1 },
		"narrow a scope":      func(s *impact.GovernanceState) { s.Rules[0].Scope = []string{"a"} },
		"incomparable rule":   func(s *impact.GovernanceState) { s.Rules[0].Severity = "error"; s.Rules[0].OnUnknown = "ignore" },
		"enforcement to mode": func(s *impact.GovernanceState) { s.Enforcement[0].Mode = "shadow" },
		"remove enforcement":  func(s *impact.GovernanceState) { s.Enforcement = nil },
		"add a reviewer":      func(s *impact.GovernanceState) { s.Authorities[0].Reviewers = []string{"alice", "bob", "mallory"} },
		"remove a reviewer":   func(s *impact.GovernanceState) { s.Authorities[0].Reviewers = []string{"alice"} },
		"edit authority path": func(s *impact.GovernanceState) { s.Authorities[0].Paths = []string{"**"} },
		"add an authority": func(s *impact.GovernanceState) {
			s.Authorities = append(s.Authorities, impact.AuthoritySetting{ID: "extra", Role: "reviewer"})
		},
		"remove an authority": func(s *impact.GovernanceState) { s.Authorities = s.Authorities[:1] },
		"add advisory entry": func(s *impact.GovernanceState) {
			s.Enforcement = append([]impact.EnforcementSetting{{Rules: []string{impact.RuleGovernance}, Mode: "advisory"}}, s.Enforcement...)
		},
	}

	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			candidate := deepClone(anchorState())
			edit(&candidate)

			changes := impact.Relaxations(impact.Changes(anchorState(), candidate))
			if len(changes) != 1 {
				t.Fatalf("relaxations = %+v", changes)
			}

			if changes[0].Digest == "" || changes[0].Detail == "" {
				t.Errorf("change lacks a digest or detail: %+v", changes[0])
			}
		})
	}
}

func deepClone(s impact.GovernanceState) impact.GovernanceState {
	out := cloneState(s)
	for i := range out.Rules {
		out.Rules[i].Scope = slices.Clone(out.Rules[i].Scope)
	}

	for i := range out.Authorities {
		out.Authorities[i].Reviewers = slices.Clone(out.Authorities[i].Reviewers)
		out.Authorities[i].Paths = slices.Clone(out.Authorities[i].Paths)
	}

	return out
}

func TestAddedStrictRulesAndMandatoryEntriesAreNotRelaxations(t *testing.T) {
	candidate := deepClone(anchorState())
	candidate.Rules = append(candidate.Rules, impact.RuleSetting{RuleID: impact.RuleGovernance, Severity: "error"})
	candidate.Enforcement = append(candidate.Enforcement, impact.EnforcementSetting{Rules: []string{impact.RuleCapability}, Mode: "mandatory"})

	changes := impact.Changes(anchorState(), candidate)
	if len(changes) != 2 || len(impact.Relaxations(changes)) != 0 {
		t.Errorf("changes = %+v", changes)
	}
}

func TestReorderedEnforcementEntriesAreARelaxation(t *testing.T) {
	anchor := anchorState()
	anchor.Enforcement = []impact.EnforcementSetting{
		{Rules: []string{impact.RuleApproval}, Mode: "mandatory"},
		{Pack: "review-currency", Mode: "advisory"},
	}

	candidate := deepClone(anchor)
	candidate.Enforcement[0], candidate.Enforcement[1] = candidate.Enforcement[1], candidate.Enforcement[0]

	relaxations := impact.Relaxations(impact.Changes(anchor, candidate))
	if len(relaxations) != 1 {
		t.Fatalf("relaxations = %+v", relaxations)
	}

	checkEq(t, "comparison", relaxations[0].Comparison, impact.Incomparable)
	checkEq(t, "kind", relaxations[0].Kind, impact.SettingEnforcement)
}

func TestChangeDigestBindsTheCandidateConfiguration(t *testing.T) {
	candidateA := deepClone(anchorState())
	candidateA.Rules[0].Severity = "info"

	candidateB := deepClone(anchorState())
	candidateB.Rules[0].Severity = "info"
	candidateB.Rules[0].MinimumCoverage = 0.1

	a := impact.Changes(anchorState(), candidateA)[0]
	again := impact.Changes(anchorState(), deepClone(candidateA))[0]
	b := impact.Changes(anchorState(), candidateB)[0]

	if a.Digest != again.Digest {
		t.Error("the digest is not deterministic")
	}

	if a.Digest == b.Digest {
		t.Error("two different candidate configurations share a digest")
	}
}

// Scenario: A31, the report requires a protected governance disposition and the candidate's own
// records are not accepted.
func TestGovernanceDispositionComesFromTheAnchorAuthority(t *testing.T) {
	candidate := deepClone(anchorState())
	candidate.Rules[0].Severity = "info"

	change := impact.Relaxations(impact.Changes(anchorState(), candidate))[0]
	anchor := impact.Anchor{Revision: "abc", State: anchorState()}

	tests := []struct {
		name    string
		want    impact.GovernanceVerdict
		records []impact.GovernanceRecord
	}{
		{"no record", impact.GovernanceViolation, nil},
		{"verified governance record", impact.GovernancePass, []impact.GovernanceRecord{{ID: "G-1", Authority: "governance", Digest: change.Digest, Verified: true}}},
		{"unverified", impact.GovernanceViolation, []impact.GovernanceRecord{{Authority: "governance", Digest: change.Digest}}},
		{"reviewer authority", impact.GovernanceViolation, []impact.GovernanceRecord{{Authority: "reviewers", Digest: change.Digest, Verified: true}}},
		{"authority the candidate invented", impact.GovernanceViolation, []impact.GovernanceRecord{{Authority: "self", Digest: change.Digest, Verified: true}}},
		{"digest of another change", impact.GovernanceViolation, []impact.GovernanceRecord{{Authority: "governance", Digest: "sha256:other", Verified: true}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The candidate adds a governance authority for itself: it must not count.
			own := deepClone(candidate)
			own.Authorities = append(own.Authorities, impact.AuthoritySetting{ID: "self", Role: impact.RoleGovernance})

			result := impact.CheckGovernance(anchor, candidate, tt.records)
			checkEq(t, "verdict", result.Verdict, tt.want)

			if tt.want == impact.GovernancePass {
				assertSingleDisposedObligation(t, &result)
			}

			// The same records, with the self-authority only in the candidate, give the same verdict
			// for the rule change (the added authority is itself a relaxation).
			withOwn := impact.CheckGovernance(anchor, own, tt.records)
			if tt.want == impact.GovernanceViolation {
				checkEq(t, "verdict with a candidate-added authority", withOwn.Verdict, impact.GovernanceViolation)
			}
		})
	}
}

// assertSingleDisposedObligation checks that the result holds one obligation, answered by a disposition.
func assertSingleDisposedObligation(t *testing.T, result *impact.GovernanceResult) {
	t.Helper()

	if len(result.Obligations) != 1 {
		t.Fatalf("obligations = %+v", result.Obligations)
	}

	checkTrue(t, result.Obligations[0].Disposition != nil, "the obligation has no disposition")
}

// Scenario: Baseline unreadable.
func TestUnreadableAnchorGivesUnknown(t *testing.T) {
	candidate := deepClone(anchorState())
	candidate.Rules = nil

	result := impact.CheckGovernance(impact.Anchor{Unreadable: true, Detail: "ref missing"}, candidate, nil)

	checkEq(t, "verdict", result.Verdict, impact.GovernanceUnknown)
	checkEq(t, "reason", result.Reason, "trust-anchor-unreadable")

	if len(result.Obligations) != 1 {
		t.Fatalf("result = %+v", result)
	}

	obligation := result.Obligations[0]
	checkEq(t, "obligation verdict", obligation.Verdict, impact.GovernanceUnknown)
	checkEq(t, "obligation reason", obligation.Reason, impact.ReasonTrustAnchorUnreadable)
	checkEq(t, "obligation detail", obligation.Detail, "ref missing")
	checkTrue(t, result.Changes != nil, "changes must not be nil")
}

func TestNoChangeIsAPass(t *testing.T) {
	result := impact.CheckGovernance(impact.Anchor{State: anchorState()}, deepClone(anchorState()), nil)
	checkEq(t, "verdict", result.Verdict, impact.GovernancePass)
	checkEq(t, "changes", len(result.Changes), 0)
	checkTrue(t, result.Obligations != nil, "obligations must not be nil")
}

func TestRuleIDs(t *testing.T) {
	want := []string{"REV-APR-001", "REV-DSP-001", "REV-CAP-001", "REV-GOV-001"}
	if got := impact.RuleIDs(); !slices.Equal(got, want) {
		t.Errorf("RuleIDs = %v", got)
	}
}
