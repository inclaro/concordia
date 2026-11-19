package integration_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/conformance"
	deptrace "github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
)

// These tests check the rendered-or-evaluated level of the check on the A10 infra workspace: the occurrences of the
// rendered documents against the merged occurrence of the provider (DEP-05, spec rendered-infrastructure-evidence),
// the rendered-level coverage record (D20), and the entry of `assurance.rendered`.

func occurrenceRecord(t *testing.T, run *conformance.Run, predicate, environment string) *observation.PredicateCoverage {
	t.Helper()

	recs := conformance.CoverageOf(run.Coverage.Predicates, []string{predicate}, observation.CoverageScope{Environment: environment})
	if len(recs) != 1 {
		t.Fatalf("records of %s in %s = %d, want 1", predicate, environment, len(recs))
	}

	return &recs[0]
}

// renderedRecordShape is the part of an occurrence-version record that the rendered-level tests compare as one value.
type renderedRecordShape struct {
	status     observation.CoverageStatus
	mergedLoss bool
	resolved   int
	levels     int
	unresolved int
}

func renderedRecordShapeOf(rec *observation.PredicateCoverage) renderedRecordShape {
	return renderedRecordShape{
		status:     rec.Status,
		mergedLoss: rec.HasLossMode(observation.LossMergedOccurrences),
		resolved:   len(rec.Resolved),
		levels:     len(rec.Levels),
		unresolved: rec.Unresolved,
	}
}

func twoMutableTagsOfOneRepository(occurrences []render.ContainerOccurrence) bool {
	if len(occurrences) != 2 || slices.ContainsFunc(occurrences, func(o render.ContainerOccurrence) bool { return o.Immutable }) {
		return false
	}

	return occurrences[0].Repository == occurrences[1].Repository && occurrences[0].Tag != occurrences[1].Tag
}

func TestRenderedOccurrencesKeepTwoTagsOfOneRepository(t *testing.T) {
	t.Parallel()

	world := &provWorld{noPack: true, sidecarImage: provRepository + ":1.5.0", appImage: provRepository + ":1.4.0"}
	run := world.run(t).run

	renders := run.AssuranceEvidence.Evidence.Renders
	if len(renders) != 1 {
		t.Fatalf("renders = %d, want 1", len(renders))
	}

	occurrences := renders[0].Occurrences
	if !twoMutableTagsOfOneRepository(occurrences) {
		t.Fatalf("occurrences = %+v: want two tags of one repository, neither immutable", occurrences)
	}

	// The provider keeps one reference: the rendered documents are the only place with both.
	if rec := occurrenceRecord(t, run, observation.FamilyContainerOccurrenceVersions, "production"); rec.Status != observation.CoverageComplete {
		t.Errorf("container-occurrence-versions = %s, want complete beside a complete rendered record", rec.Status)
	}
}

func TestAnEnvironmentWithoutRenderingStaysAsTheProviderGaveIt(t *testing.T) {
	t.Parallel()

	run := (&provWorld{noRendering: true}).run(t).run

	rec := occurrenceRecord(t, run, observation.FamilyContainerOccurrenceVersions, "production")
	if got := renderedRecordShapeOf(rec); got.status != observation.CoveragePartial || !got.mergedLoss || got.resolved != 0 || got.levels != 0 {
		t.Errorf("record = %+v, want partial with merged-occurrences, no resolved gap and no levels", rec)
	}

	// A completeness rule is `unknown` with the reason `rendered-evidence-missing` and never passes.
	ren := provRuleOf(t, run, "ASR-REN-001")
	if ren.Verdict != conformance.VerdictUnknown || firstObligationReason(ren) != conformance.ReasonRenderedEvidenceMissing {
		t.Errorf("ASR-REN-001 = %s %+v", ren.Verdict, ren.Obligations)
	}
}

// firstObligationReason gives the reason of the first obligation of a rule, or the empty reason when it has none.
func firstObligationReason(rule *conformance.RuleResult) conformance.UnknownReason {
	if len(rule.Obligations) == 0 {
		return ""
	}

	return rule.Obligations[0].Reason
}

func TestRenderedRecordResolvesTheMergedOccurrenceGap(t *testing.T) {
	t.Parallel()

	d := (&provWorld{}).run(t)
	run := d.run

	rec := occurrenceRecord(t, run, observation.FamilyContainerOccurrenceVersions, "production")
	if want := (renderedRecordShape{status: observation.CoverageComplete, resolved: 1, levels: 2}); renderedRecordShapeOf(rec) != want {
		t.Fatalf("record = %+v, want complete with no applying loss mode", rec)
	}

	// The gap stays visible, marked resolved by the rendered record, and both levels are recorded.
	if !resolvedByRenderedRecordWithGap(rec.Resolved) {
		t.Errorf("resolved = %+v, want merged-occurrences resolved by %s with its gap", rec.Resolved, observation.LevelRenderedOrEvaluated)
	}

	if !slices.Equal(rec.Levels, []observation.LevelRecord{
		{Level: observation.LevelDeclared, Status: observation.CoveragePartial},
		{Level: observation.LevelRenderedOrEvaluated, Status: observation.CoverageComplete},
	}) {
		t.Errorf("levels = %+v", rec.Levels)
	}

	assertRenderingAdapterRecordComplete(t, run)

	// INFRA-TRC-004 can pass for an environment with a complete rendered record.
	if trc := d.obligationOf(t, "INFRA-TRC-004", "api-service", "production"); trc.Verdict != conformance.VerdictPass {
		t.Errorf("INFRA-TRC-004 api-service production = %s %s (%s), want pass", trc.Verdict, trc.Reason, trc.Detail)
	}

	// The staging environment has no rendering source: its records and its verdicts are those of the provider.
	staging := occurrenceRecord(t, run, observation.FamilyContainerOccurrenceVersions, "staging")
	if got := renderedRecordShapeOf(staging); got.levels != 0 || got.resolved != 0 {
		t.Errorf("staging record = %+v", staging)
	}
}

// assertRenderingAdapterRecordComplete checks the record of the rendering adapter itself.
func assertRenderingAdapterRecordComplete(t *testing.T, run *conformance.Run) {
	t.Helper()

	set := occurrenceRecord(t, run, observation.FamilyRenderedResourceSet, "production")
	if set.Status != observation.CoverageComplete || len(set.LossModes) != 0 {
		t.Errorf("rendered-resource-set = %+v", set)
	}
}

// resolvedByRenderedRecordWithGap reports whether the merged-occurrences loss mode is listed as resolved by the
// rendered level, with the gap that it cured.
func resolvedByRenderedRecordWithGap(resolved []observation.LossMode) bool {
	i := slices.IndexFunc(resolved, func(m observation.LossMode) bool { return m.ID == observation.LossMergedOccurrences })
	if i < 0 {
		return false
	}

	return resolved[i].ResolvedBy == observation.LevelRenderedOrEvaluated && len(resolved[i].Gaps) != 0
}

func TestAWorkspaceWithoutAssuranceHasNoRenderedRecord(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	if runCarriesAssuranceResults(d.run) {
		t.Fatal("a run without an assurance key carries assurance results")
	}

	for i := range d.run.Coverage.Predicates {
		rec := &d.run.Coverage.Predicates[i]
		if hasAssuranceContent(rec) {
			t.Errorf("record %s/%+v has assurance content", rec.Predicate, rec.Scope)
		}
	}

	if trc := d.obligationOf(t, "INFRA-TRC-004", "api-service", "production"); trc.Verdict == conformance.VerdictPass {
		t.Errorf("INFRA-TRC-004 passes without rendered evidence")
	}
}

func runCarriesAssuranceResults(run *conformance.Run) bool {
	if run.Assurance != nil || run.Toolchain.Assurance != nil {
		return true
	}

	return run.AssuranceEvidence != nil
}

func hasAssuranceContent(rec *observation.PredicateCoverage) bool {
	if rec.Predicate == observation.FamilyRenderedResourceSet {
		return true
	}

	return len(rec.Resolved) != 0 || len(rec.Levels) != 0
}

// traceLevelStatuses gives the status of the three levels of a deployment trace, keyed by level.
func traceLevelStatuses(trace *deptrace.Trace) map[string]string {
	levels := map[string]string{}
	for _, l := range trace.Levels {
		levels[l.Level] = l.Status
	}

	return levels
}

// traceEnvironments finds the production and the staging environment of a trace; a missing one is nil.
func traceEnvironments(trace *deptrace.Trace) (production, staging *deptrace.EnvironmentTrace) {
	for i := range trace.Environments {
		switch trace.Environments[i].Name {
		case "production":
			production = &trace.Environments[i]
		case "staging":
			staging = &trace.Environments[i]
		}
	}

	return production, staging
}

// evaluatedShape is the evaluated state of an environment as one comparable value.
type evaluatedShape struct {
	kind      string
	coverage  string
	sources   int
	found     bool
	evaluated bool
}

func evaluatedShapeOf(env *deptrace.EnvironmentTrace) evaluatedShape {
	if env == nil {
		return evaluatedShape{}
	}

	shape := evaluatedShape{found: true, evaluated: env.Evaluated.Evaluated()}
	if env.Evaluated == nil {
		return shape
	}

	shape.sources = len(env.Evaluated.Sources)
	if shape.sources == 1 {
		shape.kind = env.Evaluated.Sources[0].Kind
		shape.coverage = env.Evaluated.Sources[0].Coverage
	}

	return shape
}

func TestTraceReportsTheRenderedLevelOfAnEnvironment(t *testing.T) {
	t.Parallel()

	run := (&provWorld{}).run(t).run
	trace := &run.DeploymentTrace

	wantLevels := map[string]string{
		deptrace.LevelRenderedOrEvaluated: "evaluated",
		deptrace.LevelDeploymentExecution: "not-available",
		deptrace.LevelRuntimeObservation:  "not-available",
	}
	gotLevels := traceLevelStatuses(trace)

	for level, want := range wantLevels {
		if gotLevels[level] != want {
			t.Errorf("levels = %+v", trace.Levels)
		}
	}

	production, staging := traceEnvironments(trace)

	wantProduction := evaluatedShape{found: true, evaluated: true, sources: 1, kind: deptrace.SourceRender, coverage: string(assurance.CoverageComplete)}
	if evaluatedShapeOf(production) != wantProduction {
		t.Errorf("production = %+v", production)
	}

	if want := (evaluatedShape{found: true}); evaluatedShapeOf(staging) != want {
		t.Errorf("staging has an evaluated level: %+v", staging)
	}

	// The rendered workload that the `workload` binding reaches is listed beside the declared chain, at its own level.
	sub := subjectTrace(t, &deployment{run: run}, "api-service", "production")
	if sub.Level != deptrace.LevelDeclared {
		t.Errorf("the subject trace level = %s, want declared", sub.Level)
	}

	if !isRenderedWorkloadOfProduction(sub.Evaluated) {
		t.Errorf("evaluated entities = %+v", sub.Evaluated)
	}
}

func isRenderedWorkloadOfProduction(entities []deptrace.Entity) bool {
	if len(entities) != 1 || entities[0].Kind != deptrace.KindRenderedWorkload {
		return false
	}

	return entities[0].Locations[0].Path == "assurance-render/production/deployment.yaml"
}

// renderedEntryShape is the entry of `assurance.rendered` with the digests reduced to whether they are set.
type renderedEntryShape struct {
	environment, source, tool, confinement, coverage, level, toolVersion string
	resourceCount, gaps                                                  int
	inputDigest, outputDigest                                            bool
}

func TestRenderedEntryListsTheSourceWithDigestsAndNoValue(t *testing.T) {
	t.Parallel()

	run := (&provWorld{}).run(t).run

	if len(run.Assurance.Rendered) != 1 {
		t.Fatalf("rendered = %+v", run.Assurance.Rendered)
	}

	entry := run.Assurance.Rendered[0]
	got := renderedEntryShape{
		environment: entry.Environment, source: entry.Source, tool: entry.Tool, confinement: entry.Confinement,
		coverage: string(entry.Coverage), level: entry.Level, toolVersion: entry.ToolVersion,
		resourceCount: entry.ResourceCount, gaps: len(entry.Gaps),
		inputDigest: entry.InputDigest != "", outputDigest: entry.OutputDigest != "",
	}
	want := renderedEntryShape{
		environment: "production", source: "assurance-render/production", tool: "plain", confinement: render.ConfinementNone,
		coverage: string(assurance.CoverageComplete), level: deptrace.LevelRenderedOrEvaluated,
		resourceCount: 1, inputDigest: true, outputDigest: true,
	}

	if got != want {
		t.Errorf("entry = %+v", entry)
	}
}
