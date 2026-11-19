package cosign_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/cosign"
	"github.com/inclaro/concordia/internal/diag"
)

// script writes an executable shell script and gives its path.
func script(t *testing.T, body string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	path := filepath.Join(t.TempDir(), "cosign")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}

	return path
}

// diagWithCode gives the diagnostic error in err when it has the code, or nil.
func diagWithCode(err error, code string) *diag.Error {
	var failure *diag.Error
	if errors.As(err, &failure) && failure.Diagnostic.Code == code {
		return failure
	}

	return nil
}

func TestPathPrefersTheEnvironment(t *testing.T) {
	t.Parallel()

	got, err := cosign.Path(func(k string) string {
		if k == "COSIGN" {
			return "/opt/cosign"
		}

		return ""
	})
	if err != nil || got != "/opt/cosign" {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

func TestPathNeedsCosign(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := cosign.Path(func(string) string { return "" })

	failure := diagWithCode(err, diag.WRK014)
	if failure == nil || failure.ExitCode != diag.Toolchain {
		t.Fatalf("err = %v, want CO-WRK-014 with exit 5", err)
	}
}

func TestSignBlobWritesTheBundleBesideTheBlob(t *testing.T) {
	t.Parallel()

	exe := script(t, `while [ $# -gt 0 ]; do if [ "$1" = "--bundle" ]; then echo sig > "$2"; fi; shift; done`)
	blob := filepath.Join(t.TempDir(), "record.json")

	got, err := cosign.SignBlob(context.Background(), exe, blob, "k")
	if err != nil || got != cosign.BundlePath(blob) {
		t.Fatalf("SignBlob = %q, %v", got, err)
	}

	if _, err := os.Stat(got); err != nil {
		t.Errorf("bundle: %v", err)
	}
}

func TestSignBlobFailureIsCoRep003(t *testing.T) {
	t.Parallel()

	exe := script(t, "echo 'no key' >&2\nexit 1\n")

	_, err := cosign.SignBlob(context.Background(), exe, filepath.Join(t.TempDir(), "x"), "")

	failure := diagWithCode(err, diag.REP003)
	if failure == nil || !strings.Contains(failure.Diagnostic.Message, "no key") {
		t.Fatalf("err = %v, want CO-REP-003", err)
	}
}

func TestVerifyBlobPassesThePinnedFlags(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	exe := script(t, `echo "$@" > "`+args+`"`+"\n")
	blob := filepath.Join(dir, "record.json")
	bundle := cosign.BundlePath(blob)

	if err := os.WriteFile(bundle, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := cosign.VerifyBlob(context.Background(), exe, blob, bundle, cosign.VerifyOptions{
		Identity: "bot@example.com", Issuer: "https://issuer.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(args)

	want := "verify-blob --bundle " + bundle + " --certificate-identity bot@example.com " +
		"--certificate-oidc-issuer https://issuer.example.com " + blob
	if strings.TrimSpace(string(got)) != want {
		t.Errorf("arguments = %q, want %q", strings.TrimSpace(string(got)), want)
	}

	err = cosign.VerifyBlob(context.Background(), exe, blob, bundle, cosign.VerifyOptions{Key: "k.pub"})
	if err != nil {
		t.Fatal(err)
	}

	got, _ = os.ReadFile(args)
	if want := "verify-blob --bundle " + bundle + " --key k.pub " + blob; strings.TrimSpace(string(got)) != want {
		t.Errorf("arguments = %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

func TestVerifyBlobErrorKinds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blob := filepath.Join(dir, "record.json")
	bundle := cosign.BundlePath(blob)

	kind := func(err error) cosign.ErrorKind {
		t.Helper()

		var failure *cosign.VerifyError
		if !errors.As(err, &failure) {
			t.Fatalf("err = %v, want a *cosign.VerifyError", err)
		}

		if failure.Diagnostic().Diagnostic.Code != diag.REP004 {
			t.Errorf("diagnostic = %+v, want CO-REP-004", failure.Diagnostic().Diagnostic)
		}

		return failure.Kind
	}

	reject := script(t, "echo bad >&2\nexit 1\n")

	if got := kind(cosign.VerifyBlob(context.Background(), reject, blob, bundle, cosign.VerifyOptions{})); got != cosign.KindMissingBundle {
		t.Errorf("no bundle: kind = %s", got)
	}

	if err := os.WriteFile(bundle, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := kind(cosign.VerifyBlob(context.Background(), reject, blob, bundle, cosign.VerifyOptions{})); got != cosign.KindNotVerified {
		t.Errorf("rejected: kind = %s", got)
	}

	if got := kind(cosign.VerifyBlob(context.Background(), "/nonexistent/cosign", blob, bundle, cosign.VerifyOptions{})); got != cosign.KindCannotRun {
		t.Errorf("not startable: kind = %s", got)
	}
}

func TestVerifyBlobAttestationPassesThePinnedFlags(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	exe := script(t, `echo "$@" > "`+args+`"`+"\n")
	bundle := filepath.Join(dir, "statement.json")

	if err := os.WriteFile(bundle, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	const predicateType = "https://concordia.io/evidence/check-result/v1"

	tests := []struct {
		name string
		opts cosign.VerifyOptions
		want string
	}{
		{
			name: "keyless with a trusted root",
			opts: cosign.VerifyOptions{Identity: "bot@example.com", Issuer: "https://issuer.example.com", TrustedRoot: "/tr.json"},
			want: "--certificate-identity bot@example.com --certificate-oidc-issuer https://issuer.example.com --trusted-root /tr.json",
		},
		{name: "key", opts: cosign.VerifyOptions{Key: "k.pub"}, want: "--key k.pub"},
	}

	for _, tt := range tests {
		if err := cosign.VerifyBlobAttestation(context.Background(), exe, bundle, predicateType, tt.opts); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}

		got, _ := os.ReadFile(args)

		// cosign must not compare the subject: Concordia does, and the subject is often not a file.
		want := "verify-blob-attestation --bundle " + bundle + " --type " + predicateType + " --check-claims=false " + tt.want
		if strings.TrimSpace(string(got)) != want {
			t.Errorf("%s: arguments = %q, want %q", tt.name, strings.TrimSpace(string(got)), want)
		}
	}
}

func TestVerifyBlobAttestationErrorKinds(t *testing.T) {
	t.Parallel()

	bundle := filepath.Join(t.TempDir(), "statement.json")
	kind := func(err error) cosign.ErrorKind {
		t.Helper()

		var failure *cosign.VerifyError
		if !errors.As(err, &failure) {
			t.Fatalf("err = %v, want a *cosign.VerifyError", err)
		}

		return failure.Kind
	}

	reject := script(t, "echo bad >&2\nexit 1\n")
	verify := func(exe string) error {
		return cosign.VerifyBlobAttestation(context.Background(), exe, bundle, "https://example.test/p/v1", cosign.VerifyOptions{})
	}

	if got := kind(verify(reject)); got != cosign.KindMissingBundle {
		t.Errorf("no bundle: kind = %s", got)
	}

	if err := os.WriteFile(bundle, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := kind(verify(reject)); got != cosign.KindNotVerified {
		t.Errorf("rejected: kind = %s", got)
	}

	if got := kind(verify("/nonexistent/cosign")); got != cosign.KindCannotRun {
		t.Errorf("not startable: kind = %s", got)
	}
}

// TestVerifyBlobAttestationWithRealCosign runs the pinned cosign (.bin/cosign, or $COSIGN) against a recorded bundle
// of a keyed attestation made with a throwaway key (the private key was discarded). The bundle holds its Rekor
// inclusion proof, so the recorded trusted root is enough: the test needs no network.
func TestVerifyBlobAttestationWithRealCosign(t *testing.T) {
	t.Parallel()

	exe := realCosign(t)
	dir := filepath.Join("..", "..", "test", "fixtures", "assurance", "statements", "real-cosign")
	bundle := filepath.Join(dir, "check-result.sigstore.json")
	opts := cosign.VerifyOptions{Key: filepath.Join(dir, "cosign.pub"), TrustedRoot: filepath.Join(dir, "trusted_root.json")}
	ctx := context.Background()

	if err := cosign.VerifyBlobAttestation(ctx, exe, bundle, "https://concordia.io/evidence/check-result/v1", opts); err != nil {
		t.Fatalf("recorded attestation: %v", err)
	}

	err := cosign.VerifyBlobAttestation(ctx, exe, bundle, "https://concordia.io/evidence/measurement/v1", opts)
	requireNotVerified(t, err, "wrong predicate type")

	// A changed payload no longer matches the signature.
	data, rerr := os.ReadFile(bundle)
	if rerr != nil {
		t.Fatal(rerr)
	}

	tampered := filepath.Join(t.TempDir(), "tampered.sigstore.json")
	if werr := os.WriteFile(tampered, []byte(strings.Replace(string(data), `"payload":"`, `"payload":"AAAA`, 1)), 0o600); werr != nil {
		t.Fatal(werr)
	}

	err = cosign.VerifyBlobAttestation(ctx, exe, tampered, "https://concordia.io/evidence/check-result/v1", opts)
	requireNotVerified(t, err, "tampered bundle")

	other := filepath.Join(t.TempDir(), "other.pub")
	if werr := os.WriteFile(other, []byte(otherKey), 0o600); werr != nil {
		t.Fatal(werr)
	}

	opts.Key = other

	err = cosign.VerifyBlobAttestation(ctx, exe, bundle, "https://concordia.io/evidence/check-result/v1", opts)
	requireNotVerified(t, err, "other key")
}

// requireNotVerified fails the test unless err is a verification that cosign rejected.
func requireNotVerified(t *testing.T, err error, what string) {
	t.Helper()

	var failure *cosign.VerifyError
	if !errors.As(err, &failure) || failure.Kind != cosign.KindNotVerified {
		t.Errorf("%s: err = %v, want a rejected verification", what, err)
	}
}

// otherKey is a public key that made no signature of the fixtures.
const otherKey = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEf4hmczeVG5LHehneP9WQC8RtUVwQ
f5+SOHKBaiWAnJoKEQPbQx219CBv5vgEgsSd2Vwn8ilqjZbPaJrSuj0U2g==
-----END PUBLIC KEY-----
`

// realCosign gives the pinned cosign binary or skips the test.
func realCosign(t *testing.T) string {
	t.Helper()

	if p := os.Getenv(cosign.EnvVar); p != "" {
		return p
	}

	p, err := filepath.Abs(filepath.Join("..", "..", ".bin", "cosign"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(p); err != nil {
		t.Skip("the pinned cosign (.bin/cosign) is not installed")
	}

	return p
}
