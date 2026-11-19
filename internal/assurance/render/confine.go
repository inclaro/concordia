package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
)

// Gap kinds of the confinement layers.
const (
	GapPathEscape    = "path-escape"
	GapSymlink       = "symlink"
	GapMissingSource = "missing-source"
	GapSpecialFile   = "special-file"
)

const (
	// dirMode and fileMode are the modes of the confined copy: only the user of Concordia can read it.
	dirMode  = 0o700
	fileMode = 0o600
)

// workdir is the confined directory of one render (design D4 layer 4): the declared inputs are copied into in, the
// renderer runs there, and home is its HOME and cache. Nothing outside it is read by the renderer.
type workdir struct {
	dir  string
	in   string
	home string
}

func newWorkdir() (*workdir, error) {
	dir, err := os.MkdirTemp("", "concordia-render-")
	if err != nil {
		return nil, fmt.Errorf("create render directory: %w", err)
	}

	work := &workdir{dir: dir, in: filepath.Join(dir, "in"), home: filepath.Join(dir, "home")}

	for _, d := range []string{work.in, work.home, filepath.Join(work.home, "tmp"), filepath.Join(work.home, "cache"),
		filepath.Join(work.home, "config"), filepath.Join(work.home, "data")} {
		if err := os.MkdirAll(d, dirMode); err != nil {
			_ = os.RemoveAll(dir)

			return nil, fmt.Errorf("create render directory: %w", err)
		}
	}

	return work, nil
}

func (w *workdir) close() { _ = os.RemoveAll(w.dir) }

// scrub removes the temporary directory from a text, so that no temporary path reaches a diagnostic or a result.
func (w *workdir) scrub(text string) string {
	text = strings.ReplaceAll(text, w.in, ".")

	return strings.ReplaceAll(text, w.dir, "<render>")
}

// copier copies declared paths from the workspace root into a workdir.
type copier struct {
	// files are the workspace-relative files with the digest of their content, the input of the input digest.
	files map[string]string
	root  string
	in    string
	// copied are the workspace-relative paths that were copied whole.
	copied []string
}

func newCopier(root string, w *workdir) *copier {
	return &copier{root: root, in: w.in, files: map[string]string{}}
}

// covered says that rel is inside a path that was copied already.
func (c *copier) covered(rel string) bool {
	for _, p := range c.copied {
		if rel == p || p == "." || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}

	return false
}

// clean checks a declared path: relative, no `..` that leaves the root, "/" separators.
func clean(rel string) (string, *assurance.Gap) {
	cleaned := path.Clean(strings.ReplaceAll(rel, `\`, "/"))

	if path.IsAbs(cleaned) || leavesRoot(cleaned) {
		return "", &assurance.Gap{Kind: GapPathEscape, Detail: "the path " + rel + " leaves the workspace root", Path: rel}
	}

	return cleaned, nil
}

// leavesRoot says whether a cleaned relative path climbs above the directory it is relative to.
func leavesRoot(cleaned string) bool {
	const parent = ".."

	return cleaned == parent || strings.HasPrefix(cleaned, parent+"/")
}

// copy copies the file or directory rel into the workdir at the same relative place. It fails with a gap, and
// copies nothing more, when the path leaves the root, does not exist, or holds a symbolic link anywhere (also on
// the way from the root). A symbolic link is never followed and its target is never read.
func (c *copier) copy(rel string) *assurance.Gap {
	cleaned, gap := clean(rel)
	if gap != nil {
		return gap
	}

	if c.covered(cleaned) {
		return nil
	}

	// Walk the components from the root with Lstat: a link in any component is a link out of the declared paths.
	cur := c.root

	if cleaned != "." {
		for _, part := range strings.Split(cleaned, "/") {
			cur = filepath.Join(cur, part)

			info, err := os.Lstat(cur)

			switch {
			case err != nil:
				return &assurance.Gap{Kind: GapMissingSource, Detail: "the declared path " + cleaned + " does not exist", Path: cleaned}
			case info.Mode()&fs.ModeSymlink != 0:
				return &assurance.Gap{Kind: GapSymlink, Detail: "the path " + cleaned + " is or contains a symbolic link, which is not followed", Path: cleaned}
			}
		}
	}

	if err := c.copyTree(cur, cleaned); err != nil {
		return err
	}

	c.copied = append(c.copied, cleaned)

	return nil
}

func (c *copier) copyTree(src, rel string) *assurance.Gap {
	info, err := os.Lstat(src)
	if err != nil {
		return &assurance.Gap{Kind: GapMissingSource, Detail: "the declared path " + rel + " does not exist", Path: rel}
	}

	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return &assurance.Gap{Kind: GapSymlink, Detail: "the path " + rel + " is a symbolic link, which is not followed", Path: rel}
	case info.IsDir():
		return c.copyDir(src, rel)
	case info.Mode().IsRegular():
		return c.copyFile(src, rel)
	default:
		return &assurance.Gap{Kind: GapSpecialFile, Detail: "the path " + rel + " is not a regular file or directory", Path: rel}
	}
}

func (c *copier) copyDir(src, rel string) *assurance.Gap {
	entries, err := os.ReadDir(src)
	if err != nil {
		return &assurance.Gap{Kind: GapMissingSource, Detail: "the directory " + rel + " cannot be read: " + err.Error(), Path: rel}
	}

	if err := os.MkdirAll(filepath.Join(c.in, filepath.FromSlash(rel)), dirMode); err != nil {
		return &assurance.Gap{Kind: GapMissingSource, Detail: err.Error(), Path: rel}
	}

	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}

		if gap := c.copyTree(filepath.Join(src, entry.Name()), path.Join(rel, entry.Name())); gap != nil {
			return gap
		}
	}

	return nil
}

// copyFile copies one regular file and records the digest of its content. src is below the declared path and was
// checked with Lstat on every component, so no link is followed.
func (c *copier) copyFile(src, rel string) *assurance.Gap {
	data, err := os.ReadFile(src) //nolint:gosec // G304: src is below the workspace root and holds no symbolic link (Lstat above).
	if err != nil {
		return &assurance.Gap{Kind: GapMissingSource, Detail: "the file " + rel + " cannot be read: " + err.Error(), Path: rel}
	}

	dest := filepath.Join(c.in, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), dirMode); err != nil {
		return &assurance.Gap{Kind: GapMissingSource, Detail: err.Error(), Path: rel}
	}

	if err := os.WriteFile(dest, data, fileMode); err != nil { //nolint:gosec // G703: dest is below the temporary directory of this render.
		return &assurance.Gap{Kind: GapMissingSource, Detail: err.Error(), Path: rel}
	}

	c.files[rel] = digest.OfBytes(data)

	return nil
}

// inputDigest is the digest of the copied files that include accepts (path and content digest, sorted; nil accepts
// all) and of the settings that change the output: tool, release, namespace, and the values files in order.
func (c *copier) inputDigest(include func(string) bool, settings ...string) string {
	paths := make([]string, 0, len(c.files))

	for p := range c.files {
		if include == nil || include(p) {
			paths = append(paths, p)
		}
	}

	slices.SortFunc(paths, canonjson.CompareUTF16)

	h := sha256.New()

	for _, s := range settings {
		_, _ = fmt.Fprintf(h, "setting\x00%s\n", s)
	}

	for _, p := range paths {
		_, _ = fmt.Fprintf(h, "file\x00%s\x00%s\n", p, c.files[p])
	}

	return digest.Prefix + hex.EncodeToString(h.Sum(nil))
}
