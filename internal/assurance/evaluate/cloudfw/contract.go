package cloudfw

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/workspace"
)

// Evaluator identity. Identifier is the workspace evaluator kind (`assurance.evaluators[].kinds`). Version changes
// with every change of the semantics, because results and digests record it.
const (
	Identifier = workspace.EvaluatorCloudFirewall
	Version    = "1.0.0"
)

// ClaimCloudNetworkIsolation is the claim that results of this evaluator can support.
const ClaimCloudNetworkIsolation = "cloud-network-isolation"

// assumptionComplete is the enforcement assumption that a `denied` result depends on: no firewall rule for the
// resources of the query exists outside the evaluated plan.
const assumptionComplete = workspace.AssumeFirewallRulesInPlan

// Limits are stated on every result: what the evaluation does not cover.
var Limits = []string{
	"network ACLs, route tables, and firewall appliances (AWS Network Firewall, Azure Firewall) are not evaluated",
	"VPC and VNet peering, transit gateways, and private links are not evaluated",
	"GCP hierarchical and network firewall policies are not evaluated",
	"rules managed outside this plan are assumed absent, not observed",
	"a plan states what would be applied, not what is applied",
}

// Contract is the evaluator contract that the claim registry and `doctor` show.
func Contract() evaluate.Contract {
	return evaluate.Contract{
		ID:      Identifier,
		Version: Version,
		Kinds: []string{
			"aws: aws_security_group (ingress, egress), aws_security_group_rule, aws_vpc_security_group_ingress_rule, " +
				"aws_vpc_security_group_egress_rule",
			"azure: azurerm_network_security_group (security_rule), azurerm_network_security_rule, " +
				"azurerm_subnet_network_security_group_association, azurerm_network_interface_security_group_association, " +
				"azurerm_network_interface_application_security_group_association",
			"gcp: google_compute_firewall (allow, deny, source and target tags and service accounts)",
		},
		RequiresEvidence: evaluate.EvidenceRendered,
		RequiresCoverage: "a complete plan (no deferred changes) for the environment",
		Assumes:          []string{assumptionComplete},
		Enables:          []string{ClaimCloudNetworkIsolation},
		DoesNotEnable: []string{
			"network-isolation",
			"effective-encryption-in-transit",
			"cloud-iam",
			"routing-reachability",
		},
		Limits: append([]string(nil), Limits...),
	}
}
