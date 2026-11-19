package packs_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
)

// packVersion is the version that every built-in pack has at this release.
const packVersion = "1.0.0"

// packText gives the YAML text of a built-in pack and fails the test when the pack is missing.
func packText(t *testing.T, name string) []byte {
	t.Helper()

	text, ok := packs.Source(name)
	if !ok {
		t.Fatalf("builtin pack %s is missing", name)
	}

	return text
}

// compilePack compiles a built-in pack against an empty architecture model.
func compilePack(t *testing.T, name string) policy.CompileResult {
	t.Helper()

	model := &intent.ArchitectureModel{
		SourceURI: "architecture/a.calm.json", Digest: "sha256:0", SchemaVersion: "1.2",
		Nodes: []intent.NodeDeclaration{}, Relationships: []intent.RelationshipDeclaration{},
		Controls: []intent.ControlAttachment{}, Metadata: map[string]any{},
	}

	result, err := policy.CompilePolicies(
		[]policy.PolicyFile{{Path: packs.Prefix + name, Text: string(packText(t, name))}}, model,
		policy.ProviderCaps{SupportedCanonicalKinds: observation.SupportedCanonicalKinds()}, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	return result
}

// compileCleanPack compiles a built-in pack and requires no diagnostics and one pack with the given name.
func compileCleanPack(t *testing.T, name string) policy.CompileResult {
	t.Helper()

	result := compilePack(t, name)

	if len(result.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %+v", result.Diagnostics)
	}

	if !isSinglePack(result.Packs, name) {
		t.Fatalf("packs: %+v", result.Packs)
	}

	return result
}

// isSinglePack reports whether refs holds exactly one pack, with this name and the release version.
func isSinglePack(refs []policy.PolicyPackRef, name string) bool {
	if len(refs) != 1 {
		return false
	}

	return refs[0].ID == name && refs[0].Version == packVersion
}

// requireRuleCounts requires that the pack compiled to want plans and declared rules.
func requireRuleCounts(t *testing.T, plans, declared, want int) {
	t.Helper()

	if !allTrue(plans == want, declared == want) {
		t.Fatalf("plans = %d, declared = %d, want %d", plans, declared, want)
	}
}

// requirePlanPack reports an error when the plan does not come from the named pack at the release version.
func requirePlanPack(t *testing.T, plan *policy.RulePlan, name string) {
	t.Helper()

	if plan.Pack.ID != name || plan.Pack.Version != packVersion {
		t.Errorf("%s pack %+v", plan.RuleID, plan.Pack)
	}
}

// requireNoEnforcement reports an error when the plan sets an enforcement.
func requireNoEnforcement(t *testing.T, plan *policy.RulePlan) {
	t.Helper()

	if plan.Enforcement != "" {
		t.Errorf("%s enforcement %q", plan.RuleID, plan.Enforcement)
	}
}

// requireTrimmedDocs reports an error when the plan lacks a name, a rationale or a remediation.
func requireTrimmedDocs(t *testing.T, plan *policy.RulePlan) {
	t.Helper()

	blank := []string{plan.Name, plan.Rationale, plan.Remediation}
	if slices.ContainsFunc(blank, func(s string) bool { return strings.TrimSpace(s) == "" }) {
		t.Errorf("%s needs name, rationale, and remediation", plan.RuleID)
	}
}

// hasDocs reports whether the plan has a name, a rationale and a remediation.
func hasDocs(plan *policy.RulePlan) bool {
	return !slices.Contains([]string{plan.Name, plan.Rationale, plan.Remediation}, "")
}

// allTrue reports whether every condition holds.
func allTrue(conds ...bool) bool {
	return !slices.Contains(conds, false)
}
