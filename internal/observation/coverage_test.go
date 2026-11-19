package observation_test

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

var coverageFiles = []string{
	"services/a/x.ts", "services/a/y.ts", "services/legacy/Old.kt", "packages/p/i.ts", "README.md", "services/a/z.py",
}

func coverageCbmFiles() []string {
	return []string{"services/a/x.ts", "services/a/y.ts", "packages/p/i.ts", "services/legacy/Old.kt", "services/a/z.py"}
}

func coversAllIncludedFiles(t *testing.T) {
	c := observation.ComputeFileCoverage(&observation.FileCoverageInput{
		WorkspaceFiles: coverageFiles,
		Include:        []string{"services/**", "packages/**"},
		CbmFiles:       coverageCbmFiles(),
	})

	obsEqual(t, "total", c.Total, 5)
	obsEqual(t, "covered", c.Covered, 5)
	obsEqual(t, "ratio", c.Ratio, 1)
	obsEqual(t, "languages[.ts]", c.Languages[".ts"], observation.LanguageCoverage{Total: 3, Covered: 3})
}

func excludedFileLowersTheRatio(t *testing.T) {
	c := observation.ComputeFileCoverage(&observation.FileCoverageInput{
		WorkspaceFiles: coverageFiles,
		Include:        []string{"services/**", "packages/**"},
		Exclude:        []string{"services/legacy/**"},
		CbmFiles:       coverageCbmFiles(),
	})

	obsEqual(t, "total", c.Total, 5)
	obsEqual(t, "covered", c.Covered, 4)
	obsRequire(t, math.Abs(c.Ratio-0.8) <= 1e-9, fmt.Sprintf("ratio = %v, want 0.8", c.Ratio))
	obsDeepEqual(t, "excluded", c.Excluded, []string{"services/legacy/Old.kt"})
}

func dirtyFilesAreStale(t *testing.T) {
	c := observation.ComputeFileCoverage(&observation.FileCoverageInput{
		WorkspaceFiles: coverageFiles,
		Include:        []string{"services/**"},
		CbmFiles:       []string{"services/a/x.ts", "services/a/y.ts"},
		DirtyFiles:     []string{"services/a/y.ts"},
	})

	obsEqual(t, "covered", c.Covered, 1)
	obsDeepEqual(t, "stale", c.Stale, []string{"services/a/y.ts"})
	obsDeepEqual(t, "unindexed", c.Unindexed, []string{"services/a/z.py", "services/legacy/Old.kt"})
}

func emptyIncludeAndScopeGlobs(t *testing.T) {
	all := observation.ComputeFileCoverage(&observation.FileCoverageInput{WorkspaceFiles: coverageFiles})
	obsEqual(t, "total", all.Total, 6)

	scoped := observation.ComputeFileCoverage(&observation.FileCoverageInput{
		WorkspaceFiles: coverageFiles,
		CbmFiles:       []string{"packages/p/i.ts"},
		ScopeGlobs:     []string{"packages/**"},
	})

	obsEqual(t, "scoped total", scoped.Total, 1)
	obsEqual(t, "scoped ratio", scoped.Ratio, 1)
}

func emptyScopeGivesRatioOne(t *testing.T) {
	c := observation.ComputeFileCoverage(&observation.FileCoverageInput{
		WorkspaceFiles: coverageFiles,
		Include:        []string{"nothing/**"},
	})

	obsEqual(t, "total", c.Total, 0)
	obsEqual(t, "ratio", c.Ratio, 1)
	obsRequire(t, c.EmptyScope, fmt.Sprintf("coverage = %+v", c))
}

func matchesDotFiles(t *testing.T) {
	c := observation.ComputeFileCoverage(&observation.FileCoverageInput{
		WorkspaceFiles: []string{".github/ci.yml"},
		Include:        []string{"**"},
		CbmFiles:       []string{".github/ci.yml"},
	})

	obsEqual(t, "total", c.Total, 1)
}

func languagesUseTheLowerCaseExtension(t *testing.T) {
	c := observation.ComputeFileCoverage(&observation.FileCoverageInput{
		WorkspaceFiles: []string{"A.TS", "Makefile", ".gitignore", "dir/.env"},
	})

	want := map[string]observation.LanguageCoverage{".ts": {Total: 1}, "(none)": {Total: 3}}
	obsDeepEqual(t, "languages", c.Languages, want)
}

func emptyListsAreNotNil(t *testing.T) {
	c := observation.ComputeFileCoverage(&observation.FileCoverageInput{})

	obsRequire(t, c.Excluded != nil, "excluded is nil")
	obsRequire(t, c.Stale != nil, "stale is nil")
	obsRequire(t, c.Unindexed != nil, "unindexed is nil")
	obsRequire(t, c.Languages != nil, "languages is nil")
}

func TestComputeFileCoverage(t *testing.T) {
	t.Run("counts all included files and covers the indexed ones", coversAllIncludedFiles)
	t.Run("an excluded file stays in the total and makes the ratio lower", excludedFileLowersTheRatio)
	t.Run("dirty files are stale and not covered; missing File nodes are unindexed", dirtyFilesAreStale)
	t.Run("empty include puts all files in scope; scope globs narrow the scope", emptyIncludeAndScopeGlobs)
	t.Run("an empty scope gives ratio 1 and emptyScope", emptyScopeGivesRatioOne)
	t.Run("matches dot files", matchesDotFiles)
	t.Run("languages use the lower-case extension; a dot file or a file without one is (none)", languagesUseTheLowerCaseExtension)
	t.Run("empty lists are not nil, so that JSON gives []", emptyListsAreNotNil)
}

func TestByteCoverageAndUnsupportedFiles(t *testing.T) {
	t.Parallel()

	c := observation.ComputeFileCoverage(&observation.FileCoverageInput{
		WorkspaceFiles: coverageFiles,
		Include:        []string{"services/**", "packages/**"},
		Exclude:        []string{"services/legacy/**"},
		CbmFiles:       []string{"services/a/x.ts", "services/a/y.ts", "packages/p/i.ts"},
		Sizes: map[string]int64{
			"services/a/x.ts": 100, "services/a/y.ts": 50, "services/legacy/Old.kt": 800, "packages/p/i.ts": 50, "services/a/z.py": 200,
		},
	})

	if want := (observation.ByteCoverage{Total: 1200, Covered: 200, Ratio: 200.0 / 1200.0}); c.Bytes != want {
		t.Errorf("bytes = %+v", c.Bytes)
	}

	// z.py is in scope and not excluded, and CBM has no .py File node. Old.kt is excluded.
	if want := []string{"services/a/z.py"}; !reflect.DeepEqual(c.Unsupported, want) {
		t.Errorf("unsupported = %v", c.Unsupported)
	}

	empty := observation.ComputeFileCoverage(&observation.FileCoverageInput{})
	if empty.Bytes.Ratio != 1 || empty.Unsupported == nil {
		t.Errorf("empty coverage = %+v", empty)
	}
}

func TestFileSizesSkipsFilesThatCannotBeRead(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "src", "a.ts"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := observation.FileSizes(root, []string{"src/a.ts", "src/missing.ts", "src"})
	if want := map[string]int64{"src/a.ts": 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("sizes = %v", got)
	}
}
