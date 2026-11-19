package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The assurance adapters end to end (tasks 11.1 to 11.4): the binary and the real CBM on the A10 infra fixture
// extended with an `assurance` block (see assurance_helpers_test.go). Every scenario asserts the result, its explanation
// (`concordia explain`), and its provenance (the statements and lineage of results.json and the bundle of
// `export bundle`). Every `check` of these tests also validates results.json against the results schema and the exit
// code against the contract.

const (
	asrCheckRule = "  - id: CHK-001\n    name: The unit tests pass for production\n    severity: error\n    require:\n" +
		"      checkResult: {check: unit-tests, environment: production, subject: {calmNodes: [api-service]}}\n"
	asrNetworkEnforced = "    - {environment: production, kinds: [network-policy], assumes: [network-policy-enforced]}\n"
	asrNetworkOnly     = "    - {environment: production, kinds: [network-policy]}\n"
)

// asrLineageWorld is the A34 world: both containers of the payments api are pinned by digest, the build provenance
// names the digest, and a deployment record and an observation agree on it.
func asrLineageWorld(t *testing.T) *asrWorld {
	t.Helper()

	return &asrWorld{
		t: t, sidecar: asrRepository + "@" + asrDigestA, attestations: []string{asrShop},
		records: []asrRecord{
			{path: "deploy-1.json", digest: asrDigestA},
			{path: "deploy-2.json", container: "sidecar", digest: asrDigestA},
			{path: "observed-1.json", digest: asrDigestA},
			{path: "observed-2.json", container: "sidecar", digest: asrDigestA},
		},
	}
}

// asrFullWorld has every adapter: rendered manifests with a network constellation, provenance, records, check results,
// and quality scenarios with a measurement.
func asrFullWorld(t *testing.T) *asrWorld {
	t.Helper()

	w := asrLineageWorld(t)
	w.withShop = true
	w.evaluators = asrNetworkEnforced
	w.rules = []string{
		asrEffectiveRule("ACC-001", "shop-api", "denied"), asrEffectiveRule("ACC-002", "shop-web", "allowed", 8080),
		asrCheckRule,
	}
	w.results = []asrCheckResult{{file: "unit.json"}}
	w.scenarios = asrScenarios
	w.measurements = []asrMeasurement{{file: "failover.json", scenario: "QS-FAILOVER", value: 12}}

	return w
}

const asrScenarios = `apiVersion: concordia.io/v1alpha1
kind: QualityScenarioSet
scenarios:
  - id: QS-FAILOVER
    area: availability
    qualityAttribute: availability
    stimulusSource: operator
    stimulus: the primary database node fails
    environment: production
    expectedResponse: a replica takes over
    owner: platform-team
    scope: [api-service]
    mode: measured
    enforcement: mandatory
    criterion:
      metric: failover-seconds
      unit: s
      comparison: lte
      threshold: 30
      maxAgeDays: 30
  - id: QS-BOUNDARY
    area: modifiability
    qualityAttribute: modifiability
    stimulusSource: developer
    stimulus: a developer adds a feature
    environment: production
    expectedResponse: the change stays inside the boundary
    owner: orders-team
    scope: [api-service]
    mode: structural
    criterion:
      rules: [ASR-REN-001]
  - id: QS-RUNBOOK
    area: operability
    qualityAttribute: operability
    stimulusSource: on-call engineer
    stimulus: an alert fires at night
    environment: production
    expectedResponse: the runbook leads to a fix
    owner: sre-team
    scope: [api-service]
    mode: reviewed
    criterion:
      subject: orders-runbook
      authority: sre-lead
`

// bundleStatements reads the statements that the bundle persisted: digest to subject digests.
func bundleStatements(t *testing.T, dir string, paths []string) map[string][]string {
	t.Helper()

	out := map[string][]string{}

	for _, rel := range paths {
		if !strings.HasPrefix(rel, "assurance/statements/") {
			continue
		}

		digest, subjects := readPersistedStatement(t, dir, rel)
		out[digest] = subjects
	}

	return out
}

// readPersistedStatement reads one persisted statement of the bundle: its digest and its sorted subject digests.
func readPersistedStatement(t *testing.T, dir, rel string) (digest string, subjects []string) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}

	var persisted struct {
		Digest  string `json:"digest"`
		Subject []struct {
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
	}

	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}

	if rel != "assurance/statements/"+strings.TrimPrefix(persisted.Digest, "sha256:")+".json" {
		t.Errorf("%s does not carry the digest %s", rel, persisted.Digest)
	}

	for _, s := range persisted.Subject {
		for algorithm, value := range s.Digest {
			subjects = append(subjects, algorithm+":"+value)
		}
	}

	slices.Sort(subjects)

	return persisted.Digest, subjects
}

// bundleFiles exports the bundle and gives the extract directory and the paths of its files.
func (r *asrRepo) bundleFiles() (dir string, paths []string) {
	r.t.Helper()

	dir, manifest := r.exportBundle()
	paths = make([]string, len(manifest.Files))

	for i := range manifest.Files {
		paths[i] = manifest.Files[i].Path
	}

	return dir, paths
}

// requireStatementInBundle says that the statement with the digest is persisted in the bundle, with its subject.
func requireStatementInBundle(t *testing.T, dir string, paths []string, digest, subject string) {
	t.Helper()

	persisted := bundleStatements(t, dir, paths)

	subjects, ok := persisted[digest]
	if !ok {
		t.Errorf("the bundle does not hold the statement %s: %v", digest, paths)

		return
	}

	if subject != "" && !slices.Contains(subjects, subject) {
		t.Errorf("the statement %s has the subjects %v in the bundle, want %s", digest, subjects, subject)
	}
}
