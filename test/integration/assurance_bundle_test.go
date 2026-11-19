package integration_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/reporting"
)

// The results, the run manifest, and the evaluation bundle of a run with assurance evidence (design D16, D17, D18):
// the `assurance` object and the toolchain, the files of the bundle, the redaction of Secret values, and the
// statements that the bundle holds.

const (
	secretValue    = "hunter2-very-secret-value"
	secretManifest = `apiVersion: v1
kind: Secret
metadata:
  name: db-credentials
  namespace: payments
type: Opaque
stringData:
  password: ` + secretValue + `
`
	// planSecret is the key material that the plan marks sensitive (testdata of the plan fixture).
	planSecret = "acme-secret-kms-key-material"
)

// secretWorld renders production with a Secret and reads a production plan that holds a sensitive value.
func secretWorld(t *testing.T) *deploymentSpec {
	t.Helper()

	plan, err := os.ReadFile("../fixtures/assurance/plans/handwritten-s3-update.json")
	if err != nil {
		t.Fatal(err)
	}

	spec := (&provWorld{}).spec(t)
	spec.files["assurance-render/production/secret.yaml"] = secretManifest
	spec.files["build/plans/production.json"] = string(plan)
	spec.files["concordia.yaml"] += "  plans:\n    - {environment: production, tool: terraform, file: build/plans/production.json}\n"
	spec.extraBindings = planBucketBinding

	return spec
}

// assuranceParts are the parts of results.json that this change adds.
type assuranceParts struct {
	Toolchain     *conformance.AssuranceToolchain `json:"toolchain"`
	Assurance     *conformance.AssuranceReport    `json:"assurance"`
	SchemaVersion string                          `json:"schemaVersion"`
}

func TestAssuranceResultsGolden(t *testing.T) {
	t.Parallel()

	run := deploymentRun(t, secretWorld(t)).run

	if run.SchemaVersion != "1.1" {
		t.Fatalf("schemaVersion = %s, want 1.1", run.SchemaVersion)
	}

	parts := assuranceParts{SchemaVersion: run.SchemaVersion, Toolchain: run.Toolchain.Assurance, Assurance: run.Assurance}
	// The sections of the other owners have their own goldens: this one is the rendered, plan, and statement parts and
	// the toolchain.
	parts.Assurance = &conformance.AssuranceReport{
		Rendered: run.Assurance.Rendered, Plans: run.Assurance.Plans, Statements: run.Assurance.Statements,
	}

	provGolden(t, "results-assurance.json", parts)
}

func TestTwoRunsGiveEqualDigests(t *testing.T) {
	t.Parallel()

	first := deploymentRun(t, secretWorld(t)).run
	second := deploymentRun(t, secretWorld(t)).run

	if first.Summary.ResultDigest != second.Summary.ResultDigest {
		t.Errorf("result digests differ: %s, %s", first.Summary.ResultDigest, second.Summary.ResultDigest)
	}

	a, b := first.Assurance.Rendered[0], second.Assurance.Rendered[0]
	if !haveEqualRenderedDigests(&a, &b) {
		t.Errorf("rendered digests differ: %+v, %+v", a, b)
	}

	requireEqualAssuranceBundleFiles(t, first, second)
}

func haveEqualRenderedDigests(a, b *conformance.RenderedEntry) bool {
	return a.InputDigest == b.InputDigest && a.OutputDigest == b.OutputDigest && a.InputDigest != ""
}

func requireEqualAssuranceBundleFiles(t *testing.T, first, second *conformance.Run) {
	t.Helper()

	filesA, _, err := conformance.AssuranceBundleFiles(first)
	if err != nil {
		t.Fatal(err)
	}

	filesB, _, err := conformance.AssuranceBundleFiles(second)
	if err != nil {
		t.Fatal(err)
	}

	if len(filesA) == 0 || len(filesA) != len(filesB) {
		t.Fatalf("bundle files = %d, %d", len(filesA), len(filesB))
	}

	for i := range filesA {
		if filesA[i].Path != filesB[i].Path || !bytes.Equal(filesA[i].Data, filesB[i].Data) {
			t.Errorf("assurance file %s differs between two runs", filesA[i].Path)
		}
	}
}

// exportedBundle runs `export bundle` and opens the archive. It gives the extract directory and the manifest.
func exportedBundle(t *testing.T, d *deployment) (dir string, manifest *bundle.Manifest, archive string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(d.w.dir, "build", "concordia"), 0o750); err != nil {
		t.Fatal(err)
	}

	if res := d.cli(t, "export", "bundle"); res.code != 0 {
		t.Fatalf("export bundle: exit code %d, stderr %q", res.code, res.stderr)
	}

	archive = filepath.Join(d.w.dir, "build", "concordia", "conformance.tar.gz")
	dir = t.TempDir()

	opened, err := bundle.Open(archive, dir)
	if err != nil {
		t.Fatalf("open the bundle: %v", err)
	}

	return dir, opened, archive
}

// assuranceRenderResource is one entry of the resource inventory of a render file.
type assuranceRenderResource struct {
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	DataKeys []string `json:"dataKeys"`
}

// assuranceRenderSource is one rendered source of a render file.
type assuranceRenderSource struct {
	Tool         string                    `json:"tool"`
	OutputDigest string                    `json:"outputDigest"`
	Coverage     string                    `json:"coverage"`
	Resources    []assuranceRenderResource `json:"resources"`
}

// assuranceRenderFile is the render file of the bundle: the renderer identity, digests, and coverage, and the
// resource inventory without values.
type assuranceRenderFile struct {
	Sources []assuranceRenderSource `json:"sources"`
}

func (r assuranceRenderFile) isOneCompleteSource() bool {
	if len(r.Sources) != 1 {
		return false
	}

	return r.Sources[0].Coverage == "complete" && r.Sources[0].OutputDigest != ""
}

func isSecretCredentialsResource(r assuranceRenderResource) bool {
	return r.Kind == "Secret" && r.Name == "db-credentials" && slices.Equal(r.DataKeys, []string{"password"})
}

func TestBundleHoldsTheAssuranceEvidenceAndRepeats(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, secretWorld(t))
	dir, manifest, archive := exportedBundle(t, d)

	requireBundleHoldsAssuranceFiles(t, manifest)
	requireRenderFileHoldsSecretInventory(t, dir)
	requireManifestToolchainRecordsContracts(t, manifest)

	if verify := d.cli(t, "export", "verify", archive); verify.code != 0 {
		t.Errorf("export verify exit code %d, stderr %q", verify.code, verify.stderr)
	}

	// A second export of the same workspace has the same file digests.
	_, again, _ := exportedBundle(t, d)
	for path, digest := range manifest.Integrity.Files {
		if strings.HasPrefix(path, "assurance/") && again.Integrity.Files[path] != digest {
			t.Errorf("%s differs between two exports: %s, %s", path, digest, again.Integrity.Files[path])
		}
	}
}

func requireBundleHoldsAssuranceFiles(t *testing.T, manifest *bundle.Manifest) {
	t.Helper()

	paths := make([]string, 0, len(manifest.Files))

	for _, f := range manifest.Files {
		paths = append(paths, f.Path)
	}

	for _, want := range []string{"assurance/render/production.json", "assurance/plans/production.json", "assurance/contracts.json"} {
		if !slices.Contains(paths, want) {
			t.Errorf("bundle has no %s: %v", want, paths)
		}
	}
}

func requireRenderFileHoldsSecretInventory(t *testing.T, dir string) {
	t.Helper()

	var render assuranceRenderFile

	if err := json.Unmarshal(mustRead(t, filepath.Join(dir, "assurance", "render", "production.json")), &render); err != nil {
		t.Fatal(err)
	}

	if !render.isOneCompleteSource() {
		t.Fatalf("render file = %+v", render)
	}

	if !slices.ContainsFunc(render.Sources[0].Resources, isSecretCredentialsResource) {
		t.Errorf("the Secret name and key names are not in the render file: %+v", render.Sources[0].Resources)
	}
}

// requireManifestToolchainRecordsContracts checks that the toolchain of the manifest records the contracts.
func requireManifestToolchainRecordsContracts(t *testing.T, manifest *bundle.Manifest) {
	t.Helper()

	var toolchain struct {
		Assurance *conformance.AssuranceToolchain `json:"assurance"`
	}

	err := json.Unmarshal(manifest.Toolchain, &toolchain)
	if err == nil && hasClaimsRegistryDigest(toolchain.Assurance) {
		return
	}

	t.Errorf("manifest toolchain = %s (%v)", manifest.Toolchain, err)
}

func hasClaimsRegistryDigest(tc *conformance.AssuranceToolchain) bool {
	return tc != nil && tc.ClaimsRegistryDigest != ""
}

// secretScanTexts gathers every text that must hold no secret value: the results, the SARIF report, the files of
// the bundle, and the files under .concordia/runs. The key says where the text comes from.
func secretScanTexts(t *testing.T, d *deployment) map[string][]byte {
	t.Helper()

	results, err := conformance.CanonicalJSON(d.run)
	if err != nil {
		t.Fatal(err)
	}

	sarif, err := reporting.Render(reporting.FormatSARIF, d.run, reporting.PrettyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// The run manifest is written by a run that writes reports, as `check` does.
	d.cli(t, "check")

	dir, _, _ := exportedBundle(t, d)

	texts := map[string][]byte{"results.json": results, "results.sarif": sarif}

	err = filepath.WalkDir(dir, func(p string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		rel, _ := filepath.Rel(dir, p)
		texts["bundle/"+filepath.ToSlash(rel)] = mustRead(t, p)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = filepath.WalkDir(filepath.Join(d.w.dir, ".concordia", "runs"), func(p string, entry os.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			rel, _ := filepath.Rel(d.w.dir, p)
			texts[filepath.ToSlash(rel)] = mustRead(t, p)
		}

		return nil
	})

	return texts
}

func isRunManifestName(name string) bool {
	return strings.HasSuffix(name, "manifest.json") && strings.Contains(name, "runs/")
}

// requireNoSecretInTexts fails for each text that holds a secret value and for a scan without any run manifest.
func requireNoSecretInTexts(t *testing.T, texts map[string][]byte) {
	t.Helper()

	manifests := 0

	for name, text := range texts {
		for _, secret := range []string{secretValue, planSecret} {
			if strings.Contains(string(text), secret) {
				t.Errorf("%s holds the secret %q", name, secret)
			}
		}

		if isRunManifestName(name) {
			manifests++
		}
	}

	if manifests == 0 {
		t.Error("no run manifest was checked")
	}
}

// secretRedaction is a redaction record as shown in the golden: the property and the class, never the value.
type secretRedaction struct {
	Class    string `json:"class"`
	Output   string `json:"output"`
	Property string `json:"property"`
}

// secretRedactionsOf lists what the bundle removed, by class and place, and never the value.
func secretRedactionsOf(t *testing.T, run *conformance.Run) []secretRedaction {
	t.Helper()

	records := run.AssuranceEvidence
	_, recs, err := conformance.AssuranceBundleFiles(run)

	if err != nil || records == nil {
		t.Fatalf("redactions: %v", err)
	}

	out := make([]secretRedaction, 0, len(recs))

	for i := range recs {
		r := &recs[i]
		out = append(out, secretRedaction{Class: r.Class, Output: r.Output, Property: r.Property})
	}

	return out
}

// requireRunManifestsRecordPasswordRedaction checks that each run manifest records the redaction by property and class.
func requireRunManifestsRecordPasswordRedaction(t *testing.T, texts map[string][]byte) {
	t.Helper()

	type runRedaction struct {
		Class    string `json:"class"`
		Property string `json:"property"`
	}

	for name, text := range texts {
		if !isRunManifestName(name) {
			continue
		}

		var manifest struct {
			Redactions []runRedaction `json:"assuranceRedactions"`
		}

		if err := json.Unmarshal(text, &manifest); err != nil {
			t.Fatal(err)
		}

		if !slices.ContainsFunc(manifest.Redactions, func(r runRedaction) bool {
			return r.Class == "secret-data" && r.Property == "password"
		}) {
			t.Errorf("%s has no redaction record for the password", name)
		}
	}
}

func TestNoSecretValueInResultsManifestOrBundle(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, secretWorld(t))
	texts := secretScanTexts(t, d)

	requireNoSecretInTexts(t, texts)

	// A redaction record exists for the field: the property and the class, never the value.
	out := secretRedactionsOf(t, d.run)
	if !slices.ContainsFunc(out, func(s secretRedaction) bool { return s.Property == "password" && s.Class == "secret-data" }) {
		t.Errorf("no redaction record for the Secret password: %+v", out)
	}

	provGolden(t, "secret-redactions.json", out)

	requireRunManifestsRecordPasswordRedaction(t, texts)
}

// assuranceDerivedStatement is the part of a statement summary that its persisted statement determines.
type assuranceDerivedStatement struct {
	Kind    string
	Digest  string
	Subject []string
}

// persistedStatement is a statement file of the bundle.
type persistedStatement struct {
	Digest        string `json:"digest"`
	PredicateType string `json:"predicateType"`
	Subject       []struct {
		Digest map[string]string `json:"digest"`
	} `json:"subject"`
}

func (p *persistedStatement) subjects() []string {
	var subjects []string

	for _, s := range p.Subject {
		for algorithm, value := range s.Digest {
			subjects = append(subjects, algorithm+":"+value)
		}
	}

	slices.Sort(subjects)

	return subjects
}

// derivedStatementsOfBundle reads each persisted statement of the bundle again.
func derivedStatementsOfBundle(t *testing.T, files []conformance.AssuranceFile) []assuranceDerivedStatement {
	t.Helper()

	var derived []assuranceDerivedStatement

	for _, f := range files {
		if !strings.HasPrefix(f.Path, "assurance/statements/") {
			continue
		}

		var persisted persistedStatement
		if err := json.Unmarshal(f.Data, &persisted); err != nil {
			t.Fatal(err)
		}

		if f.Path != "assurance/statements/"+strings.TrimPrefix(persisted.Digest, "sha256:")+".json" {
			t.Errorf("%s does not carry the digest %s", f.Path, persisted.Digest)
		}

		derived = append(derived, assuranceDerivedStatement{Kind: persisted.PredicateType, Digest: persisted.Digest, Subject: persisted.subjects()})
	}

	return derived
}

// derivedStatementsOfRun lists the distinct statements of the run.
func derivedStatementsOfRun(run *conformance.Run) []assuranceDerivedStatement {
	var derived []assuranceDerivedStatement

	seen := map[string]bool{}

	for i := range run.Assurance.Statements {
		s := &run.Assurance.Statements[i]

		if seen[s.Digest] {
			continue
		}

		seen[s.Digest] = true

		subjects := slices.Clone(s.SubjectDigests)
		slices.Sort(subjects)

		derived = append(derived, assuranceDerivedStatement{Digest: s.Digest, Subject: subjects})
	}

	return derived
}

func compareDerivedStatementDigests(a, b assuranceDerivedStatement) int {
	return strings.Compare(a.Digest, b.Digest)
}

func requireStatementsOfBundleMatchRun(t *testing.T, run *conformance.Run) {
	t.Helper()

	files, _, err := conformance.AssuranceBundleFiles(run)
	if err != nil {
		t.Fatal(err)
	}

	if len(run.Assurance.Statements) < 2 {
		t.Fatalf("statements = %+v", run.Assurance.Statements)
	}

	// Read each persisted statement of the bundle again and derive the part of the summary that it determines.
	fromBundle := derivedStatementsOfBundle(t, files)
	fromRun := derivedStatementsOfRun(run)

	slices.SortFunc(fromBundle, compareDerivedStatementDigests)
	slices.SortFunc(fromRun, compareDerivedStatementDigests)

	if len(fromBundle) != len(fromRun) {
		t.Fatalf("the bundle holds %d statements, the run lists %d", len(fromBundle), len(fromRun))
	}

	for i := range fromRun {
		if fromBundle[i].Digest != fromRun[i].Digest || !slices.Equal(fromBundle[i].Subject, fromRun[i].Subject) {
			t.Errorf("statement %d: bundle %+v, run %+v", i, fromBundle[i], fromRun[i])
		}
	}
}

// Task 4.7: the bundle holds the statements as persisted, and a second evaluation from the bundle gives the same
// statement results as the run.
func TestStatementsOfTheBundleGiveTheSameResultsAsTheRun(t *testing.T) {
	t.Parallel()

	world := &provWorld{attestations: []string{provShop}, records: []provRecord{{path: "deploy-production.json", environment: "production", container: "app", digest: provDigestA, outcome: "succeeded"}}}
	run := world.run(t).run

	requireStatementsOfBundleMatchRun(t, run)

	// The statement results (kind, integrity, admissibility) are those of the second evaluation of the same inputs.
	again := world.run(t).run

	a, err := canonjson.Marshal(run.Assurance.Statements)
	if err != nil {
		t.Fatal(err)
	}

	b, err := canonjson.Marshal(again.Assurance.Statements)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(a, b) {
		t.Errorf("statement results differ between two evaluations:\n%s\n%s", a, b)
	}
}

// sarifRenderedResult is the part of a SARIF result that locates it.
type sarifRenderedResult struct {
	RuleID    string `json:"ruleId"`
	Locations []struct {
		PhysicalLocation struct {
			ArtifactLocation struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

// sarifRenderedDoc is the part of a SARIF document that locates the results.
type sarifRenderedDoc struct {
	Runs []struct {
		Results []sarifRenderedResult `json:"results"`
	} `json:"runs"`
}

// sarifLocated is the rule of an ASR finding and the repository file it is located at.
type sarifLocated struct {
	Rule string `json:"rule"`
	URI  string `json:"uri"`
}

// isNonRepositoryPath is true for an absolute path, a path in the workspace or in a temporary directory, and a
// path that leaves its directory.
func isNonRepositoryPath(uri, workspaceDir string) bool {
	if strings.HasPrefix(uri, "/") {
		return true
	}

	return slices.ContainsFunc([]string{workspaceDir, "/tmp/", ".."}, func(part string) bool { return strings.Contains(uri, part) })
}

// sarifASRLocations lists the locations of the ASR findings and fails for a location that is not a repository path.
func sarifASRLocations(t *testing.T, doc *sarifRenderedDoc, workspaceDir string) []sarifLocated {
	t.Helper()

	out := make([]sarifLocated, 0, len(doc.Runs[0].Results))

	for i := range doc.Runs[0].Results {
		out = append(out, sarifResultLocations(t, &doc.Runs[0].Results[i], workspaceDir)...)
	}

	return out
}

// sarifResultLocations lists the locations of one result if it is an ASR finding.
func sarifResultLocations(t *testing.T, result *sarifRenderedResult, workspaceDir string) []sarifLocated {
	t.Helper()

	if !strings.HasPrefix(result.RuleID, "ASR-") {
		return nil
	}

	var out []sarifLocated

	for _, loc := range result.Locations {
		uri := loc.PhysicalLocation.ArtifactLocation.URI
		if isNonRepositoryPath(uri, workspaceDir) {
			t.Errorf("%s is located at %q: not a repository path", result.RuleID, uri)
		}

		out = append(out, sarifLocated{Rule: result.RuleID, URI: uri})
	}

	return out
}

// Task 2.7: a finding about rendered evidence is located at a repository file: the manifest that the finding is about,
// else the declared source path of the environment. No location is a temporary path.
func TestSARIFLocationsOfRenderedFindingsAreRepositoryPaths(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, secretWorld(t))

	sarif, err := reporting.Render(reporting.FormatSARIF, d.run, reporting.PrettyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var doc sarifRenderedDoc
	if err := json.Unmarshal(sarif, &doc); err != nil {
		t.Fatal(err)
	}

	out := sarifASRLocations(t, &doc, d.w.dir)
	if len(out) == 0 {
		t.Fatal("no ASR finding with a location in the SARIF")
	}

	if !slices.ContainsFunc(out, func(l sarifLocated) bool { return strings.HasPrefix(l.URI, "assurance-render/production") }) {
		t.Errorf("no finding is located in the declared source: %+v", out)
	}

	slices.SortFunc(out, func(a, b sarifLocated) int { return strings.Compare(a.Rule+a.URI, b.Rule+b.URI) })
	provGolden(t, "sarif-rendered-locations.json", out)
}
