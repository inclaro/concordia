package integration_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

// These tests replay the index_repository result of both infra recordings (O1) through
// the coverage code: the run record of the index result, the default-exclusion gaps with
// the `.cbmignore` hint, the raster file, and the partly parsed Helm template.

var infraEnvironments = []observation.EnvironmentSources{
	{ID: "edge", Sources: []string{"deploy/**"}},
	{ID: "production", Sources: []string{"k8s/prod/**", "charts/**"}},
}

func infraRecord(s *infraSnapshot) observation.IndexRecord {
	return s.index.Record(infraProject, "gen", "rev")
}

func gapsOfReason(gaps []observation.CoverageGap, reason string) []observation.CoverageGap {
	var out []observation.CoverageGap

	for i := range gaps {
		if gaps[i].Reason == reason {
			out = append(out, gaps[i])
		}
	}

	return out
}

func TestInfraIndexResultIsKeptInTheRunRecord(t *testing.T) {
	t.Parallel()

	rec := infraRecord(replayInfra(t, infraRecordingFile))

	if !slices.Equal(rec.ExcludedDirs, []string{".git", "deploy"}) {
		t.Errorf("excluded dirs = %v", rec.ExcludedDirs)
	}

	if !slices.Contains(rec.NotIndexedFiles, observation.PathReason{Path: "docs/context.png", Reason: "ignored-suffix"}) {
		t.Errorf("not indexed files = %v", rec.NotIndexedFiles)
	}

	// The task text says the Helm range is `1-4`; the recording of CBM 0.12.1 holds `1-1`.
	want := observation.PartialFile{Path: "charts/api/templates/deployment.yaml", ErrorRanges: "1-1"}
	if !slices.Contains(rec.ParsePartial, want) || !hasInfraSingleCounts(&rec) {
		t.Errorf("parse partial = %+v (counts %d, %d)", rec.ParsePartial, rec.ParsePartialCount, rec.NotIndexedCount)
	}

	if rec.ExampleListsOnly() {
		t.Error("the recording lists every file but is marked as examples only")
	}
}

func hasInfraSingleCounts(rec *observation.IndexRecord) bool {
	return rec.ParsePartialCount == 1 && rec.NotIndexedCount == 1
}

func isInfraEdgeDeployGap(g *observation.CoverageGap) bool {
	if g.Environment != "edge" || g.Path != "deploy/" {
		return false
	}

	return g.Source == "deploy/**" && strings.Contains(g.Hint, "`!deploy/`")
}

func TestInfraDefaultExcludedDirectoryGap(t *testing.T) {
	t.Parallel()

	gaps := observation.IndexGaps(new(infraRecord(replayInfra(t, infraRecordingFile))), infraEnvironments)

	excluded := gapsOfReason(gaps, observation.LossDefaultExcludedDirectory)
	if len(excluded) != 1 {
		t.Fatalf("default-excluded-directory gaps = %+v", excluded)
	}

	if !isInfraEdgeDeployGap(&excluded[0]) {
		t.Errorf("gap = %+v", excluded[0])
	}

	// The production sources are not under an excluded directory.
	for _, g := range excluded {
		if g.Environment == "production" {
			t.Errorf("production has a gap: %+v", g)
		}
	}
}

func TestInfraReincludedDirectoryHasNoGap(t *testing.T) {
	t.Parallel()

	rec := infraRecord(replayInfra(t, infraReincludeFile))
	if !slices.Equal(rec.ExcludedDirs, []string{".git"}) {
		t.Fatalf("excluded dirs with !deploy/ = %v", rec.ExcludedDirs)
	}

	if gaps := gapsOfReason(observation.IndexGaps(&rec, infraEnvironments), observation.LossDefaultExcludedDirectory); len(gaps) != 0 {
		t.Errorf("gaps = %+v", gaps)
	}
}

func isInfraRasterGap(gaps []observation.CoverageGap) bool {
	return len(gaps) == 1 && gaps[0].Path == "docs/context.png" && gaps[0].Detail == "ignored-suffix"
}

func isInfraPartialTemplateGap(gaps []observation.CoverageGap) bool {
	if len(gaps) != 1 || gaps[0].Path != "charts/api/templates/deployment.yaml" {
		return false
	}

	return gaps[0].Line == 1 && strings.Contains(gaps[0].Detail, "1-1")
}

func TestInfraRasterAndPartialTemplateAreGaps(t *testing.T) {
	t.Parallel()

	gaps := observation.IndexGaps(new(infraRecord(replayInfra(t, infraRecordingFile))), infraEnvironments)

	raster := gapsOfReason(gaps, observation.LossRasterNotIndexed)
	if !isInfraRasterGap(raster) {
		t.Errorf("raster gaps = %+v", raster)
	}

	partial := gapsOfReason(gaps, observation.LossParsePartial)
	if !isInfraPartialTemplateGap(partial) {
		t.Errorf("parse-partial gaps = %+v", partial)
	}

	if tmpl := gapsOfReason(gaps, observation.LossUnrenderedTemplate); len(tmpl) != 1 || tmpl[0].Path != partial[0].Path {
		t.Errorf("unrendered-template gaps = %+v", tmpl)
	}
}

// infraPredicateCoverage computes the predicate coverage of a recording, with or without its index result.
func infraPredicateCoverage(t *testing.T, file string, withIndex bool) []observation.PredicateCoverage {
	t.Helper()

	profile, ok := observation.ProfileFor("0.12.1", observation.AdapterSchema)
	if !ok {
		t.Fatal("no profile for CBM 0.12.1")
	}

	rec := infraRecord(replayInfra(t, file))
	in := &observation.PredicateInput{
		Families: observation.DeclaredFamilies(profile),
		Projects: []observation.ProjectScope{{Name: infraProject}},
		Environments: []observation.EnvironmentScope{
			{ID: "edge", Projects: []string{infraProject}, CrossChecked: true},
			{ID: "production", Projects: []string{infraProject}, CrossChecked: true},
		},
	}

	if withIndex {
		in.Projects[0].Index = &rec
		in.Gaps = observation.IndexGaps(&rec, infraEnvironments)
	}

	return observation.ComputePredicateCoverage(in)
}

func findInfraCoverage(t *testing.T, records []observation.PredicateCoverage, predicate, env string) *observation.PredicateCoverage {
	t.Helper()

	for i := range records {
		if records[i].Predicate == predicate && records[i].Scope.Environment == env {
			return &records[i]
		}
	}

	t.Fatalf("no record %s/%s", predicate, env)

	return nil
}

func TestInfraCoverageRecordsOfBothRecordings(t *testing.T) {
	t.Parallel()

	without := infraPredicateCoverage(t, infraRecordingFile, true)

	edge := findInfraCoverage(t, without, "deploys-artifact", "edge")
	if edge.Status != observation.CoveragePartial || !edge.HasLossMode(observation.LossDefaultExcludedDirectory) {
		t.Errorf("edge deploys-artifact without !deploy/ = %+v", edge)
	}

	prod := findInfraCoverage(t, without, "uses-image", "production")
	if prod.Status == observation.CoverageUnknown || prod.HasLossMode(observation.LossDefaultExcludedDirectory) {
		t.Errorf("production uses-image = %+v", prod)
	}

	with := infraPredicateCoverage(t, infraReincludeFile, true)
	if edge := findInfraCoverage(t, with, "deploys-artifact", "edge"); edge.HasLossMode(observation.LossDefaultExcludedDirectory) {
		t.Errorf("edge deploys-artifact with !deploy/ = %+v", edge)
	}

	// No index result: the environment sources are unknown.
	unavailable := infraPredicateCoverage(t, infraRecordingFile, false)

	unknown := findInfraCoverage(t, unavailable, "uses-image", "edge")
	if unknown.Status != observation.CoverageUnknown || !slices.Contains(unknown.Reasons, "index result unavailable") {
		t.Errorf("edge uses-image without an index result = %+v", unknown)
	}
}
