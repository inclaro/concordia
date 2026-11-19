package e2e_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/procexec"
)

// The assurance e2e world: the A10 infra fixture (the production `api` workload with the containers `app` and
// `sidecar`) extended with an `assurance` block, driven through the real binary and the real CBM. The statements are
// signed bundles; the cosign that verifies them is a fake (the contract of test/integration/assurance_provenance_world_test.go)
// that accepts exactly the bundles whose bytes it was given and writes one line to a log for each start, so that a test
// can show that cosign was never started.

const (
	asrNow        = "2026-10-31T12:00:00Z"
	asrProject    = "concordia-e2e-assurance"
	asrRepository = "registry.example.com/payments/api"
	asrSidecarTag = asrRepository + ":1.3.0"
	asrHexA       = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	asrHexB       = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	asrDigestA    = "sha256:" + asrHexA
	asrDigestB    = "sha256:" + asrHexB

	asrProvenanceFixtures = "../fixtures/assurance/provenance/"
	asrShop               = "shop.provenance.sigstore.json"
	asrModified           = "modified.provenance.sigstore.json"
	asrMalformed          = "malformed.provenance.json"
	asrEvil               = "untrusted-builder.provenance.sigstore.json"

	asrSecretValue = "hunter2-very-secret-value"
	asrBuilder     = "https://github.com/acme/shop/.github/workflows/build.yml"
)

var asrNowTime = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

const asrWorkspaceHead = `apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: assurance
architecture:
  source: architecture/infra.calm.json
observation:
  provider: codebase-memory-mcp
  projects:
    - name: %PROJECT%
      root: .
      include: ["**"]
  indexMode: fast
bindings:
  files: [bindings/concordia.bindings.yaml]
policies:
  files: [policies/**/*.policy.yaml]
  packs:
    - use: builtin:assurance
check:
  minimumCoverage: 0
  unknown: fail
  failOn: error
%ENFORCEMENT%environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/prod/**"]
`

const asrArchitecture = `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {
      "unique-id": "api-service", "node-type": "service", "name": "API service", "description": "The payments API.",
      "concordia": {"tags": ["deployable"]},
      "controls": {
        "verification-001": {
          "description": "The unit tests pass",
          "requirements": [{
            "requirement-url": "https://calm.finos.org/release/1.2/meta/control.json",
            "config": {"engine": "concordia", "check-ids": ["unit-tests"]}
          }]
        }
      }
    },
    {"unique-id": "shop-web", "node-type": "service", "name": "Shop web", "description": "The web tier."},
    {"unique-id": "shop-api", "node-type": "service", "name": "Shop API", "description": "The shop API."},
    {"unique-id": "shop-db", "node-type": "service", "name": "Shop database", "description": "The shop database."}
  ],
  "relationships": []
}
`

const asrBindings = `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-api-workload
    architectureRef: calm:node:api-service
    required: false
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: api, namespace: payments}
  - id: bind-api-source
    architectureRef: calm:node:api-service
    required: false
    match:
      any:
        - sourceRoot: app/**
  - id: bind-web
    architectureRef: calm:node:shop-web
    required: false
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: web, namespace: shop}
  - id: bind-shop-api
    architectureRef: calm:node:shop-api
    required: false
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: api, namespace: shop}
  - id: bind-db
    architectureRef: calm:node:shop-db
    required: false
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: db, namespace: shop}
`

// asrPaymentsDeployment is the rendered deployment of the payments api: the container `app` is pinned by digest and
// the container `sidecar` has a tag and no digest (A33).
const asrPaymentsDeployment = `apiVersion: apps/v1
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

// asrShopManifests are the workloads and policies of the A35 constellation in the namespace `shop`. `db` is selected
// by a deny-all policy (`podSelector: {}`) and allowed from `tier In [web, edge]` (a `matchExpressions` selector):
// neither leaves an edge in CBM 0.12.1.
const asrShopManifests = `apiVersion: v1
kind: Namespace
metadata:
  name: shop
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  template:
    metadata:
      labels: {app: web, tier: web}
    spec:
      containers:
        - name: app
          image: registry.example.com/shop/web:1.0.0
          ports: [{containerPort: 8080}]
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: shop
spec:
  template:
    metadata:
      labels: {app: api, tier: backend}
    spec:
      containers:
        - name: app
          image: registry.example.com/shop/api:1.0.0
          ports: [{containerPort: 8080}]
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: db
  namespace: shop
spec:
  template:
    metadata:
      labels: {app: db, tier: data}
    spec:
      containers:
        - name: app
          image: registry.example.com/shop/db:1.0.0
          ports: [{containerPort: 22}, {containerPort: 5432}, {containerPort: 8080}]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny
  namespace: shop
spec:
  podSelector: {}
  policyTypes: [Ingress]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: db-allow-web
  namespace: shop
spec:
  podSelector:
    matchLabels: {app: db}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchExpressions:
              - {key: tier, operator: In, values: [web, edge]}
      ports:
        - {protocol: TCP, port: 8080}
`

const asrSecretManifest = `apiVersion: v1
kind: Secret
metadata:
  name: db-credentials
  namespace: payments
type: Opaque
stringData:
  password: ` + asrSecretValue + `
`

const asrPolicyHead = `apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: %ID%
  version: 1.0.0
rules:
`

// asrEffectiveRule is a `require.effectiveAccess` network rule from a CALM node to the database.
func asrEffectiveRule(id, from, expect string, ports ...int) string {
	text := "  - id: " + id + "\n    name: " + id + "\n    severity: error\n    require:\n      effectiveAccess:\n        dimension: network" +
		"\n        from: {calmNodes: [" + from + "]}\n        to: {calmNodes: [shop-db]}\n        expect: " + expect + "\n"

	if len(ports) > 0 {
		list := make([]string, len(ports))
		for i, port := range ports {
			list[i] = strconv.Itoa(port)
		}

		text += "        ports: [" + strings.Join(list, ", ") + "]\n"
	}

	return text
}

// asrBundle makes a signed sigstore bundle of one in-toto statement (the signature is not checked by anything but the
// fake cosign, which knows the bytes).
func asrBundle(t *testing.T, predicateType string, predicate map[string]any, subjectHex string) []byte {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"_type": "https://in-toto.io/Statement/v1", "predicateType": predicateType, "predicate": predicate,
		"subject": []map[string]any{{"name": "subject", "digest": map[string]string{"sha256": subjectHex}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(map[string]any{
		"mediaType":            "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{"publicKey": map[string]string{"hint": "aGludA=="}},
		"dsseEnvelope": map[string]any{
			"payloadType": "application/vnd.in-toto+json",
			"payload":     base64.StdEncoding.EncodeToString(payload),
			"signatures":  []map[string]string{{"sig": "c2ln"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// asrCosign is the fake cosign of a world.
type asrCosign struct {
	// path is the executable and log the file with one line for each start that verifies a bundle.
	path, log string
}

// asrFakeCosign writes a cosign that accepts a bundle exactly when its bytes are among the signed ones.
func asrFakeCosign(t *testing.T, signed [][]byte) asrCosign {
	t.Helper()

	sums := make([]string, 0, len(signed))

	for _, data := range signed {
		sum := sha256.Sum256(data)
		sums = append(sums, hex.EncodeToString(sum[:]))
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "cosign.log")

	script := `#!/bin/sh
if [ "$1" = version ]; then echo 'GitVersion:    v3.0.2'; exit 0; fi
echo "verify $*" >> '` + logPath + `'
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

	path := filepath.Join(dir, "cosign")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return asrCosign{path: path, log: logPath}
}

// starts is the number of times that the fake cosign verified a bundle.
func (c asrCosign) starts(t *testing.T) int {
	t.Helper()

	data, err := os.ReadFile(c.log)
	if os.IsNotExist(err) {
		return 0
	}

	if err != nil {
		t.Fatal(err)
	}

	return strings.Count(string(data), "\n")
}

// commands is the log of the arguments of every start that verified a bundle.
func (c asrCosign) commands(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(c.log)
	if os.IsNotExist(err) {
		return ""
	}

	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// asrRecord is one deployment record or workload observation.
type asrRecord struct {
	path        string // below build/records/: `deploy-*` is a record, `observed-*` an observation
	environment string
	digest      string
	outcome     string
	container   string
}

func (r *asrRecord) bundle(t *testing.T) []byte {
	t.Helper()

	predicate := map[string]any{
		"environment": cmpOrString(r.environment, "production"), "cluster": "prod-eu", "container": cmpOrString(r.container, "app"),
		"digest": r.digest, "time": asrNowTime.Add(-time.Hour).Format(time.RFC3339),
		"workload": map[string]string{"namespace": "payments", "kind": "Deployment", "name": "api"},
	}
	subject := strings.TrimPrefix(r.digest, "sha256:")

	if strings.HasPrefix(r.path, "observed-") {
		return asrBundle(t, "https://concordia.io/evidence/workload-observation/v1", predicate, subject)
	}

	predicate["outcome"], predicate["deployer"] = cmpOrString(r.outcome, "succeeded"), "ci"

	return asrBundle(t, "https://concordia.io/evidence/deployment-record/v1", predicate, subject)
}

func cmpOrString(value, fallback string) string {
	if value != "" {
		return value
	}

	return fallback
}

// asrWorld is one scenario: the files that the world adds to the infra fixture, and the evidence that it signs.
type asrWorld struct {
	t              *testing.T
	extraFiles     map[string]string
	scenarios      string
	image          string
	sidecar        string
	rendering      string
	producerClaims string
	extra          string
	evaluators     string
	enforcement    string
	requiredAreas  string
	verification   string
	records        []asrRecord
	measurements   []asrMeasurement
	results        []asrCheckResult
	rules          []string
	unsigned       []string
	attestations   []string
	withSecret     bool
	withShop       bool
	noAssurance    bool
}

// asrCheckResult is a check result statement of the world.
type asrCheckResult struct {
	file        string
	check       string
	outcome     string
	revision    string
	environment string
	untrusted   bool
}

// asrMeasurement is a measurement statement of the world.
type asrMeasurement struct {
	file        string
	scenario    string
	environment string
	outcome     string
	revision    string
	value       float64
	untrusted   bool
}

// asrSlots limits the runs that use CBM at the same time: the tests run side by side, and a full machine makes one
// index slower than the timeout of a command.
var asrSlots = make(chan struct{}, 8)

// asrExclusive runs fn while it holds one of the CBM slots.
func asrExclusive(fn func()) {
	asrSlots <- struct{}{}

	defer func() { <-asrSlots }()

	fn()
}

// asrRepo is the repository of a world with the environment of the CLI.
type asrRepo struct {
	*repo
	world  *asrWorld
	cosign asrCosign
	env    []string
}

// build copies the infra fixture, commits the workspace, signs the evidence below build/ (ignored by git, so that the
// evaluated revision is the commit that holds no evidence), and makes the first index.
func (w *asrWorld) build() *asrRepo {
	t := w.t
	t.Helper()

	files := map[string]string{
		"concordia.yaml":                   w.workspace(),
		"architecture/infra.calm.json":     asrArchitecture,
		"bindings/concordia.bindings.yaml": asrBindings,
	}

	if len(w.rules) > 0 {
		files["policies/world.policy.yaml"] = strings.Replace(asrPolicyHead, "%ID%", "world", 1) + strings.Join(w.rules, "")
	}

	app := cmpOrString(w.image, asrRepository+"@"+asrDigestA)
	rendered := strings.Replace(asrPaymentsDeployment, "%APP%", app, 1)
	files["assurance-render/production/payments.yaml"] = strings.Replace(rendered, "%SIDECAR%", cmpOrString(w.sidecar, asrSidecarTag), 1)

	if w.withShop {
		files["assurance-render/production/shop.yaml"] = asrShopManifests
	}

	if w.withSecret {
		files["assurance-render/production/secret.yaml"] = asrSecretManifest
	}

	if w.scenarios != "" {
		files["quality/scenarios.yaml"] = w.scenarios
	}

	for rel, text := range w.extraFiles {
		files[rel] = text
	}

	r := newDeploymentRepo(t, w.project(), "assurance", files)
	out := &asrRepo{repo: r, world: w}
	out.sign()
	out.env = []string{"CONCORDIA_NOW=" + asrNow, "COSIGN=" + out.cosign.path}

	asrExclusive(func() {
		if res := out.cli(nil, "index"); res.code != 0 {
			t.Errorf("index: exit %d: %s", res.code, res.output())
		}
	})

	return out
}

// project is the CBM project of the test: one for each test, so that tests can run side by side.
func (w *asrWorld) project() string {
	return asrProject + "-" + strings.ToLower(strings.NewReplacer("/", "-", "_", "-", " ", "-").Replace(w.t.Name()))
}

// workspace is the workspace file.
func (w *asrWorld) workspace() string {
	claims := cmpOrString(w.producerClaims, "build-provenance, deployment-record, workload-observation, check-result, measurement")

	text := strings.NewReplacer("%PROJECT%", w.project(), "%ENFORCEMENT%", w.enforcement).Replace(asrWorkspaceHead)
	if w.noAssurance {
		return text + w.extra
	}

	text += "assurance:\n  producers:\n    - id: ci\n      claims: [" + claims + "]\n" +
		"      authentication: {method: cosign-keyless, issuer: \"https://issuer.example\", identity: ci@example}\n"
	text += "  rendering:\n    sources:\n" + cmpOrString(w.rendering, "      - {environment: production, tool: plain, path: assurance-render/production}\n")
	text += "  provenance:\n    statements: [\"build/attestations/*.json\"]\n    builders: [\"" + asrBuilder + "\"]\n    sourceRepositories: [shop]\n"
	text += "  records:\n    maxAgeDays: 7\n    deployments: [\"build/records/deploy-*.json\"]\n    observations: [\"build/records/observed-*.json\"]\n"
	text += "  verification:\n    results: [\"build/results/*.json\"]\n    maxAgeDays: 14\n" + w.verification

	if w.scenarios != "" {
		text += "  quality:\n    scenarios: [\"quality/*.yaml\"]\n    measurements: [\"build/measurements/*.json\"]\n" + w.requiredAreas
	}

	if w.evaluators != "" {
		text += "  evaluators:\n" + w.evaluators
	}

	return text + w.extra
}

// sign writes the evidence files below build/ and the fake cosign that accepts them.
func (r *asrRepo) sign() {
	revision := r.head()

	signed := r.signAttestations()
	signed = append(signed, r.signRecords()...)
	signed = append(signed, r.signResults(revision)...)
	signed = append(signed, r.signMeasurements(revision)...)

	r.cosign = asrFakeCosign(r.t, signed)
}

// signAttestations writes the provenance statements and returns the ones that the fake cosign accepts.
func (r *asrRepo) signAttestations() [][]byte {
	var signed [][]byte

	for _, name := range r.world.attestations {
		data, err := os.ReadFile(asrProvenanceFixtures + name)
		if err != nil {
			r.t.Fatal(err)
		}

		r.write("build/attestations/"+name, string(data))

		if asrAttestationIsSigned(r.world.unsigned, name) {
			signed = append(signed, data)
		}
	}

	return signed
}

// asrAttestationIsSigned tells whether the fake cosign accepts the named statement.
func asrAttestationIsSigned(unsigned []string, name string) bool {
	if slices.Contains(unsigned, name) {
		return false
	}

	return name != asrModified && name != asrMalformed
}

// signRecords writes the deployment and observation records and returns their bundles.
func (r *asrRepo) signRecords() [][]byte {
	signed := make([][]byte, 0, len(r.world.records))

	for i := range r.world.records {
		data := r.world.records[i].bundle(r.t)
		r.write("build/records/"+r.world.records[i].path, string(data))

		signed = append(signed, data)
	}

	return signed
}

// signResults writes the check results and returns the bundles that the fake cosign accepts.
func (r *asrRepo) signResults(revision string) [][]byte {
	var signed [][]byte

	for _, result := range r.world.results {
		predicate := map[string]any{
			"check": cmpOrString(result.check, "unit-tests"), "outcome": cmpOrString(result.outcome, "passed"),
			"revision": cmpOrString(result.revision, revision), "environment": cmpOrString(result.environment, "production"),
			"evaluator": map[string]string{"name": "go-test", "version": "1.0.0"},
			"time":      asrNowTime.Add(-time.Hour).Format(time.RFC3339), "covers": []string{"api-service"},
		}

		data := asrBundle(r.t, "https://concordia.io/evidence/check-result/v1", predicate, strings.Repeat("c", 64))
		r.write("build/results/"+result.file, string(data))

		if !result.untrusted {
			signed = append(signed, data)
		}
	}

	return signed
}

// signMeasurements writes the quality measurements and returns the bundles that the fake cosign accepts.
func (r *asrRepo) signMeasurements(revision string) [][]byte {
	var signed [][]byte

	for _, m := range r.world.measurements {
		predicate := map[string]any{
			"scenario": m.scenario, "metric": "failover-seconds", "value": m.value, "unit": "s", "threshold": 30, "comparison": "lte",
			"testConditions": map[string]any{"load": "steady", "replicas": 2},
			"revision":       cmpOrString(m.revision, revision), "environment": cmpOrString(m.environment, "production"),
			"evaluator": map[string]string{"name": "chaos-runner", "version": "2.1.0"}, "outcome": cmpOrString(m.outcome, "passed"),
			"window": map[string]string{
				"start": asrNowTime.Add(-3 * time.Hour).Format(time.RFC3339), "end": asrNowTime.Add(-2 * time.Hour).Format(time.RFC3339),
			},
		}

		data := asrBundle(r.t, "https://concordia.io/evidence/measurement/v1", predicate, strings.Repeat("d", 64))
		r.write("build/measurements/"+m.file, string(data))

		if !m.untrusted {
			signed = append(signed, data)
		}
	}

	return signed
}

// head is the commit that the check evaluates.
func (r *asrRepo) head() string {
	r.t.Helper()

	res, err := runGit(r.t, r.dir, "rev-parse", "HEAD")
	if err != nil {
		r.t.Fatal(err)
	}

	return strings.TrimSpace(res)
}

// asrTimeout is the time of one command of an assurance test: many checks run side by side on one CBM.
const asrTimeout = 20 * time.Minute

// cli runs the binary in the repository with the longer timeout of these tests. extraEnv is a list of "KEY=value" entries.
func (r *asrRepo) cli(extraEnv []string, args ...string) cliResult {
	r.t.Helper()

	env := append(os.Environ(), "NO_COLOR=1", "CONCORDIA_NOW=")
	env = append(env, extraEnv...)

	res, err := procexec.Run(r.t.Context(), concordiaBin, args, procexec.Options{Dir: r.dir, Env: env, Timeout: asrTimeout})
	if err != nil {
		r.t.Fatalf("concordia %s: %v", strings.Join(args, " "), err)
	}

	return cliResult{code: res.Code, stderr: res.Stderr, stdout: res.Stdout}
}

// check runs `concordia check --format json` and returns the parsed results and the CLI result. extra are more
// arguments after `check`.
func (r *asrRepo) check(extra ...string) (jv, cliResult) {
	r.t.Helper()

	args := append([]string{"--format", "json", "check"}, extra...)

	var res cliResult

	asrExclusive(func() { res = r.cli(r.env, args...) })

	var doc map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		r.t.Fatalf("check output is not JSON (exit %d): %v\nstdout: %s\nstderr: %s", res.code, err, res.stdout, res.stderr)
	}

	if errs := conformance.ValidateResultJSON([]byte(res.stdout)); len(errs) > 0 {
		r.t.Errorf("results.json is not valid against the results schema: %v", errs)
	}

	asrExitInSet(r.t, res)

	return jv{v: doc}, res
}

// asrExitCodes is the set of exit codes of the CLI contract.
var asrExitCodes = []int{0, 1, 2, 3, 4, 5, 6, 7, 130}

// asrExitInSet fails the test when the run exited with a code outside the contract.
func asrExitInSet(t *testing.T, res cliResult) {
	t.Helper()

	if !slices.Contains(asrExitCodes, res.code) {
		t.Errorf("exit code %d is not one of %v\nstderr: %s", res.code, asrExitCodes, res.stderr)
	}
}

// exportBundle runs `export bundle` after a check and opens the archive. It gives the extract directory and the manifest.
func (r *asrRepo) exportBundle() (string, *bundle.Manifest) {
	r.t.Helper()

	res := r.cli(r.env, "export", "bundle")
	asrExitInSet(r.t, res)

	if res.code != 0 {
		r.t.Fatalf("export bundle: exit %d: %s", res.code, res.output())
	}

	dir := r.t.TempDir()

	manifest, err := bundle.Open(r.path("build/concordia/conformance.tar.gz"), dir)
	if err != nil {
		r.t.Fatalf("open the bundle: %v", err)
	}

	return dir, manifest
}

// lineageOf is the lineage entry of a container of the payments api in production.
func lineageOf(t *testing.T, doc jv, container string) jv {
	t.Helper()

	for _, entry := range doc.at("assurance", "lineage").list() {
		if entry.at("occurrence", "container").str() == container && entry.at("occurrence", "workload", "namespace").str() == "payments" {
			return entry
		}
	}

	t.Fatalf("no lineage entry for the container %s: %s", container, doc.at("assurance", "lineage").text())

	return jv{}
}

// explain runs `concordia explain <rule>` on the results of the latest check.
func (r *asrRepo) explain(rule string) cliResult {
	r.t.Helper()

	return r.cli(r.env, "explain", rule)
}

// jv is a JSON value with path navigation for the tests.
type jv struct {
	v any
}

// at follows object keys and list indexes (`assurance`, `lineage`, `0`). A missing step gives an absent value.
func (j jv) at(path ...string) jv {
	cur := j.v

	for _, step := range path {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[step]
		case []any:
			i, ok := asrListIndex(step, len(node))
			if !ok {
				return jv{}
			}

			cur = node[i]
		default:
			return jv{}
		}
	}

	return jv{v: cur}
}

// asrListIndex parses a list index step and tells whether it lies inside a list of n entries.
func asrListIndex(step string, n int) (int, bool) {
	var i int
	if _, err := fmt.Sscanf(step, "%d", &i); err != nil {
		return 0, false
	}

	return i, i >= 0 && i < n
}

func (j jv) present() bool { return j.v != nil }

func (j jv) str() string {
	s, _ := j.v.(string)

	return s
}

func (j jv) list() []jv {
	items, _ := j.v.([]any)

	out := make([]jv, len(items))
	for i, item := range items {
		out[i] = jv{v: item}
	}

	return out
}

// where finds the first list entry whose fields equal the pairs.
func (j jv) where(pairs ...string) jv {
	for _, item := range j.list() {
		match := true

		for i := 0; i+1 < len(pairs); i += 2 {
			if item.at(pairs[i]).str() != pairs[i+1] {
				match = false
			}
		}

		if match {
			return item
		}
	}

	return jv{}
}

// text is the canonical JSON of the value.
func (j jv) text() string {
	data, err := json.Marshal(j.v)
	if err != nil {
		return ""
	}

	return string(data)
}

// rule is the rule result with the id.
func (j jv) rule(id string) jv { return j.at("rules").where("ruleId", id) }

// obligations are the obligations of a rule result.
func (j jv) obligations() []jv { return j.at("obligations").list() }

// hasDiagnostic says whether the results carry the diagnostic code.
func (j jv) hasDiagnostic(code string) bool { return j.at("diagnostics").where("code", code).present() }

// runGit runs git in dir and gives stdout.
func runGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()

	res, err := procexec.Run(t.Context(), "git", args, procexec.Options{Dir: dir, Timeout: gitTimeout})
	if err != nil {
		return "", err
	}

	if res.Code != 0 {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), res.Stderr)
	}

	return res.Stdout, nil
}

// pathWithout gives a PATH that holds git and CBM but not the named program: a directory of links to the tools that a
// run needs.
func pathWithout(t *testing.T, program string) string {
	t.Helper()

	dir := t.TempDir()

	for _, tool := range []string{"git", cbmExecutable} {
		found, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("%s is not on the PATH: %v", tool, err)
		}

		if err := os.Symlink(found, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, program)); err == nil {
		t.Fatalf("%s is a tool that the run needs", program)
	}

	return dir
}

// asrFixtureFiles reads a fixture directory into workspace files below dest.
func asrFixtureFiles(t *testing.T, src, dest string) map[string]string {
	t.Helper()

	out := map[string]string{}

	err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		out[dest+"/"+filepath.ToSlash(rel)] = string(data)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// asrTripwire writes a renderer that records every start and fails: the hostile content must be refused before any
// program starts. It gives the executable and the log.
func asrTripwire(t *testing.T, name string) (exe, log string) {
	t.Helper()

	dir := t.TempDir()
	log = filepath.Join(dir, name+".log")
	exe = filepath.Join(dir, name)

	script := "#!/bin/sh\necho \"start $*\" >> '" + log + "'\nexit 1\n"
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return exe, log
}
