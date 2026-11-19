package cli_test

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

// explainDir is a directory with the sample results file and no workspace file.
func explainDir(t *testing.T, extra map[string]string) string {
	t.Helper()

	text, err := canonjson.Marshal(testutil.SampleRun())
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	files := map[string]string{"results.json": string(text)}

	for k, v := range extra {
		files[k] = v
	}

	testutil.WriteTree(t, dir, files)

	return dir
}

func explain(t *testing.T, dir string, args ...string) result {
	t.Helper()

	return runIn(t, dir, append([]string{"explain"}, args...)...)
}

func wantContains(t *testing.T, text string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

// wantUsageFailure reports an error unless the run exited with code 2 and its stderr has CO-WRK-010 and each text.
func wantUsageFailure(t *testing.T, label string, res *result, texts ...string) {
	t.Helper()

	if res.Code != 2 {
		t.Errorf("%s: exit code = %d, stderr = %q", label, res.Code, res.Stderr)

		return
	}

	for _, text := range append([]string{"CO-WRK-010"}, texts...) {
		if !strings.Contains(res.Stderr, text) {
			t.Errorf("%s: exit code = %d, stderr = %q", label, res.Code, res.Stderr)

			return
		}
	}
}

// decodeStdoutJSON decodes the JSON that a run wrote to stdout into doc.
func decodeStdoutJSON(t *testing.T, stdout string, doc any) {
	t.Helper()

	if err := json.Unmarshal([]byte(stdout), doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
}

// explainJSONFinding is a finding of `explain --format json`.
type explainJSONFinding struct {
	SourceContext any    `json:"sourceContext"`
	Fingerprint   string `json:"fingerprint"`
}

// isPlain says whether the finding has the fingerprint and no source context.
func (f *explainJSONFinding) isPlain(fingerprint string) bool {
	return f.Fingerprint == fingerprint && f.SourceContext == nil
}

// isUnappliedADR3 says whether the decision is ADR-3 and its first candidate successor is not applied.
func isUnappliedADR3(d *conformance.FindingDecision) bool {
	return d.ID == "ADR-3" && len(d.CandidateSuccessors) > 0 && !d.CandidateSuccessors[0].Applied
}

func TestExplainExplainsARule(t *testing.T) {
	t.Parallel()

	res := explain(t, explainDir(t, nil), "CO-BND-001", "--results", "results.json")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, res.Stdout,
		"CO-BND-001 Billing uses Fulfillment contracts only",
		"Verdict: violation   Severity: error   Family: boundary   Policy: commerce-boundaries@1.0.0",
		"Rationale: Billing must depend on the published fulfillment contract.",
		"Remediation: Import from packages/fulfillment-contract.",
		"Subjects: calm:node:billing-service",
		"Targets: calm:node:fulfillment-service",
		`"family": "boundary"`,
		"files: 98.7% (99/100 covered, required 95.0%)",
		"services/legacy/Legacy.kt",
		"Fingerprint: "+testutil.FPActive,
		"Fingerprint: "+testutil.FPBaselined,
		"-[IMPORTS syntactic, cbm:IMPORTS]-> at services/billing/src/InvoiceService.ts:18",
		"Disposition: active",
		"Disposition: baselined (baseline)",
	)
}

func TestExplainExplainsTheReasonsOfAnUnknownRule(t *testing.T) {
	t.Parallel()

	res := explain(t, explainDir(t, nil), "CO-COV-001", "--results", "results.json")
	wantContains(t, res.Stdout, "Verdict: unknown", "CO-CON-021: minimum coverage not met: 0.500 < 0.9", "No findings.")
}

func TestExplainFindingSelectsOneFindingByFullFingerprintOrPrefix(t *testing.T) {
	t.Parallel()

	dir := explainDir(t, nil)

	res := explain(t, dir, "CO-LYR-001", "--results", "results.json", "--finding", testutil.FPWaived[7:15])
	wantContains(t, res.Stdout,
		"Fingerprint: "+testutil.FPWaived,
		"Disposition: waived (WV-1: Migration tracked in ARCH-932; expires 2027-12-31; owner team-billing)",
	)

	full := explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPBaselined)
	wantContains(t, full.Stdout, "Fingerprint: "+testutil.FPBaselined)

	if strings.Contains(full.Stdout, "Fingerprint: "+testutil.FPActive) {
		t.Errorf("the other finding is shown:\n%s", full.Stdout)
	}
}

func TestExplainUnknownRuleOrFingerprintGivesWRK010(t *testing.T) {
	t.Parallel()

	dir := explainDir(t, nil)

	res := explain(t, dir, "NOPE-1", "--results", "results.json")
	wantUsageFailure(t, "unknown rule", &res, "rules in the results: ", "CO-BND-001")

	for name, finding := range map[string]string{"unknown fingerprint": "sha256:ffffffff", "finding of another rule": testutil.FPWaived} {
		res = explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", finding)
		wantUsageFailure(t, name, &res)
	}
}

func TestExplainRejectsAFileThatIsNotAResultOrIsMissing(t *testing.T) {
	t.Parallel()

	dir := explainDir(t, map[string]string{"bad.json": "{}", "text.json": "not json"})

	for _, file := range []string{"bad.json", "text.json", "missing.json"} {
		res := explain(t, dir, "R", "--results", file)
		wantUsageFailure(t, file, &res)
	}
}

func TestExplainFormatJSONWritesTheRuleTheFindingsAndTheirWaivers(t *testing.T) {
	t.Parallel()

	res := explain(t, explainDir(t, nil), "CO-LYR-001", "--results", "results.json", "--format", "json")

	var doc struct {
		SchemaVersion string `json:"schemaVersion"`
		Rule          struct {
			RuleID string `json:"ruleId"`
		} `json:"rule"`
		Waivers []struct {
			ID string `json:"id"`
		} `json:"waivers"`
		Findings []explainJSONFinding `json:"findings"`
	}

	decodeStdoutJSON(t, res.Stdout, &doc)

	if doc.SchemaVersion != "1.1" || doc.Rule.RuleID != "CO-LYR-001" {
		t.Errorf("doc = %+v", doc)
	}

	if len(doc.Findings) != 1 || !doc.Findings[0].isPlain(testutil.FPWaived) {
		t.Errorf("findings = %+v", doc.Findings)
	}

	if len(doc.Waivers) != 1 || doc.Waivers[0].ID != "WV-1" {
		t.Errorf("waivers = %+v", doc.Waivers)
	}
}

func TestExplainUnsupportedFormatGivesWRK010(t *testing.T) {
	t.Parallel()

	res := explain(t, explainDir(t, nil), "CO-BND-001", "--results", "results.json", "--format", "sarif")
	wantUsageFailure(t, "unsupported format", &res)
}

func numberedLines() string {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line " + string(rune('0'+(i+1)/10)) + string(rune('0'+(i+1)%10))
	}

	return strings.Join(lines, "\n")
}

func TestExplainSourceReadsTwoLinesAroundEachLocation(t *testing.T) {
	t.Parallel()

	dir := explainDir(t, map[string]string{
		"services/billing/src/InvoiceService.ts": numberedLines(),
		"services/billing/src/Refund.ts":         "a\nb\nc\nd\n",
	})

	res := explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive, "--source")
	wantContains(t, res.Stdout,
		"    services/billing/src/InvoiceService.ts:18",
		"      "+"  16 | line 16", "      "+"  17 | line 17", "    >   18 | line 18", "      "+"  19 | line 19", "      "+"  20 | line 20",
		"    services/billing/src/Refund.ts:3",
		"     1 | a",
		"    >    3 | c",
	)

	for _, not := range []string{"line 15", "line 21"} {
		if strings.Contains(res.Stdout, not) {
			t.Errorf("output has %q", not)
		}
	}
}

func TestExplainSourceStaysInTheSourceRootAndToleratesAMissingFile(t *testing.T) {
	t.Parallel()

	var doc map[string]any

	text, _ := canonjson.Marshal(testutil.SampleRun())
	if err := json.Unmarshal(text, &doc); err != nil {
		t.Fatal(err)
	}

	findings, _ := doc["findings"].([]any)
	for _, f := range findings {
		finding, _ := f.(map[string]any)
		if finding["fingerprint"] == testutil.FPActive {
			finding["location"] = map[string]any{"path": "../outside.ts", "line": 3}
			finding["relatedLocations"] = []any{}
		}
	}

	escaped, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": string(escaped)})

	res := explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive, "--source")
	wantContains(t, res.Stdout, "(not available in the working tree)")
	wantContains(t, res.Stderr, "CO-REP-006", "../outside.ts")
}

func TestExplainSourceRefusesASymbolicLinkOutOfTheRoot(t *testing.T) {
	t.Parallel()

	dir := explainDir(t, nil)

	outside := filepath.Join(t.TempDir(), "outside.ts")
	if err := os.WriteFile(outside, []byte("l1\nl2\nOUTSIDE-CONTENT\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nl16\nl17\nl18\nl19\nl20\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "services", "billing", "src", "InvoiceService.ts")
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}

	res := explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive, "--source")
	if strings.Contains(res.Stdout, "l17") || strings.Contains(res.Stdout, "OUTSIDE") {
		t.Errorf("the output has content of a file outside the root:\n%s", res.Stdout)
	}

	wantContains(t, res.Stderr, "CO-REP-006", "services/billing/src/InvoiceService.ts")
}

func TestExplainShowsControlSequencesFromContentInAVisibleForm(t *testing.T) {
	t.Parallel()

	dir := explainDir(t, map[string]string{
		"services/billing/src/InvoiceService.ts": strings.Replace(numberedLines(), "line 17", "line 17\x1b[31m red", 1),
	})

	res := explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive, "--source")
	if strings.ContainsRune(res.Stdout, 0x1b) {
		t.Errorf("the output has an escape character:\n%q", res.Stdout)
	}

	wantContains(t, res.Stdout, `line 17\x1b[31m red`)

	// A message from the results file.
	text, err := canonjson.Marshal(testutil.SampleRun())
	if err != nil {
		t.Fatal(err)
	}

	evil := strings.ReplaceAll(string(text), "forbidden dependency billing-service", "forbidden\\u001b[2J dependency billing-service")
	if evil == string(text) {
		t.Fatal("the message is not in the sample results")
	}

	dir = t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": evil})

	res = explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive)
	if strings.ContainsRune(res.Stdout, 0x1b) {
		t.Errorf("the output has an escape character:\n%q", res.Stdout)
	}

	wantContains(t, res.Stdout, `forbidden\x1b[2J dependency`)
}

func TestExplainSourceFormatJSONAddsSourceContext(t *testing.T) {
	t.Parallel()

	dir := explainDir(t, map[string]string{"services/billing/src/InvoiceService.ts": numberedLines()})

	res := explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive, "--source", "--format", "json")

	var doc struct {
		Findings []struct {
			SourceContext []struct {
				Location struct {
					Path string `json:"path"`
					Line int    `json:"line"`
				} `json:"location"`
				Lines []struct {
					Line int `json:"line"`
				} `json:"lines"`
			} `json:"sourceContext"`
		} `json:"findings"`
	}

	decodeStdoutJSON(t, res.Stdout, &doc)

	ctxs := doc.Findings[0].SourceContext
	if len(ctxs) == 0 {
		t.Fatalf("sourceContext = %+v", ctxs)
	}

	if ctxs[0].Location.Path != "services/billing/src/InvoiceService.ts" || ctxs[0].Location.Line != 18 {
		t.Fatalf("sourceContext = %+v", ctxs)
	}

	got := make([]int, 0, len(ctxs[0].Lines))

	for _, l := range ctxs[0].Lines {
		got = append(got, l.Line)
	}

	if !slices.Equal(got, []int{16, 17, 18, 19, 20}) {
		t.Errorf("lines = %v, want 16..20", got)
	}
}

// The pipeline cases.

func TestExplainReadsTheWorkspaceResultsFileAndRunsNoQuery(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)
	s.run(t, "check", "--no-color")

	before := len(s.graph.Queries)

	res := s.run(t, "explain", "CO-BND-001")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if len(s.graph.Queries) != before {
		t.Errorf("explain ran %d queries", len(s.graph.Queries)-before)
	}

	wantContains(t, res.Stdout, "CO-BND-001 Billing uses fulfillment contracts only")
}

func TestExplainRunsTheCheckWithoutWritingReportsWhenThereIsNoResultsFile(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	res := s.run(t, "explain", "CO-BND-001")
	wantContains(t, res.Stdout, "Verdict: violation")

	if _, err := os.Stat(filepath.Join(s.dir, "build")); err == nil {
		t.Errorf("explain wrote the build directory")
	}
}

func TestExplainDoesNotChangeTheDispositionOfAFinding(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)
	s.run(t, "check", "--format", "json", "--output", "results.json")

	res := s.run(t, "explain", "CO-BND-001", "--results", "results.json", "--format", "json")

	var doc struct {
		Findings []struct {
			Disposition string `json:"disposition"`
		} `json:"findings"`
	}

	decodeStdoutJSON(t, res.Stdout, &doc)

	if len(doc.Findings) == 0 || doc.Findings[0].Disposition != "active" {
		t.Errorf("findings = %+v", doc.Findings)
	}
}

func TestExplainPathAllListsRelatedEvidence(t *testing.T) {
	t.Parallel()

	dir := explainDir(t, nil)

	all := explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive, "--path", "all")
	wantContains(t, all.Stdout, "  Related evidence:", "    services/billing/src/Refund.ts:3")

	if strings.Contains(all.Stdout, "Related locations:") {
		t.Errorf("--path all prints Related locations:\n%s", all.Stdout)
	}

	if n := strings.Count(all.Stdout, "services/billing/src/Refund.ts:3"); n != 1 {
		t.Errorf("related location printed %d times:\n%s", n, all.Stdout)
	}

	shortest := explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive)
	wantContains(t, shortest.Stdout, "  Related locations:")

	if strings.Contains(shortest.Stdout, "Related evidence:") {
		t.Errorf("default output has Related evidence:\n%s", shortest.Stdout)
	}

	if bad := explain(t, dir, "CO-BND-001", "--results", "results.json", "--path", "x"); bad.Code != 2 {
		t.Errorf("bad --path: exit code = %d", bad.Code)
	}
}

// A05: two containers share one image repository at different tags; the merged fact is
// marked possibly lossy and the profile limit is shown.
func TestExplainShowsIdentitiesAndTheLossyMarkOfAMergedFact(t *testing.T) {
	t.Parallel()

	profile, ok := observation.ProfileFor("0.12.1", "concordia-cbm/v2")
	if !ok {
		t.Fatal("no profile for 0.12.1")
	}

	merged := &observation.EvidenceEdge{
		ID: "e1", From: "cbm:demo:deploy", To: "cbm:demo:image", Kind: observation.KindUsesImage, NativeKind: "USES_IMAGE",
		Confidence: observation.ConfidenceResolved, ResolutionMethod: "cbm:USES_IMAGE", Environment: "production", Occurrence: "sidecar",
		Properties: map[string]string{"container": "sidecar", "image": "registry.example.com/shop/api:1.1"},
	}
	if observation.MarkPossiblyLossy([]*observation.EvidenceEdge{merged}, profile) != 1 {
		t.Fatal("the merged fact is not marked")
	}

	id := observation.IdentityOf(merged)
	deploy := observation.ObservedNode{
		ObservedID: "cbm:demo:deploy", Label: "Resource", QualifiedName: "k8s.api", FilePath: "k8s/prod/deployment.yaml", StartLine: 4,
		Environment: "production", ScopedID: "k8s:production/~/apps/Deployment/shop/api",
	}
	image := observation.ObservedNode{ObservedID: "cbm:demo:image", Label: "Image", QualifiedName: "registry.example.com/shop/api"}

	run := testutil.SampleRun()
	run.Toolchain.Cbm.Profile = conformance.NewCbmProfile(profile.Ref())

	for i := range run.Findings {
		if run.Findings[i].Fingerprint == testutil.FPActive {
			run.Findings[i].EvidencePath = []conformance.EvidenceStep{
				{Node: &deploy},
				{Edge: &conformance.StepEdge{
					Kind: merged.Kind, NativeKind: merged.NativeKind, Confidence: merged.Confidence, ResolutionMethod: merged.ResolutionMethod,
					From: merged.From, To: merged.To, Identity: &id,
				}},
				{Node: &image},
			}
		}
	}

	text, err := canonjson.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": string(text)})

	res := explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive)
	wantContains(t, res.Stdout,
		"    Resource k8s.api (k8s/prod/deployment.yaml:4) [k8s:production/~/apps/Deployment/shop/api]\n",
		"    -[USES_IMAGE resolved, cbm:USES_IMAGE]->\n",
		"      edge key: uses-image|cbm:demo:deploy|cbm:demo:image|production|||sidecar\n",
		"      environment: production\n",
		"      occurrence: sidecar\n",
		"      image: registry.example.com/shop/api:1.1\n",
		"      possibly lossy: the provider may have merged this fact with others; profile loss mode merged-occurrences (cbm-0.12.1)\n",
	)
}

// infraFixtureRoot is the repository with the seeded secrets, relative to this package.
const infraFixtureRoot = "../../test/fixtures/infra"

// infraSeed is one entry of test/fixtures/infra-seeds.json.
type infraSeed struct {
	Path  string `json:"path"`
	Class string `json:"class"`
	Value string `json:"value"`
}

func loadInfraSeeds(t *testing.T) []infraSeed {
	t.Helper()

	data, err := os.ReadFile("../../test/fixtures/infra-seeds.json")
	if err != nil {
		t.Fatal(err)
	}

	var seeds []infraSeed
	if err := json.Unmarshal(data, &seeds); err != nil {
		t.Fatal(err)
	}

	return seeds
}

func infraFixture(t *testing.T, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(infraFixtureRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// lineOf gives the 1-based number of the first line of text that holds needle.
func lineOf(t *testing.T, text, needle string) int {
	t.Helper()

	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}

	t.Fatalf("no line holds %q", needle)

	return 0
}

// explainSourceAt runs `explain --source` for the active finding moved to path:line of a tree that
// holds the files.
func explainSourceAt(t *testing.T, path string, line int, files map[string]string) result {
	t.Helper()

	text, err := canonjson.Marshal(testutil.SampleRun())
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(text, &doc); err != nil {
		t.Fatal(err)
	}

	findings, _ := doc["findings"].([]any)
	for _, f := range findings {
		finding, _ := f.(map[string]any)
		if finding["fingerprint"] == testutil.FPActive {
			finding["location"] = map[string]any{"path": path, "line": line}
			finding["relatedLocations"] = []any{}
		}
	}

	moved, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	tree := map[string]string{"results.json": string(moved)}
	maps.Copy(tree, files)

	dir := t.TempDir()
	testutil.WriteTree(t, dir, tree)

	return explain(t, dir, "CO-BND-001", "--results", "results.json", "--finding", testutil.FPActive, "--source")
}

func TestExplainSourceRedactsAKubernetesSecretManifest(t *testing.T) {
	t.Parallel()

	content := infraFixture(t, "k8s/prod/secret.yaml")
	res := explainSourceAt(t, "k8s/prod/secret.yaml", lineOf(t, content, "CONCORDIA-SEED-k8s-secret-7f3a"), map[string]string{"k8s/prod/secret.yaml": content})

	wantContains(t, res.Stdout, "password: [REDACTED:secret-data]", "stringData:")
	wantContains(t, res.Stderr, "CO-REP-005", "value(s) removed from explain")

	for _, seed := range loadInfraSeeds(t) {
		if strings.Contains(res.Stdout, seed.Value) || strings.Contains(res.Stderr, seed.Value) {
			t.Errorf("the output has the seed %q of %s", seed.Value, seed.Path)
		}
	}
}

func TestExplainSourceRedactsADockerfileEnv(t *testing.T) {
	t.Parallel()

	content := infraFixture(t, "docker/Dockerfile")
	res := explainSourceAt(t, "docker/Dockerfile", lineOf(t, content, "CONCORDIA-SEED-dockerfile-env-2b8c"), map[string]string{"docker/Dockerfile": content})

	wantContains(t, res.Stdout, "ENV API_KEY=[REDACTED:env-value]")
	wantContains(t, res.Stderr, "CO-REP-005", "redaction applied: 1 value(s) removed from explain")

	if strings.Contains(res.Stdout, "CONCORDIA-SEED-dockerfile-env-2b8c") {
		t.Errorf("the output has the seed:\n%s", res.Stdout)
	}
}

func TestExplainSourceRedactsATfvarsValue(t *testing.T) {
	t.Parallel()

	content := infraFixture(t, "terraform/prod.tfvars")
	res := explainSourceAt(t, "terraform/prod.tfvars", lineOf(t, content, "CONCORDIA-SEED-tfvars-6e4d"), map[string]string{"terraform/prod.tfvars": content})

	wantContains(t, res.Stdout, "db_password = [REDACTED:secret-data]")
	wantContains(t, res.Stderr, "CO-REP-005")

	if strings.Contains(res.Stdout, "CONCORDIA-SEED-tfvars-6e4d") {
		t.Errorf("the output has the seed:\n%s", res.Stdout)
	}
}

func TestExplainSourceRedactsAShellToken(t *testing.T) {
	t.Parallel()

	content := infraFixture(t, "scripts/deploy.sh")

	res := explainSourceAt(t, "scripts/deploy.sh", lineOf(t, content, "ghp_CONCORDIASEEDshelltoken0000000000000"), map[string]string{"scripts/deploy.sh": content})
	if strings.Contains(res.Stdout, "ghp_CONCORDIASEED") {
		t.Errorf("the output has the seed:\n%s", res.Stdout)
	}

	wantContains(t, res.Stdout, "[REDACTED:")
}

// infraRun is the sample run with the rule CO-BND-001 as INFRA-PLC-001: its findings are about orders-service in
// production, and the deployment trace has that subject and environment.
func infraRun(t *testing.T, status deployment.Status) string {
	t.Helper()

	run := testutil.SampleRun()
	retargetRuleToInfra(run)

	subject := traceSubject("production", deployment.Location{Path: "k8s/prod/deployment.yaml", Line: 2})
	subject.Status = status
	run.DeploymentTrace = deployment.Trace{
		Status:       deployment.StatusConfigured,
		Levels:       deployment.Levels(),
		Environments: []deployment.EnvironmentTrace{{Name: "production", Cluster: "prod-eu", Subjects: []deployment.SubjectTrace{subject}}},
	}

	text, err := canonjson.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

// retargetRuleToInfra makes the rule CO-BND-001 of the run INFRA-PLC-001, with infrastructure findings.
func retargetRuleToInfra(run *conformance.Run) {
	for i := range run.Rules {
		if run.Rules[i].RuleID == "CO-BND-001" {
			run.Rules[i].RuleID = "INFRA-PLC-001"
		}
	}

	for i := range run.Findings {
		retargetFindingToInfra(&run.Findings[i])
	}
}

// retargetFindingToInfra moves a finding of CO-BND-001 to orders-service in production.
func retargetFindingToInfra(f *conformance.Finding) {
	if f.RuleID != "CO-BND-001" {
		return
	}

	f.RuleID, f.Subject, f.Environment, f.Occurrence = "INFRA-PLC-001", "orders-service", "production", "app"
	f.EvidencePath = infraEvidencePath()
}

const infraScopedID = "k8s:production/prod-eu/apps/Deployment/shop/orders"

// infraEvidencePath is a path of a workload with a scoped identity and a fact with an edge key.
func infraEvidencePath() []conformance.EvidenceStep {
	id := observation.EdgeIdentity{Key: "uses-image|cbm:demo:deploy|cbm:demo:image|production||image|app"}

	return []conformance.EvidenceStep{
		{Node: &observation.ObservedNode{ObservedID: "cbm:demo:deploy", Label: "Resource", Environment: "production", ScopedID: infraScopedID}},
		{Edge: &conformance.StepEdge{Kind: observation.KindUsesImage, From: "cbm:demo:deploy", To: "cbm:demo:image", Identity: &id}},
		{Node: &observation.ObservedNode{ObservedID: "cbm:demo:image", Label: "Image"}},
	}
}

func TestExplainAnInfrastructureFindingPrintsTheScopedIdentityNotTheEdgeKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": infraRun(t, deployment.StatusPartial)})

	res := explain(t, dir, "INFRA-PLC-001", "--results", "results.json")
	wantContains(t, res.Stdout, "  Scoped identity: "+infraScopedID)

	if strings.Contains(res.Stdout, "Scoped identity: uses-image|") {
		t.Errorf("the scoped identity line has the edge key:\n%s", res.Stdout)
	}
}

func TestExplainAnInfrastructureFindingPrintsTheTraceExcerpt(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]deployment.Status{"A10 partial chain": deployment.StatusPartial, "A11 no workload": deployment.StatusUnresolved} {
		dir := t.TempDir()
		testutil.WriteTree(t, dir, map[string]string{"results.json": infraRun(t, status)})

		res := explain(t, dir, "INFRA-PLC-001", "--results", "results.json")
		if res.Code != 0 {
			t.Fatalf("%s: exit code = %d, stderr = %q", name, res.Code, res.Stderr)
		}

		wantContains(t, res.Stdout,
			"  Environment: production",
			"  Occurrence: app",
			"  Trace excerpt:",
			"    Deployment trace: orders-service in production (cluster prod-eu): "+string(status),
			"  at k8s/prod/deployment.yaml:2",
		)
	}
}

func TestExplainACoreFindingHasNoTraceExcerpt(t *testing.T) {
	t.Parallel()

	res := explain(t, explainDir(t, nil), "CO-BND-001", "--results", "results.json")
	if strings.Contains(res.Stdout, "Trace excerpt") {
		t.Errorf("a CO- rule prints a trace excerpt:\n%s", res.Stdout)
	}
}

// adrResults is the A12 run (ADR-0003 superseded by ADR-0007, rule CORE-DB-001 unjustified) as results.json text.
func adrResults(t *testing.T, mod func(*conformance.Run)) string {
	t.Helper()

	run := testutil.ADRRun(true)
	if mod != nil {
		mod(run)
	}

	text, err := canonjson.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

func TestExplainAGovernanceFindingPrintsTheDecisionFacts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": adrResults(t, nil)})

	res := explain(t, dir, "GOV-ADR-002", "--results", "results.json", "--finding", testutil.FPADRSuperseded)
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, res.Stdout,
		"GOV-ADR-002 A registered decision is active for the architecture it governs",
		"Fingerprint: "+testutil.FPADRSuperseded,
		"  Source: ADR-3",
		"  Location: "+testutil.ADRPath+":3",
		"  Decision: ADR-3 Use PostgreSQL for orders",
		"    Status: accepted (status text: Accepted)",
		"    Path: "+testutil.ADRPath+":3",
		"  Presence: committed",
		"  Applicability: superseded (superseded-by ADR-7) (the status still says active)",
		"  Enforcement: enforced (required)",
		"  History: 1 link(s)",
		"    ADR-7 supersedes ADR-3 (provenance: both; effect: effective)",
		"      "+testutil.ADRSuccessor+" -> "+testutil.ADRPath,
		"  Governed subjects: 1",
		"    calm:node:orders-db",
		"  Linked rules: 1",
		"    CORE-DB-001@core/1.0.0",
		"  Successors: ADR-7",
		"  Predecessors: (none)",
		"  Candidate successors: 1",
		"    ADR-7 "+testutil.ADRSuccessor+" (applied: false;",
	)

	if strings.Contains(res.Stdout, "calm:node:ADR-3") {
		t.Errorf("a decision ID is shown as a CALM node:\n%s", res.Stdout)
	}
}

func TestExplainAGovernanceFindingByFingerprintPrefix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": adrResults(t, nil)})

	res := explain(t, dir, "GOV-ADR-002", "--results", "results.json", "--finding", testutil.FPADRSuperseded[:15])
	wantContains(t, res.Stdout, "  Decision: ADR-3", "Finding "+reporting.ShortFingerprint(testutil.FPADRSuperseded))
}

func TestExplainAGovernanceFindingWithoutADecisionBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": adrResults(t, nil)})

	res := explain(t, dir, "GOV-ADR-003", "--results", "results.json", "--finding", testutil.FPADRRuleUnjustified)
	wantContains(t, res.Stdout, "  Source: CORE-DB-001", "  Target: ADR-3", "  Location: "+testutil.ADRRegisterPath+":12")

	if strings.Contains(res.Stdout, "Decision:") || strings.Contains(res.Stdout, "calm:node:") {
		t.Errorf("unexpected decision block or CALM reference:\n%s", res.Stdout)
	}
}

const adrDocument = `---
status: accepted
---
# ADR-0003 Use PostgreSQL for orders
We use PostgreSQL. $(curl https://example.invalid/never-fetched)
`

func TestExplainSourceShowsTwoLinesOfTheADRFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": adrResults(t, nil), testutil.ADRPath: adrDocument})

	res := explain(t, dir, "GOV-ADR-002", "--results", "results.json", "--finding", testutil.FPADRSuperseded, "--source")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantContains(t, res.Stdout,
		"    "+testutil.ADRPath+":3",
		"1 | ---",
		"2 | status: accepted",
		">    3 | ---",
		"4 | # ADR-0003 Use PostgreSQL for orders",
		"5 | We use PostgreSQL. $(curl https://example.invalid/never-fetched)",
	)
}

func TestExplainSourceRedactsASecretInTheADRFile(t *testing.T) {
	t.Parallel()

	const token = "ghp_CONCORDIASEEDadrtoken000000000000000"

	doc := strings.Replace(adrDocument, "We use PostgreSQL.", "export GITHUB_TOKEN="+token, 1)
	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": adrResults(t, nil), testutil.ADRPath: doc})

	res := explain(t, dir, "GOV-ADR-002", "--results", "results.json", "--finding", testutil.FPADRSuperseded, "--source")
	if strings.Contains(res.Stdout, token) || strings.Contains(res.Stderr, token) {
		t.Errorf("the output has the token:\n%s\n%s", res.Stdout, res.Stderr)
	}

	wantContains(t, res.Stdout, "[REDACTED:")
	wantContains(t, res.Stderr, "CO-REP-005")
}

func TestExplainRedactsASecretInTheDecisionTitle(t *testing.T) {
	t.Parallel()

	const token = "ghp_CONCORDIASEEDtitletoken0000000000000"

	results := adrResults(t, func(run *conformance.Run) {
		for i := range run.Findings {
			if run.Findings[i].Decision != nil {
				run.Findings[i].Decision.Title = "Use token " + token + " for access"
			}
		}
	})

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": results})

	for _, format := range []string{"pretty", "json"} {
		res := runIn(t, dir, "explain", "GOV-ADR-002", "--results", "results.json", "--finding", testutil.FPADRSuperseded, "--format", format)
		if res.Code != 0 {
			t.Fatalf("%s: exit code = %d, stderr = %q", format, res.Code, res.Stderr)
		}

		if strings.Contains(res.Stdout, token) || strings.Contains(res.Stderr, token) {
			t.Errorf("%s: the output has the token:\n%s\n%s", format, res.Stdout, res.Stderr)
		}

		wantContains(t, res.Stdout, "[REDACTED:")
		wantContains(t, res.Stderr, "CO-REP-005", "value(s) removed from explain")
	}
}

func TestExplainFormatJSONKeepsTheDecisionBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"results.json": adrResults(t, nil)})

	res := runIn(t, dir, "explain", "GOV-ADR-002", "--results", "results.json", "--finding", testutil.FPADRSuperseded, "--format", "json")

	var doc struct {
		Findings []struct {
			Decision conformance.FindingDecision `json:"decision"`
		} `json:"findings"`
	}

	decodeStdoutJSON(t, res.Stdout, &doc)

	if len(doc.Findings) != 1 || !isUnappliedADR3(&doc.Findings[0].Decision) {
		t.Errorf("decision = %+v", doc.Findings)
	}
}
