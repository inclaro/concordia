// Command record-cbm records the CBM calls of four fixtures:
//
//	mise run record-cbm
//
// Commerce: it copies test/fixtures/commerce to a fixed directory, commits it
// with a fixed author and date, indexes it with the real codebase-memory-mcp,
// and checks it. The responses go to
// test/fixtures/cbm-recordings/commerce.json.
//
// Infrastructure: it does the same for test/fixtures/infra and records the
// whole graph (infra.go). The responses go to
// test/fixtures/cbm-recordings/infra.json, and a second recording of the same
// repository with `.cbmignore` `!deploy/` goes to
// test/fixtures/cbm-recordings/infra-reinclude-deploy.json.
//
// View currency: it does the same for test/fixtures/viewcurrency (views.go), as it is
// (viewcurrency.json) and in three variants: the two diagram blocks of a section in
// another order (viewcurrency-reordered.json), a web function that calls billing
// (viewcurrency-drift.json), and an internal refactoring of the orders service
// (viewcurrency-refactored.json). Each recording holds the adapter session, the whole
// graph, and the `index` and `check` commands of the workspace.
//
// Decisions: it does the same for test/fixtures/adr (adr.go): the ADR files and the supersession
// variants, the adapter session, the whole graph, and the `index` and `check` commands. The responses go to
// test/fixtures/cbm-recordings/adr.json.
//
// Review currency: it records the variants of test/fixtures/review/cbm-0.12.1/fixture.json that the baseline
// (the infra recording) does not cover: the infra repository without a Secret manifest that a workload still
// refers to (review.go). The baseline is not recorded again.
//
// Tests replay these files without CBM (observation.ReplayClient). The key of
// the index_repository call does not contain the repository path, so a
// recording replays in any directory.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/procexec"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	// repoDirName is the directory (below the temporary directory) of the recorded repository.
	repoDirName = "concordia-record-commerce"
	// fixtureDir and outputFile are relative to the module root.
	fixtureDir = "test/fixtures/commerce"
	outputFile = "test/fixtures/cbm-recordings/commerce.json"

	recorderName  = "Concordia Recorder"
	recorderEmail = "recorder@concordia.invalid"
	recordedDate  = "2026-01-01T00:00:00Z"

	gitTimeout   = time.Minute
	pollInterval = 200 * time.Millisecond

	// extraFileMode is the mode of the files that the recorder adds to the repository.
	extraFileMode = 0o600
	// flagQuiet is a git option and a global option of concordia.
	flagQuiet = "--quiet"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:])

	stop()

	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "record-cbm: %v\n", err)

		os.Exit(1)
	}
}

// recorder records the CBM calls of one fixture.
type recorder struct {
	record func(ctx context.Context, root string) error
	name   string
}

// recorders are the fixtures that record-cbm records, in order. The arguments of the command select some of them.
var recorders = []recorder{
	{name: "commerce", record: recordCommerce},
	{name: "infra", record: recordInfra},
	{name: "views", record: recordViews},
	{name: "adr", record: recordADR},
	{name: "review", record: recordReview},
}

func run(ctx context.Context, selected []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}

	for _, name := range selected {
		if !slices.ContainsFunc(recorders, func(r recorder) bool { return r.name == name }) {
			return fmt.Errorf("unknown fixture %q: use commerce, infra, views, adr or review", name)
		}
	}

	for _, r := range recorders {
		if len(selected) > 0 && !slices.Contains(selected, r.name) {
			continue
		}

		if err := r.record(ctx, root); err != nil {
			return err
		}
	}

	return nil
}

func recordCommerce(ctx context.Context, root string) error {
	repoDir := filepath.Join(os.TempDir(), repoDirName)

	if err := prepareRepo(ctx, filepath.Join(root, filepath.FromSlash(fixtureDir)), repoDir, "commerce fixture", nil); err != nil {
		return err
	}

	defer func() { _ = os.RemoveAll(repoDir) }()

	wsp, err := workspace.Load(filepath.Join(repoDir, cli.DefaultWorkspaceFile), "")
	if err != nil {
		return fmt.Errorf("load the recorded workspace: %w", err)
	}

	project := wsp.Observation.Projects[0].Name

	headSHA, err := workspace.NewCliGit(repoDir).Revision(ctx)
	if err != nil {
		return fmt.Errorf("git revision: %w", err)
	}

	if headSHA == "" {
		return errors.New("no git revision after the commit")
	}

	raw := observation.NewProcessClient(observation.ProcessOptionsFrom(&wsp.Observation))

	defer closeClient(raw)

	// A project left by an earlier run must not change the first index call.
	deleteProject(ctx, raw, project)

	defer func() {
		// The cleanup runs after an interrupt too.
		deleteProject(context.WithoutCancel(ctx), raw, project)
	}()

	sink := observation.NewMemorySink(headSHA)

	// The commands of record start their own CBM process: end this one first.
	closeClient(raw)

	if err := record(ctx, repoDir, sink); err != nil {
		return err
	}

	recording := sink.ToRecording()

	data, err := canonjson.Marshal(recording)
	if err != nil {
		return fmt.Errorf("encode the recording: %w", err)
	}

	output := filepath.Join(root, filepath.FromSlash(outputFile))
	if err = fsutil.WriteFileAtomic(output, data); err != nil {
		return fmt.Errorf("write the recording: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "recorded %d call(s) at %s -> %s\n", len(recording.Calls), headSHA, output)

	return nil
}

// moduleRoot finds the directory of go.mod above the working directory.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}

	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above the working directory: run record-cbm inside the repository")
		}

		dir = parent
	}
}

// prepareRepo copies the fixture to repoDir (a fresh directory), writes the
// extra files (path relative to repoDir, content) and commits everything.
func prepareRepo(ctx context.Context, fixture, repoDir, message string, extra map[string]string) error {
	if err := os.RemoveAll(repoDir); err != nil {
		return fmt.Errorf("remove %s: %w", repoDir, err)
	}

	if err := os.CopyFS(repoDir, os.DirFS(fixture)); err != nil {
		return fmt.Errorf("copy the fixture: %w", err)
	}

	for name, content := range extra {
		if err := os.WriteFile(filepath.Join(repoDir, filepath.FromSlash(name)), []byte(content), extraFileMode); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	for _, args := range [][]string{
		{"init", flagQuiet, "--initial-branch=main"},
		{"add", "-A"},
		{"-c", "commit.gpgsign=false", "commit", flagQuiet, "-m", message},
	} {
		if err := git(ctx, repoDir, args...); err != nil {
			return err
		}
	}

	return nil
}

// git runs git in dir with a fixed author, committer and date, and without user configuration.
func git(ctx context.Context, dir string, args ...string) error {
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME="+recorderName,
		"GIT_AUTHOR_EMAIL="+recorderEmail,
		"GIT_COMMITTER_NAME="+recorderName,
		"GIT_COMMITTER_EMAIL="+recorderEmail,
		"GIT_AUTHOR_DATE="+recordedDate,
		"GIT_COMMITTER_DATE="+recordedDate,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)

	res, err := procexec.Run(ctx, "git", args, procexec.Options{Dir: dir, Env: env, Timeout: gitTimeout})
	if err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	if res.Code != 0 {
		return fmt.Errorf("git %s failed (exit code %d): %s", strings.Join(args, " "), res.Code, strings.TrimSpace(res.Stderr))
	}

	return nil
}

// closeClient ends the CBM process of a client. A recording does not depend on the result.
func closeClient(client *observation.ProcessClient) {
	_ = client.Close()
}

// deleteProject removes a CBM project. A project that does not exist is not an error.
func deleteProject(ctx context.Context, client observation.Client, project string) {
	_, _ = client.Call(ctx, "delete_project", map[string]any{"project": project})
}

// step is one command of the recorded session and the exit codes that mean "it ran".
type step struct {
	name string
	args []string
	ok   []int
}

// record runs `index` and `check` in repoDir with the real CBM. The client of
// each command is the real client wrapped in a RecordingClient on sink.
func record(ctx context.Context, repoDir string, sink *observation.MemorySink) error {
	deps := cli.Deps{
		NewClient: func(wsp *workspace.Workspace, app *cli.Context) observation.Client {
			return observation.NewRecordingClient(cli.DefaultClient(wsp, app), sink)
		},
		PollInterval: pollInterval,
	}

	global := []string{"--workspace", cli.DefaultWorkspaceFile, flagQuiet, "--no-color"}

	// check is a whole-pipeline run. A run with `--rule` uses a subset of the same queries.
	steps := []step{
		{name: "index", args: []string{"index"}, ok: []int{diag.OK}},
		{name: "check", args: []string{"check"}, ok: []int{diag.OK, diag.Violation, diag.Unknown}},
	}

	for _, entry := range steps {
		stdio := cli.IO{
			Stdin:  strings.NewReader(""),
			Stdout: io.Discard,
			Stderr: os.Stderr,
			Env:    environ(),
			Cwd:    repoDir,
		}

		code := cli.Run(ctx, slices.Concat(global, entry.args), &stdio, deps, cli.BuildInfo{Version: "record-cbm"})
		if !slices.Contains(entry.ok, code) {
			return fmt.Errorf("concordia %s exited with code %d", entry.name, code)
		}
	}

	return nil
}

// environ gives the process environment as a map.
func environ() map[string]string {
	env := map[string]string{}

	for _, entry := range os.Environ() {
		// Windows keeps entries like "=C:=C:\" that have an empty name.
		if key, value, ok := strings.Cut(entry, "="); ok && key != "" {
			env[key] = value
		}
	}

	return env
}
