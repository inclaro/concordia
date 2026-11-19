package provenance_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/provenance"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
)

// fixtures is test/fixtures/assurance/provenance: signed provenance bundles of the shop, the same bundle changed
// after signing, bundles that name another digest, a builder or source that the workspace does not trust, and a file
// that is not JSON.
func fixtures() string {
	return filepath.Join("..", "..", "..", "test", "fixtures", "assurance", "provenance")
}

const (
	fixtureShop      = "shop.provenance.sigstore.json"
	fixtureModified  = "modified.provenance.sigstore.json"
	fixtureWrong     = "wrong-subject.provenance.sigstore.json"
	fixtureEvil      = "untrusted-builder.provenance.sigstore.json"
	fixtureForeign   = "foreign-source.provenance.sigstore.json"
	fixtureMalformed = "malformed.provenance.json"
)

// signedFixtures are the bundles whose bytes the fake cosign knows as signed. The modified bundle is not among
// them: its payload differs from the signed one, so verification fails, as it does with real cosign.
var signedFixtures = []string{fixtureShop, fixtureWrong, fixtureEvil, fixtureForeign}

// fakeCosign stands in for cosign: it accepts a bundle exactly when its bytes are those of a signed fixture, which is
// what a signature over the payload means.
func fakeCosign(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	sums := make([]string, 0, len(signedFixtures))

	for _, name := range signedFixtures {
		data, err := os.ReadFile(filepath.Join(fixtures(), name))
		if err != nil {
			t.Fatal(err)
		}

		sum := sha256.Sum256(data)
		sums = append(sums, hex.EncodeToString(sum[:]))
	}

	script := `#!/bin/sh
bundle=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--bundle" ]; then bundle="$2"; fi
  shift
done
sum=$(sha256sum "$bundle" | cut -d' ' -f1)
case " ` + strings.Join(sums, " ") + ` " in
  *" $sum "*) echo "Verified OK" >&2; exit 0 ;;
esac
echo "Error: verifying blob attestation: no matching signatures" >&2
exit 1
`

	path := filepath.Join(t.TempDir(), "cosign")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return path
}

// verifyFixtures loads the fixture files and verifies each with the verifier, then admits the provenance of each.
func verifyFixtures(t *testing.T, names ...string) (statements []provenance.Statement, diags []diag.Diagnostic) {
	t.Helper()

	exe := fakeCosign(t)
	verifier := &statement.Verifier{Getenv: func(key string) string {
		if key == "COSIGN" {
			return exe
		}

		return ""
	}}

	cfg := config()

	loaded, diags := statement.LoadAll(names, func(path string) ([]byte, error) {
		return os.ReadFile(filepath.Join(fixtures(), path))
	})

	for i := range loaded {
		verification, err := verifier.Verify(context.Background(), &statement.VerifyInput{
			Path: filepath.Join(fixtures(), loaded[i].Path), Loaded: loaded[i], Producers: cfg.Producers,
		})
		if err != nil {
			t.Fatal(err)
		}

		diags = append(diags, verification.Diagnostics(loaded[i].Path)...)

		stmt, err := provenance.NewStatement(&loaded[i], verification, obligation(), cfg)
		if err != nil {
			t.Fatal(err)
		}

		statements = append(statements, stmt)
	}

	return statements, diags
}

func lineageOf(t *testing.T, digest string, statements []provenance.Statement) provenance.LineageResult {
	t.Helper()

	contract := provenance.NewTrustContract(config())
	occ := occurrence(t, "registry.example/shop@"+digest)

	return provenance.Lineage(&occ, statements, &contract)
}

func TestSignedFixtureGivesAPass(t *testing.T) {
	t.Parallel()

	statements, diags := verifyFixtures(t, fixtureShop)

	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}

	if got := lineageOf(t, digestA, statements); got.Verdict != provenance.VerdictPass || got.Statement.Integrity != assurance.IntegrityAuthenticated {
		t.Errorf("result = %+v", got)
	}
}

// TestModifiedAttestationIsUnknownWithAWarning is A38: the attestation was changed after signing.
func TestModifiedAttestationIsUnknownWithAWarning(t *testing.T) {
	t.Parallel()

	statements, diags := verifyFixtures(t, fixtureModified)

	if !oneWarningAbout(diags, fixtureModified) {
		t.Errorf("diagnostics = %+v, want one warning that names the file", diags)
	}

	got := lineageOf(t, digestA, statements)
	if !isUnknownWithoutEvidence(&got) || len(got.Rejected) != 1 {
		t.Fatalf("result = %+v, want unknown with the modified statement rejected", got)
	}

	if details := got.Rejected[0].Details; !slices.Equal(details, []assurance.Detail{assurance.DetailIntegrityFailure}) {
		t.Errorf("details = %v", details)
	}
}

// TestModifiedAttestationDoesNotHideAGoodOne: a modified file next to a verified one does not turn the claim into
// a pass by itself, and does not stop the verified one from passing.
func TestModifiedAttestationNeverPassesAlone(t *testing.T) {
	t.Parallel()

	statements, diags := verifyFixtures(t, fixtureModified, fixtureShop)

	if len(diags) != 1 {
		t.Errorf("diagnostics = %+v, want one warning for the modified file", diags)
	}

	got := lineageOf(t, digestA, statements)
	if got.Verdict != provenance.VerdictPass || got.Statement.Path != fixtureShop {
		t.Errorf("result = %+v", got)
	}

	if !slices.Equal(rejectedPaths(got.Rejected), []string{fixtureModified}) {
		t.Errorf("result = %+v", got)
	}
}

func TestWrongSubjectDigestIsNotLinked(t *testing.T) {
	t.Parallel()

	statements, diags := verifyFixtures(t, fixtureWrong)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v", diags)
	}

	// The statement is genuine and names `bbbb`: it supports the claim for that digest only.
	got := lineageOf(t, digestA, statements)
	if got.Verdict != provenance.VerdictUnknown || got.Detail != "no provenance for this digest" {
		t.Errorf("result = %+v, want unknown: not linked", got)
	}

	if len(got.Rejected) != 0 {
		t.Errorf("result = %+v, want unknown: not linked", got)
	}

	if other := lineageOf(t, digestB, statements); other.Verdict != provenance.VerdictPass {
		t.Errorf("result for the digest the statement names = %+v", other)
	}
}

func TestMalformedFileAmongValidOnesGivesOneWarning(t *testing.T) {
	t.Parallel()

	statements, diags := verifyFixtures(t, fixtureShop, fixtureMalformed, fixtureWrong)

	if len(statements) != 2 {
		t.Fatalf("statements = %d, want the two valid files", len(statements))
	}

	if !onePRV009(diags, fixtureMalformed) {
		t.Errorf("diagnostics = %+v, want one warning that names the malformed file", diags)
	}

	if got := lineageOf(t, digestA, statements); got.Verdict != provenance.VerdictPass {
		t.Errorf("result = %+v", got)
	}
}

func TestVerifiedProvenanceOfAnUntrustedBuilderOrSource(t *testing.T) {
	t.Parallel()

	statements, _ := verifyFixtures(t, fixtureEvil)
	if got := lineageOf(t, digestA, statements); got.Verdict != provenance.VerdictViolation || !strings.Contains(got.Builder, "evil") {
		t.Errorf("untrusted builder: %+v", got)
	}

	statements, _ = verifyFixtures(t, fixtureForeign)
	if got := lineageOf(t, digestA, statements); got.Verdict != provenance.VerdictUnknown || !slices.Equal(got.Missing, []string{provenance.MissingSource}) {
		t.Errorf("foreign source: %+v", got)
	}
}

// TestIndependentClaimsContinueAfterATamperedStatement: the deployment claims do not depend on the provenance.
func TestIndependentClaimsContinueAfterATamperedStatement(t *testing.T) {
	t.Parallel()

	statements, _ := verifyFixtures(t, fixtureModified)
	if got := lineageOf(t, digestA, statements); got.Verdict == provenance.VerdictPass {
		t.Fatalf("lineage = %+v", got)
	}

	occ := occurrence(t, "registry.example/shop:1.4")
	rec := record(t, &recordSpec{path: "build/records/deploy-1.json"})

	got := provenance.Deployment(&occ, &provenance.Evidence{Records: []provenance.Record{rec}})
	if got.DeploymentSucceeded.Verdict != provenance.VerdictPass || got.DeployedDigest.Verdict != provenance.VerdictPass {
		t.Errorf("deployment = %+v", got)
	}
}
