package impact_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/impact"
)

// contractWithout gives a contract that lists the fields of base except one.
func contractWithout(base impact.Contract, version string, kind impact.Kind, drop string) impact.Contract {
	next := impact.Contract{Version: version, Fields: map[impact.Kind][]string{}}

	for k, fields := range base.Fields {
		next.Fields[k] = slices.DeleteFunc(slices.Clone(fields), func(f string) bool { return k == kind && f == drop })
	}

	return next
}

// contractWith gives a contract that lists the fields of base plus one.
func contractWith(base impact.Contract, version string, kind impact.Kind, add string) impact.Contract {
	next := impact.Contract{Version: version, Fields: map[impact.Kind][]string{}}
	for k, fields := range base.Fields {
		next.Fields[k] = slices.Clone(fields)
	}

	next.Fields[kind] = append(next.Fields[kind], add)

	return next
}

// assertDroppedFieldAffectsTwo checks the two approvals bound under the earlier contract.
func assertDroppedFieldAffectsTwo(t *testing.T, report *impact.DigestImpactReport) {
	t.Helper()

	requireEq(t, "affected", len(report.Affected), 2)
	checkEq(t, "first affected", report.Affected[0].Assessment, "ASM-DATA@1")
	checkEq(t, "second affected", report.Affected[1].Assessment, "ASM-EDGE@2")

	for _, a := range report.Affected {
		checkEq(t, "affected detail", a.Detail, "digest contract changed")
		checkEq(t, "affected bound contract", a.BoundContract, impact.ContractVersion)
	}
}

// A28: a version that drops a security field is not equivalent and reports each affected approval;
// an add-only version marked equivalent by a fixture is accepted.
func TestA28DroppedFieldIsNotEquivalent(t *testing.T) {
	v1 := impact.V1()
	v2 := contractWithout(v1, "review-digest/2", impact.KindPublicEntryPoint, "exposureClass")
	reg := impact.NewRegistry(v1, v2)

	same, err := reg.EquivalentFrom("review-digest/2", impact.ContractVersion)
	requireEq(t, "same without a mark", same, false)
	mustOK(t, err)

	bound := []impact.Bound{
		{Assessment: "ASM-EDGE@2", Approval: "ASM-EDGE-v2-1", Contract: impact.ContractVersion},
		{Assessment: "ASM-DATA@1", Approval: "ASM-DATA-v1-1", Contract: impact.ContractVersion},
		{Assessment: "ASM-NEW@1", Approval: "ASM-NEW-v1-1", Contract: "review-digest/2"},
	}

	report := reg.DigestImpact("review-digest/2", bound)
	assertDroppedFieldAffectsTwo(t, &report)
	checkEq(t, "equivalent", len(report.Equivalent), 0)
	checkEq(t, "invalid", len(report.Invalid), 0)

	// A fixture that leaves the dropped field empty gives equal digests, but the mark still fails:
	// the new contract drops a field.
	v2.Equivalent = []impact.Equivalence{{From: impact.ContractVersion, Fixtures: emptyFieldFixtures(v1, impact.KindPublicEntryPoint, "exposureClass")}}
	reg = impact.NewRegistry(v1, v2)

	same, err = reg.EquivalentFrom("review-digest/2", impact.ContractVersion)
	checkEq(t, "same with a dropping mark", same, false)
	checkErrIs(t, err, impact.ErrEquivalenceInvalid)
	checkContains(t, fmt.Sprint(err), "exposureClass")

	report = reg.DigestImpact("review-digest/2", bound)
	checkEq(t, "affected", len(report.Affected), 2)
	checkEq(t, "invalid", len(report.Invalid), 1)
}

func emptyFieldFixtures(c impact.Contract, kind impact.Kind, empty string) []impact.Item {
	out := fixtures(c)

	for i, item := range out {
		if item.Kind == kind {
			delete(item.Facets, empty)
			out[i] = item
		}
	}

	return out
}

func TestA28AddOnlyVersionWithFixtureIsAccepted(t *testing.T) {
	v1 := impact.V1()
	v2 := contractWith(v1, "review-digest/2", impact.KindPublicEntryPoint, "tlsMode")
	v2.Equivalent = []impact.Equivalence{{From: impact.ContractVersion, Fixtures: fixtures(v1)}}
	reg := impact.NewRegistry(v1, v2)

	same, err := reg.EquivalentFrom("review-digest/2", impact.ContractVersion)
	requireEq(t, "same", same, true)
	mustOK(t, err)

	bound := []impact.Bound{{Assessment: "ASM-EDGE@2", Approval: "ASM-EDGE-v2-1", Contract: impact.ContractVersion}}
	report := reg.DigestImpact("review-digest/2", bound)

	requireEq(t, "affected", len(report.Affected), 0)
	requireEq(t, "equivalent", len(report.Equivalent), 1)
	requireEq(t, "invalid", len(report.Invalid), 0)

	// Same version needs no mark and is not listed.
	same, _ = reg.EquivalentFrom("review-digest/2", "review-digest/2")
	checkTrue(t, same, "a version is equivalent to itself")

	// The mark does not run backwards.
	same, _ = reg.EquivalentFrom(impact.ContractVersion, "review-digest/2")
	checkTrue(t, !same, "equivalence must not be symmetric")
}

// assertMarkRejected checks that the registry refuses the equivalence mark of v2 against v1 and, when
// wantText is set, that the error says why.
func assertMarkRejected(t *testing.T, v1, v2 impact.Contract, wantText string) {
	t.Helper()

	same, err := impact.NewRegistry(v1, v2).EquivalentFrom("review-digest/2", impact.ContractVersion)
	checkEq(t, "same", same, false)
	checkErrIs(t, err, impact.ErrEquivalenceInvalid)

	if wantText != "" {
		checkContains(t, fmt.Sprint(err), wantText)
	}
}

// assertEquivalenceChainHolds checks that equivalence follows a chain of marks and breaks with the first.
func assertEquivalenceChainHolds(t *testing.T, v1 impact.Contract) {
	t.Helper()

	v2 := contractWith(v1, "review-digest/2", impact.KindProtocol, "tlsMode")
	v2.Equivalent = []impact.Equivalence{{From: impact.ContractVersion, Fixtures: fixtures(v1)}}
	v3 := contractWith(v2, "review-digest/3", impact.KindProtocol, "alpn")
	v3.Equivalent = []impact.Equivalence{{From: "review-digest/2", Fixtures: fixtures(v1)}}

	same, err := impact.NewRegistry(v1, v2, v3).EquivalentFrom("review-digest/3", impact.ContractVersion)
	checkEq(t, "same", same, true)
	mustOK(t, err)

	// A broken first step breaks the chain.
	v2.Equivalent = nil
	same, _ = impact.NewRegistry(v1, v2, v3).EquivalentFrom("review-digest/3", impact.ContractVersion)
	checkTrue(t, !same, "chain held without a mark on the first step")
}

// assertEquivalenceCycleEnds checks that marks that only point at each other are not equivalent to v1.
func assertEquivalenceCycleEnds(t *testing.T, v1 impact.Contract) {
	t.Helper()

	a := contractWith(v1, "a", impact.KindProtocol, "x")
	b := contractWith(v1, "b", impact.KindProtocol, "x")
	a.Equivalent = []impact.Equivalence{{From: "b", Fixtures: fixtures(v1)}}
	b.Equivalent = []impact.Equivalence{{From: "a", Fixtures: fixtures(v1)}}

	same, _ := impact.NewRegistry(v1, a, b).EquivalentFrom("a", impact.ContractVersion)
	checkTrue(t, !same, "a cycle without a path to v1 is not equivalent")
}

func TestEquivalenceClaimsAreChecked(t *testing.T) {
	v1 := impact.V1()

	tests := []struct {
		name     string
		from     string
		fixtures func(v2 impact.Contract) []impact.Item
		wantText string
	}{
		{"fixture misses a kind", impact.ContractVersion, func(impact.Contract) []impact.Item { return fixtures(v1)[:3] }, ""},
		// The fixtures of the new contract carry a value for the added field.
		{"fixture shows different digests", impact.ContractVersion, fixtures, "different digests"},
		{"unknown earlier version", "review-digest/0", func(impact.Contract) []impact.Item { return fixtures(v1) }, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v2 := contractWith(v1, "review-digest/2", impact.KindProtocol, "tlsMode")
			v2.Equivalent = []impact.Equivalence{{From: tt.from, Fixtures: tt.fixtures(v2)}}

			assertMarkRejected(t, v1, v2, tt.wantText)
		})
	}

	t.Run("chain of marks", func(t *testing.T) { assertEquivalenceChainHolds(t, v1) })
	t.Run("cycle of marks ends", func(t *testing.T) { assertEquivalenceCycleEnds(t, v1) })
}
