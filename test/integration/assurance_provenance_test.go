package integration_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	deptrace "github.com/inclaro/concordia/internal/deployment"
)

// These tests run the pack rules ASR-PRV-001 (exact lineage) and ASR-PRV-002 (runtime drift) and the lineage section
// of results.json on the A10 workspace extended with provenance (A33, A34), through the whole check pipeline.

// runProduction checks the world for the environment production only: the world renders no staging.
func (w *provWorld) runProduction(t *testing.T) *deployment {
	t.Helper()

	spec := w.spec(t)
	edit := spec.edit

	spec.edit = func(opts *conformance.CheckOptions) {
		edit(opts)
		opts.Environments = []string{"production"}
	}

	return deploymentRun(t, spec)
}

// provAppFacts is what the lineage entry of the verified container `app` says about the build.
type provAppFacts struct {
	verdict      conformance.RuleVerdict
	sourceID     string
	contractID   string
	hasBuilder   bool
	digestHashed bool
	hasStatement bool
}

func provAppFactsOf(entry *conformance.LineageEntry) provAppFacts {
	facts := provAppFacts{
		verdict: entry.Verdict, hasBuilder: entry.Builder != "", contractID: entry.TrustContract.ID,
		digestHashed: strings.HasPrefix(entry.TrustContract.Digest, "sha256:"), hasStatement: entry.Statement != nil,
	}

	if entry.Source != nil {
		facts.sourceID = entry.Source.ID
	}

	return facts
}

// provSidecarFacts is what the lineage entry of the container `sidecar`, which names a tag only, says.
type provSidecarFacts struct {
	verdict      conformance.RuleVerdict
	reason       conformance.UnknownReason
	missing      string
	sameContract bool
}

func provSidecarFactsOf(sidecar, app *conformance.LineageEntry) provSidecarFacts {
	return provSidecarFacts{
		verdict: sidecar.Verdict, reason: sidecar.Reason, missing: strings.Join(sidecar.Missing, ","),
		sameContract: sidecar.TrustContract.Digest == app.TrustContract.Digest,
	}
}

// provFindingFacts is what a finding says about the occurrence that is not pass.
type provFindingFacts struct {
	occurrence string
	result     conformance.RuleVerdict
	reason     conformance.UnknownReason
	subject    string
	inManifest bool
}

func provFindingFactsOf(f *conformance.Finding) provFindingFacts {
	return provFindingFacts{
		occurrence: f.Occurrence, result: f.Result, reason: f.Reason, subject: f.Subject,
		inManifest: f.Location != nil && strings.HasSuffix(f.Location.Path, "deployment.yaml"),
	}
}

// provFindingFactsList gives the facts of every finding.
func provFindingFactsList(findings []conformance.Finding) []provFindingFacts {
	facts := make([]provFindingFacts, 0, len(findings))

	for i := range findings {
		facts = append(facts, provFindingFactsOf(&findings[i]))
	}

	return facts
}

// provMissingRenderFacts gives the environment and the reason of every finding.
func provMissingRenderFacts(findings []conformance.Finding) []string {
	facts := make([]string, 0, len(findings))

	for i := range findings {
		facts = append(facts, findings[i].Environment+" "+string(findings[i].Reason))
	}

	return facts
}

// provFindingsIn gives the findings of a rule for one environment.
func provFindingsIn(run *conformance.Run, rule, environment string) []conformance.Finding {
	var out []conformance.Finding

	found := provFindingsOf(run, rule)
	for i := range found {
		if found[i].Environment == environment {
			out = append(out, found[i])
		}
	}

	return out
}

// provFindingOfOccurrence gives the finding of the occurrence, or nil.
func provFindingOfOccurrence(findings []conformance.Finding, occurrence string) *conformance.Finding {
	for i := range findings {
		if findings[i].Occurrence == occurrence {
			return &findings[i]
		}
	}

	return nil
}

// provRequireRuleVerdict fails the test unless the rule of the run has the verdict.
func provRequireRuleVerdict(t *testing.T, run *conformance.Run, rule string, want conformance.RuleVerdict) {
	t.Helper()

	if got := provRuleOf(t, run, rule); got.Verdict != want {
		t.Errorf("%s = %s (%+v), want %s", rule, got.Verdict, got.Reasons, want)
	}
}

func TestASRPRV001ReportsOneFindingForEachOccurrenceThatIsNotPass(t *testing.T) {
	t.Parallel()

	// A33 beside A34: the container `app` is pinned by digest and has verified provenance; the container `sidecar`
	// names a tag only.
	run := (&provWorld{attestations: []string{provShop}}).run(t).run

	app := provLineageOf(t, run, "app")
	wantApp := provAppFacts{verdict: conformance.VerdictPass, hasBuilder: true, sourceID: "shop", contractID: "provenance", digestHashed: true, hasStatement: true}

	if got := provAppFactsOf(app); got != wantApp {
		t.Errorf("app lineage = %+v", app)
	}

	sidecar := provLineageOf(t, run, "sidecar")
	wantSidecar := provSidecarFacts{verdict: conformance.VerdictUnknown, reason: conformance.ReasonNoEvidenceSupplied, missing: "digest,provenance", sameContract: true}

	if got := provSidecarFactsOf(sidecar, app); got != wantSidecar {
		t.Errorf("sidecar lineage = %+v", sidecar)
	}

	provRequireRuleVerdict(t, run, "ASR-PRV-001", conformance.VerdictUnknown)

	// One finding for the occurrence that is not pass, none for the one that is.
	production := provFindingsIn(run, "ASR-PRV-001", "production")
	wantProduction := provFindingFacts{
		occurrence: "sidecar", result: conformance.VerdictUnknown, reason: conformance.ReasonNoEvidenceSupplied,
		subject: "Deployment/payments/api", inManifest: true,
	}

	if !reflect.DeepEqual(provFindingFactsList(production), []provFindingFacts{wantProduction}) {
		t.Errorf("production findings = %+v", production)
	}

	// The environment that has no rendered source cannot be traced at all: its own unknown, never a pass.
	staging := provFindingsIn(run, "ASR-PRV-001", "staging")
	if len(staging) != 1 || staging[0].Reason != conformance.ReasonRenderedEvidenceMissing || staging[0].Occurrence != "" {
		t.Errorf("staging findings = %+v", staging)
	}
}

func TestASRPRV001PassesWhenEveryOccurrenceHasExactLineage(t *testing.T) {
	t.Parallel()

	w := &provWorld{attestations: []string{provShop}, sidecarImage: provRepository + "@" + provDigestA}
	run := w.runProduction(t).run

	provRequireRuleVerdict(t, run, "ASR-PRV-001", conformance.VerdictPass)

	if findings := provFindingsOf(run, "ASR-PRV-001"); len(findings) != 0 {
		t.Errorf("findings = %+v", findings)
	}
}

func TestASRPRV001IsAViolationForAnUntrustedBuilder(t *testing.T) {
	t.Parallel()

	run := (&provWorld{attestations: []string{provEvil}, sidecarImage: provRepository + "@" + provDigestA}).runProduction(t).run

	findings := provFindingsOf(run, "ASR-PRV-001")
	if len(findings) != 2 {
		t.Fatalf("findings = %+v", findings)
	}

	for _, f := range findings {
		if f.Result != conformance.VerdictViolation || !strings.Contains(f.Observed, "evil") {
			t.Errorf("finding = %+v, want a violation with the builder as the observed evidence", f)
		}
	}

	provRequireRuleVerdict(t, run, "ASR-PRV-001", conformance.VerdictViolation)
}

func TestASRPRV001IsUnknownWithoutRenderedOccurrences(t *testing.T) {
	t.Parallel()

	// A statement that names a digest links to nothing when no rendered occurrence has that digest.
	run := (&provWorld{attestations: []string{provShop}, noRendering: true}).runProduction(t).run

	findings := provFindingsOf(run, "ASR-PRV-001")
	if len(findings) != 1 || findings[0].Reason != conformance.ReasonRenderedEvidenceMissing {
		t.Errorf("findings = %+v", findings)
	}

	if len(run.Assurance.Lineage) != 0 {
		t.Errorf("lineage = %+v, want none without a rendered occurrence", run.Assurance.Lineage)
	}
}

// provRequireObservedMentions fails the test for every text that the observed evidence of the finding does not
// contain.
func provRequireObservedMentions(t *testing.T, finding *conformance.Finding, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(finding.Observed, want) {
			t.Errorf("observed %q does not contain %q", finding.Observed, want)
		}
	}
}

func TestASRPRV002NamesBothDigestsOfADrift(t *testing.T) {
	t.Parallel()

	w := &provWorld{
		attestations: []string{provShop}, sidecarImage: provRepository + "@" + provDigestA,
		records: []provRecord{
			{path: "deploy-1.json", digest: provDigestA, outcome: "succeeded", age: 2 * time.Hour},
			{path: "observed-1.json", digest: provDigestB, age: time.Hour},
		},
	}

	run := w.runProduction(t).run

	findings := provFindingsOf(run, "ASR-PRV-002")

	app := provFindingOfOccurrence(findings, "app")
	if app == nil {
		t.Fatalf("findings = %+v", findings)
	}

	if app.Result != conformance.VerdictViolation {
		t.Fatalf("findings = %+v", findings)
	}

	provRequireObservedMentions(t, app, provDigestA, provDigestB, "deployment-record", "workload-observation", "build/records/deploy-1.json", "build/records/observed-1.json")
	provRequireRuleVerdict(t, run, "ASR-PRV-002", conformance.VerdictViolation)

	// The lineage section keeps both sources as separate facts.
	drift := provLineageOf(t, run, "app").Deployment.RuntimeDrift
	if len(drift.Findings) != 2 || drift.Findings[0].Observed.Digest != provDigestB {
		t.Errorf("drift findings = %+v", drift.Findings)
	}
}

func TestASRPRV002PassesWhenTheObservationAgrees(t *testing.T) {
	t.Parallel()

	w := &provWorld{
		attestations: []string{provShop}, sidecarImage: provRepository + "@" + provDigestA,
		records: []provRecord{
			{path: "deploy-1.json", digest: provDigestA, outcome: "succeeded", age: 2 * time.Hour},
			{path: "deploy-2.json", container: "sidecar", digest: provDigestA, outcome: "succeeded", age: 2 * time.Hour},
			{path: "observed-1.json", digest: provDigestA, age: time.Hour},
			{path: "observed-2.json", container: "sidecar", digest: provDigestA, age: time.Hour},
		},
	}

	run := w.runProduction(t).run

	if rule := provRuleOf(t, run, "ASR-PRV-002"); rule.Verdict != conformance.VerdictPass {
		t.Errorf("ASR-PRV-002 = %s (%+v), findings %+v", rule.Verdict, rule.Reasons, provFindingsOf(run, "ASR-PRV-002"))
	}

	entry := provLineageOf(t, run, "app")
	if entry.Deployment.DeploymentSucceeded.Verdict != "pass" || entry.Deployment.DeployedDigest.Digest != provDigestA {
		t.Errorf("deployment = %+v", entry.Deployment)
	}
}

// provRequireOneRejected fails the test unless the verdict of the claim is unknown and the deployment evidence holds one
// rejected record with the detail.
func provRequireOneRejected(t *testing.T, deployment *conformance.LineageDeployment, claimVerdict, detail string) {
	t.Helper()

	if claimVerdict != "unknown" || len(deployment.Rejected) != 1 {
		t.Errorf("deployment = %+v", *deployment)

		return
	}

	if !slices.Contains(deployment.Rejected[0].Details, assurance.Detail(detail)) {
		t.Errorf("deployment = %+v", *deployment)
	}
}

func TestASRPRV002IsUnknownForAStaleOrMissingObservation(t *testing.T) {
	t.Parallel()

	w := &provWorld{
		attestations: []string{provShop},
		records: []provRecord{
			{path: "deploy-1.json", digest: provDigestA, outcome: "succeeded", age: 2 * time.Hour},
			// Older than the 7 days of the world: no drift result is derived from it.
			{path: "observed-1.json", digest: provDigestB, age: 10 * 24 * time.Hour},
		},
	}

	run := w.runProduction(t).run

	for _, f := range provFindingsOf(run, "ASR-PRV-002") {
		if f.Result == conformance.VerdictViolation {
			t.Errorf("a stale observation gave a violation: %+v", f)
		}
	}

	entry := provLineageOf(t, run, "app")
	provRequireOneRejected(t, &entry.Deployment, string(entry.Deployment.RuntimeDrift.Verdict), "stale")
}

// A record for another environment does not bind a digest to production.
func TestARecordOfAnotherEnvironmentIsRejected(t *testing.T) {
	t.Parallel()

	w := &provWorld{
		attestations: []string{provShop},
		records:      []provRecord{{path: "deploy-1.json", environment: "staging", digest: provDigestA, outcome: "succeeded", age: time.Hour}},
	}

	entry := provLineageOf(t, w.runProduction(t).run, "app")
	provRequireOneRejected(t, &entry.Deployment, string(entry.Deployment.DeploymentSucceeded.Verdict), "different-environment")
}

// provProofFacts is what the build provenance entity of a trace step says.
type provProofFacts struct {
	lineage      deptrace.Lineage
	digest       string
	repository   string
	container    string
	locations    []deptrace.Location
	contractHash bool
}

func provProofFactsOf(proof *deptrace.Entity) provProofFacts {
	return provProofFacts{
		lineage: proof.Lineage, digest: proof.Properties["digest"], repository: proof.Properties["sourceRepository"],
		container: proof.Properties["container"], contractHash: strings.HasPrefix(proof.Properties["trustContract"], "sha256:"),
		locations: proof.Locations,
	}
}

// provRequireDockerfileCandidate fails the test unless the step holds one Dockerfile and the static link keeps it a
// candidate.
func provRequireDockerfileCandidate(t *testing.T, build *deptrace.StepTrace) {
	t.Helper()

	dockerfiles := entitiesNamed(build, deptrace.KindDockerfile)
	if len(dockerfiles) != 1 || dockerfiles[0].Lineage != deptrace.LineageCandidate {
		t.Errorf("dockerfiles = %+v", dockerfiles)
	}
}

// The trace of A10 extended with A34: the image-to-build step of the production api shows `verified` for the container
// with verified provenance and keeps the Dockerfile as a candidate; staging has no rendered occurrence and stays a
// candidate.
func TestTraceImageToBuildStepShowsVerifiedLineage(t *testing.T) {
	t.Parallel()

	// The provider keeps one occurrence for the repository (DEP-05): the container `sidecar`. Both rendered containers
	// are pinned by digest and have verified provenance.
	d := (&provWorld{attestations: []string{provShop}, sidecarImage: provRepository + "@" + provDigestA}).run(t)
	production := subjectTrace(t, d, "api-service", "production")
	build := stepNamed(t, production, deptrace.StepBuildDefinition)

	wantProof := provProofFacts{
		lineage: deptrace.LineageVerified, digest: provDigestA, repository: "shop", container: "sidecar", contractHash: true,
		locations: []deptrace.Location{{Path: "build/attestations/" + provShop}},
	}

	proofs := entitiesNamed(build, deptrace.KindBuildProvenance)
	if len(proofs) != 1 || !reflect.DeepEqual(provProofFactsOf(&proofs[0]), wantProof) {
		t.Errorf("provenance entities = %+v", proofs)
	}

	// A static link never makes a step verified: the Dockerfile stays a candidate beside the verified entity.
	provRequireDockerfileCandidate(t, build)

	staging := stepNamed(t, subjectTrace(t, d, "api-service", "staging"), deptrace.StepBuildDefinition)
	if len(entitiesNamed(staging, deptrace.KindBuildProvenance)) != 0 {
		t.Errorf("staging build step = %+v", staging)
	}

	provRequireDockerfileCandidate(t, staging)

	provGolden(t, "trace-a10-a34-production.json", production.Steps)
}

func TestTraceDockerfileLinkAloneStaysCandidate(t *testing.T) {
	t.Parallel()

	// The only statement names another digest, so no provenance is linked to the occurrence.
	d := (&provWorld{attestations: []string{provWrong}}).run(t)
	build := stepNamed(t, subjectTrace(t, d, "api-service", "production"), deptrace.StepBuildDefinition)

	if len(entitiesNamed(build, deptrace.KindBuildProvenance)) != 0 {
		t.Errorf("a provenance entity without provenance: %+v", build.Entities)
	}

	provRequireDockerfileCandidate(t, build)

	if entry := provLineageOf(t, d.run, "app"); entry.Verdict != conformance.VerdictUnknown {
		t.Errorf("lineage = %+v, want unknown beside a Dockerfile candidate", entry)
	}
}

// ASR-REN-001 reads the same rendered evidence: complete for production, missing for staging.
func TestASRREN001ReadsTheRenderedCoverageOfEachEnvironment(t *testing.T) {
	t.Parallel()

	run := (&provWorld{attestations: []string{provShop}}).run(t).run
	findings := provFindingsOf(run, "ASR-REN-001")

	if !reflect.DeepEqual(provMissingRenderFacts(findings), []string{"staging " + string(conformance.ReasonRenderedEvidenceMissing)}) {
		t.Errorf("findings = %+v", findings)
	}

	provRequireRuleVerdict(t, run, "ASR-REN-001", conformance.VerdictUnknown)

	run = (&provWorld{attestations: []string{provShop}}).runProduction(t).run
	provRequireRuleVerdict(t, run, "ASR-REN-001", conformance.VerdictPass)
}

// provLineageJSON gives the lineage section of the run of the world as canonical JSON.
func provLineageJSON(t *testing.T, w *provWorld) string {
	t.Helper()

	data, err := canonjson.Marshal(w.run(t).run.Assurance.Lineage)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// provLineageWorld is the world of the lineage section tests: a trusted and an untrusted statement, and a drift.
func provLineageWorld(noPack bool) *provWorld {
	return &provWorld{
		attestations: []string{provShop, provEvil}, noPack: noPack,
		records: []provRecord{
			{path: "deploy-1.json", digest: provDigestA, outcome: "succeeded", age: 2 * time.Hour},
			{path: "observed-1.json", digest: provDigestB, age: time.Hour},
		},
	}
}

// The lineage section is computed from the evidence once per run and does not depend on the rules that ran, and a
// second evaluation of the same inputs gives the same section.
func TestLineageSectionIsIndependentOfRulesAndRepeatable(t *testing.T) {
	t.Parallel()

	first := provLineageJSON(t, provLineageWorld(false))
	second := provLineageJSON(t, provLineageWorld(false))
	withoutRules := provLineageJSON(t, provLineageWorld(true))

	if first != second {
		t.Errorf("the second evaluation differs:\n%s\n%s", first, second)
	}

	if first != withoutRules {
		t.Errorf("the section depends on the rules that ran:\n%s\n%s", first, withoutRules)
	}

	lower := strings.ToLower(first)
	if !strings.Contains(first, `"trustContract"`) {
		t.Errorf("lineage section = %s", first)
	}

	for _, banned := range []string{"slsalevel", "slsa-level"} {
		if strings.Contains(lower, banned) {
			t.Errorf("lineage section = %s", first)
		}
	}
}

// The results of a run with every kind of lineage entry (pass, violation through the untrusted builder, unknown with
// missing items, drift, rejected statements) are valid against the results schema.
func TestResultsWithLineageAreValidAgainstTheSchema(t *testing.T) {
	t.Parallel()

	w := &provWorld{
		attestations: []string{provShop, provEvil, provModified},
		records: []provRecord{
			{path: "deploy-1.json", digest: provDigestA, outcome: "succeeded", age: 2 * time.Hour},
			{path: "observed-1.json", digest: provDigestB, age: time.Hour},
			{path: "observed-2.json", environment: "staging", digest: provDigestB, age: time.Hour},
		},
	}

	run := w.run(t).run
	if len(run.Assurance.Lineage) != 2 {
		t.Fatalf("lineage = %+v", run.Assurance.Lineage)
	}

	data, err := canonjson.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}

	if errs := conformance.ValidateResultJSON(data); len(errs) > 0 {
		t.Errorf("results.json is not valid against the schema: %v", errs)
	}
}
