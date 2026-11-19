package conformance_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	vendorPackPath = "policies/vendor/acme-1.2.0.policy.yaml"
	acmeBoundary   = "ACME-BND-001"

	vendorPackText = `
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata: {id: acme, version: 1.2.0}
rules:
  - id: ACME-BND-001
    name: Billing does not import fulfillment
    severity: error
    owner: team-acme
    subject: {calmNodes: [billing-service]}
    forbid:
      target: {calmNodes: [fulfillment-service]}
      edges: [IMPORTS]
      traversal: direct
  - id: ACME-DECL-001
    name: Dependencies are declared
    severity: warning
    subject: {nodeTypes: [service]}
    declaredOnly: {edges: [IMPORTS]}
`
)
const archPath = "architecture/a.calm.json"

// archForRule is the test architecture with a control that selects the given rule.
func archForRule(ruleID string) string {
	return archText(map[string]any{"controls": controlOf(map[string]any{"rule-ids": []any{ruleID}})})
}

func packSum(text string) string { return digest.OfBytes([]byte(text)) }

// packWorkspace is a workspace with no local policy files and the given packs section.
func packWorkspace(packsYAML string) string {
	return wsText("policies:\n  files: []\n  packs:\n" + packsYAML)
}

func vendoredPack(extra string) string {
	return "    - use: " + vendorPackPath + "\n      sha256: " + packSum(vendorPackText) + "\n" + extra
}

func packSetup(t *testing.T, packsYAML string, opts *setupOpts) *setup {
	t.Helper()

	if opts == nil {
		opts = &setupOpts{}
	}

	opts.ws = packWorkspace(packsYAML)

	return newSetup(t, map[string]string{vendorPackPath: vendorPackText, archPath: archForRule(acmeBoundary)}, opts)
}

func ruleOf(t *testing.T, run *conformance.Run, id string) conformance.RuleResult {
	t.Helper()

	i := slices.IndexFunc(run.Rules, func(r conformance.RuleResult) bool { return r.RuleID == id })
	if i < 0 {
		t.Fatalf("rule %s is not in the run", id)
	}

	return run.Rules[i]
}

func TestBuiltinPackRulesRunAndAreLocked(t *testing.T) {
	t.Parallel()

	s := newSetup(t, map[string]string{archPath: archForRule("CORE-CYC-001")}, &setupOpts{ws: packWorkspace("    - use: builtin:core\n")})
	run := s.mustCheck(t, nil)

	core := ruleOf(t, run, "CORE-CYC-001")
	if core.Policy.ID != "core" || core.Policy.Version != "1.0.0" {
		t.Errorf("rule pack = %+v", core.Policy)
	}

	text, _ := packs.Source("core")
	if want := []conformance.InputRef{{Path: "builtin:core", Digest: packSum(string(text))}}; !slices.Equal(run.Inputs.Policies, want) {
		t.Errorf("inputs = %+v, want %+v", run.Inputs.Policies, want)
	}

	lock := testutil.ReadFile(t, workspace.LockPathFor(s.dir))
	for _, want := range []string{"packs:", "id: core", "version: 1.0.0", "source: builtin:core", "sha256: " + packSum(string(text))} {
		if !strings.Contains(lock, want) {
			t.Errorf("lock has no %q:\n%s", want, lock)
		}
	}
}

func TestRunWithoutPacksLeavesNoPacksInTheLock(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	s.mustCheck(t, nil)

	// This run has no resource and no pack to lock: it writes no lockfile.
	if _, found, err := workspace.ReadLock(workspace.LockPathFor(s.dir)); err != nil || found {
		t.Errorf("found %v err %v", found, err)
	}
}

func TestVendoredPackWithMatchingDigestProducesFindings(t *testing.T) {
	t.Parallel()

	s := newSetup(t, map[string]string{vendorPackPath: vendorPackText}, &setupOpts{
		// The default glob matches the vendor directory: the pinned file is still read only once.
		ws: wsText("policies:\n  packs:\n" + vendoredPack("")),
	})
	run := s.mustCheck(t, nil)

	boundary := ruleOf(t, run, acmeBoundary)
	requirePackRule(t, &boundary, packRuleShape{conformance.VerdictViolation, policy.SeverityError, policy.PackRef{ID: "acme", Version: "1.2.0"}, 1})

	for _, d := range run.Diagnostics {
		if d.Code == diag.POL003 {
			t.Errorf("duplicate diagnostic: %+v", d)
		}
	}

	want := []conformance.InputRef{
		{Path: "policies/demo.policy.yaml", Digest: packSum(policyText)},
		{Path: vendorPackPath, Digest: packSum(vendorPackText)},
	}
	if !slices.Equal(run.Inputs.Policies, want) {
		t.Errorf("inputs = %+v, want %+v", run.Inputs.Policies, want)
	}
}

func TestPackDigestMismatchStopsTheRunWithPOL010(t *testing.T) {
	t.Parallel()

	s := newSetup(t, map[string]string{vendorPackPath: vendorPackText + "# changed\n", archPath: archForRule(acmeBoundary)}, &setupOpts{ws: packWorkspace(vendoredPack(""))})

	if e := s.failureOf(t, nil); e.Diagnostic.Code != diag.POL010 || e.ExitCode != diag.InvalidInput {
		t.Fatalf("%s %d", e.Diagnostic.Code, e.ExitCode)
	}
}

func TestPackWithoutPinOrFileStopsTheRun(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ packs, code string }{
		"no sha256":       {"    - use: " + vendorPackPath + "\n", diag.POL013},
		"missing file":    {"    - use: policies/vendor/none.policy.yaml\n      sha256: " + packSum("x") + "\n", diag.POL011},
		"unknown builtin": {"    - use: builtin:nope\n", diag.POL012},
		"remote":          {"    - use: https://example.org/p.policy.yaml\n", diag.POL014},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if e := packSetup(t, tc.packs, nil).failureOf(t, nil); e.Diagnostic.Code != tc.code || e.ExitCode != diag.InvalidInput {
				t.Fatalf("%s %d", e.Diagnostic.Code, e.ExitCode)
			}
		})
	}
}

func TestOverridesApplyToTheRun(t *testing.T) {
	t.Parallel()

	overrides := `      rules:
        ACME-BND-001: { severity: warning, owner: team-platform, evidence: { minimumConfidence: resolved } }
        ACME-DECL-001: { enabled: false }
`
	run := packSetup(t, vendoredPack(overrides), nil).mustCheck(t, nil)

	boundary := ruleOf(t, run, acmeBoundary)
	requireOverriddenBoundary(t, &boundary)

	if i := slices.IndexFunc(run.Rules, func(r conformance.RuleResult) bool { return r.RuleID == "ACME-DECL-001" }); i >= 0 {
		t.Errorf("the disabled rule is in the run: %+v", run.Rules[i])
	}

	for i := range run.Findings {
		if run.Findings[i].RuleID == acmeBoundary && run.Findings[i].Severity != policy.SeverityWarning {
			t.Errorf("finding severity %s", run.Findings[i].Severity)
		}
	}
}

func TestOverrideForAnUnknownRuleStopsTheRun(t *testing.T) {
	t.Parallel()

	e := packSetup(t, vendoredPack("      rules:\n        ACME-TYPO-001: { enabled: false }\n"), nil).failureOf(t, nil)
	if e.Diagnostic.Code != diag.POL015 || e.ExitCode != diag.InvalidInput {
		t.Fatalf("%s %d", e.Diagnostic.Code, e.ExitCode)
	}
}

func TestSameRuleIDInAPackAndAFileIsADuplicate(t *testing.T) {
	t.Parallel()

	// policies/demo.policy.yaml declares CO-BND-001; the pack declares the same ID.
	dup := strings.ReplaceAll(vendorPackText, acmeBoundary, "CO-BND-001")
	ws := wsText("policies:\n  files: [policies/demo.policy.yaml]\n  packs:\n    - use: " + vendorPackPath + "\n      sha256: " + packSum(dup) + "\n")
	s := newSetup(t, map[string]string{vendorPackPath: dup}, &setupOpts{ws: ws})

	if e := s.failureOf(t, nil); e.Diagnostic.Code != diag.POL003 {
		t.Fatalf("%s", e.Diagnostic.Code)
	}
}

func TestPolicyOptionReplacesFilesAndPacks(t *testing.T) {
	t.Parallel()

	// The pack pin is wrong: a run that used the packs would fail.
	bad := "    - use: " + vendorPackPath + "\n      sha256: " + packSum("not the file") + "\n    - use: builtin:core\n"
	s := newSetup(t, map[string]string{vendorPackPath: vendorPackText}, &setupOpts{ws: packWorkspace(bad)})

	run := s.mustCheck(t, func(o *conformance.CheckOptions) { o.Policies = []string{"policies/demo.policy.yaml"} })

	if len(run.Inputs.Policies) != 1 || run.Inputs.Policies[0].Path != "policies/demo.policy.yaml" {
		t.Errorf("inputs = %+v", run.Inputs.Policies)
	}

	if i := slices.IndexFunc(run.Rules, func(r conformance.RuleResult) bool { return strings.HasPrefix(r.RuleID, "CORE-") }); i >= 0 {
		t.Errorf("a pack rule ran with --policy: %s", run.Rules[i].RuleID)
	}

	if lock, found, _ := workspace.ReadLock(workspace.LockPathFor(s.dir)); found && len(lock.Packs) != 0 {
		t.Errorf("--policy locked packs: %+v", lock.Packs)
	}
}

func TestPackDigestDriftAgainstTheLockFailsLikeAResource(t *testing.T) {
	t.Parallel()

	s := packSetup(t, vendoredPack(""), nil)
	s.mustCheck(t, nil)

	lock := readVendoredPackLock(t, s)

	// A new version of the file, with a new pin, but the lock still has the old digest.
	changed := strings.Replace(vendorPackText, "1.2.0", "1.3.0", 1)
	testutil.WriteTree(t, s.dir, map[string]string{
		vendorPackPath:   changed,
		"concordia.yaml": packWorkspace("    - use: " + vendorPackPath + "\n      sha256: " + packSum(changed) + "\n"),
	})

	requirePackDriftFailure(t, s.failureOf(t, nil))

	// Removing the entry from the lock accepts the new digest.
	lock.Packs = nil
	if err := workspace.WriteLock(workspace.LockPathFor(s.dir), lock); err != nil {
		t.Fatal(err)
	}

	s.mustCheck(t, nil)

	requireLockedVendorPack(t, s, packSum(changed), "1.3.0")
}

func TestMakesNoNetworkRequestWithPacks(t *testing.T) {
	prior := http.DefaultTransport
	http.DefaultTransport = failingTransport{t}

	t.Cleanup(func() { http.DefaultTransport = prior })

	run := packSetup(t, "    - use: builtin:core\n"+vendoredPack(""), &setupOpts{offline: true}).mustCheck(t, nil)
	if run.Summary.ExitCode != 1 {
		t.Errorf("exit code %d", run.Summary.ExitCode)
	}
}

func TestControlMayNameADisabledRule(t *testing.T) {
	t.Parallel()

	arch := archText(map[string]any{"controls": controlOf(map[string]any{"rule-ids": []any{acmeBoundary, "ACME-DECL-001"}})})
	s := newSetup(t, map[string]string{vendorPackPath: vendorPackText, archPath: arch}, &setupOpts{
		ws: packWorkspace(vendoredPack("      rules:\n        ACME-DECL-001: { enabled: false }\n")),
	})

	run := s.mustCheck(t, nil)
	if i := slices.IndexFunc(run.Rules, func(r conformance.RuleResult) bool { return r.RuleID == "ACME-DECL-001" }); i >= 0 {
		t.Errorf("the disabled rule ran: %+v", run.Rules[i])
	}

	// The same control gives an error for a rule that no pack declares.
	typo := archText(map[string]any{"controls": controlOf(map[string]any{"rule-ids": []any{acmeBoundary, "ACME-NOPE-001"}})})
	bad := newSetup(t, map[string]string{vendorPackPath: vendorPackText, archPath: typo}, &setupOpts{ws: packWorkspace(vendoredPack(""))})

	if e := bad.failureOf(t, nil); e.Diagnostic.Code != diag.POL005 {
		t.Errorf("code %s", e.Diagnostic.Code)
	}
}

// Scenario: Pack not enabled. No `ASR-` rule runs unless the workspace lists the `assurance` pack.
func TestAssurancePackRunsOnlyWhenTheWorkspaceListsIt(t *testing.T) {
	t.Parallel()

	without := newSetup(t, map[string]string{archPath: archForRule("CORE-CYC-001")}, &setupOpts{ws: packWorkspace("    - use: builtin:core\n")}).mustCheck(t, nil)
	for i := range without.Rules {
		if strings.HasPrefix(without.Rules[i].RuleID, "ASR-") {
			t.Errorf("rule %s ran without the pack", without.Rules[i].RuleID)
		}
	}

	with := newSetup(t, map[string]string{archPath: archForRule("ASR-REN-001")}, &setupOpts{ws: packWorkspace("    - use: builtin:assurance\n")}).mustCheck(t, nil)

	// The workspace has no `assurance` key, so no contract is loaded: each rule is unknown, and the reason says so.
	for _, id := range []string{"ASR-REN-001", "ASR-PRV-001", "ASR-PRV-002"} {
		got := ruleOf(t, with, id)
		requireClaimNotEnabled(t, id, &got)
	}
}

// packRuleShape is the verdict, the severity, the pack, and the number of findings of a rule.
type packRuleShape struct {
	verdict  conformance.RuleVerdict
	severity policy.RuleSeverity
	pack     policy.PackRef
	findings int
}

// requirePackRule reports a rule that differs from the shape.
func requirePackRule(t *testing.T, rule *conformance.RuleResult, want packRuleShape) {
	t.Helper()

	if got := (packRuleShape{rule.Verdict, rule.Severity, rule.Policy, rule.FindingCount}); got != want {
		t.Errorf("rule = %+v", *rule)
	}
}

// requireOverriddenBoundary checks the severity, the owner, and the confidence that the overrides of the pack set.
func requireOverriddenBoundary(t *testing.T, rule *conformance.RuleResult) {
	t.Helper()

	type overridden struct {
		severity   policy.RuleSeverity
		owner      string
		confidence observation.Confidence
	}

	got := overridden{rule.Severity, rule.Plan.Owner, rule.Plan.EvidenceRequirements.MinimumConfidence}
	if got != (overridden{policy.SeverityWarning, "team-platform", "resolved"}) {
		t.Errorf("rule = severity %s owner %q evidence %+v", rule.Severity, rule.Plan.Owner, rule.Plan.EvidenceRequirements)
	}
}

// readVendoredPackLock reads the lock of the setup and stops the test unless it locks the vendored pack alone.
func readVendoredPackLock(t *testing.T, s *setup) *workspace.Lockfile {
	t.Helper()

	lock, found, err := workspace.ReadLock(workspace.LockPathFor(s.dir))
	if err != nil || !found {
		t.Fatalf("lock %+v found %v err %v", lock, found, err)
	}

	if len(lock.Packs) != 1 {
		t.Fatalf("lock %+v found %v err %v", lock, found, err)
	}

	if p := lock.Packs[0]; p.Source != vendorPackPath || p.SHA256 != packSum(vendorPackText) {
		t.Fatalf("lock %+v found %v err %v", lock, found, err)
	}

	return lock
}

// requireLockedVendorPack stops the test unless the lock of the setup has the vendored pack alone with the digest and
// the version.
func requireLockedVendorPack(t *testing.T, s *setup, sha, version string) {
	t.Helper()

	again, _, _ := workspace.ReadLock(workspace.LockPathFor(s.dir))
	if len(again.Packs) != 1 {
		t.Fatalf("lock = %+v", again.Packs)
	}

	if p := again.Packs[0]; p.SHA256 != sha || p.Version != version {
		t.Errorf("lock = %+v", again.Packs)
	}
}

// requirePackDriftFailure stops the test unless the failure is the digest drift of a pack against the lock.
func requirePackDriftFailure(t *testing.T, e *diag.Error) {
	t.Helper()

	if e.Diagnostic.Code != diag.INT012 || e.ExitCode != diag.Network {
		t.Fatalf("%s %d %s", e.Diagnostic.Code, e.ExitCode, e.Diagnostic.Message)
	}

	if !strings.Contains(e.Diagnostic.Message, vendorPackPath) {
		t.Fatalf("%s %d %s", e.Diagnostic.Code, e.ExitCode, e.Diagnostic.Message)
	}
}

// requireClaimNotEnabled checks the rule of the assurance pack in a workspace with no assurance contract.
func requireClaimNotEnabled(t *testing.T, id string, got *conformance.RuleResult) {
	t.Helper()

	if len(got.Reasons) != 1 {
		t.Errorf("%s = %+v", id, *got)

		return
	}

	type shape struct {
		verdict  conformance.RuleVerdict
		code     string
		severity policy.RuleSeverity
		pack     string
	}

	if have := (shape{got.Verdict, got.Reasons[0].Code, got.Severity, got.Policy.ID}); have != (shape{conformance.VerdictUnknown, "claim-not-enabled", policy.SeverityWarning, "assurance"}) {
		t.Errorf("%s = %+v", id, *got)
	}
}
