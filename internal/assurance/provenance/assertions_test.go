package provenance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/provenance"
	"github.com/inclaro/concordia/internal/diag"
)

// refKey is the path, the producer, and the integrity of a statement reference. The tests compare these and not the
// digest of the file.
type refKey struct {
	path      string
	producer  string
	integrity assurance.Integrity
}

// keyOf gives the key of the reference. The key of no reference is the zero key.
func keyOf(ref *provenance.Ref) refKey {
	if ref == nil {
		return refKey{}
	}

	return refKey{path: ref.Path, producer: ref.Producer, integrity: ref.Integrity}
}

// rejectedPaths gives the paths of the rejected statements, in order.
func rejectedPaths(rejected []provenance.Rejected) []string {
	paths := make([]string, 0, len(rejected))

	for i := range rejected {
		paths = append(paths, rejected[i].Path)
	}

	return paths
}

// rejectedWith says whether exactly one statement is rejected, with exactly these details.
func rejectedWith(rejected []provenance.Rejected, details ...assurance.Detail) bool {
	return len(rejected) == 1 && slices.Equal(rejected[0].Details, details)
}

// isUnknownWithoutEvidence says whether the lineage is `unknown` because no evidence was supplied.
func isUnknownWithoutEvidence(got *provenance.LineageResult) bool {
	return got.Verdict == provenance.VerdictUnknown && got.Reason == provenance.ReasonNoEvidenceSupplied
}

// onePRV009 says whether diags is exactly one CO-PRV-009 diagnostic that names the file.
func onePRV009(diags []diag.Diagnostic, name string) bool {
	if len(diags) != 1 {
		return false
	}

	return diags[0].Code == diag.PRV009 && strings.Contains(diags[0].Message, name)
}

// oneWarningAbout is onePRV009 for a diagnostic that is a warning.
func oneWarningAbout(diags []diag.Diagnostic, name string) bool {
	return onePRV009(diags, name) && diags[0].Severity == diag.SeverityWarning
}

// driftKey is the part of a drift finding that the tests compare: the source, the digest, and for the observed side
// the path of the statement.
type driftKey struct {
	observedSource string
	observedDigest string
	observedPath   string
	expectedSource string
	expectedDigest string
}

func driftKeyOf(finding *provenance.Drift) driftKey {
	return driftKey{
		observedSource: finding.Observed.Source, observedDigest: finding.Observed.Digest, observedPath: keyOf(finding.Observed.Statement).path,
		expectedSource: finding.Expected.Source, expectedDigest: finding.Expected.Digest,
	}
}

// checkDriftFindings checks that the findings are the wanted ones, in order.
func checkDriftFindings(t *testing.T, got, want []provenance.Drift) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("findings = %+v", got)
	}

	for i := range got {
		if driftKeyOf(&got[i]) != driftKeyOf(&want[i]) {
			t.Errorf("finding %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
