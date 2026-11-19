package integration_test

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// Acceptance fixtures of the evidence foundation (task 15.1). Each subtest of
// TestFoundationAcceptance replays the infra recording (CBM 0.12.1) through the real
// pipeline or the CLI and asserts three things: the result, the explanation (explain
// output or evidence path), and the provenance (profile identifier and digest,
// normalization version, environment, source location).

const (
	foundationWorkspace = `apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: foundation
architecture:
  source: architecture/infra.calm.json
observation:
  provider: codebase-memory-mcp
  projects:
    - name: concordia-fixture-infra
      root: .
      include: ["**"]
  indexMode: fast
bindings:
  files: [bindings/concordia.bindings.yaml]
policies:
  files: [policies/**/*.policy.yaml]
check:
  minimumCoverage: 0
  unknown: fail
  failOn: error
`

	foundationEnvironments = `environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/prod/**", "k8s/overrides/**", "kustomize/base/**", "charts/**", "scripts/**"]
  - id: staging
    cluster: stg-eu
    sources: ["k8s/staging/**", "kustomize/base/**"]
  - id: edge
    sources: ["deploy/**"]
`

	foundationArchitectureText = `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {"unique-id": "payments-api", "node-type": "service", "name": "Payments API", "description": "The payments API."},
    {"unique-id": "payments-db", "node-type": "service", "name": "Payments database", "description": "The payments database."},
    {"unique-id": "payments-worker", "node-type": "service", "name": "Payments worker", "description": "The payments worker."},
    {"unique-id": "payments-reports", "node-type": "service", "name": "Payments reports", "description": "The reports service."},
    {"unique-id": "legacy-api", "node-type": "service", "name": "Legacy API", "description": "A second owner of the API image."},
    {"unique-id": "terraform-root", "node-type": "service", "name": "Terraform root", "description": "The Terraform root module."},
    {"unique-id": "terraform-module", "node-type": "service", "name": "Terraform network module", "description": "A module that the root imports."},
    {"unique-id": "app-code", "node-type": "service", "name": "Application code", "description": "The Go application."},
    {"unique-id": "helm-charts", "node-type": "service", "name": "Helm charts", "description": "Helm charts."},
    {"unique-id": "ci-scripts", "node-type": "service", "name": "CI scripts", "description": "Deployment scripts."},
    {"unique-id": "prod-manifests", "node-type": "service", "name": "Production manifests", "description": "Production Kubernetes manifests."}
  ],
  "relationships": []
}
`

	foundationPolicyHead = `apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: foundation
  version: 1.0.0
`

	foundationBindingsHead = `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
`

	// foundationDefaultBindings binds the source roots of the deployment scripts and the production manifests.
	foundationDefaultBindings = `  - id: bind-ci-scripts
    architectureRef: calm:node:ci-scripts
    exclusive: true
    match:
      any:
        - sourceRoot: scripts/**
  - id: bind-prod-manifests
    architectureRef: calm:node:prod-manifests
    exclusive: true
    match:
      any:
        - sourceRoot: k8s/prod/**
`

	// foundationDefaultPolicies is the A43 pair of rules of deploys_rule_test.go.
	foundationDefaultPolicies = `  - id: CO-DEP-001
    name: Scripts do not deploy production manifests
    severity: error
    subject:
      calmNodes: [ci-scripts]
    forbid:
      target:
        calmNodes: [prod-manifests]
      edges: [deploys]
      traversal: direct
  - id: CO-DEP-002
    name: Manifests do not deploy scripts
    severity: error
    subject:
      calmNodes: [prod-manifests]
    forbid:
      target:
        calmNodes: [ci-scripts]
      edges: [DEPLOYS]
      traversal: direct
`
)

// foundationSpec is the workspace of one subtest. Empty fields take the defaults.
type foundationSpec struct {
	// files replace or add files of the workspace (path relative to the workspace root).
	files map[string]string
	// edit changes the options of the check.
	edit func(*conformance.CheckOptions)
	// workspace replaces the workspace text. environments replaces the declared environments.
	workspace    string
	environments string
	bindings     string
	policies     string
	// recording is the recording file. The default is the infra recording.
	recording string
	// index writes the index result of the recording first, as `concordia index` does.
	index bool
}

// foundation is a workspace copy of the infra fixture with a replayed recording.
type foundation struct {
	rec    observation.Recording
	client observation.Client
	run    *conformance.Run
	logs   *bytes.Buffer
	dir    string
	files  []string
}

// newFoundation copies the infra fixture and writes the workspace files of the spec. It runs no check.
func newFoundation(t *testing.T, spec *foundationSpec) *foundation {
	t.Helper()

	text := spec.workspace
	if text == "" {
		text = foundationWorkspace
	}

	envs := spec.environments
	if envs == "" {
		envs = foundationEnvironments
	}

	bindings := cmp.Or(spec.bindings, foundationDefaultBindings)
	policies := cmp.Or(spec.policies, foundationDefaultPolicies)

	files := map[string]string{
		"concordia.yaml":                   text + envs,
		"architecture/infra.calm.json":     foundationArchitectureText,
		"bindings/concordia.bindings.yaml": foundationBindingsHead + bindings,
		"policies/foundation.policy.yaml":  foundationPolicyHead + "rules:\n" + policies,
	}

	maps.Copy(files, spec.files)

	w := newInfraWorkspace(t, cmp.Or(spec.recording, infraRecordingFile), files)

	return &foundation{rec: w.rec, dir: w.dir, logs: &bytes.Buffer{}, files: w.files, client: observation.NewReplayClient(w.rec)}
}

func (f *foundation) checkOptions(edit func(*conformance.CheckOptions)) *conformance.CheckOptions {
	w := &infraWorkspace{rec: f.rec, dir: f.dir, files: f.files}
	opts := w.checkOptions(logger.New(logger.Options{Out: f.logs, RunID: "run", Verbose: true, Trace: true}))
	opts.Deps.CreateClient = func(*workspace.Workspace, *logger.Logger) observation.Client { return f.client }
	opts.SkipReports = true
	opts.KeepEvidence = true

	if edit != nil {
		edit(opts)
	}

	return opts
}

// check runs the pipeline in process and keeps the merged evidence in run.Evidence.
func (f *foundation) check(t *testing.T, spec *foundationSpec) (*conformance.Run, error) {
	t.Helper()

	if spec.index {
		first, err := conformance.RunCheck(context.Background(), f.checkOptions(spec.edit))
		if err != nil {
			return nil, err
		}

		recFile := spec.recording
		if recFile == "" {
			recFile = infraRecordingFile
		}

		snap := replayInfra(t, recFile)
		record := snap.index.Record(infraProject, first.Run.GraphGeneration, f.rec.HeadSHA)

		if err = conformance.WriteIndexRecord(f.dir, conformance.IndexRecordFile, &record); err != nil {
			t.Fatal(err)
		}
	}

	return conformance.RunCheck(context.Background(), f.checkOptions(spec.edit))
}

// foundationRun builds the workspace and checks it. A failed check fails the test.
func foundationRun(t *testing.T, spec *foundationSpec) *foundation {
	t.Helper()

	f := newFoundation(t, spec)

	run, err := f.check(t, spec)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}

	f.run = run

	return f
}

// cli runs the CLI in the workspace copy. The CBM client replays the recording.
func (f *foundation) cli(t *testing.T, args ...string) cliResult {
	t.Helper()

	return f.cliWith(t, t.Context(), args...)
}

func (f *foundation) cliWith(t *testing.T, ctx context.Context, args ...string) cliResult { //nolint:revive // the context follows the test handle of the helper family.
	t.Helper()

	var stdout, stderr bytes.Buffer

	stdio := cli.IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Env: map[string]string{"CONCORDIA_NOW": fixedNow}, Cwd: f.dir}
	deps := cli.Deps{
		NewClient: func(*workspace.Workspace, *cli.Context) observation.Client { return f.client },
		NewGit:    func(string) workspace.GitPort { return &workspace.FakeGit{Rev: f.rec.HeadSHA, Files: f.files} },
	}

	code := cli.Run(ctx, args, &stdio, deps, cli.BuildInfo{Version: "test"})

	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// ---- helpers of the subtests ----

func workloadBinding(id, node, env, name, namespace string) string {
	text := "  - id: " + id + "\n    architectureRef: calm:node:" + node + "\n    match:\n      any:\n        - {kind: workload, environment: " + env +
		", resourceKind: Deployment, name: " + name

	if namespace != "" {
		text += ", namespace: " + namespace
	}

	return text + "}\n"
}

func sourceRootBinding(id, node, glob string) string {
	return "  - id: " + id + "\n    architectureRef: calm:node:" + node + "\n    match:\n      any:\n        - {sourceRoot: '" + glob + "'}\n"
}

// typedRule makes a rule with an obligation block. extra holds the lines that follow `relation` (indented by six).
func typedRule(id, subject, typ, kind, direction, target, extra string) string {
	relation := "{kind: " + kind + ", direction: " + direction
	if target != "" {
		relation += ", target: {calmNodes: [" + target + "]}"
	}

	return "  - id: " + id + "\n    name: " + id + "\n    severity: error\n    subject: {calmNodes: [" + subject + "]}\n    obligation:\n      type: " + typ +
		"\n      relation: " + relation + "}\n" + extra
}

func forbidRule(id, subject, target, edges string) string {
	return "  - id: " + id + "\n    name: " + id + "\n    severity: error\n    subject: {calmNodes: [" + subject + "]}\n    forbid:\n      target: {calmNodes: [" +
		target + "]}\n      edges: [" + edges + "]\n      traversal: direct\n"
}

func ruleOf(t *testing.T, run *conformance.Run, id string) *conformance.RuleResult {
	t.Helper()

	for i := range run.Rules {
		if run.Rules[i].RuleID == id {
			return &run.Rules[i]
		}
	}

	t.Fatalf("no result for rule %s in %v", id, verdictsOf(run))

	return nil
}

func obligationFor(t *testing.T, rule *conformance.RuleResult, subject, env string) *conformance.ObligationResult {
	t.Helper()

	for i := range rule.Obligations {
		if rule.Obligations[i].Subject == subject && rule.Obligations[i].Environment == env {
			return &rule.Obligations[i]
		}
	}

	t.Fatalf("rule %s has no obligation for %s in %q", rule.RuleID, subject, env)

	return nil
}

func hasDiagnostic(run *conformance.Run, code string) bool {
	return slices.ContainsFunc(run.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == code })
}

func evidenceEdges(run *conformance.Run, kind observation.CanonicalEdgeKind) []*observation.EvidenceEdge {
	var out []*observation.EvidenceEdge

	for _, e := range run.Evidence.Edges {
		if e.Kind == kind {
			out = append(out, e)
		}
	}

	return out
}

func requireContains(t *testing.T, what, text string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q:\n%s", what, want, text)
		}
	}
}

func requireEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

// shippedProfile is the capability profile of CBM 0.12.1 that every run of the recording must record.
func shippedProfile(t *testing.T) *observation.ProfileRef {
	t.Helper()

	profile, ok := observation.ProfileFor("0.12.1", workspace.AdapterSchema)
	if !ok {
		t.Fatal("no capability profile for CBM 0.12.1")
	}

	return profile.Ref()
}

// hasShippedProvenance is true when the toolchain records the shipped profile, the CBM version, and the adapter schema.
func hasShippedProvenance(cbm *conformance.CbmToolchain, want *observation.ProfileRef) bool {
	if !cbm.Profile.Applies {
		return false
	}

	if cbm.ExecutableVersion != "0.12.1" || cbm.AdapterSchema != workspace.AdapterSchema {
		return false
	}

	return cbm.Profile.ID == want.ID && cbm.Profile.Digest == want.Digest
}

// requireProvenance asserts the provenance that every result of the recording carries: the profile identifier and
// digest, the provider version, the adapter schema, and the normalization version of each fact.
func requireProvenance(t *testing.T, run *conformance.Run) {
	t.Helper()

	want := shippedProfile(t)
	cbm := run.Toolchain.Cbm

	if !hasShippedProvenance(&cbm, want) {
		t.Fatalf("toolchain cbm = %+v, want profile %+v, version 0.12.1, adapter %s", cbm, want, workspace.AdapterSchema)
	}

	for _, e := range run.Evidence.Edges {
		if e.NormalizationVersion != observation.NormalizationVersion {
			t.Fatalf("fact %s has the normalization version %q, want %q", e.ID, e.NormalizationVersion, observation.NormalizationVersion)
		}
	}
}

// resultsOf reads the results.json that `check` wrote in the workspace.
func (f *foundation) resultsOf(t *testing.T) map[string]any {
	t.Helper()

	var results map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(f.dir, resultsFile)), &results); err != nil {
		t.Fatalf("parse results.json: %v", err)
	}

	return results
}

// jsonAt reads a nested value of a decoded JSON document.
func jsonAt(t *testing.T, doc any, keys ...string) any {
	t.Helper()

	for _, key := range keys {
		object, ok := doc.(map[string]any)
		if !ok {
			t.Fatalf("%v is not an object at %q", doc, key)
		}

		doc = object[key]
	}

	return doc
}

func findingWith(t *testing.T, run *conformance.Run, ruleID string, keep func(*conformance.Finding) bool) *conformance.Finding {
	t.Helper()

	for i := range run.Findings {
		if run.Findings[i].RuleID == ruleID && keep(&run.Findings[i]) {
			return &run.Findings[i]
		}
	}

	t.Fatalf("rule %s has no such finding: %+v", ruleID, findingsOf(run, ruleID))

	return nil
}

// ---- the fixtures ----

func TestFoundationAcceptance(t *testing.T) {
	t.Run("A01_unmapped_native_edge_and_unbound_endpoint", func(t *testing.T) {
		f := foundationRun(t, &foundationSpec{index: true})
		run, ev := f.run, f.run.Evidence

		// Result: the unmapped native edge is kept as an unsupported fact, with its native kind and properties.
		if len(ev.Unsupported) != 1 || ev.Unsupported[0].NativeKind != "HAS_BRANCH" || ev.Unsupported[0].Kind != observation.KindUnsupported ||
			ev.Unsupported[0].Properties["branch"] == "" {
			t.Fatalf("unsupported facts = %+v, want one HAS_BRANCH fact that keeps its properties", ev.Unsupported)
		}

		unmapped := 0
		for _, count := range ev.UnmappedNativeEdges {
			unmapped += count
		}

		requireEqual(t, "unsupported facts counter", run.Observability.UnsupportedFacts, unmapped)
		requireEqual(t, "unsupported skip count", run.Coverage.Skipped.Unsupported, 1)

		// The unbound endpoint fact stays in the evidence, the counters include it, and nothing is skipped silently.
		if run.Coverage.Skipped.Unbound == 0 || run.Observability.UnresolvedFacts < run.Coverage.Skipped.Unbound {
			t.Fatalf("unbound %d, unresolved facts counter %d", run.Coverage.Skipped.Unbound, run.Observability.UnresolvedFacts)
		}

		reasons := ruleOf(t, run, "CO-DEP-001").Coverage.SkipReasons
		if reasons["unbound"] == "" || reasons["unsupported"] == "" {
			t.Errorf("skip reasons = %v, want a reason for each skip count", reasons)
		}

		gap := findingWith(t, run, "CO-DEP-001", func(f *conformance.Finding) bool { return f.Kind == conformance.KindBindingGap })
		if gap.NextAction != "resolve binding" || gap.Location == nil || gap.Location.Path != "charts/api/Chart.yaml" {
			t.Errorf("binding gap = %+v, want next action resolve binding at charts/api/Chart.yaml", gap)
		}

		// A synthesized image endpoint has no binding and still shows its source location.
		var image *observation.ObservedNode

		for i := range ev.Entities {
			if ev.Entities[i].Label == "Image" && ev.Entities[i].QualifiedName == "registry.example.com/payments/api" {
				image = &ev.Entities[i]
			}
		}

		if image == nil || image.Resolution != observation.ResolutionUnbound || image.FilePath == "" || !strings.HasPrefix(image.ObservedID, "synth:") {
			t.Fatalf("image entity = %+v", image)
		}

		// A rule that uses only supported kinds keeps its normal verdict.
		requireEqual(t, "CO-DEP-001 verdict", ruleOf(t, run, "CO-DEP-001").Verdict, conformance.VerdictViolation)

		// Explanation: the diagnostic names the native kind and the count, and explain shows the binding gap.
		check := f.cli(t, "check", "--no-color")
		requireEqual(t, "check exit code", check.code, 1)
		requireContains(t, "check stderr", check.stderr, "CO-OBS-016", "HAS_BRANCH (1 edges)")
		requireContains(t, "check stdout", check.stdout, "Next action: resolve binding", "charts/api/Chart.yaml:1")

		explain := f.cli(t, "explain", "CO-DEP-001")
		requireEqual(t, "explain exit code", explain.code, 0)
		requireContains(t, "explain", explain.stdout, "CO-BND-005", "charts/api/Chart.yaml")

		// Provenance: counters and profile in results.json, normalization version on each fact.
		results := f.resultsOf(t)
		if got := jsonAt(t, results, "observability", "unsupportedFacts"); got != float64(unmapped) {
			t.Errorf("results.json observability.unsupportedFacts = %v, want %d", got, unmapped)
		}

		profile := shippedProfile(t)
		if jsonAt(t, results, "toolchain", "cbm", "profile", "id") != profile.ID || jsonAt(t, results, "toolchain", "cbm", "profile", "digest") != profile.Digest {
			t.Errorf("results.json toolchain = %v, want profile %+v", jsonAt(t, results, "toolchain"), profile)
		}

		requireEqual(t, "unsupported fact normalization version", ev.Unsupported[0].NormalizationVersion, observation.NormalizationVersion)
		requireProvenance(t, run)
	})

	t.Run("A02_two_connections_on_one_pair", func(t *testing.T) {
		// Compose api -> db carries two facts: the declared service dependency and the connection of DATABASE_URL.
		spec := &foundationSpec{
			index:        true,
			environments: "environments:\n  - {id: local, sources: [compose.yaml]}\n",
			bindings: foundationDefaultBindings +
				"  - {id: bind-api, architectureRef: 'calm:node:payments-api', match: {any: [{kind: composeService, file: compose.yaml, service: api}]}}\n" +
				"  - {id: bind-db, architectureRef: 'calm:node:payments-db', match: {any: [{kind: composeService, file: compose.yaml, service: db}]}}\n",
			policies: typedRule("CO-NET-001", "payments-api", "allowed", "infrastructure-dependency", "from-subject", "payments-db", "      scope: {environments: [local]}\n") +
				typedRule("CO-NET-002", "payments-api", "forbidden", "configured-connection", "from-subject", "payments-db", "      scope: {environments: [local]}\n"),
		}
		f := foundationRun(t, spec)
		run := f.run

		// Result: two separate results on one pair. The forbidden connection is a violation, the allowed one passes.
		allowed, forbidden := ruleOf(t, run, "CO-NET-001"), ruleOf(t, run, "CO-NET-002")
		requireEqual(t, "CO-NET-001 verdict", allowed.Verdict, conformance.VerdictPass)
		requireEqual(t, "CO-NET-002 verdict", forbidden.Verdict, conformance.VerdictViolation)

		pass, violation := obligationFor(t, allowed, "payments-api", "local"), obligationFor(t, forbidden, "payments-api", "local")
		if pass.ID == violation.ID || len(pass.Occurrences) != 1 || len(violation.Occurrences) != 1 ||
			pass.Occurrences[0].Source.QualifiedName != violation.Occurrences[0].Source.QualifiedName ||
			pass.Occurrences[0].Target.QualifiedName != violation.Occurrences[0].Target.QualifiedName {
			t.Fatalf("results %+v and %+v must be two results on one pair", pass, violation)
		}

		// The two facts differ in their edge identity.
		var keys []string

		for _, kind := range []observation.CanonicalEdgeKind{observation.KindInfrastructureDependency, observation.KindConfiguredConnection} {
			for _, e := range evidenceEdges(run, kind) {
				if run.Evidence.Nodes[e.From].Properties["service"] == "" && strings.Contains(run.Evidence.Nodes[e.From].QualifiedName, "compose/Service/-/api") {
					keys = append(keys, observation.EdgeKeyOf(e).String())
				}
			}
		}

		if len(keys) != 2 || keys[0] == keys[1] {
			t.Fatalf("edge keys of the pair = %v, want two different identities", keys)
		}

		// The pair report does not hide the violation: infrastructure facts make no reconciliation entry, and the gate
		// decision is the per-occurrence result.
		for _, entry := range run.Reconciliation {
			if entry.From == "payments-api" && entry.To == "payments-db" {
				t.Errorf("reconciliation entry for an infrastructure pair: %+v", entry)
			}
		}

		// Explanation and provenance: the finding names the occurrence, the environment, and the source line.
		finding := findingWith(t, run, "CO-NET-002", func(f *conformance.Finding) bool { return f.Result == conformance.VerdictViolation })
		if finding.Kind != conformance.KindObligationViolation || finding.Environment != "local" || finding.Location == nil || finding.Location.Path != "compose.yaml" ||
			finding.ObligationID != violation.ID || !strings.Contains(finding.Observed, "configured-connection") {
			t.Fatalf("finding = %+v", finding)
		}

		explain := f.cli(t, "explain", "CO-NET-002", "--finding", finding.Fingerprint)
		requireEqual(t, "explain exit code", explain.code, 0)
		requireContains(t, "explain", explain.stdout, "obligation-violation", "compose.yaml:3")
		requireProvenance(t, run)
	})

	t.Run("A03_absence_needs_complete_coverage", func(t *testing.T) {
		spec := &foundationSpec{
			index: true,
			bindings: foundationDefaultBindings + workloadBinding("bind-worker", "payments-worker", "staging", "worker", "payments") +
				sourceRootBinding("bind-scripts-imports", "app-code", "app/**"),
			policies: typedRule("CO-ABS-001", "payments-worker", "required", "selects-workload", "to-workload", "", "      scope: {environments: [staging]}\n") +
				typedRule("CO-ABS-002", "ci-scripts", "required", "imports", "from-subject", "", "      scope: {environments: [production]}\n"),
		}
		f := foundationRun(t, spec)
		run := f.run

		// Result: the Service of the worker is in another directory, the provider gives no join (O6), and the extraction of
		// the family is partial: the absence is unknown with the coverage reason, never a pass or a definite absence.
		worker := obligationFor(t, ruleOf(t, run, "CO-ABS-001"), "payments-worker", "staging")
		if worker.Verdict != conformance.VerdictUnknown || worker.Reason != conformance.ReasonCoverageIncomplete ||
			len(worker.Occurrences) != 1 || worker.Occurrences[0].Class != conformance.OccurrenceAbsent {
			t.Fatalf("worker obligation = %+v, want an unknown absence with the coverage reason", worker)
		}

		record := predicateRecord(t, run, "selects-workload", observation.CoverageScope{Environment: "staging"})
		if record.Status != observation.CoveragePartial {
			t.Fatalf("selects-workload in staging = %+v, want partial", record)
		}

		// Explanation: the finding names the reason and the loss mode that makes the extraction partial.
		finding := findingWith(t, run, "CO-ABS-001", func(*conformance.Finding) bool { return true })
		if finding.Kind != conformance.KindObligationUnknown || finding.Reason != conformance.ReasonCoverageIncomplete || finding.Environment != "staging" ||
			!strings.Contains(finding.Observed, "selector-join-directory-scoped") {
			t.Fatalf("finding = %+v", finding)
		}

		explain := f.cli(t, "explain", "CO-ABS-001", "--finding", finding.Fingerprint)
		requireEqual(t, "explain exit code", explain.code, 0)
		requireContains(t, "explain", explain.stdout, "obligation-unknown")

		// The same absence is a violation when the coverage of the family is complete (`imports` is a code family).
		scripts := obligationFor(t, ruleOf(t, run, "CO-ABS-002"), "ci-scripts", "production")
		if scripts.Verdict != conformance.VerdictViolation || len(scripts.Occurrences) == 0 || scripts.Occurrences[0].Class != conformance.OccurrenceAbsent {
			t.Fatalf("scripts obligation = %+v, want a violation by absence", scripts)
		}

		// Provenance: the environment of the result and of the coverage record, and the profile of the run.
		requireEqual(t, "absence environment", worker.Occurrences[0].Environment, "staging")
		requireProvenance(t, run)
	})

	t.Run("A04_same_names_in_two_environments", func(t *testing.T) {
		spec := &foundationSpec{
			index:    true,
			bindings: foundationDefaultBindings + workloadBinding("bind-api", "payments-api", "production", "api", ""),
			policies: typedRule("CO-ENV-001", "payments-api", "required", "uses-image", "from-workload", "", "      scope: {environments: [production, staging]}\n"),
		}
		f := foundationRun(t, spec)
		run := f.run

		// Result: the Deployment `api` and the Kustomize base `gateway` exist in two environments, with different identities.
		scoped := map[string]string{}

		for id := range run.Evidence.Nodes {
			n := run.Evidence.Nodes[id]
			if key, ok := observation.ObjKeyOf(&n); ok && (strings.HasSuffix(key, "/api") && strings.HasPrefix(key, "apps/Deployment/") || key == "apps/Deployment/?/gateway") && n.FilePath != "k8s/overrides/reports.yaml" {
				scoped[id] = n.ScopedID
			}
		}

		want := []string{
			"k8s:production/prod-eu/apps/Deployment/payments/api", "k8s:staging/stg-eu/apps/Deployment/payments-staging/api",
			"k8s:production/prod-eu/apps/Deployment/~/gateway", "k8s:staging/stg-eu/apps/Deployment/~/gateway",
		}

		got := slices.Sorted(maps.Values(scoped))
		for _, id := range want {
			if !slices.Contains(got, id) {
				t.Errorf("scoped identities %v lack %s", got, id)
			}
		}

		// The binding names production: only the production workload is bound, and the two environments give two
		// obligations that are evaluated separately, each with the facts of its own environment.
		rule := ruleOf(t, run, "CO-ENV-001")
		production, staging := obligationFor(t, rule, "payments-api", "production"), obligationFor(t, rule, "payments-api", "staging")

		if production.ID == staging.ID {
			t.Fatal("two environments gave one obligation")
		}

		if len(production.Occurrences) != 1 || production.Occurrences[0].Class != conformance.OccurrenceMatching || production.Occurrences[0].Occurrence != "sidecar" ||
			production.Occurrences[0].Environment != "production" {
			t.Errorf("production occurrences = %+v", production.Occurrences)
		}

		if len(staging.Occurrences) != 1 || staging.Occurrences[0].Class != conformance.OccurrenceAbsent || staging.Occurrences[0].Environment != "staging" {
			t.Errorf("staging occurrences = %+v, want an absence: the binding does not name staging", staging.Occurrences)
		}

		for _, o := range []*conformance.ObligationResult{production, staging} {
			if o.Verdict != conformance.VerdictUnknown {
				t.Errorf("%s obligation = %s, want unknown (the version coverage of the family is partial)", o.Environment, o.Verdict)
			}
		}

		// Evidence: every uses-image fact has the environment of its own manifest.
		for _, e := range evidenceEdges(run, observation.KindUsesImage) {
			if e.SourceSpan != nil && strings.HasPrefix(e.SourceSpan.Path, "k8s/staging/") && e.Environment != "staging" {
				t.Errorf("staging fact %s has the environment %q", e.ID, e.Environment)
			}
		}

		// No environment means no environment-specific assurance: a Compose service is in no environment source.
		unresolved := foundationRun(t, &foundationSpec{
			index: true,
			bindings: foundationDefaultBindings +
				"  - {id: bind-api, architectureRef: 'calm:node:payments-api', match: {any: [{kind: composeService, file: compose.yaml, service: api}]}}\n",
			policies: typedRule("CO-ENV-002", "payments-api", "required", "configured-connection", "from-subject", "", "      scope: {environments: [production]}\n"),
		})

		o := obligationFor(t, ruleOf(t, unresolved.run, "CO-ENV-002"), "payments-api", "production")
		if o.Verdict != conformance.VerdictUnknown || o.Reason != conformance.ReasonEnvironmentUnresolved || len(o.Occurrences) != 1 ||
			o.Occurrences[0].Source.FilePath != "compose.yaml" {
			t.Fatalf("obligation = %+v, want unknown with the reason environment-unresolved", o)
		}

		// Explanation and provenance: the finding names the environment and the reason.
		finding := findingWith(t, unresolved.run, "CO-ENV-002", func(*conformance.Finding) bool { return true })
		if finding.Reason != conformance.ReasonEnvironmentUnresolved || finding.Environment != "production" || finding.Location == nil || finding.Location.Path != "compose.yaml" {
			t.Fatalf("finding = %+v", finding)
		}

		requireProvenance(t, run)
	})

	t.Run("A05_merged_container_occurrences", func(t *testing.T) {
		spec := &foundationSpec{
			index: true,
			bindings: foundationDefaultBindings + workloadBinding("bind-api", "payments-api", "production", "api", "") +
				"  - {id: bind-api-image, architectureRef: 'calm:node:legacy-api', match: {any: [{kind: image, repository: registry.example.com/payments/api}]}}\n",
			policies: typedRule("CO-VER-001", "payments-api", "required", "uses-image", "from-workload", "", "      scope: {environments: [production]}\n    evidence: {completeness: complete}\n") +
				forbidRule("CO-IMG-001", "payments-api", "legacy-api", "uses-image"),
		}
		f := foundationRun(t, spec)
		run := f.run

		// Result: the Deployment has `app` at :1.2.0 and `sidecar` at :1.3.0. CBM keeps one USAGE edge (sidecar), so the
		// cross-check records the lost `app` occurrence, the predicate is partial, and the fact is possibly lossy.
		production := observation.CoverageScope{Environment: "production"}
		versions := predicateRecord(t, run, observation.FamilyContainerOccurrenceVersions, production)

		if versions.Status != observation.CoveragePartial || !slices.ContainsFunc(gapsOf(versions, observation.LossMergedOccurrences), func(g observation.CoverageGap) bool {
			return g.Container == "app" && g.Path == infraProdDeploy && g.Workload == "Deployment/api"
		}) {
			t.Fatalf("container-occurrence-versions = %+v", versions)
		}

		var merged *observation.EvidenceEdge

		for _, e := range evidenceEdges(run, observation.KindUsesImage) {
			if e.Environment == "production" && e.Occurrence == "sidecar" && e.Properties["image"] == "registry.example.com/payments/api:1.3.0" {
				merged = e
			}
		}

		if merged == nil || !merged.PossiblyLossy || !slices.Contains(merged.LossModes, observation.LossMergedOccurrences) {
			t.Fatalf("merged fact = %+v, want possibly lossy with the loss mode %s", merged, observation.LossMergedOccurrences)
		}

		version := obligationFor(t, ruleOf(t, run, "CO-VER-001"), "payments-api", "production")
		if version.Verdict != conformance.VerdictUnknown || version.Reason != conformance.ReasonCoverageIncomplete || !strings.Contains(version.Detail, observation.LossMergedOccurrences) {
			t.Fatalf("version-completeness obligation = %+v, want unknown naming %s", version, observation.LossMergedOccurrences)
		}

		// Explanation: the evidence path of the finding shows the lossy marking and the profile limit.
		finding := findingWith(t, run, "CO-IMG-001", func(*conformance.Finding) bool { return true })

		explain := f.cli(t, "explain", "CO-IMG-001", "--finding", finding.Fingerprint)
		requireEqual(t, "explain exit code", explain.code, 0)
		requireContains(t, "explain", explain.stdout, "possibly lossy: the provider may have merged this fact with others",
			"profile loss mode "+observation.LossMergedOccurrences+" (cbm-0.12.1)", "occurrence: sidecar", "image: registry.example.com/payments/api:1.3.0", "environment: production")

		// The same limit is in the evidence export, so a reader of the bundle sees the lossy fact.
		export := f.cli(t, "export", "bundle", "--include-graph")
		requireEqual(t, "export exit code", export.code, 0)

		extract := t.TempDir()
		if _, err := bundle.Open(filepath.Join(f.dir, "build", "concordia", "conformance.tar.gz"), extract); err != nil {
			t.Fatalf("open the bundle: %v", err)
		}

		var graph struct {
			Edges []struct {
				Kind       string `json:"kind"`
				Occurrence string `json:"occurrence"`
				Identity   struct {
					Image         string   `json:"image"`
					LossModes     []string `json:"lossModes"`
					PossiblyLossy bool     `json:"possiblyLossy"`
				} `json:"identity"`
			} `json:"edges"`
		}

		if err := json.Unmarshal(mustRead(t, filepath.Join(extract, "graph", "evidence.json")), &graph); err != nil {
			t.Fatal(err)
		}

		exported := false

		for _, e := range graph.Edges {
			if e.Kind == "uses-image" && e.Occurrence == "sidecar" && e.Identity.PossiblyLossy && slices.Contains(e.Identity.LossModes, observation.LossMergedOccurrences) &&
				e.Identity.Image == "registry.example.com/payments/api:1.3.0" {
				exported = true
			}
		}

		if !exported {
			t.Error("graph/evidence.json has no possibly lossy sidecar fact with its image reference")
		}

		// Provenance: the tag is the source reference only (a mutable reference), the environment, and the profile.
		ref, err := observation.ParseImageRef(merged.Properties["image"])
		if err != nil || ref.Tag != "1.3.0" || ref.Digest != "" || !ref.Mutable() {
			t.Errorf("image reference = %+v, %v, want the tag 1.3.0 as a mutable reference", ref, err)
		}

		requireProvenance(t, run)
	})

	t.Run("A06_deployment_action_is_not_placement", func(t *testing.T) {
		placement := strings.Replace(foundationArchitectureText, `"relationships": []`,
			`"relationships": [{"unique-id": "api-in-namespace", "relationship-type": {"deployed-in": {"container": "payments-namespace", "nodes": ["payments-api"]}}}]`, 1)
		placement = strings.Replace(placement, `{"unique-id": "payments-db"`,
			`{"unique-id": "payments-namespace", "node-type": "system", "name": "Payments namespace", "description": "The production namespace."},
    {"unique-id": "payments-db"`, 1)

		spec := &foundationSpec{
			index: true,
			files: map[string]string{"architecture/infra.calm.json": placement},
			bindings: foundationDefaultBindings + workloadBinding("bind-api", "payments-api", "production", "api", "") +
				"  - {id: bind-namespace, architectureRef: 'calm:node:payments-namespace', match: {any: [{kind: infraResource, environment: production, address: core/Namespace/_/payments}]}}\n",
			policies: foundationDefaultPolicies + typedRule("CO-PLC-001", "payments-api", "required", "deploys-artifact", "from-workload", "payments-namespace", "      scope: {environments: [production]}\n") +
				forbidRule("CO-CFG-001", "prod-manifests", "ci-scripts",
					"configures-env-access, configures-env-set, configures-env-read, configures-helm-values, references-secret, references-configuration"),
		}
		f := foundationRun(t, spec)
		run := f.run

		// Result: native DEPLOYS edges are `deploys-artifact` from a script or a chart to an artifact. `kubectl apply -f`
		// ends at the file, never at the resources in it, and no placement fact exists.
		deploys := evidenceEdges(run, observation.KindDeploysArtifact)
		requireEqual(t, "deploys-artifact facts", len(deploys), 3)

		for _, e := range deploys {
			from, to := run.Evidence.Nodes[e.From], run.Evidence.Nodes[e.To]
			if e.NativeKind != "DEPLOYS" || (from.Label != "Module" && from.Label != "Chart") || e.Properties["via"] == "" {
				t.Errorf("deploys-artifact fact %+v from %s", e, from.Label)
			}

			if e.Properties["via"] == "kubectl" && to.Label != "File" {
				t.Errorf("the kubectl target is a %s, want the applied file", to.Label)
			}
		}

		for _, e := range run.Evidence.Edges {
			if strings.Contains(string(e.Kind), "placement") || strings.Contains(string(e.Kind), "deployed-in") {
				t.Errorf("placement fact %+v from a deployment action", e)
			}
		}

		// A placement obligation with only the deployment action does not pass; the result names the limit of the action.
		o := obligationFor(t, ruleOf(t, run, "CO-PLC-001"), "payments-api", "production")
		if o.Verdict == conformance.VerdictPass || o.Placement == nil || o.Placement.Container != "payments-namespace" || !strings.Contains(o.Detail, "deploy-target-is-file") {
			t.Fatalf("placement obligation = %+v, want a result that is not pass and names deploy-target-is-file", o)
		}

		// Secret and ConfigMap differ by the target kind. The reference mode stays apart from the endpoint kind.
		secret, config := evidenceEdges(run, observation.KindReferencesSecret), evidenceEdges(run, observation.KindReferencesConfiguration)
		if len(secret) != 1 || len(config) != 1 {
			t.Fatalf("references-secret %d, references-configuration %d, want one each", len(secret), len(config))
		}

		if secret[0].Facets["referenceMode"] != "env" || secret[0].Facets["targetKind"] != "Secret" ||
			config[0].Facets["referenceMode"] != "envFrom" || config[0].Facets["targetKind"] != "ConfigMap" {
			t.Errorf("facets: secret %v, configuration %v", secret[0].Facets, config[0].Facets)
		}

		// CONFIGURES strategies give four kinds. env_set keeps scope, container, and source, and no fact has a value.
		kinds := map[observation.CanonicalEdgeKind]int{}

		for _, kind := range []observation.CanonicalEdgeKind{
			observation.KindConfiguresEnvAccess, observation.KindConfiguresEnvSet, observation.KindConfiguresEnvRead, observation.KindConfiguresHelmValues,
		} {
			kinds[kind] = len(evidenceEdges(run, kind))
			if kinds[kind] == 0 {
				t.Errorf("no %s fact", kind)
			}
		}

		for _, e := range evidenceEdges(run, observation.KindConfiguresEnvSet) {
			if e.Properties["scope"] == "" || e.Properties["source"] == "" || e.Properties["strategy"] != "env_set" {
				t.Errorf("env_set fact %v lacks scope, source, or strategy", e.Properties)
			}

			for name := range e.Properties {
				if !slices.Contains([]string{"scope", "container", "source", "strategy", "line"}, name) {
					t.Errorf("env_set fact has the property %s", name)
				}
			}
		}

		// Explanation and provenance: the run records the profile, and the deploys finding keeps the environment.
		finding := findingWith(t, run, "CO-DEP-001", func(f *conformance.Finding) bool { return f.Kind == conformance.KindForbiddenDependency })
		explain := f.cli(t, "explain", "CO-DEP-001", "--finding", finding.Fingerprint)
		requireContains(t, "explain", explain.stdout, "edge key: deploys-artifact|", "environment: production")
		requireProvenance(t, run)
	})

	t.Run("A07_negation_of_an_unknown_property", func(t *testing.T) {
		spec := &foundationSpec{
			index:    true,
			bindings: foundationDefaultBindings + workloadBinding("bind-api", "payments-api", "production", "api", ""),
			policies: typedRule("CO-SEL-001", "payments-api", "required", "uses-image", "from-workload", "", "      scope: {environments: [production]}\n      applies: {not: {protocol: https}}\n") +
				typedRule("CO-SEL-003", "payments-api", "required", "uses-image", "from-workload", "", "      scope: {environments: [production]}\n      applies: {trustContext: internet}\n"),
		}
		f := foundationRun(t, spec)
		run := f.run

		// Result: no CBM fact has a protocol, so `not` of the property stays unknown: the rule does not match by
		// accident and the obligation is unknown with the reason, never pass and never not-applicable.
		negated := obligationFor(t, ruleOf(t, run, "CO-SEL-001"), "payments-api", "production")
		if negated.Verdict != conformance.VerdictUnknown || negated.Reason != conformance.ReasonSelectorPropertyUnknown || !strings.Contains(negated.Detail, "protocol") ||
			len(negated.Occurrences) != 1 || negated.Occurrences[0].Class != conformance.OccurrenceMatching {
			t.Fatalf("negated obligation = %+v", negated)
		}

		// A dimension without a source in this release is unknown, not false.
		trust := obligationFor(t, ruleOf(t, run, "CO-SEL-003"), "payments-api", "production")
		if trust.Verdict != conformance.VerdictUnknown || trust.Reason != conformance.ReasonSelectorPropertyUnknown || !strings.Contains(trust.Detail, "trustContext") {
			t.Errorf("trustContext obligation = %+v", trust)
		}

		// Explanation: the finding shows the reason and the next action; the explain output names the unknown property.
		finding := findingWith(t, run, "CO-SEL-001", func(*conformance.Finding) bool { return true })
		if finding.Kind != conformance.KindObligationUnknown || finding.Reason != conformance.ReasonSelectorPropertyUnknown || finding.NextAction != "supply evidence" ||
			!strings.Contains(finding.Observed, "protocol") {
			t.Fatalf("finding = %+v", finding)
		}

		explain := f.cli(t, "explain", "CO-SEL-001", "--finding", finding.Fingerprint)
		requireEqual(t, "explain exit code", explain.code, 0)
		requireContains(t, "explain", explain.stdout, "obligation-unknown")

		// Provenance: the occurrence is the sidecar fact of the production manifest, with its source location.
		if loc := negated.Occurrences[0].Location; loc == nil || loc.Path != infraProdDeploy || negated.Occurrences[0].Environment != "production" || negated.Occurrences[0].Occurrence != "sidecar" {
			t.Errorf("occurrence = %+v", negated.Occurrences[0])
		}

		requireProvenance(t, run)
	})

	t.Run("A08_competing_declarations_and_conflicting_bindings", func(t *testing.T) {
		// Two manifests of production declare the Deployment `reports`: k8s/prod/reports.yaml and k8s/overrides/reports.yaml.
		spec := &foundationSpec{
			index:    true,
			bindings: foundationDefaultBindings + workloadBinding("bind-reports", "payments-reports", "production", "reports", "payments"),
			policies: typedRule("CO-CNF-001", "payments-reports", "required", "uses-image", "from-workload", "", "      scope: {environments: [production]}\n"),
		}
		f := foundationRun(t, spec)
		run := f.run

		// Result: one conflict diagnostic names the identity and both locations; the obligation is unknown with no winner.
		var conflict *diag.Diagnostic

		for i := range run.Diagnostics {
			if run.Diagnostics[i].Code == diag.OBS021 && strings.Contains(run.Diagnostics[i].Message, "payments/reports") {
				conflict = &run.Diagnostics[i]
			}
		}

		if conflict == nil {
			t.Fatalf("no CO-OBS-021 for the reports Deployment: %+v", run.Diagnostics)
		}

		requireContains(t, "conflict diagnostic", conflict.Message, "k8s:production/prod-eu/apps/Deployment/payments/reports", "k8s/prod/reports.yaml", "k8s/overrides/reports.yaml")

		if run.Observability.Conflicts == 0 {
			t.Error("the conflicts counter is 0")
		}

		o := obligationFor(t, ruleOf(t, run, "CO-CNF-001"), "payments-reports", "production")
		if o.Verdict != conformance.VerdictUnknown || o.Reason != conformance.ReasonEvidenceConflict || len(o.Occurrences) != 2 {
			t.Fatalf("obligation = %+v, want unknown with evidence-conflict for both declarations", o)
		}

		places := make([]string, 0, len(o.Occurrences))

		for _, occ := range o.Occurrences {
			if occ.Class != conformance.OccurrenceConflict || occ.Result != conformance.VerdictUnknown || len(occ.Sources) != 2 {
				t.Errorf("occurrence = %+v", occ)
			}

			places = append(places, occ.Location.Path)
		}

		slices.Sort(places)
		requireEqual(t, "declaration locations", strings.Join(places, ","), "k8s/overrides/reports.yaml,k8s/prod/reports.yaml")

		// Explanation: the finding and explain name the reason and both locations.
		finding := findingWith(t, run, "CO-CNF-001", func(*conformance.Finding) bool { return true })
		if finding.Reason != conformance.ReasonEvidenceConflict || finding.Environment != "production" {
			t.Fatalf("finding = %+v", finding)
		}

		check := f.cli(t, "check", "--no-color")
		requireContains(t, "check stderr", check.stderr, "CO-OBS-021", "k8s/overrides/reports.yaml:1 and k8s/prod/reports.yaml:1")

		explain := f.cli(t, "explain", "CO-CNF-001", "--finding", finding.Fingerprint)
		requireContains(t, "explain", explain.stdout, "obligation-unknown")

		// The same name in two environments is no conflict: the Deployment `api` and the Kustomize base keep one identity each.
		for i := range run.Diagnostics {
			if run.Diagnostics[i].Code == diag.OBS021 && strings.Contains(run.Diagnostics[i].Message, "/api") {
				t.Errorf("conflict for a resource of two environments: %s", run.Diagnostics[i].Message)
			}
		}

		// Two non-exclusive bindings at one tier map the API image to two CALM nodes: no binding wins by order.
		bindings := &foundationSpec{
			index: true,
			bindings: foundationDefaultBindings +
				"  - {id: bind-image-a, architectureRef: 'calm:node:payments-api', exclusive: false, match: {any: [{kind: image, repository: registry.example.com/payments/api}]}}\n" +
				"  - {id: bind-image-b, architectureRef: 'calm:node:legacy-api', exclusive: false, match: {any: [{kind: image, repository: registry.example.com/payments/api}]}}\n",
			policies: typedRule("CO-CNF-002", "payments-api", "required", "uses-image", "to-subject", "", "      scope: {environments: [production]}\n"),
		}
		g := foundationRun(t, bindings)

		ambiguous := obligationFor(t, ruleOf(t, g.run, "CO-CNF-002"), "payments-api", "production")
		if ambiguous.Verdict != conformance.VerdictUnknown || ambiguous.Reason != conformance.ReasonAmbiguousBinding || len(ambiguous.Occurrences) != 1 {
			t.Fatalf("ambiguous obligation = %+v", ambiguous)
		}

		ids := make([]string, 0, len(ambiguous.Occurrences[0].Bindings))
		for _, ref := range ambiguous.Occurrences[0].Bindings {
			ids = append(ids, ref.ID)
		}

		slices.Sort(ids)
		requireEqual(t, "binding identifiers", strings.Join(ids, ","), "bind-image-a,bind-image-b")

		if !hasDiagnostic(g.run, diag.BND009) || g.run.Observability.Conflicts == 0 {
			t.Errorf("diagnostics lack CO-BND-009 or the conflicts counter is 0: %+v", g.run.Observability)
		}

		finding = findingWith(t, g.run, "CO-CNF-002", func(*conformance.Finding) bool { return true })
		if finding.Reason != conformance.ReasonAmbiguousBinding || finding.NextAction != "resolve binding" {
			t.Fatalf("finding = %+v", finding)
		}

		requireProvenance(t, run)
		requireProvenance(t, g.run)
	})

	t.Run("A09_provider_fails_on_one_scope", func(t *testing.T) {
		// A second workspace project whose query the provider fails; the first project is complete.
		spec := &foundationSpec{
			index:     true,
			workspace: strings.Replace(foundationWorkspace, "      include: [\"**\"]\n", "      include: [\"**\"]\n    - {name: ghost-docs, root: docs}\n", 1),
			bindings:  foundationDefaultBindings + sourceRootBinding("bind-charts", "helm-charts", "charts/**") + sourceRootBinding("bind-ghost", "app-code", "@ghost-docs/**"),
			policies: foundationDefaultPolicies + forbidRule("CO-GHOST-001", "app-code", "prod-manifests", "deploys") +
				forbidRule("CO-GHOST-002", "app-code", "ci-scripts", "IMPORTS"),
		}
		f := newFoundation(t, spec)
		f.client = failingProjectClient{Client: f.client, project: "ghost-docs"}

		run, err := f.check(t, spec)
		if err != nil {
			t.Fatalf("check failed: %v", err)
		}

		// Result: obligations of the failed project are unknown with a coverage reason; the other project keeps its results.
		dependent := ruleOf(t, run, "CO-GHOST-001")
		if dependent.Verdict != conformance.VerdictUnknown || !slices.ContainsFunc(dependent.Reasons, func(r conformance.Reason) bool {
			return r.Code == diag.OBS018 && strings.Contains(r.Message, "ghost-docs")
		}) {
			t.Fatalf("dependent rule = %s %v", dependent.Verdict, dependent.Reasons)
		}

		requireEqual(t, "CO-DEP-001 verdict", ruleOf(t, run, "CO-DEP-001").Verdict, conformance.VerdictViolation)

		if independent := ruleOf(t, run, "CO-DEP-002"); independent.Verdict != conformance.VerdictPass {
			t.Fatalf("CO-DEP-002 = %s %v, want pass", independent.Verdict, independent.Reasons)
		}

		if len(run.Coverage.ScopeFailures) != 1 || run.Coverage.ScopeFailures[0].Project != "ghost-docs" || run.Observability.StageFailures != 1 || !hasDiagnostic(run, diag.OBS018) {
			t.Fatalf("scope failures %+v, counters %+v", run.Coverage.ScopeFailures, run.Observability)
		}

		// A mandatory dependent obligation blocks: with the live violation left out, exit code 4. A failure of the whole
		// provider connection (the recording knows no `ghost-docs`) gives exit code 3.
		blocked := newFoundation(t, &foundationSpec{
			index: true, workspace: spec.workspace, bindings: spec.bindings,
			policies: forbidRule("CO-DEP-002", "prod-manifests", "ci-scripts", "DEPLOYS") + forbidRule("CO-GHOST-001", "app-code", "prod-manifests", "deploys"),
		})
		blocked.client = failingProjectClient{Client: blocked.client, project: "ghost-docs"}

		result := blocked.cli(t, "check", "--no-color")
		requireEqual(t, "exit code of the mandatory dependent obligation", result.code, 4)
		requireContains(t, "check stdout", result.stdout, "UNKNOWN CO-GHOST-001", "CO-OBS-018", "ghost-docs")

		connection := newFoundation(t, spec)
		requireEqual(t, "exit code of the connection failure", connection.cli(t, "check").code, 3)

		// Explanation: the unknown names the project; coverage shows the failed scope. Provenance: the failed scope is
		// named in results.json and the other project keeps its graph generation and profile.
		explain := blocked.cli(t, "explain", "CO-GHOST-001")
		requireContains(t, "explain", explain.stdout, "CO-OBS-018", "ghost-docs")

		results := blocked.resultsOf(t)
		failures, _ := jsonAt(t, results, "coverage", "scopeFailures").([]any)

		if len(failures) != 1 || jsonAt(t, failures[0], "project") != "ghost-docs" || jsonAt(t, results, "observability", "stageFailures") != float64(1) {
			t.Errorf("results.json scope failures = %v, counters = %v", failures, jsonAt(t, results, "observability"))
		}

		if !slices.ContainsFunc(run.Run.Projects, func(p conformance.RunProject) bool {
			return p.Name == infraProject && strings.HasPrefix(p.GraphGeneration, "cbm:"+infraProject+":")
		}) {
			t.Errorf("projects = %+v, want the complete project with its graph generation", run.Run.Projects)
		}

		requireProvenance(t, run)
	})

	t.Run("A20_calm_1_3_decorator_fields_in_a_1_2_document", func(t *testing.T) {
		decorator := string(mustRead(t, "../fixtures/calm/decorator-1.3-fields-under-1.2.json"))

		for _, tc := range []struct {
			release string
			code    string
			exit    int
		}{
			{"1.2", diag.INT008, 2},
			{"1.3", "", -1},
			{"1.4", diag.INT003, 2},
		} {
			files := map[string]string{
				"architecture/infra.calm.json": strings.Replace(foundationArchitectureText, "release/1.2/meta/calm.json", "release/"+tc.release+"/meta/calm.json", 1),
				"decorators/d.json":            decorator,
			}
			withDecorator := strings.Replace(foundationWorkspace, "  source: architecture/infra.calm.json\n", "  source: architecture/infra.calm.json\n  decorators: [decorators/d.json]\n", 1)

			// `model validate --decorator` takes the decorator on the command line; `check` takes it from the workspace.
			validate := newFoundation(t, &foundationSpec{files: files}).cli(t, "model", "validate", "--decorator", "decorators/d.json")
			f := newFoundation(t, &foundationSpec{index: true, files: files, workspace: withDecorator})
			check := f.cli(t, "check", "--no-color")

			if tc.code == "" {
				// Result: the same fields under a 1.3 declaration are accepted; the check runs on the recording.
				if validate.code != 0 || check.code == 2 || strings.Contains(check.stderr+check.stdout, diag.INT008) {
					t.Errorf("release %s: validate %d %q, check %d %q", tc.release, validate.code, validate.stderr, check.code, check.stderr)
				}

				requireContains(t, "check stdout of release "+tc.release, check.stdout, "Conformance:")

				continue
			}

			// Result and explanation: exit code 2 and the diagnostic code in both commands.
			for name, res := range map[string]cliResult{"model validate": validate, "check": check} {
				if res.code != tc.exit || !strings.Contains(res.stdout+res.stderr, tc.code) {
					t.Errorf("release %s, %s: exit %d (want %d), output %q %q", tc.release, name, res.code, tc.exit, res.stdout, res.stderr)
				}
			}

			// No conformance result is produced from the refused document, so no results file exists.
			if _, err := os.Stat(filepath.Join(f.dir, resultsFile)); err == nil {
				t.Errorf("release %s: a results file was written from a refused document", tc.release)
			}
		}

		// Provenance: the diagnostic names the decorator file and the pointer of the field.
		f := newFoundation(t, &foundationSpec{
			files: map[string]string{"decorators/d.json": decorator},
		})
		requireContains(t, "model validate output", f.cli(t, "model", "validate", "--decorator", "decorators/d.json").stderr, "decorators/d.json")
	})

	t.Run("A38_secret_values_stay_out_of_every_output", func(t *testing.T) {
		// CO-ENV-001 has its finding at k8s/prod/deployment.yaml:23, next to the seeded `DB_PASSWORD` value; CO-DEP-001 has its
		// finding in scripts/deploy.sh, next to the seeded token.
		spec := &foundationSpec{
			index:    true,
			bindings: foundationDefaultBindings + workloadBinding("bind-api", "payments-api", "production", "api", ""),
			policies: foundationDefaultPolicies + forbidRule("CO-ENV-001", "payments-api", "prod-manifests", "configures-env-set"),
		}
		f := newFoundation(t, spec)

		outputs := map[string][]byte{}
		record := func(name string, res cliResult) {
			outputs[name+" stdout"], outputs[name+" stderr"] = []byte(res.stdout), []byte(res.stderr)
		}

		check := f.cli(t, "--verbose", "--trace", "check", "--no-color")
		requireEqual(t, "check exit code", check.code, 1)
		record("check", check)

		run, err := f.check(t, spec)
		if err != nil {
			t.Fatal(err)
		}

		var env *conformance.Finding

		for i := range run.Findings {
			if run.Findings[i].RuleID == "CO-ENV-001" && run.Findings[i].Location != nil && run.Findings[i].Location.Path == infraProdDeploy && run.Findings[i].Location.Line == 23 {
				env = &run.Findings[i]
			}
		}

		if env == nil {
			t.Fatalf("no CO-ENV-001 finding at %s:23: %+v", infraProdDeploy, findingsOf(run, "CO-ENV-001"))
		}

		// Explanation: explain --source shows the keys and the redaction marker, never the value.
		explain := f.cli(t, "--verbose", "--trace", "explain", "CO-ENV-001", "--finding", env.Fingerprint, "--source")
		requireEqual(t, "explain exit code", explain.code, 0)
		requireContains(t, "explain --source", explain.stdout, "[REDACTED:", "DB_PASSWORD", "value:")
		requireContains(t, "explain stderr", explain.stderr, "CO-REP-005")
		record("explain", explain)

		deploy := f.cli(t, "explain", "CO-DEP-001", "--finding", findingWith(t, run, "CO-DEP-001", func(f *conformance.Finding) bool {
			return f.Kind == conformance.KindForbiddenDependency
		}).Fingerprint, "--source")
		requireContains(t, "explain of the shell script", deploy.stdout, "export GITHUB_TOKEN=[REDACTED:")
		record("explain deploy", deploy)

		// Bundle: snippets and graph are redacted, the manifest records each redaction with class and place, and no value.
		export := f.cli(t, "--verbose", "--trace", "export", "bundle", "--include-graph", "--include-source-snippets", "violations")
		requireEqual(t, "export exit code", export.code, 0)
		requireContains(t, "export stderr", export.stderr, "CO-REP-005")
		record("export", export)

		extract := t.TempDir()

		manifest, err := bundle.Open(filepath.Join(f.dir, "build", "concordia", "conformance.tar.gz"), extract)
		if err != nil {
			t.Fatalf("open the bundle: %v", err)
		}

		if len(manifest.Redactions) < 2 || manifest.RedactionRules == "" {
			t.Fatalf("manifest redactions = %+v, rules %q", manifest.Redactions, manifest.RedactionRules)
		}

		for _, r := range manifest.Redactions {
			if r.Class == "" || r.Path == "" || r.Count == 0 {
				t.Errorf("redaction record %+v lacks the class, the output path, or the count", r)
			}
		}

		err = filepath.WalkDir(extract, func(p string, entry fs.DirEntry, walkErr error) error {
			if walkErr == nil && !entry.IsDir() {
				rel, _ := filepath.Rel(extract, p)
				outputs["bundle/"+filepath.ToSlash(rel)] = mustRead(t, p)
			}

			return walkErr
		})
		if err != nil {
			t.Fatal(err)
		}

		outputs["logs"] = f.logs.Bytes()
		outputs["results.json"] = mustRead(t, filepath.Join(f.dir, resultsFile))

		var seeds []struct {
			Path  string `json:"path"`
			Class string `json:"class"`
			Value string `json:"value"`
		}

		if err = json.Unmarshal(mustRead(t, infraSeedsFile), &seeds); err != nil {
			t.Fatal(err)
		}

		for name, data := range outputs {
			for _, seed := range seeds {
				for _, needle := range []string{seed.Value, base64.StdEncoding.EncodeToString([]byte(seed.Value))} {
					if bytes.Contains(data, []byte(needle)) {
						t.Errorf("%s contains the seed %s of %s", name, seed.Class, seed.Path)
					}
				}
			}
		}

		// Provenance: the redacted snippet keeps its source location and the manifest the profile of the run.
		if _, ok := outputs["bundle/snippets/"+hexOf(env.Fingerprint)+".txt"]; !ok {
			t.Errorf("no snippet for the finding %s in the bundle", env.Fingerprint)
		}

		want := shippedProfile(t)
		if manifest.Provenance.Graph.Profile.ID != want.ID || manifest.Provenance.Graph.Profile.Digest != want.Digest {
			t.Errorf("bundle provenance profile = %+v, want %+v", manifest.Provenance.Graph.Profile, want)
		}

		requireProvenance(t, run)
	})

	t.Run("A38_tampered_artifact", func(t *testing.T) {
		f := foundationRun(t, &foundationSpec{index: true})
		requireEqual(t, "export exit code", f.cli(t, "export", "bundle", "--include-graph").code, 0)

		archive := filepath.Join(f.dir, "build", "concordia", "conformance.tar.gz")

		// The digest check is no authentication: the integrity level says so.
		verify := f.cli(t, "export", "verify", archive)
		requireEqual(t, "verify exit code", verify.code, 0)
		requireContains(t, "verify", verify.stdout+verify.stderr, "content verified by digest")

		// One file of the bundle changes after the export: verify fails with exit code 2 and names the file.
		tamperArchive(t, archive, "results/results.json")

		verify = f.cli(t, "export", "verify", archive)
		requireEqual(t, "verify exit code after the change", verify.code, 2)
		requireContains(t, "verify", verify.stdout+verify.stderr, "CO-REP-004", "results/results.json")

		// Provenance: the bundle of the untouched run carries the profile and the environments that the run used.
		fresh := foundationRun(t, &foundationSpec{index: true})
		requireEqual(t, "export exit code", fresh.cli(t, "export", "bundle").code, 0)

		manifest, err := bundle.Verify(filepath.Join(fresh.dir, "build", "concordia", "conformance.tar.gz"))
		if err != nil {
			t.Fatalf("verify the untouched bundle: %v", err)
		}

		if manifest.Provenance.Graph.Profile.ID != shippedProfile(t).ID || !slices.Equal(manifest.Provenance.Environments.Selected, []string{"production", "staging", "edge"}) {
			t.Errorf("bundle provenance = %+v", manifest.Provenance)
		}
	})

	t.Run("A38_interrupted_run", func(t *testing.T) {
		f := newFoundation(t, &foundationSpec{index: true})

		// Interrupt the run while it reads the graph.
		ctx, cancel := context.WithCancel(t.Context())
		f.client = cancelingClient{Client: f.client, cancel: cancel}

		interrupted := f.cliWith(t, ctx, "check")
		requireEqual(t, "interrupted check exit code", interrupted.code, 130)

		// No manifest says `complete`, and no results exist: export and explain refuse the run with exit code 2.
		manifests, _ := filepath.Glob(filepath.Join(f.dir, ".concordia", "runs", "*", "manifest.json"))
		for _, p := range manifests {
			var probe struct {
				Status string `json:"status"`
			}

			if err := json.Unmarshal(mustRead(t, p), &probe); err != nil {
				t.Fatalf("parse %s: %v", p, err)
			}

			if probe.Status == conformance.ManifestComplete {
				t.Errorf("%s says complete after an interrupted run", p)
			}
		}

		f.client = observation.NewReplayClient(f.rec)

		for _, args := range [][]string{{"export", "bundle"}, {"explain", "CO-DEP-001"}} {
			res := f.cli(t, args...)
			if res.code != 2 {
				t.Errorf("%v: exit code %d, stderr %q, want exit 2", args, res.code, res.stderr)
			}
		}

		if _, err := os.Stat(filepath.Join(f.dir, "build", "concordia", "conformance.tar.gz")); err == nil {
			t.Error("a bundle was made for the interrupted run")
		}

		// A run whose manifest stays `incomplete` (the writer failed after the results) is refused with CO-REP-004.
		g := foundationRun(t, &foundationSpec{index: true})
		requireEqual(t, "check exit code", g.cli(t, "check").code, 1)

		paths, _ := filepath.Glob(filepath.Join(g.dir, ".concordia", "runs", "*", "manifest.json"))
		if len(paths) != 1 || !strings.Contains(string(mustRead(t, paths[0])), `"status": "complete"`) {
			t.Fatalf("run manifests = %v, want one complete manifest", paths)
		}

		incomplete := strings.Replace(string(mustRead(t, paths[0])), `"status": "complete"`, `"status": "incomplete"`, 1)
		if err := os.WriteFile(paths[0], []byte(incomplete), 0o600); err != nil {
			t.Fatal(err)
		}

		for _, args := range [][]string{{"export", "bundle"}, {"explain", "CO-DEP-001"}} {
			res := g.cli(t, args...)
			if res.code != 2 || !strings.Contains(res.stderr, "CO-REP-004") {
				t.Errorf("%v: exit code %d, stderr %q, want exit 2 with CO-REP-004", args, res.code, res.stderr)
			}
		}
	})

	t.Run("A38_path_traversal_in_a_location", func(t *testing.T) {
		// The script is bound by its qualified name: its file path is the one the provider gives.
		spec := &foundationSpec{
			index: true,
			bindings: sourceRootBinding("bind-prod-manifests", "prod-manifests", "k8s/prod/**") +
				"  - {id: bind-ci-scripts, architectureRef: 'calm:node:ci-scripts', match: {any: [{qualifiedName: scripts.deploy}]}}\n",
		}
		f := newFoundation(t, spec)

		// The provider names `../sentinel.txt` as the file of the deploy script. The file exists outside the workspace.
		const sentinel = "OUTSIDE-THE-ROOT-SENTINEL"
		if err := os.WriteFile(filepath.Join(filepath.Dir(f.dir), "sentinel.txt"), []byte("line 1\nline 2\nline 3\nline 4\nline 5\nline 6 "+sentinel+"\nline 7\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		calls := make(map[string]json.RawMessage, len(f.rec.Calls))
		for key, body := range f.rec.Calls {
			calls[key] = bytes.ReplaceAll(body, []byte("scripts/deploy.sh"), []byte("../sentinel.txt"))
		}

		f.rec.Calls = calls
		f.client = observation.NewReplayClient(f.rec)

		check := f.cli(t, "check", "--no-color")
		if check.code != 1 {
			t.Fatalf("check exit code %d, stderr %q", check.code, check.stderr)
		}

		run, err := f.check(t, spec)
		if err != nil {
			t.Fatal(err)
		}

		finding := findingWith(t, run, "CO-DEP-001", func(f *conformance.Finding) bool { return f.Kind == conformance.KindForbiddenDependency })
		if finding.Location == nil || finding.Location.Path != "../sentinel.txt" {
			t.Fatalf("finding location = %+v, want ../sentinel.txt", finding.Location)
		}

		// No content of that file is read or stored, and a diagnostic names the refused path.
		explain := f.cli(t, "explain", "CO-DEP-001", "--finding", finding.Fingerprint, "--source")
		requireEqual(t, "explain exit code", explain.code, 0)
		requireContains(t, "explain stderr", explain.stderr, "CO-REP-006", "../sentinel.txt")

		export := f.cli(t, "export", "bundle", "--include-graph", "--include-source-snippets", "violations")
		requireEqual(t, "export exit code", export.code, 0)
		requireContains(t, "export stderr", export.stderr, "CO-REP-006", "../sentinel.txt")

		extract := t.TempDir()
		if _, err = bundle.Open(filepath.Join(f.dir, "build", "concordia", "conformance.tar.gz"), extract); err != nil {
			t.Fatal(err)
		}

		outputs := map[string]string{"explain stdout": explain.stdout, "explain stderr": explain.stderr, "export stdout": export.stdout, "export stderr": export.stderr}

		err = filepath.WalkDir(extract, func(p string, entry fs.DirEntry, walkErr error) error {
			if walkErr == nil && !entry.IsDir() {
				outputs[p] = string(mustRead(t, p))
			}

			return walkErr
		})
		if err != nil {
			t.Fatal(err)
		}

		for name, text := range outputs {
			if strings.Contains(text, sentinel) {
				t.Errorf("%s holds the content of the file outside the roots", name)
			}
		}

		requireProvenance(t, run)
	})

	t.Run("A39_module_import_and_code_import", func(t *testing.T) {
		// Infra recording: a Terraform module import and a Kustomize import are `includes-infrastructure`. A rule with the
		// IMPORTS token examines code imports only; a rule with the includes-infrastructure token sees the module import.
		spec := &foundationSpec{
			index: true,
			bindings: foundationDefaultBindings + sourceRootBinding("bind-tf-root", "terraform-root", "terraform/*.tf") +
				sourceRootBinding("bind-tf-module", "terraform-module", "terraform/modules/**"),
			policies: forbidRule("CO-IMP-001", "terraform-root", "terraform-module", "IMPORTS") +
				forbidRule("CO-IMP-002", "terraform-root", "terraform-module", "includes-infrastructure"),
		}
		f := foundationRun(t, spec)
		run := f.run

		requireEqual(t, "CO-IMP-001 verdict", ruleOf(t, run, "CO-IMP-001").Verdict, conformance.VerdictPass)
		requireEqual(t, "CO-IMP-002 verdict", ruleOf(t, run, "CO-IMP-002").Verdict, conformance.VerdictViolation)

		if kinds := ruleOf(t, run, "CO-IMP-001").Plan.Traversal.EdgeKinds; !slices.Equal(kinds, []observation.CanonicalEdgeKind{observation.KindImports}) {
			t.Errorf("the IMPORTS token traverses %v, want [imports]", kinds)
		}

		imports := evidenceEdges(run, observation.KindIncludesInfrastructure)
		if len(imports) < 5 || len(evidenceEdges(run, observation.KindImports)) != 0 {
			t.Fatalf("includes-infrastructure facts %d, imports facts %d", len(imports), len(evidenceEdges(run, observation.KindImports)))
		}

		module := findingWith(t, run, "CO-IMP-002", func(*conformance.Finding) bool { return true })
		if module.Edge == nil || module.Edge.Kind != observation.KindIncludesInfrastructure || module.Edge.NativeKind != "IMPORTS" ||
			module.Location == nil || module.Location.Path != "terraform/main.tf" || module.Location.Line != 11 {
			t.Fatalf("module finding = %+v", module)
		}

		explain := f.cli(t, "explain", "CO-IMP-002", "--finding", module.Fingerprint)
		requireContains(t, "explain", explain.stdout, "IMPORTS", "terraform/main.tf:11")
		requireProvenance(t, run)

		// Commerce recording: a TypeScript import stays `imports` and the IMPORTS token examines it.
		c := newCommerce(t)
		commerceRun := c.mustCheck(t, func(o *conformance.CheckOptions) { o.KeepEvidence = true })

		if len(evidenceEdges(commerceRun, observation.KindImports)) == 0 || len(evidenceEdges(commerceRun, observation.KindIncludesInfrastructure)) != 0 {
			t.Fatalf("commerce: imports %d, includes-infrastructure %d", len(evidenceEdges(commerceRun, observation.KindImports)), len(evidenceEdges(commerceRun, observation.KindIncludesInfrastructure)))
		}

		var code *conformance.Finding

		for i, f := range findingsOf(commerceRun, "CO-BND-001") {
			if f.Edge != nil && f.Edge.Kind == observation.KindImports {
				code = &findingsOf(commerceRun, "CO-BND-001")[i]
			}
		}

		if code == nil || code.Edge.NativeKind != "IMPORTS" || code.Location == nil || code.Location.Path != billingFile {
			t.Fatalf("code import finding = %+v", code)
		}

		for _, e := range commerceRun.Evidence.Edges {
			if e.NormalizationVersion != observation.NormalizationVersion {
				t.Fatalf("commerce fact %s has the normalization version %q", e.ID, e.NormalizationVersion)
			}
		}
	})

	t.Run("A41_cbm_0_12_1_has_a_tested_profile", func(t *testing.T) {
		f := newFoundation(t, &foundationSpec{index: true})
		f.client = projectListClient{Client: f.client}

		// Doctor: the cbm capability is available, names the profile, and shows no version warning.
		doctor := f.cli(t, "doctor", "--format", "json")
		requireEqual(t, "doctor exit code", doctor.code, 0)

		var report struct {
			Capabilities []struct {
				Name    string `json:"name"`
				Status  string `json:"status"`
				Version string `json:"version"`
				Detail  string `json:"detail"`
				Profile struct {
					ID       string `json:"id"`
					Digest   string `json:"digest"`
					Families []struct {
						Family    string   `json:"family"`
						Level     string   `json:"level"`
						LossModes []string `json:"lossModes"`
					} `json:"families"`
				} `json:"profile"`
			} `json:"capabilities"`
		}

		if err := json.Unmarshal([]byte(doctor.stdout), &report); err != nil {
			t.Fatalf("parse the doctor output: %v\n%s", err, doctor.stdout)
		}

		want := shippedProfile(t)
		found := false

		for _, c := range report.Capabilities {
			if c.Name != "cbm" {
				continue
			}

			found = true

			if c.Status != "available" || c.Version != "0.12.1" || c.Detail != "profile "+want.ID || c.Profile.ID != want.ID || c.Profile.Digest != want.Digest {
				t.Errorf("cbm capability = %+v, want available with %+v", c, want)
			}

			selects := false

			for _, fam := range c.Profile.Families {
				if fam.Family == "selects-workload" && slices.Contains(fam.LossModes, "selector-join-directory-scoped") {
					selects = true
				}
			}

			if !selects {
				t.Error("the profile does not list selects-workload with the loss mode selector-join-directory-scoped")
			}
		}

		if !found || strings.Contains(doctor.stderr, diag.OBS004) {
			t.Errorf("doctor found the cbm capability: %v, stderr %q", found, doctor.stderr)
		}

		// The run manifest of a following check records the same profile.
		check := f.cli(t, "check")
		requireEqual(t, "check exit code", check.code, 1)

		manifests, err := filepath.Glob(filepath.Join(f.dir, ".concordia", "runs", "*", "manifest.json"))
		if err != nil || len(manifests) != 1 {
			t.Fatalf("run manifests = %v, %v", manifests, err)
		}

		var manifest struct {
			Status    string `json:"status"`
			Toolchain struct {
				Cbm struct {
					ExecutableVersion string `json:"executableVersion"`
					AdapterSchema     string `json:"adapterSchema"`
					Profile           struct {
						ID     string `json:"id"`
						Digest string `json:"digest"`
					} `json:"profile"`
				} `json:"cbm"`
			} `json:"toolchain"`
		}

		if err = json.Unmarshal(mustRead(t, manifests[0]), &manifest); err != nil {
			t.Fatal(err)
		}

		cbm := manifest.Toolchain.Cbm
		if manifest.Status != conformance.ManifestComplete || cbm.Profile.ID != want.ID || cbm.Profile.Digest != want.Digest || cbm.ExecutableVersion != "0.12.1" || cbm.AdapterSchema != workspace.AdapterSchema {
			t.Errorf("run manifest = %+v, want a complete run with %+v", manifest, want)
		}

		results := f.resultsOf(t)
		if jsonAt(t, results, "toolchain", "cbm", "profile", "digest") != want.Digest {
			t.Errorf("results.json toolchain = %v", jsonAt(t, results, "toolchain"))
		}
	})

	t.Run("A43_a_rule_uses_deploys", func(t *testing.T) {
		// The chart is bound, as in deploys_rule_test.go, so that no binding gap is left on the chart.
		f := foundationRun(t, &foundationSpec{index: true, bindings: foundationDefaultBindings + sourceRootBinding("bind-charts", "helm-charts", "charts/**")})
		run := f.run

		// Result: the rules with the `deploys` token (deploys, DEPLOYS) give violation and pass, never unknown for provider
		// support, and CO-POL-009 is not raised.
		requireEqual(t, "CO-DEP-001 verdict", ruleOf(t, run, "CO-DEP-001").Verdict, conformance.VerdictViolation)
		requireEqual(t, "CO-DEP-002 verdict", ruleOf(t, run, "CO-DEP-002").Verdict, conformance.VerdictPass)

		if hasDiagnostic(run, diag.POL009) {
			t.Error("CO-POL-009 was raised")
		}

		for i := range run.Rules {
			for _, reason := range run.Rules[i].Reasons {
				if reason.Code == diag.POL009 {
					t.Errorf("%s has the reason CO-POL-009", run.Rules[i].RuleID)
				}
			}
		}

		finding := findingWith(t, run, "CO-DEP-001", func(f *conformance.Finding) bool { return f.Kind == conformance.KindForbiddenDependency })
		if finding.Edge == nil || finding.Edge.Kind != observation.KindDeploysArtifact || finding.Edge.NativeKind != "DEPLOYS" || finding.Location == nil ||
			finding.Location.Path != "scripts/deploy.sh" || finding.Location.Line != 6 {
			t.Fatalf("finding = %+v", finding)
		}

		// Explanation: explain shows the deploys-artifact fact with its identity, environment, and source line.
		explain := f.cli(t, "explain", "CO-DEP-001", "--finding", finding.Fingerprint)
		requireEqual(t, "explain exit code", explain.code, 0)
		requireContains(t, "explain", explain.stdout, "-[DEPLOYS syntactic, cbm:DEPLOYS]-> at scripts/deploy.sh:6", "edge key: deploys-artifact|", "environment: production")

		// Provenance: the fact carries the environment, the normalization version, and the profile of the run.
		var step *conformance.StepEdge

		for i := range finding.EvidencePath {
			if edge := finding.EvidencePath[i].Edge; edge != nil {
				step = edge
			}
		}

		if step == nil || step.Identity == nil || step.Identity.Environment != "production" || step.Kind != observation.KindDeploysArtifact {
			t.Fatalf("evidence step = %+v", step)
		}

		requireProvenance(t, run)
	})
}

type projectListClient struct{ observation.Client }

// Call answers `list_projects` with the project of the recording, as the indexed CBM does; the recording has no such call.
func (c projectListClient) Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	if tool == "list_projects" {
		return json.RawMessage(`{"projects":[{"name":"` + infraProject + `"}],"has_more":false}`), nil
	}

	return c.Client.Call(ctx, tool, args)
}

// cancelingClient cancels the context of the run when the graph is queried, as an interrupt does.
type cancelingClient struct {
	observation.Client
	cancel context.CancelFunc
}

func (c cancelingClient) Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	if tool == "query_graph" {
		c.cancel()
	}

	return c.Client.Call(ctx, tool, args)
}

func hexOf(fingerprint string) string { return strings.TrimPrefix(fingerprint, "sha256:") }

// tamperArchive changes one file of a bundle without updating its manifest.
func tamperArchive(t *testing.T, archive, member string) {
	t.Helper()

	gz, err := gzip.NewReader(bytes.NewReader(mustRead(t, archive)))
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	writer := gzip.NewWriter(&out)
	tw := tar.NewWriter(writer)

	for tr := tar.NewReader(gz); ; {
		hdr, nextErr := tr.Next()
		if nextErr != nil {
			break
		}

		data, readErr := io.ReadAll(tr)
		if readErr != nil {
			t.Fatal(readErr)
		}

		if hdr.Name == member {
			data = append(data, ' ')
		}

		if err = tw.WriteHeader(&tar.Header{Name: hdr.Name, Mode: 0o644, Size: int64(len(data)), ModTime: time.Unix(0, 0)}); err != nil {
			t.Fatal(err)
		}

		if _, err = tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}

	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(archive, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// failingProjectClient fails the graph query of one project, as CBM does when its database cannot answer: a failure
// of one scope. The other calls for the project read the data of the infra project, so that the project connects.
type failingProjectClient struct {
	observation.Client
	project string
}

func (c failingProjectClient) Call(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	if name, _ := args["project"].(string); name == c.project {
		if tool == "query_graph" {
			return nil, diag.Fail(diag.OBS001, diag.Detail("query_graph: boom"))
		}

		same := maps.Clone(args)
		same["project"] = infraProject

		return c.Client.Call(ctx, tool, same)
	}

	return c.Client.Call(ctx, tool, args)
}
