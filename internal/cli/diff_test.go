package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestDiffResultsFilesFindingsAndWaivers(t *testing.T) {
	t.Parallel()

	from := testutil.SampleRun()
	to := testutil.SampleRun()
	to.Findings = slices.DeleteFunc(to.Findings, func(f conformance.Finding) bool { return f.Fingerprint == testutil.FPActive })

	for i := range to.Exceptions.Waivers {
		to.Exceptions.Waivers[i].State = "expired"
	}

	write := func(v any) string {
		text, err := canonjson.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}

		return string(text)
	}

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"from.json": write(from), "to.json": write(to)})

	res := runIn(t, dir, "diff", "--from", "from.json", "--to", "to.json", "--kind", "findings", "--format", "json")
	if res.Code != 0 {
		t.Fatalf("exit %d: %s", res.Code, res.Stderr)
	}

	var out struct {
		Findings struct {
			Resolved []json.RawMessage `json:"resolved"`
		} `json:"findings"`
		Waivers struct {
			Expired []string `json:"expired"`
		} `json:"waivers"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		t.Fatal(err)
	}

	if len(out.Findings.Resolved) != 1 || len(out.Waivers.Expired) != 1 {
		t.Fatalf("unexpected diff: %s", res.Stdout)
	}

	pretty := runIn(t, dir, "diff", "--from", "from.json", "--to", "to.json", "--kind", "findings")
	wantContains(t, pretty.Stdout, "RESOLVED CO-BND-001", "EXPIRED waiver WV-1")

	if bad := runIn(t, dir, "diff", "--from", "from.json", "--to", "to.json", "--kind", "intent"); bad.Code != 2 {
		t.Fatalf("an unavailable kind must exit 2, got %d", bad.Code)
	}

	if bad := runIn(t, dir, "diff", "--from", "from.json", "--to", "to.json", "--format", "junit"); bad.Code != 2 {
		t.Fatalf("a bad format must exit 2, got %d", bad.Code)
	}
}

func TestDiffGitRefSideShowsAnAddedNode(t *testing.T) {
	t.Parallel()

	setup := newChkSetup(t, nil, nil)
	old := chkJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes":   []any{chkNode("billing-service", nil), chkNode("fulfillment-service", nil)},
	})
	setup.cfg.Deps.NewGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{
			Rev:     "abc123",
			Refs:    map[string]string{"HEAD": "abc123"},
			FilesAt: map[string]map[string]string{"abc123": {"architecture/a.calm.json": old}},
		}
	}

	res := setup.run(t, "diff", "--from", "HEAD", "--to", "worktree", "--kind", "intent")
	if res.Code != 0 {
		t.Fatalf("exit %d: %s", res.Code, res.Stderr)
	}

	wantContains(t, res.Stdout, "+ node legacy-service")
}

func TestDiffGitRefRejectsSourceOutsideWorkspace(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"../outside.calm.json", "/etc/outside.calm.json"} {
		ws := strings.Replace(chkWorkspaceText(""), "source: architecture/a.calm.json", "source: "+source, 1)
		setup := newChkSetup(t, map[string]string{"concordia.yaml": ws}, nil)
		setup.cfg.Deps.NewGit = func(string) workspace.GitPort {
			return &workspace.FakeGit{Rev: "abc123", Refs: map[string]string{"HEAD": "abc123"}}
		}

		res := setup.run(t, "diff", "--from", "HEAD", "--to", "worktree", "--kind", "intent")
		if res.Code != 2 {
			t.Fatalf("%s: exit %d, want 2: %s", source, res.Code, res.Stderr)
		}

		wantContains(t, res.Stderr, "CO-WRK-010", "outside the workspace")
	}
}
