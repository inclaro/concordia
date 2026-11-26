package conformance

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// Tests of the ports that cloud-firewall queries for a rule without ports.

// portTexts gives `protocol/number` for each port.
func portTexts(ports []portKey) []string {
	out := make([]string, 0, len(ports))
	for _, port := range ports {
		out = append(out, port.String())
	}

	return out
}

// Without ports in the rule, the queries cover every port that a firewall rule of the plan names, the ports next to
// it, and one port that no rule names. The Azure resource also adds a UDP port, for the Azure default rules.
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
		"tcp/8079", "tcp/8080", "tcp/8081", "udp/1",
	}
	if got := portTexts(planPorts(resources)); !slices.Equal(got, want) {
		t.Errorf("ports = %v, want %v", got, want)
	}
}

// A rule that opens UDP, or every protocol, is probed over UDP, with a UDP port that no rule names.
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

// Deny rules on both ends of an allowed range must not hide its open inside.
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

// A rule without ports queries the plan's UDP rule over UDP, and the query names the protocol.
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

// azureNSGRule is an inline `security_rule` of an NSG that applies to every source and destination.
func azureNSGRule(name string, priority float64, access, protocol string) map[string]any {
	return map[string]any{
		"name": name, "priority": priority, "direction": "Inbound", "access": access, "protocol": protocol,
		"source_port_range": "*", "destination_port_range": "*",
		"source_address_prefix": "*", "destination_address_prefix": "*",
	}
}

// azureUDPDenyPlan is an Azure plan whose database NIC has an NSG that denies all UDP ports with a range and allows
// TCP with `*`, which names no range.
func azureUDPDenyPlan() *plan.Plan {
	deny := azureNSGRule("deny-udp", 100, "Deny", "Udp")
	deny["destination_port_range"] = "0-65535"

	return azureNICPlan(deny, azureNSGRule("allow-tcp", 200, "Allow", "Tcp"))
}

// azureNICPlan is an Azure plan with an app NIC and a database NIC in two subnets of one virtual network, and an NSG
// with the rules on the database NIC.
func azureNICPlan(rules ...any) *plan.Plan {
	subnet := func(address, id, prefix string) plan.Resource {
		return planned(address, map[string]any{
			"id": id, "name": id, "resource_group_name": "rg", "virtual_network_name": "vnet", "address_prefixes": []any{prefix},
		})
	}
	nic := func(address, id, subnetID, ip string) plan.Resource {
		return planned(address, map[string]any{
			"id": id, "ip_configuration": []any{map[string]any{"subnet_id": subnetID, "private_ip_address": ip}},
		})
	}

	return &plan.Plan{
		Digest: "sha256:" + strings.Repeat("a", 64), Coverage: assurance.CoverageComplete,
		Resources: []plan.Resource{
			nic("azurerm_network_interface.app", "nic-app", "sub-app", "10.0.1.4"),
			nic("azurerm_network_interface.db", "nic-db", "sub-db", "10.0.2.4"),
			planned("azurerm_network_interface_security_group_association.db", map[string]any{
				"network_interface_id": "nic-db", "network_security_group_id": "nsg-db",
			}),
			planned("azurerm_network_security_group.db", map[string]any{
				"id": "nsg-db", "name": "db", "resource_group_name": "rg",
				"security_rule": rules,
			}),
			subnet("azurerm_subnet.app", "sub-app", "10.0.1.0/24"),
			subnet("azurerm_subnet.db", "sub-db", "10.0.2.0/24"),
		},
	}
}

// A UDP range over every port must not leave TCP without a query. The TCP allow with `*` is open, so a rule `expect:
// denied` without ports is violated.
func TestCloudFirewallQueriesEveryProtocolInUse(t *testing.T) {
	t.Parallel()

	got, run := azurePortlessOutcome(t, azureUDPDenyPlan())
	if got.Verdict != VerdictViolation {
		t.Errorf("outcome = %s %q %q, want a violation through TCP", got.Verdict, got.Reason, got.Detail)
	}

	if !hasAllowedTCPFromAppToDB(run.report.Evaluations) {
		t.Errorf("no allowed TCP evaluation from the app to the database: %+v", run.report.Evaluations)
	}
}

// azurePortlessOutcome evaluates a rule `expect: denied` without ports from the app NIC to the database NIC of an Azure
// plan, with the completeness assumption declared.
func azurePortlessOutcome(t *testing.T, p *plan.Plan) (ExternalOutcome, *assuranceRun) {
	t.Helper()

	declared := evidence.Plan{
		Plan: p, Coverage: assurance.CoverageComplete,
		Declared: workspace.AssurancePlan{Environment: cloudEnvironment, Tool: "terraform", File: cloudPlanFile},
	}

	run := cloudRun(&declared, cloudEvaluator(workspace.AssumeFirewallRulesInPlan))
	run.bindings = []binding.Binding{
		infraBinding("app", "azurerm_network_interface.app"), infraBinding("db", "azurerm_network_interface.db"),
	}

	rule := &policy.RulePlan{
		RuleID: "AZ-NET", Subjects: []string{"app"}, Targets: []string{"db"},
		Predicate: policy.Predicate{Family: policy.FamilyEffectiveAccess, EffectiveAccess: &policy.EffectiveAccess{
			Dimension: policy.DimensionNetwork, Expect: policy.ExpectDenied,
		}},
	}

	return onlyOutcome(t, run, rule), run
}

// The Azure default rule AllowVnetInBound allows every protocol inside one virtual network, and it is not in the plan.
// An NSG that denies only TCP leaves UDP open, so a rule `expect: denied` without ports must query UDP and is violated.
func TestCloudFirewallQueriesUDPForAzureDefaultRules(t *testing.T) {
	t.Parallel()

	vnet := azureNICPlan(azureNSGRule("deny-tcp", 100, "Deny", "Tcp"))
	for i := range vnet.Resources {
		vnet.Resources[i].ProviderConfig = "azurerm"
	}

	got, run := azurePortlessOutcome(t, vnet)
	if got.Verdict != VerdictViolation {
		t.Errorf("outcome = %s %q %q, want a violation through UDP", got.Verdict, got.Reason, got.Detail)
	}

	if !slices.ContainsFunc(run.report.Evaluations, func(e EvaluationEntry) bool { return strings.HasPrefix(e.Query, "udp/") }) {
		t.Errorf("no UDP evaluation: %+v", run.report.Evaluations)
	}
}

// The inline rules of an `aws_default_security_group`, which the evaluator reads, name ports to query.
func TestPlanPortsReadDefaultSecurityGroups(t *testing.T) {
	t.Parallel()

	resources := []plan.Resource{{Type: "aws_default_security_group", Values: map[string]any{
		"ingress": []any{map[string]any{"protocol": "tcp", "from_port": float64(5432), "to_port": float64(5432)}},
	}}}

	if got := portTexts(planPorts(resources)); !slices.Contains(got, "tcp/5432") {
		t.Errorf("ports %v do not hold tcp/5432", got)
	}
}

// A port range with spaces around the dash, which the evaluator reads, gives the same queries as one without.
func TestPlanPortsReadRangesWithSpaces(t *testing.T) {
	t.Parallel()

	rule := func(ports string) []plan.Resource {
		return []plan.Resource{{Type: "azurerm_network_security_rule", Values: map[string]any{"protocol": "Tcp", "destination_port_range": ports}}}
	}

	if spaced, plain := portTexts(planPorts(rule("8000 - 8100"))), portTexts(planPorts(rule("8000-8100"))); !slices.Equal(spaced, plain) {
		t.Errorf("ports of `8000 - 8100` = %v, want those of `8000-8100`: %v", spaced, plain)
	}
}

// hasAllowedTCPFromAppToDB reports whether an evaluation is an allowed TCP query from the app NIC to the database NIC.
func hasAllowedTCPFromAppToDB(entries []EvaluationEntry) bool {
	for i := range entries {
		if isAllowedTCPFromAppToDB(&entries[i]) {
			return true
		}
	}

	return false
}

func isAllowedTCPFromAppToDB(e *EvaluationEntry) bool {
	const ends = " from azurerm_network_interface.app to azurerm_network_interface.db"

	return e.Verdict == string(cloudfw.VerdictAllowed) && strings.HasPrefix(e.Query, "tcp/") && strings.HasSuffix(e.Query, ends)
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
