package rbac

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/workspace"
)

// Evaluator identity. Identifier is the workspace evaluator kind (`assurance.evaluators[].kinds`). Version changes with every
// change of the semantics, because results and digests record it.
const (
	Identifier = workspace.EvaluatorRBAC
	Version    = "1.0.0"
)

// ClaimRBACGrants is the claim that results of this evaluator can support.
const ClaimRBACGrants = "rbac-grants"

// assumptionAuthorizer is the enforcement assumption that every result depends on.
const assumptionAuthorizer = workspace.AssumeRBACAuthorizerEnabled

// Limits are stated on every result: what the evaluation does not cover.
var Limits = []string{
	"cloud IAM is not evaluated",
	"admission control is not evaluated",
	"webhook authorizers and other authorization modes are not evaluated",
	"group membership of users is not known; a Group subject matches only the groups given in the query and the ServiceAccount groups",
	"ClusterRoles that the cluster provides (cluster-admin, admin, edit, view) and that are not in the rendered input are reported as missing role targets",
}

// Contract is the evaluator contract that the claim registry and `doctor` show.
func Contract() evaluate.Contract {
	return evaluate.Contract{
		ID:      Identifier,
		Version: Version,
		Kinds: []string{
			"rbac.authorization.k8s.io/v1 Role, ClusterRole (rules, aggregationRule), RoleBinding, ClusterRoleBinding " +
				"(subjects ServiceAccount, User, Group); wildcard verbs, resources, and apiGroups; subresources; resourceNames; nonResourceURLs",
		},
		RequiresEvidence: evaluate.EvidenceRendered,
		RequiresCoverage: "complete for the cluster-scoped resources and the namespace of a result",
		Assumes:          []string{assumptionAuthorizer},
		Enables:          []string{ClaimRBACGrants},
		DoesNotEnable: []string{
			"effective-authorization",
			"cloud-iam",
			"network-isolation",
		},
		Limits: append([]string(nil), Limits...),
	}
}
