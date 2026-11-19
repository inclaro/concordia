package observation_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

const testTimeout = 10 * time.Second

// namedCheck is one subtest of a test that groups related checks.
type namedCheck struct {
	run  func(t *testing.T)
	name string
}

// runChecks runs each check as a subtest.
func runChecks(t *testing.T, checks []namedCheck) {
	t.Helper()

	for _, c := range checks {
		t.Run(c.name, c.run)
	}
}

// attrRequireNotEqual stops the test when got is other.
func attrRequireNotEqual[T comparable](t *testing.T, what string, got, other T) {
	t.Helper()

	if got == other {
		t.Fatalf("%s = %v, want it to differ from %v", what, got, other)
	}
}

// requireMessageContains stops the test when the message of a diagnostic lacks part.
func requireMessageContains(t *testing.T, de *diag.Error, part string) {
	t.Helper()

	if !strings.Contains(de.Diagnostic.Message, part) {
		t.Fatalf("message = %s, want it to contain %q", de.Diagnostic.Message, part)
	}
}

// script writes an executable shell script and gives its path.
func script(tb testing.TB, body string) string {
	tb.Helper()

	if runtime.GOOS == "windows" {
		tb.Skip("the fake CBM executable is a shell script")
	}

	p := filepath.Join(tb.TempDir(), "fake-cbm.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		tb.Fatal(err)
	}

	return p
}

func TestVersionRules(t *testing.T) {
	runChecks(t, []namedCheck{
		{checkParseVersion, "parses the version text"},
		{checkAssessBuild, "AssessBuild: profile, 0.12.x without profile, 0.13+, others"},
		{checkConnectVersion, "Connect checks the version of the client"},
	})
}

func checkParseVersion(t *testing.T) {
	t.Helper()

	v, ok := observation.ParseVersion("codebase-memory-mcp 0.12.1\n")
	attrRequireEqual(t, "parsed", ok, true)
	attrRequireEqual(t, "version", v, "0.12.1")

	_, ok = observation.ParseVersion("other")
	attrRequireEqual(t, "other parsed", ok, false)
}

func checkAssessBuild(t *testing.T) {
	t.Helper()

	checkAssessBuildWithProfile(t)
	checkAssessBuildWithoutProfile(t)
	checkNoProfileFamilies(t)
	checkAssessBuildDegraded(t)
	checkAssessBuildRejected(t)
}

func checkAssessBuildWithProfile(t *testing.T) {
	t.Helper()

	got, diags, err := observation.AssessBuild("0.12.1")
	if err != nil {
		t.Fatalf("0.12.1: %v", err)
	}

	attrRequireEqual(t, "0.12.1 support", got.Support, observation.VersionSupported)
	attrRequireEqual(t, "0.12.1 diagnostics", len(diags), 0)

	if got.Profile == nil {
		t.Fatalf("0.12.1: no profile in %+v", got)
	}

	attrRequireEqual(t, "0.12.1 profile", got.Profile.ID, "cbm-0.12.1")
}

func checkAssessBuildWithoutProfile(t *testing.T) {
	t.Helper()

	got, diags, err := observation.AssessBuild("0.12.0")
	if err != nil {
		t.Fatalf("0.12.0: %v", err)
	}

	attrRequireEqual(t, "0.12.0 support", got.Support, observation.VersionSupported)
	attrRequireEqual(t, "0.12.0 diagnostics", len(diags), 0)
	attrRequireEqual(t, "0.12.0 has no profile", got.Profile == nil, true)
}

func checkNoProfileFamilies(t *testing.T) {
	t.Helper()

	for name, state := range observation.NoProfileFamilies() {
		isCode := slices.ContainsFunc(observation.CodeKinds(), func(k observation.CanonicalEdgeKind) bool { return string(k) == name })
		attrEqual(t, "no profile: family "+name+" is supported", state.Level == observation.SupportSupported, isCode)
	}
}

func checkAssessBuildDegraded(t *testing.T) {
	t.Helper()

	got, diags, err := observation.AssessBuild("0.13.0")
	if err != nil {
		t.Fatalf("0.13.0: %v", err)
	}

	attrRequireEqual(t, "0.13.0 support", got.Support, observation.VersionDegraded)
	attrRequireEqual(t, "0.13.0 has no profile", got.Profile == nil, true)
	attrRequireEqual(t, "0.13.0 diagnostics", len(diags), 1)
	attrRequireEqual(t, "diagnostic code", diags[0].Code, "CO-OBS-004")
	attrRequireEqual(t, "diagnostic severity", diags[0].Severity, diag.SeverityWarning)
}

func checkAssessBuildRejected(t *testing.T) {
	t.Helper()

	for _, v := range []string{"0.8.9", "0.9.4", "0.11.0", "0.11.2", "1.0.0", "2.1.0", "x"} {
		_, _, err := observation.AssessBuild(v)
		wantDiag(t, err, "CO-OBS-004", diag.Toolchain)
	}
}

func checkConnectVersion(t *testing.T) {
	t.Helper()

	handler := func(string, map[string]any) (any, error) { return map[string]any{}, nil }

	conn, err := observation.Connect(t.Context(), observation.NewFakeClient(handler, "0.13.1"), nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	attrRequireEqual(t, "connection version", conn.Version, "0.13.1")
	attrRequireEqual(t, "connection diagnostics", len(conn.Diagnostics), 1)

	_, err = observation.Connect(t.Context(), observation.NewFakeClient(handler, "1.0.0"), nil)
	wantDiag(t, err, "CO-OBS-004", diag.Toolchain)

	_, err = observation.Connect(t.Context(), observation.NewProcessClient(observation.ProcessOptions{Executable: "/nonexistent/cbm"}), nil)
	wantDiag(t, err, "CO-OBS-003", diag.Toolchain)
}

func TestRecordingAndReplay(t *testing.T) {
	runChecks(t, []namedCheck{
		{checkReplayByKey, "replays the recorded responses by key"},
		{checkReplayMiss, "a miss gives CO-OBS-001 'replay miss <tool>'"},
		{checkCallKeyShape, "the key is sha256(tool NUL canonicalJson(args))"},
		{checkCallKeyIgnoresRepoPath, "the key of index_repository does not depend on repo_path"},
		{checkRecordingRoundTrip, "a recording file round-trips"},
	})
}

func checkReplayByKey(t *testing.T) {
	t.Helper()

	fake := observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
		return map[string]any{"tool": tool, "args": args}, nil
	}, "0.12.1")
	sink := observation.NewMemorySink("deadbeef")
	rec := observation.NewRecordingClient(fake, sink)

	if _, err := rec.Version(t.Context()); err != nil {
		t.Fatal(err)
	}

	live, err := rec.Call(t.Context(), "index_status", map[string]any{"project": "p", "a": 1})
	if err != nil {
		t.Fatal(err)
	}

	replay := observation.NewReplayClient(sink.ToRecording())

	v, _ := replay.Version(t.Context())
	attrRequireEqual(t, "version", v, "0.12.1")

	// key order of args does not matter
	got, err := replay.Call(t.Context(), "index_status", map[string]any{"a": 1, "project": "p"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	attrRequireEqual(t, "replayed body", string(got), string(live))
	attrRequireEqual(t, "headSha", replay.HeadSHA(), "deadbeef")
}

func checkReplayMiss(t *testing.T) {
	t.Helper()

	replay := observation.NewReplayClient(observation.Recording{Version: "0.12.1", Calls: map[string]json.RawMessage{}})

	_, err := replay.Call(t.Context(), "query_graph", map[string]any{"q": 1})

	de := diagOf(t, err, "CO-OBS-001", diag.NoExit)
	requireMessageContains(t, de, "replay miss query_graph")
}

func checkCallKeyShape(t *testing.T) {
	t.Helper()

	key := observation.CallKey("a", map[string]any{"x": 1})

	attrRequireEqual(t, "key length", len(key), 64)
	attrRequireEqual(t, "non-hex characters of the key", strings.Trim(key, "0123456789abcdef"), "")

	attrRequireEqual(t, "key with another key order",
		observation.CallKey("a", map[string]any{"x": 1, "y": 2}), observation.CallKey("a", map[string]any{"y": 2, "x": 1}))
	attrRequireNotEqual(t, "key with another tool", key, observation.CallKey("b", map[string]any{"x": 1}))
}

func checkCallKeyIgnoresRepoPath(t *testing.T) {
	t.Helper()

	a := observation.CallKey("index_repository", map[string]any{"repo_path": "/tmp/a", "mode": "fast"})
	b := observation.CallKey("index_repository", map[string]any{"repo_path": `C:\work\repo`, "mode": "fast"})

	attrRequireEqual(t, "keys of two repo paths", a, b)
	attrRequireEqual(t, "key without repo_path", a, observation.CallKey("index_repository", map[string]any{"mode": "fast"}))

	args := map[string]any{"repo_path": "/tmp/a"}
	observation.CallKey("index_repository", args)

	if _, ok := args["repo_path"]; !ok {
		t.Fatal("CallKey changed the arguments")
	}

	attrRequireNotEqual(t, "key of another tool with repo_path (it must not be dropped)",
		observation.CallKey("query_graph", map[string]any{"repo_path": "x"}), observation.CallKey("query_graph", map[string]any{}))
}

func checkRecordingRoundTrip(t *testing.T) {
	t.Helper()

	sink := observation.NewMemorySink("")
	sink.SetVersion("0.12.1")
	sink.SetHeadSHA("abc")
	sink.Record(observation.CallKey("t", map[string]any{}), json.RawMessage(`{"ok":true}`))

	data, err := canonjson.Marshal(sink.ToRecording())
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "rec.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	replay, err := observation.LoadReplayClient(filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}

	got, err := replay.Call(t.Context(), "t", map[string]any{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	attrRequireEqual(t, "replayed body", strings.Join(strings.Fields(string(got)), ""), `{"ok":true}`)
	attrRequireEqual(t, "headSha", replay.HeadSHA(), "abc")

	if _, err := observation.LoadReplayClient(filepath.ToSlash(filepath.Join(t.TempDir(), "missing.json"))); err == nil {
		t.Fatal("a missing file gave no error")
	}
}

func TestFakeClient(t *testing.T) {
	fake := observation.NewFakeClient(func(tool string, _ map[string]any) (any, error) {
		if tool == "bad" {
			return nil, diag.Fail(diag.OBS001, diag.Detail("bad"))
		}

		return map[string]any{"ok": true}, nil
	}, "")

	v, _ := fake.Version(t.Context())
	attrRequireEqual(t, "version", v, "0.12.1")

	got, err := observation.CallAs[struct {
		OK bool `json:"ok"`
	}](t.Context(), fake, "good", map[string]any{"project": "p"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	attrRequireEqual(t, "call result ok", got.OK, true)

	_, err = fake.Call(t.Context(), "bad", nil)
	wantDiag(t, err, "CO-OBS-001", diag.AdapterFailure)

	calls := fake.Calls()
	attrRequireEqual(t, "call count", len(calls), 2)
	attrRequireEqual(t, "first call tool", calls[0].Tool, "good")
	attrRequireEqual[any](t, "first call project", calls[0].Args["project"], "p")
}
