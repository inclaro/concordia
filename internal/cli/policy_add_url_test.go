package cli_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/inclaro/concordia/internal/cli"
)

// ---------------------------------------------------------------------------------------------
// policy add <url>

type pkServer struct {
	srv      *httptest.Server
	requests atomic.Int32
}

func newPKServer(t *testing.T, handler http.HandlerFunc) *pkServer {
	t.Helper()

	s := &pkServer{}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		handler(w, r)
	}))

	t.Cleanup(s.srv.Close)

	return s
}

func (s *pkServer) cfg(dir string) *runConfig {
	return &runConfig{Dir: dir, Deps: cli.Deps{HTTPClient: s.srv.Client()}}
}

func TestPolicyAddFetchesAnHTTPSURL(t *testing.T) {
	t.Parallel()

	server := newPKServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(pkPackText)) })
	dir := pkDir(t, pkWorkspaceBase, nil)

	pkExpectAdded(t, runWith(t, server.cfg(dir), "policy", "add", server.srv.URL+"/acme.policy.yaml"), pkVendorRel)

	if got := readFile(t, dir, pkVendorRel); got != pkPackText {
		t.Errorf("vendored file:\n%s", got)
	}

	wantContains(t, readFile(t, dir, "concordia.yaml"), "sha256: "+pkSum(pkPackText))

	if server.requests.Load() != 1 {
		t.Errorf("requests = %d", server.requests.Load())
	}
}

func TestPolicyAddFetchFailuresGiveExitCode6(t *testing.T) {
	t.Parallel()

	cases := map[string]http.HandlerFunc{
		"not found": func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) },
		"too large": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("# filler\n", 200_000)))
		},
	}

	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := newPKServer(t, handler)
			dir := pkDir(t, pkWorkspaceBase, nil)

			pkExpectStderr(t, runWith(t, server.cfg(dir), "policy", "add", server.srv.URL+"/acme.policy.yaml"), 6, "CO-POL-016")
			pkExpectWorkspaceUnchanged(t, dir)

			if exists(dir, "policies") {
				t.Error("a failed fetch made a policies directory")
			}
		})
	}
}

func TestPolicyAddUnreachableURLGivesExitCode6(t *testing.T) {
	t.Parallel()

	server := newPKServer(t, func(http.ResponseWriter, *http.Request) {})
	url := server.srv.URL + "/x.yaml"
	server.srv.Close()

	pkExpectStderr(t, runWith(t, server.cfg(pkDir(t, pkWorkspaceBase, nil)), "policy", "add", url), 6, "CO-POL-016")
}

func TestPolicyAddRefusesToFetchOffline(t *testing.T) {
	t.Parallel()

	server := newPKServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(pkPackText)) })
	dir := pkDir(t, pkWorkspaceBase, nil)

	pkExpectStderr(t, runWith(t, server.cfg(dir), "--offline", "policy", "add", server.srv.URL+"/acme.policy.yaml"), 6, "CO-POL-016", "--offline")

	if server.requests.Load() != 0 {
		t.Errorf("an offline run made %d request(s)", server.requests.Load())
	}

	// A local file or a built-in pack still works offline.
	if local := runIn(t, pkDir(t, pkWorkspaceBase, map[string]string{"p.yaml": pkPackText}), "--offline", "policy", "add", "builtin:core"); local.Code != 0 {
		t.Errorf("offline built-in add: exit code = %d, stderr = %q", local.Code, local.Stderr)
	}
}

func TestPolicyAddFetchStopsWhenTheRunIsCanceled(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	server := newPKServer(t, func(http.ResponseWriter, *http.Request) { <-release })

	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	cfg := server.cfg(pkDir(t, pkWorkspaceBase, nil))
	cfg.Ctx = ctx

	pkExpectStderr(t, runWith(t, cfg, "policy", "add", server.srv.URL+"/x.yaml"), 130)
}
