package policy_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

func ref(id, rule string, typ policy.ObligationType, applies *policy.SelectorExpr) policy.ObligationRef {
	return policy.ObligationRef{
		ObligationID: id, RuleID: rule, Subject: "svc-s", Environment: "production", Type: typ, Applies: applies,
		Relation: policy.RelationSpec{Kind: observation.KindRoutesTo, Direction: policy.DirectionToSubject, Target: []string{}},
	}
}

func TestPolarity(t *testing.T) {
	t.Parallel()

	for typ, want := range map[policy.ObligationType]policy.Polarity{
		policy.ObligationRequired: policy.PolarityAffirmative, policy.ObligationAllowed: policy.PolarityAffirmative,
		policy.ObligationForbidden: policy.PolarityNegative,
	} {
		if got := typ.Polarity(); got != want {
			t.Errorf("%s = %s, want %s", typ, got, want)
		}
	}
}

func TestRelationKeyIsStableAndOrderIndependent(t *testing.T) {
	t.Parallel()

	a := policy.RelationSpec{Kind: observation.KindRoutesTo, Direction: policy.DirectionToSubject, Target: []string{"b", "a"}}
	b := policy.RelationSpec{Kind: observation.KindRoutesTo, Direction: policy.DirectionToSubject, Target: []string{"a", "b"}}

	if policy.RelationKey(&a) != policy.RelationKey(&b) {
		t.Fatal("the target order changes the relation key")
	}

	c := a
	c.Direction = policy.DirectionFromSubject

	if policy.RelationKey(&a) == policy.RelationKey(&c) {
		t.Fatal("the direction is part of the relation key")
	}
}

func TestPOL05ContradictingRulesConflict(t *testing.T) {
	t.Parallel()

	// Rule A requires an ingress route for service S; rule B forbids ingress routes for S in the same environment.
	report := policy.DetectConflicts([]policy.ObligationRef{
		ref("o-a", "A", policy.ObligationRequired, nil),
		ref("o-b", "B", policy.ObligationForbidden, nil),
	}, nil)

	if len(report.Conflicts) != 1 {
		t.Fatalf("report = %+v", report)
	}

	if len(report.Losses) != 0 {
		t.Fatalf("report = %+v", report)
	}

	c := report.Conflicts[0]

	got := [4]string{c.Affirmative, c.Negative, c.Subject, c.Environment}
	if want := [4]string{"A", "B", "svc-s", "production"}; got != want {
		t.Fatalf("conflict = %+v", c)
	}
}

func TestAllowedAgainstForbiddenConflicts(t *testing.T) {
	t.Parallel()

	report := policy.DetectConflicts([]policy.ObligationRef{
		ref("o-a", "A", policy.ObligationAllowed, nil),
		ref("o-b", "B", policy.ObligationForbidden, nil),
	}, nil)

	if len(report.Conflicts) != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestSamePolarityRulesDoNotConflict(t *testing.T) {
	t.Parallel()

	report := policy.DetectConflicts([]policy.ObligationRef{
		ref("o-a", "A", policy.ObligationRequired, nil),
		ref("o-b", "B", policy.ObligationAllowed, nil),
		ref("o-c", "C", policy.ObligationForbidden, nil),
	}, nil)

	// Only the forbidden rule contradicts the other two.
	if len(report.Conflicts) != 2 {
		t.Fatalf("conflicts = %+v", report.Conflicts)
	}

	none := policy.DetectConflicts([]policy.ObligationRef{
		ref("o-a", "A", policy.ObligationRequired, nil), ref("o-b", "B", policy.ObligationAllowed, nil),
	}, nil)
	if len(none.Conflicts) != 0 {
		t.Fatalf("required and allowed do not contradict: %+v", none)
	}
}

func TestDifferentSubjectsEnvironmentsAndRelationsDoNotConflict(t *testing.T) {
	t.Parallel()

	base := ref("o-a", "A", policy.ObligationRequired, nil)

	for name, mutate := range map[string]func(*policy.ObligationRef){
		"subject":     func(r *policy.ObligationRef) { r.Subject = "svc-other" },
		"environment": func(r *policy.ObligationRef) { r.Environment = "staging" },
		"kind":        func(r *policy.ObligationRef) { r.Relation.Kind = observation.KindUsesImage },
		"direction":   func(r *policy.ObligationRef) { r.Relation.Direction = policy.DirectionFromSubject },
		"target":      func(r *policy.ObligationRef) { r.Relation.Target = []string{"x"} },
	} {
		other := ref("o-b", "B", policy.ObligationForbidden, nil)

		if name == "target" {
			base.Relation.Target = []string{"y"}
		}

		mutate(&other)

		if report := policy.DetectConflicts([]policy.ObligationRef{base, other}, nil); len(report.Conflicts) != 0 {
			t.Errorf("%s: %+v", name, report.Conflicts)
		}

		base.Relation.Target = []string{}
	}
}

func TestOverlappingTargetsConflict(t *testing.T) {
	t.Parallel()

	a := ref("o-a", "A", policy.ObligationRequired, nil)
	a.Relation.Target = []string{"x", "y"}
	b := ref("o-b", "B", policy.ObligationForbidden, nil)
	b.Relation.Target = []string{"y"}

	if report := policy.DetectConflicts([]policy.ObligationRef{a, b}, nil); len(report.Conflicts) != 1 {
		t.Fatalf("report = %+v", report)
	}

	b.Relation.Target = []string{} // no target: any target
	if report := policy.DetectConflicts([]policy.ObligationRef{a, b}, nil); len(report.Conflicts) != 1 {
		t.Fatalf("an empty target overlaps any: %+v", report)
	}
}

func TestPrecedenceResolvesAndTheLoserStaysVisible(t *testing.T) {
	t.Parallel()

	refs := []policy.ObligationRef{ref("o-a", "A", policy.ObligationRequired, nil), ref("o-b", "B", policy.ObligationForbidden, nil)}
	// Precedence version 1 places rule B above rule A.
	report := policy.DetectConflicts(refs, []policy.NamedPrecedence{{ID: "pack-x", Precedence: policy.Precedence{Version: 1, Order: []string{"B", "A"}}}})

	if len(report.Conflicts) != 0 {
		t.Fatalf("report = %+v", report)
	}

	if len(report.Losses) != 1 {
		t.Fatalf("report = %+v", report)
	}

	loss := report.Losses[0]

	if got := [2]string{loss.ObligationID, loss.RuleID}; got != [2]string{"o-a", "A"} {
		t.Fatalf("loss = %+v", loss)
	}

	if want := (policy.Supersession{PrecedenceID: "pack-x", PrecedenceVersion: 1, RuleID: "B"}); loss.By != want {
		t.Fatalf("loss = %+v", loss)
	}

	requireReversedPrecedenceSwapsTheLoser(t, refs)
}

// requireReversedPrecedenceSwapsTheLoser checks that the order of the rules in
// the list, not the order of the obligations, decides.
func requireReversedPrecedenceSwapsTheLoser(t *testing.T, refs []policy.ObligationRef) {
	t.Helper()

	reversed := policy.DetectConflicts(slices.Clone(refs), []policy.NamedPrecedence{{ID: "pack-x", Precedence: policy.Precedence{Version: 2, Order: []string{"A", "B"}}}})
	if len(reversed.Losses) != 1 {
		t.Fatalf("reversed = %+v", reversed)
	}

	loss := reversed.Losses[0]
	if got := [3]string{loss.RuleID, loss.By.RuleID, loss.By.PrecedenceID}; got != [3]string{"B", "A", "pack-x"} {
		t.Fatalf("reversed = %+v", reversed)
	}

	if loss.By.PrecedenceVersion != 2 {
		t.Fatalf("reversed = %+v", reversed)
	}
}

func TestPrecedenceDoesNotResolveAnUnlistedPair(t *testing.T) {
	t.Parallel()

	refs := []policy.ObligationRef{ref("o-a", "A", policy.ObligationRequired, nil), ref("o-b", "B", policy.ObligationForbidden, nil)}

	for _, order := range [][]string{{"B"}, {"B", "C"}, {"A", "C"}, {}} {
		report := policy.DetectConflicts(refs, []policy.NamedPrecedence{{ID: "p", Precedence: policy.Precedence{Version: 1, Order: order}}})
		if len(report.Conflicts) != 1 || len(report.Losses) != 0 {
			t.Fatalf("order %v: %+v", order, report)
		}
	}
}

func TestA02DistinctOccurrencesDoNotConflict(t *testing.T) {
	t.Parallel()

	// One rule allows HTTPS and another forbids administrative connections on the same pair.
	allow := ref("o-a", "A", policy.ObligationAllowed, policy.Match(policy.DimensionProtocol, "https"))
	forbid := ref("o-b", "B", policy.ObligationForbidden, policy.Match(policy.DimensionProtocol, "administrative"))

	if report := policy.DetectConflicts([]policy.ObligationRef{allow, forbid}, nil); len(report.Conflicts) != 0 {
		t.Fatalf("report = %+v", report)
	}

	// The same protocol is the same occurrence.
	same := ref("o-b", "B", policy.ObligationForbidden, policy.Match(policy.DimensionProtocol, "https", "http"))
	if report := policy.DetectConflicts([]policy.ObligationRef{allow, same}, nil); len(report.Conflicts) != 1 {
		t.Fatalf("overlapping protocols conflict: %+v", report)
	}
}

func TestDisjoint(t *testing.T) {
	t.Parallel()

	https := policy.Match(policy.DimensionProtocol, "https")
	admin := policy.Match(policy.DimensionProtocol, "administrative")
	prod := policy.Match(policy.DimensionEnvironment, "production")

	for _, tc := range []struct { //nolint:govet // a test table.
		name string
		a, b *policy.SelectorExpr
		want bool
	}{
		{"nil applies to everything", nil, https, false},
		{"leaves with no common value", https, admin, true},
		{"leaves with a common value", policy.Match(policy.DimensionProtocol, "https", "grpc"), policy.Match(policy.DimensionProtocol, "grpc"), false},
		{"different dimensions may overlap", https, prod, false},
		{"allOf with a disjoint operand", policy.AllOf(prod, https), admin, true},
		{"allOf with no disjoint operand", policy.AllOf(prod, https), prod, false},
		{"anyOf is disjoint only if every operand is", policy.AnyOf(https, policy.Match(policy.DimensionProtocol, "grpc")), admin, true},
		{"anyOf with an overlapping operand", policy.AnyOf(https, admin), admin, false},
		{"not of a leaf against a leaf that implies it", policy.Not(https), https, true},
		{"not of a wider leaf", policy.Not(policy.Match(policy.DimensionProtocol, "https", "grpc")), https, true},
		{"not of a narrower leaf may overlap", policy.Not(https), policy.Match(policy.DimensionProtocol, "https", "grpc"), false},
		{"not against not may overlap", policy.Not(https), policy.Not(admin), false},
	} {
		if got := policy.Disjoint(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: Disjoint(a, b) = %v, want %v", tc.name, got, tc.want)
		}

		if got := policy.Disjoint(tc.b, tc.a); got != tc.want {
			t.Errorf("%s: Disjoint(b, a) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
