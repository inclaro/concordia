package netpol

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/workspace"
)

// Evaluator identity. Identifier is the workspace evaluator kind (`assurance.evaluators[].kinds`). Version changes with every
// change of the semantics, because results and digests record it.
const (
	Identifier = workspace.EvaluatorNetworkPolicy
	Version    = "1.0.0"
)

// ClaimNetworkIsolation is the claim that results of this evaluator can support.
const ClaimNetworkIsolation = "network-isolation"

// assumptionEnforced is the enforcement assumption that every result depends on.
const assumptionEnforced = workspace.AssumeNetworkPolicyEnforced

// Limits are stated on every result: what the evaluation does not cover.
var Limits = []string{
	"CNI-specific policy kinds (CiliumNetworkPolicy, CalicoNetworkPolicy, AdminNetworkPolicy) are not evaluated",
	"enforcement by the cluster's network plugin is assumed, not observed",
	"host-network pods, node traffic, service address translation, and ingress controllers are not modeled",
	"an ipBlock is evaluated only for addresses outside the pod network",
	"service mesh and application-layer authorization are not evaluated",
}

// Contract is the evaluator contract that the claim registry and `doctor` show.
func Contract() evaluate.Contract {
	return evaluate.Contract{
		ID:      Identifier,
		Version: Version,
		Kinds: []string{
			"networking.k8s.io/v1 NetworkPolicy: podSelector, namespaceSelector, matchLabels, matchExpressions, ipBlock, " +
				"ports, protocol, endPort, named ports, policyTypes",
		},
		RequiresEvidence: evaluate.EvidenceRendered,
		RequiresCoverage: "complete for the namespaces of both workloads of a result",
		Assumes:          []string{assumptionEnforced},
		Enables:          []string{ClaimNetworkIsolation},
		DoesNotEnable: []string{
			"effective-encryption-in-transit",
			"cni-specific-policy-evaluation",
			"rbac-grants",
		},
		Limits: append([]string(nil), Limits...),
	}
}
