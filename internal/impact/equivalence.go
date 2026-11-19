package impact

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// DetailContractChanged is the `review-required` detail for an approval that was bound under a
// digest contract that is neither the current one nor equivalent to it.
const DetailContractChanged = "digest contract changed"

// Equivalence marks a contract as keeping the digests of an earlier version. It needs a fixture:
// at least one item of every dependency kind. The claim holds only when the two contracts give
// equal digests for every fixture item and the new contract drops no field of the earlier one.
type Equivalence struct {
	// From is the earlier contract version.
	From string
	// Fixtures are the items that prove equal digests. They need every facet that either contract
	// lists, so a dropped or changed field shows as a different digest.
	Fixtures []Item
}

// ErrEquivalenceInvalid is returned when a contract claims an equivalence that its fixture or
// field list does not support.
var ErrEquivalenceInvalid = errors.New("impact: equivalence claim does not hold")

// Registry holds the known contract versions.
type Registry struct {
	contracts map[string]Contract
}

// NewRegistry builds a registry. A later contract with the same version replaces an earlier one.
func NewRegistry(contracts ...Contract) Registry {
	r := Registry{contracts: make(map[string]Contract, len(contracts))}
	for _, c := range contracts {
		r.contracts[c.Version] = c
	}

	return r
}

// Builtin gives the contracts that this release ships.
func Builtin() Registry { return NewRegistry(V1()) }

// Lookup gives the contract of a version.
func (r Registry) Lookup(version string) (Contract, bool) {
	c, ok := r.contracts[version]

	return c, ok
}

// Versions gives the known versions, sorted.
func (r Registry) Versions() []string {
	out := make([]string, 0, len(r.contracts))
	for v := range r.contracts {
		out = append(out, v)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// EquivalentFrom says whether an approval bound under contract version `from` still holds under
// version `to`. It holds when the versions are equal, or when `to` is marked equivalent to `from`
// (directly or through a chain of marked versions) and every marked step is proven by its fixture.
// The default is false: with no mark there is no equivalence (A28).
//
// A non-nil error means a mark exists but does not hold; the result is false then.
func (r Registry) EquivalentFrom(current, from string) (bool, error) {
	return r.equivalentFrom(current, from, map[string]bool{})
}

func (r Registry) equivalentFrom(current, from string, seen map[string]bool) (bool, error) {
	if current == from {
		return true, nil
	}

	if seen[current] {
		return false, nil
	}

	seen[current] = true

	target, ok := r.contracts[current]
	if !ok {
		return false, nil
	}

	var firstErr error

	for _, claim := range target.Equivalent {
		earlier, known := r.contracts[claim.From]
		if !known {
			firstErr = cmp.Or(firstErr, fmt.Errorf("%w: %s names the unknown version %s", ErrEquivalenceInvalid, current, claim.From))

			continue
		}

		if err := verifyEquivalence(target, earlier, claim); err != nil {
			firstErr = cmp.Or(firstErr, err)

			continue
		}

		ok, err := r.equivalentFrom(claim.From, from, seen)
		if err != nil {
			firstErr = cmp.Or(firstErr, err)
		}

		if ok {
			return true, nil
		}
	}

	return false, firstErr
}

// verifyEquivalence checks one marked step.
func verifyEquivalence(next, from Contract, claim Equivalence) error {
	covered := map[Kind]bool{}

	for _, fixture := range claim.Fixtures {
		covered[fixture.Kind] = true
	}

	for _, kind := range kinds {
		if !covered[kind] {
			return fmt.Errorf("%w: %s from %s has no fixture for kind %s", ErrEquivalenceInvalid, next.Version, from.Version, kind)
		}

		for _, field := range from.Fields[kind] {
			if !slices.Contains(next.Fields[kind], field) {
				return fmt.Errorf("%w: %s drops field %s of kind %s", ErrEquivalenceInvalid, next.Version, field, kind)
			}
		}
	}

	for _, fixture := range claim.Fixtures {
		if next.Digest(fixture, nil) != from.Digest(fixture, nil) {
			return fmt.Errorf("%w: %s and %s give different digests for %s %s",
				ErrEquivalenceInvalid, next.Version, from.Version, fixture.Kind, fixture.Identity)
		}
	}

	return nil
}

// Bound is an approval and the contract version it was bound under.
type Bound struct {
	// Assessment is the assessment id and version, for example `ASM-EDGE@2`.
	Assessment string `json:"assessment"`
	// Approval is the approval id.
	Approval string `json:"approval"`
	// Contract is the digest-contract version of the approval.
	Contract string `json:"contract"`
}

// Affected is an approval that a contract change invalidates.
type Affected struct {
	Assessment    string `json:"assessment"`
	Approval      string `json:"approval"`
	BoundContract string `json:"boundContract"`
	// Detail is DetailContractChanged.
	Detail string `json:"detail"`
}

// DigestImpactReport lists, for a current contract version, the approvals that it invalidates and
// the approvals that stay valid because the versions are equivalent.
type DigestImpactReport struct {
	// Current is the contract version that the run uses.
	Current string `json:"current"`
	// Affected are the approvals bound under a version that is not equivalent. Sorted by assessment, then approval.
	Affected []Affected `json:"affected"`
	// Equivalent are the approvals bound under an earlier version that is marked equivalent.
	Equivalent []Bound `json:"equivalent"`
	// Invalid are the equivalence marks that do not hold, one text each.
	Invalid []string `json:"invalid"`
}

// DigestImpact lists the approvals that the move to contract version `current` invalidates. An
// approval bound under the current version, or under a version that is proven equivalent, is not
// listed as affected. Everything else is affected with the detail DetailContractChanged.
func (r Registry) DigestImpact(current string, bound []Bound) DigestImpactReport {
	report := DigestImpactReport{Current: current, Affected: []Affected{}, Equivalent: []Bound{}, Invalid: []string{}}

	for _, approval := range bound {
		same, err := r.EquivalentFrom(current, approval.Contract)
		if err != nil && !slices.Contains(report.Invalid, err.Error()) {
			report.Invalid = append(report.Invalid, err.Error())
		}

		switch {
		case same && approval.Contract == current:
		case same:
			report.Equivalent = append(report.Equivalent, approval)
		default:
			report.Affected = append(report.Affected, Affected{
				Assessment: approval.Assessment, Approval: approval.Approval, BoundContract: approval.Contract, Detail: DetailContractChanged,
			})
		}
	}

	slices.SortFunc(report.Affected, func(a, b Affected) int {
		return cmp.Or(canonjson.CompareUTF16(a.Assessment, b.Assessment), canonjson.CompareUTF16(a.Approval, b.Approval))
	})
	slices.SortFunc(report.Equivalent, func(a, b Bound) int {
		return cmp.Or(canonjson.CompareUTF16(a.Assessment, b.Assessment), canonjson.CompareUTF16(a.Approval, b.Approval))
	})
	slices.SortFunc(report.Invalid, canonjson.CompareUTF16)

	return report
}
