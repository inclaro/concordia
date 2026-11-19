package evidence_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestNilAssuranceGivesEmptyEvidence(t *testing.T) {
	t.Parallel()

	got := mustLoad(t, context.Background(), &evidence.Options{Root: t.TempDir()})

	if n := len(got.Renders) + len(got.Plans) + len(got.Statements) + len(got.Diagnostics); n != 0 || got.Scenarios != nil {
		t.Errorf("evidence is not empty: %+v", got)
	}

	if !strings.HasPrefix(got.ClaimsDigest, "sha256:") {
		t.Errorf("claims digest = %q", got.ClaimsDigest)
	}
}

// TestFailedEnvironmentDoesNotStopTheOthers: the staging source does not exist. Production still renders, and the
// failure is a failed result with a diagnostic, not an error.
func TestFailedEnvironmentDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Assurance{Rendering: rendering(plainSource("staging", "no-such-dir"), plainSource("production", "plain"))}
	opts := options(fixtures(t), cfg, "")
	opts.Run = forbiddenRun(t)

	got := mustLoad(t, context.Background(), opts)

	if len(got.Renders) != 2 {
		t.Fatalf("renders = %d, want 2", len(got.Renders))
	}

	// Sorted by environment: production first.
	production, staging := got.Renders[0], got.Renders[1]
	expectEqual(t, "production environment", production.Environment, "production")
	expectEqual(t, "production coverage", production.Coverage, assurance.CoverageComplete)

	if production.ResourceCount() == 0 {
		t.Error("production has no resources")
	}

	expectEqual(t, "production cluster", production.Cluster, "prod-cluster")
	expectEqual(t, "staging environment", staging.Environment, "staging")
	expectEqual(t, "staging coverage", staging.Coverage, assurance.CoverageFailed)
	expectCode(t, got.Diagnostics, diag.PRV005)
}

// TestToolchainErrorStopsTheLoad: a helm that is not found is exit 5, and the error is the one of the renderer.
func TestToolchainErrorStopsTheLoad(t *testing.T) {
	t.Parallel()

	cfg := &workspace.Assurance{Rendering: rendering(helmSource("production"))}
	opts := options(fixtures(t), cfg, "")
	opts.Locate = func(string) (string, error) { return "", os.ErrNotExist }

	got, err := evidence.Load(context.Background(), opts)

	expectDiagError(t, err, diag.PRV003, diag.Toolchain)
	expectNoEvidence(t, got, "a toolchain error")
}

func TestCanceledContextReturnsTheContextError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := &workspace.Assurance{Rendering: rendering(plainSource("production", "plain"))}

	if _, err := evidence.Load(ctx, options(fixtures(t), cfg, "")); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// renderIdentities lists the renderers of the toolchain as name@version.
func renderIdentities(renderers []render.Identity) []string {
	out := make([]string, 0, len(renderers))
	for _, r := range renderers {
		out = append(out, r.Name+"@"+r.Version)
	}

	return out
}

// TestRendererIdentityAndRenderTwice uses a fake helm: the identity goes into the toolchain, a run renders once, and
// `assurance validate` renders twice and finds that the output differs.
func TestRendererIdentityAndRenderTwice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		wantRuns  int32
		twice     bool
		wantDrift bool
	}{
		{name: "one render per run", body: randomManifest, twice: false, wantRuns: 1},
		{name: "validate renders twice and sees the difference", body: randomManifest, twice: true, wantRuns: 2, wantDrift: true},
		{name: "validate finds a stable renderer stable", body: stableManifest, twice: true, wantRuns: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			locate, pins := fakeRenderer(t, tt.body)
			counter := &countingRun{}

			cfg := &workspace.Assurance{Rendering: rendering(helmSource("production"))}
			opts := options(fixtures(t), cfg, "")
			opts.Locate, opts.Pins, opts.Run, opts.RenderTwice = locate, pins, counter.run, tt.twice

			got := mustLoad(t, context.Background(), opts)

			if got.Renders[0].Coverage != assurance.CoverageComplete {
				t.Fatalf("coverage = %s, gaps %+v", got.Renders[0].Coverage, got.Renders[0].Gaps)
			}

			expectDeepEqual(t, "renderers", renderIdentities(got.Toolchain.Renderers), []string{"helm@v4.3.0"})

			// Each render starts the renderer once for the version probe and once for the template.
			expectEqual(t, "renderer starts", counter.n.Load(), tt.wantRuns*2)
			expectEqual(t, "CO-PRV-014", hasCode(got.Diagnostics, diag.PRV014), tt.wantDrift)
		})
	}
}
