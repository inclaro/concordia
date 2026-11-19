package statement_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/admit"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// producer is a declared producer with a signing key and the claim kinds it may attest.
func producer(id, key string, claims ...string) workspace.Producer {
	return workspace.Producer{
		ID: id, Claims: claims,
		Authentication: workspace.ProducerAuthentication{Method: workspace.AuthCosignKey, Key: key},
	}
}

func checkResultBundle(t *testing.T) (loaded statement.Loaded, path string) {
	t.Helper()

	data := bundleJSON(t, statementJSON(t, statement.PredicateCheckResult, map[string]any{"check": "unit-tests"}))

	return mustParse(t, "build/results/unit.json", data), writeFile(t, "unit.json", data)
}

// acceptOnly is a fake cosign that records its arguments, accepts a verification only when it is given the key, and
// otherwise fails with the recorded output of a real rejection.
func acceptOnly(t *testing.T, key, argsFile string) string {
	t.Helper()

	reject := filepath.Join(t.TempDir(), "rejected")
	if err := os.WriteFile(reject, readFixture(t, "verify-failed.stderr"), 0o600); err != nil {
		t.Fatal(err)
	}

	accept := filepath.Join(t.TempDir(), "accepted")
	if err := os.WriteFile(accept, readFixture(t, "verify-ok.stderr"), 0o600); err != nil {
		t.Fatal(err)
	}

	return fakeCosign(t, `echo "$@" >> "`+argsFile+`"
case "$*" in
  *"--key `+key+` "*|*"--key `+key+`") cat "`+accept+`" >&2; exit 0 ;;
esac
cat "`+reject+`" >&2
exit 1
`)
}

func verifier(exe string) *statement.Verifier {
	return &statement.Verifier{Getenv: func(k string) string {
		if k == "COSIGN" {
			return exe
		}

		return ""
	}}
}

func admission(v statement.Verification, producers []workspace.Producer, kind string) assurance.Admission {
	return admit.Admit(&admit.Input{
		Kind: kind, Verification: v, Producers: producers, Context: assurance.Context{Now: now},
		Facts: admit.Facts{},
	})
}

// runVerify verifies the loaded statement with the fake cosign and stops the test on an error.
func runVerify(t *testing.T, exe string, in *statement.VerifyInput) statement.Verification {
	t.Helper()

	got, err := verifier(exe).Verify(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// expectAuthenticated reports an admission that is not admissible with a producer authenticated integrity.
func expectAuthenticated(t *testing.T, a assurance.Admission) {
	t.Helper()

	expectEqual(t, "integrity", a.Integrity, assurance.IntegrityAuthenticated)
	expectEqual(t, "admissible", a.Admissible, true)
}

// expectInadmissible reports an admission that is admissible or that has other details than the wanted ones.
func expectInadmissible(t *testing.T, a assurance.Admission, details ...assurance.Detail) {
	t.Helper()

	expectEqual(t, "admissible", a.Admissible, false)

	if !slices.Equal(a.Details, details) {
		t.Errorf("admission = %+v, want details %v", a, details)
	}
}

// expectNoDiagnostics reports a verification that gives diagnostics for the file.
func expectNoDiagnostics(t *testing.T, got *statement.Verification, file string) {
	t.Helper()

	if d := got.Diagnostics(file); len(d) != 0 {
		t.Errorf("diagnostics = %+v, want none", d)
	}
}

// expectCosignArguments reports a log of cosign arguments that does not hold the wanted part.
func expectCosignArguments(t *testing.T, argsFile, want string) {
	t.Helper()

	recorded, _ := os.ReadFile(argsFile)
	expectContains(t, "cosign arguments", string(recorded), want)
}

// expectToolchainError reports an error that is not CO-WRK-014 with the toolchain exit code.
func expectToolchainError(t *testing.T, err error) {
	t.Helper()

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v, want CO-WRK-014 with exit 5", err)
	}

	expectEqual(t, "code", failure.Diagnostic.Code, diag.WRK014)
	expectEqual(t, "exit code", failure.ExitCode, diag.Toolchain)
}

func TestVerifyValidSignatureIsProducerAuthenticated(t *testing.T) {
	t.Parallel()

	loaded, path := checkResultBundle(t)
	args := filepath.Join(t.TempDir(), "args")
	producers := []workspace.Producer{
		producer("a-other", "/keys/other.pub", workspace.ProducerClaimCheckResult),
		producer("ci", "/keys/ci.pub", workspace.ProducerClaimCheckResult),
	}

	got := runVerify(t, acceptOnly(t, "/keys/ci.pub", args), &statement.VerifyInput{
		Path: path, Loaded: loaded, Producers: producers,
	})

	expectEqual(t, "signature", got.Signature, statement.SignatureVerified)
	expectEqual(t, "producer", got.ProducerID, "ci")
	expectEqual(t, "subject", got.Subject, statement.SubjectUnchecked)
	expectAuthenticated(t, admission(got, producers, workspace.ProducerClaimCheckResult))
	expectCosignArguments(t, args, "verify-blob-attestation --bundle "+path+" --type "+statement.PredicateCheckResult+" --check-claims=false --key /keys/other.pub")
	expectNoDiagnostics(t, &got, "build/results/unit.json")
}

func TestVerifyFailedSignatureIsInadmissibleWithAWarning(t *testing.T) {
	t.Parallel()

	loaded, path := checkResultBundle(t)
	producers := []workspace.Producer{producer("ci", "/keys/ci.pub", workspace.ProducerClaimCheckResult)}

	got := runVerify(t, acceptOnly(t, "/keys/nobody.pub", filepath.Join(t.TempDir(), "args")), &statement.VerifyInput{
		Path: path, Loaded: loaded, Producers: producers,
	})

	expectEqual(t, "signature", got.Signature, statement.SignatureFailed)
	expectContains(t, "failure", got.Failure, "transparency log certificate does not match")

	a := admission(got, producers, workspace.ProducerClaimCheckResult)
	expectInadmissible(t, a, assurance.DetailIntegrityFailure)
	expectEqual(t, "integrity", a.Integrity, assurance.IntegrityNotVerified)

	diags := got.Diagnostics("build/results/unit.json")
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %+v, want one CO-PRV-009 warning naming the file", diags)
	}

	expectPRV009Warning(t, &diags[0])
	expectContains(t, "message", diags[0].Message, "build/results/unit.json")
}

func TestVerifyOfflineWithATrustedRootRunsCosignWithIt(t *testing.T) {
	t.Parallel()

	loaded, path := checkResultBundle(t)
	args := filepath.Join(t.TempDir(), "args")
	producers := []workspace.Producer{producer("ci", "/keys/ci.pub", workspace.ProducerClaimCheckResult)}

	got := runVerify(t, acceptOnly(t, "/keys/ci.pub", args), &statement.VerifyInput{
		Path: path, Loaded: loaded, Producers: producers, Offline: true, TrustedRoot: "/ws/.sigstore/trusted_root.json",
	})

	expectEqual(t, "signature", got.Signature, statement.SignatureVerified)
	expectCosignArguments(t, args, "--trusted-root /ws/.sigstore/trusted_root.json")
}

func TestVerifyOfflineWithoutATrustedRootDoesNotStartCosign(t *testing.T) {
	t.Parallel()

	loaded, path := checkResultBundle(t)
	started := filepath.Join(t.TempDir(), "started")
	exe := fakeCosign(t, `echo started > "`+started+`"`+"\n")
	producers := []workspace.Producer{producer("ci", "/keys/ci.pub", workspace.ProducerClaimCheckResult)}

	got := runVerify(t, exe, &statement.VerifyInput{
		Path: path, Loaded: loaded, Producers: producers, Offline: true,
	})

	if _, statErr := os.Stat(started); statErr == nil {
		t.Fatal("cosign was started")
	}

	a := admission(got, producers, workspace.ProducerClaimCheckResult)
	expectInadmissible(t, a, assurance.DetailOfflineVerificationUnavailable)

	if a.Integrity.Rank() >= assurance.IntegrityAuthenticated.Rank() {
		t.Errorf("integrity = %s, want below producer authenticated", a.Integrity)
	}

	expectNoDiagnostics(t, &got, "x")
}

func TestVerifyNeedsCosignOnlyWhenItRuns(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	loaded, path := checkResultBundle(t)
	producers := []workspace.Producer{producer("ci", "/keys/ci.pub", workspace.ProducerClaimCheckResult)}
	none := &statement.Verifier{Getenv: func(string) string { return "" }}

	_, err := none.Verify(context.Background(), &statement.VerifyInput{Path: path, Loaded: loaded, Producers: producers})
	expectToolchainError(t, err)

	// --offline is decided first, so a missing cosign does not hide it.
	got, err := none.Verify(context.Background(), &statement.VerifyInput{Path: path, Loaded: loaded, Producers: producers, Offline: true})
	if err != nil {
		t.Errorf("offline: err = %v", err)
	}

	expectEqual(t, "offline signature", got.Signature, statement.SignatureUnavailable)

	// A statement that needs no verification needs no cosign either.
	bare := mustParse(t, "bare.json", statementJSON(t, statement.PredicatePlan, map[string]any{}))

	got, err = none.Verify(context.Background(), &statement.VerifyInput{Loaded: bare, Producers: producers})
	if err != nil {
		t.Errorf("bare statement: err = %v", err)
	}

	expectEqual(t, "bare statement signature", got.Signature, statement.SignatureNone)
}

func TestVerifyWithoutSignatureOrProducer(t *testing.T) {
	t.Parallel()

	data := statementJSON(t, statement.PredicateCheckResult, map[string]any{})
	producers := []workspace.Producer{producer("ci", "/keys/ci.pub", workspace.ProducerClaimCheckResult)}

	tests := []struct {
		name      string
		want      assurance.Detail
		loaded    statement.Loaded
		producers []workspace.Producer
	}{
		{name: "unsigned", loaded: mustParse(t, "a.json", data), producers: producers, want: assurance.DetailNotAuthenticated},
		{name: "envelope without bundle", loaded: mustParse(t, "b.json", envelopeJSON(t, data)), producers: producers, want: assurance.DetailNotAuthenticated},
		{name: "signed, no producer declared", loaded: mustParse(t, "c.json", bundleJSON(t, data)), want: assurance.DetailProducerNotTrusted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A COSIGN path that cannot start proves that cosign is not needed.
			got := runVerify(t, "/nonexistent/cosign", &statement.VerifyInput{
				Path: "x", Loaded: tt.loaded, Producers: tt.producers,
			})

			a := admission(got, tt.producers, workspace.ProducerClaimCheckResult)
			expectInadmissible(t, a, tt.want)
			expectEqual(t, "integrity", a.Integrity, assurance.IntegrityNotVerified)
		})
	}
}

func TestVerifyComparesTheSubjectDigest(t *testing.T) {
	t.Parallel()

	data := statementJSON(t, statement.PredicatePlan, map[string]any{}, statement.Subject{
		Name: "prod.json", Digest: map[string]string{"sha256": "aaa"},
	})
	loaded := mustParse(t, "plan.intoto.json", data)
	matching, other := statement.Digest{Algorithm: "sha256", Value: "aaa"}, statement.Digest{Algorithm: "sha256", Value: "bbb"}

	tests := []struct {
		name      string
		expected  *statement.Digest
		integrity assurance.Integrity
		details   []assurance.Detail
		warnings  int
	}{
		{"digest only", &matching, assurance.IntegrityDigest, []assurance.Detail{assurance.DetailNotAuthenticated}, 0},
		{"digest differs", &other, assurance.IntegrityNotVerified, []assurance.Detail{assurance.DetailIntegrityFailure, assurance.DetailNotAuthenticated}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := (&statement.Verifier{}).Verify(context.Background(), &statement.VerifyInput{Loaded: loaded, Expected: tt.expected})
			if err != nil {
				t.Fatal(err)
			}

			slices.Sort(tt.details)

			a := admission(got, nil, workspace.ProducerClaimPlan)

			if a.Integrity != tt.integrity || !slices.Equal(a.Details, tt.details) {
				t.Errorf("admission = %+v, want %s with %v", a, tt.integrity, tt.details)
			}

			if n := len(got.Diagnostics("plan.intoto.json")); n != tt.warnings {
				t.Errorf("warnings = %d, want %d", n, tt.warnings)
			}
		})
	}
}

// recordedAttestation is the recorded keyed attestation and the pinned cosign that verifies it.
type recordedAttestation struct {
	exe, root, bundlePath string
	loaded                statement.Loaded
}

// pinnedCosign finds the cosign to run, and skips the test when the pinned cosign is not installed.
func pinnedCosign(t *testing.T) string {
	t.Helper()

	if exe := os.Getenv("COSIGN"); exe != "" {
		return exe
	}

	abs, err := filepath.Abs(filepath.Join("..", "..", "..", ".bin", "cosign"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err = os.Stat(abs); err != nil {
		t.Skip("the pinned cosign (.bin/cosign) is not installed")
	}

	return abs
}

// newRecordedAttestation reads the recorded keyed attestation, and skips the test when there is no cosign for it.
func newRecordedAttestation(t *testing.T) *recordedAttestation {
	t.Helper()

	exe := pinnedCosign(t)

	root, err := filepath.Abs(realFixtures())
	if err != nil {
		t.Fatal(err)
	}

	return &recordedAttestation{
		exe: exe, root: root, bundlePath: filepath.Join(root, "check-result.sigstore.json"),
		loaded: mustParse(t, "check-result.sigstore.json", readFixture(t, "check-result.sigstore.json")),
	}
}

// verify runs the real cosign on the attestation, offline, with the trusted root of the fixtures.
func (r *recordedAttestation) verify(t *testing.T, producers []workspace.Producer, expected *statement.Digest) statement.Verification {
	t.Helper()

	return runVerify(t, r.exe, &statement.VerifyInput{
		Path: r.bundlePath, Loaded: r.loaded, Producers: producers, Root: r.root, Offline: true,
		TrustedRoot: filepath.Join(r.root, "trusted_root.json"), Expected: expected,
	})
}

// TestVerifyWithRealCosign verifies the recorded keyed attestation with the pinned cosign, offline, through the
// whole path: the producer key is a workspace-relative file, the trusted root is a file, and the subject digest is the
// digest of the plan file that the statement names.
func TestVerifyWithRealCosign(t *testing.T) {
	t.Parallel()

	attestation := newRecordedAttestation(t)
	producers := []workspace.Producer{
		producer("stranger", "unknown.pub", workspace.ProducerClaimCheckResult),
		producer("ci", "cosign.pub", workspace.ProducerClaimCheckResult),
	}
	planDigest := "sha256:5041bf1f713df204784353e82f6a4a535931cb64f1f4b4a5aeaffcb720918b22"
	expected, _ := statement.ParseDigest(planDigest)

	got := attestation.verify(t, producers, &expected)

	expectEqual(t, "signature", got.Signature, statement.SignatureVerified)
	expectEqual(t, "producer", got.ProducerID, "ci")
	expectEqual(t, "subject", got.Subject, statement.SubjectMatches)
	expectAuthenticated(t, admission(got, producers, workspace.ProducerClaimCheckResult))

	// The producer is verified but may not attest this kind of claim.
	expectInadmissible(t, admission(got, producers[1:], workspace.ProducerClaimPlan), assurance.DetailProducerNotTrustedForClaim)

	// No declared key signed the statement.
	expectEqual(t, "stranger only: signature", attestation.verify(t, producers[:1], nil).Signature, statement.SignatureFailed)

	wrong := statement.Digest{Algorithm: "sha256", Value: "0000"}
	expectEqual(t, "wrong digest: subject", attestation.verify(t, producers, &wrong).Subject, statement.SubjectDiffers)
}
