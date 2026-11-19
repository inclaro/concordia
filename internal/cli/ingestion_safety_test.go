package cli_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// failingTransport fails the test for each request.
type failingTransport struct {
	t    *testing.T
	hits *atomic.Int32
}

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.hits.Add(1)
	f.t.Errorf("network call: %s %s", r.Method, r.URL)

	return nil, errors.New("no network in this test")
}

// sentinelCommands are the programs that hostile content could name. A script of each name is
// put on PATH: when the code starts one, it leaves a line in the log.
var sentinelCommands = []string{"sh", "bash", "zsh", "curl", "wget", "git", "touch", "open", "xdg-open", "codebase-memory-mcp", "cosign", "dot", "java"}

// The ingestion of a repository with embedded commands, diagram links, and include directives
// starts no process and makes no network call because of that content (`--offline`), and the
// content stays text.
func TestIngestionOfHostileRepositoryContentStartsNoProcessAndMakesNoNetworkCall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}

	var served atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		t.Errorf("a link of the repository was fetched: %s", r.URL)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	dir := t.TempDir()
	marker := filepath.Join(dir, "MARKER")
	spawnLog := filepath.Join(dir, "spawned.log")
	bin := filepath.Join(dir, "bin")

	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}

	for _, name := range sentinelCommands {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> '" + spawnLog + "'\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("PATH", bin)

	// The sentinels work: starting one leaves a line in the log.
	if err := exec.CommandContext(t.Context(), "git", "--version").Run(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(spawnLog); err != nil {
		t.Fatalf("the sentinel for git did not run: %v", err)
	}

	if err := os.Remove(spawnLog); err != nil {
		t.Fatal(err)
	}

	command := "$(touch " + marker + ") `touch " + marker + "`; touch " + marker
	link := server.URL + "/diagram.png"
	include := "!include " + server.URL + "/included.yaml"

	arch := strings.Replace(chkArch(), `"description":"billing-service"`, `"description":"`+command+` see `+link+` `+include+`"`, 1)
	if arch == chkArch() {
		t.Fatal("the architecture has no description to change")
	}

	graph := chkGraph()

	for _, e := range graph.Edges {
		if e.From.Path == chkBillingFile {
			e.From.Line = 2
		}
	}

	s := newChkSetup(t, map[string]string{
		"architecture/a.calm.json": arch,
		chkBillingFile:             "// " + command + "\n// ![diagram](" + link + ")\n#include \"" + server.URL + "/header.h\"\n",
		"docs/README.md": "```sh\n" + command + "\n```\n\n```mermaid\nclick A href \"" + link + "\"\n```\n" +
			"<!--" + include + "-->\n!include ../../etc/passwd\n",
		"policies/demo.policy.yaml": strings.Replace(chkPolicy, "Billing must not import fulfillment internals.", command+" "+link+" "+include, 1),
	}, graph)

	var calls atomic.Int32

	s.cfg.Deps.HTTPClient = &http.Client{Transport: failingTransport{t: t, hits: &calls}}
	s.cfg.Deps.NewClient = func(*workspace.Workspace, *cli.Context) observation.Client {
		return observation.NewFakeClient(s.graph.Handler, "")
	}

	commands := [][]string{
		{"--offline", "check"},
		{"--offline", "export", "bundle", "--include-source-snippets", "violations"},
		{"--offline", "explain", "CO-BND-001", "--source"},
		{"--offline", "check", "--format", "sarif"},
	}

	for _, args := range commands {
		if res := s.run(t, args...); res.Code > 1 {
			t.Fatalf("concordia %s: exit code %d, stderr %q", strings.Join(args, " "), res.Code, res.Stderr)
		}
	}

	if _, err := os.Stat(marker); err == nil {
		t.Error("an embedded command ran: the marker file exists")
	}

	if log, err := os.ReadFile(spawnLog); err == nil {
		t.Errorf("a process was started:\n%s", log)
	}

	if calls.Load() != 0 || served.Load() != 0 {
		t.Errorf("network calls: transport %d, server %d", calls.Load(), served.Load())
	}

	// The content is kept as text with its location: the snippet of the violation shows the hostile line.
	res := s.run(t, "--offline", "explain", "CO-BND-001", "--source")
	if !strings.Contains(res.Stdout, chkBillingFile+":2") || !strings.Contains(res.Stdout, "$(touch ") {
		t.Errorf("explain has no location of the finding and the text of its line: %s", res.Stdout)
	}
}
