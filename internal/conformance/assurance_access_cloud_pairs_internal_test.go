package conformance

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/plan"
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

// A binding to the configuration address of a resource whose second instance the plan destroys reads only the live
// instance. Without the opening rule the path is closed, so the rule passes.
func TestCloudFirewallSkipsDestroyedInstances(t *testing.T) {
	t.Parallel()

	scaled := cloudPlan(t, openingRule)
	for i := range scaled.Plan.Resources {
		if r := &scaled.Plan.Resources[i]; r.Address == "aws_db_instance.ledger" {
			r.Address = "aws_db_instance.ledger[0]"
		}
	}

	scaled.Plan.Resources = append(scaled.Plan.Resources, plan.Resource{
		Address: "aws_db_instance.ledger[1]", Mode: "managed", Type: "aws_db_instance", Name: "ledger", Actions: []string{"delete"},
	})

	run := cloudRun(&scaled, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))

	if got := onlyOutcome(t, run, cloudRule("ledger-db")); got.Verdict != VerdictPass {
		t.Errorf("outcome = %s %q %q, want pass from the live instance only", got.Verdict, got.Reason, got.Detail)
	}
}

// An outcome points at the plan that holds the bound resources, not at a declared plan that was not read.
func TestCloudFirewallLocatesTheContributingPlan(t *testing.T) {
	t.Parallel()

	unread := evidence.Plan{
		Coverage: assurance.CoverageFailed,
		Declared: workspace.AssurancePlan{Environment: cloudEnvironment, Tool: "terraform", File: "infra/plans/broken.json"},
	}
	read := cloudPlan(t)

	run := cloudRun(&unread, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))
	run.ev.Plans = append(run.ev.Plans, read)

	if got := onlyOutcome(t, run, cloudRule("ledger-db")); !planLocated(&got) {
		t.Errorf("location = %+v, want %s", got.Location, cloudPlanFile)
	}
}

// The lowest port that no rule names is found from the sorted ranges, with the same answer as a scan of every port,
// also when many ranges overlap.
func TestFirstUncoveredMatchesAScanOfEveryPort(t *testing.T) {
	t.Parallel()

	many := make([][2]int, 0, 1001)
	for range 1000 {
		many = append(many, [2]int{65000, 65535})
	}

	tests := []struct {
		named  []int
		ranges [][2]int
		want   int
		found  bool
	}{
		{nil, nil, 1, true},
		{[]int{1, 2, 4}, nil, 3, true},
		{nil, [][2]int{{0, 0}, {1, 9}, {5, 20}, {22, 30}}, 21, true},
		{[]int{21}, [][2]int{{1, 20}, {22, 30}}, 31, true},
		{nil, append(many, [2]int{1, 65535}), 0, false},
	}

	for _, test := range tests {
		set := &portSet{seen: map[portKey]bool{}, named: map[int]bool{}, ranges: test.ranges}
		for _, number := range test.named {
			set.named[number] = true
		}

		got, found := set.firstUncovered()
		if !found {
			got = 0
		}

		if [2]any{got, found} != [2]any{test.want, test.found} {
			t.Errorf("firstUncovered(%v, %d ranges) = %d %t, want %d %t", test.named, len(test.ranges), got, found, test.want, test.found)
		}

		if scan := firstUncoveredByScan(set); scan != got {
			t.Errorf("firstUncovered = %d, a scan gives %d", got, scan)
		}
	}
}

// firstUncoveredByScan is the reference answer: the lowest port that no named port or range holds, 0 when none.
func firstUncoveredByScan(set *portSet) int {
	for number := 1; number <= maxPortNumber; number++ {
		inRange := slices.ContainsFunc(set.ranges, func(r [2]int) bool { return number >= r[0] && number <= r[1] })
		if !set.named[number] && !inRange {
			return number
		}
	}

	return 0
}
