package cli_test

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const indexWorkspaceYAML = `
apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: demo
observation:
  projects:
    - name: demo
      root: .
      include: [services/**]
      exclude: [services/legacy/**]
  indexMode: fast
`

var indexFiles = []string{"services/a/x.ts", "services/a/y.ts", "services/legacy/Old.kt", "README.md"}

type indexCall struct {
	args map[string]any
	tool string
}

type indexCallLog struct {
	calls []indexCall
	mu    sync.Mutex
}

func (l *indexCallLog) add(tool string, args map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls = append(l.calls, indexCall{tool: tool, args: args})
}

func (l *indexCallLog) find(tool string) map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, c := range l.calls {
		if c.tool == tool {
			return c.args
		}
	}

	return nil
}

func indexGraph(project string) *testutil.FakeGraph {
	var nodes []testutil.FakeNode

	for i, f := range indexFiles {
		if f != "README.md" {
			nodes = append(nodes, testutil.FileNode(string(rune('0'+i)), f))
		}
	}

	return testutil.NewFakeGraph(project, nodes, nil)
}

func indexDeps(g *testutil.FakeGraph, log *indexCallLog) cli.Deps {
	client := observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
		if log != nil {
			log.add(tool, args)
		}

		if tool == "index_repository" {
			return map[string]any{"project": "demo", "status": "indexed"}, nil
		}

		return g.Handler(tool, args)
	}, "")

	return cli.Deps{
		NewClient:    func(*workspace.Workspace, *cli.Context) observation.Client { return client },
		NewGit:       func(string) workspace.GitPort { return &workspace.FakeGit{Rev: "abc123", Files: indexFiles} },
		PollInterval: time.Millisecond,
	}
}

func indexDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"concordia.yaml": indexWorkspaceYAML})

	return dir
}

type coverageRecord struct {
	Project         string   `json:"project"`
	GraphGeneration string   `json:"graphGeneration"`
	SourceRevision  string   `json:"sourceRevision"`
	Excluded        []string `json:"excluded"`
	Files           struct {
		Ratio   float64 `json:"ratio"`
		Total   int     `json:"total"`
		Covered int     `json:"covered"`
	} `json:"files"`
}

func readCoverage(t *testing.T, dir string) coverageRecord {
	t.Helper()

	var rec coverageRecord
	if err := json.Unmarshal([]byte(readFile(t, dir, "build/concordia/coverage.json")), &rec); err != nil {
		t.Fatal(err)
	}

	return rec
}

// indexArgsMatch reports whether the call arguments hold every wanted value.
func indexArgsMatch(args, want map[string]any) bool {
	for key, value := range want {
		if args[key] != value {
			return false
		}
	}

	return true
}

// indexFilesCovered reports whether the coverage record counts the files.
func indexFilesCovered(rec *coverageRecord, total, covered int) bool {
	return rec.Files.Total == total && rec.Files.Covered == covered
}

func assertIndexCoverageRecord(t *testing.T, rec *coverageRecord) {
	t.Helper()

	if got := [2]string{rec.Project, rec.SourceRevision}; got != [2]string{"demo", "abc123"} {
		t.Errorf("record = %+v", rec)
	}

	if !regexp.MustCompile(`^cbm:demo:[0-9a-f]{16}$`).MatchString(rec.GraphGeneration) {
		t.Errorf("generation = %q", rec.GraphGeneration)
	}

	if !indexFilesCovered(rec, 3, 2) || math.Abs(rec.Files.Ratio-2.0/3.0) > 1e-9 {
		t.Errorf("files = %+v", rec.Files)
	}

	if !slices.Equal(rec.Excluded, []string{"services/legacy/Old.kt"}) {
		t.Errorf("excluded = %v", rec.Excluded)
	}
}

func TestIndexWritesTheCoverageFile(t *testing.T) {
	t.Parallel()

	dir := indexDir(t)
	log := &indexCallLog{}
	res := runWith(t, &runConfig{Dir: dir, Deps: indexDeps(indexGraph("demo"), log)}, "index", "--persist")

	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	args := log.find("index_repository")
	if !indexArgsMatch(args, map[string]any{"repo_path": filepath.Clean(dir), "mode": "fast", "name": "demo", "persistence": true}) {
		t.Errorf("index_repository args = %v", args)
	}

	rec := readCoverage(t, dir)
	assertIndexCoverageRecord(t, &rec)

	for _, want := range []string{"project: demo", "generation: cbm:demo:", "66.7%", "coverage file: build/concordia/coverage.json"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout lacks %q: %q", want, res.Stdout)
		}
	}
}

// indexResultClient is a client whose index_repository answer lists the excluded and the partly parsed files.
func indexResultClient() observation.Client { //nolint:ireturn // the Client port is the point: tests replace it.
	return observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
		if tool != "index_repository" {
			return indexGraph("demo").Handler(tool, args)
		}

		return map[string]any{
			"project": "demo", "status": "indexed",
			"excluded":          map[string]any{"dirs": []string{".git", "deploy"}, "count": 2},
			"not_indexed_files": map[string]any{"files": []map[string]string{{"path": "docs/context.png", "reason": "ignored-suffix"}}, "count": 1},
			"parse_partial":     map[string]any{"files": []map[string]string{{"path": "charts/api/templates/deployment.yaml", "error_ranges": "1-4"}}, "count": 1},
		}, nil
	}, "")
}

func TestIndexWritesTheIndexResultRecord(t *testing.T) {
	t.Parallel()

	dir := indexDir(t)
	deps := indexDeps(indexGraph("demo"), nil)
	client := indexResultClient()
	deps.NewClient = func(*workspace.Workspace, *cli.Context) observation.Client { return client }

	if res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "index"); res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var rec observation.IndexRecord
	if err := json.Unmarshal([]byte(readFile(t, dir, "build/concordia/index-result.json")), &rec); err != nil {
		t.Fatal(err)
	}

	got := [3]string{rec.Project, rec.GraphGeneration, rec.SourceRevision}
	if want := [3]string{"demo", readCoverage(t, dir).GraphGeneration, "abc123"}; got != want {
		t.Errorf("record = %+v", rec)
	}

	if !slices.Equal(rec.ExcludedDirs, []string{".git", "deploy"}) {
		t.Errorf("record = %+v", rec)
	}

	if len(rec.NotIndexedFiles) != 1 || len(rec.ParsePartial) != 1 || rec.ParsePartial[0].ErrorRanges != "1-4" {
		t.Errorf("record = %+v", rec)
	}
}

func TestIndexModeProjectIncludeExcludeOverrideTheWorkspace(t *testing.T) {
	t.Parallel()

	dir := indexDir(t)
	log := &indexCallLog{}
	res := runWith(t, &runConfig{Dir: dir, Deps: indexDeps(indexGraph("demo"), log)},
		"index", "--mode", "full", "--project", "demo", "--exclude", "nothing/**")

	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	args := log.find("index_repository")
	if !indexArgsMatch(args, map[string]any{"mode": "full", "name": "demo", "persistence": false}) {
		t.Errorf("index_repository args = %v", args)
	}

	rec := readCoverage(t, dir)
	if !indexFilesCovered(&rec, 3, 3) {
		t.Errorf("files = %+v", rec.Files)
	}
}

func TestIndexPollsUntilTheProjectIsReady(t *testing.T) {
	t.Parallel()

	graph := indexGraph("demo")
	graph.Status = "indexing"

	var (
		mu    sync.Mutex
		polls int
	)

	base := indexDeps(graph, nil)
	inner := base.NewClient(nil, nil)
	base.NewClient = func(*workspace.Workspace, *cli.Context) observation.Client {
		return observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
			mu.Lock()
			defer mu.Unlock()

			if tool == "index_status" {
				polls++
				if polls == 3 {
					graph.Status = "ready"
				}
			}

			raw, err := inner.Call(context.Background(), tool, args)
			if err != nil {
				return nil, err
			}

			return raw, nil
		}, "")
	}

	res := runWith(t, &runConfig{Dir: indexDir(t), Deps: base}, "index")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	mu.Lock()
	defer mu.Unlock()

	if polls < 3 {
		t.Errorf("polls = %d, want at least 3", polls)
	}
}

func TestIndexProjectThatNeverGetsReadyGivesOBS002AfterTheTimeout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"concordia.yaml": strings.Replace(indexWorkspaceYAML, "indexMode: fast", "indexMode: fast\n  indexTimeoutSeconds: 1", 1),
	})

	graph := indexGraph("demo")
	graph.Status = "indexing"

	res := runWith(t, &runConfig{Dir: dir, Deps: indexDeps(graph, nil)}, "index")
	if res.Code != 3 || !strings.Contains(res.Stderr, "CO-OBS-002") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestIndexGraphAtAnotherRevisionGivesOBS008(t *testing.T) {
	t.Parallel()

	graph := indexGraph("demo")
	graph.HeadSHA = "old"

	res := runWith(t, &runConfig{Dir: indexDir(t), Deps: indexDeps(graph, nil)}, "index")
	if !strings.Contains(res.Stderr, "CO-OBS-008") || res.Code == 0 {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestIndexIncompatibleCbmVersionGivesOBS004(t *testing.T) {
	t.Parallel()

	deps := cli.Deps{NewClient: func(*workspace.Workspace, *cli.Context) observation.Client {
		return observation.NewFakeClient(func(string, map[string]any) (any, error) { return map[string]any{}, nil }, "1.2.0")
	}}

	res := runWith(t, &runConfig{Dir: indexDir(t), Deps: deps}, "index")
	if res.Code != 5 || !strings.Contains(res.Stderr, "CO-OBS-004") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestIndexFormatJSONPrintsASummary(t *testing.T) {
	t.Parallel()

	res := runWith(t, &runConfig{Dir: indexDir(t), Deps: indexDeps(indexGraph("demo"), nil)}, "index", "--format", "json")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var summary struct {
		Project string `json:"project"`
		Files   struct {
			Total int `json:"total"`
		} `json:"files"`
	}

	if err := json.Unmarshal([]byte(res.Stdout), &summary); err != nil {
		t.Fatalf("stdout is not JSON: %v: %q", err, res.Stdout)
	}

	if summary.Project != "demo" || summary.Files.Total != 3 {
		t.Errorf("summary = %+v", summary)
	}
}

func TestIndexUnknownProjectIsAUsageError(t *testing.T) {
	t.Parallel()

	res := runWith(t, &runConfig{Dir: indexDir(t), Deps: indexDeps(indexGraph("demo"), nil)}, "index", "--project", "other")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// indexMultiProjectDeps is a workspace of two projects whose second project has no cross-repo support. It gives
// the workspace directory, the dependencies, and the log of the calls.
func indexMultiProjectDeps(t *testing.T) (string, cli.Deps, *indexCallLog) {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"concordia.yaml": strings.Replace(indexWorkspaceYAML, "  indexMode: fast", "    - name: pay\n      root: pay\n  indexMode: fast", 1)})

	log := &indexCallLog{}
	graphs := []*testutil.FakeGraph{indexGraph("demo"), indexGraph("pay")}
	multi := testutil.MultiHandler(graphs...)
	client := observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
		log.add(tool, args)

		if tool != "index_repository" {
			return multi(tool, args)
		}

		if args["mode"] == "cross-repo-intelligence" && args["name"] == "pay" {
			return map[string]any{"status": "error", "hint": "no cross-repo support"}, nil
		}

		return map[string]any{"status": "indexed"}, nil
	}, "")

	deps := indexDeps(graphs[0], nil)
	deps.NewClient = func(*workspace.Workspace, *cli.Context) observation.Client { return client }

	return dir, deps, log
}

// indexCrossRepoTargets gives the target projects of each cross-repo indexing call, in call order.
func indexCrossRepoTargets(log *indexCallLog) []string {
	var targets []string

	for _, c := range log.calls {
		if c.tool != "index_repository" || c.args["mode"] != "cross-repo-intelligence" {
			continue
		}

		list, _ := c.args["target_projects"].([]string)
		targets = append(targets, strings.Join(list, ","))
	}

	return targets
}

func TestIndexProjectsIndexesEachProjectAndMatchesAcrossProjects(t *testing.T) {
	t.Parallel()

	dir, deps, log := indexMultiProjectDeps(t)

	res := runWith(t, &runConfig{Dir: dir, Deps: deps}, "index", "--format", "json")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var summaries []map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &summaries); err != nil || len(summaries) != 2 {
		t.Fatalf("summaries = %v, %v (%q)", summaries, err, res.Stdout)
	}

	if summaries[0]["coverageFile"] != "build/concordia/coverage.json" || summaries[1]["coverageFile"] != "build/concordia/coverage-pay.json" {
		t.Errorf("summaries = %v", summaries)
	}

	if cross := indexCrossRepoTargets(log); !slices.Equal(cross, []string{"pay", "demo"}) {
		t.Errorf("cross calls = %v", cross)
	}

	if !stderrNames(res.Stderr, "CO-OBS-014", "pay") {
		t.Errorf("stderr = %q", res.Stderr)
	}

	if rec := readFile(t, dir, "build/concordia/coverage-pay.json"); !strings.Contains(rec, `"project": "pay"`) {
		t.Errorf("pay coverage = %s", rec)
	}
}
