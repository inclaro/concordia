package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
)

// A32 on the replayed recordings of test/fixtures/review/cbm-0.12.1: the same inputs evaluated incrementally and fully
// give the same currency, triggers, paths, digests, and recorded run inputs, after the human timestamps are removed.
// The tests run without CBM. (The recorded-CBM end-to-end run of the same steps is test/e2e/review_test.go.)

// semanticReview is the canonical JSON of a value without what differs between the two modes by design.
func semanticReview(t *testing.T, v any, drop func(tree map[string]any)) string {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}

	drop(tree)

	text, err := canonjson.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

// withoutCacheNotes removes the diagnostics `CO-ASM-010` from a list in the tree: they say that a cache was not usable.
func withoutCacheNotes(tree map[string]any, key string) {
	list, _ := tree[key].([]any)
	list = slices.DeleteFunc(list, func(d any) bool {
		m, ok := d.(map[string]any)

		return ok && m["code"] == diag.ASM010
	})

	if len(list) == 0 {
		delete(tree, key)
	} else {
		tree[key] = list
	}
}

func firstReviewDifference(got, want string) string {
	if got == want {
		return ""
	}

	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range min(len(gl), len(wl)) {
		if gl[i] != wl[i] {
			return fmt.Sprintf("line %d: incremental %q, full %q", i+1, gl[i], wl[i])
		}
	}

	return "the lengths differ"
}

// reviewIncWorld is the assessment set of ASM-API, the two replayed recordings, and the incremental cache that the
// evaluations share.
type reviewIncWorld struct {
	set      *assessment.Set
	baseline *reviewRun
	variant  *reviewRun
	now      time.Time
	cache    *assessment.Incremental
}

func newReviewIncWorld(t *testing.T) *reviewIncWorld {
	t.Helper()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{"assessments/ASM-API/v1.yaml": reviewAssessment})

	set, err := assessment.Load(root, []string{"assessments"})
	if err != nil {
		t.Fatal(err)
	}

	return &reviewIncWorld{
		set:      set,
		baseline: replayReview(t, reviewBaselineFile, nil),
		variant:  replayReview(t, reviewVariantFile, []string{reviewSecretFile}),
		now:      time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		cache:    &assessment.Incremental{Dir: filepath.Join(t.TempDir(), "impact"), AnchorRevision: "abc123"},
	}
}

func (w *reviewIncWorld) input(facts impact.FactSource, incremental bool) *assessment.EvalInput {
	in := &assessment.EvalInput{
		Set: w.set, Verifier: approvedAll{}, Facts: facts, Now: func() time.Time { return w.now },
		PolicyDigest: "sha256:" + strings.Repeat("1", 64), AuthorityDigest: "sha256:" + strings.Repeat("2", 64),
	}
	if incremental {
		in.Incremental = w.cache
	}

	return in
}

// approveBaseline binds the assessment to the baseline and adds the approval of the binding to the set.
func (w *reviewIncWorld) approveBaseline(t *testing.T) {
	t.Helper()

	bind, err := assessment.Bind(t.Context(), w.input(w.baseline.facts, false), "ASM-API", 1)
	if err != nil {
		t.Fatal(err)
	}

	w.set.Approvals = append(w.set.Approvals, &assessment.Approval{
		ID: "ASM-API-v1-0001", Decision: assessment.DecisionApproved, Assessment: bind.Assessment, Subject: bind.Subject, Scope: bind.Scope,
		Environment: bind.Environment, Assumptions: bind.Assumptions, Evidence: bind.Evidence,
		ContractVersion: bind.ContractVersion, CapabilityProfile: bind.CapabilityProfile, ProducedUnder: bind.ProducedUnder,
		Reviewer:  assessment.Reviewer{Identity: "alice@example.com", Authority: "security-review"},
		DecidedAt: w.now.Add(-24 * time.Hour), Location: assessment.Location{File: "assessments/approvals/ASM-API-v1-0001.yaml"},
		Digest: "sha256:" + strings.Repeat("a", 64),
	})
}

// reviewIncStep is one evaluation of the sequence: the facts, the currency that both modes give, and the cache hits of
// the incremental one.
type reviewIncStep struct {
	name  string
	facts impact.FactSource
	state assessment.CurrencyState
	hits  int
}

// steps are the baseline (a cache is written), the variant (the Secret declaration is gone), and back.
func (w *reviewIncWorld) steps() []reviewIncStep {
	return []reviewIncStep{
		{"baseline, no cache yet", w.baseline.facts, assessment.CurrencyCurrent, 0},
		{"baseline, cache", w.baseline.facts, assessment.CurrencyCurrent, 1},
		{"variant, one item removed", w.variant.facts, assessment.CurrencyReviewRequired, 0},
		{"variant again", w.variant.facts, assessment.CurrencyReviewRequired, 1},
		{"baseline again", w.baseline.facts, assessment.CurrencyCurrent, 0},
	}
}

// reviewIncDrop removes what differs between the two modes by design from an evaluation.
func reviewIncDrop(tree map[string]any) {
	delete(tree, "incremental")
	withoutCacheNotes(tree, "diagnostics")
}

// requireStep evaluates the step incrementally and fully, and checks that the results are equal and that the currency
// and the cache hits are the ones of the step.
func (w *reviewIncWorld) requireStep(t *testing.T, step *reviewIncStep) {
	t.Helper()

	incremental, err := assessment.Evaluate(context.Background(), w.input(step.facts, true))
	if err != nil {
		t.Fatalf("%s: %v", step.name, err)
	}

	full, err := assessment.Evaluate(context.Background(), w.input(step.facts, false))
	if err != nil {
		t.Fatalf("%s: %v", step.name, err)
	}

	if d := firstReviewDifference(semanticReview(t, incremental, reviewIncDrop), semanticReview(t, full, reviewIncDrop)); d != "" {
		t.Errorf("%s: the incremental result differs from the full result: %s", step.name, d)
	}

	res := incremental.Assessments[0]
	if res.Currency.State != step.state {
		t.Errorf("%s: state %s (reasons %v, causes %+v), want %s", step.name, res.Currency.State, res.Currency.Reasons, res.Currency.Causes, step.state)
	}

	if got := incremental.Incremental.Hits; got != step.hits {
		t.Errorf("%s: %d cache hits, want %d (%+v)", step.name, got, step.hits, incremental.Incremental)
	}

	if full.Incremental != nil {
		t.Errorf("%s: a full evaluation reports a cache", step.name)
	}
}

// requireVariantTriggers checks that the triggers and paths are not empty on the variant: the equality of the steps is
// the equality of real results.
func (w *reviewIncWorld) requireVariantTriggers(t *testing.T) {
	t.Helper()

	variantRun, err := assessment.Evaluate(context.Background(), w.input(w.variant.facts, true))
	if err != nil {
		t.Fatal(err)
	}

	res := variantRun.Assessments[0].Currency
	if len(res.Triggers) == 0 {
		t.Fatalf("the removal of the Secret declaration has no trigger with a path: %+v", res.Triggers)
	}

	if len(res.Triggers[0].Path) < 2 {
		t.Errorf("the removal of the Secret declaration has no trigger with a path: %+v", res.Triggers)
	}
}

// Scenario: A32 two modes equal.
func TestReviewIncrementalEvaluationEqualsFullOnTheReplayedRecordings(t *testing.T) {
	t.Parallel()

	w := newReviewIncWorld(t)
	w.approveBaseline(t)

	for _, step := range w.steps() {
		w.requireStep(t, &step)
	}

	w.requireVariantTriggers(t)
}

// reviewPipelineWorkspace is the deployment workspace with the review pack and one required assessment.
func reviewPipelineWorkspace() map[string]string {
	text := strings.Replace(deploymentWorkspaceHead, "    - use: builtin:infrastructure\n",
		"    - use: builtin:infrastructure\n    - use: builtin:review-currency\n", 1) +
		deploymentCheck + deploymentEnvironments + `assessments:
  paths: [assessments]
  authorities:
    - id: security-review
      role: reviewer
      mechanism: protected-review
      reviewers: [alice@example.com]
      paths: [assessments/approvals/**]
  required:
    - { id: api-secrets, assessment: ASM-API, type: security, scope: { calmNodes: [api-service] }, environment: production, authority: security-review }
`

	// The Secret references of the workloads that the CALM node api-service is bound to.
	document := strings.Replace(reviewAssessment, `      selector:
        identity: [`+reviewProdIdentity+`]
        reference: [`+reviewSecretObjKey+`]
`, `      selector:
        calmNodes: [api-service]
`, 1)

	return map[string]string{"concordia.yaml": text, "assessments/ASM-API/v1.yaml": document}
}

// reviewPipelineRun is one check of the workspace copy against a recording. The run manifest and the results are read
// back from the files that the check wrote.
type reviewPipelineRun struct {
	run      *conformance.Run
	manifest map[string]any
	results  map[string]any
}

// readReviewJSON reads a JSON object that the check wrote under the workspace.
func readReviewJSON(t *testing.T, w *infraWorkspace, rel string) map[string]any {
	t.Helper()

	var tree map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(w.dir, filepath.FromSlash(rel))), &tree); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}

	return tree
}

func checkReviewPipeline(t *testing.T, w *infraWorkspace, incremental bool) *reviewPipelineRun {
	t.Helper()

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	opts := w.checkOptions(nil, func(o *conformance.CheckOptions) {
		o.Deps.Now = func() time.Time { return now }
		o.Incremental = incremental
	})

	run, err := conformance.RunCheck(context.Background(), opts)
	if err != nil {
		t.Fatalf("check (incremental %v): %v", incremental, err)
	}

	return &reviewPipelineRun{
		run:      run,
		manifest: readReviewJSON(t, w, conformance.RunManifestPath(runID)),
		results:  readReviewJSON(t, w, "build/concordia/results.json"),
	}
}

// writeReviewIndex writes the index result that `concordia index` writes for the recording: the coverage of the
// dependency kinds is unknown without it.
func writeReviewIndex(t *testing.T, w *infraWorkspace, recording string) {
	t.Helper()

	first := checkReviewPipeline(t, w, false)
	record := replayInfra(t, recording).index.Record(infraProject, first.run.Run.GraphGeneration, w.rec.HeadSHA)

	if err := conformance.WriteIndexRecord(w.dir, conformance.IndexRecordFile, &record); err != nil {
		t.Fatal(err)
	}
}

// volatile removes the human timestamps and the identity of a run, which differ between two runs of equal inputs.
func volatile(tree map[string]any) {
	if run, ok := tree["run"].(map[string]any); ok {
		for _, key := range []string{"id", "startedAt", "finishedAt", "host"} {
			delete(run, key)
		}
	}
}

// reviewAnalysisFacts is what the `analysis` object of a run says about the cache.
type reviewAnalysisFacts struct {
	mode   string
	hits   int
	missed bool
}

// reviewAnalysisOf gives the facts of the analysis object of the run, and false when it has none.
func reviewAnalysisOf(run *conformance.Run) (reviewAnalysisFacts, bool) {
	if run.Analysis == nil {
		return reviewAnalysisFacts{}, false
	}

	return reviewAnalysisFacts{mode: run.Analysis.Mode, hits: run.Analysis.CacheHits, missed: run.Analysis.CacheMisses != 0}, true
}

// requireFullRunWithoutCache checks that a check without the flag evaluates fully and writes nothing under the cache
// directory, whatever it evaluates.
func requireFullRunWithoutCache(t *testing.T, plain *reviewPipelineRun, cacheDir string) {
	t.Helper()

	if plain.run.Analysis == nil {
		t.Fatalf("the plain run has no full analysis: %+v", plain.run.Analysis)
	}

	if plain.run.Analysis.Mode != conformance.AnalysisFull {
		t.Fatalf("the plain run has no full analysis: %+v", plain.run.Analysis)
	}

	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("a check without --incremental made %s: %v", cacheDir, err)
	}
}

// requireColdThenWarm checks that the first incremental run fills the cache (every assessment is a miss) and that the
// second one reuses it.
func requireColdThenWarm(t *testing.T, w *infraWorkspace) *reviewPipelineRun {
	t.Helper()

	cold := checkReviewPipeline(t, w, true)

	coldFacts, ok := reviewAnalysisOf(cold.run)
	if !ok {
		t.Fatalf("cold run analysis: %+v", cold.run.Analysis)
	}

	if want := (reviewAnalysisFacts{mode: conformance.AnalysisIncremental, hits: 0, missed: true}); coldFacts != want {
		t.Fatalf("cold run analysis: %+v", cold.run.Analysis)
	}

	warm := checkReviewPipeline(t, w, true)
	if got := warm.run.Analysis; got.CacheMisses != 0 || got.CacheHits != cold.run.Analysis.CacheMisses {
		t.Fatalf("warm run analysis: %+v, cold %+v", got, cold.run.Analysis)
	}

	return warm
}

// switchToVariant deletes the Secret declaration: the recording and the repository are the variant.
func switchToVariant(t *testing.T, w *infraWorkspace) {
	t.Helper()

	variant := observation.Recording{}
	if err := json.Unmarshal(mustRead(t, reviewVariantFile), &variant); err != nil {
		t.Fatal(err)
	}

	w.rec = variant

	if err := os.Remove(filepath.Join(w.dir, filepath.FromSlash(reviewSecretFile))); err != nil {
		t.Fatal(err)
	}

	w.files = slices.DeleteFunc(w.files, func(p string) bool { return p == reviewSecretFile })

	writeReviewIndex(t, w, reviewVariantFile)
}

// requireOnlyCacheFile checks that the cache directory has a cache file and nothing else.
func requireOnlyCacheFile(t *testing.T, cacheDir string) {
	t.Helper()

	entries, err := fs.ReadDir(os.DirFS(cacheDir), ".")
	if err != nil {
		t.Fatalf("the cache directory has %v (%v)", entries, err)
	}

	if len(entries) != 1 {
		t.Errorf("the cache directory has %v (%v)", entries, err)

		return
	}

	if !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Errorf("the cache directory has %v (%v)", entries, err)
	}
}

// Scenario: A32 two modes equal (the pipeline: results, run manifest inputs).
// Scenario: Plain check writes no cache.
func TestReviewIncrementalPipelineEqualsFull(t *testing.T) {
	t.Parallel()

	w := newDeploymentWorkspace(t, &deploymentSpec{files: reviewPipelineWorkspace()})
	cacheDir := filepath.Join(w.dir, filepath.FromSlash(impact.CacheDir))

	writeReviewIndex(t, w, reviewBaselineFile)

	plain := checkReviewPipeline(t, w, false)
	requireFullRunWithoutCache(t, plain, cacheDir)

	warm := requireColdThenWarm(t, w)

	(&reviewRunPair{name: "unchanged", incremental: warm, full: plain, wantTriggers: true}).requireSame(t)

	// The cache still has the results of the baseline.
	switchToVariant(t, w)

	// The full run first: the cache directory must not change it.
	full := checkReviewPipeline(t, w, false)
	changed := checkReviewPipeline(t, w, true)

	if got := changed.run.Analysis; got.Mode != conformance.AnalysisIncremental || got.CacheMisses == 0 {
		t.Errorf("changed run analysis: %+v, want the assessment of the removed item evaluated again", got)
	}

	(&reviewRunPair{name: "changed", incremental: changed, full: full}).requireSame(t)

	requireOnlyCacheFile(t, cacheDir)
}

// reviewRunPair is the incremental and the full run of the same inputs.
type reviewRunPair struct {
	incremental *reviewPipelineRun
	full        *reviewPipelineRun
	name        string
	// wantTriggers says that the unapproved assessment has a trigger for the Secret reference that the baseline declares.
	wantTriggers bool
}

// dropResults removes what differs between the modes by design from a results document.
func reviewDropResults(tree map[string]any) {
	volatile(tree)
	delete(tree, "analysis")
	withoutCacheNotes(tree, "diagnostics")

	if sum, ok := tree["summary"].(map[string]any); ok {
		delete(sum, "resultDigest")
	}
}

func reviewDropManifest(tree map[string]any) {
	reviewDropResults(tree)
	delete(tree, "status")
}

// reviewCurrencyList gives the assessments of the assessment currency of the results, nil when there are none.
func reviewCurrencyList(results map[string]any) []any {
	cur, _ := results["assessmentCurrency"].(map[string]any)
	list, _ := cur["assessments"].([]any)

	return list
}

// requireSame fails when the two runs differ in anything but the human timestamps, the run identity, and what describes
// the mode: the counters of the `analysis` object, the result digest that covers them, and the note CO-ASM-010.
func (p *reviewRunPair) requireSame(t *testing.T) {
	t.Helper()

	if d := firstReviewDifference(semanticReview(t, p.incremental.results, reviewDropResults), semanticReview(t, p.full.results, reviewDropResults)); d != "" {
		t.Errorf("%s: results.json differs between the modes: %s", p.name, d)
	}

	if d := firstReviewDifference(semanticReview(t, p.incremental.manifest, reviewDropManifest), semanticReview(t, p.full.manifest, reviewDropManifest)); d != "" {
		t.Errorf("%s: the run manifest (recorded inputs) differs between the modes: %s", p.name, d)
	}

	list := reviewCurrencyList(p.incremental.results)
	if len(list) == 0 {
		t.Fatalf("%s: the run has no assessment currency to compare: %v", p.name, p.incremental.results["assessmentCurrency"])
	}

	// The equality is the equality of real results.
	first, _ := list[0].(map[string]any)
	if triggers, _ := first["triggers"].([]any); p.wantTriggers && len(triggers) == 0 {
		t.Errorf("%s: the assessment has no trigger: %v", p.name, first)
	}

	inputs, _ := p.incremental.manifest["inputs"].(map[string]any)
	if list, _ := inputs["assessments"].([]any); len(list) == 0 {
		t.Errorf("%s: the manifest records no assessment input: %v", p.name, inputs)
	}
}
