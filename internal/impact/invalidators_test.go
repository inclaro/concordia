package impact_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/impact"
)

func baseBasis() impact.Basis {
	return impact.Basis{
		Contract:  impact.ContractVersion,
		Policy:    "sha256:policy-1",
		Authority: "sha256:authority-1",
		Evaluators: map[string]string{
			impact.EvaluatorNormalization: "norm-1",
			"cbm":                         "0.12.1",
		},
	}
}

func invalidationKinds(list []impact.Invalidation) []impact.InvalidatorKind {
	out := make([]impact.InvalidatorKind, 0, len(list))
	for _, inv := range list {
		out = append(out, inv.Kind)
	}

	return out
}

func TestNoInvalidatorWhenNothingChanged(t *testing.T) {
	found, invalid := impact.Invalidators(impact.Builtin(), baseBasis(), baseBasis(), nil)
	checkEq(t, "found", len(found), 0)
	checkEq(t, "invalid", len(invalid), 0)
}

// Scenario: A28 digest contract removes a security field.
func TestA28ContractChangeIsReviewRequired(t *testing.T) {
	v1 := impact.V1()
	v2 := contractWithout(v1, "review-digest/2", impact.KindPublicEntryPoint, "exposureClass")
	reg := impact.NewRegistry(v1, v2)

	current := baseBasis()
	current.Contract = "review-digest/2"

	report := impact.Invalidate(reg, current, []impact.BoundBasis{
		{Assessment: "ASM-EDGE@2", Approval: "ASM-EDGE-v2-1", Basis: baseBasis()},
	}, nil)

	if len(report.Affected) != 1 {
		t.Fatalf("affected = %+v", report.Affected)
	}

	got := report.Affected[0]
	checkEq(t, "state", got.State, impact.StateReviewRequired)
	checkEq(t, "assessment", got.Assessment, "ASM-EDGE@2")

	if len(got.Invalidations) != 1 {
		t.Fatalf("invalidations = %+v", got.Invalidations)
	}

	inv := got.Invalidations[0]
	checkEq(t, "kind", inv.Kind, impact.InvalidatorDigestContract)
	checkEq(t, "detail", inv.Detail, "digest contract changed")
	checkEq(t, "bound", inv.Bound, impact.ContractVersion)
}

// Scenario: A28, the marked equivalent version keeps the approval.
func TestEquivalentContractIsNotAnInvalidator(t *testing.T) {
	v1 := impact.V1()
	v2 := contractWith(v1, "review-digest/2", impact.KindPublicEntryPoint, "tlsMode")
	v2.Equivalent = []impact.Equivalence{{From: impact.ContractVersion, Fixtures: fixtureItems(t)}}

	current := baseBasis()
	current.Contract = "review-digest/2"

	found, invalid := impact.Invalidators(impact.NewRegistry(v1, v2), baseBasis(), current, nil)
	checkEq(t, "found", len(found), 0)
	checkEq(t, "invalid", len(invalid), 0)
}

// Scenario: Normalization upgrade without equivalence.
func TestNormalizationUpgradeListsAffectedAssessments(t *testing.T) {
	current := baseBasis()
	current.Evaluators = map[string]string{impact.EvaluatorNormalization: "norm-2", "cbm": "0.12.1"}

	approvals := []impact.BoundBasis{
		{Assessment: "ASM-B@1", Approval: "B-1", Basis: baseBasis()},
		{Assessment: "ASM-A@3", Approval: "A-3", Basis: baseBasis()},
	}

	report := impact.Invalidate(impact.Builtin(), current, approvals, nil)

	checkSlice(t, "normalization list", report.Normalization, []string{"ASM-A@3", "ASM-B@1"})

	if len(report.Affected) != 2 {
		t.Fatalf("affected = %+v", report.Affected)
	}

	checkEq(t, "first affected assessment", report.Affected[0].Assessment, "ASM-A@3")

	inv := report.Affected[0].Invalidations
	if len(inv) != 1 {
		t.Fatalf("invalidations = %+v", inv)
	}

	checkEq(t, "kind", inv[0].Kind, impact.InvalidatorNormalization)
	checkEq(t, "bound", inv[0].Bound, "norm-1")
	checkEq(t, "current", inv[0].Current, "norm-2")

	// With an equivalence mark for exactly this change, the approvals stay.
	marked := func(kind impact.InvalidatorKind, _, from, to string) bool {
		return kind == impact.InvalidatorNormalization && from == "norm-1" && to == "norm-2"
	}

	remaining := impact.Invalidate(impact.Builtin(), current, approvals, marked)
	checkEq(t, "affected by a marked normalization change", len(remaining.Affected), 0)
}

// Scenario: Evaluator version change.
func TestEvaluatorVersionChangeListsDependentAssessments(t *testing.T) {
	current := baseBasis()
	current.Evaluators = map[string]string{impact.EvaluatorNormalization: "norm-1", "cbm": "0.13.0"}

	unrelated := baseBasis()
	unrelated.Evaluators = map[string]string{impact.EvaluatorNormalization: "norm-1"}

	report := impact.Invalidate(impact.Builtin(), current, []impact.BoundBasis{
		{Assessment: "ASM-1@1", Approval: "P-1", Basis: baseBasis()},
		{Assessment: "ASM-2@1", Approval: "P-2", Basis: unrelated},
	}, nil)

	checkSlice(t, "cbm assessments", report.Evaluators["cbm"], []string{"ASM-1@1"})

	if len(report.Affected) != 1 {
		t.Fatalf("report = %+v", report)
	}

	inv := report.Affected[0].Invalidations[0]
	checkEq(t, "kind", inv.Kind, impact.InvalidatorEvaluator)
	checkEq(t, "name", inv.Name, "cbm")

	gone := baseBasis()
	gone.Evaluators = map[string]string{impact.EvaluatorNormalization: "norm-1"}

	found, _ := impact.Invalidators(impact.Builtin(), baseBasis(), gone, nil)
	checkSlice(t, "kinds of a missing evaluator", invalidationKinds(found), []impact.InvalidatorKind{impact.InvalidatorEvaluator})
}

func TestPolicyAndTrustAuthorityChangesAreInvalidators(t *testing.T) {
	current := baseBasis()
	current.Policy = "sha256:policy-2"
	current.Authority = "sha256:authority-2"

	found, _ := impact.Invalidators(impact.Builtin(), baseBasis(), current, nil)

	checkSlice(t, "kinds", invalidationKinds(found), []impact.InvalidatorKind{impact.InvalidatorPolicy, impact.InvalidatorTrustAuthority})

	// A value that the approval did not record is not compared.
	bound := baseBasis()
	bound.Policy, bound.Authority = "", ""

	unrecorded, _ := impact.Invalidators(impact.Builtin(), bound, current, nil)
	checkEq(t, "invalidators of unrecorded values", len(unrecorded), 0)
}

func TestInvalidatorsOrderIsStable(t *testing.T) {
	current := impact.Basis{
		Contract: "review-digest/9", Policy: "p2", Authority: "a2",
		Evaluators: map[string]string{impact.EvaluatorNormalization: "n2", "zeta": "2", "alpha": "2"},
	}
	bound := impact.Basis{
		Contract: impact.ContractVersion, Policy: "p1", Authority: "a1",
		Evaluators: map[string]string{impact.EvaluatorNormalization: "n1", "zeta": "1", "alpha": "1"},
	}

	found, _ := impact.Invalidators(impact.Builtin(), bound, current, nil)

	want := []impact.InvalidatorKind{
		impact.InvalidatorPolicy, impact.InvalidatorTrustAuthority, impact.InvalidatorEvaluator,
		impact.InvalidatorEvaluator, impact.InvalidatorNormalization, impact.InvalidatorDigestContract,
	}
	checkSlice(t, "kinds", invalidationKinds(found), want)

	if len(found) != len(want) {
		t.Fatalf("found = %+v", found)
	}

	checkEq(t, "first evaluator", found[2].Name, "alpha")
	checkEq(t, "second evaluator", found[3].Name, "zeta")
}

// Scenario: A27 provider upgrade loses selector support.
func TestA27SelectorSupportLossIsCoverageRegression(t *testing.T) {
	profile := impact.CapabilityProfile{
		ID:        "cbm-0.13.0",
		Kinds:     []impact.Kind{impact.KindPublicEntryPoint, impact.KindSecretReference},
		Selectors: map[impact.Kind][]string{impact.KindPublicEntryPoint: {"host"}},
	}

	regressions := impact.CapabilityRegressions(profile, []impact.Use{
		{Assessment: "ASM-EDGE@2", Kind: impact.KindPublicEntryPoint, Identity: "prod/ingress/web", Facets: []string{"host", "path"}},
		{Assessment: "ASM-EDGE@2", Kind: impact.KindSecretReference, Identity: "prod/secret/db"},
	})

	if len(regressions) != 1 {
		t.Fatalf("regressions = %+v", regressions)
	}

	got := regressions[0]
	checkEq(t, "assessment", got.Assessment, "ASM-EDGE@2")
	checkEq(t, "kind", got.Kind, impact.KindPublicEntryPoint)
	checkEq(t, "facet", got.Facet, "path")
	checkEq(t, "dependency", got.Dependency, "unknown")
	checkEq(t, "review", got.Review, impact.StateReviewRequired)
	checkEq(t, "reason", got.Reason, impact.ReasonCoverageIncomplete)
	checkEq(t, "profile", got.Profile, "cbm-0.13.0")

	entries := impact.RegressionEntries(regressions)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}

	checkTrue(t, entries[0].Reason != "", "the entry has no reason")
	checkEq(t, "entry digest", entries[0].Digest, "")
	checkSlice(t, "assessments", impact.RegressedAssessments(regressions), []string{"ASM-EDGE@2"})
}

func TestLostKindIsUnsupportedFamilyAndSupportedUseHasNoRegression(t *testing.T) {
	profile := impact.CapabilityProfile{ID: "p", Kinds: []impact.Kind{impact.KindProtocol}, Selectors: map[impact.Kind][]string{impact.KindProtocol: {"port"}}}

	got := impact.CapabilityRegressions(profile, []impact.Use{
		{Assessment: "ASM-1@1", Kind: impact.KindCertificateReference, Identity: "tls"},
		{Assessment: "ASM-1@1", Kind: impact.KindProtocol, Identity: "https", Facets: []string{"port"}},
	})

	if len(got) != 1 {
		t.Fatalf("regressions = %+v", got)
	}

	checkEq(t, "reason", got[0].Reason, impact.ReasonUnsupportedFamily)
	checkEq(t, "facet", got[0].Facet, "")
	checkTrue(t, impact.CapabilityRegressions(profile, nil) != nil, "an empty result must not be nil")
}

// fixtureItems gives one item of every kind with every contract facet set, for an equivalence mark.
func fixtureItems(t *testing.T) []impact.Item {
	t.Helper()

	v1 := impact.V1()
	items := make([]impact.Item, 0, len(impact.Kinds()))

	for _, kind := range impact.Kinds() {
		facets := impact.Facets{}
		for _, field := range v1.FieldsOf(kind) {
			facets[field] = []string{field + "-value"}
		}

		items = append(items, v1.NewItem(kind, "fixture-"+string(kind), facets, nil, nil))
	}

	return items
}
