package conformance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	scopedAnchorSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	scopedHeadSHA   = "abc123"
	scopedRegister  = "exceptions/waivers.yaml"
	scopedSecret    = "sk_live_9f8e7d6c5b4a39281706"
)

// scopedWorkspace is the workspace with one authority of role exception.
func scopedWorkspace(extra string) string {
	return wsText(`assessments:
  paths: []
  trustAnchor: { ref: origin/main }
  authorities:
    - { id: exception-board, role: exception, mechanism: protected-review, reviewers: [alice@example.com], paths: [exceptions/waivers.yaml] }
`) + extra
}

// scopedEntry is a scoped waiver of CO-TYP-001 for the subject (the absent workload of billing-service).
func scopedEntry(scope, extra string) string {
	return "  - id: WV-1\n    rule: CO-TYP-001\n    scope: " + scope + "\n    owner: team-platform\n    reason: 'token " + scopedSecret + "'\n" +
		"    expires: 2026-12-31\n    approver: { identity: alice@example.com, authority: exception-board }\n" + extra
}

func register(entries ...string) string {
	return "apiVersion: concordia.io/v1alpha1\nkind: ExceptionRegister\nwaivers:\n" + strings.Join(entries, "")
}

// scopedArgs is the input of scopedSetup: the workspace text, the candidate register, the register at the trust anchor,
// and the other files of the setup.
type scopedArgs struct {
	extra     map[string]string
	ws        string
	candidate string
	anchor    string
}

// scopedFiles gives the files of the setup: the typed rule CO-TYP-001, the candidate register, and the extra files.
func scopedFiles(args *scopedArgs) map[string]string {
	files := typedFiles("", typedRuleText("CO-TYP-001", "", requireImage))
	files[scopedRegister] = args.candidate

	for k, v := range args.extra {
		files[k] = v
	}

	return files
}

// scopedSetup runs the typed rule CO-TYP-001 (one `violation`, subject billing-service) with the candidate register
// and the register at the trust anchor.
func scopedSetup(t *testing.T, args *scopedArgs) *setup {
	t.Helper()

	t.Setenv("CONCORDIA_NOW", "2026-06-01T00:00:00Z")

	s := newSetup(t, scopedFiles(args), &setupOpts{ws: args.ws})

	// The fake git of the setup, with the trust anchor: origin/main is an ancestor of HEAD and holds the register.
	base, ok := s.deps.CreateGit("").(*workspace.FakeGit)
	if !ok || base.Rev != scopedHeadSHA {
		t.Fatalf("the setup git is %T at %s", s.deps.CreateGit(""), base.Rev)
	}

	base.Refs = map[string]string{"origin/main": scopedAnchorSHA}
	base.Ancestors = map[[2]string]bool{{scopedAnchorSHA, scopedHeadSHA}: true}
	base.FilesAt = map[string]map[string]string{scopedAnchorSHA: {"concordia.yaml": args.ws, scopedRegister: args.anchor}}
	s.deps.CreateGit = func(string) workspace.GitPort { return base }

	return s
}

func onlyRule(o *conformance.CheckOptions) { o.Rules = []string{"CO-TYP-001"} }

// scopedCheck runs the rule with the register text as both the candidate and the register at the trust anchor.
func scopedCheck(t *testing.T, text string) *conformance.Run {
	t.Helper()

	return scopedSetup(t, &scopedArgs{ws: scopedWorkspace(""), candidate: text, anchor: text}).mustCheck(t, onlyRule)
}

// scopedBillingRegister is the register with one waiver of the subject billing-service.
func scopedBillingRegister() string {
	return register(scopedEntry("{ subject: billing-service }", ""))
}

func diagnosticsOf(run *conformance.Run, code string) []diag.Diagnostic {
	var out []diag.Diagnostic

	for _, d := range run.Diagnostics {
		if d.Code == code {
			out = append(out, d)
		}
	}

	return out
}

// scopedRecordFiles gives the two places that hold the record of the scoped waivers: the run and the bundle file.
func scopedRecordFiles(t *testing.T, run *conformance.Run) map[string][]byte {
	t.Helper()

	record, err := json.Marshal(run.Exceptions.ScopedWaivers)
	if err != nil {
		t.Fatal(err)
	}

	file, err := conformance.ScopedWaiversJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	return map[string][]byte{"scopedWaivers": record, "exceptions/scoped.json": file}
}

// assertWaivedByWV1 checks that the only finding of the run is waived by WV-1 and that the verdict stays.
func assertWaivedByWV1(t *testing.T, run *conformance.Run) {
	t.Helper()

	if len(run.Findings) != 1 {
		t.Fatalf("findings = %+v", run.Findings)
	}

	f := &run.Findings[0]
	if f.Disposition != exceptions.DispositionWaived || f.DispositionRef == nil {
		t.Fatalf("findings = %+v", run.Findings)
	}

	if *f.DispositionRef != "WV-1" {
		t.Fatalf("findings = %+v", run.Findings)
	}

	if f.Result != conformance.VerdictViolation || run.Summary.Verdict != conformance.VerdictViolation {
		t.Errorf("a waiver never changes the verdict: result %s, summary %s", f.Result, run.Summary.Verdict)
	}
}

// assertWV1Record checks the identity, rule, scope, state, expiry, and approver of the record of WV-1.
func assertWV1Record(t *testing.T, got *conformance.ScopedWaiver) {
	t.Helper()

	const message = "record = %+v"

	if got.ID != "WV-1" || got.Rule != "CO-TYP-001" {
		t.Errorf(message, *got)
	}

	if got.Scope.Subject != "billing-service" {
		t.Errorf(message, *got)
	}

	if got.State != "active" || got.Expires != "2026-12-31" {
		t.Errorf(message, *got)
	}

	if got.Approver.Identity != "alice@example.com" || got.Approver.Authority != "exception-board" {
		t.Errorf(message, *got)
	}
}

// assertWV1Verification checks that the approver of WV-1 is verified by protected review.
func assertWV1Verification(t *testing.T, got *conformance.ScopedWaiver) {
	t.Helper()

	v := got.Verification
	if !v.Verified || v.Mechanism != workspace.MechanismProtectedReview {
		t.Errorf("verification = %+v", v)
	}

	if v.Authority != "exception-board" || v.Reason != "" {
		t.Errorf("verification = %+v", v)
	}
}

// assertNoWaiverWarnings checks that the run holds none of the diagnostics about waivers.
func assertNoWaiverWarnings(t *testing.T, run *conformance.Run) {
	t.Helper()

	for _, code := range []string{diag.EXC009, diag.EXC010, diag.EXC011, diag.EXC003} {
		if d := diagnosticsOf(run, code); len(d) != 0 {
			t.Errorf("unexpected %s: %+v", code, d)
		}
	}
}

// assertWV1Applied checks everything the record of an applied waiver WV-1 holds.
func assertWV1Applied(t *testing.T, run *conformance.Run) {
	t.Helper()

	if len(run.Exceptions.ScopedWaivers) != 1 {
		t.Fatalf("scoped waivers = %+v", run.Exceptions.ScopedWaivers)
	}

	got := &run.Exceptions.ScopedWaivers[0]
	assertWV1Record(t, got)
	assertWV1Verification(t, got)

	if got.CompensatingEvidence == nil {
		t.Errorf("covered = %v, evidence = %v", got.CoveredFindings, got.CompensatingEvidence)
	}

	if len(got.CoveredFindings) != 1 {
		t.Fatalf("covered = %v, evidence = %v", got.CoveredFindings, got.CompensatingEvidence)
	}

	if got.CoveredFindings[0] != run.Findings[0].Fingerprint {
		t.Errorf("covered = %v, evidence = %v", got.CoveredFindings, got.CompensatingEvidence)
	}
}

// assertWaiversInRegister checks that the register holds one waiver and no orphan, and that the waiver has no finding.
func assertWaiversInRegister(t *testing.T, run *conformance.Run) {
	t.Helper()

	if len(run.Exceptions.OrphanedWaivers) != 0 || len(run.Exceptions.Waivers) != 1 {
		t.Fatalf("exceptions = %+v", run.Exceptions)
	}

	if run.Exceptions.Waivers[0].Finding != "" {
		t.Errorf("exceptions = %+v", run.Exceptions)
	}
}

// Scenario: Waiver for one environment.
// Scenario: Fingerprints differ between runs.
func TestAScopedWaiverOnTheAnchorWaivesTheFindingAndIsRecorded(t *testing.T) {
	run := scopedCheck(t, scopedBillingRegister())

	assertWaivedByWV1(t, run)
	assertWV1Applied(t, run)
	assertNoWaiverWarnings(t, run)
	assertWaiversInRegister(t, run)

	encoded, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	if errs := conformance.ValidateResultJSON(encoded); len(errs) > 0 {
		t.Fatalf("results.json does not validate: %v", errs)
	}

	// No secret value of the waiver text reaches the record of the scoped waiver or the bundle file.
	for name, data := range scopedRecordFiles(t, run) {
		if strings.Contains(string(data), scopedSecret) {
			t.Errorf("%s holds the free text of the waiver: %s", name, data)
		}

		if strings.Contains(string(data), "token") {
			t.Errorf("%s holds the free text of the waiver: %s", name, data)
		}
	}
}

// assertNotOnAnchorWarning checks the one EXC009 warning that names the approver and the trust anchor.
func assertNotOnAnchorWarning(t *testing.T, run *conformance.Run) {
	t.Helper()

	warnings := diagnosticsOf(run, diag.EXC009)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v", warnings)
	}

	if !strings.Contains(warnings[0].Message, "alice@example.com") || !strings.Contains(warnings[0].Message, "trust anchor") {
		t.Fatalf("warnings = %+v", warnings)
	}

	if warnings[0].Severity != diag.SeverityWarning {
		t.Fatalf("warnings = %+v", warnings)
	}
}

// Scenario: Candidate adds its own waiver.
func TestAScopedWaiverThatIsNotOnTheAnchorIsNotApplied(t *testing.T) {
	text := scopedBillingRegister()
	run := scopedSetup(t, &scopedArgs{ws: scopedWorkspace(""), candidate: text, anchor: register()}).mustCheck(t, onlyRule)

	if run.Findings[0].Disposition != exceptions.DispositionActive || run.Findings[0].DispositionRef != nil {
		t.Fatalf("finding = %+v, want active", run.Findings[0])
	}

	assertNotOnAnchorWarning(t, run)

	got := run.Exceptions.ScopedWaivers[0]
	if got.Verification.Verified || got.Verification.Reason != "not-on-anchor" {
		t.Errorf("record = %+v", got)
	}

	if len(got.CoveredFindings) != 0 {
		t.Errorf("record = %+v", got)
	}

	// A waiver that is not applied is not an unmatched waiver.
	if d := diagnosticsOf(run, diag.EXC010); len(d) != 0 || len(run.Exceptions.OrphanedWaivers) != 0 {
		t.Errorf("EXC010 = %+v, orphaned = %v", d, run.Exceptions.OrphanedWaivers)
	}
}

func TestAScopedWaiverWithoutATrustAnchorIsNotApplied(t *testing.T) {
	text := scopedBillingRegister()
	ws := wsText("") // no `assessments` key: no authority and no anchor.

	run := scopedSetup(t, &scopedArgs{ws: ws, candidate: text, anchor: text}).mustCheck(t, onlyRule)
	if run.Findings[0].Disposition != exceptions.DispositionActive || len(diagnosticsOf(run, diag.EXC009)) != 1 {
		t.Fatalf("finding = %+v, diagnostics = %+v", run.Findings[0], run.Diagnostics)
	}

	if got := run.Exceptions.ScopedWaivers[0].Verification; got.Verified || got.Reason != "trust-anchor-unreadable" {
		t.Errorf("verification = %+v", got)
	}
}

// assertUnmatchedWarning checks the one EXC010 warning that names WV-1 and the absence of an EXC003 warning.
func assertUnmatchedWarning(t *testing.T, run *conformance.Run) {
	t.Helper()

	warnings := diagnosticsOf(run, diag.EXC010)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "WV-1") {
		t.Fatalf("warnings = %+v", run.Diagnostics)
	}

	if len(diagnosticsOf(run, diag.EXC003)) != 0 {
		t.Fatalf("warnings = %+v", run.Diagnostics)
	}
}

// Scenario: Obligation resolved.
func TestAnUnmatchedScopedWaiverWarnsAndStaysInTheRegister(t *testing.T) {
	run := scopedCheck(t, register(scopedEntry("{ subject: ledger-service }", "")))

	if run.Findings[0].Disposition != exceptions.DispositionActive {
		t.Fatalf("finding = %+v", run.Findings[0])
	}

	assertUnmatchedWarning(t, run)

	if got := run.Exceptions.OrphanedWaivers; len(got) != 1 || got[0] != "WV-1" {
		t.Errorf("orphaned = %v", got)
	}

	if len(run.Exceptions.ScopedWaivers) != 1 {
		t.Fatalf("scoped = %+v", run.Exceptions.ScopedWaivers)
	}

	scoped := run.Exceptions.ScopedWaivers[0]
	if !scoped.Verification.Verified || len(scoped.CoveredFindings) != 0 {
		t.Errorf("scoped = %+v", run.Exceptions.ScopedWaivers)
	}
}

// scopedEvidenceCheck runs the rule with a waiver that cites docs/mitigation.md under the evidence line.
func scopedEvidenceCheck(t *testing.T, evidence string) *conformance.Run {
	t.Helper()

	text := register(scopedEntry("{ subject: billing-service }", evidence))
	args := &scopedArgs{ws: scopedWorkspace(""), candidate: text, anchor: text, extra: map[string]string{"docs/mitigation.md": "controls\n"}}

	return scopedSetup(t, args).mustCheck(t, onlyRule)
}

// Scenario: Digest differs.
func TestACompensatingEvidenceDigestMismatchWarnsAndKeepsTheVerdict(t *testing.T) {
	run := scopedEvidenceCheck(t, "    compensatingEvidence:\n      - { path: docs/mitigation.md, sha256: sha256:"+strings.Repeat("1", 64)+" }\n")
	assertEvidenceMismatch(t, run)

	// The same file with its digest resolves.
	run = scopedEvidenceCheck(t, "    compensatingEvidence:\n      - { path: docs/mitigation.md, sha256: "+digest.OfBytes([]byte("controls\n"))+" }\n")
	assertEvidenceResolved(t, run)
}

// assertEvidenceMismatch checks that a digest mismatch warns, keeps the waiver applied, and keeps the verdict.
func assertEvidenceMismatch(t *testing.T, run *conformance.Run) {
	t.Helper()

	if run.Findings[0].Disposition != exceptions.DispositionWaived || run.Findings[0].Result != conformance.VerdictViolation {
		t.Fatalf("the waiver still applies and the verdict stays: %+v", run.Findings[0])
	}

	warnings := diagnosticsOf(run, diag.EXC011)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "docs/mitigation.md") {
		t.Fatalf("warnings = %+v", run.Diagnostics)
	}

	if got := run.Exceptions.ScopedWaivers[0].CompensatingEvidence; len(got) != 1 || got[0].State != exceptions.EvidenceDigestMismatch {
		t.Errorf("evidence = %+v", got)
	}
}

// assertEvidenceResolved checks that the evidence of the waiver resolves without a warning.
func assertEvidenceResolved(t *testing.T, run *conformance.Run) {
	t.Helper()

	got := run.Exceptions.ScopedWaivers[0].CompensatingEvidence
	if len(got) != 1 || got[0].State != exceptions.EvidenceResolved {
		t.Errorf("evidence = %+v, diagnostics = %+v", got, run.Diagnostics)
	}

	if len(diagnosticsOf(run, diag.EXC011)) != 0 {
		t.Errorf("evidence = %+v, diagnostics = %+v", got, run.Diagnostics)
	}
}

func TestAnInvalidScopedWaiverStopsTheRunWithEXC002(t *testing.T) {
	t.Run("both finding and scope", func(t *testing.T) {
		text := register(scopedEntry("{ subject: billing-service }", "    finding: sha256:"+strings.Repeat("1", 64)+"\n"))

		failure := scopedSetup(t, &scopedArgs{ws: scopedWorkspace(""), candidate: text, anchor: text}).failureOf(t, onlyRule)
		if failure.Diagnostic.Code != diag.EXC002 || failure.ExitCode != diag.InvalidInput {
			t.Errorf("failure = %+v", failure)
		}
	})

	t.Run("duration above exceptions.maxExpiryDays", func(t *testing.T) {
		text := scopedBillingRegister()
		args := &scopedArgs{ws: scopedWorkspace("exceptions: { maxExpiryDays: 30 }\n"), candidate: text, anchor: text}

		failure := scopedSetup(t, args).failureOf(t, onlyRule)
		if failure.Diagnostic.Code != diag.EXC002 || failure.ExitCode != diag.InvalidInput {
			t.Errorf("failure = %+v", failure)
		}

		if !strings.Contains(failure.Diagnostic.Message, "maxExpiryDays") {
			t.Errorf("failure = %+v", failure)
		}
	})
}

func TestSecretShapedTextInAScopedWaiverIsRedactedInTheRecord(t *testing.T) {
	const key = "AKIAIOSFODNN7EXAMPLE"

	text := strings.Replace(scopedBillingRegister(), "owner: team-platform", "owner: team-"+key, 1)
	run := scopedCheck(t, text)

	for name, data := range scopedRecordFiles(t, run) {
		if strings.Contains(string(data), key) {
			t.Errorf("%s holds the secret-shaped value: %s", name, data)
		}
	}
}
