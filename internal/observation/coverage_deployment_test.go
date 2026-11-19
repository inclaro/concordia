package observation_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

const (
	workloadSet = observation.FamilyDeploymentWorkloadSet
	placedIn    = "placed-in"
)

func prodScope() observation.CoverageScope { return observation.CoverageScope{Environment: "prod"} }

// depSubtest is one named case of a test whose body is a function.
type depSubtest struct {
	run  func(t *testing.T)
	name string
}

func depRunSubtests(t *testing.T, cases []depSubtest) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t)
		})
	}
}

// depWorkloadSetRecord computes the workload-set record of the prod environment.
func depWorkloadSetRecord(t *testing.T, in *observation.PredicateInput) *observation.PredicateCoverage {
	t.Helper()

	return recordOf(t, observation.ComputePredicateCoverage(in), workloadSet, prodScope())
}

func depExpectStatus(t *testing.T, rec *observation.PredicateCoverage, want observation.CoverageStatus) {
	t.Helper()

	if rec.Status != want {
		t.Errorf("status = %s, want %s: record = %+v", rec.Status, want, rec)
	}
}

func depExpectReason(t *testing.T, rec *observation.PredicateCoverage, reason string) {
	t.Helper()

	if !slices.Contains(rec.Reasons, reason) {
		t.Errorf("reason %q missing: record = %+v", reason, rec)
	}
}

func depExpectLossMode(t *testing.T, rec *observation.PredicateCoverage, mode string) {
	t.Helper()

	if !rec.HasLossMode(mode) {
		t.Errorf("loss mode %s missing: record = %+v", mode, rec)
	}
}

func depExpectNoLossMode(t *testing.T, rec *observation.PredicateCoverage, mode string) {
	t.Helper()

	if rec.HasLossMode(mode) {
		t.Errorf("loss mode %s present: record = %+v", mode, rec)
	}
}

func depExpectGapPath(t *testing.T, rec *observation.PredicateCoverage, gap *observation.CoverageGap, want string) {
	t.Helper()

	if gap.Path != want {
		t.Errorf("gap path = %q, want %q: record = %+v", gap.Path, want, rec)
	}
}

func depExpectGapContains(t *testing.T, rec *observation.PredicateCoverage, gapText, want string) {
	t.Helper()

	if !strings.Contains(gapText, want) {
		t.Errorf("gap text %q lacks %q: record = %+v", gapText, want, rec)
	}
}

func depInputWithIndex(t *testing.T, rec *observation.IndexRecord) *observation.PredicateInput {
	t.Helper()

	in := baseInput(t)
	in.Projects = []observation.ProjectScope{{Name: "app", Index: rec}}

	return in
}

func TestDeploymentWorkloadSetStatuses(t *testing.T) {
	t.Parallel()

	depRunSubtests(t, []depSubtest{
		{name: "plain manifests that were all read are complete", run: depPlainManifestsComplete},
		{name: "manifests under deploy are partial with the re-include hint", run: depDeployDirectoryPartial},
		{name: "a re-included directory leaves no loss mode", run: depReincludedDirectory},
		{name: "a partly parsed Helm template is partial with parse-partial and unrendered-template", run: depPartlyParsedHelm},
		{name: "a Kustomize source is partial with override-unsignalled", run: depKustomizeSource},
		{name: "a Terraform or Compose source is unknown and names the file", run: depTerraformComposeSource},
		{name: "a workload with no provider node is unknown with the reason", run: depWorkloadWithoutProviderNode},
		{name: "competing declarations leave the coverage unchanged", run: depCompetingDeclarations},
		{name: "a failed scope is failed", run: depFailedScope},
		{name: "an unavailable index result is unknown", run: depUnavailableIndex},
		{name: "an environment that the cross-check did not read is unknown", run: depCrossCheckNotRun},
		{name: "no profile is unsupported", run: depNoProfile},
		{name: "a Dockerfile gap does not limit the workload set", run: depDockerfileGapIgnored},
	})
}

func depPlainManifestsComplete(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Environments[0].Workloads = []observation.WorkloadDocument{
		{Kind: "Deployment", Name: "api", Namespace: "payments", Environment: "prod", Path: "k8s/prod/deployment.yaml", Line: 1, ProviderNode: true},
	}
	rec := depWorkloadSetRecord(t, in)

	depExpectStatus(t, rec, observation.CoverageComplete)

	if len(rec.LossModes) != 0 {
		t.Errorf("loss modes = %+v", rec.LossModes)
	}

	if len(rec.Reasons) != 0 {
		t.Errorf("reasons = %+v", rec.Reasons)
	}
}

func depDeployDirectoryPartial(t *testing.T) {
	t.Helper()

	rec := observation.IndexRecord{Project: "app", GraphGeneration: "g", ExcludedDirs: []string{".git", "deploy"}}
	in := depInputWithIndex(t, &rec)
	in.Gaps = observation.IndexGaps(&rec, []observation.EnvironmentSources{{ID: "prod", Sources: []string{"deploy/prod/**"}}})
	// The provider has no node for a document that it did not index: the loss mode explains it.
	in.Environments[0].Workloads = []observation.WorkloadDocument{{Kind: "Deployment", Name: "edge", Environment: "prod", Path: "deploy/prod/edge.yaml", Line: 1}}

	got := depWorkloadSetRecord(t, in)
	gap := gapOf(t, got, observation.LossDefaultExcludedDirectory)

	depExpectStatus(t, got, observation.CoveragePartial)
	depExpectGapPath(t, got, &gap, "deploy/")
	depExpectGapContains(t, got, gap.Hint, "`!deploy/`")
}

func depReincludedDirectory(t *testing.T) {
	t.Helper()

	rec := observation.IndexRecord{Project: "app", GraphGeneration: "g", ExcludedDirs: []string{".git"}}
	in := depInputWithIndex(t, &rec)
	in.Gaps = observation.IndexGaps(&rec, []observation.EnvironmentSources{{ID: "prod", Sources: []string{"deploy/prod/**"}}})

	got := depWorkloadSetRecord(t, in)

	depExpectStatus(t, got, observation.CoverageComplete)
	depExpectNoLossMode(t, got, observation.LossDefaultExcludedDirectory)
}

func depPartlyParsedHelm(t *testing.T) {
	t.Helper()

	rec := observation.IndexRecord{
		Project: "app", GraphGeneration: "g",
		ParsePartial: []observation.PartialFile{{Path: "charts/api/templates/deployment.yaml", ErrorRanges: "1-1"}},
	}
	in := depInputWithIndex(t, &rec)

	// The pipeline attributes the gaps of a file to the environments that select it.
	gaps := observation.IndexGaps(&rec, nil)
	for i := range gaps {
		gaps[i].Environment = "prod"
	}

	in.Gaps = append(in.Gaps, gaps...)

	got := depWorkloadSetRecord(t, in)
	partial := gapOf(t, got, observation.LossParsePartial)

	depExpectStatus(t, got, observation.CoveragePartial)
	depExpectLossMode(t, got, observation.LossUnrenderedTemplate)
	depExpectGapContains(t, got, partial.Detail, "1-1")
}

func depKustomizeSource(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Gaps = []observation.CoverageGap{{Reason: observation.LossOverrideUnsignalled, Path: "kustomize/overlays/prod/kustomization.yaml", Environment: "prod", Detail: "Kustomize source"}}

	got := depWorkloadSetRecord(t, in)
	gap := gapOf(t, got, observation.LossOverrideUnsignalled)

	depExpectStatus(t, got, observation.CoveragePartial)
	depExpectGapPath(t, got, &gap, "kustomize/overlays/prod/kustomization.yaml")
}

func depTerraformComposeSource(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Environments[0].UnsupportedSources = []string{"terraform/main.tf", "compose.yaml"}

	got := depWorkloadSetRecord(t, in)

	depExpectStatus(t, got, observation.CoverageUnknown)
	depExpectReason(t, got, observation.ReasonUnsupportedDeploymentSource+": terraform/main.tf")
	depExpectReason(t, got, observation.ReasonUnsupportedDeploymentSource+": compose.yaml")
}

func depWorkloadWithoutProviderNode(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Environments[0].Workloads = []observation.WorkloadDocument{
		{Kind: "Deployment", Name: "api", Environment: "prod", Path: "k8s/prod/deployment.yaml", Line: 1, ProviderNode: true},
		{Kind: "Deployment", Name: "worker", Environment: "prod", Path: "k8s/prod/worker.yaml", Line: 1},
	}

	got := depWorkloadSetRecord(t, in)

	depExpectStatus(t, got, observation.CoverageUnknown)
	depExpectReason(t, got, "workload declared in k8s/prod/worker.yaml has no provider node")
}

func depCompetingDeclarations(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Environments[0].Workloads = []observation.WorkloadDocument{
		{Kind: "Deployment", Name: "reports", Environment: "prod", Path: "k8s/prod/reports.yaml", Line: 1, ProviderNode: true},
		{Kind: "Deployment", Name: "reports", Environment: "prod", Path: "k8s/overrides/reports.yaml", Line: 1, ProviderNode: true},
	}

	depExpectStatus(t, depWorkloadSetRecord(t, in), observation.CoverageComplete)
}

func depFailedScope(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Failures = []observation.ScopeFailure{{Project: "app", Reason: "CO-OBS-001: boom"}}

	depExpectStatus(t, depWorkloadSetRecord(t, in), observation.CoverageFailed)
}

func depUnavailableIndex(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Projects = []observation.ProjectScope{{Name: "app"}}

	got := depWorkloadSetRecord(t, in)

	depExpectStatus(t, got, observation.CoverageUnknown)
	depExpectReason(t, got, observation.ReasonIndexUnavailable)
}

func depCrossCheckNotRun(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Environments[0].CrossChecked = false

	got := depWorkloadSetRecord(t, in)

	depExpectStatus(t, got, observation.CoverageUnknown)
	depExpectReason(t, got, observation.ReasonCrossCheckNotRun)
}

func depNoProfile(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Families = nil

	depExpectStatus(t, depWorkloadSetRecord(t, in), observation.CoverageUnsupported)
}

func depDockerfileGapIgnored(t *testing.T) {
	t.Helper()

	in := baseInput(t)
	in.Gaps = []observation.CoverageGap{{
		Reason: observation.LossParsePartial, Path: "k8s/prod/Dockerfile", Project: "app", Environment: "prod", Line: 2, Detail: "error ranges 2-3",
	}}

	depExpectStatus(t, depWorkloadSetRecord(t, in), observation.CoverageComplete)
}

func TestPlacedInRecordEqualsTheWorkloadSetRecord(t *testing.T) {
	t.Parallel()

	in := baseInput(t)
	in.Gaps = []observation.CoverageGap{
		{Reason: observation.LossUnrenderedTemplate, Path: "charts/api/templates/deployment.yaml", Line: 1, Environment: "prod", Project: "app"},
		{Reason: observation.LossOverrideUnsignalled, Path: "kustomization.yaml", Environment: "prod", Project: "app"},
	}
	in.Environments[0].UnsupportedSources = []string{"compose.yaml"}
	in.Environments[0].Workloads = []observation.WorkloadDocument{{Kind: "Job", Name: "j", Environment: "prod", Path: "k8s/prod/job.yaml", Line: 1}}

	records := observation.ComputePredicateCoverage(in)
	set := recordOf(t, records, workloadSet, prodScope())
	placed := recordOf(t, records, placedIn, prodScope())

	if !depSameSummary(placed, set) {
		t.Fatalf("placed-in = %+v\nworkload set = %+v", placed, set)
	}

	for i := range set.LossModes {
		depExpectSameLossMode(t, i, &placed.LossModes[i], &set.LossModes[i])
	}

	if placed.Predicate != placedIn {
		t.Errorf("placed-in identity = %s %+v", placed.Predicate, placed.Scope)
	}

	if placed.Scope != prodScope() {
		t.Errorf("placed-in identity = %s %+v", placed.Predicate, placed.Scope)
	}

	if set.Status != observation.CoverageUnknown {
		t.Errorf("the combined input should be unknown: %+v", set)
	}
}

// depSameSummary says whether two records have the same status, the same
// unresolved count, the same reasons, and the same number of loss modes.
func depSameSummary(placed, set *observation.PredicateCoverage) bool {
	if placed.Status != set.Status {
		return false
	}

	if placed.Unresolved != set.Unresolved {
		return false
	}

	if !slices.Equal(placed.Reasons, set.Reasons) {
		return false
	}

	return len(placed.LossModes) == len(set.LossModes)
}

func depExpectSameLossMode(t *testing.T, index int, placed, set *observation.LossMode) {
	t.Helper()

	if placed.ID != set.ID {
		t.Errorf("loss mode %d: %+v vs %+v", index, *placed, *set)
	}

	if len(placed.Gaps) != len(set.Gaps) {
		t.Errorf("loss mode %d: %+v vs %+v", index, *placed, *set)
	}
}

func TestPlacedInWithoutAProfileIsUnsupported(t *testing.T) {
	t.Parallel()

	in := baseInput(t)
	in.Families = nil

	if rec := recordOf(t, observation.ComputePredicateCoverage(in), placedIn, prodScope()); rec.Status != observation.CoverageUnsupported {
		t.Errorf("placed-in = %+v", rec)
	}
}

// depDockerfileSetRecord computes the Dockerfile-set record of the app project.
func depDockerfileSetRecord(t *testing.T, rec *observation.IndexRecord, dockerfiles, unindexed []string) *observation.PredicateCoverage {
	t.Helper()

	in := baseInput(t)
	in.Projects = []observation.ProjectScope{{Name: "app", Index: rec, UnindexedDockerfiles: unindexed}}
	in.Gaps = observation.DockerfileGaps(rec, dockerfiles)

	return recordOf(t, observation.ComputePredicateCoverage(in), observation.FamilyDockerfileSet, observation.CoverageScope{Project: "app"})
}

func TestDockerfileSetCoverage(t *testing.T) {
	t.Parallel()

	depRunSubtests(t, []depSubtest{
		{name: "every Dockerfile indexed is complete", run: depEveryDockerfileIndexed},
		{name: "a Dockerfile under build is partial with default-excluded-directory", run: depDockerfileUnderBuild},
		{name: "a partly parsed Dockerfile is partial with parse-partial", run: depPartlyParsedDockerfile},
		{name: "the gap of another file does not apply", run: depGapOfAnotherFile},
		{name: "an unavailable index result is unknown", run: depDockerfileUnavailableIndex},
		{name: "a Dockerfile that is not indexed is unknown", run: depDockerfileNotIndexed},
	})
}

func depEveryDockerfileIndexed(t *testing.T) {
	t.Helper()

	got := depDockerfileSetRecord(t, &observation.IndexRecord{Project: "app", ExcludedDirs: []string{".git"}}, []string{"docker/Dockerfile", "svc/Dockerfile.dev"}, nil)

	depExpectStatus(t, got, observation.CoverageComplete)

	if len(got.LossModes) != 0 {
		t.Errorf("record = %+v", got)
	}

	// The set says nothing about image links: that is the loss mode of built-from.
	built := recordOf(t, observation.ComputePredicateCoverage(baseInput(t)), "built-from", observation.CoverageScope{Project: "app"})
	depExpectLossMode(t, built, observation.LossBuildLinkNeedsBuildCommand)
}

func depDockerfileUnderBuild(t *testing.T) {
	t.Helper()

	got := depDockerfileSetRecord(t, &observation.IndexRecord{Project: "app", ExcludedDirs: []string{".git", "build"}}, []string{"build/Dockerfile", "docker/Dockerfile"}, nil)
	gap := gapOf(t, got, observation.LossDefaultExcludedDirectory)

	depExpectStatus(t, got, observation.CoveragePartial)
	depExpectGapPath(t, got, &gap, "build/Dockerfile")
	depExpectGapContains(t, got, gap.Hint, "`!build/`")

	if len(got.LossModes) != 1 {
		t.Errorf("record = %+v", got)
	}
}

func depPartlyParsedDockerfile(t *testing.T) {
	t.Helper()

	rec := &observation.IndexRecord{Project: "app", ParsePartial: []observation.PartialFile{{Path: "docker/Dockerfile", ErrorRanges: "4-6"}}}
	got := depDockerfileSetRecord(t, rec, []string{"docker/Dockerfile"}, nil)
	gap := gapOf(t, got, observation.LossParsePartial)

	depExpectStatus(t, got, observation.CoveragePartial)

	if gap.Line != 4 {
		t.Errorf("gap line = %d, want 4: record = %+v", gap.Line, got)
	}
}

func depGapOfAnotherFile(t *testing.T) {
	t.Helper()

	rec := &observation.IndexRecord{
		Project: "app", ExcludedDirs: []string{"deploy"},
		ParsePartial: []observation.PartialFile{{Path: "charts/api/templates/deployment.yaml", ErrorRanges: "1-1"}},
	}
	in := baseInput(t)
	sources := []observation.EnvironmentSources{{ID: "prod", Sources: []string{"deploy/**"}}}
	in.Gaps = append(observation.IndexGaps(rec, sources), observation.DockerfileGaps(rec, []string{"docker/Dockerfile"})...)

	got := recordOf(t, observation.ComputePredicateCoverage(in), observation.FamilyDockerfileSet, observation.CoverageScope{Project: "app"})

	depExpectStatus(t, got, observation.CoverageComplete)
}

func depDockerfileUnavailableIndex(t *testing.T) {
	t.Helper()

	got := depDockerfileSetRecord(t, nil, []string{"docker/Dockerfile"}, nil)

	depExpectStatus(t, got, observation.CoverageUnknown)
	depExpectReason(t, got, observation.ReasonIndexUnavailable)
}

func depDockerfileNotIndexed(t *testing.T) {
	t.Helper()

	got := depDockerfileSetRecord(t, &observation.IndexRecord{Project: "app"}, []string{"docker/Dockerfile"}, []string{"docker/Dockerfile"})

	depExpectStatus(t, got, observation.CoverageUnknown)
	depExpectReason(t, got, "Dockerfile docker/Dockerfile is not indexed")
}

func TestIsDockerfile(t *testing.T) {
	t.Parallel()

	for file, want := range map[string]bool{
		"Dockerfile": true, "docker/Dockerfile": true, "svc/Dockerfile.dev": true, "Dockerfile.": false,
		"dockerfile": false, "Dockerfile.md.bak": true, "docker/Dockerfile/readme.md": false, "MyDockerfile": false,
	} {
		if got := observation.IsDockerfile(file); got != want {
			t.Errorf("IsDockerfile(%q) = %v, want %v", file, got, want)
		}
	}
}
