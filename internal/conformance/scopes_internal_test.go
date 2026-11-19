package conformance

import (
	"errors"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestFinishCoverageReportsMissingEndpointsWithADiagnostic(t *testing.T) {
	t.Parallel()

	c := &checker{}
	cov := NewRunCoverage()

	c.finishCoverage(&cov, &EvaluateOutput{Skipped: SkipCounts{EndpointMissing: 3, Unbound: 2}})

	if cov.Skipped.EndpointMissing != 3 || cov.SkipReasons[SkipEndpointMissing] == "" {
		t.Errorf("coverage = %+v", cov)
	}

	if cov.SkipReasons[SkipUnbound] == "" {
		t.Errorf("coverage = %+v", cov)
	}

	if len(c.diags) != 1 {
		t.Fatalf("diagnostics = %+v", c.diags)
	}

	if c.diags[0].Code != diag.CON033 || c.diags[0].Message == "" {
		t.Fatalf("diagnostics = %+v", c.diags)
	}

	clean := &checker{}
	clean.finishCoverage(&cov, &EvaluateOutput{})

	if len(clean.diags) != 0 {
		t.Errorf("a run without missing endpoints has diagnostics: %+v", clean.diags)
	}
}

func TestScopeFailureOfOnlyTakesQueryLevelErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err    error
		name   string
		scoped bool
	}{
		{diag.Fail(diag.OBS001, diag.Detail("boom")), "cbm error", true},
		{diag.Fail(diag.OBS010, diag.Detail("rows")), "row count", true},
		{diag.Fail(diag.OBS008, diag.Detail("generation")), "graph changed", false},
		{diag.Fail(diag.OBS003, diag.Params{"executable": "cbm"}), "no executable", false},
		{diag.Fail(diag.OBS004, diag.Params{"version": "1"}), "bad version", false},
		{diag.Fail(diag.OBS002, diag.Detail("x")), "not indexed", false},
		{errors.New("crash"), "not diagnostic", false},
	}

	for _, tc := range cases {
		f, ok := scopeFailureOf("pay", tc.err)
		if ok != tc.scoped {
			t.Errorf("%s: scoped = %v failure %+v", tc.name, ok, f)
		}

		if ok && f.Project != "pay" {
			t.Errorf("%s: scoped = %v failure %+v", tc.name, ok, f)
		}

		if ok && f.Reason == "" {
			t.Errorf("%s: scoped = %v failure %+v", tc.name, ok, f)
		}
	}
}

func TestTraversalStopsAtItsLimit(t *testing.T) {
	t.Parallel()

	tr := newTraversal(2)
	first, second := tr.visit(), tr.visit()

	if !first || !second {
		t.Fatal("two visits within the limit")
	}

	if tr.stopped() {
		t.Fatal("two visits within the limit")
	}

	if tr.visit() {
		t.Fatal("the third visit passes the limit")
	}

	if !tr.stopped() {
		t.Fatal("the third visit passes the limit")
	}
}

func TestTraversalBreachNamesTheLimitAndTheVisits(t *testing.T) {
	t.Parallel()

	tr := newTraversal(2)
	for range 3 {
		tr.visit()
	}

	b := tr.breach()
	if b.Configured != 2 || b.Observed != 3 {
		t.Errorf("breach = %+v", b)
	}

	if b.Limit != LimitTraversalMaxVisit {
		t.Errorf("breach = %+v", b)
	}
}

func TestTraversalWithoutALimitNeverStops(t *testing.T) {
	t.Parallel()

	var none *traversal
	if !none.visit() {
		t.Error("a traversal without a limit stops")
	}

	if none.stopped() {
		t.Error("a traversal without a limit stops")
	}

	if !newTraversal(0).visit() {
		t.Error("a traversal without a limit stops")
	}
}

func TestSccsOnACycleAreCompleteWithinTheLimitAndIncompleteOverIt(t *testing.T) {
	t.Parallel()

	adj := map[string][]string{"a": {"b"}, "b": {"a"}}

	if comps := sccs(adj, newTraversal(10)); len(comps) != 1 {
		t.Errorf("components = %v", comps)
	}

	limited := newTraversal(1)
	if comps := sccs(adj, limited); len(comps) != 0 || !limited.stopped() {
		t.Errorf("components = %v incomplete = %v", comps, limited.stopped())
	}
}

func TestWithoutFailedDependentsKeepsTheRequiredBindingsOfOtherProjects(t *testing.T) {
	t.Parallel()

	ws := &workspace.Workspace{Observation: workspace.Observation{Projects: []workspace.Project{{Name: "demo"}, {Name: "pay"}}}}
	all := []string{"demo", "pay"}
	loaded := &binding.Loaded{Bindings: []binding.Binding{
		{ID: "b-pay", ArchitectureRef: "pay-service", Required: true, Match: []binding.Match{{Kind: binding.MatchSourceRoot, Value: "@pay/src/**"}}},
		{ID: "b-demo", ArchitectureRef: "billing", Required: true, Match: []binding.Match{{Kind: binding.MatchSourceRoot, Value: "services/billing/**"}}},
		{ID: "b-name", ArchitectureRef: "other", Required: true, Match: []binding.Match{{Kind: binding.MatchQualifiedName, Value: "x.y"}}},
		{ID: "b-opt", ArchitectureRef: "pay-service", Match: []binding.Match{{Kind: binding.MatchSourceRoot, Value: "@pay/lib/**"}}},
	}}

	got := withoutFailedDependents(loaded, ws, all, []observation.ScopeFailure{{Project: "pay", Reason: "x"}})

	ids := make([]string, len(got.Bindings))
	for i := range got.Bindings {
		ids[i] = got.Bindings[i].ID
	}

	if !slices.Equal(ids, []string{"b-demo", "b-opt"}) {
		t.Errorf("kept bindings = %v", ids)
	}

	if same := withoutFailedDependents(loaded, ws, all, nil); len(same.Bindings) != 4 {
		t.Errorf("no failure keeps all bindings: %d", len(same.Bindings))
	}

	projects := nodeProjects(loaded, ws, all)
	wantProjects := map[string][]string{"pay-service": {"pay"}, "billing": {"demo"}, "other": all}

	for node, want := range wantProjects {
		if !slices.Equal(projects[node], want) {
			t.Errorf("node projects = %v", projects)
		}
	}
}

// A plan with a `placed-in` relation fetches the occurrence and selector facts, because the workload nodes are
// provider nodes only as endpoints of these facts, also when the workspace declares no environment.
func TestRequiredNativeKindsForAPlacementPlan(t *testing.T) {
	t.Parallel()

	plain := requiredNativeKinds(&workspace.Workspace{}, nil)
	if slices.Contains(plain, "USAGE") {
		t.Fatalf("a run without environments or placement plans fetches USAGE: %v", plain)
	}

	plans := []policy.RulePlan{{Obligation: &policy.ObligationSpec{Relation: policy.RelationSpec{Kind: observation.KindPlacedIn}}}}
	if got := requiredNativeKinds(&workspace.Workspace{}, plans); !slices.Contains(got, "USAGE") || !slices.Contains(got, "INFRA_MAPS") {
		t.Errorf("native kinds of a placement plan = %v", got)
	}

	traversal := []policy.RulePlan{{Traversal: policy.Traversal{EdgeKinds: []observation.CanonicalEdgeKind{observation.KindPlacedIn}}}}
	if got := requiredNativeKinds(&workspace.Workspace{}, traversal); !slices.Contains(got, "USAGE") {
		t.Errorf("native kinds of a placed-in traversal = %v", got)
	}
}

// CO-CON-032 names the predicate, the status, the scope, and the detail of a record that is not complete: for the
// deployment workload set of an environment with a Terraform source, the detail is the unsupported source.
func TestPredicateReasonsNameTheUnsupportedDeploymentSource(t *testing.T) {
	t.Parallel()

	profile, ok := observation.ProfileFor("0.12.1", observation.AdapterSchema)
	if !ok {
		t.Fatal("no profile for CBM 0.12.1")
	}

	index := observation.IndexRecord{Project: "app", GraphGeneration: "g"}
	records := observation.ComputePredicateCoverage(&observation.PredicateInput{
		Families: observation.DeclaredFamilies(profile),
		Projects: []observation.ProjectScope{{Name: "app", Index: &index}},
		Environments: []observation.EnvironmentScope{{
			ID: "production", Projects: []string{"app"}, CrossChecked: true, UnsupportedSources: []string{"terraform/main.tf"},
		}},
	})

	var environment []observation.PredicateCoverage

	for _, rec := range records {
		if rec.Predicate == observation.FamilyDeploymentWorkloadSet && rec.Scope.Environment == "production" {
			environment = append(environment, rec)
		}
	}

	reasons := predicateReasons(environment, nil)
	if len(reasons) != 1 {
		t.Fatalf("reasons = %+v", reasons)
	}

	const want = "predicate coverage not complete: deployment-workload-set is unknown in production: unsupported deployment source: terraform/main.tf"
	if reasons[0].Code != diag.CON032 || reasons[0].Message != want {
		t.Fatalf("reasons = %+v", reasons)
	}
}
