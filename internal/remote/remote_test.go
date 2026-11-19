package remote_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/workspace"
)

func newResolver(t *testing.T, srv *httptest.Server, offline bool) *remote.Resolver {
	t.Helper()

	root := t.TempDir()

	return &remote.Resolver{Root: root, LockPath: workspace.LockPathFor(root), Client: srv.Client(), Offline: offline}
}

func TestResolveDownloadsAndCaches(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"a":1}`)) }))
	t.Cleanup(srv.Close)

	r := newResolver(t, srv, false)

	p, code, detail := r.Resolve(context.Background(), srv.URL+"/req.json")
	if p == "" {
		t.Fatalf("%s %s", code, detail)
	}

	sum := sha256.Sum256([]byte(`{"a":1}`))
	if want := filepath.Join(r.Root, ".concordia", "cache", "resources", hex.EncodeToString(sum[:])); p != want {
		t.Errorf("path %s, want %s", p, want)
	}

	if data, err := os.ReadFile(p); err != nil || string(data) != `{"a":1}` {
		t.Errorf("cache content %q %v", data, err)
	}
}

func TestResolveOfflineUsesLockAndCache(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request in offline mode") }))
	t.Cleanup(srv.Close)

	r := newResolver(t, srv, true)

	path, hexSum, err := remote.WriteCache(r.Root, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}

	uri := srv.URL + "/req.json"
	lock := workspace.NewLock("1", "")
	lock.Resources = []workspace.LockResource{{URI: uri, Path: remote.CacheDir + "/" + hexSum, SHA256: "sha256:" + hexSum}}

	if err := workspace.WriteLock(r.LockPath, lock); err != nil {
		t.Fatal(err)
	}

	got, code, detail := r.Resolve(context.Background(), uri)
	if got != path {
		t.Errorf("%q %s %s", got, code, detail)
	}
}

func TestResolveOfflineWithoutEntryGivesINT012(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request in offline mode") }))
	t.Cleanup(srv.Close)

	r := newResolver(t, srv, true)

	p, code, detail := r.Resolve(context.Background(), srv.URL+"/req.json")
	if p != "" || code != diag.INT012 || !strings.Contains(detail, "--offline") {
		t.Errorf("%q %s %s", p, code, detail)
	}
}

func TestResolveServerErrorGivesINT013(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(srv.Close)

	r := newResolver(t, srv, false)

	p, code, _ := r.Resolve(context.Background(), srv.URL+"/req.json")
	if p != "" || code != diag.INT013 {
		t.Errorf("%q %s", p, code)
	}
}

func TestCachePathRejectsBadDigests(t *testing.T) {
	t.Parallel()

	good := strings.Repeat("a", 64)
	if p, ok := remote.CachePath("/r", "sha256:"+good); !ok || filepath.Base(p) != good {
		t.Errorf("good digest: %q %v", p, ok)
	}

	for _, bad := range []string{"", "sha256:", "sha256:../../etc/passwd", "sha256:" + strings.Repeat("A", 64), "sha256:" + good + "0", "../" + good} {
		if p, ok := remote.CachePath("/r", bad); ok {
			t.Errorf("%q accepted as %q", bad, p)
		}
	}
}

func TestResolveIgnoresLockEntryWithBadDigest(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request in offline mode") }))
	t.Cleanup(srv.Close)

	r := newResolver(t, srv, true)
	uri := srv.URL + "/req.json"

	secret := filepath.Join(r.Root, "secret")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	lock := workspace.NewLock("1", "")
	lock.Resources = []workspace.LockResource{{URI: uri, Path: remote.CacheDir + "/x", SHA256: "sha256:../../../secret"}}

	if err := workspace.WriteLock(r.LockPath, lock); err != nil {
		t.Fatal(err)
	}

	p, code, _ := r.Resolve(context.Background(), uri)
	if p != "" || code != diag.INT012 {
		t.Errorf("%q %s", p, code)
	}
}

func TestResolveRejectsUserinfo(t *testing.T) {
	t.Parallel()

	r := &remote.Resolver{Root: t.TempDir(), Offline: false}

	p, code, detail := r.Resolve(context.Background(), "https://user:pw@example.com/a.json")
	if p != "" || code != diag.INT012 || !strings.Contains(detail, "userinfo in URLs is not supported") || strings.Contains(detail, "pw") {
		t.Errorf("%q %s %s", p, code, detail)
	}
}
