package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/testutil"
)

func workspaceWithArtifacts(t *testing.T) string {
	t.Helper()

	dir := initialized(t)
	testutil.WriteTree(t, dir, map[string]string{
		".concordia/runs/x":            "x",
		"build/concordia/results.json": "{}",
		"build/other/keep.txt":         "keep",
	})

	return dir
}

func TestCleanWithoutYesAndWithoutTTYGivesWRK011(t *testing.T) {
	t.Parallel()

	dir := workspaceWithArtifacts(t)

	res := runIn(t, dir, "clean", "--cache")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-011") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if !exists(dir, ".concordia") {
		t.Error(".concordia was removed")
	}
}

func TestCleanYesRemovesTheSelectedDirectoriesOnly(t *testing.T) {
	t.Parallel()

	dir := workspaceWithArtifacts(t)

	res := runIn(t, dir, "clean", "--cache", "--yes")
	if res.Code != 0 || res.Stdout != "removed: .concordia/\n" {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	if exists(dir, ".concordia") || !exists(dir, "build/concordia") {
		t.Error("only .concordia must be removed")
	}

	if res = runIn(t, dir, "clean", "--generated", "--yes"); res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if exists(dir, "build/concordia") || !exists(dir, "build/other/keep.txt") {
		t.Error("only build/concordia must be removed")
	}
}

// abortedClean reports whether `clean` exited 0 after it asked, and aborted.
func abortedClean(res *result) bool {
	return res.Code == 0 && containsEvery(res.Stderr, "Remove .concordia/? [y/N] ", "aborted")
}

func TestCleanAsksOnATerminalAndOnlyYContinues(t *testing.T) {
	t.Parallel()

	dir := workspaceWithArtifacts(t)

	for _, answer := range []string{"n\n", "\n", "yes\n", ""} {
		res := runWith(t, &runConfig{Dir: dir, StdinTTY: true, Stdin: answer}, "clean", "--cache")
		if !abortedClean(&res) {
			t.Fatalf("answer %q: exit code = %d, stderr = %q", answer, res.Code, res.Stderr)
		}

		if !exists(dir, ".concordia") {
			t.Fatalf("answer %q removed the directory", answer)
		}
	}

	res := runWith(t, &runConfig{Dir: dir, StdinTTY: true, Stdin: "y\n"}, "clean", "--cache")
	if res.Code != 0 || exists(dir, ".concordia") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestCleanProjectAndAllCallDeleteProjectWithTheWorkspaceProject(t *testing.T) {
	t.Parallel()

	dir := workspaceWithArtifacts(t)
	calls := &recordingClient{}

	res := runWith(t, &runConfig{Dir: dir, Deps: clientDeps(calls, nil)}, "clean", "--all", "--yes")
	if res.Code != 0 || !calls.singleToolCallForProject("delete_project", "doctor-repo") {
		t.Fatalf("exit code = %d, calls = %+v, stderr = %q", res.Code, calls.calls, res.Stderr)
	}

	if exists(dir, "build/concordia") || exists(dir, ".concordia") {
		t.Error("--all must remove the cache and the generated files")
	}
}

type recordedCall struct {
	args map[string]any
	tool string
}

type recordingClient struct{ calls []recordedCall }

// singleCallForProject reports whether the client got exactly one call, and it named the project.
func (r *recordingClient) singleCallForProject(project string) bool {
	return len(r.calls) == 1 && r.calls[0].args["project"] == project
}

// singleToolCallForProject reports whether the client got exactly one call, of the tool and for the project.
func (r *recordingClient) singleToolCallForProject(tool, project string) bool {
	return r.singleCallForProject(project) && r.calls[0].tool == tool
}

func (r *recordingClient) Call(_ context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	r.calls = append(r.calls, recordedCall{tool: tool, args: args})

	return json.RawMessage(`{}`), nil
}

func (r *recordingClient) Version(context.Context) (string, error) { return "0.12.1", nil }

func TestCleanProjectNameForms(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"clean", "--project=other", "--yes"},
		{"clean", "--project", "other", "--yes"},
		{"clean", "--yes", "--project", "other"},
	} {
		dir := workspaceWithArtifacts(t)
		calls := &recordingClient{}

		res := runWith(t, &runConfig{Dir: dir, Deps: clientDeps(calls, nil)}, args...)
		if res.Code != 0 || !calls.singleCallForProject("other") {
			t.Errorf("%v: exit code = %d, calls = %+v, stderr = %q", args, res.Code, calls.calls, res.Stderr)
		}

		if res.Stdout != "removed: CBM project 'other'\n" {
			t.Errorf("%v: stdout = %q", args, res.Stdout)
		}
	}
}

func TestCleanNothingSelectedIsAUsageError(t *testing.T) {
	t.Parallel()

	dir := workspaceWithArtifacts(t)

	for _, args := range [][]string{{"clean", "--yes"}, {"clean", "stray", "--yes"}} {
		res := runIn(t, dir, args...)
		if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
			t.Errorf("%v: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
		}
	}
}

func TestCleanWithoutWorkspaceGivesWRK001(t *testing.T) {
	t.Parallel()

	res := runCLI(t, "clean", "--cache", "--yes")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-001") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}
