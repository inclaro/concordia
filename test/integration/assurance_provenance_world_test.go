package integration_test

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
)

// The provenance world is the A10 infra workspace (the production `api` workload with the containers `app` and
// `sidecar`, and the Dockerfile that a build command links to the image repository) extended with an `assurance`
// block (A34): the production manifests rendered from `assurance-render/production`, signed provenance statements of
// the shop fixtures, and signed deployment records and workload observations. The cosign that verifies the statements
// is a fake that accepts the files whose bytes it was given, as a signature over the payload does.

const (
	provFixtureDir = "../fixtures/assurance/provenance"

	provRepository = "registry.example.com/payments/api"
	provSidecarTag = provRepository + ":1.3.0"

	provHex  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	provHexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	provDigestA = "sha256:" + provHex
	provDigestB = "sha256:" + provHexB

	// The shop fixtures: provenance bundles that were signed (and one changed after signing).
	provShop      = "shop.provenance.sigstore.json"
	provModified  = "modified.provenance.sigstore.json"
	provWrong     = "wrong-subject.provenance.sigstore.json"
	provEvil      = "untrusted-builder.provenance.sigstore.json"
	provForeign   = "foreign-source.provenance.sigstore.json"
	provMalformed = "malformed.provenance.json"

	provAttestations = "build/attestations/"
	provRecords      = "build/records/"
)

var provNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// provRecord is one deployment record or workload observation of the world.
type provRecord struct {
	// path is the file name below build/records/. `deploy-*` is read as a record, `observed-*` as an observation.
	path        string
	environment string
	container   string
	digest      string
	outcome     string
	age         time.Duration
}

// provWorld is the extension of the A10 workspace of one test.
type provWorld struct {
	// appImage is the image of the container `app` in the rendered production manifest. Empty: the digest aaa.
	appImage string
	// sidecarImage is the image of the container `sidecar`. Empty: a tag with no digest (A33).
	sidecarImage string
	// attestations are the fixture file names that are copied below build/attestations/.
	attestations []string
	records      []provRecord
	// unsigned lists the fixture files that the fake cosign must refuse (a changed bundle).
	unsigned []string
	// noPack leaves the `assurance` pack out: the same run without the ASR rules.
	noPack bool
	// noRendering leaves the rendering source out: the world has statements and no rendered occurrence.
	noRendering bool
}

func (w *provWorld) image() string {
	if w.appImage != "" {
		return w.appImage
	}

	return provRepository + "@" + provDigestA
}

const provRenderedDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: payments
spec:
  selector:
    matchLabels:
      app: api
  template:
    metadata:
      labels:
        app: api
    spec:
      containers:
        - name: app
          image: %APP%
        - name: sidecar
          image: %SIDECAR%
`

func provBundle(t *testing.T, predicateType string, predicate map[string]any, subjectHex string) []byte {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "predicateType": predicateType, "predicate": predicate,
		"subject": []map[string]any{{"name": "subject", "digest": map[string]string{"sha256": subjectHex}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(map[string]any{
		"mediaType":            "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{"publicKey": map[string]string{"hint": "aGludA=="}},
		"dsseEnvelope": map[string]any{
			"payloadType": statement.PayloadType,
			"payload":     base64.StdEncoding.EncodeToString(payload),
			"signatures":  []map[string]string{{"sig": "c2ln"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// bundle gives the signed statement of a record.
func (r *provRecord) bundle(t *testing.T) []byte {
	t.Helper()

	environment := r.environment
	if environment == "" {
		environment = "production"
	}

	container := r.container
	if container == "" {
		container = "app"
	}

	at := provNow.Add(-r.age).Format(time.RFC3339)
	predicate := map[string]any{
		"environment": environment, "cluster": "prod-eu", "container": container, "digest": r.digest, "time": at,
		"workload": map[string]string{"namespace": "payments", "kind": "Deployment", "name": "api"},
	}

	if strings.HasPrefix(r.path, "observed-") {
		return provBundle(t, statement.PredicateWorkloadObservation, predicate, strings.TrimPrefix(r.digest, "sha256:"))
	}

	predicate["outcome"], predicate["deployer"] = r.outcome, "ci"

	return provBundle(t, statement.PredicateDeploymentRecord, predicate, strings.TrimPrefix(r.digest, "sha256:"))
}

// provFakeCosign writes a cosign that accepts a bundle exactly when its bytes are among the signed ones.
func provFakeCosign(t *testing.T, signed [][]byte) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	sums := make([]string, 0, len(signed))

	for _, data := range signed {
		sum := sha256.Sum256(data)
		sums = append(sums, hex.EncodeToString(sum[:]))
	}

	script := `#!/bin/sh
if [ "$1" = version ]; then echo 'GitVersion:    v3.0.2'; exit 0; fi
bundle=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--bundle" ]; then bundle="$2"; fi
  shift
done
sum=$(sha256sum "$bundle" | cut -d' ' -f1)
case " ` + strings.Join(sums, " ") + ` " in
  *" $sum "*) echo "Verified OK" >&2; exit 0 ;;
esac
echo "Error: verifying blob attestation: no matching signatures" >&2
exit 1
`

	path := filepath.Join(t.TempDir(), "cosign")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return path
}

const provAssuranceHead = `assurance:
  producers:
    - id: ci
      claims: [build-provenance, deployment-record, workload-observation]
      authentication: {method: cosign-keyless, issuer: "https://issuer.example", identity: ci@example}
  provenance:
    statements: ["build/attestations/*.json"]
    builders: ["https://github.com/acme/shop/.github/workflows/build.yml"]
    sourceRepositories: [shop]
`

func (w *provWorld) assuranceBlock() string {
	text := provAssuranceHead

	if !w.noRendering {
		text += "  rendering:\n    sources:\n      - {environment: production, tool: plain, path: assurance-render/production}\n"
	}

	var deployments, observations bool

	for i := range w.records {
		if strings.HasPrefix(w.records[i].path, "observed-") {
			observations = true
		} else {
			deployments = true
		}
	}

	if deployments || observations {
		text += "  records:\n    maxAgeDays: 7\n"

		if deployments {
			text += "    deployments: [\"build/records/deploy-*.json\"]\n"
		}

		if observations {
			text += "    observations: [\"build/records/observed-*.json\"]\n"
		}
	}

	return text
}

// provSpec gives the deployment spec, with the cosign that signs what the world lists, and the fixed clock.
func (w *provWorld) spec(t *testing.T) *deploymentSpec {
	t.Helper()

	files := map[string]string{}

	var signed [][]byte

	for _, name := range w.attestations {
		data, err := os.ReadFile(filepath.Join(provFixtureDir, name))
		if err != nil {
			t.Fatal(err)
		}

		files[provAttestations+name] = string(data)

		if !slices.Contains(w.unsigned, name) && name != provModified && name != provMalformed {
			signed = append(signed, data)
		}
	}

	for i := range w.records {
		data := w.records[i].bundle(t)
		files[provRecords+w.records[i].path] = string(data)
		signed = append(signed, data)
	}

	if !w.noRendering {
		rendered := strings.Replace(provRenderedDeployment, "%APP%", w.image(), 1)
		files["assurance-render/production/deployment.yaml"] = strings.Replace(rendered, "%SIDECAR%", cmp.Or(w.sidecarImage, provSidecarTag), 1)
	}

	head := deploymentWorkspaceHead
	if !w.noPack {
		head = strings.Replace(head, "    - use: builtin:infrastructure\n", "    - use: builtin:infrastructure\n    - use: builtin:assurance\n", 1)
	}

	files["concordia.yaml"] = head + deploymentCheck + deploymentEnvironments + w.assuranceBlock()

	exe := provFakeCosign(t, signed)

	return &deploymentSpec{
		files: files,
		edit: func(opts *conformance.CheckOptions) {
			opts.Deps.Now = func() time.Time { return provNow }
			opts.Deps.Getenv = func(key string) string {
				if key == "COSIGN" {
					return exe
				}

				return ""
			}
		},
	}
}

// run checks the world.
func (w *provWorld) run(t *testing.T) *deployment {
	t.Helper()

	return deploymentRun(t, w.spec(t))
}

// provFindingsOf gives the findings of a rule.
func provFindingsOf(run *conformance.Run, rule string) []conformance.Finding {
	var out []conformance.Finding

	for i := range run.Findings {
		if run.Findings[i].RuleID == rule {
			out = append(out, run.Findings[i])
		}
	}

	return out
}

// provLineageOf gives the lineage entry of the container in production.
func provLineageOf(t *testing.T, run *conformance.Run, container string) *conformance.LineageEntry {
	t.Helper()

	if run.Assurance == nil {
		t.Fatal("results have no assurance object")
	}

	for i := range run.Assurance.Lineage {
		entry := &run.Assurance.Lineage[i]
		if entry.Environment == "production" && entry.Occurrence.Container == container {
			return entry
		}
	}

	t.Fatalf("no lineage entry for %s: %+v", container, run.Assurance.Lineage)

	return nil
}

// provRuleOf gives the result of a rule.
func provRuleOf(t *testing.T, run *conformance.Run, rule string) *conformance.RuleResult {
	t.Helper()

	for i := range run.Rules {
		if run.Rules[i].RuleID == rule {
			return &run.Rules[i]
		}
	}

	t.Fatalf("no result for rule %s", rule)

	return nil
}

// provGolden compares the canonical JSON of v with test/golden/assurance/<name>. CONCORDIA_UPDATE_GOLDEN=1 rewrites it.
func provGolden(t *testing.T, name string, v any) {
	t.Helper()

	got, err := canonjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	file := filepath.Join("..", "golden", "assurance", name)

	if os.Getenv(updateGoldenEnv) != "" {
		if err := os.MkdirAll(filepath.Dir(file), goldenDirMode); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(file, got, goldenFileMode); err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with %s=1 to create it)", name, err, updateGoldenEnv)
	}

	if !bytes.Equal(want, got) {
		t.Errorf("golden %s differs (run with %s=1 to update it)\n--- want\n%s\n--- got\n%s", name, updateGoldenEnv, want, got)
	}
}
