package quality

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
)

// DecisionState is the applicability state of a registered decision in the decision summary of the run. The
// integrator maps the applicability state of internal/conformance onto it.
type DecisionState string

// The decision states.
const (
	DecisionActive     DecisionState = "active"
	DecisionInactive   DecisionState = "inactive"
	DecisionSuperseded DecisionState = "superseded"
	DecisionAmbiguous  DecisionState = "ambiguous"
	DecisionUnknown    DecisionState = "unknown"
)

// LinkStatus is the status of an `addressedBy` link.
type LinkStatus string

// The link statuses. `not-active` is shown as "not active" and never counts as the active rationale.
const (
	LinkActive    LinkStatus = "active"
	LinkNotActive LinkStatus = "not-active"
	LinkUnknown   LinkStatus = "unknown"
)

// Link is one `addressedBy` entry of a scenario or a finding (the ADDRESSES link, design D26).
type Link struct {
	// OwnerKind is `scenario` or `finding`; Owner is its id.
	OwnerKind string `json:"ownerKind"`
	Owner     string `json:"owner"`
	// ADR is the identifier as written and Canonical its canonical form, which the comparison used.
	ADR       string     `json:"adr"`
	Canonical string     `json:"canonical"`
	Status    LinkStatus `json:"status"`
	// State is the state of the registered decision, empty when ADR governance is not available.
	State DecisionState `json:"state,omitempty"`
	// Detail is `adr-governance-not-available` when no decision summary exists, and the state for an unknown link
	// of a registered decision.
	Detail   string   `json:"detail,omitempty"`
	Location Location `json:"location"`
	// ReviewRationale flags the owner for review of its rationale: the decision is superseded or inactive.
	ReviewRationale bool `json:"reviewRationale"`
}

// CountsAsActive says whether the link may count as an active rationale of its owner.
func (l *Link) CountsAsActive() bool { return l.Status == LinkActive }

// ResolveLinks reads the status of every `addressedBy` link of the scenarios and findings from the decision states
// of the run. canonical is the canonical-ID function of the ADR package (`adr.CanonicalID`); it is a parameter
// because internal/adr imports internal/conformance, which this package must not.
//
// states is the decision summary: canonical decision ID to state. Nil means that the run does not evaluate ADR
// governance (no decision register, or the governance pack is not enabled); every link is then `unknown` with
// `adr-governance-not-available`, never active, and the file stays valid. With a non-nil map, an identifier that is
// not a registered decision is invalid input: CO-QUA-002 with exit code 2, at the entry. `active` gives an active
// link; `superseded` and `inactive` give `not-active` and flag the owner for review of its rationale; `ambiguous`
// and `unknown` give `unknown`.
//
// The links come in the order of the scenarios, then the findings (both sorted by id), then the order written.
func ResolveLinks(set *Set, states map[string]DecisionState, canonical func(string) string) ([]Link, []*diag.Error) {
	links := []Link{}

	var errs []*diag.Error

	var byCanonical map[string]DecisionState

	if states != nil {
		byCanonical = make(map[string]DecisionState, len(states))
		for id, state := range states {
			byCanonical[canonical(id)] = state
		}
	}

	add := func(kind, owner string, refs []ADRRef) {
		for _, ref := range refs {
			link := Link{OwnerKind: kind, Owner: owner, ADR: ref.ID, Canonical: canonical(ref.ID), Location: ref.Location}

			if states == nil {
				link.Status, link.Detail = LinkUnknown, DetailADRGovernanceNotAvailable
				links = append(links, link)

				continue
			}

			state, registered := byCanonical[link.Canonical]
			if !registered {
				errs = append(errs, diag.Fail(diag.QUA002, diag.Detail(fmt.Sprintf(
					"%s %s: addressedBy %s is not a registered decision", kind, owner, ref.ID)), at(ref.Location)))

				continue
			}

			link.State = state

			switch state {
			case DecisionActive:
				link.Status = LinkActive
			case DecisionSuperseded, DecisionInactive:
				link.Status, link.ReviewRationale = LinkNotActive, true
			case DecisionAmbiguous, DecisionUnknown:
			}

			if link.Status == "" {
				// ambiguous, unknown, or a state that this package does not know: not evidence of an active rationale.
				link.Status, link.Detail = LinkUnknown, string(state)
			}

			links = append(links, link)
		}
	}

	scenarios := slices.Clone(set.Scenarios)
	slices.SortStableFunc(scenarios, func(a, b *Scenario) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	for _, sc := range scenarios {
		add("scenario", sc.ID, sc.AddressedBy)
	}

	findings := slices.Clone(set.Findings)
	slices.SortStableFunc(findings, func(a, b *Finding) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	for _, f := range findings {
		add("finding", f.ID, f.AddressedBy)
	}

	return links, errs
}
