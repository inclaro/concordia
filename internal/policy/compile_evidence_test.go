package policy_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
)

func ctl(cfg *intent.ControlConfig, id string) intent.ControlAttachment {
	cfg.Engine = "concordia"
	cfg.RuleIDs = []string{"CO-X-001"}

	return intent.ControlAttachment{
		ControlID: id, Scope: intent.ControlScope{Level: intent.ScopeNode, ID: "billing"},
		Config: cfg, Pointer: "/nodes/0/controls/" + id,
	}
}

func ptr(v float64) *float64 { return &v }

func requirements(t *testing.T, ev string, m *intent.ArchitectureModel, overrides *policy.CompileOverrides) policy.EvidenceRequirements {
	t.Helper()

	body := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n" + ev

	return compile(t, []policy.PolicyFile{pack(rule(body))}, m, *overrides).Plans[0].EvidenceRequirements
}

func TestEvidenceRequirementDefaults(t *testing.T) {
	t.Parallel()

	got := requirements(t, "", model(), &policy.CompileOverrides{})
	if want := (policy.EvidenceRequirements{MinimumConfidence: "syntactic", MinimumCoverage: 0.95, OnUnknown: "fail"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("requirements = %+v", got)
	}
}

func TestEvidenceRequirementPrecedence(t *testing.T) {
	t.Parallel()

	ws := &policy.CompileOverrides{Workspace: policy.PartialEvidence{MinimumCoverage: ptr(0.5), OnUnknown: "ignore", MinimumConfidence: "heuristic"}}
	if got, want := requirements(t, "", model(), ws), (policy.EvidenceRequirements{MinimumConfidence: "heuristic", MinimumCoverage: 0.5, OnUnknown: "ignore"}); !reflect.DeepEqual(got, want) {
		t.Errorf("workspace: %+v", got)
	}

	ev := "    evidence: {minimumConfidence: resolved, minimumCoverage: 0.6, onUnknown: warn}\n"
	if got, want := requirements(t, ev, model(), ws), (policy.EvidenceRequirements{MinimumConfidence: "resolved", MinimumCoverage: 0.6, OnUnknown: "warn"}); !reflect.DeepEqual(got, want) {
		t.Errorf("rule: %+v", got)
	}

	if got := requirements(t, "    evidence: {coverage: {files: 0.7}}\n", model(), ws).MinimumCoverage; got != 0.7 {
		t.Errorf("coverage.files: %v", got)
	}

	m := model(ctl(&intent.ControlConfig{MinimumPolicyCoverage: ptr(0.8), UnknownDisposition: "fail"}, "c1"))
	if got, want := requirements(t, ev, m, ws), (policy.EvidenceRequirements{MinimumConfidence: "resolved", MinimumCoverage: 0.8, OnUnknown: "fail"}); !reflect.DeepEqual(got, want) {
		t.Errorf("control: %+v", got)
	}

	cliOverrides := *ws
	cli := &cliOverrides
	cliOverrides.CLI = policy.PartialEvidence{MinimumCoverage: ptr(0.99), OnUnknown: "ignore", MinimumConfidence: "runtime"}

	if got, want := requirements(t, ev, m, cli), (policy.EvidenceRequirements{MinimumConfidence: "runtime", MinimumCoverage: 0.99, OnUnknown: "ignore"}); !reflect.DeepEqual(got, want) {
		t.Errorf("cli: %+v", got)
	}
}

func TestSeveralControlsCombineHighestCoverageStrictestDisposition(t *testing.T) {
	t.Parallel()

	m := model(
		ctl(&intent.ControlConfig{MinimumPolicyCoverage: ptr(0.8), UnknownDisposition: "warn"}, "c1"),
		ctl(&intent.ControlConfig{MinimumPolicyCoverage: ptr(0.9), UnknownDisposition: "ignore"}, "c2"),
	)

	got := requirements(t, "", m, &policy.CompileOverrides{})
	if got.MinimumCoverage != 0.9 || got.OnUnknown != "warn" {
		t.Fatalf("requirements = %+v", got)
	}
}

func TestAControlOfAnotherEngineDoesNotOverride(t *testing.T) {
	t.Parallel()

	m := model(intent.ControlAttachment{ControlID: "x", Scope: intent.ControlScope{Level: intent.ScopeArchitecture}, Engine: "other", Pointer: "/controls/x"})

	got := requirements(t, "", m, &policy.CompileOverrides{})
	if got.MinimumCoverage != 0.95 || got.OnUnknown != "fail" {
		t.Fatalf("requirements = %+v", got)
	}
}

func TestEvidenceEdgeKindsBytesAndLanguages(t *testing.T) {
	t.Parallel()

	ev := "    evidence:\n      edgeKinds: {calls: heuristic}\n      coverage: {bytes: 0.9, supportedLanguages: 0.8}\n"
	body := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS, CALLS]}\n" + ev

	res := compileBody(t, body)
	wantCodes(t, &res)

	req := res.Plans[0].EvidenceRequirements
	if req.MinimumConfidenceFor("calls") != "heuristic" || req.MinimumConfidenceFor("imports") != "syntactic" {
		t.Errorf("confidence = %+v", req.EdgeKindConfidence)
	}

	if req.MinimumByteCoverage == nil || *req.MinimumByteCoverage != 0.9 || req.LanguageThreshold() != 0.8 {
		t.Errorf("bytes/languages = %v / %v", req.MinimumByteCoverage, req.LanguageThreshold())
	}

	if plain := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"))); plain.Plans[0].EvidenceRequirements.LanguageThreshold() != 0.95 {
		t.Errorf("language threshold = %v", plain.Plans[0].EvidenceRequirements.LanguageThreshold())
	}
}

func TestRejectsAnEdgeKindConfidenceForAKindThatTheRuleDoesNotTraverse(t *testing.T) {
	t.Parallel()

	res := compileBody(t, "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n    evidence:\n      edgeKinds: {calls: heuristic}\n")
	wantCodes(t, &res, diag.POL001)

	if !strings.Contains(res.Diagnostics[0].Message, "evidence.edgeKinds names calls, which the rule does not traverse") {
		t.Errorf("message = %q", res.Diagnostics[0].Message)
	}

	res = compileBody(t, "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n    evidence:\n      edgeKinds: {NOPE: heuristic}\n")
	wantCodes(t, &res, diag.POL004)
}
