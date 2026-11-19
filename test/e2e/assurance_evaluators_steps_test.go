package e2e_test

import (
	"slices"
	"strings"
	"testing"
)

// asrEvStrFields says whether every key of the pairs (key, want, key, want, ...) holds the wanted string in the value.
func asrEvStrFields(j jv, pairs ...string) bool {
	for i := 0; i+1 < len(pairs); i += 2 {
		if j.at(pairs[i]).str() != pairs[i+1] {
			return false
		}
	}

	return true
}

// asrEvTextHas says whether the text of every key of the pairs (key, part, ...) contains the part.
func asrEvTextHas(j jv, pairs ...string) bool {
	for i := 0; i+1 < len(pairs); i += 2 {
		if !strings.Contains(j.at(pairs[i]).text(), pairs[i+1]) {
			return false
		}
	}

	return true
}

// asrEvSingleObligation says whether the rule has exactly one obligation with the verdict (when given) and the reason.
func asrEvSingleObligation(rule jv, verdict, reason string) bool {
	got := rule.obligations()
	if len(got) != 1 {
		return false
	}

	if verdict != "" && got[0].at("verdict").str() != verdict {
		return false
	}

	return got[0].at("reason").str() == reason
}

// asrEvRequireNoEvaluator checks that a workspace without an evaluator of the kind leaves the obligation unknown.
func asrEvRequireNoEvaluator(t *testing.T, rules []string, evaluators string) {
	t.Helper()

	r := (&asrWorld{t: t, withShop: true, rules: rules[:1], evaluators: evaluators}).build()
	doc, _ := r.check()

	rule := doc.rule("ACC-001")
	if rule.at("verdict").str() != "unknown" {
		t.Fatalf("ACC-001 = %s", rule.text())
	}

	if !asrEvSingleObligation(rule, "", "no-evaluator-available") {
		t.Errorf("obligations = %s, want unknown with no-evaluator-available", rule.at("obligations").text())
	}

	// The provenance: nothing was evaluated, so no evaluation entry exists, and the explanation does not claim isolation.
	if n := len(doc.at("assurance", "evaluations").list()); n != 0 {
		t.Errorf("evaluations = %d, want none", n)
	}

	out := r.explain("ACC-001").output()
	containsAll(t, out, "Verdict: unknown", "no-evaluator-available")

	if strings.Contains(strings.ToLower(out), "isolated") {
		t.Errorf("the explanation claims isolation:\n%s", out)
	}
}

// asrEvRequireEnforcedVerdicts checks the verdicts of the three rules when the enforcement is declared: db is closed to
// api (denied), open from web to 8080, and closed from web to 22, which the third rule expected open.
func asrEvRequireEnforcedVerdicts(t *testing.T, doc jv) {
	t.Helper()

	for rule, want := range map[string]string{"ACC-001": "pass", "ACC-002": "pass", "ACC-003": "violation"} {
		if got := doc.rule(rule).at("verdict").str(); got != want {
			t.Errorf("%s = %s, want %s: %s", rule, got, want, doc.rule(rule).text())
		}
	}
}

// asrEvRequireViolationExplained checks that the violation names the port, the policies, and the assumption.
func asrEvRequireViolationExplained(t *testing.T, r *asrRepo, doc jv) {
	t.Helper()

	out := r.explain("ACC-003").output()
	containsAll(t, out, "Verdict: violation", "obligation-violation")

	detail := doc.rule("ACC-003").obligations()[0].at("detail").str()
	if !asrEvContainsAll(detail, "closed: tcp/22 from shop/web to shop/db", "network-policy-enforced") {
		t.Errorf("ACC-003 detail = %q", detail)
	}
}

// asrEvContainsAll says whether the text has every part.
func asrEvContainsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}

	return true
}

// asrEvEvaluationComplete says whether the evaluation entry names the evaluator, its version, the input digest, the
// policies that it read, the limits, and the assumption that the workspace declared.
func asrEvEvaluationComplete(entry jv) bool {
	if entry.at("evaluator").str() != "network-policy" || entry.at("evaluatorVersion").str() == "" {
		return false
	}

	if entry.at("inputDigest").str() == "" || len(entry.at("limits").list()) == 0 {
		return false
	}

	return asrEvTextHas(entry,
		"assumptions", "network-policy-enforced",
		"declaredAssumptions", "network-policy-enforced",
		"policies", "NetworkPolicy shop/db-allow-web (ingress)")
}

// asrEvRequireEvaluationProvenance checks the evaluation entries and the rendered evidence in the bundle.
func asrEvRequireEvaluationProvenance(t *testing.T, r *asrRepo, doc jv) {
	t.Helper()

	evaluations := doc.at("assurance", "evaluations").list()
	if len(evaluations) == 0 {
		t.Fatal("no evaluation entry")
	}

	for _, entry := range evaluations {
		if !asrEvEvaluationComplete(entry) {
			t.Errorf("evaluation = %s", entry.text())
		}
	}

	// The rendered evidence that the evaluator read is in the bundle with its digest.
	_, paths := r.bundleFiles()
	if !slices.Contains(paths, "assurance/render/production.json") {
		t.Errorf("the bundle has no rendered evidence: %v", paths)
	}
}

// asrEvRequireAssumptionUndeclared checks that an evaluator without the declared assumption leaves the obligation unknown.
func asrEvRequireAssumptionUndeclared(t *testing.T, rules []string) {
	t.Helper()

	r := (&asrWorld{t: t, withShop: true, rules: rules[:1], evaluators: asrNetworkOnly}).build()
	doc, _ := r.check()

	rule := doc.rule("ACC-001")
	if !asrEvSingleObligation(rule, "unknown", "enforcement-not-declared") {
		t.Errorf("obligations = %s, want unknown with enforcement-not-declared", rule.at("obligations").text())
	}

	for _, entry := range doc.at("assurance", "evaluations").list() {
		if len(entry.at("declaredAssumptions").list()) != 0 {
			t.Errorf("the entry lists an assumption that the workspace does not declare: %s", entry.text())
		}
	}

	containsAll(t, r.explain("ACC-001").output(), "enforcement-not-declared")
}

// asrEvCheckCase is one check-result scenario: the results of the world and what the check makes of them.
type asrEvCheckCase struct {
	name        string
	verdict     string
	reason      string
	control     string
	inadmissibl string
	verify      func(t *testing.T, run *asrEvCheckRun)
	results     []asrCheckResult
}

// asrEvCheckRun is the outcome of the check of a case.
type asrEvCheckRun struct {
	r           *asrRepo
	doc         jv
	c           *asrEvCheckCase
	out         string
	obligations []jv
}

// asrEvCheckCases are the check-result scenarios of A36.
func asrEvCheckCases() []asrEvCheckCase {
	return []asrEvCheckCase{
		{name: "no result", verdict: "unknown", reason: "no-evidence-supplied", control: "unknown", verify: asrEvVerifyNoResult},
		{
			name: "failed run", results: []asrCheckResult{{file: "unit.json", outcome: "failed"}}, verdict: "violation", control: "violation",
			verify: asrEvVerifyFailedRun,
		},
		{
			name: "different revision", results: []asrCheckResult{{file: "unit.json", revision: strings.Repeat("e", 40)}},
			verdict: "unknown", reason: "no-evidence-supplied", control: "unknown", inadmissibl: "different-revision",
			verify: asrEvVerifyDifferentRevision,
		},
		{name: "passed", results: []asrCheckResult{{file: "unit.json"}}, verdict: "pass", control: "pass", verify: asrEvVerifyPassed},
	}
}

// asrEvUnitResult is the check result entry of the unit run in the results of the check.
func asrEvUnitResult(doc jv) jv {
	return doc.at("assurance", "checkResults").where("path", "build/results/unit.json")
}

func asrEvVerifyNoResult(t *testing.T, run *asrEvCheckRun) {
	t.Helper()

	containsAll(t, run.out, "no-evidence-supplied", "candidate, not evidence")

	if len(run.doc.at("assurance", "checkResults").list()) != 0 {
		t.Errorf("check results = %s", run.doc.at("assurance", "checkResults").text())
	}
}

// asrEvVerifyFailedRun checks that the failed result is admissible and its digest is the evidence of the finding.
func asrEvVerifyFailedRun(t *testing.T, run *asrEvCheckRun) {
	t.Helper()

	entry := asrEvUnitResult(run.doc)
	if entry.at("admissible").text() != "true" || !asrEvStrFields(entry, "outcome", "failed", "producer", "ci", "integrity", "producer-authenticated", "revision", run.r.head()) {
		t.Fatalf("check result = %s", entry.text())
	}

	containsAll(t, run.out, "obligation-violation", entry.at("statementDigest").str())

	dir, paths := run.r.bundleFiles()
	requireStatementInBundle(t, dir, paths, entry.at("statementDigest").str(), "sha256:"+strings.Repeat("c", 64))
}

func asrEvVerifyDifferentRevision(t *testing.T, run *asrEvCheckRun) {
	t.Helper()

	entry := asrEvUnitResult(run.doc)
	if entry.at("admissible").text() != "false" || !strings.Contains(entry.at("reasons").text(), run.c.inadmissibl) {
		t.Errorf("check result = %s, want inadmissible with %s", entry.text(), run.c.inadmissibl)
	}

	containsAll(t, run.out, "different-revision")
}

func asrEvVerifyPassed(t *testing.T, run *asrEvCheckRun) {
	t.Helper()

	entry := asrEvUnitResult(run.doc)
	if entry.at("admissible").text() != "true" || !strings.Contains(run.obligations[0].at("detail").str(), entry.at("statementDigest").str()) {
		t.Errorf("the verdict does not cite the result: %s / %s", run.obligations[0].text(), entry.text())
	}
}

// asrEvCandidatesListed says whether the candidates are the two associations of the fixture.
func asrEvCandidatesListed(candidates jv) bool {
	if len(candidates.list()) != 2 {
		return false
	}

	return asrEvContainsAll(candidates.text(), `"kind":"tests"`, `"kind":"tests-file"`)
}

// asrEvRunCheckCase checks the obligation, the candidates, the control, and the explanation of one case, then the
// assertions that are specific to it.
func asrEvRunCheckCase(t *testing.T, c *asrEvCheckCase) {
	t.Helper()

	r := (&asrWorld{t: t, rules: []string{asrCheckRule}, results: c.results}).build()
	doc, _ := r.check()

	rule := doc.rule("CHK-001")
	obligations := rule.obligations()

	if !asrEvSingleObligation(rule, c.verdict, c.reason) {
		t.Fatalf("CHK-001 = %s, want %s %q", rule.text(), c.verdict, c.reason)
	}

	// The TESTS and TESTS_FILE edges of the fixture are candidates in every case, never evidence.
	candidates := doc.at("assurance", "candidates")
	if !asrEvCandidatesListed(candidates) {
		t.Errorf("candidates = %s", candidates.text())
	}

	// The control that lists the check has its verdict from the result.
	control := doc.at("controls").where("controlId", "verification-001")
	if control.at("verdict").str() != c.control || !strings.Contains(control.at("checkIds").text(), "unit-tests") {
		t.Errorf("control = %s, want %s", control.text(), c.control)
	}

	out := r.explain("CHK-001").output()
	containsAll(t, out, "Verdict: "+c.verdict, "Candidate test associations (not evidence")

	c.verify(t, &asrEvCheckRun{r: r, doc: doc, c: c, out: out, obligations: obligations})
}

// asrEvRequireUntrustedProducerUnknown checks that a result that no producer signed decides nothing and is reported.
func asrEvRequireUntrustedProducerUnknown(t *testing.T) {
	t.Helper()

	w := &asrWorld{t: t, rules: []string{asrCheckRule}, results: []asrCheckResult{{file: "unit.json", untrusted: true}}}
	r := w.build()
	doc, _ := r.check()

	if got := doc.rule("CHK-001").at("verdict").str(); got != "unknown" {
		t.Errorf("CHK-001 = %s, want unknown", got)
	}

	if !doc.hasDiagnostic("CO-PRV-009") {
		t.Errorf("no CO-PRV-009 warning for a result that no producer signed: %s", doc.at("diagnostics").text())
	}
}
