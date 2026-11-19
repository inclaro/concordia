package policy_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

func TestCompilesThePOL02FullDeclaration(t *testing.T) {
	t.Parallel()

	res := typedBody(t, "    family: deployment\n    enforcement: advisory\n    subject: {tags: [core]}\n"+
		"    obligation:\n      type: required\n      relation: {kind: uses-image, direction: from-workload, target: {calmNodes: [db]}}\n"+
		"      scope: {environments: [production, production], repositories: [payments]}\n      multiplicity: forAll\n"+
		"    evidence:\n      minimumConfidence: syntactic\n      roles: [runtime-observation, parsed-source-fact]\n      minimumResolution: exact\n      completeness: complete\n")
	wantCodes(t, &res)

	p := res.Plans[0]
	requireTypedPlanHeader(t, &p)

	want := &policy.ObligationSpec{
		Type: policy.ObligationRequired,
		Relation: policy.RelationSpec{
			Kind: observation.KindUsesImage, Direction: policy.DirectionFromWorkload, Target: []string{"db"},
		},
		Scope:             policy.ObligationScope{Environments: []string{"production"}, Repositories: []string{"payments"}},
		Multiplicity:      policy.MultiplicityForAll,
		Roles:             []observation.EvidenceRole{observation.RoleParsedSourceFact, observation.RoleRuntimeObservation},
		MinimumResolution: observation.ResolutionExact,
		Completeness:      policy.CompletenessComplete,
	}
	if !reflect.DeepEqual(p.Obligation, want) {
		t.Errorf("obligation = %+v, want %+v", p.Obligation, want)
	}

	requireTypedTraversal(t, &p)

	if p.EvidenceRequirements.MinimumConfidence != observation.ConfidenceSyntactic {
		t.Errorf("minimumConfidence = %s", p.EvidenceRequirements.MinimumConfidence)
	}

	wantLine := "required forAll from-workload uses-image scope=production;repositories=payments roles=parsed-source-fact,runtime-observation " +
		"minimumResolution=exact completeness=complete"
	if got := p.Obligation.Declaration(); got != wantLine {
		t.Errorf("declaration = %q, want %q", got, wantLine)
	}
}

// requireTypedPlanHeader checks the predicate family and the declared family and enforcement of the plan.
func requireTypedPlanHeader(t *testing.T, p *policy.RulePlan) {
	t.Helper()

	if p.Predicate.Family != policy.FamilyTyped {
		t.Fatalf("plan = %+v", *p)
	}

	if p.Family != "deployment" {
		t.Fatalf("plan = %+v", *p)
	}

	if p.Enforcement != policy.EnforcementAdvisory {
		t.Fatalf("plan = %+v", *p)
	}
}

// requireTypedTraversal checks the subjects, the targets and the traversal of the plan of the full declaration.
func requireTypedTraversal(t *testing.T, p *policy.RulePlan) {
	t.Helper()

	if !slices.Equal(p.Subjects, []string{"billing", "ledger"}) {
		t.Errorf("subjects %v targets %v traversal %+v", p.Subjects, p.Targets, p.Traversal)
	}

	if !slices.Equal(p.Targets, []string{"db"}) {
		t.Errorf("subjects %v targets %v traversal %+v", p.Subjects, p.Targets, p.Traversal)
	}

	if !slices.Equal(p.Traversal.EdgeKinds, kinds("uses-image")) {
		t.Errorf("subjects %v targets %v traversal %+v", p.Subjects, p.Targets, p.Traversal)
	}

	if p.Traversal.Direction != "outbound" {
		t.Errorf("subjects %v targets %v traversal %+v", p.Subjects, p.Targets, p.Traversal)
	}

	if len(p.UnsupportedEdgeKinds) != 0 {
		t.Errorf("subjects %v targets %v traversal %+v", p.Subjects, p.Targets, p.Traversal)
	}
}

func TestATypedRuleWithOnlyMinimumConfidenceTakesTheDocumentedDefaults(t *testing.T) {
	t.Parallel()

	res := typedBody(t, typedHead+"    evidence:\n      minimumConfidence: heuristic\n")
	wantCodes(t, &res)

	p := res.Plans[0]
	if p.Family != "" || p.Enforcement != "" {
		t.Errorf("family %q enforcement %q, want neither declared", p.Family, p.Enforcement)
	}

	want := policy.ObligationSpec{
		Type:     policy.ObligationRequired,
		Relation: policy.RelationSpec{Kind: observation.KindUsesImage, Direction: policy.DirectionFromWorkload, Target: []string{}},
		Scope:    policy.ObligationScope{Environments: []string{}, Repositories: []string{}},
		// The defaults.
		Multiplicity:      policy.MultiplicityForAll,
		Roles:             []observation.EvidenceRole{observation.RoleParsedSourceFact, observation.RoleRuntimeObservation},
		MinimumResolution: observation.ResolutionExplicitlyBound,
		Completeness:      policy.CompletenessComplete,
	}
	if !reflect.DeepEqual(*p.Obligation, want) {
		t.Errorf("obligation = %+v, want %+v", *p.Obligation, want)
	}

	if p.EvidenceRequirements.MinimumConfidence != observation.ConfidenceHeuristic {
		t.Errorf("minimumConfidence = %s", p.EvidenceRequirements.MinimumConfidence)
	}
}

func TestATypedRuleDirectionToTheEntityTraversesInbound(t *testing.T) {
	t.Parallel()

	res := typedBody(t, "    subject: {tags: [core]}\n    obligation:\n      type: forbidden\n      multiplicity: exists\n"+
		"      relation: {kind: routes-to, direction: to-subject}\n")
	wantCodes(t, &res)

	p := res.Plans[0]
	if p.Traversal.Direction != "inbound" {
		t.Errorf("plan = %+v", p)
	}

	if p.Obligation.Multiplicity != policy.MultiplicityExists {
		t.Errorf("plan = %+v", p)
	}

	if p.Obligation.Type != policy.ObligationForbidden {
		t.Errorf("plan = %+v", p)
	}
}

func TestRejectsInvalidTypedValues(t *testing.T) {
	t.Parallel()

	obligation := func(fields string) string {
		return "    subject: {tags: [core]}\n    obligation:\n      type: required\n      relation: {kind: uses-image, direction: from-workload}\n" + fields
	}
	evidence := func(fields string) string { return typedHead + "    evidence:\n" + fields }

	for name, tc := range map[string]struct {
		body string
		code string
		want string
	}{
		"type":                           {strings.Replace(typedHead, "required", "mandatory", 1), diag.POL001, "/rules/0/obligation/type"},
		"direction":                      {strings.Replace(typedHead, "from-workload", "sideways", 1), diag.POL001, "/rules/0/obligation/relation/direction"},
		"multiplicity":                   {obligation("      multiplicity: some\n"), diag.POL001, "/rules/0/obligation/multiplicity"},
		"missing relation":               {"    subject: {tags: [core]}\n    obligation:\n      type: required\n", diag.POL001, "/rules/0/obligation"},
		"unknown field":                  {obligation("      owner: x\n"), diag.POL001, "/rules/0/obligation"},
		"environment id":                 {obligation("      scope: {environments: [Production]}\n"), diag.POL001, "/rules/0/obligation/scope/environments/0"},
		"enforcement":                    {"    enforcement: strict\n" + typedHead, diag.POL001, "/rules/0/enforcement"},
		"family":                         {"    family: Deployment\n" + typedHead, diag.POL001, "/rules/0/family"},
		"role":                           {evidence("      roles: [guess]\n"), diag.POL001, "/rules/0/evidence/roles/0"},
		"empty roles":                    {evidence("      roles: []\n"), diag.POL001, "/rules/0/evidence/roles"},
		"resolution":                     {evidence("      minimumResolution: certain\n"), diag.POL001, "/rules/0/evidence/minimumResolution"},
		"unbound resolution":             {evidence("      minimumResolution: unbound\n"), diag.POL001, "/rules/0/evidence/minimumResolution"},
		"completeness":                   {evidence("      completeness: full\n"), diag.POL001, "/rules/0/evidence/completeness"},
		"relation kind":                  {strings.Replace(typedHead, "uses-image", "NOPE", 1), diag.POL004, "/rules/0/obligation/relation/kind"},
		"relation kind of several kinds": {strings.Replace(typedHead, "uses-image", "configures", 1), diag.POL004, "/rules/0/obligation/relation/kind"},
		"unknown CALM target":            {strings.Replace(typedHead, "from-workload}", "from-workload, target: {calmNodes: [nope]}}", 1), diag.POL005, "/rules/0/obligation/relation/target/calmNodes"},
		"no subject":                     {"    obligation:\n      type: required\n      relation: {kind: uses-image, direction: from-workload}\n", diag.POL001, "/rules/0"},
		"second predicate":               {typedHead + "    declaredOnly: {edges: [IMPORTS]}\n", diag.POL001, "/rules/0"},
		"typed key without an obligation": {
			"    subject: {tags: [core]}\n    declaredOnly: {edges: [IMPORTS]}\n    evidence:\n      roles: [parsed-source-fact]\n",
			diag.POL001, "/rules/0/evidence/roles",
		},
		"aggregation": {typedHead + "    aggregation: file\n", diag.POL002, "/rules/0/aggregation"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := typedBody(t, tc.body)
			if !rejectedAt(&res, tc.code, tc.want) {
				t.Fatalf("plans %d, diagnostics %+v, want %s at %s", len(res.Plans), res.Diagnostics, tc.code, tc.want)
			}
		})
	}
}

func TestATypedRuleOnAnUnsupportedKindGivesPOL009(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule(typedHead)))
	wantCodes(t, &res, diag.POL009)

	if len(res.Plans) != 1 || !slices.Equal(res.Plans[0].UnsupportedEdgeKinds, kinds("uses-image")) {
		t.Errorf("plans = %+v", res.Plans)
	}
}
