package impact

import (
	"context"
	"slices"
)

// Selector chooses the items of one dependency kind. An empty list means no restriction on that
// axis. Match restricts by facet name: an item matches when, for every name, it has at least one of
// the listed values.
type Selector struct {
	// Match restricts the items by facet values.
	Match map[string][]string `json:"match,omitempty"`
	// Kind is the dependency kind to select.
	Kind Kind `json:"kind"`
	// Environment restricts the items to one declared environment.
	Environment string `json:"environment,omitempty"`
	// CalmNodes restricts the items to those bound to the CALM nodes.
	CalmNodes []string `json:"calmNodes,omitempty"`
	// Identities restricts the items to the listed identities.
	Identities []string `json:"identities,omitempty"`
}

// MatchState is the outcome of selecting items.
type MatchState string

// Match states.
const (
	// MatchFound: the selector matched one or more items.
	MatchFound MatchState = "matched"
	// MatchEmpty: coverage for the kind is complete and nothing matched. This is a fact about the
	// system and differs from unsupported and unknown.
	MatchEmpty MatchState = "empty"
	// MatchUnsupported: the capability profile does not map the kind (reason `unsupported-family`).
	MatchUnsupported MatchState = "unsupported"
	// MatchUnknown: the items cannot be told (reason names why, for example `target-not-declared`
	// or `coverage-incomplete`).
	MatchUnknown MatchState = "unknown"
)

// Match is the result of FactSource.Items.
type Match struct {
	State MatchState `json:"state"`
	// Reason is one of the Reason constants for MatchUnsupported and MatchUnknown.
	Reason string `json:"reason,omitempty"`
	// Profile is the identity of the capability profile for MatchUnsupported.
	Profile string `json:"profile,omitempty"`
	// Detail says what is missing in human terms.
	Detail string `json:"detail,omitempty"`
	// Coverage is the observation coverage status of the kind in the scope (`complete`, `partial`,
	// `unknown`, and so on). Empty when the source has no coverage statement.
	Coverage string `json:"coverage,omitempty"`
	// LossModes are the loss modes that apply, sorted, for example `merged-occurrences`.
	LossModes []string `json:"lossModes,omitempty"`
	// Items are the matched items, sorted by identity. Empty unless State is MatchFound. An
	// unknown match can still hold the items that were found before the gap.
	Items []Item `json:"items"`
}

// CapabilityProfile says which dependency kinds and which selector facets the provider supports.
type CapabilityProfile struct {
	// Selectors maps a kind to the facet names that the profile can select on.
	Selectors map[Kind][]string `json:"selectors,omitempty"`
	// ID identifies the profile, for example `cbm-0.12.1`. An approval binds it.
	ID string `json:"id"`
	// Kinds are the dependency kinds that the profile maps.
	Kinds []Kind `json:"kinds"`
}

// Supports says whether the profile maps the kind.
func (p CapabilityProfile) Supports(kind Kind) bool { return slices.Contains(p.Kinds, kind) }

// SupportsSelector says whether the profile can select the kind on the facet name.
func (p CapabilityProfile) SupportsSelector(kind Kind, facet string) bool {
	return p.Supports(kind) && slices.Contains(p.Selectors[kind], facet)
}

// EvaluatorNormalization is the key of EvaluatorVersions that holds the normalization version.
const EvaluatorNormalization = "normalization"

// FactSource gives the facts that dependency items are built from. internal/conformance
// implements it over the observation snapshot, the bindings, the CALM model, and the Kubernetes
// facts. internal/assessment and this package only use it.
//
// An implementation builds every item with Contract.NewItem, so facets pass the redaction rules.
type FactSource interface {
	// Items selects the items of selector.Kind. A kind that the profile does not map gives
	// MatchUnsupported; a selector with complete coverage and no match gives MatchEmpty.
	Items(ctx context.Context, selector Selector) (Match, error)
	// FileDigest gives the digest (`sha256:<hex>`) of the file at path in the revision. found is
	// false when the path does not exist there. The error is for a failed read, not a missing file.
	FileDigest(ctx context.Context, revision, path string) (digest string, found bool, err error)
	// Profile gives the capability profile that supplies the evidence of this run.
	Profile() CapabilityProfile
	// EvaluatorVersions gives the version of each evaluator that supplies evidence, by name. The
	// key EvaluatorNormalization holds the normalization version.
	EvaluatorVersions() map[string]string
}

// FileDigesterAt binds the file reads of a FactSource to one revision for Contract.Seal.
func FileDigesterAt(ctx context.Context, facts FactSource, revision string) FileDigester {
	return func(path string) (string, bool, error) {
		return facts.FileDigest(ctx, revision, path) //nolint:wrapcheck // the FactSource error is already specific; Seal wraps it with the path.
	}
}
