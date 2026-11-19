// Package evaluate holds what the built-in effective-policy evaluators (netpol, rbac) share: the evidence gate
// vocabulary, the evaluator contract that the claim registry and `doctor` show, and the input digest. The
// evaluators live in the subpackages and import this package; this package imports none of them.
package evaluate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/inclaro/concordia/internal/canonjson"
)

// Evidence levels an evaluator input can have. An evaluator runs only on rendered-or-evaluated evidence; declared
// evidence (provider or code-graph level) names resources but does not carry the selectors and rules.
const (
	EvidenceDeclared = "declared"
	EvidenceRendered = "rendered-or-evaluated"
)

// digestPrefix starts every digest that this package writes.
const digestPrefix = "sha256:"

// Unknown reasons that evaluators set on an `unknown` result. They are members of the closed reason set of the
// conformance results (reasons.go) or details that explain one of them; the integrator maps them.
const (
	// ReasonRenderedEvidenceMissing: the input is declared-level only.
	ReasonRenderedEvidenceMissing = "rendered-evidence-missing"
	// ReasonCoverageIncomplete: a namespace (or the cluster-scoped set) the result depends on is partial or failed.
	ReasonCoverageIncomplete = "coverage-incomplete"
	// ReasonUnsupportedConstruct: the result depends on a construct the evaluator does not support.
	ReasonUnsupportedConstruct = "unsupported-construct"
	// ReasonSubjectNotFound: a workload or subject of the query is not in the input.
	ReasonSubjectNotFound = "subject-not-found"
	// ReasonRoleTargetMissing: a binding that applies refers to a role that is not in the input.
	ReasonRoleTargetMissing = "role-target-missing"
)

// Gap kinds that evaluators record. The kind is stable; the detail names the construct.
const (
	GapNamespaceCoverage  = "namespace-coverage-incomplete"
	GapClusterCoverage    = "cluster-coverage-incomplete"
	GapNamespaceMissing   = "namespace-not-in-input"
	GapWorkloadMissing    = "workload-not-in-input"
	GapUnsupportedKind    = "unsupported-policy-kind"
	GapUnresolvedPort     = "unresolved-named-port"
	GapClusterRange       = "cluster-dependent-range"
	GapInvalidSelector    = "invalid-selector"
	GapInvalidRange       = "invalid-address-range"
	GapInvalidPeer        = "invalid-peer"
	GapMissingRole        = "missing-role-target"
	GapInvalidRoleRef     = "invalid-role-reference"
	GapAggregationCycle   = "aggregation-cycle"
	GapDeclaredOnly       = "declared-evidence-only"
	GapUnsupportedSubject = "unsupported-subject"
)

// Contract is the declared contract of one evaluator (spec "An evaluator has a validated contract"): what it
// supports, what input it needs, which assumptions about enforcement its results rest on, and which claims it does
// and does not enable. `doctor` lists it and the claim registry derives the claim contracts from it.
type Contract struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// Kinds are the policy kinds and syntax that the evaluator supports.
	Kinds []string `json:"kinds"`
	// RequiresEvidence is the evidence level the input must have.
	RequiresEvidence string `json:"requiresEvidence"`
	// RequiresCoverage is the completeness the input must have for the policies, workloads, and namespaces a result
	// depends on.
	RequiresCoverage string `json:"requiresCoverage"`
	// Assumes are the enforcement assumptions a result depends on; the workspace must declare them explicitly.
	Assumes []string `json:"assumes"`
	// Enables are the claims that results of this evaluator can support.
	Enables []string `json:"enables"`
	// DoesNotEnable are the claims that the evaluator explicitly does not support.
	DoesNotEnable []string `json:"doesNotEnable"`
	// Limits are what the evaluator does not evaluate.
	Limits []string `json:"limits"`
}

// Digest is the contract digest ("sha256:<hex>" of the canonical JSON): it changes when the contract changes, so a
// run manifest can show that an evaluator upgrade changed the claims.
func (c *Contract) Digest() string { return DigestOf(c) }

// DigestOf gives "sha256:<hex>" of the canonical JSON of v. It panics only when v cannot be encoded, which the
// evaluator inputs and contracts (plain structs) cannot cause.
func DigestOf(v any) string {
	b, err := canonjson.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("evaluate: digest: %v", err))
	}

	sum := sha256.Sum256(b)

	return digestPrefix + hex.EncodeToString(sum[:])
}
