package conformance_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// nextActions is the vocabulary of OUT-02.
var nextActions = []string{
	"fix implementation", "correct intent", "resolve binding", "supply evidence", "regenerate view", "get review", "fix analysis configuration",
}

// forbiddenCallWorld has a forbidden `calls-service` fact from billing to ledger in a profile that names a loss mode.
func forbiddenCallWorld() *world {
	w := newWorld()
	w.typed.Workspace = nil
	w.typed.Families = map[string]observation.FamilyState{
		"calls-service": {Level: observation.SupportPartial, Maturity: observation.MaturityAdvisory, LossModes: []string{"merged-occurrences", "dynamic-target"}},
	}
	w.scope.Predicates = []observation.PredicateCoverage{{
		Predicate: "calls-service", Scope: observation.CoverageScope{Project: "p"}, Status: observation.CoverageComplete, LossModes: []observation.LossMode{}, Reasons: []string{},
	}}
	w.add(observation.ObservedNode{ObservedID: "cbm:p:a", Label: "Module", QualifiedName: "a", FilePath: "services/billing/a.ts"}, "billing")
	w.add(observation.ObservedNode{ObservedID: "cbm:p:b", Label: "Module", QualifiedName: "b", FilePath: "services/ledger/b.ts"}, "ledger")
	w.edges = []*observation.EvidenceEdge{fact(observation.KindCallsService, "a", "b", nil)}
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationForbidden, observation.KindCallsService, policy.DirectionFromSubject,
		func(s *policy.ObligationSpec) {
			s.Relation.Target, s.Scope.Environments = []string{"ledger"}, []string{}
		})}

	return w
}

// textHasAll says whether the text holds every part.
func textHasAll(text string, parts ...string) bool {
	return !slices.ContainsFunc(parts, func(part string) bool { return !strings.Contains(text, part) })
}

// violationFindingOf evaluates the forbidden call world with a profile and gives its only finding.
func violationFindingOf(t *testing.T) conformance.Finding {
	t.Helper()

	w := forbiddenCallWorld()
	input := conformance.EvaluateInput{
		Plans: w.plans, Snapshot: conformance.Snapshot{Project: "p", GraphGeneration: "g"}, Scope: w.scope, Typed: w.typed,
		Profile: &observation.ProfileRef{ID: "cbm-0.12.1", Digest: "sha256:abc"},
	}
	w.set.ByObserved, w.set.MatchCounts = w.bound, map[string]int{}
	input.ReconcileInput = conformance.ReconcileInput{
		Model: w.model, BindingSet: w.set, Evidence: conformance.EvidenceSet{Nodes: w.nodes, Edges: w.edges}, BindingRootsByNode: map[string][]string{},
	}

	out := conformance.Evaluate(&input)
	if len(out.Findings) != 1 {
		t.Fatalf("findings = %+v", out.Findings)
	}

	return out.Findings[0]
}

// assertViolationIdentity checks the identity, rule, subject, result, severity, and disposition of the finding.
func assertViolationIdentity(t *testing.T, f *conformance.Finding) {
	t.Helper()

	const message = "identity, rule, subject, result, severity, or disposition is missing: %+v"

	if f.Fingerprint == "" || f.ObligationID == "" {
		t.Errorf(message, *f)
	}

	if f.RuleID != "T-1" || f.Subject != "billing" {
		t.Errorf(message, *f)
	}

	if f.Result != conformance.VerdictViolation {
		t.Errorf(message, *f)
	}

	if f.Severity == "" || f.Disposition == "" {
		t.Errorf(message, *f)
	}
}

// assertViolationPlacement checks the location, the related list, the area, and the next action of the finding.
func assertViolationPlacement(t *testing.T, f *conformance.Finding) {
	t.Helper()

	const message = "location, related, area, or next action: %+v"

	if f.Location == nil || f.Related == nil {
		t.Errorf(message, *f)
	}

	if len(f.Related) != 0 || f.Area != conformance.AreaConformance {
		t.Errorf(message, *f)
	}

	if !slices.Contains(nextActions, f.NextAction) {
		t.Errorf(message, *f)
	}
}

// assertViolationProviderLimits checks that the limits name the profile and the loss modes.
func assertViolationProviderLimits(t *testing.T, f *conformance.Finding) {
	t.Helper()

	const message = "provider limits = %+v: they must name the profile and the loss modes"

	limits := f.ProviderLimits
	if !limits.Profile.Applies || limits.Profile.ID != "cbm-0.12.1" {
		t.Errorf(message, limits)
	}

	if len(limits.Families) != 1 {
		t.Errorf(message, limits)

		return
	}

	if limits.Families[0].Family != "calls-service" || !slices.Equal(limits.Families[0].LossModes, []string{"dynamic-target", "merged-occurrences"}) {
		t.Errorf(message, limits)
	}
}

func TestOut02ViolationFindingHasAllFields(t *testing.T) {
	t.Parallel()

	f := violationFindingOf(t)

	assertViolationIdentity(t, &f)

	if !textHasAll(f.Expected, "forbidden", "calls-service", "ledger") {
		t.Errorf("expected = %q", f.Expected)
	}

	if !textHasAll(f.Observed, "calls-service fact", "a to b") {
		t.Errorf("observed = %q", f.Observed)
	}

	assertViolationPlacement(t, &f)
	assertViolationProviderLimits(t, &f)
}

// findingTree marshals the finding to JSON and decodes it into a tree.
func findingTree(t *testing.T, f *conformance.Finding) (tree map[string]any, text []byte) {
	t.Helper()

	text, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(text, &tree); err != nil {
		t.Fatal(err)
	}

	return tree, text
}

func TestOut02FindingsHaveAFixedShape(t *testing.T) {
	t.Parallel()

	w := forbiddenCallWorld()
	out := w.evaluate()

	tree, text := findingTree(t, &out.Findings[0])

	for _, key := range []string{"expected", "observed", "delta", "providerLimits", "related", "area", "nextAction", "enforcement", "obligationId", "subject", "result"} {
		if _, ok := tree[key]; !ok {
			t.Errorf("the finding has no key %q: %s", key, text)
		}
	}

	delta, _ := tree["delta"].(map[string]any)
	for key, want := range map[string]string{"class": "undeclared", "from": "billing", "to": "ledger"} {
		if delta[key] != want {
			t.Errorf("delta = %v, want the undeclared pair that the fact belongs to", tree["delta"])
		}
	}

	limits, _ := tree["providerLimits"].(map[string]any)

	profile, _ := limits["profile"].(map[string]any)
	if profile["applies"] != false {
		t.Errorf("a run with no profile says so: %v", tree["providerLimits"])
	}
}

// unknownFindingCase is a world edit that makes the obligation unknown, with the finding it must give.
type unknownFindingCase struct {
	name   string
	edit   func(*world)
	reason conformance.UnknownReason
	action string
}

// assertUnknownFinding checks that the finding explains why the obligation is unknown.
func assertUnknownFinding(t *testing.T, f *conformance.Finding, o *conformance.ObligationResult, tc *unknownFindingCase) {
	t.Helper()

	const message = "finding = %+v"

	if f.Kind != conformance.KindObligationUnknown || f.Result != conformance.VerdictUnknown {
		t.Errorf(message, *f)
	}

	if f.Reason != tc.reason || f.ObligationID != o.ID {
		t.Errorf(message, *f)
	}

	if f.NextAction != tc.action || f.Expected == "" {
		t.Errorf(message, *f)
	}

	if !textHasAll(f.Observed, string(tc.reason), o.Detail) {
		t.Errorf(message, *f)
	}
}

func TestOut02UnknownObligationHasAFindingThatExplainsTheGap(t *testing.T) {
	t.Parallel()

	for _, tc := range []unknownFindingCase{
		{"partial coverage", func(w *world) { w.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage) }, conformance.ReasonCoverageIncomplete, "supply evidence"},
		{"limit exceeded", func(w *world) {
			w.scope.Breaches = []conformance.LimitBreach{{Limit: conformance.LimitGraphMaxEdges, Configured: 1, Observed: 2}}
		}, conformance.ReasonLimitExceeded, "fix analysis configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld(observation.KindUsesImage)
			tc.edit(w)
			w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, nil)}

			out := w.evaluate()
			rule := typedRuleResult(t, &out, "T-1")
			o := obligationOf(t, rule, "billing", "production")

			if len(out.Findings) != 1 {
				t.Fatalf("findings = %+v, want one for the unknown obligation", out.Findings)
			}

			f := out.Findings[0]
			assertUnknownFinding(t, &f, o, &tc)

			if conformance.IsFailing(&f, "info") {
				t.Errorf("an unknown finding never gates by itself: the verdict of the rule does")
			}
		})
	}
}

func TestOut02UnknownFindingIsLeftOutWhenTheRuleIgnoresUnknown(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindUsesImage)
	w.scope.Predicates = records(observation.CoveragePartial, observation.KindUsesImage)
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, nil)}
	w.plans[0].EvidenceRequirements.OnUnknown = policy.UnknownIgnore

	out := w.evaluate()
	if len(out.Findings) != 0 {
		t.Fatalf("findings = %+v", out.Findings)
	}
}

func TestOut02BindingGapKeepsItsFindingAndGetsNoSecondOne(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindCallsService)
	w.typed.Workspace = nil
	w.add(observation.ObservedNode{ObservedID: "cbm:p:a", Label: "Module", QualifiedName: "a", FilePath: "services/billing/a.ts"}, "billing")
	w.add(observation.ObservedNode{ObservedID: "cbm:p:b", Label: "Module", QualifiedName: "b", FilePath: "services/other/b.ts"})
	w.edges = []*observation.EvidenceEdge{fact(observation.KindCallsService, "a", "b", nil)}
	w.scope.Predicates = records(observation.CoverageComplete, observation.KindCallsService)
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationForbidden, observation.KindCallsService, policy.DirectionFromSubject,
		func(s *policy.ObligationSpec) {
			s.Relation.Target, s.Scope.Environments = []string{"ledger"}, []string{}
		})}

	out := w.evaluate()

	kinds := map[conformance.FindingKind]int{}
	for i := range out.Findings {
		kinds[out.Findings[i].Kind]++

		f := &out.Findings[i]
		if slices.Contains([]string{f.NextAction, f.Expected, f.Observed}, "") {
			t.Errorf("finding %+v lacks a field", *f)
		}
	}

	if kinds[conformance.KindObligationUnknown] > 0 && kinds[conformance.KindBindingGap] > 0 {
		t.Errorf("an obligation has a binding gap finding and an unknown finding: %v", kinds)
	}
}
