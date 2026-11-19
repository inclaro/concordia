package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// These tests run `check` with the view checks (Deps.EvaluateViews = views.Evaluate) on the view-currency fixture,
// replayed against the recorded CBM graph: the pipeline fetches the code edges and the diagram arrows itself, the
// tracked revision is a FakeGit that holds the fixture and the rendered views that a test commits.

const viewsGoldenPath = "../golden/views/results.json"

// committedViews gives the files that `render --views` writes for the two generated views of the fixture.
func (r *vcRepo) committedViews(t *testing.T) map[string]string {
	t.Helper()

	committed := map[string]string{}

	for _, id := range []string{vcContainers, vcContext} {
		_, files := r.generated(t, id)
		maps.Copy(committed, files)
	}

	return committed
}

// checkViews runs the check pipeline on the fixture at the recording of the repo, against a tracked revision that
// holds the fixture without the given paths, and the given files.
func (r *vcRepo) checkViews(t *testing.T, committed map[string]string, without ...string) *conformance.Run {
	t.Helper()

	files := map[string]string{}

	for p, content := range r.tracked {
		if !slices.Contains(without, p) {
			files[p] = content
		}
	}

	maps.Copy(files, committed)

	run, err := conformance.RunCheck(t.Context(), &conformance.CheckOptions{
		Env: conformance.Env{
			Logger: logger.New(logger.Options{Out: io.Discard, RunID: "run", Quiet: true}), RunID: runID,
			WorkspacePath: filepath.Join(r.dir, workspaceFile), Cwd: r.dir, ToolVersion: "test",
		},
		Deps: conformance.Deps{
			CreateClient: func(*workspace.Workspace, *logger.Logger) observation.Client {
				return observation.NewReplayClient(r.rec)
			},
			CreateGit: func(string) workspace.GitPort {
				return &workspace.FakeGit{Rev: r.rec.HeadSHA, FilesAt: map[string]map[string]string{r.rec.HeadSHA: files}}
			},
			EvaluateViews: views.Evaluate,
		},
		SkipReports: true,
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	return run
}

func viewOf(t *testing.T, run *conformance.Run, id string) *conformance.ViewResult {
	t.Helper()

	i := slices.IndexFunc(run.Views, func(v conformance.ViewResult) bool { return v.ID == id })
	if i < 0 {
		t.Fatalf("no view %s", id)
	}

	return &run.Views[i]
}

func viewFindings(run *conformance.Run, ruleID, viewID string) []conformance.Finding {
	var out []conformance.Finding

	for i := range run.Findings {
		if run.Findings[i].RuleID == ruleID && run.Findings[i].Source.ArchitectureID == viewID {
			out = append(out, run.Findings[i])
		}
	}

	return out
}

// maskedResults is the canonical JSON of the run without the fields that differ between two runs of the same inputs.
func maskedResults(t *testing.T, run *conformance.Run) []byte {
	t.Helper()

	text, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	var tree map[string]any
	if err := json.Unmarshal(text, &tree); err != nil {
		t.Fatal(err)
	}

	info, _ := tree["run"].(map[string]any)
	for _, k := range []string{"id", "startedAt", "finishedAt", "host"} {
		delete(info, k)
	}

	out, err := canonjson.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}

	return append(out, '\n')
}

// vcWriteGolden rewrites the golden of the view results.
func vcWriteGolden(t *testing.T, got []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(viewsGoldenPath), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(viewsGoldenPath, got, 0o600); err != nil {
		t.Fatal(err)
	}
}

// vcRequireValidResult fails the test when the result of the run does not validate against the schema.
func vcRequireValidResult(t *testing.T, run *conformance.Run) {
	t.Helper()

	full, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	if errs := conformance.ValidateResultJSON(full); len(errs) > 0 {
		t.Fatalf("the result does not validate: %v", errs)
	}
}

func vcViewIDs(run *conformance.Run) []string {
	ids := make([]string, len(run.Views))
	for i := range run.Views {
		ids[i] = run.Views[i].ID
	}

	return ids
}

// The results of a run with views: `views` holds one entry for each declared view, sorted by ID, with the GENERATED_FROM
// link and the assessment-currency interface; the golden test/golden/views/results.json pins the whole document, and
// the schema (schemaVersion 1.1) accepts it. CONCORDIA_UPDATE_GOLDEN=1 rewrites the golden.
func TestViewCheckResultsGolden(t *testing.T) {
	repo := newVCRepo(t, vcBase)
	run := repo.checkViews(t, repo.committedViews(t), vcDraftDoc)

	got := maskedResults(t, run)

	vcRequireValidResult(t, run)

	if os.Getenv(updateGoldenEnv) != "" {
		vcWriteGolden(t, got)

		return
	}

	want, err := os.ReadFile(viewsGoldenPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(want, got) {
		t.Errorf("results.json differs from %s (CONCORDIA_UPDATE_GOLDEN=1 rewrites it)", viewsGoldenPath)
	}

	// A second run in another working copy gives the same bytes.
	second := newVCRepo(t, vcBase)
	if again := maskedResults(t, second.checkViews(t, second.committedViews(t), vcDraftDoc)); !bytes.Equal(again, got) {
		t.Error("a second run gives other bytes")
	}

	ids := vcViewIDs(run)
	if !slices.IsSorted(ids) || len(ids) != 6 {
		t.Errorf("views are not sorted by ID: %v", ids)
	}
}

// vcIsDraftPresenceFinding reports whether the only finding says that the draft is missing from the complete listing.
func vcIsDraftPresenceFinding(found []conformance.Finding, headSHA string) bool {
	if len(found) != 1 || found[0].Location == nil {
		return false
	}

	listing := "repository listing of revision " + headSHA + " is complete"

	return strings.Contains(found[0].Observed, listing) && found[0].Location.Path == vcDraftDoc
}

// vcIsRegeneratedAndPresent reports whether the view is present and its freshness is verified by regenerating it.
func vcIsRegeneratedAndPresent(view *conformance.ViewResult) bool {
	if view.Presence.Status != conformance.VerdictPass || view.Freshness.Status != conformance.VerdictPass {
		return false
	}

	return view.Freshness.Verification == views.VerificationRegenerated
}

// vcIsPresenceOnlyUnknown reports whether the view is present and its correspondence is unknown with method presence-only.
func vcIsPresenceOnlyUnknown(view *conformance.ViewResult) bool {
	if view.Presence.Status != conformance.VerdictPass || view.Correspondence.Status != conformance.VerdictUnknown {
		return false
	}

	return view.Correspondence.Method == views.MethodPresenceOnly
}

// vcDiagramAreaViolatesAllViews reports whether the diagram currency area is a violation over the six views.
func vcDiagramAreaViolatesAllViews(run *conformance.Run) bool {
	area := run.Areas.DiagramCurrency

	return area.Verdict == conformance.AreaVerdict(conformance.VerdictViolation) && area.Counts["views"] == 6
}

// A14 through the pipeline: the draft diagram is in the index workspace and not in the tracked revision, so presence is a
// violation and the finding says so; the other views keep their own verdicts (independent checks).
func TestViewCheckA14AndIndependentVerdicts(t *testing.T) {
	repo := newVCRepo(t, vcBase)

	// The draft is in the CBM graph only: it is not tracked and it is not in the working tree either.
	if err := os.Remove(filepath.Join(repo.dir, filepath.FromSlash(vcDraftDoc))); err != nil {
		t.Fatal(err)
	}

	run := repo.checkViews(t, repo.committedViews(t), vcDraftDoc)

	draft := viewOf(t, run, vcDraftView)
	if draft.Presence.Status != conformance.VerdictViolation || !slices.Contains(draft.Presence.Reasons, views.ReasonIndexOnly) {
		t.Fatalf("draft presence = %+v", draft.Presence)
	}

	found := viewFindings(run, views.RulePresence, vcDraftView)
	if !vcIsDraftPresenceFinding(found, repo.rec.HeadSHA) {
		t.Errorf("draft findings = %+v", found)
	}

	containers := viewOf(t, run, vcContainers)
	if !vcIsRegeneratedAndPresent(containers) {
		t.Errorf("containers = %+v", containers)
	}

	if run.Summary.ExitCode != diag.Violation {
		t.Errorf("exit code = %d, want %d (presence violation)", run.Summary.ExitCode, diag.Violation)
	}

	// The image-only view: presence passes, correspondence is unknown with method presence-only (A19), and
	// `check.unknown: fail` makes the unknown a rule verdict that the report cannot call a pass.
	image := viewOf(t, run, vcImageView)
	if !vcIsPresenceOnlyUnknown(image) {
		t.Errorf("image view = %+v", image)
	}

	if !vcDiagramAreaViolatesAllViews(run) {
		t.Errorf("area = %+v", run.Areas.DiagramCurrency)
	}
}

// vcIsViewDriftFinding reports whether the finding is a view-drift that asks to correct the intent.
func vcIsViewDriftFinding(f *conformance.Finding) bool {
	return f.Kind == views.KindViewDrift && f.NextAction == views.NextActionCorrectIntent
}

// A16 through the pipeline: the generated view is fresh and the implementation has a relationship that CALM lacks, so
// freshness passes and correspondence is a violation: each check has its own verdict.
func TestViewCheckA16FreshRenderOfStaleIntent(t *testing.T) {
	repo := newVCRepo(t, vcDrift)
	run := repo.checkViews(t, repo.committedViews(t), vcDraftDoc)

	containers := viewOf(t, run, vcContainers)
	if containers.Freshness.Status != conformance.VerdictPass || containers.Correspondence.Status != conformance.VerdictViolation {
		t.Fatalf("containers: freshness %s, correspondence %s", containers.Freshness.Status, containers.Correspondence.Status)
	}

	found := viewFindings(run, views.RuleCorrespondence, vcContainers)
	if len(found) == 0 || !vcIsViewDriftFinding(&found[0]) {
		t.Errorf("findings = %+v", found)
	}
}

// vcDiagramFingerprints gives the sorted fingerprints of the correspondence findings of the diagram view under the
// recording, with the billing mapping removed from the workspace.
func vcDiagramFingerprints(t *testing.T, recording string) []string {
	t.Helper()

	unmapBilling := vcEdit{path: workspaceFile, from: "        billing: billing-service\n", to: ""}
	repo := newVCRepo(t, recording, unmapBilling)
	run := repo.checkViews(t, repo.committedViews(t), vcDraftDoc)

	found := viewFindings(run, views.RuleCorrespondence, vcDiagramView)

	out := make([]string, 0, len(found))
	for i := range found {
		out = append(out, found[i].Fingerprint)
	}

	slices.Sort(out)

	return out
}

// A18 through the pipeline: the provider ordinal of a diagram block changes when the blocks swap places, and the
// fingerprints of the findings of the diagram view do not. The workspace maps the diagram so that it differs from the
// declared model, so the view has a correspondence finding under both recordings.
func TestViewCheckA18FingerprintsSurviveABlockReorder(t *testing.T) {
	base, reordered := vcDiagramFingerprints(t, vcBase), vcDiagramFingerprints(t, vcReordered)
	if len(base) == 0 || len(reordered) == 0 {
		t.Fatalf("%s has no correspondence finding: base %v, reordered %v", vcDiagramView, base, reordered)
	}

	if !slices.Equal(base, reordered) {
		t.Errorf("fingerprints changed with the block order:\n%v\n%v", base, reordered)
	}
}
