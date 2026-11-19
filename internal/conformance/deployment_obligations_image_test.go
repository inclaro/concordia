package conformance_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// ---- from-image ----

// imageWorld has the image `registry/billing`, a Dockerfile that built-from leads to, and a source file. The image is bound
// to billing by an active `image` binding when bound is true.
func imageWorld(bound bool) *world {
	w := newWorld(observation.KindBuiltFrom)
	img := observation.ObservedNode{
		ObservedID: "cbm:p:img", Label: observation.LabelImage, QualifiedName: "registry/billing", Properties: map[string]string{"repository": "registry/billing"},
	}
	dockerfile := observation.ObservedNode{ObservedID: "cbm:p:df", Label: "File", QualifiedName: "docker/Dockerfile", FilePath: "docker/Dockerfile", StartLine: 1}
	source := observation.ObservedNode{ObservedID: "cbm:p:src", Label: "File", QualifiedName: "app/main.go", FilePath: "app/main.go", StartLine: 1}

	w.add(dockerfile)
	w.add(source, "billing")

	if bound {
		w.add(img, "billing")
		w.loaded.Bindings = []binding.Binding{{
			ID: "b-img", ArchitectureRef: "billing", Status: binding.StatusActive,
			Match: []binding.Match{{Kind: binding.MatchImage, Repository: "registry/billing"}},
		}}
		w.set.UsedBy = map[string][]string{"cbm:p:img": {"b-img"}}
	} else {
		w.add(img)
	}

	// The Dockerfile is outside every environment source: the fact has the environment `unresolved`.
	w.edges = []*observation.EvidenceEdge{fact(observation.KindBuiltFrom, "img", "df", func(e *observation.EvidenceEdge) {
		e.Environment = observation.EnvironmentUnresolved
		e.SourceSpan = &observation.SourceSpan{Path: "build.sh", Line: 4}
	})}

	return w
}

func builtFrom(subjects ...string) policy.RulePlan {
	return typedRule("T-1", subjects, policy.ObligationRequired, observation.KindBuiltFrom, policy.DirectionFromImage,
		func(s *policy.ObligationSpec) { s.Scope.Environments = []string{"production", "staging"} })
}

// depExpectImageOccurrence expects an occurrence that passes with the fact of the image as its anchor.
func depExpectImageOccurrence(t *testing.T, env string, occ *conformance.OccurrenceResult) {
	t.Helper()

	evalExpectEqual(t, env+": anchor", occ.Anchor, "cbm:p:img")
	evalExpectEqual(t, env+": result", occ.Result, conformance.VerdictPass)

	if occ.Edge == nil {
		t.Errorf("%s: occurrence = %+v", env, occ)
	}
}

func TestFromImageUsesTheImageOfTheSubjectInEveryEnvironment(t *testing.T) {
	t.Parallel()

	w := imageWorld(true)
	w.plans = []policy.RulePlan{builtFrom("billing")}

	out := w.evaluate()
	rule := typedRuleResult(t, &out, "T-1")

	for _, env := range []string{"production", "staging"} {
		o := obligationOf(t, rule, "billing", env)
		if o.Verdict != conformance.VerdictPass {
			t.Fatalf("%s: verdict %s reason %q (%s)", env, o.Verdict, o.Reason, o.Detail)
		}

		// The source file that the sourceRoot binding maps to billing is no anchor: only the image has a result.
		for i := range o.Occurrences {
			depExpectImageOccurrence(t, env, &o.Occurrences[i])
		}
	}
}

func TestFromImageIgnoresTheEnvironmentOfADigestImageEntity(t *testing.T) {
	t.Parallel()

	w := imageWorld(true)
	node := w.nodes["cbm:p:img"]
	node.Environment = "staging" // a digest entity has the environment of the fact that names it.
	w.nodes["cbm:p:img"] = node
	w.plans = []policy.RulePlan{builtFrom("billing")}

	out := w.evaluate()
	if o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production"); o.Verdict != conformance.VerdictPass {
		t.Fatalf("verdict %s (%s)", o.Verdict, o.Detail)
	}
}

func TestSubjectWithoutAnImageBindingIsNotApplicableForFromImage(t *testing.T) {
	t.Parallel()

	t.Run("no binding", func(t *testing.T) {
		t.Parallel()

		w := imageWorld(false)
		w.plans = []policy.RulePlan{builtFrom("billing")}

		out := w.evaluate()
		o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")

		depNotApplicable(t, o, "no image binding names the subject")
	})

	t.Run("a source binding is not an image binding", func(t *testing.T) {
		t.Parallel()

		w := imageWorld(false)
		w.plans = []policy.RulePlan{builtFrom("billing")}
		w.loaded.Bindings = []binding.Binding{{
			ID: "b-src", ArchitectureRef: "billing", Status: binding.StatusActive, Match: []binding.Match{{Kind: binding.MatchSourceRoot, Value: "app/"}},
		}}

		out := w.evaluate()
		if o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production"); o.Verdict != conformance.VerdictNotApplicable {
			t.Fatalf("verdict %s", o.Verdict)
		}
	})

	t.Run("a proposed binding does not count", func(t *testing.T) {
		t.Parallel()

		w := imageWorld(false)
		w.loaded.Bindings = []binding.Binding{{
			ID: "b-img", ArchitectureRef: "billing", Status: binding.StatusProposed,
			Match: []binding.Match{{Kind: binding.MatchImage, Repository: "registry/billing"}},
		}}
		w.plans = []policy.RulePlan{builtFrom("billing")}

		out := w.evaluate()
		if o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production"); o.Verdict != conformance.VerdictNotApplicable {
			t.Fatalf("verdict %s (%s)", o.Verdict, o.Detail)
		}
	})

	t.Run("an active binding whose image is not observed is an absence", func(t *testing.T) {
		t.Parallel()

		w := imageWorld(true)
		w.set.ByObserved = nil
		w.bound = map[string][]string{"cbm:p:src": {"billing"}} // the image entity is not in the graph of the run
		w.plans = []policy.RulePlan{builtFrom("billing")}

		out := w.evaluate()
		if o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production"); o.Verdict != conformance.VerdictViolation {
			t.Fatalf("verdict %s (%s)", o.Verdict, o.Detail)
		}
	})
}

// ---- lineage kinds are environment independent ----

func TestLineageFactOutsideTheEnvironmentSourcesIsEvidence(t *testing.T) {
	t.Parallel()

	// docker/Dockerfile is in no environment: the fact has the environment `unresolved` and is still admissible in production.
	w := imageWorld(true)
	w.plans = []policy.RulePlan{builtFrom("billing")}

	out := w.evaluate()
	o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")

	if o.Verdict != conformance.VerdictPass {
		t.Fatalf("verdict %s reason %q (%s)", o.Verdict, o.Reason, o.Detail)
	}

	if o.Reason == conformance.ReasonEnvironmentUnresolved {
		t.Fatalf("reason %q (%s)", o.Reason, o.Detail)
	}

	// An attribution to another declared environment does not exclude it either.
	w.edges[0].Environment = "staging"

	out = w.evaluate()
	if o = obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production"); o.Verdict != conformance.VerdictPass {
		t.Fatalf("a fact of another environment: verdict %s (%s)", o.Verdict, o.Detail)
	}
}

func TestOtherKindsKeepTheEnvironmentRule(t *testing.T) {
	t.Parallel()

	w := newWorld(observation.KindUsesImage)
	w.add(resource("billing-a", "Deployment", "production", "billing-a"), "billing")
	w.add(observation.ObservedNode{ObservedID: "cbm:p:img", Label: observation.LabelImage, QualifiedName: "registry/billing"})
	w.edges = []*observation.EvidenceEdge{fact(observation.KindUsesImage, "billing-a", "img", func(e *observation.EvidenceEdge) {
		e.Environment = observation.EnvironmentUnresolved
	})}
	w.plans = []policy.RulePlan{typedRule("T-1", []string{"billing"}, policy.ObligationRequired, observation.KindUsesImage, policy.DirectionFromWorkload, nil)}

	out := w.evaluate()
	o := obligationOf(t, typedRuleResult(t, &out, "T-1"), "billing", "production")
	evalExpectEqual(t, "verdict", o.Verdict, conformance.VerdictUnknown)
	evalExpectEqual(t, "reason", o.Reason, conformance.ReasonEnvironmentUnresolved)
}

func TestLineageKindsAreBuiltFromBaseImageAndCopiesFromImage(t *testing.T) {
	t.Parallel()

	for _, kind := range observation.CodeKinds() {
		if observation.IsLineageKind(kind) {
			t.Errorf("%s is a code kind", kind)
		}
	}

	for kind, want := range map[observation.CanonicalEdgeKind]bool{
		observation.KindBuiltFrom: true, observation.KindBaseImage: true, observation.KindCopiesFromImage: true,
		observation.KindUsesImage: false, observation.KindPlacedIn: false, observation.KindDeploysArtifact: false,
	} {
		if got := observation.IsLineageKind(kind); got != want {
			t.Errorf("IsLineageKind(%s) = %v, want %v", kind, got, want)
		}
	}
}
