package conformance

import (
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

// planned is a planned resource of the address with the values.
func planned(address string, values map[string]any) plan.Resource {
	typ, name, _ := strings.Cut(address, ".")

	return plan.Resource{Address: address, Mode: "managed", Type: typ, Name: name, Actions: []string{"create"}, Values: values}
}

// azureNSGRule is an inline `security_rule` of an NSG that applies to every source and destination.
func azureNSGRule(name string, priority float64, access, protocol string) map[string]any {
	return map[string]any{
		"name": name, "priority": priority, "direction": "Inbound", "access": access, "protocol": protocol,
		"source_port_range": "*", "destination_port_range": "*",
		"source_address_prefix": "*", "destination_address_prefix": "*",
	}
}

// azureUDPDenyPlan is the reviewer's input for finding C: the NSG of the database NIC denies all UDP ports with a
// range and allows TCP with `*`, which names no range.
func azureUDPDenyPlan() *plan.Plan {
	deny := azureNSGRule("deny-udp", 100, "Deny", "Udp")
	deny["destination_port_range"] = "0-65535"

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
				"security_rule": []any{deny, azureNSGRule("allow-tcp", 200, "Allow", "Tcp")},
			}),
			subnet("azurerm_subnet.app", "sub-app", "10.0.1.0/24"),
			subnet("azurerm_subnet.db", "sub-db", "10.0.2.0/24"),
		},
	}
}

// Finding C: a UDP range over every port must not leave TCP without a query. The TCP allow with `*` is open, so a
// rule `expect: denied` without ports is violated.
func TestCloudFirewallQueriesEveryProtocolInUse(t *testing.T) {
	t.Parallel()

	declared := evidence.Plan{
		Plan: azureUDPDenyPlan(), Coverage: assurance.CoverageComplete,
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

	if got := onlyOutcome(t, run, rule); got.Verdict != VerdictViolation {
		t.Errorf("outcome = %s %q %q, want a violation through TCP", got.Verdict, got.Reason, got.Detail)
	}

	if !hasAllowedTCPFromAppToDB(run.report.Evaluations) {
		t.Errorf("no allowed TCP evaluation from the app to the database: %+v", run.report.Evaluations)
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

// Finding F: an address that two plans hold blocks the environment only when the evaluator reads that type.
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

// Review 4, step 5a: an address of a provider resource that two plans hold makes only the pairs that bind it
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
