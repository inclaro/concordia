package policy_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

const decisionsRuleBody = "    family: decision-governance\n    decisions: {obligation: presence}\n"

func TestDecisionsRuleCompilesToTheGovernanceFamily(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule(decisionsRuleBody, "GOV-X-001")))
	wantCodes(t, &res)

	if len(res.Plans) != 1 {
		t.Fatalf("plans = %+v", res.Plans)
	}

	plan := res.Plans[0]
	if plan.Predicate.Family != policy.FamilyDecisionGovernance || plan.Predicate.Decisions != "presence" || plan.Family != "decision-governance" {
		t.Errorf("predicate = %+v, family = %q", plan.Predicate, plan.Family)
	}

	if plan.Predicate.Family.EdgeFindings() {
		t.Error("the decision-governance family has edge findings")
	}

	if len(plan.Subjects) != 0 || len(plan.Targets) != 0 || len(plan.Traversal.EdgeKinds) != 0 {
		t.Errorf("subjects = %v, targets = %v, traversal = %+v", plan.Subjects, plan.Targets, plan.Traversal)
	}
}

func TestDecisionsRuleAcceptsEveryGovernanceObligation(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"presence", "applicability", "justification", "enforcement", "history", "scope"} {
		res := compileOne(t, pack(rule("    decisions: {obligation: "+name+"}\n")))
		wantCodes(t, &res)

		if len(res.Plans) != 1 || res.Plans[0].Predicate.Decisions != name {
			t.Errorf("%s: plans = %+v", name, res.Plans)
		}
	}
}

func TestDecisionsRuleWithAnUnknownObligationGivesPOL002(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    decisions: {obligation: provenance}\n")))
	wantCodes(t, &res, diag.POL002)

	if len(res.Plans) != 0 {
		t.Errorf("plans = %+v", res.Plans)
	}

	if d := res.Diagnostics[0]; d.Source.Pointer != "/rules/0/decisions/obligation" {
		t.Errorf("pointer = %q", d.Source.Pointer)
	}
}

func TestDecisionsRuleRejectsASubjectAndAnotherPredicate(t *testing.T) {
	t.Parallel()

	withSubject := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    decisions: {obligation: presence}\n")))
	wantCodes(t, &withSubject, "CO-POL-001")

	other := compileOne(t, pack(rule("    decisions: {obligation: presence}\n    observedRequired: {}\n")))
	wantCodes(t, &other, "CO-POL-001")

	closed := compileOne(t, pack(rule("    decisions: {obligation: presence, extra: 1}\n")))
	wantCodes(t, &closed, "CO-POL-001")

	missing := compileOne(t, pack(rule("    decisions: {}\n")))
	wantCodes(t, &missing, "CO-POL-001")
}

func TestDecisionsRuleRejectsAnAggregation(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    aggregation: file\n    decisions: {obligation: presence}\n")))
	wantCodes(t, &res, diag.POL002)
}

func TestDeclaredRulesListTheRulesThatTheRuleFilterRemoved(t *testing.T) {
	t.Parallel()

	body := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"
	other := policy.PolicyFile{
		Path: "policies/b.policy.yaml",
		Text: "apiVersion: concordia.io/v1alpha1\nkind: PolicyPack\nmetadata:\n  id: second\n  version: 2.1.0\nrules:\n" +
			rule(decisionsRuleBody, "GOV-X-001"),
	}
	files := []policy.PolicyFile{other, pack(rule(body, "CO-B-001") + rule(body, "CO-A-001"))}

	res := compile(t, files, model(), policy.CompileOverrides{RuleIDs: []string{"CO-A-001"}})
	if len(res.Plans) != 1 || res.Plans[0].RuleID != "CO-A-001" {
		t.Fatalf("plans = %+v", res.Plans)
	}

	want := []policy.DeclaredRule{
		{RuleID: "CO-A-001", Pack: policy.PackRef{ID: "demo", Version: "1.0.0"}},
		{RuleID: "CO-B-001", Pack: policy.PackRef{ID: "demo", Version: "1.0.0"}},
		{RuleID: "GOV-X-001", Pack: policy.PackRef{ID: "second", Version: "2.1.0"}},
	}
	if !slices.Equal(res.DeclaredRules, want) {
		t.Errorf("declared = %+v, want %+v", res.DeclaredRules, want)
	}
}

func TestDeclaredRulesIncludeADisabledRule(t *testing.T) {
	t.Parallel()

	body := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"
	file := pack(rule(body, "CO-A-001") + rule(body, "CO-A-002"))
	off := false
	file.Overrides = map[string]workspace.RuleOverride{"CO-A-002": {Enabled: &off}}

	res := compileOne(t, file)

	got := make([]string, len(res.DeclaredRules))
	for i, r := range res.DeclaredRules {
		got[i] = r.RuleID
	}

	if len(res.Plans) != 1 || !slices.Equal(got, []string{"CO-A-001", "CO-A-002"}) {
		t.Errorf("plans = %d, declared = %v", len(res.Plans), got)
	}
}

func TestDeclaredRulesAreEmptyForNoPack(t *testing.T) {
	t.Parallel()

	res := compile(t, nil, model())
	if res.DeclaredRules == nil || len(res.DeclaredRules) != 0 {
		t.Errorf("declared = %#v", res.DeclaredRules)
	}
}

func TestResolveNodeSelector(t *testing.T) {
	t.Parallel()

	m := model()

	cases := map[string]struct {
		calm, types, tags []string
		want              []string
	}{
		"tags":                {nil, nil, []string{"core"}, []string{"billing", "ledger"}},
		"nodeTypes":           {nil, []string{"database", "actor"}, nil, []string{"db", "user"}},
		"calmNodes":           {[]string{"user", "billing"}, nil, nil, []string{"billing", "user"}},
		"union":               {[]string{"user"}, []string{"database"}, []string{"core"}, []string{"billing", "db", "ledger", "user"}},
		"duplicates":          {[]string{"billing"}, []string{"service"}, []string{"core"}, []string{"billing", "fulfillment", "ledger"}},
		"unknown node ID":     {[]string{"ghost"}, nil, nil, []string{}},
		"unknown tag":         {nil, nil, []string{"nope"}, []string{}},
		"nothing given":       {nil, nil, nil, []string{}},
		"several tags a type": {nil, []string{"actor"}, []string{"core", "other"}, []string{"billing", "ledger", "user"}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := policy.ResolveNodeSelector(m, tc.calm, tc.types, tc.tags)
			if got == nil || !slices.Equal(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}

	if got := policy.ResolveNodeSelector(nil, []string{"billing"}, nil, nil); got == nil || len(got) != 0 {
		t.Errorf("nil model: %#v", got)
	}
}

func TestResolveNodeSelectorAgreesWithThePolicySelector(t *testing.T) {
	t.Parallel()

	body := "    subject: {calmNodes: [user], nodeTypes: [database], tags: [core]}\n    declaredOnly: {edges: [IMPORTS]}\n"
	res := compileOne(t, pack(rule(body)))
	wantCodes(t, &res)

	got := policy.ResolveNodeSelector(model(), []string{"user"}, []string{"database"}, []string{"core"})
	if !slices.Equal(res.Plans[0].Subjects, got) {
		t.Errorf("rule subjects = %v, ResolveNodeSelector = %v", res.Plans[0].Subjects, got)
	}
}
