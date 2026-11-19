package policy_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// deployedModel declares `billing` and `ledger` deployed in `prod-cluster`, and `fulfillment` in `batch-cluster`.
func deployedModel() *intent.ArchitectureModel {
	m := model()
	m.Nodes = append(m.Nodes, node("prod-cluster", "system"), node("batch-cluster", "system"))
	m.Relationships = []intent.RelationshipDeclaration{
		{ID: "r-prod", Kind: intent.RelDeployedIn, Container: "prod-cluster", Targets: []string{"billing", "ledger"}, Pointer: "/relationships/0"},
		{ID: "r-batch", Kind: intent.RelDeployedIn, Container: "batch-cluster", Targets: []string{"fulfillment"}, Pointer: "/relationships/1"},
		{ID: "r-calls", Kind: intent.RelInteracts, Source: "billing", Targets: []string{"db"}, Pointer: "/relationships/2"},
	}

	return m
}

var deploymentCaps = policy.ProviderCaps{SupportedCanonicalKinds: []observation.CanonicalEdgeKind{"placed-in", "built-from", "uses-image", "imports"}}

func compileDeployed(t *testing.T, p policy.PolicyFile) policy.CompileResult {
	t.Helper()

	res, err := policy.CompilePolicies([]policy.PolicyFile{p}, deployedModel(), deploymentCaps, nil)
	if err != nil {
		t.Fatalf("CompilePolicies: %v", err)
	}

	return res
}

const placementBlock = "    obligation:\n      type: %s\n      relation: {kind: placed-in, direction: from-workload, target: %s}\n"

func placementRule(typ, target string) string {
	return "    subject: {deployedIn: true}\n" + fmt.Sprintf(placementBlock, typ, target)
}

func TestDeployedInSubjectSelectsTheDeployedNodes(t *testing.T) {
	t.Parallel()

	res := compileDeployed(t, pack(rule(placementRule("required", "{deployedIn: true}"))))
	wantCodes(t, &res)

	p := res.Plans[0]
	// The nodes that a deployed-in relationship lists, and neither a container nor a node of another relationship kind.
	if want := []string{"billing", "fulfillment", "ledger"}; !slices.Equal(p.Subjects, want) {
		t.Fatalf("subjects = %v, want %v", p.Subjects, want)
	}

	if len(p.Targets) != 0 || len(p.Obligation.Relation.Target) != 0 {
		t.Errorf("a target from the model names no ID: plan targets %v, relation target %v", p.Targets, p.Obligation.Relation.Target)
	}

	if p.Obligation.Relation.TargetMode != policy.TargetDeclaredContainers {
		t.Errorf("target mode = %q", p.Obligation.Relation.TargetMode)
	}

	want := "required forAll from-workload placed-in deployedIn=true scope=any roles=parsed-source-fact,runtime-observation minimumResolution=explicitly-bound completeness=complete"
	if got := p.Obligation.Declaration(); got != want {
		t.Errorf("declaration = %q, want %q", got, want)
	}
}

func TestDeployedInFalseTargetIsTheOtherHostingTargets(t *testing.T) {
	t.Parallel()

	res := compileDeployed(t, pack(rule(placementRule("forbidden", "{deployedIn: false}"))))
	wantCodes(t, &res)

	spec := res.Plans[0].Obligation
	if !targetsOtherHostingTargets(spec) {
		t.Fatalf("relation = %+v, declaration %q", spec.Relation, spec.Declaration())
	}

	// The mode is part of the relation identity: the two targets are two relations.
	declared := compileDeployed(t, pack(rule(placementRule("forbidden", "{deployedIn: true}"))))
	if policy.RelationKey(&declared.Plans[0].Obligation.Relation) == policy.RelationKey(&spec.Relation) {
		t.Error("the relation key does not tell `deployedIn: true` from `deployedIn: false`")
	}

	named := compileDeployed(t, pack(rule("    subject: {tags: [core]}\n    obligation:\n      type: forbidden\n"+
		"      relation: {kind: placed-in, direction: from-workload, target: {calmNodes: [prod-cluster]}}\n")))
	if got := policy.RelationKey(&named.Plans[0].Obligation.Relation); got != "placed-in|from-workload|prod-cluster" {
		t.Errorf("a rule with an explicit target changed its relation key: %q", got)
	}
}

// targetsOtherHostingTargets says whether the obligation takes the other hosting targets from the model, and shows it.
func targetsOtherHostingTargets(spec *policy.ObligationSpec) bool {
	if spec.Relation.TargetMode != policy.TargetOtherHostingTargets || len(spec.Relation.Target) != 0 {
		return false
	}

	return strings.Contains(spec.Declaration(), " deployedIn=false ")
}

func TestDeployedInWithNoModelKeepsTheContract(t *testing.T) {
	t.Parallel()

	info, problems := policy.DescribePack(pack(rule(placementRule("required", "{deployedIn: true}"))))
	if len(problems) > 0 {
		t.Fatalf("problems: %+v", problems)
	}

	if got := info.Rules[0].Obligation.Relation.TargetMode; got != policy.TargetDeclaredContainers {
		t.Errorf("target mode = %q", got)
	}
}

func TestDeployedInIsRejectedOutsideTheWholeSubjectAndTarget(t *testing.T) {
	t.Parallel()

	typed := func(subject, relation, extra string) string {
		return "    subject: " + subject + "\n    obligation:\n      type: required\n      relation: " + relation + "\n" + extra
	}
	relation := "{kind: placed-in, direction: from-workload}"

	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"applies":                {typed("{deployedIn: true}", relation, "      applies: {deployedIn: true}\n"), "/rules/0/obligation/applies"},
		"operator in applies":    {typed("{deployedIn: true}", relation, "      applies: {not: {deployedIn: true}}\n"), "/rules/0/obligation/applies"},
		"operator in subject":    {typed("{allOf: [{deployedIn: true}]}", relation, ""), "/rules/0/subject"},
		"operator in target":     {typed("{deployedIn: true}", "{kind: placed-in, direction: from-workload, target: {anyOf: [{deployedIn: false}]}}", ""), "/rules/0/obligation/relation/target"},
		"next to calmNodes":      {typed("{deployedIn: true, calmNodes: [billing]}", relation, ""), "/rules/0/subject"},
		"target next to tags":    {typed("{deployedIn: true}", "{kind: placed-in, direction: from-workload, target: {deployedIn: true, tags: [core]}}", ""), "/rules/0/obligation/relation/target"},
		"subject false":          {typed("{deployedIn: false}", relation, ""), "/rules/0/subject"},
		"named selector":         {typed("{ref: topology}", relation, ""), "/selectors/topology"},
		"non-typed rule subject": {"    subject: {deployedIn: true}\n    declaredOnly: {edges: [IMPORTS]}\n", "/rules/0/subject"},
		"forbid target":          {"    subject: {tags: [core]}\n    forbid:\n      target: {deployedIn: false}\n      edges: [IMPORTS]\n", "/rules/0/forbid/target"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			res := compileDeployed(t, pack(rule(tc.body), "selectors:\n  topology: {deployedIn: true}\n"))
			if !rejectedAt(&res, diag.POL002, tc.want) {
				t.Fatalf("plans %d, diagnostics %+v, want %s at %s", len(res.Plans), res.Diagnostics, diag.POL002, tc.want)
			}
		})
	}
}

func TestDeployedInNeedsABoolean(t *testing.T) {
	t.Parallel()

	res := compileDeployed(t, pack(rule(placementRule("required", "{deployedIn: yes-please}"))))
	if !rejectedAt(&res, diag.POL001, "") {
		t.Fatalf("plans %d, diagnostics %+v", len(res.Plans), res.Diagnostics)
	}
}

// rejectedAt says whether the compile gave no plan and its first diagnostic has the code and a pointer that starts with pointer.
func rejectedAt(res *policy.CompileResult, code, pointer string) bool {
	if len(res.Plans) != 0 || len(res.Diagnostics) == 0 {
		return false
	}

	first := res.Diagnostics[0]

	return first.Code == code && strings.HasPrefix(first.Source.Pointer, pointer)
}

func TestFromImageIsAValidDirection(t *testing.T) {
	t.Parallel()

	res := compileDeployed(t, pack(rule("    subject: {tags: [core]}\n    obligation:\n      type: required\n      relation: {kind: built-from, direction: from-image}\n")))
	wantCodes(t, &res)

	spec := res.Plans[0].Obligation
	if spec.Relation.Direction != policy.DirectionFromImage || res.Plans[0].Traversal.Direction != "outbound" {
		t.Errorf("direction %q, traversal %q", spec.Relation.Direction, res.Plans[0].Traversal.Direction)
	}

	if !strings.HasPrefix(spec.Declaration(), "required forAll from-image built-from ") {
		t.Errorf("declaration = %q", spec.Declaration())
	}
}

func TestPassCompletenessDefaultsToCompleteness(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		evidence string
		pass     policy.Completeness
		shown    bool
	}{
		"unset":                {"      completeness: complete\n", policy.CompletenessComplete, false},
		"unset and partial":    {"      completeness: partial\n", policy.CompletenessPartial, false},
		"any for a pass":       {"      completeness: complete\n      passCompleteness: any\n", policy.CompletenessAny, true},
		"partial for a pass":   {"      passCompleteness: partial\n", policy.CompletenessPartial, true},
		"complete for a pass":  {"      completeness: any\n      passCompleteness: complete\n", policy.CompletenessComplete, true},
		"no evidence block":    {"", policy.CompletenessComplete, false},
		"same as completeness": {"      completeness: complete\n      passCompleteness: complete\n", policy.CompletenessComplete, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body := typedHead
			if tc.evidence != "" {
				body += "    evidence:\n" + tc.evidence
			}

			res := typedBody(t, body)
			wantCodes(t, &res)

			spec := res.Plans[0].Obligation
			if got := spec.PassNeed(); got != tc.pass {
				t.Errorf("PassNeed = %q, want %q", got, tc.pass)
			}

			if shown := strings.Contains(spec.Declaration(), "passCompleteness="); shown != tc.shown {
				t.Errorf("declaration %q shows passCompleteness: %v, want %v", spec.Declaration(), shown, tc.shown)
			}
		})
	}
}

func TestPassCompletenessIsATypedKey(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"    subject: {tags: [core]}\n    declaredOnly: {edges: [IMPORTS]}\n    evidence:\n      passCompleteness: any\n",
		typedHead + "    evidence:\n      passCompleteness: full\n",
	} {
		res := typedBody(t, body)
		if !rejectedAt(&res, diag.POL001, "/rules/0/evidence/passCompleteness") {
			t.Fatalf("plans %d, diagnostics %+v", len(res.Plans), res.Diagnostics)
		}
	}
}
