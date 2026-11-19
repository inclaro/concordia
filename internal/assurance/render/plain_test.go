package render_test

import (
	"context"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
)

// renderPlainFixture renders a directory of test/fixtures/assurance as a plain source. No program may start.
func renderPlainFixture(t *testing.T, env, dir string) *render.Result {
	t.Helper()

	res, err := render.Render(context.Background(), &render.Options{
		Root: fixtures(t), Source: *plainSource(env, dir), Cluster: "prod-eu", Run: forbidden(t),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	return res
}

// TestPlainKeepsEveryContainerOccurrence reproduces CBM 0.12.1 observation O4: one Deployment with two containers
// of one image repository at two tags. The code graph keeps one edge; the rendered documents give two occurrences.
func TestPlainKeepsEveryContainerOccurrence(t *testing.T) {
	t.Parallel()

	res := renderPlainFixture(t, "production", "plain")

	if res.Coverage != assurance.CoverageComplete {
		t.Fatalf("coverage = %s, gaps %v", res.Coverage, res.Gaps)
	}

	expectDeep(t, "confinement", res.Confinement, render.ConfinementNone)
	expectDeep(t, "level", res.Level, "rendered-or-evaluated")

	if res.Renderer != nil {
		t.Errorf("renderer = %v, want none for plain manifests", res.Renderer)
	}

	api := render.ResourceID{Environment: "production", Cluster: "prod-eu", Group: "apps", Kind: "Deployment", Namespace: "payments", Name: "api"}
	occ := res.OccurrencesOf(&api)

	if len(occ) != 2 {
		t.Fatalf("api has %d occurrences, want 2: %+v", len(occ), occ)
	}

	for i, tag := range []string{"1.2.0", "1.3.0"} {
		checkMutableOccurrence(t, &occ[i], tag)
	}

	expectDeep(t, "first container", occ[0].Container, "app")
	expectDeep(t, "second container", occ[1].Container, "sidecar")

	if res.ResourceCount() == 0 {
		t.Error("no resources")
	}

	if res.InputDigest == "" || res.OutputDigest == "" {
		t.Errorf("input %q, output %q", res.InputDigest, res.OutputDigest)
	}
}

// checkMutableOccurrence checks a container occurrence of the api image with a tag, but no digest.
func checkMutableOccurrence(t *testing.T, occ *render.ContainerOccurrence, tag string) {
	t.Helper()

	expectDeep(t, "repository of the "+tag+" occurrence", occ.Repository, "registry.example.com/payments/api")
	expectDeep(t, "tag", occ.Tag, tag)
	expectDeep(t, tag+" immutable", occ.Immutable, false)
	expectDeep(t, tag+" digest", occ.Digest, "")
	expectDeep(t, tag+" kind", occ.Kind, render.KindContainer)
}

func TestPlainDigestImmutability(t *testing.T) {
	t.Parallel()

	res := renderPlainFixture(t, "production", "plain")
	pod := render.ResourceID{Environment: "production", Cluster: "prod-eu", Kind: "Pod", Namespace: "payments", Name: "debug"}

	got := map[string]render.ContainerOccurrence{}
	for _, o := range res.OccurrencesOf(&pod) {
		got[o.Container] = o
	}

	debugger := got["debugger"]
	checkImmutableEphemeral(t, &debugger)

	shell := got["shell"]
	checkUntaggedRegistryWithPort(t, &shell)

	cron := render.ResourceID{Environment: "production", Cluster: "prod-eu", Group: "batch", Kind: "CronJob", Namespace: "payments", Name: "nightly"}
	nightly := res.OccurrencesOf(&cron)
	expectLen(t, "nightly", nightly, 1)

	if nightly[0].Tag != "1.0" || !nightly[0].Immutable {
		t.Errorf("nightly = %+v, want tag and digest together, immutable", nightly)
	}
}

func checkImmutableEphemeral(t *testing.T, debugger *render.ContainerOccurrence) {
	t.Helper()

	if !debugger.Immutable || debugger.Digest == "" {
		t.Errorf("debugger = %+v, want an immutable container with a digest", debugger)
	}

	if debugger.Tag != "" || debugger.Kind != render.KindEphemeral {
		t.Errorf("debugger = %+v, want an ephemeral container without a tag", debugger)
	}
}

func checkUntaggedRegistryWithPort(t *testing.T, shell *render.ContainerOccurrence) {
	t.Helper()

	if shell.Repository != "registry:5000/tools/shell" || shell.Tag != "" {
		t.Errorf("shell = %+v, want a registry with a port, no tag", shell)
	}

	if shell.Immutable {
		t.Errorf("shell = %+v, want a mutable container", shell)
	}
}

func TestPlainBadFileIsAPartialGap(t *testing.T) {
	t.Parallel()

	res := renderPlainFixture(t, "production", "plain-broken")

	if res.Coverage != assurance.CoveragePartial {
		t.Fatalf("coverage = %s, want partial", res.Coverage)
	}

	if !slices.Equal(gapKinds(res), []string{render.GapUnparseableDocument}) || res.Gaps[0].Path != "plain-broken/bad.yaml" {
		t.Errorf("gaps = %+v, want one unparseable-document gap of plain-broken/bad.yaml", res.Gaps)
	}

	named := anyDiagnostic(res, func(d *diag.Diagnostic) bool {
		return isWarningAbout(d, diag.PRV005, "plain-broken/bad.yaml")
	})
	if !named {
		t.Errorf("no CO-PRV-005 warning for the file: %+v", res.Diagnostics)
	}

	if len(res.Resources) != 1 || res.Resources[0].ID.Name != "api" {
		t.Errorf("the good file is kept: %+v", res.Resources)
	}
}

// isWarningAbout tells whether d is a warning with the given code that names the file uri.
func isWarningAbout(d *diag.Diagnostic, code, uri string) bool {
	if d.Code != code || d.Severity != diag.SeverityWarning {
		return false
	}

	return d.Source != nil && d.Source.URI == uri
}

func TestPlainMissingSourceIsFailed(t *testing.T) {
	t.Parallel()

	res := renderPlainFixture(t, "production", "does-not-exist")

	expectDeep(t, "coverage", res.Coverage, assurance.CoverageFailed)
	expectDeep(t, "gap kinds", gapKinds(res), []string{render.GapMissingSource})

	if res.OutputDigest != "" {
		t.Errorf("digest %q, want none", res.OutputDigest)
	}
}

func TestPlainSingleFileAndOrderIndependence(t *testing.T) {
	t.Parallel()

	a := renderPlainFixture(t, "production", "plain/net")
	b := renderPlainFixture(t, "production", "plain/net")

	if a.OutputDigest != b.OutputDigest || a.InputDigest != b.InputDigest {
		t.Errorf("two renders differ: %s/%s %s/%s", a.InputDigest, b.InputDigest, a.OutputDigest, b.OutputDigest)
	}

	file := renderPlainFixture(t, "production", "plain/net/service.yaml")
	if file.Coverage != assurance.CoverageComplete || len(file.Resources) != 3 {
		t.Errorf("a single file: coverage %s, %d resources", file.Coverage, len(file.Resources))
	}
}
