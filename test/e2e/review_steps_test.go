package e2e_test

import (
	"slices"
	"strings"
	"testing"
)

// The steps of the scenarios in review_test.go: each helper is one check or one move of a scenario.

// rvWaiverRegister is the register of the scoped waivers that `concordia waiver add` writes.
const rvWaiverRegister = "exceptions/waivers.yaml"

// rvRequireVerdict wants the verdict of a rule of the results.
func rvRequireVerdict(t *testing.T, doc *rvDoc, rule, want string) {
	t.Helper()

	if got := doc.verdict(rule); got != want {
		t.Errorf("%s = %q, want %s", rule, got, want)
	}
}

// rvAllContained tells whether the list has every part.
func rvAllContained(list []string, parts ...string) bool {
	for _, part := range parts {
		if !contains(list, part) {
			return false
		}
	}

	return true
}

// rvTextHasAll tells whether the text has every part.
func rvTextHasAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}

	return true
}

// rvFirstCauseIs tells whether the first cause of an assessment has the reason and a detail with the part.
func rvFirstCauseIs(a *rvAssessment, reason, detailPart string) bool {
	if len(a.Causes) == 0 {
		return false
	}

	return a.Causes[0].Reason == reason && strings.Contains(a.Causes[0].Detail, detailPart)
}

// rvRemovalTrigger tells whether the trigger is the removal of a capability that reaches the assessment.
func rvRemovalTrigger(trigger *rvTrigger, assessmentRef string) bool {
	return trigger != nil && slices.Equal([]string{trigger.Class, trigger.Change, trigger.Assessment}, []string{"removal-capability-loss", "removed", assessmentRef})
}

// rvHasDiagnosticWith tells whether one diagnostic has every part in its message.
func rvHasDiagnosticWith(doc *rvDoc, parts ...string) bool {
	for _, d := range doc.Diagnostics {
		if rvTextHasAll(d.Message, parts...) {
			return true
		}
	}

	return false
}

// rvRequireRouteCurrent wants the approval on the anchor to cover the Ingress as it is. The incremental run writes the
// cache.
func rvRequireRouteCurrent(t *testing.T, r *repo) {
	t.Helper()

	base := r.check(0, "--incremental")
	base.assessment(t, "ASM-ROUTE").want(t, "current")

	if base.verdict("REV-APR-001") != "pass" {
		t.Errorf("before the change: REV-APR-001 = %q, want pass", base.verdict("REV-APR-001"))
	}
}

// rvExposeAdminPath adds a new path on the public host: the manifest changes, the assessment file does not.
func rvExposeAdminPath(r *repo) {
	r.t.Helper()

	r.editFile("k8s/prod/ingress.yaml", "          - path: /\n            pathType: Prefix\n",
		"          - path: /admin\n            pathType: Prefix\n            backend:\n              service:\n                name: api\n                port:\n                  number: 80\n"+
			"          - path: /\n            pathType: Prefix\n")
	r.commit("feat: expose /admin")
}

// rvRequireRouteMismatch wants the incremental run to find the approval of the route stale, and gives the assessment.
func rvRequireRouteMismatch(t *testing.T, incremental *rvDoc) *rvAssessment {
	t.Helper()

	if incremental.Analysis.Mode != "incremental" {
		t.Errorf("analysis.mode = %q, want incremental", incremental.Analysis.Mode)
	}

	a := incremental.assessment(t, "ASM-ROUTE")
	a.want(t, "review-required", "subject digest mismatch")

	if !anyContains(a.Details, "modified public-entry-point") {
		t.Errorf("details %v do not name the modified Ingress", a.Details)
	}

	rvRequireVerdict(t, incremental, "REV-APR-001", "violation")

	return a
}

// rvIsRouteTrigger tells whether the trigger is the modified Ingress that reaches the assessment of the route.
func rvIsRouteTrigger(trigger *rvTrigger) bool {
	got := []string{trigger.Class, trigger.Change, trigger.Dependency, trigger.Assessment, trigger.Action}

	return slices.Equal(got, []string{"exposure", "modified", rvIngressRoute, "ASM-ROUTE@1", "get review"})
}

// rvRequireRouteTrigger wants the report to name the changed dependency, the class, the assessment, and the shortest
// path; the assumption that the path could also take is listed with the evidence.
func rvRequireRouteTrigger(t *testing.T, a *rvAssessment) {
	t.Helper()

	if len(a.Triggers) != 1 {
		t.Fatalf("triggers = %+v, want one merged trigger", a.Triggers)
	}

	trigger := &a.Triggers[0]
	if !rvIsRouteTrigger(trigger) {
		t.Errorf("trigger = %+v", trigger)
	}

	if want := []string{rvIngressRoute, "ASM-ROUTE@1"}; strings.Join(trigger.Path, " -> ") != strings.Join(want, " -> ") {
		t.Errorf("path = %v, want %v", trigger.Path, want)
	}

	if !rvAllContained(trigger.Assumption, "ASM-ROUTE-1") || !rvAllContained(trigger.Evidence, "ASM-ROUTE-1", rvIngressRoute) {
		t.Errorf("assumption %v and evidence %v lack the assumption or the Ingress", trigger.Assumption, trigger.Evidence)
	}
}

// rvRequireChangedKeepsCurrency wants `check --changed` to reduce the edge-finding rules and never the currency; so does
// a full run.
func rvRequireChangedKeepsCurrency(t *testing.T, changed, incremental, full *rvDoc) {
	t.Helper()

	for _, f := range changed.Findings {
		if !strings.HasPrefix(f.RuleID, "REV-") {
			t.Errorf("--changed reports the finding of %s; only the review rules have findings here", f.RuleID)
		}
	}

	changed.assessment(t, "ASM-ROUTE").want(t, "review-required", "subject digest mismatch")

	if !sameJSON(changed.currencySection(), full.currencySection()) {
		t.Errorf("--changed gives another assessmentCurrency than a full run:\n%v\n%v", changed.currencySection(), full.currencySection())
	}

	if !sameJSON(incremental.currencySection(), full.currencySection()) {
		t.Errorf("--incremental gives another assessmentCurrency than a full run:\n%v\n%v", incremental.currencySection(), full.currencySection())
	}
}

// rvRequireRemovedIdentityTrigger wants the baseline identity to stay as a removal with its path to the assessment; no
// output omits it.
func rvRequireRemovedIdentityTrigger(t *testing.T, a *rvAssessment) {
	t.Helper()

	if !anyContains(a.Details, "removed runtime-identity") {
		t.Errorf("details %v do not name the removed identity", a.Details)
	}

	trigger := a.triggerFor(rvEdgeIdentity)
	if !rvRemovalTrigger(trigger, "ASM-EDGE@1") || strings.Join(trigger.Path, " -> ") != rvEdgeIdentity+" -> ASM-EDGE@1" {
		t.Errorf("trigger = %+v", trigger)
	}
}

// rvRequireRemovalBlocks wants the removed identity to be undisposed, and the approval rule and the disposition rule to
// block: the removal needs a recorded disposition.
func rvRequireRemovalBlocks(t *testing.T, doc *rvDoc) {
	t.Helper()

	if got := doc.undisposed("ASM-EDGE"); len(got) != 1 || got[0] != rvEdgeIdentity {
		t.Errorf("undisposed = %v", got)
	}

	if doc.verdict("REV-APR-001") != "violation" || doc.verdict("REV-DSP-001") != "violation" {
		t.Errorf("REV-APR-001 = %q, REV-DSP-001 = %q, want violation for both", doc.verdict("REV-APR-001"), doc.verdict("REV-DSP-001"))
	}
}

// rvRequireSecretRemoval wants the missing edge to be a removal and never "no change": CBM emits no edge when the
// declaration of the reference target is gone.
func rvRequireSecretRemoval(t *testing.T, doc *rvDoc, a *rvAssessment) {
	t.Helper()

	if !rvFirstCauseIs(a, "target-not-declared", "api-secrets") {
		t.Errorf("causes = %+v, want target-not-declared for api-secrets", a.Causes)
	}

	trigger := a.triggerFor("core/Secret/payments/api-secrets")
	if !rvRemovalTrigger(trigger, "ASM-SECRET@1") {
		t.Errorf("trigger = %+v", trigger)
	}

	if got := doc.undisposed("ASM-SECRET"); len(got) != 1 || !strings.Contains(got[0], "core/Secret/payments/api-secrets") {
		t.Errorf("undisposed = %v", got)
	}
}

// rvRequireFlagIsNotApproval wants the `reviewed` flag to leave the assessment without an approval and the gate blocked.
func rvRequireFlagIsNotApproval(t *testing.T, doc *rvDoc, a *rvAssessment) {
	t.Helper()

	if a.Approval != nil {
		t.Errorf("approval = %+v, want none", a.Approval)
	}

	if !anyContains(a.Details, "no verified approval binds ASM-FLAG@v1") || !anyContains(a.Details, "reviewed flag") {
		t.Errorf("details %v do not name the missing approval", a.Details)
	}

	if req := doc.required(t, "flag"); req.Verdict != "violation" || doc.verdict("REV-APR-001") != "violation" {
		t.Errorf("verdict of the required assessment = %q, REV-APR-001 = %q, want violation", req.Verdict, doc.verdict("REV-APR-001"))
	}
}

// rvRequireFlagStatus wants `assessment status` to say that no verified approval binds the assessment of the flag.
func rvRequireFlagStatus(t *testing.T, r *repo) {
	t.Helper()

	status := r.cli(rvEnv(), "assessment", "status")
	if status.code != 0 || !strings.Contains(status.stdout, "no verified approval binds ASM-FLAG@v1") {
		t.Errorf("assessment status (%d): %s", status.code, status.output())
	}
}

// rvEnvironmentMismatch tells whether the required assessment is review-required with the reason environment mismatch and
// is a violation.
func rvEnvironmentMismatch(req *rvRequiredResult) bool {
	if req.Currency != "review-required" || req.Verdict != "violation" {
		return false
	}

	return contains(req.Reasons, "environment mismatch")
}

// rvEdgeTriggerNamesBoth tells whether the trigger reaches ASM-EDGE@1, asks for a review and has both identities as
// evidence.
func rvEdgeTriggerNamesBoth(trigger *rvTrigger, oldIdentity, newIdentity string) bool {
	if trigger == nil || trigger.Assessment != "ASM-EDGE@1" {
		return false
	}

	return trigger.Action == "get review" && rvAllContained(trigger.Evidence, oldIdentity, newIdentity)
}

// rvRequireIdentityReplaced wants the old identity (a removal) and the new one (an addition) to reach the assessment as
// one trigger.
func rvRequireIdentityReplaced(t *testing.T, a *rvAssessment) {
	t.Helper()

	newIdentity := strings.Replace(rvEdgeIdentity, "api-sa", "api-sa-v2", 1)

	if trigger := a.triggerFor(rvEdgeIdentity); !rvEdgeTriggerNamesBoth(trigger, rvEdgeIdentity, newIdentity) {
		t.Fatalf("triggers = %+v, want one that names both identities and ASM-EDGE@1", a.Triggers)
	}

	if len(a.Triggers) != 1 {
		t.Errorf("triggers = %+v, want one", a.Triggers)
	}

	if !anyContains(a.Details, "removed runtime-identity") || !anyContains(a.Details, "added runtime-identity") {
		t.Errorf("details %v", a.Details)
	}
}

// rvRevocationDigest wants the first run of `assessment revoke` to refuse with CO-ASM-005 and print the digest of the
// approval, and gives the digest.
func rvRevocationDigest(t *testing.T, res cliResult) string {
	t.Helper()

	m := rvApprovalDigestRe.FindStringSubmatch(res.stdout)

	refused := res.code == 2 && strings.Contains(res.stderr, "CO-ASM-005")
	if !refused || m == nil {
		t.Fatalf("revoke without --confirm-subject: exit %d, want 2 with CO-ASM-005 and the digest: %s", res.code, res.output())
	}

	return m[1]
}

// rvRevokeOnCandidate revokes ASM-EDGE-v1-1 in the candidate and gives the record. The record is removed from the
// candidate again: the revocation is not applied on the candidate alone (protected-review).
func rvRevokeOnCandidate(t *testing.T, r *repo) string {
	t.Helper()

	args := []string{"assessment", "revoke", "ASM-EDGE-v1-1", "--reviewer", rvReviewer, "--reason", "gateway replaced"}

	// The first run prints the approval and its digest and writes nothing.
	digest := rvRevocationDigest(t, r.cli(rvEnv(), args...))

	if res := r.cli(rvEnv(), slices.Concat(args, []string{"--confirm-subject", digest})...); !rvWrote(res, "revocation written: "+rvEdgeRevoke) {
		t.Fatalf("revoke (%d): %s", res.code, res.output())
	}

	record := r.read(rvEdgeRevoke)

	r.rvRemoveFile(rvEdgeRevoke)

	return record
}

// rvRequireRevoked wants the approval on the anchor to be revoked and the required assessment to be a violation.
func rvRequireRevoked(t *testing.T, doc *rvDoc, a *rvAssessment) {
	t.Helper()

	if !anyContains(a.Details, "was revoked by ASM-EDGE-v1-1") || doc.required(t, "edge").Verdict != "violation" {
		t.Errorf("details %v, required %+v", a.Details, doc.required(t, "edge"))
	}
}

// rvRequireTerraformUnknown wants the approved assessment to be unknown with the reason unsupported-family, and the
// coverage regression of the selector to be a cause.
func rvRequireTerraformUnknown(t *testing.T, doc *rvDoc) {
	t.Helper()

	a := doc.assessment(t, "ASM-TERRAFORM")

	if a.Currency != "unknown" || a.UnknownReason != "unsupported-family" {
		t.Errorf("currency %q reason %q (causes %+v), want unknown with unsupported-family", a.Currency, a.UnknownReason, a.Causes)
	}

	if !anyCause(a, "no longer supports the selector via on public-entry-point") {
		t.Errorf("causes = %+v, want the coverage regression of the selector via", a.Causes)
	}

	if doc.verdict("REV-APR-001") != "unknown" || doc.required(t, "tf").Verdict != "unknown" {
		t.Errorf("REV-APR-001 = %q, required %+v, want unknown", doc.verdict("REV-APR-001"), doc.required(t, "tf"))
	}
}

// rvRequireLimitExceeded wants the assessment to be unknown with the reason limit-exceeded and a diagnostic that names
// the assessment and the limit.
func rvRequireLimitExceeded(t *testing.T, doc *rvDoc) {
	t.Helper()

	a := doc.assessment(t, "ASM-ROUTE")

	if a.Currency != "unknown" || a.UnknownReason != "limit-exceeded" {
		t.Errorf("currency %q reason %q (causes %+v), want unknown with limit-exceeded", a.Currency, a.UnknownReason, a.Causes)
	}

	rvRequireVerdict(t, doc, "REV-APR-001", "unknown")

	if !rvHasDiagnosticWith(doc, "ASM-ROUTE", "maxVisited") {
		t.Errorf("no diagnostic names ASM-ROUTE and maxVisited: %+v", doc.Diagnostics)
	}
}

// rvAuthorityRelaxed tells whether the only relaxation of the run is the one of the authority of the case.
func rvAuthorityRelaxed(doc *rvDoc) bool {
	relaxations := doc.Governance.Relaxations
	if len(relaxations) != 1 {
		return false
	}

	got := relaxations[0]

	return got.Relaxation && slices.Equal([]string{got.Kind, got.Subject}, []string{"authority", rvAuthority})
}

// rvEnforcementLoosened tells whether the only relaxation of the run is a looser enforcement entry.
func rvEnforcementLoosened(doc *rvDoc) bool {
	relaxations := doc.Governance.Relaxations
	if len(relaxations) != 1 {
		return false
	}

	got := relaxations[0]

	return got.Relaxation && slices.Equal([]string{got.Kind, got.Comparison}, []string{"enforcement", "looser"})
}

// rvFindingMessage gives the message of the last finding of the rule.
func rvFindingMessage(doc *rvDoc, rule string) string {
	var message string

	for _, f := range doc.Findings {
		if f.RuleID == rule {
			message = f.Message
		}
	}

	return message
}

// rvRequireAuthorityRelaxation wants the governance rule to be a violation that names the relaxation of the authority
// and its missing governance disposition.
func rvRequireAuthorityRelaxation(t *testing.T, doc *rvDoc) {
	t.Helper()

	rvRequireVerdict(t, doc, "REV-GOV-001", "violation")

	if !rvAuthorityRelaxed(doc) {
		t.Errorf("relaxations = %+v, want the relaxation of authority %s", doc.Governance.Relaxations, rvAuthority)
	}

	finding := rvFindingMessage(doc, "REV-GOV-001")
	if !rvTextHasAll(finding, "authority:"+rvAuthority, "without a governance disposition", "get review") {
		t.Errorf("finding = %q, want the relaxation of the authority and the governance disposition", finding)
	}
}

// rvAddWaiver adds a scoped waiver of the approval rule for the subject with `concordia waiver add` and gives the
// register that it wrote.
func rvAddWaiver(t *testing.T, r *repo, subject string) string {
	t.Helper()

	res := r.cli(rvEnv(), "waiver", "add", "--rule", "REV-APR-001", "--subject", subject, "--environment", "production",
		"--approver", rvReviewer+"@exception-board", "--owner", "team-platform", "--reason", "The review board meets after the release", "--expires", "2026-12-31")
	if res.code != 0 {
		t.Fatalf("waiver add: %d %s", res.code, res.output())
	}

	return r.read(rvWaiverRegister)
}

// rvWaiverViolation: the waiver applies to a violation. A waiver that is only in the candidate is not applied; on the
// anchor it is, and the verdict stays.
func rvWaiverViolation(t *testing.T, exception string) {
	t.Helper()

	spec := &rvSpec{name: "waiver-violation", docs: []string{"ASM-EDGE"}, required: []rvRequired{{id: "edge", assessment: "ASM-EDGE"}}, authorities: exception}
	r := newRvRepo(t, spec)

	register := rvAddWaiver(t, r, "ASM-EDGE")

	// A waiver that is only in the candidate is not applied: the gate blocks.
	before := r.check(1)
	if !before.hasDiagnostic("CO-EXC-009") || before.Summary.Waived != 0 {
		t.Errorf("a candidate-only waiver: diagnostics %v, waived %d; want CO-EXC-009 and none waived", before.Diagnostics, before.Summary.Waived)
	}

	r.rvRemoveFile(rvWaiverRegister)
	r.toAnchor("docs: waive the review of ASM-EDGE", map[string]string{rvWaiverRegister: register})

	doc := r.check(0)
	rvWantWaived(t, doc, "ASM-EDGE")

	a := doc.assessment(t, "ASM-EDGE")
	a.want(t, "review-required", "no approval")

	if doc.verdict("REV-APR-001") != "violation" || doc.required(t, "edge").Verdict != "violation" {
		t.Errorf("a waiver changed the verdict: REV-APR-001 = %q, required %+v", doc.verdict("REV-APR-001"), doc.required(t, "edge"))
	}
}

// rvAuthorityDigest gives the digest of the authority configuration that the results of the case record.
func rvAuthorityDigest(t *testing.T, r *repo) string {
	t.Helper()

	authorityDigest, _ := r.check(1).Raw["inputs"].(map[string]any)["authorityDigest"].(string)
	if authorityDigest == "" {
		t.Fatal("results have no inputs.authorityDigest")
	}

	return authorityDigest
}

// rvWaiverUnknown: a waived unknown still exits 4.
func rvWaiverUnknown(t *testing.T, exception string) {
	t.Helper()

	spec := rvTerraform("waiver-unknown")
	spec.authorities = exception
	r := newRvRepo(t, spec)

	// The approval binds the digest of the authority configuration, which has the exception authority here.
	authorityDigest := rvAuthorityDigest(t, r)
	register := rvAddWaiver(t, r, "ASM-TERRAFORM")

	r.rvRemoveFile(rvWaiverRegister)
	r.toAnchor("docs: approve ASM-TERRAFORM v1 and waive its review", map[string]string{
		rvTerraformRecord: rvTerraformApproval(t, authorityDigest), rvWaiverRegister: register,
	})

	// The currency is unknown, and a waived unknown still exits 4.
	doc := r.check(4)
	a := doc.assessment(t, "ASM-TERRAFORM")

	if a.Currency != "unknown" || doc.verdict("REV-APR-001") != "unknown" {
		t.Errorf("currency %q, REV-APR-001 = %q, want unknown for both", a.Currency, doc.verdict("REV-APR-001"))
	}

	if len(doc.Exceptions.ScopedWaivers) != 1 || doc.Exceptions.ScopedWaivers[0].State != "active" {
		t.Errorf("scoped waivers = %+v", doc.Exceptions.ScopedWaivers)
	}
}

// rvWantWaived checks that the scoped waiver is verified and the findings of the assessment have the disposition waived.
func rvWantWaived(t *testing.T, doc *rvDoc, subject string) {
	t.Helper()

	waivers := doc.Exceptions.ScopedWaivers
	if len(waivers) != 1 || waivers[0].State != "active" {
		t.Errorf("scoped waivers = %+v, want one active waiver", waivers)
	}

	if len(waivers) == 0 {
		return
	}

	if verification, _ := waivers[0].Verification.(map[string]any); verification["verified"] != true {
		t.Errorf("verification = %+v, want verified", waivers[0].Verification)
	}

	waived := rvCountWaivedFindings(t, doc)
	if waived == 0 || doc.Summary.Waived != waived {
		t.Errorf("waived findings = %d, summary.waived = %d, want equal and not zero (%s)", waived, doc.Summary.Waived, subject)
	}
}

// rvCountWaivedFindings counts the findings of the approval rule, and wants each of them to be waived.
func rvCountWaivedFindings(t *testing.T, doc *rvDoc) int {
	t.Helper()

	waived := 0

	for _, f := range doc.Findings {
		if f.RuleID != "REV-APR-001" {
			continue
		}

		if f.Disposition != "waived" {
			t.Errorf("finding disposition = %q, want waived: %s", f.Disposition, f.Message)
		}

		waived++
	}

	return waived
}
