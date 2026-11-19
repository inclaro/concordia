package policy_test

import (
	"reflect"
	"regexp"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

func TestCompilesForbidWithAllow(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule(`    subject: {calmNodes: [billing]}
    forbid:
      target: {calmNodes: [fulfillment]}
      edges: [IMPORTS, CALLS]
      traversal: direct
    allow:
      target:
        sourceRoots: [packages/contract/**]
        calmNodes: [db]
      when:
        any:
          - edge: INHERITS
          - targetTag: public
    rationale: why
    remediation: fix
    owner: team-a
`)))
	wantCodes(t, &res)

	p := res.Plans[0]
	if !slices.Equal(p.Subjects, []string{"billing"}) || !slices.Equal(p.Targets, []string{"fulfillment"}) {
		t.Errorf("subjects/targets = %v / %v", p.Subjects, p.Targets)
	}

	wantTraversal := policy.Traversal{EdgeKinds: kinds("calls", "imports"), MinimumDepth: 1, MaximumDepth: 1, Direction: "outbound"}
	if !reflect.DeepEqual(p.Traversal, wantTraversal) {
		t.Errorf("traversal = %+v", p.Traversal)
	}

	wantPredicate := policy.Predicate{Family: policy.FamilyBoundary, Allow: &policy.BoundaryAllow{
		TargetSourceRoots: []string{"packages/contract/**"},
		TargetCalmNodes:   []string{"db"},
		EdgeKinds:         kinds("inherits"),
		TargetTags:        []string{"public"},
	}}
	if !reflect.DeepEqual(p.Predicate, wantPredicate) {
		t.Errorf("predicate = %+v", p.Predicate)
	}

	if got := (planIdentity{p.Pack, p.Owner, p.Aggregation}); got != (planIdentity{policy.PackRef{ID: "demo", Version: "1.0.0"}, "team-a", "edge"}) {
		t.Errorf("plan = %+v", p)
	}

	assertForbidWithAllowPackRef(t, &res)
}

// planIdentity is the pack, the owner, and the aggregation of a plan.
type planIdentity struct {
	pack        policy.PackRef
	owner       string
	aggregation string
}

func assertForbidWithAllowPackRef(t *testing.T, res *policy.CompileResult) {
	t.Helper()

	ref := res.Packs[0]
	if got := [3]string{ref.ID, ref.Version, ref.Path}; got != [3]string{"demo", "1.0.0", "policies/a.policy.yaml"} {
		t.Errorf("pack = %+v", ref)
	}

	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(ref.Digest) {
		t.Errorf("digest = %q", ref.Digest)
	}
}

func TestCompilesLayerOrder(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    layerOrder:\n      layers: [{calmNodes: [billing]}, {nodeTypes: [database]}]\n      edges: [IMPORTS]\n")))
	wantCodes(t, &res)

	p := res.Plans[0]
	if !reflect.DeepEqual(p.Layers, [][]string{{"billing"}, {"db"}}) {
		t.Errorf("layers = %v", p.Layers)
	}

	if !slices.Equal(p.Subjects, []string{"billing", "db"}) || p.Predicate.Family != policy.FamilyLayer {
		t.Errorf("plan = %+v", p)
	}
}

func TestCompilesDeclaredOnlySelectorsAndRefs(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(
		rule("    subject: {ref: core-or-db}\n    declaredOnly:\n      edges: [IMPORTS]\n"),
		"selectors:\n  core-or-db:\n    tags: [core]\n    nodeTypes: [database]\n"))
	wantCodes(t, &res)

	p := res.Plans[0]
	if !slices.Equal(p.Subjects, []string{"billing", "db", "ledger"}) {
		t.Errorf("subjects = %v", p.Subjects)
	}

	if !reflect.DeepEqual(p.Predicate, policy.Predicate{Family: policy.FamilyDeclaredOnly}) {
		t.Errorf("plan = %+v", p)
	}

	if len(p.Targets) != 0 || p.Targets == nil {
		t.Errorf("plan = %+v", p)
	}
}

func TestCompilesObservedRequiredWithTheSupportedEvidenceKinds(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {nodeTypes: [service]}\n    observedRequired: {}\n")))
	wantCodes(t, &res)

	p := res.Plans[0]
	if p.Predicate.Family != policy.FamilyObservedRequired {
		t.Errorf("predicate = %+v", p.Predicate)
	}

	want := kinds("ambiguous-usage", "calls", "imports", "inherits", "reads", "references-callable", "writes")
	if !slices.Equal(p.Traversal.EdgeKinds, want) || len(p.UnsupportedEdgeKinds) != 0 {
		t.Errorf("traversal = %v, unsupported = %v", p.Traversal.EdgeKinds, p.UnsupportedEdgeKinds)
	}
}

func TestCompilesAcyclic(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {nodeTypes: [service]}\n    require:\n      graphProperty: acyclic\n      edges: [IMPORTS]\n      collapseBy: architectureBinding\n")))
	wantCodes(t, &res)

	want := policy.Predicate{Family: policy.FamilyAcyclic, CollapseBy: "architectureBinding"}
	if !reflect.DeepEqual(res.Plans[0].Predicate, want) {
		t.Errorf("predicate = %+v", res.Plans[0].Predicate)
	}
}

func TestASelectorThatMatchesNoNodeGivesAnEmptySubject(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {tags: [nope]}\n    declaredOnly:\n      edges: [IMPORTS]\n")))
	wantCodes(t, &res)

	if s := res.Plans[0].Subjects; s == nil || len(s) != 0 {
		t.Errorf("subjects = %#v", s)
	}
}

func TestFamilyAndEnforcementAreValidOnAnyRule(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    family: layering\n    enforcement: shadow\n    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n")))
	wantCodes(t, &res)

	p := res.Plans[0]
	if p.Family != "layering" || p.Enforcement != policy.EnforcementShadow {
		t.Errorf("plan = %+v", p)
	}

	if p.Obligation != nil {
		t.Errorf("plan = %+v", p)
	}
}

func TestCompilesPrecedence(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"),
		"precedence:\n  version: 3\n  order: [CO-X-002, CO-X-001]\n"))
	wantCodes(t, &res)

	want := &policy.Precedence{Version: 3, Order: []string{"CO-X-002", "CO-X-001"}}
	if got := res.Packs[0].Precedence; !reflect.DeepEqual(got, want) {
		t.Errorf("precedence = %+v, want %+v", got, want)
	}

	if got := want.Rank("CO-X-001"); got != 1 || want.Rank("CO-X-009") != -1 {
		t.Errorf("rank = %d", got)
	}

	if plain := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"))); plain.Packs[0].Precedence != nil {
		t.Errorf("precedence = %+v, want none", plain.Packs[0].Precedence)
	}
}

func TestRejectsAnInvalidPrecedenceBlock(t *testing.T) {
	t.Parallel()

	for name, extra := range map[string]string{
		"no version":  "precedence:\n  order: [CO-X-001]\n",
		"version 0":   "precedence:\n  version: 0\n  order: [CO-X-001]\n",
		"empty order": "precedence:\n  version: 1\n  order: []\n",
		"duplicate":   "precedence:\n  version: 1\n  order: [CO-X-001, CO-X-001]\n",
		"bad id":      "precedence:\n  version: 1\n  order: [rule-one]\n",
		"unknown key": "precedence:\n  version: 1\n  order: [CO-X-001]\n  mode: first\n",
	} {
		res := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"), extra))
		if !rejectedAt(&res, diag.POL001, "") {
			t.Errorf("%s: plans %d, diagnostics %+v", name, len(res.Plans), res.Diagnostics)
		}
	}
}
