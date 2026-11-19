package evidence_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/checkresult/verdict"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// signedResults writes check-result bundles into root and gives the configuration that declares them.
func signedResults(t *testing.T, root string, files map[string]map[string]any) *workspace.Assurance {
	t.Helper()

	for name, predicate := range files {
		writeFile(t, root, "build/results/"+name, bundle(t, bareStatement(t, statement.PredicateCheckResult, predicate, "aa")))
	}

	return &workspace.Assurance{
		Producers:    []workspace.Producer{producer("ci", workspace.ProducerClaimCheckResult, workspace.ProducerClaimMeasurement)},
		Verification: &workspace.AssuranceVerification{Results: []string{"build/results/*.json"}},
	}
}

// expectVerdict reports a check whose verdict over an environment is not the wanted one.
func expectVerdict(t *testing.T, ev *evidence.Evidence, check, environment, wantVerdict, wantReason string) {
	t.Helper()

	got := ev.CheckVerdict(check, environment)
	if got.Verdict != wantVerdict || got.Reason != wantReason {
		t.Errorf("verdict of %s in %q = %s (%s), want %s (%s)", check, environment, got.Verdict, got.Reason, wantVerdict, wantReason)
	}
}

// expectCosignNotStarted stops the test when the log of the fake cosign has a call.
func expectCosignNotStarted(t *testing.T, logPath string) {
	t.Helper()

	if text := calls(t, logPath); text != "" {
		t.Fatalf("cosign started offline without a trusted root: %s", text)
	}
}

// expectOfflineWithoutRoot checks the statement that stays unverified because the run is offline and has no root.
func expectOfflineWithoutRoot(t *testing.T, summary *evidence.StatementSummary) {
	t.Helper()

	expectEqual(t, "admissible", summary.Admissible, false)
	expectEqual(t, "signature", summary.Signature, statement.SignatureUnavailable)

	if summary.Integrity == assurance.IntegrityAuthenticated {
		t.Errorf("integrity = %s, want less than authenticated", summary.Integrity)
	}

	if !slices.Contains(summary.Details, assurance.DetailOfflineVerificationUnavailable) {
		t.Errorf("details = %v, want %s", summary.Details, assurance.DetailOfflineVerificationUnavailable)
	}
}

// expectOfflineWithRoot checks the statement that cosign verified offline with the trusted root.
func expectOfflineWithRoot(t *testing.T, summary *evidence.StatementSummary) {
	t.Helper()

	expectEqual(t, "admissible", summary.Admissible, true)
	expectEqual(t, "integrity", summary.Integrity, assurance.IntegrityAuthenticated)
	expectEqual(t, "producer", summary.Producer, "ci")
}

// TestOfflineWithoutTrustedRootStartsNoCosign: design D24. The statement keeps a low integrity, the detail says why,
// and the fake cosign has no call in its log. With a trusted root the same statement is verified offline.
func TestOfflineWithoutTrustedRootStartsNoCosign(t *testing.T) {
	t.Parallel()

	exe, log := fakeCosign(t)
	root := t.TempDir()
	cfg := signedResults(t, root, map[string]map[string]any{"unit.json": checkResult("unit-tests", "passed", "production")})

	opts := options(root, cfg, exe)
	opts.Context.Offline = true

	got := mustLoad(t, context.Background(), opts)

	expectCosignNotStarted(t, log)
	expectOfflineWithoutRoot(t, &got.Statements[0])
	expectVerdict(t, got, "unit-tests", "production", verdict.Unknown, verdict.ReasonNoEvidenceSupplied)
	expectEqual(t, "cosign version recorded for a cosign that never started", got.Toolchain.Cosign, "")

	// The same statement with a trusted root runs cosign offline, with the root.
	writeFile(t, root, ".sigstore/trusted_root.json", []byte(`{}`))

	cfg.Verification.TrustedRoot = ".sigstore/trusted_root.json"

	got = mustLoad(t, context.Background(), opts)

	text := calls(t, log)
	if !strings.Contains(text, "verify-blob-attestation") {
		t.Errorf("cosign calls = %q, want verify-blob-attestation", text)
	}

	// cosign sees the root through its symbolic links (macOS temporary directories are one).
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, ".sigstore", "trusted_root.json"))
	if err != nil {
		t.Fatal(err)
	}

	if want := "--trusted-root " + resolved; !strings.Contains(text, want) {
		t.Errorf("cosign calls = %q, want %q", text, want)
	}

	expectOfflineWithRoot(t, &got.Statements[0])
	expectEqual(t, "cosign version", got.Toolchain.Cosign, "3.0.2")
}

// outcomeKey is a check outcome as the test compares it.
type outcomeKey struct{ check, environment, verdict string }

// TestCheckResultsAreGroupedByCheckAndEnvironment: one check passes in production and fails in staging, and the
// results do not conflict. Asked for no environment, the same results do.
func TestCheckResultsAreGroupedByCheckAndEnvironment(t *testing.T) {
	t.Parallel()

	exe, _ := fakeCosign(t)
	root := t.TempDir()
	cfg := signedResults(t, root, map[string]map[string]any{
		"unit-production.json": checkResult("unit-tests", "passed", "production"),
		"unit-staging.json":    checkResult("unit-tests", "failed", "staging"),
		"lint-production.json": checkResult("lint", "passed", "production"),
		// The fake cosign refuses the file name: the signature fails, the result is inadmissible.
		"refuse-production.json": checkResult("e2e", "passed", "production"),
	})

	got := mustLoad(t, context.Background(), options(root, cfg, exe))

	expectDeepEqual(t, "check ids", got.CheckIDs, []string{"e2e", "lint", "unit-tests"})

	outcomes := make([]outcomeKey, 0, len(got.CheckOutcomes))
	for _, o := range got.CheckOutcomes {
		outcomes = append(outcomes, outcomeKey{o.Check, o.Environment, o.Verdict})
	}

	expectDeepEqual(t, "outcomes", outcomes, []outcomeKey{
		{"e2e", "production", verdict.Unknown},
		{"lint", "production", verdict.Pass},
		{"unit-tests", "production", verdict.Pass},
		{"unit-tests", "staging", verdict.Violation},
	})

	if hasCode(got.Diagnostics, diag.PRV012) {
		t.Error("CO-PRV-012 for results of two environments")
	}

	expectVerdict(t, got, "unit-tests", "", verdict.Unknown, verdict.ReasonEvidenceConflict)
	expectVerdict(t, got, "never-ran", "production", verdict.Unknown, verdict.ReasonNoEvidenceSupplied)

	// The refused signature gives a warning for the file and an info with the reason; the others are quiet.
	expectCode(t, got.Diagnostics, diag.PRV009)
	expectCode(t, got.Diagnostics, diag.PRV010)
}

// pinnedCosign finds the pinned cosign, and skips the test when it is not installed.
func pinnedCosign(t *testing.T) string {
	t.Helper()

	exe := os.Getenv("COSIGN")
	if exe == "" {
		exe = filepath.Join(repoRoot(t), ".bin", "cosign")
	}

	if _, err := os.Stat(exe); err != nil {
		t.Skip("the pinned cosign (.bin/cosign) is not installed")
	}

	return exe
}

// keyedCosignConfig declares the recorded keyed attestation and its trusted root.
func keyedCosignConfig() *workspace.Assurance {
	return &workspace.Assurance{
		Producers: []workspace.Producer{{
			ID: "ci", Claims: []string{workspace.ProducerClaimCheckResult},
			Authentication: workspace.ProducerAuthentication{Method: workspace.AuthCosignKey, Key: "cosign.pub"},
		}},
		Verification: &workspace.AssuranceVerification{Results: []string{"*.sigstore.json"}, TrustedRoot: "trusted_root.json"},
	}
}

// TestRealCosignVerifiesTheRecordedAttestation runs the pinned cosign on the recorded keyed attestation with its
// trusted root: the check passes and the toolchain records the version.
func TestRealCosignVerifiesTheRecordedAttestation(t *testing.T) {
	t.Parallel()

	exe := pinnedCosign(t)
	opts := options(filepath.Join(fixtures(t), "statements", "real-cosign"), keyedCosignConfig(), exe)
	opts.Context.Offline = true

	got := mustLoad(t, context.Background(), opts)

	if out := got.CheckVerdict("unit-tests", "production"); out.Verdict != verdict.Pass {
		t.Errorf("verdict = %+v, diagnostics %v", out, got.Diagnostics)
	}

	if len(got.Statements) != 1 {
		t.Fatalf("statements = %+v", got.Statements)
	}

	expectEqual(t, "integrity", got.Statements[0].Integrity, assurance.IntegrityAuthenticated)
	expectEqual(t, "producer", got.Statements[0].Producer, "ci")

	if got.Toolchain.Cosign == "" || got.Toolchain.Cosign == "unknown" {
		t.Errorf("cosign version = %q", got.Toolchain.Cosign)
	}
}
