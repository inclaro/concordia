package conformance

import (
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/workspace"
)

// workloadBinding binds a CALM element to a rendered Deployment of the environment.
func workloadBinding(calmID, namespace, name string) binding.Binding {
	return binding.Binding{ID: calmID + "-workload", ArchitectureRef: calmID, Match: []binding.Match{{
		Kind: binding.MatchWorkload, Environment: cloudEnvironment, ResourceKind: "Deployment", Namespace: namespace, Name: name,
	}}}
}

// renderedDeployment is a rendered Deployment.
func renderedDeployment(namespace, name string) render.Resource {
	return render.Resource{ID: render.ResourceID{Kind: "Deployment", Namespace: namespace, Name: name}, Workload: &render.Workload{}}
}

// A source that is bound to a planned resource and to a workload, with a target that is a workload, keeps its pair on
// network-policy: the path stays inside the cluster. Without a network-policy evaluator the answer says so, and it is
// not the binding gap of a mixed pair.
func TestCloudFirewallDualBoundSourceKeepsNetworkPolicy(t *testing.T) {
	t.Parallel()

	withRule := cloudPlan(t)
	run := cloudRun(&withRule, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))
	run.bindings = append(run.bindings,
		workloadBinding("public-load-balancer", "edge", "gateway"), workloadBinding("fraud-cache", "fraud", "fraud-cache"))
	run.ev.Renders = []*render.Result{{
		Environment: cloudEnvironment, Tool: workspace.RenderToolPlain, Path: "deploy", Coverage: assurance.CoverageComplete,
		Resources: []render.Resource{renderedDeployment("edge", "gateway"), renderedDeployment("fraud", "fraud-cache")},
	}}

	got := onlyOutcome(t, run, cloudRule("fraud-cache"))

	want := outcomeKey{VerdictUnknown, ReasonNoEvaluatorAvailable, evaluatorScope{kind: workspace.EvaluatorNetworkPolicy, environment: cloudEnvironment}.noEvaluatorDetail()}
	if key := (outcomeKey{got.Verdict, got.Reason, got.Detail}); key != want {
		t.Errorf("outcome = %+v, want %+v", key, want)
	}
}

// A pair of a planned resource and a rendered workload has no evaluator; a cloud pair without a cloud-firewall
// declaration has none either.
func TestCloudFirewallPairGates(t *testing.T) {
	t.Parallel()

	withRule := cloudPlan(t)

	mixed := cloudRun(&withRule, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))
	mixed.bindings = append(mixed.bindings, binding.Binding{ID: "fraud-cache", ArchitectureRef: "fraud-cache", Match: []binding.Match{{
		Kind: binding.MatchWorkload, Environment: cloudEnvironment, ResourceKind: "Deployment", Namespace: "fraud", Name: "fraud-cache",
	}}})
	mixed.ev.Renders = []*render.Result{{
		Environment: cloudEnvironment, Tool: workspace.RenderToolPlain, Path: "deploy", Coverage: assurance.CoverageComplete,
		Resources: []render.Resource{{ID: render.ResourceID{Kind: "Deployment", Namespace: "fraud", Name: "fraud-cache"}, Workload: &render.Workload{}}},
	}}

	got := onlyOutcome(t, mixed, cloudRule("fraud-cache"))
	if key := (outcomeKey{got.Verdict, got.Reason, got.Detail}); key != (outcomeKey{VerdictUnknown, ReasonBindingGap, mixedPairDetail}) {
		t.Errorf("mixed pair = %+v", key)
	}

	unbound := onlyOutcome(t, mixed, cloudRule("nowhere"))
	if unbound.Reason != ReasonBindingGap || !planLocated(&unbound) {
		t.Errorf("unbound end of a cloud pair = %q at %+v, want binding-gap at %s", unbound.Reason, unbound.Location, cloudPlanFile)
	}

	undeclared := cloudRun(&withRule, workspace.AssuranceEvaluator{Environment: cloudEnvironment, Kinds: []string{workspace.EvaluatorNetworkPolicy}})

	got = onlyOutcome(t, undeclared, cloudRule("ledger-db"))
	if key := (outcomeKey{got.Verdict, got.Reason, got.Detail}); key != (outcomeKey{
		VerdictUnknown, ReasonNoEvaluatorAvailable,
		evaluatorScope{kind: workspace.EvaluatorCloudFirewall, environment: cloudEnvironment}.noEvaluatorDetail(),
	}) {
		t.Errorf("undeclared evaluator = %+v", key)
	}
}
