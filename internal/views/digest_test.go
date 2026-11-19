package views_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/views"
)

// digestCase is the input of one view, with everything that the digest reads.
type digestCase struct {
	view      views.Definition
	model     *intent.ArchitectureModel
	placement *views.PlacementProjection
	bindings  []binding.Binding
	policies  []policy.RulePlan
	fns       []fn
	edges     []call
}

func newDigestCase(view *views.Definition) *digestCase {
	fns, edges := codeGraph()

	return &digestCase{
		view: *view, model: projectionModel("HTTPS"), bindings: projectionBindings(), fns: fns, edges: edges,
		policies: []policy.RulePlan{
			{RuleID: "CO-BND-001", Severity: policy.SeverityError, Subjects: []string{"web-ui"}, Targets: []string{"payments-db"}},
			{RuleID: "CO-RPT-001", Severity: policy.SeverityError, Subjects: []string{"reporting-service"}, Targets: []string{"reporting-service"}},
		},
	}
}

func (c *digestCase) digests(t *testing.T) views.Digests {
	t.Helper()

	in := codeEvidence(t, c.fns, c.edges, c.bindings)
	in.Model = c.model

	got, err := views.ComputeDigests(&views.DigestInput{
		View:               &c.view,
		Model:              c.model,
		ModelProjection:    views.BuildModelProjection(&c.view, &views.ModelInput{Model: c.model, Bindings: c.bindings, Placement: c.placement}),
		ObservedProjection: views.ObserveProjection(&c.view, &views.ObserveInput{Reconcile: in, Bindings: c.bindings}),
		Bindings:           c.bindings,
		Controls:           c.model.Controls,
		Policies:           c.policies,
	})
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func mustEqual(t *testing.T, what, a, b string) {
	t.Helper()

	if a != b {
		t.Errorf("%s changed: %s -> %s", what, a, b)
	}
}

func mustDiffer(t *testing.T, what, a, b string) {
	t.Helper()

	if a == b {
		t.Errorf("%s did not change: %s", what, a)
	}
}

func TestSemanticDigestShape(t *testing.T) {
	t.Parallel()

	got := newDigestCase(ptr(containerView("payments-container"))).digests(t)

	for name, d := range map[string]string{
		"semantic": got.Semantic, "definition": got.Definition, "model": got.Model, "observed": got.Observed,
		"binding": got.Binding, "control": got.Control, "policy": got.Policy,
	} {
		if len(d) != len("sha256:")+64 {
			t.Errorf("%s digest = %q", name, d)
		}
	}

	if got.Normalization == "" {
		t.Error("the normalization version is not recorded")
	}

	if views.DigestContract != "concordia-view-semantic/1" {
		t.Errorf("digest contract = %q", views.DigestContract)
	}

	mustEqual(t, "the semantic digest of identical inputs", got.Semantic, newDigestCase(ptr(containerView("payments-container"))).digests(t).Semantic)
}

// A15: an internal refactor changes no dependency between elements. A function body is not in the graph that the
// projection reads; the refactor shows in the graph as new functions and calls inside one container, and as new
// calls between two functions of one container.
func TestSemanticDigestA15FunctionBodyChangeLeavesDigestEqual(t *testing.T) {
	t.Parallel()

	prod := placementOf("prod", deployment.Placement{Node: "orders-service", Container: "cluster-a", Environment: "prod", Workload: "cbm:p:orders-1"})

	for _, view := range []views.Definition{containerView("c"), contextView("s"), deploymentView("d", "prod")} {
		before, after := newDigestCase(&view), newDigestCase(&view)
		before.placement, after.placement = &prod, &prod

		after.fns = append(after.fns, fn{"ord3", "services/orders/c.go"})
		after.edges = append(after.edges, calls("ord2", "ord3"), calls("ord3", "ord1"), calls("ord2", "ord1"))

		mustEqual(t, "the semantic digest of the "+string(view.Kind)+" view after an internal refactor",
			before.digests(t).Semantic, after.digests(t).Semantic)
	}
}

func TestSemanticDigestLayoutAndColorEditChangesTheArtifactOnly(t *testing.T) {
	t.Parallel()

	c := newDigestCase(ptr(containerView("payments-container")))
	view := c.view
	p := views.BuildModelProjection(&view, &views.ModelInput{Model: c.model, Bindings: c.bindings})

	text, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// The artifact of a view carries layout and colors; the semantic digest has no artifact bytes as input.
	artifact := func(layout string) string {
		return digest.OfBytes(append(slices.Clone(text), layout...))
	}

	before := c.digests(t).Semantic

	mustDiffer(t, "the artifact digest after a layout and color edit", artifact("autoLayout lr\n"), artifact("autoLayout tb\nstyles { color #ff0000 }\n"))
	mustEqual(t, "the semantic digest after a layout and color edit", before, newDigestCase(ptr(containerView("payments-container"))).digests(t).Semantic)
}

func TestSemanticDigestProtocolChange(t *testing.T) {
	t.Parallel()

	https := newDigestCase(ptr(containerView("payments-container")))
	grpc := newDigestCase(ptr(containerView("payments-container")))
	grpc.model = projectionModel("gRPC")

	b, a := https.digests(t), grpc.digests(t)

	mustDiffer(t, "the semantic digest after HTTPS -> gRPC", b.Semantic, a.Semantic)
	mustDiffer(t, "the model projection digest after HTTPS -> gRPC", b.Model, a.Model)
	mustEqual(t, "the observed projection digest (protocol is declared)", b.Observed, a.Observed)
}

func TestSemanticDigestUnrelatedPolicyEditLeavesDigestEqual(t *testing.T) {
	t.Parallel()

	base := newDigestCase(ptr(containerView("payments-container")))
	unrelated := newDigestCase(ptr(containerView("payments-container")))
	unrelated.policies[1].Severity = policy.SeverityWarning
	unrelated.policies[1].Subjects = []string{"reporting-service", "reporting-service"}
	unrelated.policies = append(unrelated.policies, policy.RulePlan{RuleID: "CO-NEW-001", Subjects: []string{"reporting-service"}})

	b, a := base.digests(t), unrelated.digests(t)

	mustEqual(t, "the policy digest after an edit of a rule that selects no depicted element", b.Policy, a.Policy)
	mustEqual(t, "the semantic digest after an unrelated policy edit", b.Semantic, a.Semantic)

	related := newDigestCase(ptr(containerView("payments-container")))
	related.policies[0].Severity = policy.SeverityWarning

	mustDiffer(t, "the semantic digest after an edit of a rule that selects a depicted element", b.Semantic, related.digests(t).Semantic)
}

func TestSemanticDigestControlAndBindingSlices(t *testing.T) {
	t.Parallel()

	base := newDigestCase(ptr(containerView("payments-container")))
	unrelatedControl := newDigestCase(ptr(containerView("payments-container")))
	unrelatedControl.model = projectionModel("HTTPS")
	unrelatedControl.model.Controls[1].ControlID = "reporting-retention-v2"

	mustEqual(t, "the semantic digest after a control of a node outside the view", base.digests(t).Semantic, unrelatedControl.digests(t).Semantic)

	related := newDigestCase(ptr(containerView("payments-container")))
	related.model = projectionModel("HTTPS")
	related.model.Controls[0].ControlID = "orders-authz-v2"

	mustDiffer(t, "the semantic digest after a control of a depicted node", base.digests(t).Semantic, related.digests(t).Semantic)

	// A binding of a node outside the view, and a move of a declaration, change nothing.
	other := newDigestCase(ptr(containerView("payments-container")))
	other.bindings = append(slices.Clone(other.bindings), rootBinding("b-report", "reporting-service", "services/reporting/**", true))
	other.bindings[0].Source = "somewhere/else.yaml"
	other.bindings[0].ID = "renamed-id"

	mustEqual(t, "the binding digest after an unrelated binding and a moved declaration", base.digests(t).Binding, other.digests(t).Binding)
}

func TestSemanticDigestOwnerAndSeverityAreNotArchitecture(t *testing.T) {
	t.Parallel()

	base := newDigestCase(ptr(containerView("payments-container")))
	changed := newDigestCase(ptr(containerView("payments-container")))
	changed.view.Owner = "team-other"
	changed.view.Comparison = &views.Comparison{Severity: "warning"}
	changed.view.Projection = &views.ProjectionRules{SelfEdges: "omit", ParallelEdges: "merge", Merge: "binding"}

	mustEqual(t, "the semantic digest after an owner, severity and explicit-default edit", base.digests(t).Semantic, changed.digests(t).Semantic)

	scope := newDigestCase(ptr(containerView("payments-container")))
	scope.view.Projection = &views.ProjectionRules{ParallelEdges: "separate"}

	mustDiffer(t, "the semantic digest after a projection rule edit", base.digests(t).Semantic, scope.digests(t).Semantic)
}

// A new observed relationship from a container to a database appears in the view scope.
func TestSemanticDigestNewDatabaseDependency(t *testing.T) {
	t.Parallel()

	base := newDigestCase(ptr(containerView("payments-container")))
	withDependency := newDigestCase(ptr(containerView("payments-container")))
	withDependency.edges = append(withDependency.edges, calls("web1", "aud1"))

	b, a := base.digests(t), withDependency.digests(t)

	mustDiffer(t, "the observed projection digest after a new dependency on the audit database", b.Observed, a.Observed)
	mustDiffer(t, "the semantic digest after a new dependency on the audit database", b.Semantic, a.Semantic)
	mustEqual(t, "the model projection digest (the dependency is observed only)", b.Model, a.Model)
}

func TestSemanticDigestNewWorkloadIdentity(t *testing.T) {
	t.Parallel()

	one := placementOf("prod", deployment.Placement{Node: "orders-service", Container: "cluster-a", Environment: "prod", Workload: "cbm:p:orders-1"})
	two := placementOf("prod",
		deployment.Placement{Node: "orders-service", Container: "cluster-a", Environment: "prod", Workload: "cbm:p:orders-1"},
		deployment.Placement{Node: "orders-service", Container: "cluster-a", Environment: "prod", Workload: "cbm:p:orders-2"},
	)

	b, a := newDigestCase(ptr(deploymentView("payments-prod", "prod"))), newDigestCase(ptr(deploymentView("payments-prod", "prod")))
	b.placement, a.placement = &one, &two

	mustDiffer(t, "the semantic digest after a new workload identity", b.digests(t).Semantic, a.digests(t).Semantic)
}

func TestSemanticDigestEnvironmentMove(t *testing.T) {
	t.Parallel()

	record := func(environment string) deployment.Placement {
		return deployment.Placement{Node: "orders-service", Container: "cluster-a", Environment: environment, Workload: "cbm:p:orders-1"}
	}

	// The workload is in prod, then it moves to staging; both views see it, so the records are of the run.
	for _, environment := range []string{"prod", "staging"} {
		before := placementOf(environment, record("prod"))
		after := placementOf(environment, record("staging"))

		b, a := newDigestCase(ptr(deploymentView("payments-"+environment, environment))), newDigestCase(ptr(deploymentView("payments-"+environment, environment)))
		b.placement, a.placement = &before, &after

		mustDiffer(t, "the semantic digest of the "+environment+" view after the move", b.digests(t).Semantic, a.digests(t).Semantic)
	}
}

// The implementation of an authorization check changes: the code gives other functions and calls inside the
// container that owns it, and no element, relationship, protocol or placement changes.
func TestSemanticDigestAuthorizationCodeChangesTopologyDoesNot(t *testing.T) {
	t.Parallel()

	prod := placementOf("prod", deployment.Placement{Node: "orders-service", Container: "cluster-a", Environment: "prod", Workload: "cbm:p:orders-1"})

	for _, view := range []views.Definition{containerView("c"), contextView("s"), deploymentView("d", "prod")} {
		before, after := newDigestCase(&view), newDigestCase(&view)
		before.placement, after.placement = &prod, &prod

		after.fns = append(after.fns, fn{"authz1", "services/orders/authz.go"}, fn{"authz2", "services/orders/handler/authz.go"})
		after.edges = append(after.edges, calls("hdl1", "authz1"), calls("authz1", "authz2"), calls("authz2", "ord2"))

		mustEqual(t, "the semantic digest of the "+string(view.Kind)+" view after an authorization change",
			before.digests(t).Semantic, after.digests(t).Semantic)
	}
}

func TestSemanticDigestBindingRemovedIsNotKeptCurrent(t *testing.T) {
	t.Parallel()

	base := newDigestCase(ptr(containerView("payments-container")))
	removed := newDigestCase(ptr(containerView("payments-container")))
	removed.bindings = slices.DeleteFunc(slices.Clone(removed.bindings), func(b binding.Binding) bool { return b.ArchitectureRef == "payments-db" })

	b, a := base.digests(t), removed.digests(t)

	mustDiffer(t, "the binding digest after a binding is removed", b.Binding, a.Binding)
	mustDiffer(t, "the semantic digest after a binding is removed", b.Semantic, a.Semantic)
}

func TestSemanticDigestMissingObservedProjectionIsExplicit(t *testing.T) {
	t.Parallel()

	c := newDigestCase(ptr(containerView("payments-container")))

	got, err := views.ComputeDigests(&views.DigestInput{
		View: &c.view, Model: c.model,
		ModelProjection: views.BuildModelProjection(&c.view, &views.ModelInput{Model: c.model, Bindings: c.bindings}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Observed != "" {
		t.Errorf("observed digest = %q, want the explicit missing value (empty), not a digest of nothing", got.Observed)
	}

	mustDiffer(t, "the semantic digest without and with the observed projection", got.Semantic, c.digests(t).Semantic)
}

func ptr[T any](v T) *T { return &v }
