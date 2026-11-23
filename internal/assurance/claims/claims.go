// Package claims is the claim registry of the assurance adapters (design D7, D23). A claim is a statement that a
// rule can need, such as `exact-lineage` or `network-isolation`. Each claim is enabled by exactly one contract: the
// contract of the adapter or evaluator that can support it, with the evidence level, the completeness, and the
// integrity that the evidence needs. A rule that needs a claim that no loaded contract enables is `unknown` with the
// reason `claim-not-enabled`; it never passes.
//
// The registry is owned by Concordia and is not part of the provider capability profile, so registering a contract
// never changes a profile digest. Its own digest goes into the run manifest, so a change of a contract shows.
package claims

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/policy"
)

// ReasonClaimNotEnabled is the closed-set `unknown` reason of a claim that no loaded contract enables. It is the
// value of conformance.ReasonClaimNotEnabled; this package cannot import conformance, and a test there keeps the two
// equal.
const ReasonClaimNotEnabled = "claim-not-enabled"

// ID names a claim.
type ID string

// The claims of release R3 (design D7), in registry order.
const (
	CompleteResourceSet         ID = "complete-resource-set"
	ContainerOccurrenceVersions ID = "container-occurrence-versions"
	ResolvedSelections          ID = "resolved-selections"
	PlannedResources            ID = "planned-resources"
	ExactLineage                ID = "exact-lineage"
	DeployedDigest              ID = "deployed-digest"
	DeploymentSucceeded         ID = "deployment-succeeded"
	RuntimeDrift                ID = "runtime-drift"
	NetworkIsolation            ID = "network-isolation"
	CloudNetworkIsolation       ID = "cloud-network-isolation"
	RBACGrants                  ID = "rbac-grants"
	CheckOutcome                ID = "check-outcome"
	QualityScenarioVerdict      ID = "quality-scenario-verdict"
)

// IDs gives the thirteen registered claims in registry order.
func IDs() []ID {
	return []ID{
		CompleteResourceSet, ContainerOccurrenceVersions, ResolvedSelections, PlannedResources,
		ExactLineage, DeployedDigest, DeploymentSucceeded, RuntimeDrift,
		NetworkIsolation, CloudNetworkIsolation, RBACGrants, CheckOutcome, QualityScenarioVerdict,
	}
}

// Registered says whether the id is one of the thirteen claims.
func Registered(id ID) bool { return slices.Contains(IDs(), id) }

// LevelNotApplicable is the evidence level of a claim that is not about a level of the deployment trace, such as the
// outcome of a check.
const LevelNotApplicable = "not-applicable"

// Kind says what a contract describes.
type Kind string

// Contract kinds.
const (
	// KindAdapter is an evidence adapter: it reads or produces evidence.
	KindAdapter Kind = "adapter"
	// KindEvaluator is a built-in effective-policy evaluator.
	KindEvaluator Kind = "evaluator"
)

// Requirement is what evidence needs to support a claim.
type Requirement struct {
	// Level is the evidence level of the deployment trace (`declared`, `rendered-or-evaluated`,
	// `deployment-execution`, `runtime-observation`), or LevelNotApplicable.
	Level string `json:"level"`
	// Completeness is the coverage that the input of the claim must have.
	Completeness assurance.Coverage `json:"completeness"`
	// Integrity is the lowest integrity of the evidence: below it the claim is not supported.
	Integrity assurance.Integrity `json:"integrity"`
}

// Enablement is a claim that a contract enables, with the evidence it needs.
type Enablement struct {
	Claim    ID          `json:"claim"`
	Requires Requirement `json:"requires"`
}

// Contract is the contract of one adapter or evaluator. `doctor` lists it, and the run manifest records its digest.
type Contract struct {
	// ID names the contract (`rendering`, `plan`, `provenance`, `network-policy`, `rbac`, `verification`, `quality`).
	ID string `json:"id"`
	// Kind is `adapter` or `evaluator`.
	Kind Kind `json:"kind"`
	// Version changes with every change of the semantics of the contract.
	Version string `json:"version"`
	// Tools are the supported versions of external tools, for example `helm 4`. Empty when the contract runs none.
	Tools []string `json:"tools"`
	// Inputs are the supported inputs and their syntax.
	Inputs []string `json:"inputs"`
	// Fixtures are the fixtures (or test files, for an evaluator) that show the behavior of the contract.
	Fixtures []string `json:"fixtures"`
	// Limits are the known limits: what the contract does not cover.
	Limits []string `json:"limits"`
	// Assumes are the enforcement assumptions that a result depends on and that the workspace must declare.
	Assumes []string `json:"assumes"`
	// Enabled are the claims that the contract enables.
	Enabled []Enablement `json:"enabled"`
	// NotEnabled are the claims that the contract states it does not enable. Names that are not one of the
	// registered claims (for example `slsa-level`) are claims that no contract enables.
	NotEnabled []string `json:"notEnabled"`
	// Experimental caps the severity of a rule that needs a claim of this contract at `warning`, until the fixture
	// suite of the contract is listed as passing in the registry.
	Experimental bool `json:"experimental"`
}

// Registry is the set of loaded contracts. It is immutable after New.
type Registry struct {
	byClaim   map[ID]int
	contracts []Contract
}

// ErrInvalidRegistry is wrapped by the error of New.
var ErrInvalidRegistry = errors.New("claim registry not valid")

// New builds a registry of the contracts. It refuses a duplicate contract id, a claim that is not one of the thirteen,
// and a claim that two contracts enable: each claim has one contract, so a rule can name the contract that gives it
// meaning. The input is copied and sorted by contract id.
func New(contracts []Contract) (*Registry, error) {
	sorted := slices.Clone(contracts)
	slices.SortStableFunc(sorted, func(a, b Contract) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	byClaim := make(map[ID]int, len(IDs()))

	for i := range sorted {
		contract := &sorted[i]

		if i > 0 && sorted[i-1].ID == contract.ID {
			return nil, fmt.Errorf("%w: contract %q appears two times", ErrInvalidRegistry, contract.ID)
		}

		if err := indexClaims(sorted, i, byClaim); err != nil {
			return nil, err
		}
	}

	return &Registry{contracts: sorted, byClaim: byClaim}, nil
}

// indexClaims records the claims that the contract at index i of sorted enables. It refuses a claim that is not
// registered and a claim that an earlier contract enables.
func indexClaims(sorted []Contract, i int, byClaim map[ID]int) error {
	contract := &sorted[i]

	for _, enabled := range contract.Enabled {
		if !Registered(enabled.Claim) {
			return fmt.Errorf("%w: contract %q enables %q, which is not a registered claim", ErrInvalidRegistry, contract.ID, enabled.Claim)
		}

		if other, dup := byClaim[enabled.Claim]; dup {
			return fmt.Errorf("%w: claim %q is enabled by contracts %q and %q", ErrInvalidRegistry, enabled.Claim, sorted[other].ID, contract.ID)
		}

		byClaim[enabled.Claim] = i
	}

	return nil
}

// Contracts gives the loaded contracts sorted by id. The caller may change the result.
func (r *Registry) Contracts() []Contract {
	out := make([]Contract, len(r.contracts))
	for i := range r.contracts {
		out[i] = cloneContract(&r.contracts[i])
	}

	return out
}

// Contract gives the contract with the id.
func (r *Registry) Contract(id string) (Contract, bool) {
	for i := range r.contracts {
		if r.contracts[i].ID == id {
			return cloneContract(&r.contracts[i]), true
		}
	}

	return Contract{}, false
}

func cloneContract(c *Contract) Contract {
	out := *c
	out.Tools = slices.Clone(c.Tools)
	out.Inputs = slices.Clone(c.Inputs)
	out.Fixtures = slices.Clone(c.Fixtures)
	out.Limits = slices.Clone(c.Limits)
	out.Assumes = slices.Clone(c.Assumes)
	out.Enabled = slices.Clone(c.Enabled)
	out.NotEnabled = slices.Clone(c.NotEnabled)

	return out
}

// Digest is `sha256:<hex>` of the canonical JSON of the contracts. It changes with any change of a contract, so the
// run manifest shows an upgrade that changes the claims.
func (r *Registry) Digest() string {
	data, err := canonjson.Marshal(r.Contracts())
	if err != nil {
		// Contracts are plain structs of strings: encoding cannot fail.
		panic(fmt.Sprintf("claims: digest: %v", err))
	}

	sum := sha256.Sum256(data)

	return "sha256:" + hex.EncodeToString(sum[:])
}

// Outcome is the answer of the registry to a rule that needs a claim.
type Outcome struct {
	// Claim is the claim that the rule needs, as the rule names it.
	Claim string `json:"claim"`
	// Reason is ReasonClaimNotEnabled when no loaded contract enables the claim, else empty.
	Reason string `json:"reason,omitempty"`
	// Detail says why the claim is not enabled: it is not registered, or no loaded contract enables it.
	Detail string `json:"detail,omitempty"`
	// Contract is the id of the contract that enables the claim. Empty when it is not enabled.
	Contract string `json:"contract,omitempty"`
	// Requires is the evidence the claim needs. The zero value when it is not enabled.
	Requires Requirement `json:"requires"`
	// Enabled is true when a loaded contract enables the claim.
	Enabled bool `json:"enabled"`
	// Experimental is true when the enabling contract is experimental: the severity of the rule is capped.
	Experimental bool `json:"experimental"`
}

// Lookup says whether a loaded contract enables the claim. A claim outside the registry, or one whose contract is not
// loaded, gives the outcome `claim-not-enabled`: the obligation is `unknown`, never `pass`.
func (r *Registry) Lookup(claim string) Outcome {
	claimID := ID(claim)

	if !Registered(claimID) {
		return Outcome{Claim: claim, Reason: ReasonClaimNotEnabled, Detail: fmt.Sprintf("claim %q is not in the claim registry", claim)}
	}

	i, ok := r.byClaim[claimID]
	if !ok {
		return Outcome{Claim: claim, Reason: ReasonClaimNotEnabled, Detail: fmt.Sprintf("no loaded contract enables claim %q", claim)}
	}

	contract := &r.contracts[i]
	enabled := contract.Enabled[slices.IndexFunc(contract.Enabled, func(e Enablement) bool { return e.Claim == claimID })]

	return Outcome{
		Claim: claim, Enabled: true, Contract: contract.ID, Requires: enabled.Requires, Experimental: contract.Experimental,
	}
}

// CapSeverity gives the severity that a rule has when the contract of its claim is experimental: `error` becomes
// `warning`, and `warning` and `info` stay. A contract that is not experimental leaves the severity as it is.
func CapSeverity(severity policy.RuleSeverity, experimental bool) policy.RuleSeverity {
	if experimental && severity == policy.SeverityError {
		return policy.SeverityWarning
	}

	return severity
}

// Severity gives the severity of a rule with the severity `declared` that needs the claim of the outcome. A claim
// that is not enabled gives the declared severity: the rule is `unknown` and the severity is not what limits it.
//
//nolint:gocritic // an outcome is a read-only answer that callers hold by value.
func (o Outcome) Severity(declared policy.RuleSeverity) policy.RuleSeverity {
	return CapSeverity(declared, o.Enabled && o.Experimental)
}
