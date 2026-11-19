package e2e_test

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/jsonschema"
)

const (
	commerceProject = "e2e-commerce"
	architectureRel = "architecture/commerce.calm.json"
	bindingsRel     = "bindings/concordia.bindings.yaml"
	billingSource   = "services/billing/src/InvoiceService.ts"
	legacySource    = "services/legacy/src/Legacy.kt"
	billingNode     = "billing-service"
	ruleBoundary    = "CO-BND-001"
	ruleCoverage    = "CO-COV-001"
	verdictFail     = "violation"
)

// commerceRun is the state the commerce subtests hand to each other.
type commerceRun struct {
	repo *repo
	doc  *checkDoc
}

// The fixture against the real CBM. The subtests run in sequence and share one repository.
func TestCommerce(t *testing.T) {
	requireE2E(t)

	run := &commerceRun{repo: newRepo(t, repoSpec{source: fixtureDir, oldName: "concordia-fixture-commerce", project: commerceProject, dirName: "commerce"})}

	t.Run("model validate: the fixture is valid", run.modelValidate)
	t.Run("model validate: a node-type that is not correct gives exit 2, CO-INT-002, and the architecture path", run.modelBadNodeType)
	t.Run("model validate: a decorator with CALM 1.3 fields under a 1.2 architecture gives exit 2 and CO-INT-008", run.modelBadDecorator)
	t.Run("index: prints a generation of the project and writes coverage.json", run.index)
	t.Run("doctor --json: cbm is available", run.doctor)
	t.Run("bind validate: valid; a bad buildTarget label gives exit 2 and CO-BND-002", run.bindValidate)
	t.Run("check: forbidden direct dependency and the other rule verdicts", run.checkVerdicts)
	t.Run("coverage gap: CO-COV-001 is unknown (CO-CON-021) and --rule CO-COV-001 exits 4", run.coverageGap)
	t.Run("check --format sarif --output agrees with the SARIF 2.1.0 schema", run.sarif)
	t.Run("check --format junit: well-formed XML with 5 test cases", run.junit)
	t.Run("check --format pretty: a FAIL block with an IMPORTS evidence step", run.pretty)
	t.Run("render --format dsl: every element has calm.unique-id", run.renderDSL)
	t.Run("render --format mermaid: exports with Structurizr, else exit 5 with CO-PRJ-001", run.renderMermaid)
	t.Run("two check runs give the same result without run.id, startedAt, finishedAt, and host", run.deterministicRuns)
	t.Run("check --offline: a url-mapped resource that is not in the lock gives exit 6 and CO-INT-012", run.offlineWithoutLock)
}

func (c *commerceRun) modelValidate(t *testing.T) {
	if res := c.repo.gu("model", "validate"); res.code != 0 {
		t.Fatalf("exit %d: %s", res.code, res.stderr)
	}
}

func (c *commerceRun) modelBadNodeType(t *testing.T) {
	good := c.repo.read(architectureRel)

	t.Cleanup(func() { c.repo.write(architectureRel, good) })

	c.repo.write(architectureRel, strings.Replace(good, `"node-type": "actor"`, `"node-type": 42`, 1))

	res := c.repo.gu("model", "validate")
	if res.code != 2 {
		t.Fatalf("exit %d, want 2", res.code)
	}

	containsAll(t, res.output(), "CO-INT-002", architectureRel)
}

func (c *commerceRun) modelBadDecorator(t *testing.T) {
	fixture, err := os.ReadFile("../fixtures/calm/decorator-1.3-fields-under-1.2.json")
	if err != nil {
		t.Fatal(err)
	}

	const decoratorRel = "decorators/deployment.json"

	t.Cleanup(func() { _ = os.Remove(c.repo.path(decoratorRel)) })

	c.repo.write(decoratorRel, strings.Replace(string(fixture), "../architecture/a.calm.json", "../"+architectureRel, 1))

	res := c.repo.gu("model", "validate", "--decorator", decoratorRel)
	if res.code != 2 {
		t.Fatalf("exit %d, want 2", res.code)
	}

	containsAll(t, res.output(), "CO-INT-008")
}

func (c *commerceRun) index(t *testing.T) {
	res := c.repo.gu("index")
	if res.code != 0 {
		t.Fatalf("exit %d: %s", res.code, res.stderr)
	}

	containsAll(t, res.stdout, "generation: cbm:"+commerceProject+":")

	var cov struct {
		Project         string   `json:"project"`
		GraphGeneration string   `json:"graphGeneration"`
		Excluded        []string `json:"excluded"`
	}

	if err := json.Unmarshal([]byte(c.repo.read("build/concordia/coverage.json")), &cov); err != nil {
		t.Fatal(err)
	}

	if cov.Project != commerceProject {
		t.Errorf("project = %q", cov.Project)
	}

	if !regexp.MustCompile("^cbm:" + commerceProject + ":[0-9a-f]{16}$").MatchString(cov.GraphGeneration) {
		t.Errorf("graphGeneration = %q", cov.GraphGeneration)
	}

	if !slices.Equal(cov.Excluded, []string{legacySource}) {
		t.Errorf("excluded = %v", cov.Excluded)
	}
}

func (c *commerceRun) doctor(t *testing.T) {
	res := c.repo.gu("doctor", "--json")
	if res.code != 0 {
		t.Fatalf("exit %d: %s", res.code, res.stderr)
	}

	var report struct {
		Capabilities []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"capabilities"`
	}

	if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
		t.Fatal(err)
	}

	status := ""

	for _, capability := range report.Capabilities {
		if capability.Name == "cbm" {
			status = capability.Status
		}
	}

	if status != "available" {
		t.Errorf("cbm status = %q", status)
	}
}

func (c *commerceRun) bindValidate(t *testing.T) {
	if res := c.repo.gu("bind", "validate"); res.code != 0 {
		t.Fatalf("exit %d: %s", res.code, res.stderr)
	}

	good := c.repo.read(bindingsRel)

	t.Cleanup(func() { c.repo.write(bindingsRel, good) })

	c.repo.write(bindingsRel,
		good+"  - id: bind-build\n    architectureRef: calm:node:billing-service\n    match:\n      any:\n        - buildTarget: \"services/billing\"\n")

	res := c.repo.gu("bind", "validate")
	if res.code != 2 {
		t.Fatalf("exit %d, want 2", res.code)
	}

	containsAll(t, res.output(), "CO-BND-002")
}

func (c *commerceRun) checkVerdicts(t *testing.T) {
	doc, res := c.repo.checkJSON(nil)
	c.doc = doc

	if res.code != 1 {
		t.Fatalf("exit %d, want 1: %s", res.code, res.stderr)
	}

	if !strings.HasPrefix(doc.Run.GraphGeneration, "cbm:"+commerceProject+":") {
		t.Errorf("graphGeneration = %q", doc.Run.GraphGeneration)
	}

	requireBoundaryFindings(t, doc)
	requireContractPackageAllowed(t, doc)
	requireCommerceVerdicts(t, doc)
	requireCommerceFindingKinds(t, doc)
}

// requireBoundaryFindings checks the forbidden dependency from billing to fulfillment is reported and active.
func requireBoundaryFindings(t *testing.T, doc *checkDoc) {
	t.Helper()

	boundary := doc.findingsWhere(func(f *checkFinding) bool { return f.RuleID == ruleBoundary })
	if len(boundary) == 0 {
		t.Error("no CO-BND-001 finding")
	}

	for _, f := range boundary {
		if f.Source.ArchitectureID != billingNode || f.Target.ArchitectureID != "fulfillment-service" {
			t.Errorf("endpoints = %s -> %s", f.Source.ArchitectureID, f.Target.ArchitectureID)
		}

		if f.locationPath() != billingSource {
			t.Errorf("location = %q", f.locationPath())
		}

		if f.Disposition != "active" {
			t.Errorf("disposition = %q", f.Disposition)
		}
	}
}

// requireContractPackageAllowed checks the contract package is allowed: no finding has a target there.
func requireContractPackageAllowed(t *testing.T, doc *checkDoc) {
	t.Helper()

	for _, f := range doc.Findings {
		if f.Target.Observed != nil && strings.HasPrefix(f.Target.Observed.FilePath, "packages/fulfillment-contract") {
			t.Errorf("finding targets the contract package: %+v", f)
		}
	}
}

// requireCommerceVerdicts checks the four rules of the fixture fail.
func requireCommerceVerdicts(t *testing.T, doc *checkDoc) {
	t.Helper()

	verdicts := map[string]string{
		ruleBoundary:  verdictFail,
		"CO-CYC-001":  verdictFail, // billing <-> fulfillment
		"CO-DECL-001": verdictFail, // fulfillment -> billing is not declared
		"CO-LYR-001":  verdictFail,
	}
	for id, want := range verdicts {
		rule := doc.rule(id)
		if rule == nil || rule.Verdict != want {
			t.Errorf("verdict of %s = %+v, want %s", id, rule, want)
		}
	}
}

// requireCommerceFindingKinds checks the kind of the first finding of each rule.
func requireCommerceFindingKinds(t *testing.T, doc *checkDoc) {
	t.Helper()

	kinds := map[string]string{"CO-CYC-001": "cycle", "CO-DECL-001": "undeclared", "CO-LYR-001": "layer-violation"}
	for id, want := range kinds {
		found := doc.findingsWhere(func(f *checkFinding) bool { return f.RuleID == id })
		if len(found) == 0 || found[0].Kind != want {
			t.Errorf("first finding of %s: %+v, want kind %s", id, found, want)
		}
	}
}

func (c *commerceRun) coverageGap(t *testing.T) {
	cov := c.doc.rule(ruleCoverage)
	if cov == nil {
		t.Fatal("no CO-COV-001 rule")
	}

	if cov.Verdict != "unknown" {
		t.Errorf("verdict = %q", cov.Verdict)
	}

	codes := make([]string, 0, len(cov.Reasons))
	for _, reason := range cov.Reasons {
		codes = append(codes, reason.Code)
	}

	if !slices.Contains(codes, "CO-CON-021") {
		t.Errorf("reasons = %v", codes)
	}

	// Legacy.kt is excluded by the workspace, but stays in the total.
	if !slices.Contains(cov.Coverage.Files.Excluded, legacySource) {
		t.Errorf("excluded = %v", cov.Coverage.Files.Excluded)
	}

	if cov.Coverage.Files.Covered >= cov.Coverage.Files.Total {
		t.Errorf("covered %d, total %d", cov.Coverage.Files.Covered, cov.Coverage.Files.Total)
	}

	if res := c.repo.gu("check", "--rule", ruleCoverage); res.code != 4 {
		t.Errorf("exit %d, want 4: %s", res.code, res.stderr)
	}
}

func (c *commerceRun) sarif(t *testing.T) {
	res := c.repo.gu("--format", "sarif", "--output", "r.sarif", "check")
	if res.code != 1 {
		t.Fatalf("exit %d, want 1: %s", res.code, res.stderr)
	}

	text := c.repo.read("r.sarif")
	requireValidSarif(t, text)

	var sarif struct {
		Runs []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
			} `json:"results"`
		} `json:"runs"`
	}

	if err := json.Unmarshal([]byte(text), &sarif); err != nil {
		t.Fatal(err)
	}

	if len(sarif.Runs) != 1 || len(sarif.Runs[0].Results) != len(c.doc.Findings) {
		t.Errorf("SARIF has %+v results, want %d", sarif.Runs, len(c.doc.Findings))
	}
}

// requireValidSarif checks the text against the SARIF 2.1.0 schema.
func requireValidSarif(t *testing.T, text string) {
	t.Helper()

	schemaDoc, err := os.ReadFile(sarifSchemaPath)
	if err != nil {
		t.Fatal(err)
	}

	schema, err := jsonschema.Compile(sarifSchemaID, schemaDoc)
	if err != nil {
		t.Fatal(err)
	}

	if errs := schema.ValidateJSON([]byte(text)); len(errs) > 0 {
		t.Fatalf("SARIF is not valid: %+v", errs)
	}
}

func (c *commerceRun) junit(t *testing.T) {
	res := c.repo.gu("--format", "junit", "--output", "r.xml", "check")
	if res.code != 1 {
		t.Fatalf("exit %d, want 1: %s", res.code, res.stderr)
	}

	text := c.repo.read("r.xml")
	if !xmlIsWellFormed(text) {
		t.Error("r.xml is not well-formed")
	}

	if n := strings.Count(text, "<testcase "); n != 5 {
		t.Errorf("%d test cases, want 5", n)
	}
}

func (c *commerceRun) pretty(t *testing.T) {
	res := c.repo.gu("check")
	if res.code != 1 {
		t.Fatalf("exit %d, want 1", res.code)
	}

	containsAll(t, res.stdout, "FAIL CO-BND-001", "  imports ")

	summary := regexp.MustCompile(`\d+ active error\(s\), 0 baselined, 0 waived, 0 expired waiver\(s\), 1 unknown`)
	if !summary.MatchString(res.stdout) {
		t.Errorf("no summary line in:\n%s", res.stdout)
	}
}

func (c *commerceRun) renderDSL(t *testing.T) {
	res := c.repo.gu("--format", "dsl", "render")
	if res.code != 0 {
		t.Fatalf("exit %d: %s", res.code, res.stderr)
	}

	ids := dslUniqueIDs(t, c.repo.read("build/concordia/structurizr/workspace.dsl"))
	slices.Sort(ids)

	want := []string{"billing-service", "commerce", "customer", "fulfillment-service", "ledger-database", "ledger-service", "legacy-service"}
	if !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

// dslUniqueIDs returns the calm.unique-id of every element of the DSL text and reports each element without one.
func dslUniqueIDs(t *testing.T, text string) []string {
	t.Helper()

	lines := strings.Split(text, "\n")
	element := regexp.MustCompile(`^\s*gu_\w+ = (person|softwareSystem|container|component) `)

	var ids []string

	for i, line := range lines {
		if !element.MatchString(line) {
			continue
		}

		if id, ok := dslElementUniqueID(t, lines, i); ok {
			ids = append(ids, id)
		}
	}

	return ids
}

// dslElementUniqueID reads the calm.unique-id property that must follow the element on line i.
func dslElementUniqueID(t *testing.T, lines []string, i int) (string, bool) {
	t.Helper()

	if i+2 >= len(lines) || strings.TrimSpace(lines[i+1]) != "properties {" {
		t.Errorf("element %q has no properties block", lines[i])

		return "", false
	}

	m := regexp.MustCompile(`^\s*"calm\.unique-id" "([^"]+)"$`).FindStringSubmatch(lines[i+2])
	if m == nil {
		t.Errorf("element %q has no calm.unique-id", lines[i])

		return "", false
	}

	return m[1], true
}

func (c *commerceRun) renderMermaid(t *testing.T) {
	res := c.repo.gu("--format", "mermaid", "render")

	switch res.code {
	case 5:
		containsAll(t, res.stderr, "CO-PRJ-001")
	case 0:
		containsAll(t, res.stdout+res.stderr, "mermaid")
	default:
		t.Errorf("exit %d: %s", res.code, res.stderr)
	}
}

func (c *commerceRun) deterministicRuns(t *testing.T) {
	env := []string{"CONCORDIA_NOW=2026-06-01T12:00:00Z"}

	// The first check of the session wrote .concordia.lock; it is a stable input now.
	_, first := c.repo.checkJSON(env)
	_, second := c.repo.checkJSON(env)

	if first.code != second.code {
		t.Errorf("exit codes differ: %d, %d", first.code, second.code)
	}

	if a, b := withoutVolatile(t, first.stdout), withoutVolatile(t, second.stdout); a != b {
		t.Errorf("results differ:\n%s\n%s", a, b)
	}
}

func (c *commerceRun) offlineWithoutLock(t *testing.T) {
	if res := c.repo.gu("--offline", "check"); res.code != 1 { // locked: offline works
		t.Fatalf("with lock: exit %d, want 1: %s", res.code, res.stderr)
	}

	lock := c.repo.read(".concordia.lock")

	t.Cleanup(func() { c.repo.write(".concordia.lock", lock) })

	if err := os.Remove(c.repo.path(".concordia.lock")); err != nil {
		t.Fatal(err)
	}

	res := c.repo.gu("--offline", "check")
	if res.code != 6 {
		t.Fatalf("without lock: exit %d, want 6", res.code)
	}

	containsAll(t, res.stderr, "CO-INT-012")
}
