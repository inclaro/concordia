package impact

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// InvalidatorKind names a value that, when it changes, can invalidate an approval that was
// produced under the earlier value (change-impact, "Possible invalidators").
type InvalidatorKind string

// The five possible invalidators.
const (
	InvalidatorPolicy         InvalidatorKind = "policy"
	InvalidatorTrustAuthority InvalidatorKind = "trust-authority"
	InvalidatorEvaluator      InvalidatorKind = "evaluator"
	InvalidatorNormalization  InvalidatorKind = "normalization"
	InvalidatorDigestContract InvalidatorKind = "digest-contract"
)

// InvalidatorKinds gives the five kinds in the order of the spec.
func InvalidatorKinds() []InvalidatorKind {
	return []InvalidatorKind{
		InvalidatorPolicy, InvalidatorTrustAuthority, InvalidatorEvaluator,
		InvalidatorNormalization, InvalidatorDigestContract,
	}
}

// StateReviewRequired is the currency state that an invalidator or a trigger gives. The strings
// mirror the states of internal/assessment, which this package cannot import.
const StateReviewRequired = "review-required"

// Basis is the set of values that an approval was produced under, or that the current run uses.
// An empty string, or a missing evaluator, in the bound basis means that the approval did not
// record the value, and Invalidators does not compare it. Contract is always compared: an approval
// without a contract version is bound to nothing and is never equivalent to the current one.
type Basis struct {
	// Evaluators are the versions of the evaluators that supplied evidence, by name. The key
	// EvaluatorNormalization holds the normalization version (see FactSource.EvaluatorVersions).
	Evaluators map[string]string `json:"evaluators,omitempty"`
	// Contract is the digest-contract version (`review-digest/1`).
	Contract string `json:"contract"`
	// Policy is the digest of the review policy rules that the evaluation used.
	Policy string `json:"policy,omitempty"`
	// Authority is the digest of the authority configuration read from the trust anchor.
	Authority string `json:"authority,omitempty"`
}

// Invalidation is one possible invalidator of one approval.
type Invalidation struct {
	Kind InvalidatorKind `json:"kind"`
	// Name is the evaluator name. Empty for the other kinds.
	Name string `json:"name,omitempty"`
	// Bound is the earlier value; Current the value now. Empty means absent.
	Bound   string `json:"bound,omitempty"`
	Current string `json:"current,omitempty"`
	// Detail is the text for the report. The digest contract gives DetailContractChanged.
	Detail string `json:"detail"`
}

// Equivalent says whether a changed value of an invalidator, other than the digest contract, is
// marked equivalent to the earlier value by shipped, tested evidence. A nil function marks nothing:
// the default is that no change is equivalent. name is the evaluator name and is empty for the
// other kinds.
type Equivalent func(kind InvalidatorKind, name, from, to string) bool

func (e Equivalent) holds(kind InvalidatorKind, name, from, to string) bool {
	return e != nil && e(kind, name, from, to)
}

// Invalidators gives the possible invalidators of an approval bound under `bound`, given the values
// that the run uses now (`current`). The digest contract is compared through the registry: equal
// versions, or a version proven equivalent, do not invalidate; anything else gives
// DetailContractChanged. The other values invalidate when they differ and no equivalence marks
// the change. An evaluator that the approval did not use is not compared; an evaluator that the
// approval used and that is no longer present counts as changed. The result is sorted by kind
// order, then name. The error text of a failed equivalence mark is returned separately.
func Invalidators(reg Registry, bound, current Basis, equivalent Equivalent) (found []Invalidation, invalid []string) {
	found = []Invalidation{}

	same, err := reg.EquivalentFrom(current.Contract, bound.Contract)
	if err != nil {
		invalid = append(invalid, err.Error())
	}

	if !same {
		found = append(found, Invalidation{
			Kind: InvalidatorDigestContract, Bound: bound.Contract, Current: current.Contract, Detail: DetailContractChanged,
		})
	}

	found = appendIfChanged(found, &Invalidation{Kind: InvalidatorPolicy, Bound: bound.Policy, Current: current.Policy}, equivalent)
	found = appendIfChanged(found, &Invalidation{Kind: InvalidatorTrustAuthority, Bound: bound.Authority, Current: current.Authority}, equivalent)
	found = appendEvaluatorChanges(found, bound, current, equivalent)

	order := InvalidatorKinds()

	slices.SortStableFunc(found, func(a, b Invalidation) int {
		return cmp.Or(
			cmp.Compare(slices.Index(order, a.Kind), slices.Index(order, b.Kind)),
			canonjson.CompareUTF16(a.Name, b.Name),
		)
	})

	return found, invalid
}

// appendEvaluatorChanges appends the changed evaluators that the bound basis recorded, in name order.
func appendEvaluatorChanges(found []Invalidation, bound, current Basis, equivalent Equivalent) []Invalidation {
	names := make([]string, 0, len(bound.Evaluators))
	for name := range bound.Evaluators {
		names = append(names, name)
	}

	slices.SortFunc(names, canonjson.CompareUTF16)

	for _, name := range names {
		inv := Invalidation{Kind: InvalidatorEvaluator, Name: name, Bound: bound.Evaluators[name], Current: current.Evaluators[name]}
		if name == EvaluatorNormalization {
			inv.Kind, inv.Name = InvalidatorNormalization, ""
		}

		found = appendIfChanged(found, &inv, equivalent)
	}

	return found
}

// appendIfChanged appends inv, with its detail, when the bound value was recorded, differs from the
// current one, and no equivalence marks the change.
func appendIfChanged(found []Invalidation, inv *Invalidation, equivalent Equivalent) []Invalidation {
	if inv.Bound == "" || inv.Bound == inv.Current {
		return found
	}

	if equivalent.holds(inv.Kind, inv.Name, inv.Bound, inv.Current) {
		return found
	}

	inv.Detail = changedDetail(inv.Kind, inv.Name, inv.Current)

	return append(found, *inv)
}

func changedDetail(kind InvalidatorKind, name, current string) string {
	switch kind {
	case InvalidatorPolicy:
		return "policy changed"
	case InvalidatorTrustAuthority:
		return "trust authority changed"
	case InvalidatorNormalization:
		return "normalization version changed"
	case InvalidatorEvaluator:
		if current == "" {
			return fmt.Sprintf("evaluator %s is no longer present", name)
		}

		return fmt.Sprintf("evaluator %s changed version", name)
	case InvalidatorDigestContract:
		return DetailContractChanged
	default:
		return string(kind) + " changed"
	}
}

// BoundBasis is the basis of one approval.
type BoundBasis struct {
	// Basis is what the approval recorded.
	Basis Basis `json:"basis"`
	// Assessment is the assessment id and version, for example `ASM-EDGE@2`.
	Assessment string `json:"assessment"`
	// Approval is the approval id.
	Approval string `json:"approval"`
}

// InvalidatedApproval is an approval that at least one possible invalidator reaches. Its assessment
// is review-required with the invalidators named.
type InvalidatedApproval struct {
	Assessment    string         `json:"assessment"`
	Approval      string         `json:"approval"`
	State         string         `json:"state"`
	Invalidations []Invalidation `json:"invalidations"`
}

// InvalidationReport lists the approvals that a change of policy, trust authority, evaluator,
// normalization, or digest contract reaches.
type InvalidationReport struct {
	// Evaluators lists, by evaluator name, the assessments that an evaluator change possibly
	// invalidates, sorted. Never nil.
	Evaluators map[string][]string `json:"evaluators"`
	// Affected are sorted by assessment, then approval. Never nil.
	Affected []InvalidatedApproval `json:"affected"`
	// Invalid are the equivalence marks that do not hold. Never nil.
	Invalid []string `json:"invalid"`
	// Normalization lists the assessments that a normalization change reaches, sorted. It is the
	// digest-impact list for a normalization upgrade. Never nil.
	Normalization []string `json:"normalization"`
}

// Invalidate evaluates every approval against the current basis.
func Invalidate(reg Registry, current Basis, approvals []BoundBasis, equivalent Equivalent) InvalidationReport {
	report := InvalidationReport{
		Affected: []InvalidatedApproval{}, Invalid: []string{}, Normalization: []string{}, Evaluators: map[string][]string{},
	}

	for _, approval := range approvals {
		found, invalid := Invalidators(reg, approval.Basis, current, equivalent)

		for _, text := range invalid {
			report.Invalid = appendUnique(report.Invalid, text)
		}

		if len(found) > 0 {
			report.addAffected(&approval, found)
		}
	}

	report.sortAll()

	return report
}

// addAffected records an approval that at least one invalidator reaches.
func (r *InvalidationReport) addAffected(approval *BoundBasis, found []Invalidation) {
	r.Affected = append(r.Affected, InvalidatedApproval{
		Assessment: approval.Assessment, Approval: approval.Approval, State: StateReviewRequired, Invalidations: found,
	})

	for _, inv := range found {
		switch inv.Kind {
		case InvalidatorNormalization:
			r.Normalization = appendUnique(r.Normalization, approval.Assessment)
		case InvalidatorEvaluator:
			r.Evaluators[inv.Name] = appendUnique(r.Evaluators[inv.Name], approval.Assessment)
		case InvalidatorPolicy, InvalidatorTrustAuthority, InvalidatorDigestContract:
		}
	}
}

// sortAll puts every list of the report into its documented order.
func (r *InvalidationReport) sortAll() {
	slices.SortFunc(r.Affected, func(a, b InvalidatedApproval) int {
		return cmp.Or(canonjson.CompareUTF16(a.Assessment, b.Assessment), canonjson.CompareUTF16(a.Approval, b.Approval))
	})
	slices.SortFunc(r.Invalid, canonjson.CompareUTF16)
	slices.SortFunc(r.Normalization, canonjson.CompareUTF16)

	for _, list := range r.Evaluators {
		slices.SortFunc(list, canonjson.CompareUTF16)
	}
}

func appendUnique(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}

	return append(list, s)
}

// Use is a selector facet or dependency kind that an approval's evidence used.
type Use struct {
	// Assessment is the assessment id and version.
	Assessment string `json:"assessment"`
	// Kind is the dependency kind.
	Kind Kind `json:"kind"`
	// Identity is the identity of the approved dependency item. May be empty for a selector use.
	Identity string `json:"identity,omitempty"`
	// Facets are the facet names that the selector of the dependency uses.
	Facets []string `json:"facets,omitempty"`
}

// DependencyUnknown is the state of a dependency after a capability loss.
const DependencyUnknown = "unknown"

// Regression is one capability loss that touches an approved dependency: the profile of this run
// no longer supports the kind or a selector facet that the approval's evidence used.
type Regression struct {
	Assessment string `json:"assessment"`
	Kind       Kind   `json:"kind"`
	Identity   string `json:"identity,omitempty"`
	// Facet is the lost selector facet. Empty when the whole kind is lost.
	Facet string `json:"facet,omitempty"`
	// Profile is the capability profile of this run.
	Profile string `json:"profile"`
	// Reason is ReasonUnsupportedFamily for a lost kind and ReasonCoverageIncomplete for a lost
	// selector facet.
	Reason string `json:"reason"`
	// Dependency is DependencyUnknown: the claim about this dependency cannot be told.
	Dependency string `json:"dependency"`
	// Review is StateReviewRequired: the assessment shows that a review applies.
	Review string `json:"review"`
	// Detail says what was lost.
	Detail string `json:"detail"`
}

// Trigger gives the report form of the regression: class removal-or-capability-loss, change
// `removed`, action `get review`.
func (r Regression) Trigger() ReviewTrigger { //nolint:gocritic // a report value; a pointer receiver would not suit the slices of values.
	dependency := DependencyName(r.Kind, cmp.Or(r.Identity, "*"))

	return ReviewTrigger{
		Class: ClassRemoval, Change: ChangeRemoved, Dependency: dependency,
		Assumption: []string{}, Assessment: r.Assessment, Path: []string{dependency, r.Assessment},
		Evidence: []string{dependency}, Related: []Related{}, Action: ActionGetReview,
	}
}

// CapabilityRegressions compares what the approvals used with the profile of this run. It returns
// one regression for each use whose kind, or selector facet, the profile does not support, sorted
// by assessment, kind, identity, facet. Never nil. The dependencies it names are `unknown` and
// every affected assessment is shown as needing review; a regression is never read as no change.
func CapabilityRegressions(profile CapabilityProfile, uses []Use) []Regression {
	out := []Regression{}

	for _, use := range uses {
		if !profile.Supports(use.Kind) {
			out = append(out, lostKindRegression(profile, use))

			continue
		}

		out = append(out, lostFacetRegressions(profile, use)...)
	}

	slices.SortFunc(out, func(a, b Regression) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Assessment, b.Assessment),
			compareKey(a.Kind, a.Identity, b.Kind, b.Identity),
			canonjson.CompareUTF16(a.Facet, b.Facet),
		)
	})

	return slices.CompactFunc(out, func(a, b Regression) bool {
		return a.Assessment == b.Assessment && a.Kind == b.Kind && a.Identity == b.Identity && a.Facet == b.Facet
	})
}

// lostKindRegression is the regression for a use whose whole kind the profile does not support.
func lostKindRegression(profile CapabilityProfile, use Use) Regression {
	return Regression{
		Assessment: use.Assessment, Kind: use.Kind, Identity: use.Identity, Profile: profile.ID,
		Reason: ReasonUnsupportedFamily, Dependency: DependencyUnknown, Review: StateReviewRequired,
		Detail: fmt.Sprintf("profile %s no longer supports the kind %s", profile.ID, use.Kind),
	}
}

// lostFacetRegressions gives one regression for each selector facet of the use that the profile
// does not support.
func lostFacetRegressions(profile CapabilityProfile, use Use) []Regression {
	var out []Regression

	for _, facet := range use.Facets {
		if profile.SupportsSelector(use.Kind, facet) {
			continue
		}

		out = append(out, Regression{
			Assessment: use.Assessment, Kind: use.Kind, Identity: use.Identity, Facet: facet, Profile: profile.ID,
			Reason: ReasonCoverageIncomplete, Dependency: DependencyUnknown, Review: StateReviewRequired,
			Detail: fmt.Sprintf("profile %s no longer supports the selector %s on %s", profile.ID, facet, use.Kind),
		})
	}

	return out
}

// RegressionEntries gives the dependencies of the regressions as unknown entries for Diff-style
// reporting, sorted and de-duplicated by kind and identity.
func RegressionEntries(regressions []Regression) []Entry {
	out := make([]Entry, 0, len(regressions))

	for i := range regressions {
		r := &regressions[i]
		out = append(out, Entry{Kind: r.Kind, Identity: r.Identity, Reason: r.Reason})
	}

	slices.SortFunc(out, func(a, b Entry) int { return compareKey(a.Kind, a.Identity, b.Kind, b.Identity) })

	return slices.CompactFunc(out, func(a, b Entry) bool { return a.Kind == b.Kind && a.Identity == b.Identity })
}

// RegressedAssessments gives the assessments that the regressions affect, sorted.
func RegressedAssessments(regressions []Regression) []string {
	out := []string{}

	for i := range regressions {
		out = appendUnique(out, regressions[i].Assessment)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}
