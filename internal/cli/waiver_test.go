package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/testutil"
)

const waiverWorkspaceYAML = "metadata: {name: t}\nobservation:\n  projects:\n    - {name: t, root: .}\n"

func waiverFP() string { return fmt.Sprintf("sha256:%064d", 1) }

func waiverResults() string {
	return fmt.Sprintf(`{"run":{"sourceRevision":"abc","graphGeneration":"g"},"findings":[{"fingerprint":%q,"ruleId":"R-1"}]}`, waiverFP())
}

func TestWaiverAddAssignsIDsAndValidateFlagsExpiry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"concordia.yaml": waiverWorkspaceYAML, "build/concordia/results.json": waiverResults()})

	cfg := &runConfig{Dir: dir, Env: map[string]string{"CONCORDIA_NOW": "2026-06-01T00:00:00Z"}}
	add := func(expires string) result {
		return runWith(t, cfg, "waiver", "add", "--finding", waiverFP(), "--owner", "o", "--reason", "r", "--expires", expires)
	}

	if res := add("2026-12-31"); res.Code != 0 {
		t.Fatalf("first add: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if res := add("2026-05-01"); res.Code != 2 {
		t.Errorf("past date: exit code = %d, want 2", res.Code)
	}

	if res := add("2026-12-31"); res.Code != 0 {
		t.Fatalf("second add: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	text := readFile(t, dir, "exceptions/waivers.yaml")
	for _, want := range []string{"WV-1", "WV-2"} {
		if !strings.Contains(text, want) {
			t.Errorf("waiver file lacks %q:\n%s", want, text)
		}
	}

	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"validate", []string{"waiver", "validate"}, 0},
		{"expired at", []string{"waiver", "validate", "--at", "2027-01-01"}, 2},
		{"expiring within", []string{"waiver", "validate", "--fail-expiring-within", "400"}, 2},
	} {
		if res := runWith(t, cfg, tc.args...); res.Code != tc.code {
			t.Errorf("%s: exit code = %d, want %d, stderr = %q", tc.name, res.Code, tc.code, res.Stderr)
		}
	}
}

func TestWaiverAddNeedsARuleWhenTheFingerprintIsNotInTheResults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"concordia.yaml": waiverWorkspaceYAML})

	cfg := &runConfig{Dir: dir, Env: map[string]string{"CONCORDIA_NOW": "2026-06-01T00:00:00Z"}}

	res := runWith(t, cfg, "waiver", "add", "--finding", waiverFP(), "--owner", "o", "--reason", "r", "--expires", "2026-12-31")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-EXC-002") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	res = runWith(t, cfg, "waiver", "add", "--finding", waiverFP(), "--rule", "R-1", "--issue", "X-1", "--owner", "o", "--reason", "r", "--expires", "2026-12-31")
	if res.Code != 0 {
		t.Fatalf("with --rule: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if text := readFile(t, dir, "exceptions/waivers.yaml"); !strings.Contains(text, "X-1") {
		t.Errorf("waiver file lacks the issue:\n%s", text)
	}
}

func TestWaiverAddRejectsBadInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"concordia.yaml": waiverWorkspaceYAML})

	cfg := &runConfig{Dir: dir, Env: map[string]string{"CONCORDIA_NOW": "2026-06-01T00:00:00Z"}}

	if res := runWith(t, cfg, "waiver", "add", "--finding", "nope", "--owner", "o", "--reason", "r", "--expires", "2026-12-31"); res.Code != 2 {
		t.Errorf("bad finding: exit code = %d", res.Code)
	}

	if res := runWith(t, cfg, "waiver", "add", "--finding", waiverFP(), "--owner", "o", "--reason", "r", "--expires", "soon"); res.Code != 2 {
		t.Errorf("bad date: exit code = %d", res.Code)
	}

	res := runWith(t, cfg, "waiver", "add", "--finding", waiverFP())
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
		t.Errorf("missing flags: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// scopedAddArgs are the arguments of a valid `waiver add` of a scoped waiver; the extra arguments follow.
func scopedAddArgs(extra ...string) []string {
	return append([]string{
		"waiver", "add", "--rule", "REV-APR-001", "--subject", "ASM-EDGE", "--environment", "production",
		"--approver", "alice@example.com@exception-board", "--owner", "team-platform", "--reason", "Review board meets after the release",
		"--expires", "2026-12-31",
	}, extra...)
}

func scopedConfig(t *testing.T, workspaceYAML string) (cfg *runConfig, dir string) {
	t.Helper()

	dir = t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"concordia.yaml": workspaceYAML, "build/concordia/results.json": waiverResults(), "docs/mitigation.md": "controls\n",
	})

	return &runConfig{Dir: dir, Env: map[string]string{"CONCORDIA_NOW": "2026-06-01T00:00:00Z"}}, dir
}

// waiverRunWant is the exit code and the stderr text that a waiver command must give.
type waiverRunWant struct {
	label  string
	stderr []string
	code   int
}

// run runs the command and stops the test when its exit code or stderr differs.
func (w waiverRunWant) run(t *testing.T, cfg *runConfig, args ...string) {
	t.Helper()

	res := runWith(t, cfg, args...)
	if res.Code != w.code {
		t.Fatalf("%s: exit code = %d, stderr = %q", w.label, res.Code, res.Stderr)
	}

	for _, want := range w.stderr {
		if !strings.Contains(res.Stderr, want) {
			t.Fatalf("%s: exit code = %d, stderr = %q", w.label, res.Code, res.Stderr)
		}
	}
}

// Scenario: Add a scoped waiver.
func TestWaiverAddScopedThenValidatePasses(t *testing.T) {
	t.Parallel()

	cfg, dir := scopedConfig(t, waiverWorkspaceYAML)

	waiverRunWant{label: "add"}.run(t, cfg, scopedAddArgs("--evidence", "docs/mitigation.md", "--issue", "PLAT-1")...)

	text := readFile(t, dir, "exceptions/waivers.yaml")
	for _, want := range []string{
		"id: WV-1", "rule: REV-APR-001", "subject: ASM-EDGE", "environment: production", "identity: alice@example.com", "authority: exception-board",
		"path: docs/mitigation.md", "sha256: sha256:", "expires: 2026-12-31", "issue: PLAT-1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("waiver file lacks %q:\n%s", want, text)
		}
	}

	if strings.Contains(text, "finding:") {
		t.Errorf("a scoped waiver has no fingerprint:\n%s", text)
	}

	// The next free id is used, also after a finding waiver.
	waiverRunWant{label: "second add", stderr: []string{"WV-2"}}.run(t, cfg, scopedAddArgs("--obligation", "0123456789abcdef")...)
	waiverRunWant{label: "finding add", stderr: []string{"WV-3"}}.run(t, cfg,
		"waiver", "add", "--finding", waiverFP(), "--rule", "R-1", "--owner", "o", "--reason", "r", "--expires", "2026-12-31")

	// The anchor is not configured, so validate warns that the approvers are not verified, and still passes.
	waiverRunWant{label: "validate", stderr: []string{"CO-EXC-009", "3 waiver(s) checked"}}.run(t, cfg, "waiver", "validate")
}

func TestWaiverAddScopedRejectsInvalidInputWithEXC002(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct { //nolint:govet // a test table.
		args      []string
		workspace string
	}{
		"finding with a scope flag":      {args: append(scopedAddArgs(), "--finding", waiverFP())},
		"no finding and no scope":        {args: []string{"waiver", "add", "--rule", "R-1", "--owner", "o", "--reason", "r", "--expires", "2026-12-31"}},
		"wildcard subject":               {args: replaceArg(scopedAddArgs(), "ASM-EDGE", "*")},
		"wildcard environment":           {args: replaceArg(scopedAddArgs(), "production", "prod-*")},
		"missing approver":               {args: removeArgs(scopedAddArgs(), "--approver", "alice@example.com@exception-board")},
		"approver without authority":     {args: replaceArg(scopedAddArgs(), "alice@example.com@exception-board", "alice")},
		"approver with empty authority":  {args: replaceArg(scopedAddArgs(), "alice@example.com@exception-board", "alice@")},
		"missing rule":                   {args: removeArgs(scopedAddArgs(), "--rule", "REV-APR-001")},
		"approver without a scope":       {args: []string{"waiver", "add", "--finding", waiverFP(), "--rule", "R-1", "--approver", "a@b", "--owner", "o", "--reason", "r", "--expires", "2026-12-31"}},
		"evidence that does not exist":   {args: scopedAddArgs("--evidence", "docs/missing.md")},
		"evidence outside the workspace": {args: scopedAddArgs("--evidence", "../outside.md")},
		"expiry in the past":             {args: replaceArg(scopedAddArgs(), "2026-12-31", "2026-05-01")},
		"duration above the maximum": {
			args:      scopedAddArgs(),
			workspace: waiverWorkspaceYAML + "exceptions: { maxExpiryDays: 30 }\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ws := tc.workspace
			if ws == "" {
				ws = waiverWorkspaceYAML
			}

			cfg, dir := scopedConfig(t, ws)

			res := runWith(t, cfg, tc.args...)
			if res.Code != 2 || !strings.Contains(res.Stderr, "CO-EXC-002") {
				t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
			}

			if _, err := os.Stat(filepath.Join(dir, "exceptions", "waivers.yaml")); err == nil {
				t.Errorf("a rejected waiver must not create the register")
			}
		})
	}
}

func replaceArg(args []string, old, replacement string) []string {
	out := slices.Clone(args)
	if i := slices.Index(out, old); i >= 0 {
		out[i] = replacement
	}

	return out
}

func removeArgs(args []string, flag, value string) []string {
	out := slices.Clone(args)
	if i := slices.Index(out, flag); i >= 0 && out[i+1] == value {
		out = slices.Delete(out, i, i+2)
	}

	return out
}

func TestWaiverValidateChecksScopedEntries(t *testing.T) {
	t.Parallel()

	const head = "apiVersion: concordia.io/v1alpha1\nkind: ExceptionRegister\nwaivers:\n"

	entry := func(scope, approver string) string {
		return head + "  - {id: WV-1, rule: R-1, scope: " + scope + ", owner: o, reason: r, expires: 2026-12-31" + approver + "}\n"
	}

	const approver = ", approver: {identity: alice@example.com, authority: exception-board}"

	for name, tc := range map[string]struct { //nolint:govet // a test table.
		register  string
		workspace string
		code      int
		want      string
	}{
		"valid":              {register: entry("{subject: payments}", approver), code: 0, want: "1 waiver(s) checked"},
		"empty scope":        {register: entry("{}", approver), code: 2, want: "CO-EXC-002"},
		"wildcard":           {register: entry("{subject: '*'}", approver), code: 2, want: "CO-EXC-002"},
		"missing approver":   {register: entry("{subject: payments}", ""), code: 2, want: "CO-EXC-002"},
		"unknown scope key":  {register: entry("{team: payments}", approver), code: 2, want: "CO-EXC-002"},
		"above the maximum":  {register: entry("{subject: payments}", approver), workspace: waiverWorkspaceYAML + "exceptions: { maxExpiryDays: 30 }\n", code: 2, want: "maxExpiryDays"},
		"within the maximum": {register: entry("{subject: payments}", approver), workspace: waiverWorkspaceYAML + "exceptions: { maxExpiryDays: 365 }\n", code: 0, want: "1 waiver(s) checked"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ws := tc.workspace
			if ws == "" {
				ws = waiverWorkspaceYAML
			}

			cfg, dir := scopedConfig(t, ws)
			testutil.WriteTree(t, dir, map[string]string{"exceptions/waivers.yaml": tc.register})

			res := runWith(t, cfg, "waiver", "validate")
			if res.Code != tc.code || !strings.Contains(res.Stderr, tc.want) {
				t.Errorf("exit code = %d, stderr = %q, want %d with %q", res.Code, res.Stderr, tc.code, tc.want)
			}
		})
	}
}

// Scenario: Obligation resolved.
func TestWaiverValidateWarnsForAScopedWaiverWithoutAFinding(t *testing.T) {
	t.Parallel()

	const register = "waivers:\n" +
		"  - {id: WV-1, rule: R-1, scope: {subject: payments}, owner: o, reason: r, expires: 2026-12-31, approver: {identity: a@b.c, authority: board}}\n" +
		"  - {id: WV-2, rule: R-1, scope: {obligation: ob-1}, owner: o, reason: r, expires: 2026-12-31, approver: {identity: a@b.c, authority: board}}\n"

	cfg, dir := scopedConfig(t, waiverWorkspaceYAML)
	testutil.WriteTree(t, dir, map[string]string{
		"exceptions/waivers.yaml":      register,
		"build/concordia/results.json": `{"findings":[{"fingerprint":"` + waiverFP() + `","ruleId":"R-1","subject":"billing","environment":"production","obligationId":"ob-1"}]}`,
	})

	res := runWith(t, cfg, "waiver", "validate")

	hasWarning := strings.Count(res.Stderr, "CO-EXC-010") == 1 && strings.Contains(res.Stderr, "WV-1")
	if res.Code != 0 || !hasWarning || strings.Contains(res.Stderr, "CO-EXC-003") {
		t.Errorf("exit code = %d, stderr = %q: WV-1 covers no finding, WV-2 covers one", res.Code, res.Stderr)
	}
}
