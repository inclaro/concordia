package conformance_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestInputErrors(t *testing.T) {
	t.Parallel()

	badCalm := mustJSON(map[string]any{
		"$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
		"nodes":   []any{map[string]any{"unique-id": "a", "name": "a", "description": "d"}}, "relationships": []any{},
	})
	dup := bindingsText + `  - {id: b-dup, architectureRef: "calm:node:legacy-service", match: {any: [{sourceRoot: "services/billing/**"}]}}` + "\n"

	cases := []struct {
		name  string
		files map[string]string
		code  string
		exit  int
	}{
		{"calm schema violation", map[string]string{"architecture/a.calm.json": badCalm}, "CO-INT-002", 2},
		{"invalid policy", map[string]string{"policies/demo.policy.yaml": "apiVersion: nope\n"}, "CO-POL-001", 2},
		{"ambiguous binding", map[string]string{"bindings/concordia.bindings.yaml": dup}, "CO-BND-003", 2},
	}

	for _, c := range cases {
		e := newSetup(t, c.files, nil).failureOf(t, nil)
		if e.Diagnostic.Code != c.code || e.ExitCode != c.exit {
			t.Errorf("%s: %s %d", c.name, e.Diagnostic.Code, e.ExitCode)
		}
	}
}

func TestGraphIndexedAtAnotherRevision(t *testing.T) {
	t.Parallel()

	graph := buildGraph(graphOpts{})
	graph.HeadSHA = "def456"
	s := newSetup(t, nil, &setupOpts{graph: graph})

	e := s.failureOf(t, nil)
	if e.Diagnostic.Code != "CO-OBS-008" || e.ExitCode != 3 {
		t.Fatalf("%s %d queries %v", e.Diagnostic.Code, e.ExitCode, s.graph.Queries)
	}

	if len(s.graph.Queries) != 0 {
		t.Fatalf("%s %d queries %v", e.Diagnostic.Code, e.ExitCode, s.graph.Queries)
	}
}

func TestGraphChangesDuringQueries(t *testing.T) {
	t.Parallel()

	graph := buildGraph(graphOpts{})
	s := newSetup(t, nil, &setupOpts{graph: graph})
	s.deps.CreateClient = func(*workspaceT, *loggerT) observation.Client {
		return observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
			res, err := graph.Handler(tool, args)
			if tool == "query_graph" {
				graph.NodeBump = 1
			}

			return res, err
		}, "")
	}

	if e := s.failureOf(t, nil); e.Diagnostic.Code != "CO-OBS-008" || e.ExitCode != 3 {
		t.Fatalf("%s %d", e.Diagnostic.Code, e.ExitCode)
	}
}

func TestProjectNotIndexed(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, &setupOpts{ws: strings.Replace(wsText(""), "{name: demo, root", "{name: other, root", 1)})

	if e := s.failureOf(t, nil); e.Diagnostic.Code != "CO-OBS-002" {
		t.Fatalf("code %s", e.Diagnostic.Code)
	}
}

func TestFreezesBeforeFirstQueryAndVerifiesAfterLast(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)

	var calls []string

	s.deps.CreateClient = func(*workspaceT, *loggerT) observation.Client {
		return observation.NewFakeClient(func(tool string, args map[string]any) (any, error) {
			calls = append(calls, tool)

			return s.graph.Handler(tool, args)
		}, "")
	}
	s.mustCheck(t, nil)

	first := slices.Index(calls, "query_graph")
	slices.Reverse(calls)
	lastFromEnd := slices.Index(calls, "query_graph")
	slices.Reverse(calls)

	last := len(calls) - 1 - lastFromEnd

	if !slices.Equal(calls[:first], []string{"index_status", "get_graph_schema"}) {
		t.Errorf("before the first query: %v", calls[:first])
	}

	after := 0

	for _, c := range calls[last+1:] {
		if c == "index_status" {
			after++
		}
	}

	if after != 1 {
		t.Errorf("index_status after the last query: %d", after)
	}
}

func TestDirtyTreeDigestIgnoresOwnOutputFiles(t *testing.T) {
	t.Parallel()

	a := newSetup(t, nil, nil)
	b := newSetup(t, nil, &setupOpts{dirty: []string{"build/concordia/results.json", ".concordia/runs/x/manifest.json", "build/concordia/coverage.json"}})

	if a.mustCheck(t, nil).Run.DirtyTreeDigest != b.mustCheck(t, nil).Run.DirtyTreeDigest {
		t.Error("the digests differ")
	}
}

func TestDirtyTreeDigestIgnoresViewFiles(t *testing.T) {
	t.Parallel()

	const views = `
views:
  - id: demo-container
    kind: container
    origin: generated
    owner: team
    scope: [billing]
    abstraction: container
    output:
      path: docs/views/demo.dsl
      exports:
        - {format: mermaid, path: docs/views/demo.mmd}
`

	ws := wsText(views)
	clean := newSetup(t, nil, &setupOpts{ws: ws, dirty: []string{"services/billing/x.go"}})
	withViews := newSetup(t, nil, &setupOpts{ws: ws, dirty: []string{
		"services/billing/x.go", "docs/views/demo.dsl", "docs/views/demo.mmd", "docs/views/demo.dsl.manifest.json",
	}})
	other := newSetup(t, nil, &setupOpts{ws: ws, dirty: []string{"services/billing/x.go", "docs/views/other.dsl"}})

	want := clean.mustCheck(t, nil).Run.DirtyTreeDigest

	if got := withViews.mustCheck(t, nil).Run.DirtyTreeDigest; got != want {
		t.Errorf("view files change the dirty-tree digest: %s != %s", got, want)
	}

	if got := other.mustCheck(t, nil).Run.DirtyTreeDigest; got == want {
		t.Error("a file that is no view output leaves the dirty-tree digest unchanged")
	}
}

func TestResultRecordsVersionsAndDigestsOfInputs(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	run := s.mustCheck(t, nil)

	assertRunIdentity(t, run)
	assertRunToolchain(t, &run.Toolchain)
	assertRunInputDigests(t, s.dir, run)
}

func assertRunIdentity(t *testing.T, run *conformance.Run) {
	t.Helper()

	if run.Run.Project != "demo" || run.Run.SourceRevision == nil {
		t.Fatalf("run %+v", run.Run)
	}

	if *run.Run.SourceRevision != "abc123" {
		t.Errorf("run %+v", run.Run)
	}

	if !strings.HasPrefix(run.Run.GraphGeneration, "cbm:demo:") || len(run.Run.GraphGeneration) != len("cbm:demo:")+16 {
		t.Errorf("generation %s", run.Run.GraphGeneration)
	}
}

func assertRunToolchain(t *testing.T, tc *conformance.RunToolchain) {
	t.Helper()

	expectAll(t, "toolchain %+v", tc, tc.CalmSchema == "1.2", tc.Cbm.ExecutableVersion == "0.12.1", tc.Cbm.AdapterSchema == "concordia-cbm/v2", tc.Concordia == "test")

	profile := tc.Cbm.Profile
	expectAll(t, "profile %+v", profile, profile.Applies, profile.ID == "cbm-0.12.1", strings.HasPrefix(profile.Digest, "sha256:"))
}

func assertRunInputDigests(t *testing.T, dir string, run *conformance.Run) {
	t.Helper()

	read := func(rel string) string { return testutil.ReadFile(t, filepath.Join(dir, rel)) }

	if run.Inputs.Workspace != (conformance.InputRef{Path: "concordia.yaml", Digest: "sha256:" + sha(read("concordia.yaml"))}) {
		t.Errorf("workspace %+v", run.Inputs.Workspace)
	}

	if run.Inputs.Architecture != (conformance.InputRef{Path: "architecture/a.calm.json", Digest: "sha256:" + sha(read("architecture/a.calm.json"))}) {
		t.Errorf("architecture %+v", run.Inputs.Architecture)
	}

	if !slices.Equal(run.Inputs.Policies, []conformance.InputRef{{Path: "policies/demo.policy.yaml", Digest: "sha256:" + sha(policyText)}}) {
		t.Errorf("policies %+v", run.Inputs.Policies)
	}

	if !slices.Equal(run.Inputs.Bindings, []conformance.InputRef{{Path: "bindings/concordia.bindings.yaml", Digest: "sha256:" + sha(bindingsText)}}) {
		t.Errorf("bindings %+v", run.Inputs.Bindings)
	}

	if len(run.Inputs.QueryPlanDigest) != 64 || run.Inputs.Baseline != nil {
		t.Errorf("inputs %+v", run.Inputs)
	}
}

func TestTwoRunsGiveTheSameJSONApartFromVolatileFields(t *testing.T) {
	t.Setenv("CONCORDIA_NOW", "2026-06-01T00:00:00Z")

	a, b := newSetup(t, nil, nil), newSetup(t, nil, nil)
	a.env.RunID, b.env.RunID = "20260601T000000Z-aaaaaaaa", "20260601T000000Z-bbbbbbbb"

	first, second := a.mustCheck(t, nil), b.mustCheck(t, nil)

	if first.Run.ID == second.Run.ID || stable(t, first) != stable(t, second) {
		t.Error("the runs differ, or the IDs are equal")
	}
}

func TestCollectionsAreSorted(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, &setupOpts{graph: buildGraph(graphOpts{refund: true})}).mustCheck(t, nil)

	ids := make([]string, 0, len(run.Rules))
	for _, r := range run.Rules {
		ids = append(ids, r.RuleID)
	}

	if !slices.IsSorted(ids) {
		t.Errorf("rules %v", ids)
	}

	keys := make([]string, 0, len(run.Findings))
	for _, f := range run.Findings {
		keys = append(keys, f.RuleID+"\x00"+f.Fingerprint)
	}

	if !slices.IsSorted(keys) {
		t.Errorf("findings %v", keys)
	}
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Error("network use")

	return nil, http.ErrNotSupported
}

func TestMakesNoNetworkRequest(t *testing.T) {
	prior := http.DefaultTransport
	http.DefaultTransport = failingTransport{t}

	t.Cleanup(func() { http.DefaultTransport = prior })

	run := newSetup(t, nil, &setupOpts{offline: true}).mustCheck(t, nil)
	if run.Summary.ExitCode != 1 {
		t.Errorf("exit code %d", run.Summary.ExitCode)
	}
}

func TestOfflineWithUnlockedRemoteResourceFails(t *testing.T) {
	t.Parallel()

	arch := archText(map[string]any{
		"controls": map[string]any{"control-1": map[string]any{
			"description": "d",
			"requirements": []any{map[string]any{
				"requirement-url": "https://example.org/req.json",
				"config":          map[string]any{"control-id": "control-1", "engine": "concordia", "rule-ids": []any{"CO-BND-001"}},
			}},
		}},
	})
	ws := strings.Replace(wsText(""), "architecture: {source: architecture/a.calm.json}",
		"architecture: {source: architecture/a.calm.json, urlMapping: architecture/urls.json}", 1)
	s := newSetup(t, map[string]string{
		"architecture/a.calm.json": arch,
		"architecture/urls.json":   `{"https://example.org/req.json": "req.json"}`,
		"architecture/req.json":    "{}",
	}, &setupOpts{offline: true, ws: ws})

	if e := s.failureOf(t, nil); e.Diagnostic.Code != "CO-INT-012" || e.ExitCode != 6 {
		t.Fatalf("%s %d", e.Diagnostic.Code, e.ExitCode)
	}
}

func TestRemoteResourceIsFetchedLockedAndReusedOffline(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)

		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)

	arch := archText(map[string]any{
		"controls": map[string]any{"control-1": map[string]any{
			"description": "d",
			"requirements": []any{map[string]any{
				"requirement-url": srv.URL + "/req.json",
				"config":          map[string]any{"control-id": "control-1", "engine": "concordia", "rule-ids": []any{"CO-BND-001"}},
			}},
		}},
	})
	s := newSetup(t, map[string]string{"architecture/a.calm.json": arch}, nil)
	withClient := func(o *conformance.CheckOptions) { o.Deps.HTTPClient = srv.Client() }

	s.mustCheck(t, withClient)

	if requests.Load() != 1 {
		t.Fatalf("%d requests", requests.Load())
	}

	lock, found, err := workspace.ReadLock(workspace.LockPathFor(s.dir))
	if err != nil || !found {
		t.Fatalf("lock: %v %v", found, err)
	}

	if !slices.ContainsFunc(lock.Resources, func(r workspace.LockResource) bool {
		return r.URI == srv.URL+"/req.json" && strings.HasPrefix(r.Path, ".concordia/cache/resources/")
	}) {
		t.Fatalf("lock resources %+v", lock.Resources)
	}

	s.env.Offline = true
	s.mustCheck(t, withClient)

	if requests.Load() != 1 {
		t.Errorf("%d requests after the offline run", requests.Load())
	}
}

func TestCheckRecordsDecoratorInputs(t *testing.T) {
	t.Parallel()

	ws := strings.Replace(wsText(""), "architecture: {source: architecture/a.calm.json}",
		"architecture: {source: architecture/a.calm.json, decorators: [decorators/d.json]}", 1)
	decorator := func(appliesTo string) string {
		return `{"unique-id":"d1","type":"deployment","target":["../architecture/a.calm.json"],"applies-to":["` +
			appliesTo + `"],"data":{"status":"completed"}}`
	}

	text := decorator("billing-service")
	run := newSetup(t, map[string]string{"decorators/d.json": text}, &setupOpts{ws: ws}).mustCheck(t, nil)

	want := []conformance.InputRef{{Path: "decorators/d.json", Digest: "sha256:" + sha(text)}}
	if !slices.Equal(run.Inputs.Decorators, want) {
		t.Errorf("decorators = %+v, want %+v", run.Inputs.Decorators, want)
	}

	e := newSetup(t, map[string]string{"decorators/d.json": decorator("nope")}, &setupOpts{ws: ws}).failureOf(t, nil)
	if e.Diagnostic.Code != "CO-INT-009" {
		t.Errorf("code = %s, want CO-INT-009", e.Diagnostic.Code)
	}
}
