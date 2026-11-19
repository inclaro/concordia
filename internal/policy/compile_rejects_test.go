package policy_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

func TestRejectsARuleWithTwoPredicates(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n    observedRequired: {}\n")))
	wantCodes(t, &res, diag.POL001)

	if !strings.Contains(res.Diagnostics[0].Message, "exactly one predicate") || len(res.Plans) != 0 {
		t.Errorf("result = %+v", res)
	}

	err := policy.ThrowOnErrors(res.Diagnostics)

	var failure *diag.Error
	if !errors.As(err, &failure) || failure.ExitCode != diag.InvalidInput {
		t.Errorf("ThrowOnErrors = %v", err)
	}
}

func TestRejectsARuleWithNoPredicateLayerOrderWithSubjectAndAllowWithoutForbid(t *testing.T) {
	t.Parallel()

	none := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n")))
	wantCodes(t, &none, diag.POL001)

	lo := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    layerOrder: {layers: [{calmNodes: [billing]}, {calmNodes: [db]}], edges: [IMPORTS]}\n")))
	wantCodes(t, &lo, diag.POL001)

	if !strings.Contains(lo.Diagnostics[0].Message, "not permitted") {
		t.Errorf("message = %q", lo.Diagnostics[0].Message)
	}

	al := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n    allow: {target: {calmNodes: [db]}}\n")))
	wantCodes(t, &al, diag.POL001)
}

func TestRejectsBothMinimumCoverageAndCoverageFiles(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n    evidence: {minimumCoverage: 0.9, coverage: {files: 0.8}}\n")))
	wantCodes(t, &res, diag.POL001)
}

func TestRejectsABadRuleIDAndAMissingSubjectWithThePointer(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    declaredOnly: {edges: [IMPORTS]}\n", "bad-id")))
	wantCodes(t, &res, diag.POL001, diag.POL001)

	pointers := []string{res.Diagnostics[0].Source.Pointer, res.Diagnostics[1].Source.Pointer}
	slices.Sort(pointers)

	if !slices.Equal(pointers, []string{"/rules/0", "/rules/0/id"}) {
		t.Errorf("pointers = %v", pointers)
	}
}

func TestRejectsTraversalTransitive(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    forbid:\n      target: {calmNodes: [db]}\n      edges: [IMPORTS]\n      traversal: transitive\n")))
	wantCodes(t, &res, diag.POL002)

	if res.Diagnostics[0].Source.Pointer != "/rules/0/forbid/traversal" ||
		!strings.Contains(res.Diagnostics[0].Message, "traversal 'transitive' is not supported; use denyPath for transitive paths") {
		t.Errorf("pointer = %q, message = %q", res.Diagnostics[0].Source.Pointer, res.Diagnostics[0].Message)
	}
}

func TestRejectsUnknownSelectorKeysRefsCyclesAndNonEdgeAggregation(t *testing.T) {
	t.Parallel()

	sel := func(s string) policy.CompileResult {
		return compileOne(t, pack(rule("    subject: "+s+"\n    declaredOnly: {edges: [IMPORTS]}\n")))
	}

	for _, s := range []string{"{owners: [a]}", "{ref: missing}"} {
		res := sel(s)
		wantCodes(t, &res, diag.POL002)
	}

	cyc := compileOne(t, pack(rule("    subject: {ref: a}\n    declaredOnly: {edges: [IMPORTS]}\n"), "selectors:\n  a: {ref: b}\n  b: {ref: a}\n"))
	wantCodes(t, &cyc, diag.POL002)

	agg := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    aggregation: path\n    declaredOnly: {edges: [IMPORTS]}\n")))
	wantCodes(t, &agg, diag.POL002)
}

func TestRejectsDuplicateRuleIDs(t *testing.T) {
	t.Parallel()

	body := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"
	a := pack(rule(body))
	b := pack(rule(body))
	b.Path = "policies/b.policy.yaml"

	res := compile(t, []policy.PolicyFile{a, b}, model())
	wantCodes(t, &res, diag.POL003)

	if len(res.Plans) != 1 {
		t.Errorf("plans = %d", len(res.Plans))
	}

	same := compileOne(t, pack(rule(body)+rule(body)))
	wantCodes(t, &same, diag.POL003)
}

func TestRejectsUnknownEdgeKindsAndUnknownCalmIDs(t *testing.T) {
	t.Parallel()

	edge := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [NOPE]}\n")))
	wantCodes(t, &edge, diag.POL004)

	id := compileOne(t, pack(rule("    subject: {calmNodes: [ghost]}\n    declaredOnly: {edges: [IMPORTS]}\n")))
	wantCodes(t, &id, diag.POL005)

	allow := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    forbid: {target: {calmNodes: [db]}, edges: [IMPORTS]}\n    allow: {target: {calmNodes: [ghost]}}\n")))
	wantCodes(t, &allow, diag.POL005)
}

func TestReportsYAMLSyntaxErrorsAsPOL001(t *testing.T) {
	t.Parallel()

	res := compileOne(t, policy.PolicyFile{Path: "p.yaml", Text: "a: [1,\n"})
	wantCodes(t, &res, diag.POL001)

	if res.Diagnostics[0].Source.Line == 0 {
		t.Errorf("no line: %+v", res.Diagnostics[0])
	}
}

func TestReportsDuplicateYAMLKeysAsPOL001(t *testing.T) {
	t.Parallel()

	res := compileOne(t, policy.PolicyFile{Path: "p.yaml", Text: "a: 1\na: 2\n"})
	wantCodes(t, &res, diag.POL001)

	if res.Diagnostics[0].Source.Line != 2 {
		t.Errorf("line = %d", res.Diagnostics[0].Source.Line)
	}
}

func TestKeepsTheOtherRulesWhenOneRuleFails(t *testing.T) {
	t.Parallel()

	ok := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"
	res := compileOne(t, pack(rule(ok, "CO-A-002")+rule("    subject: {calmNodes: [ghost]}\n    declaredOnly: {edges: [IMPORTS]}\n", "CO-A-001")))

	ids := make([]string, 0, 8)
	for i := range res.Plans {
		ids = append(ids, res.Plans[i].RuleID)
	}

	if !slices.Equal(ids, []string{"CO-A-002"}) || !slices.Equal(res.RuleIDs, []string{"CO-A-001", "CO-A-002"}) {
		t.Errorf("plans = %v, rule IDs = %v", ids, res.RuleIDs)
	}
}

func TestUnsupportedKindsGivePOL009(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    forbid: {target: {calmNodes: [fulfillment]}, edges: [IMPORTS, USES_TYPE]}\n")))

	p := res.Plans[0]
	if !slices.Equal(p.UnsupportedEdgeKinds, kinds("uses-type")) || !slices.Equal(p.Traversal.EdgeKinds, kinds("imports", "uses-type")) {
		t.Errorf("plan = %+v", p)
	}

	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != diag.POL009 || res.Diagnostics[0].Severity != diag.SeverityWarning {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}

	if err := policy.ThrowOnErrors(res.Diagnostics); err != nil {
		t.Errorf("ThrowOnErrors = %v", err)
	}
}

func TestRejectsDenyPathDepthAboveTen(t *testing.T) {
	t.Parallel()

	res := compileBody(t, "    subject: {calmNodes: [billing]}\n    denyPath:\n      target: {calmNodes: [db]}\n      edges: [IMPORTS]\n      maxDepth: 11\n")
	wantCodes(t, &res, diag.POL001)

	if len(res.Plans) != 0 {
		t.Errorf("plans = %d", len(res.Plans))
	}
}

func TestRejectsAggregationOnRulesWithoutEdgeFindings(t *testing.T) {
	t.Parallel()

	res := compileBody(t, "    subject: {calmNodes: [billing]}\n    aggregation: file\n    observedRequired: {}\n")
	wantCodes(t, &res, diag.POL002)

	if !strings.Contains(res.Diagnostics[0].Message, "aggregation 'file' applies only to rules with edge findings") {
		t.Errorf("message = %q", res.Diagnostics[0].Message)
	}

	res = compileBody(t, "    subject: {calmNodes: [billing]}\n    aggregation: path\n    declaredOnly: {edges: [IMPORTS]}\n")
	wantCodes(t, &res, diag.POL002)

	if !strings.Contains(res.Diagnostics[0].Message, "aggregation 'path' is not supported; use edge, file, symbol or architecture-pair") {
		t.Errorf("message = %q", res.Diagnostics[0].Message)
	}
}
