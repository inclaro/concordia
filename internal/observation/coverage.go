package observation

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/fsutil"
)

// FileCoverageInput is the input of ComputeFileCoverage.
type FileCoverageInput struct {
	// WorkspaceFiles are the files of the workspace (git list of files).
	WorkspaceFiles []string
	Include        []string
	Exclude        []string
	// CbmFiles are the file paths of the CBM File nodes.
	CbmFiles   []string
	DirtyFiles []string
	// Sizes are the sizes in bytes of the workspace files, by "/" path. A file without
	// an entry has the size 0.
	Sizes map[string]int64
	// ScopeGlobs, when not nil, keeps only the files that also match one of
	// these globs. An empty, non-nil list keeps no file.
	ScopeGlobs []string
}

const noExtension = "(none)"

// extname gives the extension of a "/" path as Node.js path.extname does: a
// dot at the start of the base name is not an extension.
func extname(p string) string {
	base := path.Base(p)
	if base == ".." {
		return ""
	}

	i := strings.LastIndex(base, ".")
	if i <= 0 {
		return ""
	}

	return base[i:]
}

// languageOf gives the language key of a file: its lower-case extension, or noExtension.
func languageOf(file string) string {
	if ext := strings.ToLower(extname(file)); ext != "" {
		return ext
	}

	return noExtension
}

// LanguageGap is a language (file extension) of a rule scope whose coverage is below the requirement.
type LanguageGap struct {
	Language string
	Ratio    float64
}

// checkedLanguages gives the languages that LanguageGaps checks: the ones of
// the CBM files, and of the excluded and stale files of cov.
func checkedLanguages(cov *FileCoverage, cbmFiles []string) map[string]struct{} {
	checked := make(map[string]struct{})

	for _, f := range cbmFiles {
		checked[languageOf(fsutil.ToPosix(f))] = struct{}{}
	}

	for _, f := range slices.Concat(cov.Excluded, cov.Stale) {
		checked[languageOf(f)] = struct{}{}
	}

	return checked
}

// languageGapOf gives the gap of one language, and whether its ratio is below required.
func languageGapOf(lang string, stats LanguageCoverage, required float64) (LanguageGap, bool) {
	if stats.Total == 0 {
		return LanguageGap{}, false
	}

	ratio := float64(stats.Covered) / float64(stats.Total)

	return LanguageGap{Language: lang, Ratio: ratio}, ratio < required
}

// LanguageGaps gives the languages of cov with Covered/Total below required, sorted by language.
// A language is checked only when cbmFiles has at least one file of it, or when a file of it
// in cov.Excluded or cov.Stale exists. Languages that CBM never indexes are left to the total ratio.
func LanguageGaps(cov *FileCoverage, cbmFiles []string, required float64) []LanguageGap {
	checked := checkedLanguages(cov, cbmFiles)
	gaps := []LanguageGap{}

	for _, lang := range sortedKeys(cov.Languages) {
		if !hasKey(checked, lang) {
			continue
		}

		if gap, below := languageGapOf(lang, cov.Languages[lang], required); below {
			gaps = append(gaps, gap)
		}
	}

	return gaps
}

// coverageClassifier decides which files are in the scope of a coverage and
// how a file in scope is counted.
type coverageClassifier struct {
	// matchInclude is nil without Include globs, matchScope without ScopeGlobs.
	matchInclude func(string) bool
	matchScope   func(string) bool
	matchExclude func(string) bool
	// cbm are the CBM files, and cbmLanguages their languages.
	cbm          map[string]struct{}
	cbmLanguages map[string]struct{}
	dirty        map[string]struct{}
}

func newCoverageClassifier(input *FileCoverageInput) *coverageClassifier {
	classifier := &coverageClassifier{
		matchExclude: fsutil.GlobMatcher(input.Exclude),
		cbm:          make(map[string]struct{}, len(input.CbmFiles)),
		cbmLanguages: make(map[string]struct{}),
		dirty:        make(map[string]struct{}, len(input.DirtyFiles)),
	}

	if len(input.Include) > 0 {
		classifier.matchInclude = fsutil.GlobMatcher(input.Include)
	}

	if input.ScopeGlobs != nil {
		classifier.matchScope = fsutil.GlobMatcher(input.ScopeGlobs)
	}

	for _, f := range input.CbmFiles {
		posix := fsutil.ToPosix(f)
		classifier.cbm[posix] = struct{}{}
		classifier.cbmLanguages[languageOf(posix)] = struct{}{}
	}

	for _, f := range input.DirtyFiles {
		classifier.dirty[fsutil.ToPosix(f)] = struct{}{}
	}

	return classifier
}

// inScope says whether a file matches the Include and ScopeGlobs filters.
func (c *coverageClassifier) inScope(file string) bool {
	if c.matchInclude != nil && !c.matchInclude(file) {
		return false
	}

	return c.matchScope == nil || c.matchScope(file)
}

// count adds one file in scope to the coverage.
func (c *coverageClassifier) count(cov *FileCoverage, file string, size int64) {
	cov.Total++
	cov.Bytes.Total += size

	ext := languageOf(file)

	lang := cov.Languages[ext]
	lang.Total++

	excluded := c.matchExclude(file)

	switch {
	case excluded:
		cov.Excluded = append(cov.Excluded, file)
	case hasKey(c.dirty, file):
		cov.Stale = append(cov.Stale, file)
	case !hasKey(c.cbm, file):
		cov.Unindexed = append(cov.Unindexed, file)
	default:
		cov.Covered++
		cov.Bytes.Covered += size
		lang.Covered++
	}

	if !excluded && !hasKey(c.cbmLanguages, ext) {
		cov.Unsupported = append(cov.Unsupported, file)
	}

	cov.Languages[ext] = lang
}

// coverageRatio gives covered/total, or 1 for an empty total.
func coverageRatio(covered, total float64) float64 {
	if total > 0 {
		return covered / total
	}

	return 1
}

// ComputeFileCoverage gives the file coverage of a scope. Files that match
// Exclude STAY in the total: they are counted as not covered and listed in
// Excluded. A file is covered when CBM has a File node for it, it is not
// excluded, and it is not dirty.
func ComputeFileCoverage(input *FileCoverageInput) FileCoverage {
	classifier := newCoverageClassifier(input)

	cov := FileCoverage{
		Excluded:    []string{},
		Stale:       []string{},
		Unindexed:   []string{},
		Unsupported: []string{},
		Languages:   map[string]LanguageCoverage{},
	}

	for _, file := range uniqueSortedPosix(input.WorkspaceFiles) {
		if classifier.inScope(file) {
			classifier.count(&cov, file, input.Sizes[file])
		}
	}

	cov.Ratio = coverageRatio(float64(cov.Covered), float64(cov.Total))
	cov.Bytes.Ratio = coverageRatio(float64(cov.Bytes.Covered), float64(cov.Bytes.Total))
	cov.EmptyScope = cov.Total == 0

	return cov
}

// FileSizes gives the size in bytes of each file below root. The files are "/" paths
// relative to root. A file that cannot be read is left out.
func FileSizes(root string, files []string) map[string]int64 {
	sizes := make(map[string]int64, len(files))

	for _, f := range files {
		posix := fsutil.ToPosix(f)

		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(posix)))
		if err != nil || info.IsDir() {
			continue
		}

		sizes[posix] = info.Size()
	}

	return sizes
}

func hasKey(set map[string]struct{}, key string) bool {
	_, ok := set[key]

	return ok
}

func uniqueSortedPosix(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = fsutil.ToPosix(f)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return slices.Compact(out)
}
