package policy_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

func node(id, nodeType string, tags ...string) intent.NodeDeclaration {
	if tags == nil {
		tags = []string{}
	}

	return intent.NodeDeclaration{
		ID: id, NodeType: nodeType, Name: id, Description: id, Tags: tags,
		Bindings: []intent.ExtBinding{}, Pointer: "/nodes/" + id,
	}
}

func model(controls ...intent.ControlAttachment) *intent.ArchitectureModel {
	return &intent.ArchitectureModel{
		SourceURI:     "architecture/a.calm.json",
		Digest:        "sha256:00",
		SchemaVersion: "1.2",
		Nodes: []intent.NodeDeclaration{
			node("billing", "service", "core"),
			node("fulfillment", "service"),
			node("ledger", "service", "core"),
			node("db", "database"),
			node("user", "actor"),
		},
		Relationships: []intent.RelationshipDeclaration{},
		Controls:      controls,
		Metadata:      map[string]any{},
	}
}

var caps = policy.ProviderCaps{SupportedCanonicalKinds: []observation.CanonicalEdgeKind{
	"imports", "calls", "references-callable", "ambiguous-usage", "inherits", "contains", "defines", "reads", "writes",
}}

func pack(rules string, extra ...string) policy.PolicyFile {
	return policy.PolicyFile{
		Path: "policies/a.policy.yaml",
		Text: "apiVersion: concordia.io/v1alpha1\nkind: PolicyPack\nmetadata:\n  id: demo\n  version: 1.0.0\n" +
			strings.Join(extra, "") + "rules:\n" + rules,
	}
}

func rule(body string, id ...string) string {
	rid := "CO-X-001"
	if len(id) > 0 {
		rid = id[0]
	}

	return "  - id: " + rid + "\n    name: n " + rid + "\n    severity: error\n" + body
}

func compile(t *testing.T, files []policy.PolicyFile, m *intent.ArchitectureModel, overrides ...policy.CompileOverrides) policy.CompileResult {
	t.Helper()

	var o policy.CompileOverrides
	if len(overrides) > 0 {
		o = overrides[0]
	}

	res, err := policy.CompilePolicies(files, m, caps, &o)
	if err != nil {
		t.Fatalf("CompilePolicies: %v", err)
	}

	return res
}

func compileOne(t *testing.T, p policy.PolicyFile) policy.CompileResult {
	t.Helper()

	return compile(t, []policy.PolicyFile{p}, model())
}

func codes(res *policy.CompileResult) []string {
	out := make([]string, len(res.Diagnostics))
	for i := range res.Diagnostics {
		out[i] = res.Diagnostics[i].Code
	}

	return out
}

func wantCodes(t *testing.T, res *policy.CompileResult, want ...string) {
	t.Helper()

	if got := codes(res); !slices.Equal(got, want) {
		t.Fatalf("codes = %v, want %v (%+v)", got, want, res.Diagnostics)
	}
}

func kinds(list ...string) []observation.CanonicalEdgeKind {
	out := make([]observation.CanonicalEdgeKind, len(list))
	for i, s := range list {
		out[i] = observation.CanonicalEdgeKind(s)
	}

	return out
}

func compileBody(t *testing.T, body string) policy.CompileResult {
	t.Helper()

	return compileOne(t, pack(rule(body)))
}

var typedCaps = policy.ProviderCaps{SupportedCanonicalKinds: []observation.CanonicalEdgeKind{"routes-to", "uses-image", "imports"}}

func compileTyped(t *testing.T, p policy.PolicyFile) policy.CompileResult {
	t.Helper()

	res, err := policy.CompilePolicies([]policy.PolicyFile{p}, model(), typedCaps, nil)
	if err != nil {
		t.Fatalf("CompilePolicies: %v", err)
	}

	return res
}

const typedHead = "    subject: {tags: [core]}\n    obligation:\n      type: required\n      relation: {kind: uses-image, direction: from-workload}\n"

func typedBody(t *testing.T, body string, extra ...string) policy.CompileResult {
	t.Helper()

	return compileTyped(t, pack(rule(body), extra...))
}

// requireFailure checks that err is a *diag.Error and gives it.
func requireFailure(t *testing.T, err error) *diag.Error {
	t.Helper()

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v", err)
	}

	return failure
}
