package evidence_test

import (
	"context"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// TestEmptyGlobIsAWarningPerPattern: a glob with no file is CO-PRV-011 naming the pattern, and the input is recorded
// as empty so that the obligations that need it are unknown.
func TestEmptyGlobIsAWarningPerPattern(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "build/results/found.json", bareStatement(t, statement.PredicateCheckResult, checkResult("unit-tests", "passed", "production"), "aa"))

	cfg := &workspace.Assurance{
		Provenance:   &workspace.AssuranceProvenance{Statements: []string{"build/attestations/*.json"}},
		Records:      &workspace.AssuranceRecords{Deployments: []string{"build/records/deploy-*.json"}, Observations: []string{"build/records/observed-*.json"}},
		Verification: &workspace.AssuranceVerification{Results: []string{"build/results/*.json", "build/other/*.json"}},
		Quality:      &workspace.AssuranceQuality{Measurements: []string{"build/measurements/*.json"}},
	}

	got := mustLoad(t, context.Background(), options(root, cfg, ""))

	var warned []string

	for i := range got.Diagnostics {
		if got.Diagnostics[i].Code == diag.PRV011 {
			warned = append(warned, got.Diagnostics[i].Source.URI)
		}
	}

	want := []string{
		"build/attestations/*.json", "build/measurements/*.json", "build/other/*.json",
		"build/records/deploy-*.json", "build/records/observed-*.json",
	}
	if !slices.Equal(warned, want) {
		t.Errorf("CO-PRV-011 for %v, want %v", warned, want)
	}

	for kind, empty := range map[string]bool{
		evidence.KindProvenance: true, evidence.KindDeployment: true, evidence.KindObservation: true,
		evidence.KindMeasurement: true,
		// One of the two result globs matched: the input is not empty.
		evidence.KindCheckResult: false,
		// Nothing declared for plans.
		evidence.KindPlan: false,
	} {
		expectEqual(t, "Empty("+kind+")", got.Empty(kind), empty)
	}
}

// plansByFile loads the plans of the configuration and indexes them by the base name of the file.
func plansByFile(t *testing.T, root string, cfg *workspace.Assurance) (map[string]evidence.Plan, *evidence.Evidence) {
	t.Helper()

	got := mustLoad(t, context.Background(), options(root, cfg, ""))

	byFile := map[string]evidence.Plan{}
	for i := range got.Plans {
		byFile[path.Base(got.Plans[i].Declared.File)] = got.Plans[i]
	}

	expectEqual(t, "plans", len(got.Plans), 5)

	return byFile, got
}

// plansFixture writes the plan files of TestPlansAreReadAndBound and gives the configuration that declares them.
func plansFixture(t *testing.T, root string) *workspace.Assurance {
	t.Helper()

	copyAs(t, root, "plans/terraform-1.2-create.json", "build/plans/create.json")
	copyAs(t, root, "plans/terraform-1.2-create.json", "build/plans/bound.json")
	copyAs(t, root, "plans/terraform-1.2-create.json", "build/plans/other.json")
	copyAs(t, root, "plans/unsupported-version.json", "build/plans/unsupported.json")

	planDigest := strings.TrimPrefix(digestOf(t, filepath.Join(root, "build", "plans", "create.json")), "sha256:")
	predicate := map[string]any{
		"environment": "production", "revision": revision, "tool": "terraform", "time": now.Add(-time.Hour).Format(time.RFC3339),
	}

	writeFile(t, root, "build/plans/bound.intoto.json", bareStatement(t, statement.PredicatePlan, predicate, planDigest))
	writeFile(t, root, "build/plans/other.intoto.json", bareStatement(t, statement.PredicatePlan, predicate, strings.Repeat("0", 64)))

	return &workspace.Assurance{Plans: []workspace.AssurancePlan{
		{Environment: "production", Tool: "terraform", File: "build/plans/create.json"},
		{Environment: "production", Tool: "terraform", File: "build/plans/bound.json", Statement: "build/plans/bound.intoto.json"},
		{Environment: "production", Tool: "terraform", File: "build/plans/other.json", Statement: "build/plans/other.intoto.json"},
		{Environment: "production", Tool: "terraform", File: "build/plans/missing.json"},
		{Environment: "production", Tool: "terraform", File: "build/plans/unsupported.json"},
	}}
}

// planIsUnbound tells whether the plan was read and is not admissible, for it has no statement.
func planIsUnbound(p *evidence.Plan) bool {
	return p.Plan != nil && p.Coverage == assurance.CoverageComplete && !p.Binding.Admissible() && p.Statement == nil &&
		p.Binding.Admission.Integrity == assurance.IntegrityNotVerified
}

// planIsBoundByDigest tells whether the statement of the plan covers the digest of its file.
func planIsBoundByDigest(p *evidence.Plan) bool {
	return p.Statement != nil && p.Statement.Kind == evidence.KindPlan && p.Binding.Admission.Integrity == assurance.IntegrityDigest
}

// planHasGap tells whether the plan was not read and has exactly the one gap.
func planHasGap(p *evidence.Plan, kind string) bool {
	return p.Plan == nil && len(p.Gaps) == 1 && p.Gaps[0].Kind == kind
}

// TestPlansAreReadAndBound covers a plan without a statement, with a statement for its digest, with a statement for
// another file, a plan that is missing, and one the reader does not support.
func TestPlansAreReadAndBound(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	byFile, got := plansByFile(t, root, plansFixture(t, root))

	create, bound, other := byFile["create.json"], byFile["bound.json"], byFile["other.json"]
	missing, unsupported := byFile["missing.json"], byFile["unsupported.json"]

	expectThat(t, planIsUnbound(&create), "plan without statement", create.Binding)
	expectThat(t, planIsBoundByDigest(&bound), "bound plan", bound.Binding.Admission)
	expectThat(t, slices.Contains(other.Binding.Admission.Details, assurance.DetailIntegrityFailure), "plan with another digest", other.Binding.Admission)
	expectThat(t, planHasGap(&missing, evidence.GapPlanMissing) && missing.Coverage == assurance.CoverageFailed, "missing plan", missing)
	expectThat(t, planHasGap(&unsupported, evidence.GapPlanUnusable), "unsupported plan", unsupported)

	for _, code := range []string{diag.PRV008, diag.PRV009} {
		expectCode(t, got.Diagnostics, code)
	}

	// Two plan files give a warning each: the missing one and the unsupported one.
	expectEqual(t, "CO-PRV-008 count", strings.Count(strings.Join(codes(got.Diagnostics), " "), diag.PRV008), 2)
}

func TestPlanOutsideTheRootIsAConfigurationError(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Assurance{Plans: []workspace.AssurancePlan{{Environment: "production", Tool: "terraform", File: "../plan.json"}}}

	_, err := evidence.Load(context.Background(), options(t.TempDir(), cfg, ""))

	expectDiagError(t, err, diag.PRV002, diag.InvalidInput)
}

func TestPinsFromLock(t *testing.T) {
	t.Parallel()

	if pins := evidence.PinsFromLock(nil); pins != nil {
		t.Errorf("pins of no lock = %v", pins)
	}

	lock := workspace.NewLock("1.0.0", "", workspace.RendererPin{Tool: workspace.RenderToolHelm, Version: "v4.3.0", SHA256: "sha256:aa"})

	pins := evidence.PinsFromLock(lock)
	expectEqual(t, "pins", len(pins), 1)
	expectEqual(t, "helm version", pins["helm"].Version, "v4.3.0")
	expectEqual(t, "helm digest", pins["helm"].SHA256, "sha256:aa")
}
