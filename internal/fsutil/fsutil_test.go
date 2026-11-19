package fsutil_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/fsutil"
)

func TestWriteFileAtomicWritesCreatesParentsAndLeavesNoTemporaryFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "a", "b", "out.json")

	for _, content := range []string{"one", "two"} {
		if err := fsutil.WriteFileAtomic(target, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "two" {
		t.Errorf("content %q", got)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "a", "b"))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 || entries[0].Name() != "out.json" {
		t.Errorf("entries %v", entries)
	}
}

func TestWriteFileAtomicFailsWhenParentIsAFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	blocker := filepath.Join(dir, "f")

	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fsutil.WriteFileAtomic(filepath.Join(blocker, "out"), []byte("x")); err == nil {
		t.Error("expected an error")
	}
}

func TestToPosixRelativeUsesForwardSlashes(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "r")

	got, err := fsutil.ToPosixRelative(root, filepath.Join(root, "a", "b.ts"))
	if err != nil {
		t.Fatal(err)
	}

	if got != "a/b.ts" {
		t.Errorf("got %q", got)
	}
}

func TestToPosix(t *testing.T) {
	t.Parallel()

	if got := fsutil.ToPosix(`a\b/c`); got != "a/b/c" {
		t.Errorf("got %q", got)
	}
}

func TestGlobMatcherIncludesDotFiles(t *testing.T) {
	t.Parallel()

	m := fsutil.GlobMatcher([]string{"services/**"})

	if !m("services/a/.hidden.ts") {
		t.Error("services/a/.hidden.ts must match")
	}

	if m("packages/a.ts") {
		t.Error("packages/a.ts must not match")
	}

	if fsutil.GlobMatcher(nil)("x") {
		t.Error("an empty list must match nothing")
	}
}

func TestGlobMatcherDetails(t *testing.T) {
	t.Parallel()

	m := fsutil.GlobMatcher([]string{"src/**/*.ts", "./*.md", "{a,b}/x.go", "[bad"})
	cases := map[string]bool{
		"src/a/b/c.ts":   true,
		"src/c.ts":       true,
		"src/a/b/c.js":   false,
		"README.md":      true,
		"docs/README.md": false,
		"a/x.go":         true,
		`src\win\a.ts`:   true,
		"c/x.go":         false,
		"./README.md":    true,
		".hidden/a.ts":   false,
		"src/.d/e.ts":    true,
	}

	for p, want := range cases {
		if got := m(p); got != want {
			t.Errorf("match(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestWriteFileAtomicKeepsTheModeOfAnExistingFile(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fsutil.WriteFileAtomic(target, []byte("new")); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}

	// Windows has no Unix permission bits: only check where they exist.
	if info.Mode().Perm()&0o077 != 0 && info.Mode().Perm() != 0o666 {
		t.Errorf("mode = %v, want the old 0600", info.Mode().Perm())
	}
}

func TestConfineResolvesAFileBelowTheRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "b.txt"), "x")

	got, err := fsutil.Confine(root, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}

	want, err := filepath.EvalSymlinks(filepath.Join(root, "a", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConfineRefusesPathsThatLeaveTheRoot(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	writeFile(t, filepath.Join(root, "ok.txt"), "x")
	writeFile(t, filepath.Join(base, "outside.txt"), "secret")

	if err := os.Symlink(filepath.Join(base, "outside.txt"), filepath.Join(root, "link-file")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}

	if err := os.Symlink(base, filepath.Join(root, "link-dir")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("ok.txt", filepath.Join(root, "link-inside")); err != nil {
		t.Fatal(err)
	}

	refused := []string{
		"../outside.txt", "../../etc/passwd", "a/../../outside.txt", "/etc/passwd", `\etc\passwd`, `..\outside.txt`,
		filepath.Join(base, "outside.txt"), "link-file", "link-dir/outside.txt", "", "ok.txt\x00",
	}

	for _, rel := range refused {
		if got, err := fsutil.Confine(root, rel); !errors.Is(err, fsutil.ErrOutsideRoot) {
			t.Errorf("Confine(%q) = %q, %v: want ErrOutsideRoot", rel, got, err)
		}
	}

	if _, err := fsutil.Confine(root, "link-inside"); err != nil {
		t.Errorf("a link that stays inside the root is refused: %v", err)
	}
}

func TestConfineReportsAMissingFileAsMissing(t *testing.T) {
	t.Parallel()

	_, err := fsutil.Confine(t.TempDir(), "nothing.txt")
	if !errors.Is(err, fs.ErrNotExist) || errors.Is(err, fsutil.ErrOutsideRoot) {
		t.Errorf("err = %v, want a not-exist error", err)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()

	if err := fsutil.WriteFileAtomic(path, []byte(text)); err != nil {
		t.Fatal(err)
	}
}
