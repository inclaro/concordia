package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/procexec"
)

const (
	gitTimeout = 120 * time.Second
	// statusMinEntry is the shortest "XY path" entry of git status --porcelain.
	statusMinEntry = 4
	// statusPathStart is the index of the path in a porcelain entry: "XY " comes first.
	statusPathStart = 3
	deletedMarker   = "deleted"
	revParse        = "rev-parse"
	gitHead         = "HEAD"
	gitNulFlag      = "-z"
	// maxFileBuffer is the output limit of git for file contents.
	maxFileBuffer = 64 << 20
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

// skipDirs are the directories that the file walk outside a repository does not enter.
var skipDirs = map[string]bool{".git": true, "node_modules": true, ".concordia": true}

// GitPort is the git state of the workspace root. Paths are relative to the
// workspace root and use "/".
type GitPort interface {
	// Revision is the HEAD commit. It gives "" when the directory is not in a git repository or has no commit.
	// It gives a CO-WRK-012 error when git is missing or fails.
	Revision(ctx context.Context) (string, error)
	// DirtyFiles gives changed, staged and untracked files, sorted.
	DirtyFiles(ctx context.Context) ([]string, error)
	// ListFiles gives tracked and untracked-but-not-ignored files, sorted.
	ListFiles(ctx context.Context) ([]string, error)
	// ResolveRef gives the commit SHA of ref. It gives CO-WRK-015 when ref is not a commit.
	ResolveRef(ctx context.Context, ref string) (string, error)
	// ChangedFiles gives the files that changed since the merge base of ref and HEAD,
	// with the working-tree, staged and untracked changes.
	ChangedFiles(ctx context.Context, ref string) (ChangeSet, error)
	// ShowFile gives the content of path at rev. The bool is false when path is not in rev.
	ShowFile(ctx context.Context, rev, path string) ([]byte, bool, error)
	// ListFilesAt gives the files of rev below the GitPort root, as sorted "/" relative paths.
	ListFilesAt(ctx context.Context, rev string) ([]string, error)
	// IsIgnored says, for each path, whether a Git ignore rule matches it. The result has an entry for
	// every path. A tracked path is not ignored. Outside a repository no path is ignored.
	IsIgnored(ctx context.Context, paths []string) (map[string]bool, error)
	// IsAncestor says whether ancestor is an ancestor of descendant, or the same commit. It gives false when it
	// is not and an error (CO-WRK-012 or CO-WRK-015) when git fails or a reference is unknown.
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
}

// ChangeSet is the result of GitPort.ChangedFiles.
type ChangeSet struct {
	// Ref is the reference that the caller gave. "HEAD" means the working-tree changes.
	Ref string
	// Base is the commit SHA that the files are compared with.
	Base string
	// Files are "/" paths relative to the GitPort root. They are sorted and unique.
	Files []string
}

// CliGit is the real git, through the git executable. The directory Root is
// the workspace root (working directory of each git call).
type CliGit struct {
	root string
}

// NewCliGit makes a CliGit for the workspace root (an OS path).
func NewCliGit(root string) *CliGit {
	return &CliGit{root: root}
}

// git runs git. It gives a CO-WRK-012 error when git is missing, cannot run or times out.
func (g *CliGit) git(ctx context.Context, args ...string) (procexec.Result, error) {
	return g.run(ctx, 0, args)
}

// gitBig is git with a larger output limit, for file contents.
func (g *CliGit) gitBig(ctx context.Context, args ...string) (procexec.Result, error) {
	return g.run(ctx, maxFileBuffer, args)
}

func (g *CliGit) run(ctx context.Context, maxBuffer int, args []string) (procexec.Result, error) {
	res, err := procexec.Run(ctx, "git", args, procexec.Options{Dir: g.root, Timeout: gitTimeout, MaxBuffer: maxBuffer})
	if err != nil {
		var spawn *procexec.SpawnError
		if errors.As(err, &spawn) {
			return procexec.Result{}, diag.Fail(diag.WRK012, diag.Detail("git executable not found"))
		}

		return procexec.Result{}, diag.Fail(diag.WRK012, diag.Detail(fmt.Sprintf("git %s: %v", strings.Join(args, " "), err)))
	}

	if res.TimedOut {
		return procexec.Result{}, diag.Fail(diag.WRK012, diag.Detail("git "+strings.Join(args, " ")+": timed out"))
	}

	return res, nil
}

// gitFailed is the CO-WRK-012 error of a git command that exited with a non-zero code.
func gitFailed(args []string, res procexec.Result) error {
	return diag.Fail(diag.WRK012, diag.Detail(fmt.Sprintf("git %s: exit %d: %s", strings.Join(args, " "), res.Code, strings.TrimSpace(res.Stderr))))
}

// Revision implements GitPort.
func (g *CliGit) Revision(ctx context.Context) (string, error) {
	repo, err := g.isRepo(ctx)
	if err != nil || !repo {
		return "", err
	}

	args := []string{revParse, "--verify", "--quiet", gitHead}

	res, err := g.git(ctx, args...)
	if err != nil {
		return "", err
	}

	if res.Code == 0 {
		sha := strings.TrimSpace(res.Stdout)
		if !shaPattern.MatchString(sha) {
			return "", gitFailed(args, res)
		}

		return sha, nil
	}

	// --verify --quiet exits non-zero without output when HEAD does not exist: a repository without commit.
	if strings.TrimSpace(res.Stdout) == "" && strings.TrimSpace(res.Stderr) == "" {
		return "", nil
	}

	return "", gitFailed(args, res)
}

// isRepo says whether the directory is inside a git work tree. Git's "not a git repository"
// answer means "no"; any other non-zero exit (for example unsafe ownership) is a CO-WRK-012 error.
func (g *CliGit) isRepo(ctx context.Context) (bool, error) {
	args := []string{revParse, "--is-inside-work-tree"}

	res, err := g.git(ctx, args...)
	if err != nil {
		return false, err
	}

	if res.Code != 0 {
		if strings.Contains(res.Stderr, "not a git repository") {
			return false, nil
		}

		return false, gitFailed(args, res)
	}

	return strings.TrimSpace(res.Stdout) == "true", nil
}

func (g *CliGit) prefix(ctx context.Context) (string, error) {
	args := []string{revParse, "--show-prefix"}

	res, err := g.git(ctx, args...)
	if err != nil {
		return "", err
	}

	if res.Code != 0 {
		return "", gitFailed(args, res)
	}

	return strings.TrimSpace(res.Stdout), nil
}

// DirtyFiles implements GitPort.
func (g *CliGit) DirtyFiles(ctx context.Context) ([]string, error) {
	repo, err := g.isRepo(ctx)
	if err != nil {
		return nil, err
	}

	if !repo {
		return []string{}, nil
	}

	args := []string{"status", "--porcelain=v1", gitNulFlag, "--untracked-files=all"}

	res, err := g.git(ctx, args...)
	if err != nil {
		return nil, err
	}

	if res.Code != 0 {
		return nil, gitFailed(args, res)
	}

	prefix, err := g.prefix(ctx)
	if err != nil {
		return nil, err
	}

	return parseStatus(res.Stdout, prefix), nil
}

// parseStatus reads the output of git status --porcelain=v1 -z. It gives the
// paths below prefix (a directory with a final "/", or ""), relative to prefix.
func parseStatus(out, prefix string) []string {
	parts := strings.Split(out, "\x00")
	seen := make(map[string]struct{})

	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if len(entry) < statusMinEntry {
			continue
		}

		if isRenameOrCopy(entry) {
			i++ // the next entry is the origin path
		}

		if path, found := relativeToPrefix(entry[statusPathStart:], prefix); found {
			seen[path] = struct{}{}
		}
	}

	return sortedKeys(seen)
}

// isRenameOrCopy says whether the porcelain entry has R or C as its index or work-tree status.
func isRenameOrCopy(entry string) bool {
	return strings.ContainsAny(entry[:statusPathStart-1], "RC")
}

// relativeToPrefix gives path relative to prefix. The bool is false when path is not below prefix.
func relativeToPrefix(path, prefix string) (string, bool) {
	if prefix == "" {
		return path, true
	}

	return strings.CutPrefix(path, prefix)
}

// ListFiles implements GitPort.
func (g *CliGit) ListFiles(ctx context.Context) ([]string, error) {
	repo, err := g.isRepo(ctx)
	if err != nil {
		return nil, err
	}

	if !repo {
		return walkFiles(g.root)
	}

	args := []string{"ls-files", "--cached", "--others", "--exclude-standard", gitNulFlag}

	res, err := g.git(ctx, args...)
	if err != nil {
		return nil, err
	}

	if res.Code != 0 {
		return nil, gitFailed(args, res)
	}

	// Tracked files that are deleted in the working tree stay listed by --cached: drop missing files.
	seen := make(map[string]struct{})

	for _, f := range strings.Split(res.Stdout, "\x00") {
		if f == "" {
			continue
		}

		if _, statErr := os.Stat(filepath.Join(g.root, filepath.FromSlash(f))); statErr == nil {
			seen[f] = struct{}{}
		}
	}

	return sortedKeys(seen), nil
}

// refCheck rejects an option-like reference and a directory outside a repository.
func (g *CliGit) refCheck(ctx context.Context, ref string) error {
	if strings.HasPrefix(ref, "-") {
		return diag.Fail(diag.WRK015, diag.Detail("'"+ref+"' is not a git reference"))
	}

	repo, err := g.isRepo(ctx)
	if err != nil {
		return err
	}

	if !repo {
		return diag.Fail(diag.WRK015, diag.Detail("the workspace is not in a git repository"))
	}

	return nil
}

// ResolveRef implements GitPort.
func (g *CliGit) ResolveRef(ctx context.Context, ref string) (string, error) {
	if err := g.refCheck(ctx, ref); err != nil {
		return "", err
	}

	args := []string{revParse, "--verify", "--quiet", ref + "^{commit}"}

	res, err := g.git(ctx, args...)
	if err != nil {
		return "", err
	}

	if res.Code != 0 {
		return "", diag.Fail(diag.WRK015, diag.Detail("unknown git reference '"+ref+"'"))
	}

	sha := strings.TrimSpace(res.Stdout)
	if !shaPattern.MatchString(sha) {
		return "", gitFailed(args, res)
	}

	return sha, nil
}

// ChangedFiles implements GitPort.
func (g *CliGit) ChangedFiles(ctx context.Context, ref string) (ChangeSet, error) {
	base, err := g.changeBase(ctx, ref)
	if err != nil {
		return ChangeSet{}, err
	}

	seen := make(map[string]struct{})

	for _, args := range [][]string{
		{"diff", "--name-only", gitNulFlag, "--no-renames", "--relative", base},
		{"ls-files", "--others", "--exclude-standard", gitNulFlag},
	} {
		res, runErr := g.git(ctx, args...)
		if runErr != nil {
			return ChangeSet{}, runErr
		}

		if res.Code != 0 {
			return ChangeSet{}, gitFailed(args, res)
		}

		for _, f := range strings.Split(res.Stdout, "\x00") {
			if f != "" {
				seen[f] = struct{}{}
			}
		}
	}

	return ChangeSet{Ref: ref, Base: base, Files: sortedKeys(seen)}, nil
}

// changeBase gives the commit that ChangedFiles compares with.
func (g *CliGit) changeBase(ctx context.Context, ref string) (string, error) {
	if ref == gitHead {
		if err := g.refCheck(ctx, ref); err != nil {
			return "", err
		}

		sha, err := g.ResolveRef(ctx, ref)
		if err != nil {
			return "", diag.Fail(diag.WRK015, diag.Detail("the repository has no commit"))
		}

		return sha, nil
	}

	sha, err := g.ResolveRef(ctx, ref)
	if err != nil {
		return "", err
	}

	args := []string{"merge-base", sha, gitHead}

	res, err := g.git(ctx, args...)
	if err != nil {
		return "", err
	}

	switch res.Code {
	case 0:
		return strings.TrimSpace(res.Stdout), nil
	case 1:
		return "", diag.Fail(diag.WRK015, diag.Detail("no merge base for '"+ref+"' and HEAD"))
	default:
		return "", gitFailed(args, res)
	}
}

// ShowFile implements GitPort.
func (g *CliGit) ShowFile(ctx context.Context, rev, path string) (content []byte, found bool, err error) {
	if refErr := g.refCheck(ctx, rev); refErr != nil {
		return nil, false, refErr
	}

	spec := rev + ":./" + path

	res, err := g.git(ctx, "cat-file", "-e", spec)
	if err != nil {
		return nil, false, err
	}

	if res.Code != 0 {
		return nil, false, nil
	}

	args := []string{"cat-file", "blob", spec}

	res, err = g.gitBig(ctx, args...)
	if err != nil {
		return nil, false, err
	}

	if res.Code != 0 {
		return nil, false, gitFailed(args, res)
	}

	return []byte(res.Stdout), true, nil
}

// ListFilesAt implements GitPort.
func (g *CliGit) ListFilesAt(ctx context.Context, rev string) ([]string, error) {
	if err := g.refCheck(ctx, rev); err != nil {
		return nil, err
	}

	args := []string{"ls-tree", "-r", "--name-only", gitNulFlag, rev, "--", "."}

	res, err := g.git(ctx, args...)
	if err != nil {
		return nil, err
	}

	if res.Code != 0 {
		return nil, gitFailed(args, res)
	}

	out := []string{}

	for _, f := range strings.Split(res.Stdout, "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out, nil
}

// IsAncestor implements GitPort. `git merge-base --is-ancestor` exits 0 for an ancestor (or the same commit),
// 1 for a non-ancestor; every other exit (for example an unknown reference) is an error.
func (g *CliGit) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	for _, ref := range []string{ancestor, descendant} {
		if err := g.refCheck(ctx, ref); err != nil {
			return false, err
		}
	}

	args := []string{"merge-base", "--is-ancestor", ancestor, descendant}

	res, err := g.git(ctx, args...)
	if err != nil {
		return false, err
	}

	switch res.Code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, gitFailed(args, res)
	}
}

// IsIgnored implements GitPort.
func (g *CliGit) IsIgnored(ctx context.Context, paths []string) (map[string]bool, error) {
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[p] = false
	}

	if len(paths) == 0 {
		return out, nil
	}

	repo, err := g.isRepo(ctx)
	if err != nil || !repo {
		return out, err
	}

	// One call for each path: the exit code (0 ignored, 1 not ignored) is exact, while the output of
	// several paths would need quoting rules. A workspace declares few view outputs.
	for _, p := range paths {
		ignored, err := g.isPathIgnored(ctx, p)
		if err != nil {
			return nil, err
		}

		out[p] = ignored
	}

	return out, nil
}

// isPathIgnored asks git whether one path is ignored. The exit code (0 ignored, 1 not ignored) is exact.
func (g *CliGit) isPathIgnored(ctx context.Context, p string) (bool, error) {
	args := []string{"check-ignore", "-q", "--", p}

	res, err := g.git(ctx, args...)
	if err != nil {
		return false, err
	}

	switch res.Code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, gitFailed(args, res)
	}
}

// FakeGit is a test double with fixed state. An empty Rev means "no revision".
type FakeGit struct {
	// Ancestors maps {ancestor, descendant} to the result of IsAncestor. The same two revisions are always
	// ancestors; a pair that is not listed is not.
	Ancestors map[[2]string]bool
	// Refs maps a reference to its commit SHA.
	Refs map[string]string
	// FilesAt maps a rev and then a path to the file content.
	FilesAt map[string]map[string]string
	// ChangedErr is the error of ChangedFiles.
	ChangedErr error
	// AncestorErr is the error of IsAncestor.
	AncestorErr error
	// Ignored is the set of paths that IsIgnored reports as ignored.
	Ignored map[string]bool
	Rev     string
	Files   []string
	Dirty   []string
	// Changed is the result of ChangedFiles.
	Changed ChangeSet
}

// Revision implements GitPort.
func (f *FakeGit) Revision(context.Context) (string, error) { return f.Rev, nil }

// DirtyFiles implements GitPort.
func (f *FakeGit) DirtyFiles(context.Context) ([]string, error) { return sortedCopy(f.Dirty), nil }

// ListFiles implements GitPort.
func (f *FakeGit) ListFiles(context.Context) ([]string, error) { return sortedCopy(f.Files), nil }

// ResolveRef implements GitPort.
func (f *FakeGit) ResolveRef(_ context.Context, ref string) (string, error) {
	if sha, ok := f.Refs[ref]; ok {
		return sha, nil
	}

	return "", diag.Fail(diag.WRK015, diag.Detail("unknown git reference '"+ref+"'"))
}

// ChangedFiles implements GitPort.
func (f *FakeGit) ChangedFiles(context.Context, string) (ChangeSet, error) {
	return f.Changed, f.ChangedErr
}

// ShowFile implements GitPort.
func (f *FakeGit) ShowFile(_ context.Context, rev, path string) (content []byte, found bool, err error) {
	text, ok := f.FilesAt[rev][path]
	if !ok {
		return nil, false, nil
	}

	return []byte(text), true, nil
}

// ListFilesAt implements GitPort.
func (f *FakeGit) ListFilesAt(_ context.Context, rev string) ([]string, error) {
	out := make([]string, 0, len(f.FilesAt[rev]))
	for p := range f.FilesAt[rev] {
		out = append(out, p)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out, nil
}

// IsAncestor implements GitPort.
func (f *FakeGit) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	if f.AncestorErr != nil {
		return false, f.AncestorErr
	}

	return ancestor == descendant || f.Ancestors[[2]string{ancestor, descendant}], nil
}

// IsIgnored implements GitPort.
func (f *FakeGit) IsIgnored(_ context.Context, paths []string) (map[string]bool, error) {
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[p] = f.Ignored[p]
	}

	return out, nil
}

func sortedCopy(list []string) []string {
	out := slices.Clone(list)
	if out == nil {
		out = []string{}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// walkFiles lists the regular files below root (skipping .git, node_modules
// and .concordia), as "/" paths relative to root, sorted.
func walkFiles(root string) ([]string, error) {
	out := []string{}

	if err := walkDir(root, "", &out); err != nil {
		return nil, err
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out, nil
}

func walkDir(root, rel string, out *[]string) error {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read directory %s: %w", rel, err)
	}

	for _, entry := range entries {
		child := entry.Name()
		if rel != "" {
			child = rel + "/" + entry.Name()
		}

		switch {
		case entry.IsDir():
			if skipDirs[entry.Name()] {
				continue
			}

			if err := walkDir(root, child, out); err != nil {
				return err
			}
		case entry.Type().IsRegular():
			*out = append(*out, child)
		}
	}

	return nil
}

// DirtyTreeDigest gives the sha256 of the canonical JSON of the sorted pairs
// [path, fileDigest | "deleted"]. root is an OS path, dirty are "/" paths.
func DirtyTreeDigest(root string, dirty []string) (string, error) {
	sorted := sortedCopy(dirty)
	pairs := make([][2]string, 0, len(sorted))

	for _, p := range sorted {
		d, err := digest.OfFile(filepath.Join(root, filepath.FromSlash(p)))
		if errors.Is(err, fs.ErrNotExist) {
			d = deletedMarker
		} else if err != nil {
			return "", fmt.Errorf("dirty tree digest: %w", err)
		}

		pairs = append(pairs, [2]string{p, d})
	}

	text, err := canonjson.Marshal(pairs)
	if err != nil {
		return "", fmt.Errorf("dirty tree digest: %w", err)
	}

	return digest.OfBytes(text), nil
}
