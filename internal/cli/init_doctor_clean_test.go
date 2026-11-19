package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()

	return testutil.ReadFile(t, filepath.Join(dir, filepath.FromSlash(rel)))
}

func exists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))

	return err == nil
}

func cbmVersion(version string) cli.Deps {
	return cli.Deps{Cbm: &cli.CbmDeps{Find: func(context.Context) cli.CbmBuild { return cli.CbmBuild{Version: version} }}}
}

func initIn(t *testing.T, dir string, deps cli.Deps, args ...string) result {
	t.Helper()

	return runWith(t, &runConfig{Dir: dir, Deps: deps}, append([]string{"init"}, args...)...)
}

func fakeCbm(version string, projects ...string) *observation.FakeClient {
	return observation.NewFakeClient(func(tool string, _ map[string]any) (any, error) {
		if tool != "list_projects" {
			return nil, errors.New("unexpected " + tool)
		}

		list := []map[string]any{}
		for _, name := range projects {
			list = append(list, map[string]any{"name": name})
		}

		return map[string]any{"projects": list}, nil
	}, version)
}

func okProbe(_ context.Context, cmd string, _ []string) *cli.ProbeResult {
	switch cmd {
	case "git":
		return &cli.ProbeResult{Stdout: "git version 2.43.0\n"}
	case "java":
		return &cli.ProbeResult{Stderr: "openjdk version \"21.0.1\"\n"}
	default:
		return nil
	}
}

func clientDeps(client observation.Client, probe func(context.Context, string, []string) *cli.ProbeResult) cli.Deps {
	return cli.Deps{
		NewClient: func(*workspace.Workspace, *cli.Context) observation.Client { return client },
		Probe:     probe,
	}
}

func initialized(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "doctor-repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if res := initIn(t, dir, cbmVersion("0.12.1")); res.Code != 0 {
		t.Fatalf("init: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	return dir
}

type capabilityList struct {
	Capabilities []struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Detail  string `json:"detail"`
		Version string `json:"version"`
	} `json:"capabilities"`
}

// capState is the status, the detail and the version of one capability in the output of `doctor`.
type capState struct{ Status, Detail, Version string }

func capabilities(t *testing.T, stdout string) map[string]capState {
	t.Helper()

	var list capabilityList
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}

	out := map[string]capState{}
	for _, c := range list.Capabilities {
		out[c.Name] = capState{c.Status, c.Detail, c.Version}
	}

	return out
}

// containsEvery reports whether text contains every one of the parts.
func containsEvery(text string, parts ...string) bool {
	return !slices.ContainsFunc(parts, func(part string) bool { return !strings.Contains(text, part) })
}
