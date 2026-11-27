package conformance

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/workspace"
)

// Tests of how cloud-firewall reads the plans of an environment: missing evidence, several plans, destroyed
// instances, and the plan file that an outcome points at.

// An address that two plans of the environment hold is not read last-wins.
func TestCloudFirewallAddressInTwoPlansIsUnknown(t *testing.T) {
	t.Parallel()

	first, second := cloudPlan(t), cloudPlan(t)
	second.Declared.File = "infra/plans/other.json"

	run := cloudRun(&first, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))
	run.ev.Plans = append(run.ev.Plans, second)

	got := onlyOutcome(t, run, cloudRule("ledger-db"))
	if got.Reason != ReasonCoverageIncomplete || !strings.Contains(got.Detail, "is in 2 plans of environment production") {
		t.Errorf("outcome = %s %q %q, want coverage-incomplete for an address in two plans", got.Verdict, got.Reason, got.Detail)
	}
}

// A bound address that the plan does not hold, and a plan that was not read, are answered by
// cloud-firewall, not sent to network-policy.
func TestCloudFirewallReportsMissingPlanEvidence(t *testing.T) {
	t.Parallel()

	withRule := cloudPlan(t)
	typo := cloudRun(&withRule, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))
	typo.bindings[1] = infraBinding("ledger-db", "aws_db_instance.ledgr")

	got := onlyOutcome(t, typo, cloudRule("ledger-db"))

	named := got.Reason == ReasonRenderedEvidenceMissing && strings.Contains(got.Detail, "aws_db_instance.ledgr")
	if !named || !planLocated(&got) {
		t.Errorf("address not in the plan = %q %q at %+v", got.Reason, got.Detail, got.Location)
	}

	unread := evidence.Plan{Coverage: assurance.CoverageFailed, Declared: withRule.Declared}

	got = onlyOutcome(t, cloudRun(&unread, cloudEvaluator()), cloudRule("ledger-db"))
	if got.Reason != ReasonRenderedEvidenceMissing || !strings.Contains(got.Detail, "has no usable plan") {
		t.Errorf("unread plan = %q %q", got.Reason, got.Detail)
	}
}

// An address that two plans hold blocks the environment only when the evaluator reads that type.
func TestCloudFirewallSharedAddressOfAnotherProviderDoesNotBlock(t *testing.T) {
	t.Parallel()

	suffix := planned("random_id.suffix", map[string]any{"hex": "beef"})

	first := cloudPlan(t)
	first.Plan.Resources = append(first.Plan.Resources, suffix)

	second := evidence.Plan{
		Plan: &plan.Plan{
			Digest: "sha256:" + strings.Repeat("b", 64), Coverage: assurance.CoverageComplete, Resources: []plan.Resource{suffix},
		},
		Coverage: assurance.CoverageComplete,
		Declared: workspace.AssurancePlan{Environment: cloudEnvironment, Tool: "terraform", File: "infra/plans/other.json"},
	}

	run := cloudRun(&first, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))
	run.ev.Plans = append(run.ev.Plans, second)

	if got := onlyOutcome(t, run, cloudRule("ledger-db")); got.Verdict != VerdictViolation {
		t.Errorf("outcome = %s %q %q, want the violation of the first plan", got.Verdict, got.Reason, got.Detail)
	}
}

// An address of a provider resource that two plans hold makes only the pairs that bind it
// `unknown`. Here the second plan holds another `aws_security_group.gateway`; the pair binds the load balancer and
// the database of the first plan, whose configuration references resolve inside that plan, so the violation stands.
func TestCloudFirewallSharedGroupAddressDoesNotBlockOtherPairs(t *testing.T) {
	t.Parallel()

	second := evidence.Plan{
		Plan: &plan.Plan{
			Digest: "sha256:" + strings.Repeat("c", 64), Coverage: assurance.CoverageComplete,
			Resources: []plan.Resource{planned("aws_security_group.gateway", map[string]any{
				"id": "sg-gateway-other-state", "ingress": []any{}, "egress": []any{},
			})},
		},
		Coverage: assurance.CoverageComplete,
		Declared: workspace.AssurancePlan{Environment: cloudEnvironment, Tool: "terraform", File: "infra/plans/edge.json"},
	}

	first := cloudPlan(t)
	run := cloudRun(&first, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))
	run.ev.Plans = append(run.ev.Plans, second)

	got := onlyOutcome(t, run, cloudRule("ledger-db"))
	if got.Verdict != VerdictViolation || !strings.Contains(got.Detail, "aws_vpc_security_group_ingress_rule.ledger_from_gateway") {
		t.Errorf("outcome = %s %q %q, want the violation of the first plan", got.Verdict, got.Reason, got.Detail)
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
