package exceptions_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/exceptions"
)

const (
	scopedCommon = "  - id: WV-9\n    rule: REV-APR-001\n    owner: team-platform\n    reason: Review board meets after the release\n    expires: 2026-12-31\n"
	scopedBody   = "    scope: { subject: ASM-EDGE, environment: production }\n    approver: { identity: alice@example.com, authority: exception-board }\n"
	scopeFlow    = "scope: { subject: ASM-EDGE, environment: production }"
	approverFlow = "approver: { identity: alice@example.com, authority: exception-board }"
	registerHead = "waivers:\n"
)

// scoped gives a register with one scoped waiver; the edit changes the text of the entry.
func scoped(edit func(string) string) string {
	return registerHead + edit(scopedCommon+scopedBody)
}

func same(s string) string { return s }

func scopedWaiver(id string, scope exceptions.Scope, expires string) exceptions.Waiver {
	return exceptions.Waiver{
		ID: id, Rule: ruleOne, Owner: "o", Reason: "r", Expires: expires, Scope: &scope,
		Approver: &exceptions.Approver{Identity: "alice@example.com", Authority: "exception-board"},
	}
}

func replacing(old, replacement string) func(string) string {
	return func(s string) string { return strings.Replace(s, old, replacement, 1) }
}

func appending(extra string) func(string) string {
	return func(s string) string { return s + extra }
}

func TestParseScopedWaiver(t *testing.T) {
	t.Parallel()

	t.Run("a scoped waiver parses with its scope, approver, and evidence", func(t *testing.T) {
		t.Parallel()

		text := scoped(appending("    issue: PLAT-1\n    compensatingEvidence:\n      - { path: docs/mitigation.md, sha256: " + fp(2) + " }\n"))

		got, err := exceptions.ParseWaivers(text, "w.yaml")
		if err != nil {
			t.Fatal(err)
		}

		want := scopedWaiver("WV-9", exceptions.Scope{Subject: "ASM-EDGE", Environment: "production"}, "2026-12-31")
		want.Rule, want.Owner, want.Reason, want.Issue = "REV-APR-001", "team-platform", "Review board meets after the release", "PLAT-1"
		want.CompensatingEvidence = []exceptions.Evidence{{Path: "docs/mitigation.md", SHA256: fp(2)}}

		if !isScopedMatch(got, &want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("an obligation alone is a scope", func(t *testing.T) {
		t.Parallel()

		text := scoped(replacing(scopeFlow, "scope: { obligation: 0123456789abcdef }"))
		if _, err := exceptions.ParseWaivers(text, "w.yaml"); err != nil {
			t.Fatal(err)
		}
	})

	// Scenario: Single-fingerprint waiver applies.
	t.Run("a finding waiver needs no approver and ignores unknown keys", func(t *testing.T) {
		t.Parallel()

		text := "waivers:\n  - id: WV-1\n    finding: " + fp(1) + "\n    rule: R\n    owner: o\n    reason: r\n    expires: 2026-12-31\n    ticket: ABC-1\n    note: free\n"

		got, err := exceptions.ParseWaivers(text, "w.yaml")
		if !isUnscopedWaiver(got, err) {
			t.Fatalf("got %+v, %v", got, err)
		}

		r := exceptions.ApplyDispositions([]exceptions.Key{{Fingerprint: fp(1), RuleID: "R"}}, nil, got, at(t, "2026-06-01T00:00:00Z"), nil)
		if want := (exceptions.Assignment{Disposition: exceptions.DispositionWaived, Ref: idOne}); r.Assignments[0] != want {
			t.Errorf("assignment = %+v, want %+v", r.Assignments[0], want)
		}
	})
}

// isScopedMatch says whether got is one scoped waiver that equals want.
func isScopedMatch(got []exceptions.Waiver, want *exceptions.Waiver) bool {
	if len(got) != 1 || !got[0].IsScoped() {
		return false
	}

	return equalWaiver(&got[0], want)
}

// isUnscopedWaiver says whether got is one finding waiver without an approver, and err is nil.
func isUnscopedWaiver(got []exceptions.Waiver, err error) bool {
	if err != nil || len(got) != 1 {
		return false
	}

	return !got[0].IsScoped() && got[0].Approver == nil
}

// isWaiverNotValid says whether de is the CO-EXC-002 error of a waiver that is not valid.
func isWaiverNotValid(de *diag.Error) bool {
	if de.ExitCode != diag.InvalidInput || de.Diagnostic.Code != diag.EXC002 {
		return false
	}

	return strings.HasPrefix(de.Diagnostic.Message, "waiver not valid: ")
}

// Scenario: Entry with both keys.
// Scenario: Entry with neither key.
// Scenario: Unknown key in a scoped waiver.
// Scenario: Wildcard subject.
// Scenario: Empty scope.
// Scenario: Missing approver.
// Scenario: Duration above the maximum.
func TestInvalidScopedWaiversFailWithEXC002(t *testing.T) {
	t.Parallel()

	now := at(t, "2026-06-01T00:00:00Z")

	for name, tc := range map[string]struct {
		text  string
		limit int
	}{
		"both finding and scope":              {text: scoped(appending("    finding: " + fp(1) + "\n"))},
		"neither finding nor scope":           {text: registerHead + scopedCommon},
		"unknown key in a scoped entry":       {text: scoped(appending("    ticket: ABC-1\n"))},
		"unknown key in the scope":            {text: scoped(replacing("environment: production", "region: eu"))},
		"unknown key in the approver":         {text: scoped(replacing("authority: exception-board", "authority: exception-board, mail: x"))},
		"wildcard subject":                    {text: scoped(replacing("subject: ASM-EDGE", "subject: '*'"))},
		"wildcard environment":                {text: scoped(replacing("production", "prod-*"))},
		"wildcard rule":                       {text: scoped(replacing("rule: REV-APR-001", "rule: 'REV-*'"))},
		"empty scope":                         {text: scoped(replacing(scopeFlow, "scope: {}"))},
		"scope not an object":                 {text: scoped(replacing(scopeFlow, "scope: production"))},
		"empty subject":                       {text: scoped(replacing("subject: ASM-EDGE", "subject: ''"))},
		"missing approver":                    {text: registerHead + scopedCommon + "    scope: { subject: ASM-EDGE }\n"},
		"approver without authority":          {text: scoped(replacing(", authority: exception-board", ""))},
		"approver not an object":              {text: scoped(replacing(approverFlow, "approver: alice"))},
		"duration above the maximum":          {text: scoped(replacing("2026-12-31", "2026-12-18")), limit: 90},
		"evidence digest not sha256":          {text: scoped(appending("    compensatingEvidence:\n      - { path: docs/a.md, sha256: abc }\n"))},
		"evidence path outside the workspace": {text: scoped(appending("    compensatingEvidence:\n      - { path: ../a.md, sha256: " + fp(1) + " }\n"))},
		"evidence not an array":               {text: scoped(appending("    compensatingEvidence: docs/a.md\n"))},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []exceptions.ParseOption
			if tc.limit > 0 {
				opts = append(opts, exceptions.WithMaxExpiry(tc.limit, now))
			}

			_, err := exceptions.ParseWaivers(tc.text, "w.yaml", opts...)
			if err == nil {
				t.Fatal("no error")
			}

			de := asDiag(t, err)
			if !isWaiverNotValid(de) {
				t.Errorf("exit %d, diagnostic = %+v", de.ExitCode, de.Diagnostic)
			}
		})
	}
}

func TestMaximumExpiryAppliesToScopedWaiversOnly(t *testing.T) {
	t.Parallel()

	now := at(t, "2026-06-01T10:00:00Z")
	// Expires 2026-12-31: 213 days after 2026-06-01.
	limit := func(days int) exceptions.ParseOption { return exceptions.WithMaxExpiry(days, now) }

	if _, err := exceptions.ParseWaivers(scoped(same), "w.yaml", limit(213)); err != nil {
		t.Errorf("at the maximum: %v", err)
	}

	if _, err := exceptions.ParseWaivers(scoped(same), "w.yaml", limit(212)); err == nil {
		t.Error("one day above the maximum must fail")
	}

	if _, err := exceptions.ParseWaivers(scoped(same), "w.yaml", limit(0)); err != nil {
		t.Errorf("no maximum: %v", err)
	}

	finding := "waivers:\n  - id: WV-1\n    finding: " + fp(1) + "\n    rule: R\n    owner: o\n    reason: r\n    expires: 2999-12-31\n"
	if _, err := exceptions.ParseWaivers(finding, "w.yaml", limit(1)); err != nil {
		t.Errorf("a finding waiver is not limited: %v", err)
	}
}

func TestValidateWaiverAppliesTheRegisterRules(t *testing.T) {
	t.Parallel()

	w := scopedWaiver("WV-1", exceptions.Scope{Subject: "payments"}, "2026-12-31")
	if err := exceptions.ValidateWaiver(&w); err != nil {
		t.Fatal(err)
	}

	w.Scope.Subject = "*"
	if de := asDiag(t, exceptions.ValidateWaiver(&w)); de.Diagnostic.Code != diag.EXC002 {
		t.Errorf("wildcard: %+v", de.Diagnostic)
	}

	w = scopedWaiver("WV-1", exceptions.Scope{Subject: "payments"}, "2026-12-31")
	w.Scope = &exceptions.Scope{}

	if de := asDiag(t, exceptions.ValidateWaiver(&w)); de.Diagnostic.Code != diag.EXC002 {
		t.Errorf("empty scope: %+v", de.Diagnostic)
	}
}

func TestAppendScopedWaiverRoundTrips(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), registerFile)

	w := scopedWaiver("WV-1", exceptions.Scope{Subject: "ASM-EDGE", Environment: "production", Obligation: "0123456789abcdef"}, "2026-12-31")
	w.Issue = "PLAT-1"
	w.CompensatingEvidence = []exceptions.Evidence{{Path: "docs/mitigation.md", SHA256: fp(3)}}

	if err := exceptions.AppendWaiver(path, &w); err != nil {
		t.Fatal(err)
	}

	got, err := exceptions.ReadWaivers(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || !equalWaiver(&got[0], &w) {
		t.Errorf("got %+v, want %+v", got, w)
	}
}

func equalWaiver(a, b *exceptions.Waiver) bool {
	left, errLeft := a.CanonicalJSON()
	right, errRight := b.CanonicalJSON()

	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}

func TestCanonicalDigestIgnoresLayoutAndTracksContent(t *testing.T) {
	t.Parallel()

	flow, err := exceptions.ParseWaivers(scoped(same), "a.yaml")
	if err != nil {
		t.Fatal(err)
	}

	block, err := exceptions.ParseWaivers("# comment\nkind: ExceptionRegister\n"+scoped(replacing(scopeFlow, "scope:\n      environment: production\n      subject: ASM-EDGE")), "b.yaml")
	if err != nil {
		t.Fatal(err)
	}

	left, _ := flow[0].Digest()
	right, _ := block[0].Digest()

	if left != right || !strings.HasPrefix(left, "sha256:") {
		t.Errorf("digests differ for the same entry: %s %s", left, right)
	}

	changed := block[0]
	changed.Scope = &exceptions.Scope{Subject: "ASM-EDGE", Environment: "staging"}

	if other, _ := changed.Digest(); other == left {
		t.Error("a changed scope must change the digest")
	}
}

// Scenario: Waiver for one environment.
func TestScopedDispositions(t *testing.T) {
	t.Parallel()

	now := at(t, "2026-06-01T00:00:00Z")
	staging := stagingWaiver()

	t.Run("a waiver for staging does not cover production", func(t *testing.T) {
		t.Parallel()

		r := exceptions.ApplyDispositions([]exceptions.Key{paymentsKey("staging", 1), paymentsKey("production", 2)}, nil, []exceptions.Waiver{staging}, now, nil)

		want := []exceptions.Assignment{{Disposition: exceptions.DispositionWaived, Ref: "WV-1"}, {Disposition: exceptions.DispositionActive}}
		wantAssignments(t, r.Assignments, want)
	})

	t.Run("a finding of another rule or subject is not covered", func(t *testing.T) {
		t.Parallel()

		other := paymentsKey("staging", 1)
		other.RuleID = ruleTwo

		elsewhere := paymentsKey("staging", 2)
		elsewhere.Subject = "ledger"

		bare := paymentsKey("staging", 3)
		bare.Subject = ""

		r := exceptions.ApplyDispositions([]exceptions.Key{other, elsewhere, bare}, nil, []exceptions.Waiver{staging}, now, []string{ruleOne})
		wantAllActive(t, r.Assignments)
	})

	t.Run("a scope of an obligation covers only that obligation", func(t *testing.T) {
		t.Parallel()

		w := scopedWaiver("WV-1", exceptions.Scope{Obligation: "ob-2"}, "2026-12-31")
		first, second := paymentsKey("staging", 1), paymentsKey("staging", 2)
		second.ObligationID = "ob-2"

		r := exceptions.ApplyDispositions([]exceptions.Key{first, second}, nil, []exceptions.Waiver{w}, now, nil)
		if r.Assignments[0].Disposition != exceptions.DispositionActive || r.Assignments[1].Disposition != exceptions.DispositionWaived {
			t.Errorf("assignments = %+v", r.Assignments)
		}
	})

	// Scenario: Fingerprints differ between runs.
	t.Run("different fingerprints keep the waiver applied and give no orphan warning", func(t *testing.T) {
		t.Parallel()

		for _, n := range []int{1, 7} {
			r := exceptions.ApplyDispositions([]exceptions.Key{paymentsKey("staging", n)}, nil, []exceptions.Waiver{staging}, now, []string{ruleOne})
			if !isWaivedWithoutWarnings(&r) {
				t.Errorf("fingerprint %d: %+v, orphaned %v, diagnostics %v", n, r.Assignments, r.OrphanedWaivers, codes(r.Diagnostics))
			}
		}
	})

	t.Run("finding waiver dispositions are unchanged", func(t *testing.T) {
		t.Parallel()

		withScope := exceptions.Key{Fingerprint: fp(1), RuleID: ruleOne, Subject: "payments", Environment: "staging", ObligationID: "ob-1"}

		r := exceptions.ApplyDispositions([]exceptions.Key{withScope, finding(2)}, nil, []exceptions.Waiver{waiver(1, "2026-12-31", idOne)}, now, nil)

		want := []exceptions.Assignment{{Disposition: exceptions.DispositionWaived, Ref: idOne}, {Disposition: exceptions.DispositionActive}}
		wantAssignments(t, r.Assignments, want)
	})
}

func TestScopedWaiversCompeteByTheSameOrder(t *testing.T) {
	t.Parallel()

	now := at(t, "2026-06-01T00:00:00Z")
	expired := scopedWaiver("WV-1", exceptions.Scope{Subject: "payments"}, "2026-01-01")
	soon := scopedWaiver("WV-3", exceptions.Scope{Subject: "payments"}, "2026-08-01")
	late := scopedWaiver("WV-4", exceptions.Scope{Subject: "payments"}, "2026-12-31")
	tie := scopedWaiver("WV-2", exceptions.Scope{Subject: "payments"}, "2026-12-31")
	byFinding := waiver(1, "2026-10-01", "WV-0")

	pick := func(waivers ...exceptions.Waiver) exceptions.Assignment {
		return exceptions.ApplyDispositions([]exceptions.Key{paymentsKey("staging", 1)}, nil, waivers, now, nil).Assignments[0]
	}

	if got := pick(expired, soon); got != (exceptions.Assignment{Disposition: exceptions.DispositionWaived, Ref: "WV-3"}) {
		t.Errorf("active over expired: %+v", got)
	}

	if got := pick(soon, late); got.Ref != "WV-4" {
		t.Errorf("later expiry: %+v", got)
	}

	if got := pick(late, tie); got.Ref != "WV-2" {
		t.Errorf("smaller id: %+v", got)
	}

	if got := pick(byFinding, soon); got.Ref != "WV-0" {
		t.Errorf("a scoped and a finding waiver compete by the same order: %+v", got)
	}

	if got := pick(expired); got != (exceptions.Assignment{Disposition: exceptions.DispositionExpiredWaiver, Ref: "WV-1"}) {
		t.Errorf("expired: %+v", got)
	}
}

// paymentsKey is a finding of the subject payments in the environment env, with the obligation ob-1.
func paymentsKey(env string, n int) exceptions.Key {
	return exceptions.Key{Fingerprint: fp(n), RuleID: ruleOne, Subject: "payments", Environment: env, ObligationID: "ob-1"}
}

// stagingWaiver is a scoped waiver for the subject payments in the environment staging.
func stagingWaiver() exceptions.Waiver {
	return scopedWaiver("WV-1", exceptions.Scope{Subject: "payments", Environment: "staging"}, "2026-12-31")
}

func wantAssignments(t *testing.T, got, want []exceptions.Assignment) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("assignments = %+v, want %+v", got, want)
	}
}

func wantAllActive(t *testing.T, assignments []exceptions.Assignment) {
	t.Helper()

	for i, a := range assignments {
		if a.Disposition != exceptions.DispositionActive {
			t.Errorf("finding %d: %+v", i, a)
		}
	}
}

// isWaivedWithoutWarnings says whether the first finding is waived and the result has no orphan or diagnostic.
func isWaivedWithoutWarnings(r *exceptions.Result) bool {
	if r.Assignments[0].Disposition != exceptions.DispositionWaived {
		return false
	}

	return len(r.OrphanedWaivers) == 0 && len(r.Diagnostics) == 0
}

// Scenario: Obligation resolved.
func TestUnmatchedScopedWaiverWarnsAndStays(t *testing.T) {
	t.Parallel()

	now := at(t, "2026-06-01T00:00:00Z")
	waivers := []exceptions.Waiver{
		scopedWaiver("WV-1", exceptions.Scope{Subject: "payments", Environment: "staging"}, "2026-12-31"),
		waiver(7, "2026-12-31", "WV-2"),
	}

	r := exceptions.ApplyDispositions([]exceptions.Key{finding(1)}, nil, waivers, now, []string{ruleOne})

	if !slices.Equal(r.OrphanedWaivers, []string{"WV-1", "WV-2"}) {
		t.Fatalf("orphaned = %v", r.OrphanedWaivers)
	}

	if got := codes(r.Diagnostics); !slices.Equal(got, []string{diag.EXC010, diag.EXC003}) {
		t.Fatalf("diagnostics = %v, want CO-EXC-010 for the scoped waiver and CO-EXC-003 for the finding waiver", got)
	}

	if !strings.Contains(r.Diagnostics[0].Message, "WV-1") {
		t.Errorf("the warning names the waiver: %s", r.Diagnostics[0].Message)
	}

	if len(waivers) != 2 {
		t.Error("the register keeps the entry")
	}
}

// Scenario: Evidence reference resolves.
// Scenario: Digest differs.
func TestCompensatingEvidenceResolution(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sum := writeEvidenceFile(t, root)

	w := scopedWaiver("WV-1", exceptions.Scope{Subject: "payments"}, "2026-12-31")
	w.CompensatingEvidence = []exceptions.Evidence{
		{Path: "docs/mitigation.md", SHA256: sum},
		{Path: "docs/mitigation.md", SHA256: fp(5)},
		{Path: "docs/missing.md", SHA256: sum},
	}

	results, warnings := exceptions.ResolveEvidence(root, &w)

	states := make([]string, len(results))
	for i := range results {
		states[i] = results[i].State
	}

	if want := []string{exceptions.EvidenceResolved, exceptions.EvidenceDigestMismatch, exceptions.EvidenceUnresolved}; !slices.Equal(states, want) {
		t.Fatalf("states = %v, want %v", states, want)
	}

	if got := codes(warnings); !slices.Equal(got, []string{diag.EXC011, diag.EXC011}) {
		t.Fatalf("warnings = %v", got)
	}

	for i := range warnings {
		if !isEvidenceWarning(&warnings[i]) {
			t.Errorf("warning = %+v", warnings[i])
		}
	}

	if _, none := exceptions.ResolveEvidence(root, &exceptions.Waiver{ID: idOne}); len(none) != 0 {
		t.Errorf("no evidence: %v", none)
	}
}

// writeEvidenceFile writes docs/mitigation.md under root and gives its digest.
func writeEvidenceFile(t *testing.T, root string) string {
	t.Helper()

	path := filepath.Join(root, "docs", "mitigation.md")

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("compensating control\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sum, err := digest.OfFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return sum
}

// isEvidenceWarning says whether d is a warning that names a file of docs/.
func isEvidenceWarning(d *diag.Diagnostic) bool {
	return d.Severity == diag.SeverityWarning && strings.Contains(d.Message, "docs/")
}

var yamlBlock = regexp.MustCompile("(?s)```yaml\n(.*?)```")

// TestDocumentedRegisterSampleLoads loads the register samples of the exceptions reference, so that the documented
// shapes stay valid.
func TestDocumentedRegisterSampleLoads(t *testing.T) {
	t.Parallel()

	text, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "exceptions.md"))
	if err != nil {
		t.Fatal(err)
	}

	var findingCount, scopedCount int

	for _, sample := range registerSamples(string(text)) {
		findings, scopes := countWaivers(loadSample(t, sample))
		findingCount += findings
		scopedCount += scopes
	}

	if findingCount == 0 || scopedCount == 0 {
		t.Fatalf("the reference must show a finding waiver and a scoped waiver: %d, %d", findingCount, scopedCount)
	}
}

// registerSamples gives the yaml blocks of text that show an exception register.
func registerSamples(text string) []string {
	var samples []string

	for _, m := range yamlBlock.FindAllStringSubmatch(text, -1) {
		if strings.Contains(m[1], "kind: ExceptionRegister") {
			samples = append(samples, m[1])
		}
	}

	return samples
}

// loadSample parses the register sample and stops the test when it does not load.
func loadSample(t *testing.T, sample string) []exceptions.Waiver {
	t.Helper()

	list, err := exceptions.ParseWaivers(sample, "exceptions.md", exceptions.WithMaxExpiry(10000, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("sample does not load: %v\n%s", err, sample)
	}

	return list
}

// countWaivers gives the number of finding waivers and the number of scoped waivers in list.
func countWaivers(list []exceptions.Waiver) (findings, scopes int) {
	for i := range list {
		if list[i].IsScoped() {
			scopes++
		} else {
			findings++
		}
	}

	return findings, scopes
}
