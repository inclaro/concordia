package cli_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	pkWorkspaceBase = `# The workspace of the demo.
apiVersion: concordia.io/v1alpha1
kind: Workspace
observation:
  projects:
    - name: demo # the CBM project
check: {minimumCoverage: 0.5, unknown: fail, failOn: error}
`
	pkVendorRel = "policies/vendor/acme@1.2.0.policy.yaml"
	pkPackText  = `apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata: {id: acme, version: 1.2.0}
rules:
  - id: ACME-BND-001
    name: Billing does not import fulfillment
    severity: error
    subject: {calmNodes: [billing-service]}
    forbid:
      target: {calmNodes: [fulfillment-service]}
      edges: [IMPORTS]
      traversal: direct
  - id: ACME-DECL-001
    name: Dependencies are declared
    severity: warning
    subject: {nodeTypes: [service]}
    declaredOnly: {edges: [IMPORTS]}
`
)

func pkSum(text string) string { return digest.OfBytes([]byte(text)) }

// pkDir makes a directory with a workspace file and the extra files.
func pkDir(t *testing.T, workspaceText string, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	tree := map[string]string{"concordia.yaml": workspaceText}

	for k, v := range files {
		tree[k] = v
	}

	testutil.WriteTree(t, dir, tree)

	return dir
}

func pkVendored() string {
	return "    - use: " + pkVendorRel + "\n      sha256: " + pkSum(pkPackText) + "\n"
}

func pkCorePack(t *testing.T) policy.PackInfo {
	t.Helper()

	text, _ := packs.Source("core")

	info, problems := policy.DescribePack(policy.PolicyFile{Path: "core", Text: string(text)})
	if len(problems) != 0 {
		t.Fatalf("core pack: %+v", problems)
	}

	return info
}

// pkCheck is one value that a test compares with the value that it wants.
type pkCheck struct {
	got  any
	want any
	name string
}

// pkExpectAll reports an error for every check whose value differs from the wanted one.
func pkExpectAll(t *testing.T, checks ...pkCheck) {
	t.Helper()

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// pkExpectOK stops the test when the run did not exit with 0.
func pkExpectOK(t *testing.T, res result) {
	t.Helper()

	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// pkExpectStderr reports an error when the run did not exit with the code or its stderr lacks one of the texts.
func pkExpectStderr(t *testing.T, res result, code int, wants ...string) {
	t.Helper()

	if res.Code != code {
		t.Errorf("exit code = %d, want %d, stderr = %q", res.Code, code, res.Stderr)

		return
	}

	for _, want := range wants {
		if !strings.Contains(res.Stderr, want) {
			t.Errorf("exit code = %d, stderr = %q, want %s", res.Code, res.Stderr, want)
		}
	}
}

// pkExpectAdded stops the test unless `policy add` succeeded and reported the entry.
func pkExpectAdded(t *testing.T, res result, entry string) {
	t.Helper()

	if res.Code != 0 || res.Stdout != "added "+entry+"\n" {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

// pkOnlyPack loads the workspace of dir and gives its only pack entry.
func pkOnlyPack(t *testing.T, dir string) workspace.PolicyPackUse {
	t.Helper()

	w, err := workspace.Load(filepath.Join(dir, "concordia.yaml"), "")
	if err != nil || len(w.Policies.Packs) != 1 {
		t.Fatalf("workspace %+v err %v", w, err)
	}

	return w.Policies.Packs[0]
}

// pkDecodeJSON decodes the JSON output of a run.
func pkDecodeJSON[T any](t *testing.T, stdout string) T { //nolint:ireturn // T is the caller's result type, not an interface.
	t.Helper()

	var out T
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}

	return out
}

type pkListedRule struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Enabled  bool   `json:"enabled"`
}

type pkBuiltinPack struct { //nolint:govet // a test decoder: the field order follows the JSON output.
	Name      string `json:"name"`
	ID        string `json:"id"`
	Version   string `json:"version"`
	Digest    string `json:"digest"`
	RuleCount int    `json:"ruleCount"`
	Rules     []struct {
		ID string `json:"id"`
	} `json:"rules"`
}

type pkConfiguredPack struct { //nolint:govet // a test decoder: the field order follows the JSON output.
	Source    string         `json:"source"`
	ID        string         `json:"id"`
	Version   string         `json:"version"`
	Digest    string         `json:"digest"`
	Status    string         `json:"status"`
	Problem   string         `json:"problem"`
	RuleCount int            `json:"ruleCount"`
	Overrides int            `json:"overrides"`
	Rules     []pkListedRule `json:"rules"`
}

type pkListing struct {
	Builtin    []pkBuiltinPack    `json:"builtin"`
	Configured []pkConfiguredPack `json:"configured"`
}

func TestPolicyListShowsBuiltinPacksWithoutAWorkspace(t *testing.T) {
	t.Parallel()

	core := pkCorePack(t)

	res := runCLI(t, "policy", "list")
	pkExpectOK(t, res)

	wantContains(t, res.Stdout, "Built-in packs:", "builtin:core  "+core.Version+"  "+fmt.Sprintf("%d rules", len(core.Rules)), "Packs of the workspace:", "none")

	if strings.Contains(res.Stdout, core.Rules[0].ID) {
		t.Errorf("rules listed without --rules:\n%s", res.Stdout)
	}

	withRules := runCLI(t, "policy", "list", "--rules")
	for _, rule := range core.Rules {
		wantContains(t, withRules.Stdout, rule.ID, string(rule.Severity), rule.Name)
	}
}

// pkExpectBuiltinCore checks the listing of the built-in core pack.
func pkExpectBuiltinCore(t *testing.T, got pkListing, core policy.PackInfo, text string) {
	t.Helper()

	coreAt := slices.IndexFunc(got.Builtin, func(b pkBuiltinPack) bool { return b.Name == "core" })
	if coreAt < 0 {
		t.Fatalf("builtin = %+v", got.Builtin)
	}

	builtinCore := got.Builtin[coreAt]
	pkExpectAll(t,
		pkCheck{len(got.Builtin), len(packs.Names()), "built-in packs"},
		pkCheck{builtinCore.ID, "core", "built-in id"},
		pkCheck{builtinCore.Digest, pkSum(text), "built-in digest"},
		pkCheck{builtinCore.RuleCount, len(core.Rules), "built-in rule count"},
		pkCheck{len(builtinCore.Rules), len(core.Rules), "built-in rules"},
	)
}

// pkExpectConfiguredBuiltin checks the configured entry of builtin:core with two overrides.
func pkExpectConfiguredBuiltin(t *testing.T, builtin *pkConfiguredPack, text string) {
	t.Helper()

	pkExpectAll(t,
		pkCheck{builtin.Source, "builtin:core", "builtin entry source"},
		pkCheck{builtin.Status, "builtin", "builtin entry status"},
		pkCheck{builtin.Overrides, 2, "builtin entry overrides"},
		pkCheck{builtin.Digest, pkSum(text), "builtin entry digest"},
	)

	pkExpectAll(t,
		pkCheck{builtin.Rules[0].Severity, "info", "severity of the first rule"},
		pkCheck{builtin.Rules[0].Enabled, true, "first rule enabled"},
		pkCheck{builtin.Rules[1].Enabled, false, "second rule enabled"},
	)
}

// pkExpectConfiguredVendored checks the configured entry of the vendored acme pack.
func pkExpectConfiguredVendored(t *testing.T, vendored *pkConfiguredPack) {
	t.Helper()

	pkExpectAll(t,
		pkCheck{vendored.Source, pkVendorRel, "vendored source"},
		pkCheck{vendored.ID, "acme", "vendored id"},
		pkCheck{vendored.Version, "1.2.0", "vendored version"},
		pkCheck{vendored.Status, "verified", "vendored status"},
		pkCheck{vendored.RuleCount, 2, "vendored rule count"},
		pkCheck{vendored.Digest, pkSum(pkPackText), "vendored digest"},
	)
}

func TestPolicyListJSONHasStableKeys(t *testing.T) {
	t.Parallel()

	core := pkCorePack(t)
	text, _ := packs.Source("core")
	dir := pkDir(t, pkWorkspaceBase+"policies:\n  packs:\n    - use: builtin:core\n      rules:\n        "+core.Rules[0].ID+": { severity: info }\n        "+core.Rules[1].ID+": { enabled: false }\n"+pkVendored(),
		map[string]string{pkVendorRel: pkPackText})

	res := runIn(t, dir, "policy", "list", "--rules", "--format", "json")
	pkExpectOK(t, res)

	got := pkDecodeJSON[pkListing](t, res.Stdout)
	pkExpectBuiltinCore(t, got, core, string(text))

	if len(got.Configured) != 2 {
		t.Fatalf("configured = %+v", got.Configured)
	}

	pkExpectConfiguredBuiltin(t, &got.Configured[0], string(text))
	pkExpectConfiguredVendored(t, &got.Configured[1])

	// The keys are camelCase and the output is stable.
	for _, key := range []string{`"ruleCount"`, `"configured"`, `"builtin"`, `"overrides"`} {
		wantContains(t, res.Stdout, key)
	}

	if again := runIn(t, dir, "policy", "list", "--rules", "--format", "json"); again.Stdout != res.Stdout {
		t.Error("the JSON output is not stable")
	}
}

func TestPolicyListShowsTheStatusOfABadPackAndStillSucceeds(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase+"policies:\n  packs:\n    - use: "+pkVendorRel+"\n      sha256: sha256:"+strings.Repeat("0", 64)+"\n    - use: builtin:nope\n",
		map[string]string{pkVendorRel: pkPackText})

	res := runIn(t, dir, "policy", "list", "--format", "json")
	pkExpectOK(t, res)

	got := pkDecodeJSON[pkListing](t, res.Stdout)
	if len(got.Configured) != 2 {
		t.Fatalf("configured = %+v", got.Configured)
	}

	pkExpectAll(t,
		pkCheck{got.Configured[0].Status, "mismatch", "status of the bad digest"},
		pkCheck{got.Configured[1].Status, "unknown", "status of the unknown pack"},
		pkCheck{got.Configured[0].Problem != "", true, "problem of the bad digest is given"},
	)
}

func TestPolicyListFailsForAnInvalidWorkspace(t *testing.T) {
	t.Parallel()

	pkExpectStderr(t, runIn(t, pkDir(t, "observation: 3\n", nil), "policy", "list"), 2, "CO-WRK-002")
}

func TestPolicyListSaysTheViewsPackAppliesAutomatically(t *testing.T) {
	t.Parallel()

	res := runCLI(t, "policy", "list")
	pkExpectOK(t, res)
	wantContains(t, res.Stdout, "builtin:views", "applies automatically when views are declared")
}

func TestPolicyListShowsTheADRGovernancePack(t *testing.T) {
	t.Parallel()

	res := runCLI(t, "policy", "list")
	pkExpectOK(t, res)

	wantContains(t, res.Stdout, "builtin:adr-governance  1.0.0  6 rules")

	withRules := runCLI(t, "policy", "list", "--rules")
	for _, id := range []string{"GOV-ADR-001", "GOV-ADR-002", "GOV-ADR-003", "GOV-ADR-004", "GOV-ADR-005", "GOV-ADR-006"} {
		wantContains(t, withRules.Stdout, id)
	}
}

func TestPolicyCommandsAreInTheHelp(t *testing.T) {
	t.Parallel()

	res := runCLI(t, "policy", "--help")
	wantContains(t, res.Stdout, "list", "add", "verify")

	for _, sub := range []string{"list", "add", "verify"} {
		if help := runCLI(t, "policy", sub, "--help"); help.Code != 0 {
			t.Errorf("policy %s --help: exit code %d", sub, help.Code)
		}
	}

	if add := runCLI(t, "policy", "add", "--help"); !strings.Contains(add.Stdout, "--force") || !strings.Contains(add.Stdout, "--id") {
		t.Errorf("add help:\n%s", add.Stdout)
	}

	pkExpectStderr(t, runCLI(t, "policy", "add"), 2, "CO-WRK-010")
}
