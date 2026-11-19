package integration_test

import (
	"slices"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
)

// A38 for provenance: a statement that was changed after signing, one that names another digest, and a file that is
// not JSON. The fixtures are test/fixtures/assurance/provenance/. Each bad file is one warning, the claim that needs
// the statement is `unknown`, the claims that do not need it go on, and nothing shows a completed pass that depends
// on a bad statement.

const (
	tamperedPath  = provAttestations + provModified
	malformedPath = provAttestations + provMalformed
)

// warningsFor counts the CO-PRV-009 warnings that name the file.
func warningsFor(run *conformance.Run, path string) int {
	count := 0

	for i := range run.Diagnostics {
		if isStatementWarningFor(&run.Diagnostics[i], path) {
			count++
		}
	}

	return count
}

func isStatementWarningFor(d *diag.Diagnostic, path string) bool {
	if d.Code != diag.PRV009 || d.Severity != diag.SeverityWarning {
		return false
	}

	return d.Source != nil && d.Source.URI == path
}

// assertStatementWarnings checks the number of CO-PRV-009 warnings for each path.
func assertStatementWarnings(t *testing.T, run *conformance.Run, want map[string]int) {
	t.Helper()

	for path, count := range want {
		if got := warningsFor(run, path); got != count {
			t.Errorf("warnings for %s = %d, want %d", path, got, count)
		}
	}
}

// assertRuleVerdict checks the verdict of a rule; the failure shows the reasons.
func assertRuleVerdict(t *testing.T, run *conformance.Run, rule string, want conformance.RuleVerdict) {
	t.Helper()

	if got := provRuleOf(t, run, rule); got.Verdict != want {
		t.Errorf("%s = %s (%+v), want %s", rule, got.Verdict, got.Reasons, want)
	}
}

func tamperWorld(attestations ...string) *provWorld {
	return &provWorld{
		attestations: attestations, sidecarImage: provRepository + "@" + provDigestA,
		records: []provRecord{
			{path: "deploy-1.json", digest: provDigestA, outcome: "succeeded", age: 2 * time.Hour},
			{path: "deploy-2.json", container: "sidecar", digest: provDigestA, outcome: "succeeded", age: 2 * time.Hour},
			{path: "observed-1.json", digest: provDigestA, age: time.Hour},
			{path: "observed-2.json", container: "sidecar", digest: provDigestA, age: time.Hour},
		},
	}
}

func TestTamperedStatementsGiveWarningsAndAnUnknownLineage(t *testing.T) {
	t.Parallel()

	run := tamperWorld(provModified, provWrong, provMalformed).runProduction(t).run

	// One warning for each bad file. The statement for another digest is a valid file: it is not linked, not a warning.
	assertStatementWarnings(t, run, map[string]int{tamperedPath: 1, malformedPath: 1, provAttestations + provWrong: 0})

	// The claim that depends on the statements is unknown, and no result shows a pass that depends on them.
	app := provLineageOf(t, run, "app")
	if !isUnknownLineageWithoutStatement(app) {
		t.Errorf("app lineage = %+v", app)
	}

	if !isOnlyTheChangedAttestationRejected(app) {
		t.Errorf("rejected = %+v, want the changed attestation with its reason", app.Rejected)
	}

	assertNoLineagePass(t, run)

	if rule := provRuleOf(t, run, "ASR-PRV-001"); rule.Verdict == conformance.VerdictPass {
		t.Errorf("ASR-PRV-001 = %s", rule.Verdict)
	}

	// The statements are listed with their actual integrity: the changed one is not authenticated.
	assertTamperedStatementNotAuthenticated(t, run)

	// The claims that do not read the statements continue: the deployment records, the observation, and the
	// completeness of the rendered evidence.
	if !deploymentClaimsPass(&app.Deployment) {
		t.Errorf("deployment claims = %+v", app.Deployment)
	}

	assertRuleVerdict(t, run, "ASR-REN-001", conformance.VerdictPass)
	assertRuleVerdict(t, run, "ASR-PRV-002", conformance.VerdictPass)
}

func assertNoLineagePass(t *testing.T, run *conformance.Run) {
	t.Helper()

	for i := range run.Assurance.Lineage {
		if entry := &run.Assurance.Lineage[i]; entry.Verdict == conformance.VerdictPass {
			t.Errorf("a lineage pass without a good statement: %+v", entry)
		}
	}
}

func assertTamperedStatementNotAuthenticated(t *testing.T, run *conformance.Run) {
	t.Helper()

	for i := range run.Assurance.Statements {
		if summary := &run.Assurance.Statements[i]; summary.Path == tamperedPath && isAuthenticatedStatement(summary) {
			t.Errorf("statement summary = %+v", *summary)
		}
	}
}

func isUnknownLineageWithoutStatement(entry *conformance.LineageEntry) bool {
	if entry.Verdict != conformance.VerdictUnknown || entry.Reason != conformance.ReasonNoEvidenceSupplied {
		return false
	}

	return entry.Statement == nil
}

func isOnlyTheChangedAttestationRejected(entry *conformance.LineageEntry) bool {
	if len(entry.Rejected) != 1 || entry.Rejected[0].Path != tamperedPath {
		return false
	}

	return slices.Contains(entry.Rejected[0].Details, "integrity-failure")
}

// isAuthenticatedStatement reports whether a statement summary claims more than a changed statement can have.
func isAuthenticatedStatement(summary *evidence.StatementSummary) bool {
	if summary.Admissible || summary.Signature != statement.SignatureFailed {
		return true
	}

	return summary.Integrity == "producer-authenticated"
}

func deploymentClaimsPass(deployment *conformance.LineageDeployment) bool {
	verdicts := []string{string(deployment.DeploymentSucceeded.Verdict), string(deployment.DeployedDigest.Verdict), string(deployment.RuntimeDrift.Verdict)}

	return !slices.ContainsFunc(verdicts, func(verdict string) bool { return verdict != "pass" })
}

func TestBadStatementsAmongValidOnesDoNotStopTheValidOnes(t *testing.T) {
	t.Parallel()

	run := tamperWorld(provShop, provMalformed, provModified).runProduction(t).run

	assertStatementWarnings(t, run, map[string]int{tamperedPath: 1, malformedPath: 1, provAttestations + provShop: 0})

	// The valid statement is used: lineage passes, and the changed one is listed as considered and rejected.
	app := provLineageOf(t, run, "app")
	if !isPassedByStatement(app, provAttestations+provShop) {
		t.Errorf("app lineage = %+v", app)
	}

	if len(app.Rejected) != 1 || app.Rejected[0].Path != tamperedPath {
		t.Errorf("rejected = %+v", app.Rejected)
	}

	assertRuleVerdict(t, run, "ASR-PRV-001", conformance.VerdictPass)
}

func isPassedByStatement(entry *conformance.LineageEntry, path string) bool {
	if entry.Verdict != conformance.VerdictPass || entry.Statement == nil {
		return false
	}

	return entry.Statement.Path == path
}

func TestAnUnsignedStatementIsNotAuthenticated(t *testing.T) {
	t.Parallel()

	// The same bundle that verifies in the other tests, but the fake cosign was not given its bytes.
	w := tamperWorld(provShop)
	w.unsigned = []string{provShop}

	run := w.runProduction(t).run

	app := provLineageOf(t, run, "app")
	if app.Verdict != conformance.VerdictUnknown || len(app.Rejected) != 1 {
		t.Errorf("app lineage = %+v", app)
	}

	if got := warningsFor(run, provAttestations+provShop); got != 1 {
		t.Errorf("warnings = %d, want 1 for the signature that failed", got)
	}
}
