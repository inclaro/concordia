package observation_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

// profileFamilies gives the families of the shipped CBM 0.12.1 profile as declared.
func profileFamilies(t *testing.T) map[string]observation.FamilyState {
	t.Helper()

	p, ok := observation.ProfileFor("0.12.1", observation.AdapterSchema)
	if !ok {
		t.Fatal("no profile for CBM 0.12.1")
	}

	return observation.DeclaredFamilies(p)
}

func recordOf(t *testing.T, records []observation.PredicateCoverage, predicate string, scope observation.CoverageScope) *observation.PredicateCoverage {
	t.Helper()

	for i := range records {
		if records[i].Predicate == predicate && records[i].Scope == scope {
			return &records[i]
		}
	}

	t.Fatalf("no record for %s in %+v", predicate, scope)

	return nil
}

func gapOf(t *testing.T, rec *observation.PredicateCoverage, mode string) observation.CoverageGap {
	t.Helper()

	for _, m := range rec.LossModes {
		if m.ID == mode {
			if len(m.Gaps) == 0 {
				t.Fatalf("%s: loss mode %s has no gap", rec.Predicate, mode)
			}

			return m.Gaps[0]
		}
	}

	t.Fatalf("%s: loss mode %s missing in %+v", rec.Predicate, mode, rec.LossModes)

	return observation.CoverageGap{}
}

func indexed(project string) observation.ProjectScope {
	rec := observation.IndexRecord{Project: project, GraphGeneration: "g"}

	return observation.ProjectScope{Name: project, Index: &rec}
}

func baseInput(t *testing.T) *observation.PredicateInput {
	t.Helper()

	return &observation.PredicateInput{
		Families:     profileFamilies(t),
		Projects:     []observation.ProjectScope{indexed("app")},
		Environments: []observation.EnvironmentScope{{ID: "prod", Projects: []string{"app"}, CrossChecked: true}},
	}
}

// coverageCase is one expectation about a predicate record: its status, and
// optionally a reason, loss modes, and the container of the merged-occurrence gap.
type coverageCase struct {
	// mutate changes the base input; nil keeps it.
	mutate    func(*observation.PredicateInput)
	scope     observation.CoverageScope
	name      string
	predicate string
	status    observation.CoverageStatus
	// reason is a reason that the record must state; "" asks for none.
	reason string
	// container is the container of the merged-occurrences gap; "" asks for none.
	container string
	lossModes []string
	// noLoss asks for a record without loss modes.
	noLoss bool
}

func runCoverageCases(t *testing.T, cases []coverageCase) {
	t.Helper()

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := baseInput(t)
			if tc.mutate != nil {
				tc.mutate(in)
			}

			rec := recordOf(t, observation.ComputePredicateCoverage(in), tc.predicate, tc.scope)
			requireCoverageCase(t, rec, tc)
		})
	}
}

func requireCoverageCase(t *testing.T, rec *observation.PredicateCoverage, tc *coverageCase) {
	t.Helper()

	obsEqual(t, tc.predicate+" status", rec.Status, tc.status)

	if tc.reason != "" {
		obsRequire(t, slices.Contains(rec.Reasons, tc.reason), fmt.Sprintf("%s lacks the reason %q: %+v", tc.predicate, tc.reason, rec))
	}

	for _, mode := range tc.lossModes {
		obsRequire(t, rec.HasLossMode(mode), fmt.Sprintf("%s lacks %s: %+v", tc.predicate, mode, rec.LossModes))
	}

	if tc.noLoss {
		obsEqual(t, tc.predicate+" unresolved", rec.Unresolved, 0)
		obsEqual(t, tc.predicate+" loss modes", len(rec.LossModes), 0)
	}

	if tc.container != "" {
		obsEqual(t, "merged container", gapOf(t, rec, observation.LossMergedOccurrences).Container, tc.container)
	}
}

func withFamilyState(family string, change func(*observation.FamilyState)) func(*observation.PredicateInput) {
	return func(in *observation.PredicateInput) {
		state := in.Families[family]
		change(&state)
		in.Families[family] = state
	}
}

func withGaps(gaps ...observation.CoverageGap) func(*observation.PredicateInput) {
	return func(in *observation.PredicateInput) { in.Gaps = gaps }
}

func withoutCrossCheck(in *observation.PredicateInput) {
	in.Environments[0].CrossChecked = false
}

func TestPredicateCoverageStatuses(t *testing.T) {
	t.Parallel()

	app := observation.CoverageScope{Project: "app"}

	runCoverageCases(t, []coverageCase{
		{
			name: "complete for a supported family without loss", predicate: "calls", scope: app,
			status: observation.CoverageComplete, noLoss: true,
		},
		{
			name: "partial for a partial family, with the loss modes of the profile", predicate: "selects-workload", scope: app,
			status:    observation.CoveragePartial,
			lossModes: []string{observation.LossSelectorJoinDirectoryScoped, observation.LossSelectorJoinOneOfCompeting},
		},
		{
			name: "unsupported for an unsupported family", predicate: "calls-grpc", scope: app,
			status: observation.CoverageUnsupported, reason: observation.ReasonFamilyUnsupported,
		},
		{
			name: "unknown for a family that the profile does not declare", predicate: "calls", scope: app,
			mutate: func(in *observation.PredicateInput) { delete(in.Families, "calls") },
			status: observation.CoverageUnknown, reason: observation.ReasonFamilyNotDeclared,
		},
		{
			name: "a missing required property is a reason", predicate: "calls", scope: app,
			mutate: withFamilyState("calls", func(s *observation.FamilyState) {
				s.Level, s.MissingProperties = observation.SupportPartial, []string{"callee"}
			}),
			status: observation.CoveragePartial, reason: "required property missing: callee",
		},
		{
			name: "a family without native edges in the graph keeps its level and says so", predicate: "calls", scope: app,
			mutate: withFamilyState("calls", func(s *observation.FamilyState) { s.NotPresent = true }),
			status: observation.CoverageComplete, reason: observation.ReasonNoNativeEdges,
		},
	})
}

func TestPredicateCoverageFailedProjectFailsItsEnvironments(t *testing.T) {
	t.Parallel()

	in := baseInput(t)
	in.Failures = []observation.ScopeFailure{{Project: "app", Reason: "CO-OBS-001: query failed"}}
	records := observation.ComputePredicateCoverage(in)

	for _, scope := range []observation.CoverageScope{{Project: "app"}, {Environment: "prod"}} {
		rec := recordOf(t, records, "uses-image", scope)
		obsEqual(t, "uses-image status", rec.Status, observation.CoverageFailed)
		obsRequire(t, slices.Contains(rec.Reasons, "scope failed: CO-OBS-001: query failed"), fmt.Sprintf("uses-image in %+v = %+v", scope, rec))
	}
}

func TestPredicateCoverageWithoutProfileCodeFamiliesAreComplete(t *testing.T) {
	t.Parallel()

	in := baseInput(t)
	in.Families = nil
	records := observation.ComputePredicateCoverage(in)
	app := observation.CoverageScope{Project: "app"}

	obsEqual(t, "imports", recordOf(t, records, "imports", app).Status, observation.CoverageComplete)
	obsEqual(t, "uses-image", recordOf(t, records, "uses-image", app).Status, observation.CoverageUnsupported)
}

// lossCases lists each loss mode of the CBM 0.12.1 observations with a predicate that it applies to.
var lossCases = []struct {
	mode      string
	predicate string
	scope     observation.CoverageScope
	// inherent is true for a loss mode that the profile states for a partial family as a whole (no gap is needed).
	inherent bool
}{
	{observation.LossMergedOccurrences, "uses-image", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossMergedOccurrences, "container-occurrence-versions", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossTargetNotDeclared, "references-secret", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossTargetNotDeclared, "route-attaches-to-gateway", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossEmptySelector, "policy-selects-workload", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossUnsupportedSelectorExpression, "policy-selects-workload", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossSelectorJoinDirectoryScoped, "selects-workload", observation.CoverageScope{Environment: "prod"}, true},
	{observation.LossSelectorJoinOneOfCompeting, "selects-workload", observation.CoverageScope{Environment: "prod"}, true},
	{observation.LossRouteIdentityUnscoped, "routes-to", observation.CoverageScope{Environment: "prod"}, true},
	{observation.LossDeployTargetIsFile, "deploys-artifact", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossBuildLinkNeedsBuildCommand, "built-from", observation.CoverageScope{Environment: "prod"}, true},
	{observation.LossOverrideUnsignalled, "overrides", observation.CoverageScope{Environment: "prod"}, true},
	{observation.LossUnrenderedTemplate, "rendered-templates", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossUnrenderedTemplate, "uses-image", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossDefaultExcludedDirectory, "deploys-artifact", observation.CoverageScope{Environment: "prod"}, false},
	{observation.LossDefaultExcludedDirectory, "index-result", observation.CoverageScope{Project: "app"}, false},
	{observation.LossRasterNotIndexed, "diagram-arrow", observation.CoverageScope{Project: "app"}, false},
	{observation.LossRasterNotIndexed, "diagram-identity", observation.CoverageScope{Project: "app"}, false},
	{observation.LossParsePartial, "index-result", observation.CoverageScope{Project: "app"}, false},
	{observation.LossParsePartial, "references-secret", observation.CoverageScope{Environment: "prod"}, false},
}

// requireGapLocation checks that the record keeps the location of the gap that the test fed in.
func requireGapLocation(t *testing.T, rec *observation.PredicateCoverage, mode string) {
	t.Helper()

	gap := gapOf(t, rec, mode)
	obsEqual(t, "gap path", gap.Path, "k8s/prod/x.yaml")
	obsEqual(t, "gap line", gap.Line, 7)
	obsEqual(t, "gap workload", gap.Workload, "Deployment/api")
	obsRequire(t, rec.Unresolved >= 1, fmt.Sprintf("unresolved = %d", rec.Unresolved))
}

func TestPredicateCoverageEachLossModeHasItsLocation(t *testing.T) {
	t.Parallel()

	for _, tc := range lossCases {
		t.Run(tc.mode+" in "+tc.predicate, func(t *testing.T) {
			t.Parallel()

			in := baseInput(t)
			in.Gaps = []observation.CoverageGap{{
				Reason: tc.mode, Path: "k8s/prod/x.yaml", Line: 7, Project: "app", Environment: "prod", Workload: "Deployment/api",
			}}

			rec := recordOf(t, observation.ComputePredicateCoverage(in), tc.predicate, tc.scope)
			obsEqual(t, "status", rec.Status, observation.CoveragePartial)
			requireGapLocation(t, rec, tc.mode)

			if !tc.inherent {
				return
			}

			// The profile states the mode for the whole family: it is there without any gap.
			bare := recordOf(t, observation.ComputePredicateCoverage(baseInput(t)), tc.predicate, tc.scope)
			obsEqual(t, "status without gap", bare.Status, observation.CoveragePartial)
			obsRequire(t, bare.HasLossMode(tc.mode), fmt.Sprintf("without gap: %+v", bare))
		})
	}
}

func TestPredicateCoverageSupportedFamilyIsCompleteWithoutTheGap(t *testing.T) {
	t.Parallel()

	records := observation.ComputePredicateCoverage(baseInput(t))
	for _, p := range []string{"uses-image", "references-secret", "deploys-artifact", "references-storage"} {
		obsEqual(t, p, recordOf(t, records, p, observation.CoverageScope{Environment: "prod"}).Status, observation.CoverageComplete)
	}
}

func TestPredicateCoverageGapOfOtherScopeDoesNotApply(t *testing.T) {
	t.Parallel()

	in := baseInput(t)
	in.Environments = append(in.Environments, observation.EnvironmentScope{ID: "staging", Projects: []string{"app"}, CrossChecked: true})
	in.Gaps = []observation.CoverageGap{{Reason: observation.LossMergedOccurrences, Path: "k8s/prod/deployment.yaml", Line: 3, Environment: "prod", Workload: "Deployment/api", Container: "app"}}
	records := observation.ComputePredicateCoverage(in)

	obsEqual(t, "staging uses-image", recordOf(t, records, "uses-image", observation.CoverageScope{Environment: "staging"}).Status, observation.CoverageComplete)

	prod := recordOf(t, records, "uses-image", observation.CoverageScope{Environment: "prod"})
	obsEqual(t, "prod uses-image", prod.Status, observation.CoveragePartial)
	obsEqual(t, "prod merged container", gapOf(t, prod, observation.LossMergedOccurrences).Container, "app")
}

func TestContainerOccurrenceVersionsPredicate(t *testing.T) {
	t.Parallel()

	prod := observation.CoverageScope{Environment: "prod"}
	versions := observation.FamilyContainerOccurrenceVersions

	runCoverageCases(t, []coverageCase{
		{
			name: "unknown without the cross-check, with the loss mode of the profile", predicate: versions, scope: prod,
			mutate: withoutCrossCheck,
			status: observation.CoverageUnknown, lossModes: []string{observation.LossMergedOccurrences},
		},
		{
			name: "partial by profile when the sources were read in a project scope", predicate: versions, scope: observation.CoverageScope{Project: "app"},
			status: observation.CoveragePartial, lossModes: []string{observation.LossMergedOccurrences},
		},
		{
			name: "complete when the cross-check finds an occurrence for every container", predicate: versions, scope: prod,
			status: observation.CoverageComplete, noLoss: true,
		},
		{
			name: "partial with the merged container and its name", predicate: versions, scope: prod,
			mutate: withGaps(observation.CoverageGap{
				Reason: observation.LossMergedOccurrences, Path: "k8s/prod/deployment.yaml", Line: 22, Environment: "prod", Workload: "Deployment/api", Container: "app",
			}),
			status: observation.CoveragePartial, container: "app",
		},
		{
			name: "partial with unrendered-template for a templated workload", predicate: versions, scope: prod,
			mutate: withGaps(observation.CoverageGap{Reason: observation.LossUnrenderedTemplate, Path: "charts/api/templates/deployment.yaml", Line: 1, Environment: "prod"}),
			status: observation.CoveragePartial, lossModes: []string{observation.LossUnrenderedTemplate},
		},
		{
			name: "unknown for an environment that was not cross-checked when the profile family is supported", predicate: "uses-image", scope: prod,
			mutate: withoutCrossCheck,
			status: observation.CoverageUnknown, reason: observation.ReasonCrossCheckNotRun,
		},
	})
}

func TestPredicateCoverageUnknownWithoutIndexResult(t *testing.T) {
	t.Parallel()

	in := baseInput(t)
	in.Projects = []observation.ProjectScope{{Name: "app"}}
	records := observation.ComputePredicateCoverage(in)

	rec := recordOf(t, records, "uses-image", observation.CoverageScope{Environment: "prod"})
	obsEqual(t, "uses-image status", rec.Status, observation.CoverageUnknown)
	obsRequire(t, slices.Contains(rec.Reasons, "index result unavailable"), fmt.Sprintf("uses-image = %+v", rec))

	// The code families of the project do not depend on the index result.
	obsEqual(t, "calls", recordOf(t, records, "calls", observation.CoverageScope{Project: "app"}).Status, observation.CoverageComplete)
	obsEqual(t, "index-result", recordOf(t, records, "index-result", observation.CoverageScope{Project: "app"}).Status, observation.CoverageUnknown)
}

func marshalPredicateCoverage(t *testing.T, in *observation.PredicateInput) []byte {
	t.Helper()

	out, err := json.Marshal(observation.ComputePredicateCoverage(in))
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func TestPredicateCoverageIsDeterministic(t *testing.T) {
	t.Parallel()

	gaps := []observation.CoverageGap{
		{Reason: observation.LossTargetNotDeclared, Path: "a.yaml", Line: 2, Environment: "prod", Target: "x", TargetKind: "Secret", ReferenceMode: "env"},
		{Reason: observation.LossMergedOccurrences, Path: "b.yaml", Line: 9, Environment: "prod", Container: "c"},
		{Reason: observation.LossTargetNotDeclared, Path: "a.yaml", Line: 1, Environment: "prod", Target: "y", TargetKind: "Secret", ReferenceMode: "env"},
	}

	first, second := baseInput(t), baseInput(t)
	first.Gaps = gaps
	second.Gaps = []observation.CoverageGap{gaps[2], gaps[0], gaps[1], gaps[0]}

	if !bytes.Equal(marshalPredicateCoverage(t, first), marshalPredicateCoverage(t, second)) {
		t.Error("the records depend on the order of the gaps")
	}
}

func factGapsResult() *observation.EvidenceQueryResult {
	return &observation.EvidenceQueryResult{
		Nodes: map[string]observation.ObservedNode{
			"1": {ObservedID: "1", Label: "Resource", FilePath: "charts/api/templates/deployment.yaml", StartLine: 2, Properties: map[string]string{"obj_key": "apps/Deployment/?/{{ .Release.Name }}-api"}},
			"2": {ObservedID: "2", Label: "Resource", FilePath: "k8s/prod/deployment.yaml", StartLine: 1, Properties: map[string]string{"obj_key": "apps/Deployment/payments/api"}},
			"3": {ObservedID: "3", Label: "File", FilePath: "scripts/deploy.sh"},
			"4": {ObservedID: "4", Label: "Resource", FilePath: "concordia.yaml", StartLine: 1, Properties: map[string]string{"obj_key": "concordia.io/Workspace/?/commerce-platform"}},
		},
		Edges: []*observation.EvidenceEdge{
			{Kind: observation.KindDeploysArtifact, Properties: map[string]string{"via": "kubectl"}, From: "3", To: "3", SourceSpan: &observation.SourceSpan{Path: "scripts/deploy.sh", Line: 12}},
			{Kind: observation.KindDeploysArtifact, Properties: map[string]string{"via": "helm"}, From: "3", To: "3"},
		},
	}
}

// gapPlace is the reason and the location that a gap is expected to have.
type gapPlace struct {
	reason, path string
	line         int
}

func requireGapAt(t *testing.T, what string, gap *observation.CoverageGap, want gapPlace) {
	t.Helper()

	obsEqual(t, what+" reason", gap.Reason, want.reason)
	obsEqual(t, what+" path", gap.Path, want.path)
	obsEqual(t, what+" line", gap.Line, want.line)
}

func TestFactGaps(t *testing.T) {
	t.Parallel()

	gaps := observation.FactGaps(factGapsResult())
	obsRequire(t, len(gaps) == 2, fmt.Sprintf("gaps = %+v", gaps))

	requireGapAt(t, "deploy gap", &gaps[0], gapPlace{observation.LossDeployTargetIsFile, "scripts/deploy.sh", 12})
	requireGapAt(t, "template gap", &gaps[1], gapPlace{observation.LossUnrenderedTemplate, "charts/api/templates/deployment.yaml", 2})
}

func TestGlobOverlapsDir(t *testing.T) {
	t.Parallel()

	cases := []struct {
		glob, dir string
		want      bool
	}{
		{"deploy/prod/**", "deploy", true},
		{"deploy/**", "deploy/", true},
		{"k8s/prod/**", "deploy", false},
		{"**/*.yaml", "deploy", true},
		{"k8s/*/deployment.yaml", "deploy", false},
		{"*/deployment.yaml", "deploy", true},
		{"docs/**", "build", false},
		{"build/out/**", "build", true},
		{"deploy/prod/**", "deploy/staging", false},
		{"deploy/**", "deploy/staging", true},
		{"deploy", "deploy", true},
		{"deployments/**", "deploy", false},
		{"", "deploy", false},
	}

	for _, tc := range cases {
		if got := observation.GlobOverlapsDir(tc.glob, tc.dir); got != tc.want {
			t.Errorf("GlobOverlapsDir(%q, %q) = %v, want %v", tc.glob, tc.dir, got, tc.want)
		}
	}
}

func indexGapEnvironments() []observation.EnvironmentSources {
	return []observation.EnvironmentSources{
		{ID: "edge", Sources: []string{"deploy/**"}},
		{ID: "production", Sources: []string{"k8s/prod/**"}},
	}
}

func indexGapRecord() observation.IndexRecord {
	return observation.IndexRecord{
		Project: "app", ExcludedDirs: []string{".git", "deploy"},
		NotIndexedFiles: []observation.PathReason{{Path: "docs/context.png", Reason: "ignored-suffix"}, {Path: "notes.bin", Reason: "ignored-suffix"}},
		ParsePartial:    []observation.PartialFile{{Path: "charts/api/templates/deployment.yaml", ErrorRanges: "3-9,12-14"}},
	}
}

func gapsByReason(gaps []observation.CoverageGap) map[string][]observation.CoverageGap {
	byReason := map[string][]observation.CoverageGap{}
	for i := range gaps {
		byReason[gaps[i].Reason] = append(byReason[gaps[i].Reason], gaps[i])
	}

	return byReason
}

// soleGap gives the only gap of a reason, and fails the test when there is not exactly one.
func soleGap(t *testing.T, byReason map[string][]observation.CoverageGap, reason string) observation.CoverageGap {
	t.Helper()

	gaps := byReason[reason]
	obsRequire(t, len(gaps) == 1, fmt.Sprintf("%s = %+v", reason, gaps))

	return gaps[0]
}

func TestIndexGaps(t *testing.T) {
	t.Parallel()

	envs := indexGapEnvironments()
	rec := indexGapRecord()
	byReason := gapsByReason(observation.IndexGaps(&rec, envs))

	excluded := soleGap(t, byReason, observation.LossDefaultExcludedDirectory)
	obsEqual(t, "excluded environment", excluded.Environment, "edge")
	obsEqual(t, "excluded path", excluded.Path, "deploy/")
	obsEqual(t, "excluded source", excluded.Source, "deploy/**")
	obsContains(t, "excluded hint", excluded.Hint, "`!deploy/`")
	obsContains(t, "excluded hint", excluded.Hint, ".cbmignore")

	obsEqual(t, "raster path", soleGap(t, byReason, observation.LossRasterNotIndexed).Path, "docs/context.png")

	partial := soleGap(t, byReason, observation.LossParsePartial)
	obsEqual(t, "parse-partial line", partial.Line, 3)
	obsContains(t, "parse-partial detail", partial.Detail, "3-9,12-14")

	obsEqual(t, "unrendered path", soleGap(t, byReason, observation.LossUnrenderedTemplate).Path, "charts/api/templates/deployment.yaml")
}

func TestIndexGapsReincludedDirectoryHasNoGap(t *testing.T) {
	t.Parallel()

	envs := indexGapEnvironments()
	rec := indexGapRecord()

	// `.cbmignore` with `!deploy/` re-includes the directory: only .git is excluded.
	rec.ExcludedDirs = []string{".git"}
	again := observation.IndexGaps(&rec, envs)
	obsRequire(t, !slices.ContainsFunc(again, func(g observation.CoverageGap) bool { return g.Reason == observation.LossDefaultExcludedDirectory }),
		fmt.Sprintf("a re-included directory still has a gap: %+v", again))

	if none := observation.IndexGaps(nil, envs); none != nil {
		t.Errorf("a missing index result gives gaps: %+v", none)
	}
}

func TestIndexGapsListsAFileOnceWhenTheProviderRepeatsIt(t *testing.T) {
	t.Parallel()

	rec := observation.IndexRecord{
		Project:         "app",
		NotIndexedFiles: []observation.PathReason{{Path: "a.png", Reason: "ignored-suffix"}, {Path: "a.png", Reason: "ignored-suffix"}},
		ParsePartial:    []observation.PartialFile{{Path: "t.yaml", ErrorRanges: "1-2"}, {Path: "t.yaml", ErrorRanges: "1-2"}},
	}

	gaps := observation.IndexGaps(&rec, nil)
	if len(gaps) != 3 { // raster-not-indexed, parse-partial, unrendered-template
		t.Fatalf("want one gap for each reason and file, got %+v", gaps)
	}

	for _, g := range gaps {
		if g.Reason == "" || g.Path == "" {
			t.Errorf("a duplicate left an empty gap: %+v", gaps)
		}
	}
}

func parseIndexResultBody(t *testing.T, raw string) observation.IndexResult {
	t.Helper()

	result, err := observation.ParseIndexResult([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	return result
}

func TestIndexRecordNormalizesAndKeepsCounts(t *testing.T) {
	t.Parallel()

	raw := `{"project":"p","status":"indexed","excluded":{"dirs":["./deploy/",".git"],"count":2},
	"not_indexed_files":{"files":[{"path":"b.png","reason":"ignored-suffix"},{"path":"a.png","reason":"ignored-suffix"}],"count":5},
	"parse_partial":{"files":[],"count":0}}`

	result := parseIndexResultBody(t, raw)
	rec := result.Record("p", "gen", "rev")

	obsRequire(t, slices.Equal(rec.ExcludedDirs, []string{".git", "deploy"}), fmt.Sprintf("record = %+v", rec))
	obsEqual(t, "first not indexed path", rec.NotIndexedFiles[0].Path, "a.png")
	obsEqual(t, "not indexed count", rec.NotIndexedCount, 5)
	obsRequire(t, rec.ExampleListsOnly(), fmt.Sprintf("record = %+v", rec))
	obsRequire(t, rec.ParsePartial != nil, fmt.Sprintf("record = %+v", rec))
}

func TestIndexRecordOfAFlatResult(t *testing.T) {
	t.Parallel()

	flat := parseIndexResultBody(t, `{"project":"p","not_indexed_files_count":0,"excluded":{"dirs":[".git"],"count":1}}`)

	got := flat.Record("p", "g", "")
	obsRequire(t, !got.ExampleListsOnly(), fmt.Sprintf("flat record = %+v", got))
	obsEqual(t, "excluded dirs", len(got.ExcludedDirs), 1)
}

func TestParseIndexResultRejectsABodyThatIsNotJSON(t *testing.T) {
	t.Parallel()

	if _, err := observation.ParseIndexResult([]byte("not json")); err == nil {
		t.Error("a body that is not JSON was accepted")
	}
}

func TestKnownLossModesAreSortedAndStated(t *testing.T) {
	t.Parallel()

	modes := observation.KnownLossModes()
	obsRequire(t, slices.IsSorted(modes), fmt.Sprintf("modes = %v", modes))
	obsEqual(t, "loss modes", len(modes), 14)

	// Every loss mode of the profile is a known identifier.
	for name, state := range profileFamilies(t) {
		for _, m := range state.LossModes {
			if !slices.Contains(modes, m) {
				t.Errorf("family %s states the unknown loss mode %s", name, m)
			}
		}
	}
}
