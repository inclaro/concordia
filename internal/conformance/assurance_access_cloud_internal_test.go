package conformance

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	cloudEnvironment = "production"
	// openingRule is the rule of the AWS fixture that allows the gateway group to reach the database on 5432.
	openingRule = "aws_vpc_security_group_ingress_rule.ledger_from_gateway"
	// cloudPlanFile is the declared plan file of the environment.
	cloudPlanFile = "infra/plans/production.json"
)

// cloudPlan reads the AWS fixture as the plan of the environment, without the resources at the addresses.
func cloudPlan(t *testing.T, without ...string) evidence.Plan {
	t.Helper()

	data, err := os.ReadFile("../../test/fixtures/assurance/plans/cloudfw-aws.json")
	if err != nil {
		t.Fatal(err)
	}

	read, diags := plan.Read(data, cloudPlanFile)
	if read == nil {
		t.Fatalf("plan not read: %v", diags)
	}

	read.Resources = slices.DeleteFunc(read.Resources, func(r plan.Resource) bool { return slices.Contains(without, r.Address) })

	return evidence.Plan{
		Plan: read, Coverage: read.Coverage,
		Declared: workspace.AssurancePlan{Environment: cloudEnvironment, Tool: "terraform", File: cloudPlanFile},
		Binding: plan.Binding{Admission: assurance.NewAdmission(assurance.IntegrityNotVerified,
			[]assurance.Detail{assurance.DetailNotAuthenticated})},
	}
}

// infraBinding binds a CALM element to a planned resource of the environment.
func infraBinding(calmID, address string) binding.Binding {
	return binding.Binding{ID: calmID, ArchitectureRef: calmID, Match: []binding.Match{
		{Kind: binding.MatchInfraResource, Environment: cloudEnvironment, Address: address},
	}}
}

// cloudRun is a run with the plan, the bindings of the load balancer and the database, and the evaluators.
func cloudRun(p *evidence.Plan, evaluators ...workspace.AssuranceEvaluator) *assuranceRun {
	return &assuranceRun{
		cfg: &workspace.Assurance{Evaluators: evaluators},
		ev:  &evidence.Evidence{Plans: []evidence.Plan{*p}},
		bindings: []binding.Binding{
			infraBinding("public-load-balancer", "aws_lb.public"), infraBinding("ledger-db", "aws_db_instance.ledger"),
		},
		report: newAssuranceReport(),
	}
}

// cloudEvaluator enables cloud-firewall for the environment with the assumptions.
func cloudEvaluator(assumes ...string) workspace.AssuranceEvaluator {
	return workspace.AssuranceEvaluator{Environment: cloudEnvironment, Kinds: []string{workspace.EvaluatorCloudFirewall}, Assumes: assumes}
}

// cloudRule expects no network access from the load balancer to the target on 5432.
func cloudRule(target string) *policy.RulePlan {
	return &policy.RulePlan{
		RuleID: "PAY-NET-002", Subjects: []string{"public-load-balancer"}, Targets: []string{target},
		Predicate: policy.Predicate{Family: policy.FamilyEffectiveAccess, EffectiveAccess: &policy.EffectiveAccess{
			Dimension: policy.DimensionNetwork, Expect: policy.ExpectDenied, Ports: []int{5432},
		}},
	}
}

// onlyOutcome gives the one outcome of a network rule in the environment.
func onlyOutcome(t *testing.T, run *assuranceRun, rule *policy.RulePlan) ExternalOutcome {
	t.Helper()

	outcomes := run.accessIn(rule, evaluatorScope{kind: workspace.EvaluatorNetworkPolicy, environment: cloudEnvironment})
	if len(outcomes) != 1 {
		t.Fatalf("%d outcomes, want 1: %+v", len(outcomes), outcomes)
	}

	return outcomes[0]
}

// outcomeKey is what a test compares of an outcome: the verdict, the reason, and one text (occurrence or detail).
type outcomeKey struct {
	verdict RuleVerdict
	reason  UnknownReason
	text    string
}

// A pair bound to planned resources is answered by cloud-firewall from the plan, without rendered resources: the
// opening rule is a violation; without it the closed result needs the declared assumption.
func TestCloudFirewallAnswersPlannedPairs(t *testing.T) {
	t.Parallel()

	complete := workspace.AssumeFirewallRulesInPlan
	withRule, withoutRule := cloudPlan(t), cloudPlan(t, openingRule)

	tests := []struct {
		plan       *evidence.Plan
		name       string
		want       RuleVerdict
		wantReason UnknownReason
		assumes    []string
	}{
		{&withRule, "the rule opens 5432", VerdictViolation, "", []string{complete}},
		{&withoutRule, "closed, assumption not declared", VerdictUnknown, ReasonEnforcementNotDeclared, nil},
		{&withoutRule, "closed, assumption declared", VerdictPass, "", []string{complete}},
	}

	for _, test := range tests {
		run := cloudRun(test.plan, cloudEvaluator(test.assumes...))

		got := onlyOutcome(t, run, cloudRule("ledger-db"))
		if key := (outcomeKey{got.Verdict, got.Reason, got.Occurrence}); key != (outcomeKey{test.want, test.wantReason, "tcp/5432"}) {
			t.Errorf("%s: outcome = %+v (%s), want %s %q tcp/5432", test.name, key, got.Detail, test.want, test.wantReason)
		}

		if !planLocated(&got) {
			t.Errorf("%s: location = %+v, want the plan file %s", test.name, got.Location, cloudPlanFile)
		}
	}
}

// planLocated says whether the outcome points at the plan file of the environment.
func planLocated(outcome *ExternalOutcome) bool {
	return outcome.Location != nil && outcome.Location.Path == cloudPlanFile
}

// The evaluation entry of a cloud query names the opening rule, the plan digest, and the plan integrity.
func TestCloudFirewallRecordsTheEvaluation(t *testing.T) {
	t.Parallel()

	withRule := cloudPlan(t)
	run := cloudRun(&withRule, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))

	got := onlyOutcome(t, run, cloudRule("ledger-db"))
	if !strings.Contains(got.Detail, openingRule) {
		t.Errorf("detail %q does not name %s", got.Detail, openingRule)
	}

	if len(run.report.Evaluations) != 1 {
		t.Fatalf("%d evaluations, want 1", len(run.report.Evaluations))
	}

	entry := run.report.Evaluations[0]
	identity := [4]string{entry.Kind, entry.Query, entry.InputDigest, entry.Integrity}

	want := [4]string{
		workspace.EvaluatorCloudFirewall, "tcp/5432 from aws_lb.public to aws_db_instance.ledger", withRule.Plan.Digest, "content not verified",
	}
	if identity != want {
		t.Errorf("evaluation = %v, want %v", identity, want)
	}

	if !slices.Contains(entry.Policies, openingRule+" rule") {
		t.Errorf("policies %v do not name %s", entry.Policies, openingRule)
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

// portTexts gives `protocol/number` for each port.
func portTexts(ports []portKey) []string {
	out := make([]string, 0, len(ports))
	for _, port := range ports {
		out = append(out, port.String())
	}

	return out
}

// Without ports in the rule, the queries cover every port that a firewall rule of the plan names, the ports next to
// it, and one port that no rule names.
func TestPlanPortsIncludeAProbeOutsideEveryRule(t *testing.T) {
	t.Parallel()

	resources := []plan.Resource{
		{Type: "aws_vpc_security_group_ingress_rule", Values: map[string]any{"ip_protocol": "tcp", "from_port": float64(5432), "to_port": float64(5432)}},
		{Type: "azurerm_network_security_rule", Values: map[string]any{"protocol": "Tcp", "destination_port_ranges": []any{"1-2", "443"}}},
		{Type: "google_compute_firewall", Values: map[string]any{"allow": []any{map[string]any{"protocol": "tcp", "ports": []any{"8080"}}}}},
		{Type: "aws_s3_bucket", Values: map[string]any{"from_port": float64(9), "to_port": float64(9)}},
	}

	want := []string{
		"tcp/1", "tcp/2", "tcp/3", "tcp/4", "tcp/442", "tcp/443", "tcp/444", "tcp/5431", "tcp/5432", "tcp/5433",
		"tcp/8079", "tcp/8080", "tcp/8081",
	}
	if got := portTexts(planPorts(resources)); !slices.Equal(got, want) {
		t.Errorf("ports = %v, want %v", got, want)
	}
}

// Finding 9: a rule that opens UDP, or every protocol, is probed over UDP, with a UDP port that no rule names.
func TestPlanPortsProbeUDPWhereARuleOpensIt(t *testing.T) {
	t.Parallel()

	resources := []plan.Resource{
		{Type: "aws_vpc_security_group_ingress_rule", Values: map[string]any{"ip_protocol": "udp", "from_port": float64(53), "to_port": float64(53)}},
		{Type: "aws_security_group_rule", Values: map[string]any{"protocol": "icmp", "from_port": float64(8), "to_port": float64(8)}},
	}

	got := portTexts(planPorts(resources))
	for _, want := range []string{"udp/53", "udp/1", "tcp/1"} {
		if !slices.Contains(got, want) {
			t.Errorf("ports %v do not hold %s", got, want)
		}
	}

	if slices.Contains(got, "tcp/53") || slices.Contains(got, "tcp/8") {
		t.Errorf("ports %v probe a UDP or ICMP rule over TCP", got)
	}
}

// Finding 12: deny rules on both ends of an allowed range must not hide its open inside.
func TestPlanPortsSampleTheInsideOfARange(t *testing.T) {
	t.Parallel()

	rule := func(ports string) plan.Resource {
		return plan.Resource{Type: "azurerm_network_security_rule", Values: map[string]any{"protocol": "Tcp", "destination_port_range": ports}}
	}

	got := planPorts([]plan.Resource{rule("8000-8100"), rule("8000"), rule("8100")})

	inside := slices.ContainsFunc(got, func(port portKey) bool { return port.number > 8000 && port.number < 8100 })
	if !inside {
		t.Errorf("ports %v sample no port inside 8000-8100", portTexts(got))
	}
}

// Finding 9: a rule without ports queries the plan's UDP rule over UDP, and the query names the protocol.
func TestCloudFirewallProbesUDPForARuleWithoutPorts(t *testing.T) {
	t.Parallel()

	udp := cloudPlan(t)
	for i := range udp.Plan.Resources {
		if r := &udp.Plan.Resources[i]; r.Address == openingRule {
			r.Values["ip_protocol"], r.Values["from_port"], r.Values["to_port"] = "udp", float64(53), float64(53)
		}
	}

	run := cloudRun(&udp, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))
	rule := cloudRule("ledger-db")
	rule.Predicate.EffectiveAccess.Ports = nil

	got := onlyOutcome(t, run, rule)

	const want = "udp/53 from aws_lb.public to aws_db_instance.ledger"
	if !slices.ContainsFunc(run.report.Evaluations, func(e EvaluationEntry) bool { return e.Query == want }) {
		t.Errorf("no evaluation %q", want)
	}

	if got.Verdict != VerdictViolation || !strings.Contains(got.Detail, want) {
		t.Errorf("outcome = %s %q, want a violation through %s", got.Verdict, got.Detail, want)
	}
}

// Finding 4: an address that two plans of the environment hold is not read last-wins.
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

// Finding 11: a bound address that the plan does not hold, and a plan that was not read, are answered by
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
