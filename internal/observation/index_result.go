package observation

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/inclaro/concordia/internal/canonjson"
)

// PathReason is a file that CBM did not index, with the reason.
type PathReason struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// PartialFile is a file that CBM parsed in part, with the line ranges it could not parse.
type PartialFile struct {
	Path        string `json:"path"`
	ErrorRanges string `json:"error_ranges"`
}

// IndexResult is the part of the `index_repository` result that records coverage loss
// (CBM 0.12.1 observations, O1). The shape is the one of the provider.
type IndexResult struct {
	Project string `json:"project"`
	Status  string `json:"status"`
	// Excluded lists the directories that CBM skips (`.git`, and by default `deploy/`, `build/`, ...).
	Excluded struct {
		Dirs  []string `json:"dirs"`
		Count int      `json:"count"`
	} `json:"excluded"`
	NotIndexedFiles struct {
		Files []PathReason `json:"files"`
		Count int          `json:"count"`
	} `json:"not_indexed_files"`
	ParsePartial struct {
		Files []PartialFile `json:"files"`
		Count int           `json:"count"`
	} `json:"parse_partial"`
	ParseUnusable struct {
		Count int `json:"count"`
	} `json:"parse_unusable"`
	Nodes      int  `json:"nodes"`
	Edges      int  `json:"edges"`
	ADRPresent bool `json:"adr_present"`
}

// ParseIndexResult decodes the body of an `index_repository` call.
func ParseIndexResult(raw []byte) (IndexResult, error) {
	var result IndexResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return IndexResult{}, fmt.Errorf("decode index_repository: %w", err)
	}

	return result, nil
}

// IndexRecord is the index result of one project as the run keeps it: the
// lists of `excluded.dirs`, `not_indexed_files` and `parse_partial`, sorted,
// with the graph generation and revision that the index produced. `concordia
// index` writes it; `concordia check` reads it into the run record.
type IndexRecord struct {
	Project string `json:"project"`
	// GraphGeneration is the generation of the graph that the index made. A record of another generation does not describe the graph of a run.
	GraphGeneration string `json:"graphGeneration"`
	SourceRevision  string `json:"sourceRevision,omitempty"`
	// ExcludedDirs are the directories that the provider skipped, as the provider names them.
	ExcludedDirs []string `json:"excludedDirs"`
	// NotIndexedFiles are the files that the provider did not index (the provider lists examples only: see NotIndexedCount).
	NotIndexedFiles []PathReason `json:"notIndexedFiles"`
	// ParsePartial are the files that the provider parsed in part, with the line ranges that it could not parse.
	ParsePartial []PartialFile `json:"parsePartial"`
	// NotIndexedCount is the provider count of not indexed files. It is larger than the list when the provider listed examples only.
	NotIndexedCount int `json:"notIndexedCount"`
	// ParsePartialCount is the provider count of partly parsed files.
	ParsePartialCount int `json:"parsePartialCount"`
}

// Record gives the run record of the index result of a project at a graph generation and revision.
func (r *IndexResult) Record(project, generation, revision string) IndexRecord {
	rec := IndexRecord{
		Project:           project,
		GraphGeneration:   generation,
		SourceRevision:    revision,
		ExcludedDirs:      slices.Clone(r.Excluded.Dirs),
		NotIndexedFiles:   slices.Clone(r.NotIndexedFiles.Files),
		NotIndexedCount:   max(r.NotIndexedFiles.Count, len(r.NotIndexedFiles.Files)),
		ParsePartial:      slices.Clone(r.ParsePartial.Files),
		ParsePartialCount: max(r.ParsePartial.Count, len(r.ParsePartial.Files)),
	}

	rec.normalize()

	return rec
}

// normalize sorts the lists and makes them non-nil, so that equal results give equal records.
func (r *IndexRecord) normalize() {
	if r.ExcludedDirs == nil {
		r.ExcludedDirs = []string{}
	}

	if r.NotIndexedFiles == nil {
		r.NotIndexedFiles = []PathReason{}
	}

	if r.ParsePartial == nil {
		r.ParsePartial = []PartialFile{}
	}

	for i, d := range r.ExcludedDirs {
		r.ExcludedDirs[i] = cleanDir(d)
	}

	slices.SortFunc(r.ExcludedDirs, canonjson.CompareUTF16)
	r.ExcludedDirs = slices.Compact(r.ExcludedDirs)

	slices.SortFunc(r.NotIndexedFiles, func(a, b PathReason) int {
		return canonjson.CompareUTF16(a.Path+"\x00"+a.Reason, b.Path+"\x00"+b.Reason)
	})
	slices.SortFunc(r.ParsePartial, func(a, b PartialFile) int {
		return canonjson.CompareUTF16(a.Path+"\x00"+a.ErrorRanges, b.Path+"\x00"+b.ErrorRanges)
	})
}

// cleanDir gives a directory as a "/" path without leading "./" and without a trailing "/".
func cleanDir(dir string) string {
	dir = strings.ReplaceAll(dir, `\`, "/")
	dir = strings.TrimPrefix(dir, "./")

	return strings.Trim(dir, "/")
}

// Normalized gives a copy with the lists sorted and non-nil: the form that is written and compared.
func (r IndexRecord) Normalized() IndexRecord { //nolint:gocritic // a small value type that is copied on purpose.
	r.ExcludedDirs = slices.Clone(r.ExcludedDirs)
	r.NotIndexedFiles = slices.Clone(r.NotIndexedFiles)
	r.ParsePartial = slices.Clone(r.ParsePartial)
	r.normalize()

	return r
}

// ExampleListsOnly says whether the provider listed examples only for the
// not indexed or partly parsed files (its count is larger than the list).
func (r *IndexRecord) ExampleListsOnly() bool {
	return r.NotIndexedCount > len(r.NotIndexedFiles) || r.ParsePartialCount > len(r.ParsePartial)
}

// rasterSuffixes are the file suffixes of raster images that the provider does not index.
var rasterSuffixes = []string{".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp", ".tif", ".tiff", ".ico"}

// IsRasterImage says whether a path is a raster image by its suffix.
func IsRasterImage(file string) bool {
	return slices.Contains(rasterSuffixes, strings.ToLower(path.Ext(file)))
}

// firstErrorLine gives the first line of the `error_ranges` of a partly parsed file
// ("1-4", "3-9,12-14"), 0 when it is not a range list.
func firstErrorLine(ranges string) int {
	first, _, _ := strings.Cut(ranges, ",")
	start, _, _ := strings.Cut(strings.TrimSpace(first), "-")

	line, err := strconv.Atoi(start)
	if err != nil || line < 1 {
		return 0
	}

	return line
}

// File extensions that several path classifiers share.
const (
	extTpl  = ".tpl" // a Helm template helper file
	extYAML = ".yaml"
	extYML  = ".yml"
)

// dockerfileName is the file name of a Dockerfile, and the prefix of `Dockerfile.<suffix>`.
const dockerfileName = "Dockerfile"

// vcsDir is the directory that the provider always skips and no source can be in.
const vcsDir = ".git"

// EnvironmentSources are the declared sources of one environment: workspace-relative
// globs, with the `@<project>/` prefix for a project that is not the primary one.
type EnvironmentSources struct {
	ID      string
	Sources []string
}

// GlobOverlapsDir says whether a source glob can match a file below a directory
// that the provider excluded. The directory is a path relative to the project root, as the provider lists it.
func GlobOverlapsDir(glob, dir string) bool {
	glob = strings.TrimPrefix(cleanDir(glob), "/")
	dir = cleanDir(dir)

	if glob == "" || dir == "" {
		return false
	}

	globParts := strings.Split(glob, "/")
	dirParts := strings.Split(dir, "/")

	for i, part := range dirParts {
		if i >= len(globParts) {
			return false
		}

		if globParts[i] == "**" {
			return true
		}

		if !segmentMatches(globParts[i], part) {
			return false
		}
	}

	return true
}

func segmentMatches(pattern, name string) bool {
	ok, err := doublestar.Match(pattern, name)

	return err == nil && ok
}

// IndexGaps gives the coverage gaps that an index result shows, for a project:
//   - `default-excluded-directory` for each environment source that overlaps a directory the provider skipped, with the `.cbmignore` `!<dir>/` re-include hint (the environment and the source are named; `.git` is never a gap),
//   - `raster-not-indexed` for each raster image in `not_indexed_files`,
//   - `parse-partial` for each partly parsed file, and `unrendered-template` when it is a YAML or template file (the provider could not parse a template).
//
// The gaps are sorted. A nil record gives none: the caller marks the scope unknown instead.
func IndexGaps(rec *IndexRecord, environments []EnvironmentSources) []CoverageGap {
	if rec == nil {
		return nil
	}

	var gaps []CoverageGap

	for _, env := range environments {
		gaps = append(gaps, indexExcludedDirGaps(rec, env)...)
	}

	gaps = append(gaps, indexRasterGaps(rec)...)
	gaps = append(gaps, indexParsePartialGaps(rec)...)

	return SortGaps(gaps)
}

// indexExcludedDirGaps gives a gap for each directory the provider skipped that a source of the environment overlaps.
func indexExcludedDirGaps(rec *IndexRecord, env EnvironmentSources) []CoverageGap {
	var gaps []CoverageGap

	for _, dir := range rec.ExcludedDirs {
		if dir == vcsDir {
			continue
		}

		source, overlaps := indexFirstOverlappingSource(env.Sources, dir)
		if !overlaps {
			continue
		}

		gaps = append(gaps, CoverageGap{
			Reason: LossDefaultExcludedDirectory, Environment: env.ID, Project: rec.Project, Path: dir + "/",
			Source: source, Hint: ReincludeHint(dir),
		})
	}

	return gaps
}

// indexFirstOverlappingSource gives the first source glob that can match a file below dir.
func indexFirstOverlappingSource(sources []string, dir string) (string, bool) {
	for _, source := range sources {
		if GlobOverlapsDir(source, dir) {
			return source, true
		}
	}

	return "", false
}

// indexRasterGaps gives a gap for each raster image in `not_indexed_files`.
func indexRasterGaps(rec *IndexRecord) []CoverageGap {
	var gaps []CoverageGap

	for _, file := range rec.NotIndexedFiles {
		if IsRasterImage(file.Path) {
			gaps = append(gaps, CoverageGap{Reason: LossRasterNotIndexed, Project: rec.Project, Path: file.Path, Detail: file.Reason})
		}
	}

	return gaps
}

// indexParsePartialGaps gives a `parse-partial` gap for each partly parsed file, and an
// `unrendered-template` gap when the file can be a template.
func indexParsePartialGaps(rec *IndexRecord) []CoverageGap {
	var gaps []CoverageGap

	for _, file := range rec.ParsePartial {
		line := firstErrorLine(file.ErrorRanges)
		gaps = append(gaps, CoverageGap{Reason: LossParsePartial, Project: rec.Project, Path: file.Path, Line: line, Detail: "error ranges " + file.ErrorRanges})

		if isTemplateCandidate(file.Path) {
			gaps = append(gaps, CoverageGap{Reason: LossUnrenderedTemplate, Project: rec.Project, Path: file.Path, Line: line, Detail: "partly parsed template"})
		}
	}

	return gaps
}

// IsDockerfile says whether a path is a Dockerfile by its name: `Dockerfile` or `Dockerfile.<suffix>`.
func IsDockerfile(file string) bool {
	base := path.Base(file)

	return base == dockerfileName || (strings.HasPrefix(base, dockerfileName+".") && len(base) > len(dockerfileName)+1)
}

// DockerfileGaps gives the coverage gaps of the Dockerfile set of a project: `default-excluded-directory` for each
// Dockerfile under a directory of `excluded.dirs` (the `.cbmignore` re-include hint names the directory), and
// `parse-partial` for each Dockerfile that the provider parsed in part. dockerfiles are the project-relative "/"
// paths of the Dockerfiles in the workspace files of the project. The gaps say nothing about image links: that is
// the loss mode `build-link-needs-build-command` of `built-from`. A nil record gives none.
func DockerfileGaps(rec *IndexRecord, dockerfiles []string) []CoverageGap {
	if rec == nil {
		return nil
	}

	var gaps []CoverageGap

	for _, file := range dockerfiles {
		if dir, excluded := dockerfileExcludedDir(rec.ExcludedDirs, file); excluded {
			gaps = append(gaps, CoverageGap{
				Reason: LossDefaultExcludedDirectory, Project: rec.Project, Path: file, Hint: ReincludeHint(dir),
				Detail: "Dockerfile is in the excluded directory " + dir + "/",
			})
		}

		gaps = append(gaps, dockerfilePartialGaps(rec, file)...)
	}

	return SortGaps(gaps)
}

// dockerfileExcludedDir gives the first excluded directory that holds the file, `.git` aside.
func dockerfileExcludedDir(excludedDirs []string, file string) (string, bool) {
	for _, dir := range excludedDirs {
		if dir != vcsDir && strings.HasPrefix(file, dir+"/") {
			return dir, true
		}
	}

	return "", false
}

// dockerfilePartialGaps gives a `parse-partial` gap for each time the provider parsed the file in part.
func dockerfilePartialGaps(rec *IndexRecord, file string) []CoverageGap {
	var gaps []CoverageGap

	for _, partial := range rec.ParsePartial {
		if partial.Path == file {
			gaps = append(gaps, CoverageGap{
				Reason: LossParsePartial, Project: rec.Project, Path: file, Line: firstErrorLine(partial.ErrorRanges),
				Detail: "error ranges " + partial.ErrorRanges,
			})
		}
	}

	return gaps
}

// ReincludeHint gives the `.cbmignore` line that includes a directory again.
func ReincludeHint(dir string) string {
	return "add `!" + cleanDir(dir) + "/` to .cbmignore and run `concordia index` again"
}

// isTemplateCandidate says whether a partly parsed file can be a rendered-template source:
// a YAML manifest or a template file.
func isTemplateCandidate(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case extYAML, extYML, extTpl, ".tmpl", ".gotmpl":
		return true
	default:
		return false
	}
}
