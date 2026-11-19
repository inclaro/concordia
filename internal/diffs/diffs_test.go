package diffs_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diffs"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
)

// diffsKeys projects each element of list to a comparable key.
func diffsKeys[T any](list []T, key func(*T) string) []string {
	keys := make([]string, 0, len(list))
	for i := range list {
		keys = append(keys, key(&list[i]))
	}

	return keys
}

// diffsAssertKeys fails unless the projected keys of list equal want exactly.
func diffsAssertKeys[T any](t *testing.T, label string, list []T, key func(*T) string, want ...string) {
	t.Helper()

	if got := diffsKeys(list, key); !slices.Equal(got, want) {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
}

func diffsChangeID(c *diffs.Change) string { return c.ID }

func diffsChangeType(c *diffs.Change) string { return c.Type }

func diffsPairRel(p *diffs.ObservedPair) string { return p.RelationshipID }

func diffsObservedChangeRel(c *diffs.ObservedChange) string { return c.RelationshipID }

func diffsObservedChangeTo(c *diffs.ObservedChange) string { return c.ToClassification }

func diffsRefFingerprint(r *diffs.FindingRef) string { return r.Fingerprint }

func diffsFindingChangeFingerprint(c *diffs.FindingChange) string { return c.Fingerprint }

func diffsFindingChangeTo(c *diffs.FindingChange) string { return c.To }

func diffsCoverageRule(c *diffs.CoverageChange) string { return c.RuleID }

func diffsString(s *string) string { return *s }

func TestDiffIntent(t *testing.T) {
	t.Parallel()

	from := &intent.ArchitectureModel{
		Nodes: []intent.NodeDeclaration{{ID: "a", Name: "A", Pointer: "/nodes/0"}, {ID: "gone"}},
	}
	to := &intent.ArchitectureModel{
		Nodes:         []intent.NodeDeclaration{{ID: "a", Name: "A2", Pointer: "/nodes/5"}, {ID: "new"}},
		Relationships: []intent.RelationshipDeclaration{{ID: "r"}},
	}

	got := diffs.Intent(from, to)
	diffsAssertKeys(t, "added types", got.Added, diffsChangeType, "node", "relationship")
	diffsAssertKeys(t, "added ids", got.Added[:1], diffsChangeID, "new")
	diffsAssertKeys(t, "removed", got.Removed, diffsChangeID, "gone")
	diffsAssertKeys(t, "changed", got.Changed, diffsChangeID, "a")
}

func TestDiffIntentAddedNodeIsExactlyTypeAndID(t *testing.T) {
	t.Parallel()

	from := &intent.ArchitectureModel{}
	to := &intent.ArchitectureModel{Nodes: []intent.NodeDeclaration{{ID: "new"}}}

	got := diffs.Intent(from, to)
	if len(got.Added) != 1 {
		t.Fatalf("added: %+v", got.Added)
	}

	if want := (diffs.Change{Type: "node", ID: "new"}); got.Added[0] != want {
		t.Fatalf("added: got %+v, want %+v", got.Added[0], want)
	}
}

func TestDiffIntentIgnoresPointer(t *testing.T) {
	t.Parallel()

	from := &intent.ArchitectureModel{
		Nodes: []intent.NodeDeclaration{{ID: "a", Name: "A", Pointer: "/nodes/0"}, {ID: "gone"}},
	}
	same := &intent.ArchitectureModel{Nodes: []intent.NodeDeclaration{{ID: "a", Name: "A", Pointer: "/other"}}}

	if d := diffs.Intent(from, same); len(d.Changed) != 0 {
		t.Fatalf("pointer must not count: %+v", d)
	}
}

func TestDiffIntentNilListsAreNonNil(t *testing.T) {
	t.Parallel()

	d := diffs.Intent(nil, nil)
	if d.Added == nil {
		t.Fatal("added must be non-nil")
	}

	if d.Removed == nil {
		t.Fatal("removed must be non-nil")
	}

	if d.Changed == nil {
		t.Fatal("changed must be non-nil")
	}
}

func TestDiffBindings(t *testing.T) {
	t.Parallel()

	got := diffs.Bindings(
		[]binding.Binding{{ID: "b1", Source: "x", ArchitectureRef: "a"}, {ID: "b2"}},
		[]binding.Binding{{ID: "b1", Source: "y", ArchitectureRef: "a"}, {ID: "b3"}},
	)
	diffsAssertKeys(t, "added", got.Added, diffsChangeType, "binding")
	diffsAssertKeys(t, "removed", got.Removed, diffsChangeID, "b2")
	diffsAssertKeys(t, "changed", got.Changed, diffsChangeID)
}

func TestDiffObserved(t *testing.T) {
	t.Parallel()

	from := &conformance.Run{Reconciliation: []conformance.ReconciliationEntry{
		{From: "a", To: "b", Classification: conformance.ClassConforming},
		{From: "a", To: "c", Classification: conformance.ClassUndeclared},
	}}
	to := &conformance.Run{Reconciliation: []conformance.ReconciliationEntry{
		{From: "a", To: "b", Classification: conformance.ClassContradiction},
		{From: "b", To: "c", Classification: conformance.ClassUndeclared},
	}}

	got := diffs.Observed(from, to)
	if len(got.Added) != 1 || len(got.Removed) != 1 {
		t.Fatalf("added/removed: %+v", got)
	}

	diffsAssertKeys(t, "changed", got.Changed, diffsObservedChangeTo, "contradiction")
}

func TestDiffObservedKeepsUnobservedPairsOfDifferentRelationships(t *testing.T) {
	t.Parallel()

	from := &conformance.Run{Reconciliation: []conformance.ReconciliationEntry{
		{From: "a", To: "b", RelationshipID: "r2", Classification: conformance.ClassUnobserved},
		{From: "a", To: "b", RelationshipID: "r1", Classification: conformance.ClassUnobserved},
	}}
	to := &conformance.Run{Reconciliation: []conformance.ReconciliationEntry{
		{From: "a", To: "b", RelationshipID: "r1", Classification: conformance.ClassUnobserved},
		{From: "a", To: "b", RelationshipID: "r2", Classification: conformance.ClassConforming},
		{From: "a", To: "b", RelationshipID: "r3", Classification: conformance.ClassUnobserved},
	}}

	got := diffs.Observed(from, to)
	diffsAssertKeys(t, "removed", got.Removed, diffsPairRel)
	diffsAssertKeys(t, "added", got.Added, diffsPairRel, "r3")
	diffsAssertKeys(t, "changed", got.Changed, diffsObservedChangeRel, "r2")
}

func TestDiffFindings(t *testing.T) {
	t.Parallel()

	from := &conformance.Run{
		Findings: []conformance.Finding{
			{Fingerprint: "f1", RuleID: "r", Disposition: exceptions.DispositionActive},
			{Fingerprint: "f2", RuleID: "r", Disposition: exceptions.DispositionActive},
		},
		Exceptions: conformance.RunExceptions{ResolvedBaselineEntries: []string{"f4"}},
	}
	to := &conformance.Run{Findings: []conformance.Finding{
		{Fingerprint: "f1", RuleID: "r", Disposition: exceptions.DispositionWaived},
		{Fingerprint: "f3", RuleID: "r"},
		{Fingerprint: "f4", RuleID: "r"},
	}}

	got := diffs.Findings(from, to)
	diffsAssertKeys(t, "new", got.New, diffsRefFingerprint, "f3")
	diffsAssertKeys(t, "resolved", got.Resolved, diffsRefFingerprint, "f2")
	diffsAssertKeys(t, "reintroduced", got.Reintroduced, diffsRefFingerprint, "f4")
	diffsAssertKeys(t, "changed", got.Changed, diffsFindingChangeTo, "waived")
	diffsAssertKeys(t, "changed fingerprint", got.Changed, diffsFindingChangeFingerprint, "f1")
}

func ruleWith(id string, ratio float64) conformance.RuleResult {
	r := conformance.RuleResult{RuleID: id, Plan: policy.RulePlan{}}
	r.Coverage = &conformance.Coverage{}
	r.Coverage.Files.Ratio = ratio

	return r
}

func TestDiffCoverage(t *testing.T) {
	t.Parallel()

	from := &conformance.Run{Rules: []conformance.RuleResult{ruleWith("a", 0.9), ruleWith("b", 0.5), ruleWith("c", 1)}}
	to := &conformance.Run{Rules: []conformance.RuleResult{ruleWith("a", 0.8), ruleWith("b", 0.7), ruleWith("d", 1)}}

	got := diffs.Coverage(from, to)
	diffsAssertKeys(t, "regressions", got.Regressions, diffsCoverageRule, "a")
	diffsAssertKeys(t, "improvements", got.Improvements, diffsCoverageRule, "b")
}

func TestDiffWaivers(t *testing.T) {
	t.Parallel()

	from := &conformance.Run{Exceptions: conformance.RunExceptions{Waivers: []conformance.AppliedWaiver{
		{ID: "w1", State: "active"}, {ID: "w2", State: "active"},
	}}}
	to := &conformance.Run{Exceptions: conformance.RunExceptions{Waivers: []conformance.AppliedWaiver{
		{ID: "w1", State: "expired"}, {ID: "w3", State: "active"},
	}}}

	got := diffs.Waivers(from, to)
	diffsAssertKeys(t, "expired", got.Expired, diffsString, "w1")

	if len(got.Added) != 1 || len(got.Removed) != 1 {
		t.Fatalf("added/removed: %+v", got)
	}
}
