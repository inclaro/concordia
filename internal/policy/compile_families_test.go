package policy_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

func TestCompilesDenyPathWithTheDefaultAndAnExplicitDepth(t *testing.T) {
	t.Parallel()

	res := compileBody(t, "    subject: {calmNodes: [billing]}\n    denyPath:\n      target: {calmNodes: [db]}\n      edges: [IMPORTS, CALLS]\n")
	wantCodes(t, &res)

	p := res.Plans[0]
	if p.Predicate.Family != policy.FamilyDenyPath || p.Traversal.MaximumDepth != 4 {
		t.Errorf("plan = %+v", p)
	}

	if !slices.Equal(p.Targets, []string{"db"}) {
		t.Errorf("plan = %+v", p)
	}

	res = compileBody(t, "    subject: {calmNodes: [billing]}\n    denyPath:\n      target: {calmNodes: [db]}\n      edges: [IMPORTS]\n      maxDepth: 10\n")
	wantCodes(t, &res)

	if res.Plans[0].Traversal.MaximumDepth != 10 {
		t.Errorf("maxDepth = %d", res.Plans[0].Traversal.MaximumDepth)
	}
}

// edgeFamilyCase is one rule of an edge family: its body, the family it compiles to, and the check of its plan.
type edgeFamilyCase struct {
	check  func(t *testing.T, p *policy.RulePlan)
	family policy.PredicateFamily
	body   string
}

func edgeFamilyCases() []edgeFamilyCase {
	return []edgeFamilyCase{
		{
			body:   "    subject: {calmNodes: [billing]}\n    allowOnly:\n      targets: {calmNodes: [db]}\n      edges: [IMPORTS]\n",
			family: policy.FamilyAllowOnly,
			check:  checkTargetsAreDB,
		},
		{
			body:   "    subject: {nodeTypes: [service]}\n    independent:\n      edges: [IMPORTS]\n",
			family: policy.FamilyIndependent,
			check:  checkIndependentPlan,
		},
		{
			body:   "    subject: {calmNodes: [billing]}\n    ownership:\n      owners: {calmNodes: [ledger]}\n      edges: [IMPORTS]\n",
			family: policy.FamilyOwnership,
			check:  checkOwnershipPlan,
		},
		{
			body:   "    subject: {calmNodes: [billing]}\n    publicApiOnly:\n      api: {sourceRoots: [\"b/**\", \"a/**\"]}\n      edges: [IMPORTS]\n",
			family: policy.FamilyPublicAPIOnly,
			check:  checkPublicAPIPlanWithEveryTarget,
		},
		{
			body:   "    subject: {calmNodes: [billing]}\n    publicApiOnly:\n      target: {calmNodes: [db]}\n      api: {sourceRoots: [\"a/**\"]}\n      edges: [IMPORTS]\n",
			family: policy.FamilyPublicAPIOnly,
			check:  checkTargetsAreDB,
		},
		{
			body:   "    subject: {calmNodes: [billing]}\n    require:\n      edge:\n        target: {calmNodes: [db]}\n        edges: [IMPORTS]\n",
			family: policy.FamilyRequireEdge,
			check:  checkRequireEdgePlan,
		},
		{
			body:   "    subject: {calmNodes: [billing]}\n    require:\n      entity:\n        labels: [Route, Class]\n        namePattern: '^api\\.'\n",
			family: policy.FamilyRequireEntity,
			check:  checkRequireEntityPlan,
		},
	}
}

func checkTargetsAreDB(t *testing.T, p *policy.RulePlan) {
	t.Helper()

	if !slices.Equal(p.Targets, []string{"db"}) {
		t.Errorf("targets = %v", p.Targets)
	}
}

func checkIndependentPlan(t *testing.T, p *policy.RulePlan) {
	t.Helper()

	if len(p.Targets) != 0 || p.Targets == nil {
		t.Errorf("subjects/targets = %v / %v", p.Subjects, p.Targets)
	}

	if len(p.Subjects) != 3 {
		t.Errorf("subjects/targets = %v / %v", p.Subjects, p.Targets)
	}
}

func checkOwnershipPlan(t *testing.T, p *policy.RulePlan) {
	t.Helper()

	if p.Traversal.Direction != "inbound" || !slices.Equal(p.Targets, []string{"ledger"}) {
		t.Errorf("plan = %+v", p)
	}
}

func checkPublicAPIPlanWithEveryTarget(t *testing.T, p *policy.RulePlan) {
	t.Helper()

	if !slices.Equal(p.Predicate.APISourceRoots, []string{"a/**", "b/**"}) {
		t.Errorf("plan = %+v", p)
	}

	if !slices.Equal(p.Targets, []string{"billing", "db", "fulfillment", "ledger", "user"}) {
		t.Errorf("plan = %+v", p)
	}
}

func checkRequireEdgePlan(t *testing.T, p *policy.RulePlan) {
	t.Helper()

	if !slices.Equal(p.Targets, []string{"db"}) || !slices.Equal(p.Traversal.EdgeKinds, kinds("imports")) {
		t.Errorf("plan = %+v", p)
	}
}

func checkRequireEntityPlan(t *testing.T, p *policy.RulePlan) {
	t.Helper()

	want := policy.Predicate{Family: policy.FamilyRequireEntity, EntityLabels: []string{"Class", "Route"}, NamePattern: `^api\.`}
	if !reflect.DeepEqual(p.Predicate, want) || len(p.Traversal.EdgeKinds) != 0 {
		t.Errorf("plan = %+v", p)
	}
}

func TestCompilesTheEdgeFamilies(t *testing.T) {
	t.Parallel()

	for i, tc := range edgeFamilyCases() {
		res := compileBody(t, tc.body)
		wantCodes(t, &res)

		p := res.Plans[0]
		if p.Predicate.Family != tc.family {
			t.Fatalf("case %d: family = %s", i, p.Predicate.Family)
		}

		tc.check(t, &p)
	}
}

func TestCompilesBudgets(t *testing.T) {
	t.Parallel()

	res := compileBody(t, "    subject: {calmNodes: [billing]}\n    budget:\n      measure: forbidden-edges\n      max: 3\n      target: {calmNodes: [db]}\n      edges: [IMPORTS]\n")
	wantCodes(t, &res)

	want := policy.Predicate{Family: policy.FamilyBudget, Budget: &policy.Budget{Measure: "forbidden-edges", Max: 3}}
	if p := res.Plans[0]; !reflect.DeepEqual(p.Predicate, want) || !slices.Equal(p.Targets, []string{"db"}) {
		t.Errorf("plan = %+v", p)
	}

	res = compileBody(t, "    subject: {nodeTypes: [service]}\n    budget:\n      measure: cycles\n      max: 0\n      edges: [IMPORTS]\n")
	wantCodes(t, &res)

	want = policy.Predicate{Family: policy.FamilyBudget, Budget: &policy.Budget{Measure: "cycles"}, CollapseBy: "architectureBinding"}
	if p := res.Plans[0]; !reflect.DeepEqual(p.Predicate, want) || len(p.Targets) != 0 {
		t.Errorf("plan = %+v", p)
	}
}

func TestRejectsBudgetsWithTheWrongTarget(t *testing.T) {
	t.Parallel()

	res := compileBody(t, "    subject: {calmNodes: [billing]}\n    budget:\n      measure: forbidden-edges\n      max: 1\n      edges: [IMPORTS]\n")
	wantCodes(t, &res, diag.POL001)

	if !strings.Contains(res.Diagnostics[0].Message, "budget.target is required for measure forbidden-edges") {
		t.Errorf("message = %q", res.Diagnostics[0].Message)
	}

	res = compileBody(t, "    subject: {calmNodes: [billing]}\n    budget:\n      measure: cycles\n      max: 1\n      target: {calmNodes: [db]}\n      edges: [IMPORTS]\n")
	wantCodes(t, &res, diag.POL001)

	if !strings.Contains(res.Diagnostics[0].Message, "budget.target is not allowed for measure cycles") {
		t.Errorf("message = %q", res.Diagnostics[0].Message)
	}
}

func TestRejectsABadNamePatternAndAnEmptyAPIRootList(t *testing.T) {
	t.Parallel()

	res := compileBody(t, "    subject: {calmNodes: [billing]}\n    require:\n      entity:\n        labels: [Route]\n        namePattern: '('\n")
	wantCodes(t, &res, diag.POL001)

	if !strings.Contains(res.Diagnostics[0].Message, "require.entity.namePattern: ") {
		t.Errorf("message = %q", res.Diagnostics[0].Message)
	}

	res = compileBody(t, "    subject: {calmNodes: [billing]}\n    publicApiOnly:\n      api: {sourceRoots: []}\n      edges: [IMPORTS]\n")
	wantCodes(t, &res, diag.POL001)
}

func TestAcceptsTheAggregationModesOfEdgeRules(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"edge", "file", "symbol", "architecture-pair"} {
		res := compileBody(t, "    subject: {calmNodes: [billing]}\n    aggregation: "+mode+"\n    declaredOnly: {edges: [IMPORTS]}\n")
		wantCodes(t, &res)

		if res.Plans[0].Aggregation != mode {
			t.Errorf("aggregation = %q, want %q", res.Plans[0].Aggregation, mode)
		}
	}

	res := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n")))
	if res.Plans[0].Aggregation != "edge" {
		t.Errorf("default aggregation = %q", res.Plans[0].Aggregation)
	}
}
