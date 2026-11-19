package workspace_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

func equalList(t *testing.T, got, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCliGitMissingExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := workspace.NewCliGit(t.TempDir()).Revision(t.Context())

	var de *diag.Error
	if !errors.As(err, &de) || de.Diagnostic.Code != "CO-WRK-012" || de.ExitCode != diag.Toolchain {
		t.Fatalf("err = %v", err)
	}
}

func TestCliGitRepositoryWithoutCommit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")

	rev, err := workspace.NewCliGit(dir).Revision(t.Context())
	if err != nil || rev != "" {
		t.Fatalf("revision %q, err %v", rev, err)
	}
}

func TestCliGitOutsideRepository(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"a/b.ts": "x", "node_modules/m/i.js": "y", ".concordia/runs/r/manifest.json": "{}"})

	g := workspace.NewCliGit(dir)

	// The temporary directory is below the system temporary directory; a parent repository is not expected.
	rev, err := g.Revision(t.Context())
	if err != nil || rev != "" {
		t.Fatalf("revision %q, err %v", rev, err)
	}

	files, err := g.ListFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	equalList(t, files, []string{"a/b.ts"})

	dirty, err := g.DirtyFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	equalList(t, dirty, []string{})
}

func TestCliGitInRepositoryAlsoInSubdirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"ws/concordia.yaml": "k: v", "ws/src/a.ts": "1", "other.txt": "o", ".gitignore": "ignored.txt\n",
	})
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")

	g := workspace.NewCliGit(filepath.Join(dir, "ws"))

	rev, err := g.Revision(t.Context())
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(rev) {
		t.Fatalf("revision %q, err %v", rev, err)
	}

	dirty, err := g.DirtyFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	equalList(t, dirty, []string{})

	write(t, filepath.Join(dir, "ws", "src", "a.ts"), "2")
	write(t, filepath.Join(dir, "ws", "src", "new.ts"), "n")
	write(t, filepath.Join(dir, "ws", "ignored.txt"), "i")
	write(t, filepath.Join(dir, "other.txt"), "changed outside the workspace")

	dirty, err = g.DirtyFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	equalList(t, dirty, []string{srcA, srcNew})

	files, err := g.ListFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	equalList(t, files, []string{workspaceFile, srcA, srcNew})

	if err := os.Remove(filepath.Join(dir, "ws", "src", "a.ts")); err != nil {
		t.Fatal(err)
	}

	files, err = g.ListFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	equalList(t, files, []string{workspaceFile, srcNew})

	dirty, err = g.DirtyFiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	equalList(t, dirty, []string{srcA, srcNew})
}

func TestCliGitChangedFilesSinceMergeBase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"a.txt": "1", "b.txt": "1"})
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	testutil.Git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, filepath.Join(dir, "a.txt"), "2")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "change a")
	write(t, filepath.Join(dir, "b.txt"), "2")
	write(t, filepath.Join(dir, "c.txt"), "new")

	g := workspace.NewCliGit(dir)

	set, err := g.ChangedFiles(t.Context(), "main")
	if err != nil {
		t.Fatal(err)
	}

	equalList(t, set.Files, []string{"a.txt", "b.txt", "c.txt"})

	if set.Ref != "main" || set.Base == "" {
		t.Errorf("set %+v", set)
	}

	head, err := g.ChangedFiles(t.Context(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	equalList(t, head.Files, []string{"b.txt", "c.txt"})

	_, err = g.ChangedFiles(t.Context(), "nope")

	var de *diag.Error
	if !errors.As(err, &de) || de.Diagnostic.Code != diag.WRK015 {
		t.Fatalf("err %v", err)
	}
}

func TestDirtyTreeDigestDependsOnContentAndDeletionNotOrder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"a.txt": "1", "b.txt": "2"})

	digestOf := func(dirty ...string) string {
		t.Helper()

		d, err := workspace.DirtyTreeDigest(dir, dirty)
		if err != nil {
			t.Fatal(err)
		}

		return d
	}

	d1 := digestOf("a.txt", "b.txt", "gone.txt")

	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(d1) {
		t.Fatalf("digest %q", d1)
	}

	if got := digestOf("gone.txt", "b.txt", "a.txt"); got != d1 {
		t.Fatalf("order changed the digest: %s != %s", got, d1)
	}

	write(t, filepath.Join(dir, "a.txt"), "changed")

	if got := digestOf("a.txt", "b.txt", "gone.txt"); got == d1 {
		t.Fatal("content change did not change the digest")
	}
}

func TestFakeGitServesFixedState(t *testing.T) {
	t.Parallel()

	var port workspace.GitPort = &workspace.FakeGit{Rev: "abc", Files: []string{"b", "a"}, Dirty: []string{"z", "y"}}

	rev, _ := port.Revision(t.Context())
	if rev != "abc" {
		t.Fatalf("revision %q", rev)
	}

	files, _ := port.ListFiles(t.Context())
	equalList(t, files, []string{"a", "b"})

	dirty, _ := port.DirtyFiles(t.Context())
	equalList(t, dirty, []string{"y", "z"})
}

func TestCliGitIsAncestor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"a.txt": "1"})
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
	testutil.Git(t, dir, "branch", "base")
	write(t, filepath.Join(dir, "a.txt"), "2")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "second")
	testutil.Git(t, dir, "checkout", "-q", "-b", "side", "base")
	write(t, filepath.Join(dir, "b.txt"), "side")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "side")

	g := workspace.NewCliGit(dir)

	cases := []struct {
		name, ancestor, descendant string
		want                       bool
	}{
		{"ancestor", "base", "main", true},
		{"equal revision", "main", "main", true},
		{"not an ancestor (descendant first)", "main", "base", false},
		{"diverged branches", "main", "side", false},
	}

	for _, tc := range cases {
		got, err := g.IsAncestor(t.Context(), tc.ancestor, tc.descendant)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, err %v, want %v", tc.name, got, err, tc.want)
		}
	}

	// An unknown reference is an error, not "false".
	got, err := g.IsAncestor(t.Context(), "nope", "main")
	if err == nil || got {
		t.Errorf("unknown ancestor: got %v, err %v", got, err)
	}

	if _, err = g.IsAncestor(t.Context(), "main", "nope"); err == nil {
		t.Error("unknown descendant: no error")
	}

	if _, err = g.IsAncestor(t.Context(), "--all", "main"); err == nil {
		t.Error("option-like reference: no error")
	}
}

func TestFakeGitIsAncestor(t *testing.T) {
	t.Parallel()

	fake := &workspace.FakeGit{Ancestors: map[[2]string]bool{{"a", "b"}: true}}

	for _, tc := range []struct {
		ancestor, descendant string
		want                 bool
	}{{"a", "b", true}, {"b", "a", false}, {"c", "c", true}, {"a", "c", false}} {
		if got, err := fake.IsAncestor(t.Context(), tc.ancestor, tc.descendant); err != nil || got != tc.want {
			t.Errorf("IsAncestor(%s, %s) = %v, %v; want %v", tc.ancestor, tc.descendant, got, err, tc.want)
		}
	}

	boom := errors.New("boom")
	fake.AncestorErr = boom

	if _, err := fake.IsAncestor(t.Context(), "a", "b"); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestCliGitIsIgnored(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"ws/concordia.yaml": "k: v", "ws/.gitignore": "build/concordia/\n*.gen.dsl\n",
		"ws/docs/kept.gen.dsl": "tracked although it matches", "ws/docs/plain.dsl": "p",
	})
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "add", "-f", "ws/docs/kept.gen.dsl")
	testutil.Git(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")

	g := workspace.NewCliGit(filepath.Join(dir, "ws"))
	paths := []string{"build/concordia/views/container.dsl", "docs/kept.gen.dsl", "docs/new.gen.dsl", "docs/plain.dsl"}

	got, err := g.IsIgnored(t.Context(), paths)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		"build/concordia/views/container.dsl": true,
		"docs/kept.gen.dsl":                   false,
		"docs/new.gen.dsl":                    true,
		"docs/plain.dsl":                      false,
	}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for p, w := range want {
		if got[p] != w {
			t.Errorf("%s: ignored %v, want %v", p, got[p], w)
		}
	}

	none, err := workspace.NewCliGit(t.TempDir()).IsIgnored(t.Context(), []string{"a"})
	if err != nil || none["a"] {
		t.Fatalf("outside a repository: %v, %v", none, err)
	}
}
