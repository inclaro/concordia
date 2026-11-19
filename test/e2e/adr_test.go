package e2e_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	adrFixtureDir = "../fixtures/adr"
	adrProjectOld = "concordia-fixture-adr"
	adrRegister   = "governance/decisions.yaml"
	adrModel      = "architecture/orders.calm.json"
	adrResults    = "build/concordia/results.json"
	adrADR0003    = "docs/adr/0003-orders-own-database.md"
	adrADR0007    = "docs/adr/0007-orders-managed-database.md"
)

// adrRegisterReplaced is the register of docs/how-to/govern-architecture-decisions.md after "Replace a decision".
const adrRegisterReplaced = `apiVersion: concordia.io/v1alpha1
kind: DecisionRegister
decisions:
  - id: ADR-0003
    path: docs/adr/0003-orders-own-database.md
    expect: historical
    categories: [system-boundaries]
    governs:
      - ref: calm:node:orders-db
      - ref: calm:relationship:orders-to-db
      - ref: calm:interface:orders-db/sql
      - select: {tags: [orders-data]}
    released:
      - ref: calm:interface:orders-db/sql
        reason: The managed database exposes no direct SQL interface to the orders service.
  - id: ADR-0007
    path: docs/adr/0007-orders-managed-database.md
    categories: [system-boundaries]
    governs:
      - ref: calm:node:orders-db
      - ref: calm:relationship:orders-to-db
    enforcement: required
    enforcedBy:
      - rule: CORE-DB-001
        pack: {id: core, version: 1.0.0}
`

// adrTriggers is the adr.triggers block of the how-to "Require a decision for a change".
const adrTriggers = `  triggers:
    - id: TRG-ORDERS-CODE
      category: system-boundaries
      paths: ["services/orders/**"]
    - id: TRG-DB-LINK
      category: system-boundaries
      architecture: [relationship-added, relationship-removed]
      nodeTypes: [database]
`

// newADRRepo copies the fixture; the CBM project is named after the test so that no two tests share one.
func newADRRepo(t *testing.T, project string) *repo {
	t.Helper()

	return newRepo(t, repoSpec{source: adrFixtureDir, oldName: adrProjectOld, project: project, dirName: "adr"})
}

// adrDigest is the digest of the CALM model, the register, and every ADR file.
func (r *repo) adrDigest() string {
	r.t.Helper()

	sum := sha256.New()

	for _, root := range []string{adrModel, adrRegister, "docs/adr"} {
		abs := r.path(root)

		err := filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}

			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return readErr
			}

			sum.Write([]byte(p + "\x00"))
			sum.Write(data)

			return nil
		})
		if err != nil {
			r.t.Fatal(err)
		}
	}

	return hex.EncodeToString(sum.Sum(nil))
}

// adrResultsDoc reads build/concordia/results.json.
func (r *repo) adrResultsDoc() map[string]any {
	r.t.Helper()

	var doc map[string]any
	if err := json.Unmarshal([]byte(r.read(adrResults)), &doc); err != nil {
		r.t.Fatal(err)
	}

	return doc
}

// adrRuleVerdict is the verdict of a rule in results.json ("" when absent).
func (r *repo) adrRuleVerdict(id string) string {
	r.t.Helper()

	rules, _ := r.adrResultsDoc()["rules"].([]any)
	for _, raw := range rules {
		rule, _ := raw.(map[string]any)
		if rule["ruleId"] == id {
			verdict, _ := rule["verdict"].(string)

			return verdict
		}
	}

	return ""
}

func (r *repo) adrFindings() []map[string]any {
	r.t.Helper()

	raw, _ := r.adrResultsDoc()["findings"].([]any)
	out := make([]map[string]any, 0, len(raw))

	for _, f := range raw {
		m, _ := f.(map[string]any)
		out = append(out, m)
	}

	return out
}

func (r *repo) mustCLI(want int, args ...string) cliResult {
	r.t.Helper()

	res := r.gu(args...)
	if res.code != want {
		r.t.Fatalf("concordia %s: exit %d, want %d\n%s", strings.Join(args, " "), res.code, want, res.output())
	}

	return res
}

// index and check leave the model, the register, and the ADR files byte-identical; two checks on identical inputs
// give the same governance area, decision summary, and findings.
func TestADRCheckIsReadOnlyAndDeterministic(t *testing.T) {
	requireE2E(t)

	repo := newADRRepo(t, "e2e-adr-readonly")
	before := repo.adrDigest()

	repo.mustCLI(0, "index")
	repo.mustCLI(1, "check")

	first := repo.adrResultsDoc()

	repo.mustCLI(1, "check")

	second := repo.adrResultsDoc()

	if after := repo.adrDigest(); after != before {
		t.Error("index or check changed the model, the register, or an ADR file")
	}

	for _, key := range []string{"areas", "decisionSummary", "findings"} {
		a, errA := json.Marshal(first[key])
		b, errB := json.Marshal(second[key])

		if errA != nil || errB != nil {
			t.Fatal(errA, errB)
		}

		if len(a) < 3 || !bytes.Equal(a, b) {
			t.Errorf("%s differs between two runs or is empty:\n%s\n%s", key, a, b)
		}
	}

	if _, ok := first["decisionSummary"]; !ok {
		t.Error("results.json has no decisionSummary")
	}
}

// adr validate reads files only: no network flag trouble, no CBM on the PATH.
func TestADRValidateOfflineWithoutCBM(t *testing.T) {
	requireE2E(t)

	repo := newADRRepo(t, "e2e-adr-offline")

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}

	res := repo.cli([]string{"PATH=" + filepath.Dir(gitPath)}, "--offline", "adr", "validate")
	if res.code != 0 {
		t.Fatalf("adr validate --offline (%d): %s", res.code, res.output())
	}

	if !strings.Contains(res.output(), "valid") {
		t.Errorf("unexpected output: %s", res.output())
	}
}

// The commands of docs/how-to/govern-architecture-decisions.md on test/fixtures/adr, against the real CBM.
func TestADRDocumentedCommands(t *testing.T) {
	requireE2E(t)

	repo := newADRRepo(t, "e2e-adr-commands")

	// Register a decision.
	repo.mustCLI(0, "adr", "validate")
	repo.mustCLI(0, "index")

	res := repo.mustCLI(0, "check", "--rule", "GOV-ADR-001")
	containsAll(t, res.stdout, "ADR-3 accepted (", "presence committed")

	if v := repo.adrRuleVerdict("GOV-ADR-001"); v != "pass" {
		t.Errorf("GOV-ADR-001 verdict %q, want pass", v)
	}

	// Replace a decision: the superseded ADR-0003 violates, ADR-0007 is a candidate that is not applied.
	repo.mustCLI(1, "check", "--rule", "GOV-ADR-002")

	if v := repo.adrRuleVerdict("GOV-ADR-002"); v != "violation" {
		t.Errorf("GOV-ADR-002 verdict %q, want violation", v)
	}

	findings := repo.adrFindings()
	if len(findings) != 1 {
		t.Fatalf("GOV-ADR-002: %d findings, want 1", len(findings))
	}

	decision, _ := findings[0]["decision"].(map[string]any)
	if decision["path"] != adrADR0003 {
		t.Errorf("finding decision path %v, want %s", decision["path"], adrADR0003)
	}

	candidates, _ := decision["candidateSuccessors"].([]any)
	if len(candidates) != 1 {
		t.Fatalf("candidateSuccessors %v, want one", candidates)
	}

	candidate, _ := candidates[0].(map[string]any)
	if candidate["path"] != adrADR0007 || candidate["applied"] != false {
		t.Errorf("candidate %v, want ADR-0007 with applied false", candidate)
	}

	// Edit the register, commit, validate, index (the graph is stale after a commit), check.
	repo.write(adrRegister, adrRegisterReplaced)
	repo.commitAll("docs: replace ADR-0003 by ADR-0007")
	repo.mustCLI(0, "adr", "validate")
	repo.mustCLI(0, "index")
	repo.mustCLI(0, "check", "--rule", "GOV-ADR-002")

	if v := repo.adrRuleVerdict("GOV-ADR-002"); v != "pass" {
		t.Errorf("GOV-ADR-002 verdict %q after the replacement, want pass", v)
	}

	// Require a decision for a change.
	workspace := repo.read(workspaceFile)
	repo.write(workspaceFile, strings.Replace(workspace, "  files: [governance/decisions.yaml]\n", "  files: [governance/decisions.yaml]\n"+adrTriggers, 1))
	repo.mustCLI(0, "adr", "validate")
	repo.commitAll("docs: add ADR triggers")

	repo.write("services/orders/src/change.txt", "a change without a decision\n")
	repo.commitAll("feat: change the orders service")
	repo.mustCLI(0, "index")
	repo.mustCLI(0, "check", "--changed", "HEAD~1", "--rule", "GOV-ADR-006")

	if v := repo.adrRuleVerdict("GOV-ADR-006"); v != "violation" {
		t.Errorf("GOV-ADR-006 verdict %q, want violation", v)
	}

	// Roll out: shadow, then advisory.
	committed := repo.read(workspaceFile)

	for _, mode := range []string{"shadow", "advisory"} {
		entry := "check:\n  enforcement:\n    - family: decision-governance\n      mode: " + mode + "\n"
		repo.write(workspaceFile, strings.Replace(committed, "check:\n", entry, 1))
		repo.mustCLI(0, "check", "--changed", "HEAD~1")

		summary := repo.jq(".areas.adrGovernance, (.decisionSummary.decisions[] | {id, presence: .presence.state, applicability: .applicability.state, enforcement: .enforcement.state})")
		containsAll(t, summary, `"verdict"`, `"ADR-7"`, `"applicability": "active"`)
	}
}

// jq runs the jq query of the how-to on results.json.
func (r *repo) jq(query string) string {
	r.t.Helper()

	out, err := exec.CommandContext(r.t.Context(), "jq", query, r.path(adrResults)).CombinedOutput()
	if err != nil {
		r.t.Fatalf("jq: %v\n%s", err, out)
	}

	return string(out)
}
