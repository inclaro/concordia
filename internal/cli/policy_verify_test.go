package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyVerifySucceeds(t *testing.T) {
	t.Parallel()

	dir := pkDir(t, pkWorkspaceBase+"policies:\n  packs:\n    - use: builtin:core\n"+pkVendored(), map[string]string{pkVendorRel: pkPackText})

	res := runIn(t, dir, "policy", "verify")
	pkExpectOK(t, res)

	wantContains(t, res.Stdout, "policy verified: 2 packs")

	parsed := pkDecodeJSON[struct {
		Status string `json:"status"`
		Packs  int    `json:"packs"`
		Rules  int    `json:"rules"`
	}](t, runIn(t, dir, "policy", "verify", "--format", "json").Stdout)

	pkExpectAll(t, pkCheck{parsed.Status, "ok", "status"}, pkCheck{parsed.Packs, 2, "packs"})

	if parsed.Rules <= 2 {
		t.Errorf("json = %+v", parsed)
	}
}

func TestPolicyVerifyFailsWithExitCode2(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		packs string
		files map[string]string
		code  string
	}{
		"digest mismatch": {"    - use: " + pkVendorRel + "\n      sha256: sha256:" + strings.Repeat("0", 64) + "\n", map[string]string{pkVendorRel: pkPackText}, "CO-POL-010"},
		"missing file":    {pkVendored(), nil, "CO-POL-011"},
		"unknown builtin": {"    - use: builtin:nope\n", nil, "CO-POL-012"},
		"no pin":          {"    - use: " + pkVendorRel + "\n", map[string]string{pkVendorRel: pkPackText}, "CO-POL-013"},
		"unknown rule":    {"    - use: builtin:core\n      rules:\n        CORE-TYPO-001: { enabled: false }\n", nil, "CO-POL-015"},
		"duplicate rule id": {
			"    - use: builtin:core\n    - use: builtin:core\n", nil, "CO-POL-003",
		},
		"invalid pack": {
			"    - use: " + pkVendorRel + "\n      sha256: " + pkSum("kind: x\n") + "\n", map[string]string{pkVendorRel: "kind: x\n"}, "CO-POL-001",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := pkDir(t, pkWorkspaceBase+"policies:\n  files: []\n  packs:\n"+tc.packs, tc.files)

			pkExpectStderr(t, runIn(t, dir, "policy", "verify"), 2, tc.code)
		})
	}
}

const pkTypedPackText = `apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata: {id: acme, version: 1.2.0}
rules:
  - id: ACME-DEP-001
    name: Billing runs an image in production
    severity: error
    family: deployment
    enforcement: advisory
    subject: {calmNodes: [billing-service]}
    obligation:
      type: required
      relation: {kind: uses-image, direction: from-workload}
      scope: {environments: [production]}
      multiplicity: forAll
      applies:
        allOf:
          - {environment: production}
          - {workload: {namespace: payments, kind: Deployment}}
          - not: {protocol: http}
    evidence: {roles: [parsed-source-fact], minimumResolution: exact, completeness: complete}
  - id: ACME-DEP-002
    name: Nothing routes to the database
    severity: warning
    subject: {nodeTypes: [database]}
    obligation:
      type: forbidden
      multiplicity: exists
      relation: {kind: routes-to, direction: to-subject}
  - id: ACME-DECL-001
    name: Dependencies are declared
    severity: warning
    subject: {nodeTypes: [service]}
    declaredOnly: {edges: [IMPORTS]}
precedence:
  version: 1
  order: [ACME-DEP-001, ACME-DEP-002]
`

// pkTypedVerifyDir is a workspace whose only pack is the vendored pack with the given text.
func pkTypedVerifyDir(t *testing.T, text string) string {
	t.Helper()

	return pkDir(t, pkWorkspaceBase+"policies:\n  packs:\n    - use: "+pkVendorRel+"\n      sha256: "+pkSum(text)+"\n",
		map[string]string{pkVendorRel: text})
}

type pkTypedObligation struct {
	Type              string `json:"type"`
	Multiplicity      string `json:"multiplicity"`
	MinimumResolution string `json:"minimumResolution"`
	Completeness      string `json:"completeness"`
	Relation          struct {
		Kind      string `json:"kind"`
		Direction string `json:"direction"`
	} `json:"relation"`
	Roles []string `json:"roles"`
}

type pkTypedRule struct {
	ID         string            `json:"id"`
	Family     string            `json:"family"`
	Obligation pkTypedObligation `json:"obligation"`
}

type pkTypedVerification struct {
	TypedRules []pkTypedRule `json:"typedRules"`
	Rules      int           `json:"rules"`
}

// pkExpectTypedVerification checks the JSON output of `policy verify` for the typed pack.
func pkExpectTypedVerification(t *testing.T, parsed pkTypedVerification) {
	t.Helper()

	if len(parsed.TypedRules) != 2 {
		t.Fatalf("json = %+v", parsed)
	}

	first := parsed.TypedRules[0]
	pkExpectAll(t,
		pkCheck{parsed.Rules, 3, "rules"},
		pkCheck{first.ID, "ACME-DEP-001", "first rule"},
		pkCheck{first.Family, "deployment", "family"},
		pkCheck{first.Obligation.Type, "required", "obligation type"},
		pkCheck{first.Obligation.Multiplicity, "forAll", "multiplicity"},
		pkCheck{first.Obligation.MinimumResolution, "exact", "minimum resolution"},
		pkCheck{first.Obligation.Completeness, "complete", "completeness"},
		pkCheck{first.Obligation.Relation.Direction, "from-workload", "relation direction"},
		pkCheck{strings.Join(first.Obligation.Roles, ","), "parsed-source-fact", "roles"},
	)
}

// The text output of `policy verify` shows the declaration of each typed rule. The golden file is
// test/golden/policy/verify-typed.out.
func TestPolicyVerifyShowsTheDeclarationOfTypedRules(t *testing.T) {
	t.Parallel()

	dir := pkTypedVerifyDir(t, pkTypedPackText)

	res := runIn(t, dir, "policy", "verify")
	pkExpectOK(t, res)

	golden, err := os.ReadFile(filepath.Join("..", "..", "test", "golden", "policy", "verify-typed.out"))
	if err != nil {
		t.Fatal(err)
	}

	if res.Stdout != string(golden) {
		t.Errorf("stdout = %q\nwant     %q", res.Stdout, golden)
	}

	pkExpectTypedVerification(t, pkDecodeJSON[pkTypedVerification](t, runIn(t, dir, "policy", "verify", "--format", "json").Stdout))
}

func TestPolicyVerifyRejectsAnImplicitSelectorComposition(t *testing.T) {
	t.Parallel()

	text := strings.Replace(pkTypedPackText, "applies:\n        allOf:\n          - {environment: production}\n          - {workload: {namespace: payments, kind: Deployment}}\n          - not: {protocol: http}\n",
		"applies: {environment: production, protocol: https}\n", 1)

	pkExpectStderr(t, runIn(t, pkTypedVerifyDir(t, text), "policy", "verify"), 2, "CO-POL-002")
}
