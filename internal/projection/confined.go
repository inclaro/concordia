package projection

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/procexec"
)

const (
	scratchPattern = "concordia-structurizr-*"
	// scratchDSL is the name of the DSL file in the scratch directory.
	scratchDSL = "workspace.dsl"
	// scratchOut is the output directory in the scratch directory.
	scratchOut = "out"
	// scratchDirMode and scratchFileMode are the permissions in the scratch directory.
	scratchDirMode  = 0o700
	scratchFileMode = 0o600
)

// ErrIncludeUnavailable is the cause (see errors.Is) of an IncludeUnavailableError.
var ErrIncludeUnavailable = errors.New("include not available")

// IncludeUnavailableError says that an include that projection.allowIncludes
// allows cannot be read. The freshness of the export is then `unknown`, not `pass`.
type IncludeUnavailableError struct {
	Err error
	// Path is the workspace-relative path of the include.
	Path string
}

// Error gives the text of the error.
func (e *IncludeUnavailableError) Error() string {
	return fmt.Sprintf("%v: %s: %v", ErrIncludeUnavailable, e.Path, e.Err)
}

// Is makes errors.Is(err, ErrIncludeUnavailable) true.
func (e *IncludeUnavailableError) Is(target error) bool { return target == ErrIncludeUnavailable }

// Unwrap gives the cause.
func (e *IncludeUnavailableError) Unwrap() error { return e.Err }

// ConfinedOptions are the settings of RunConfined.
type ConfinedOptions struct {
	// Getenv supplies PATH and JAVA_HOME for the confined environment. nil reads
	// the process environment.
	Getenv func(string) string
	// CLI is the Structurizr CLI program.
	CLI string
	// Format is the export format.
	Format ExportFormat
	// WorkspaceRoot is the absolute workspace directory. Allowed includes are
	// read from it and nothing else is.
	WorkspaceRoot string
	// DSL is the Structurizr DSL to export.
	DSL []byte
	// AllowIncludes is projection.allowIncludes: the workspace-relative paths
	// that the DSL may include.
	AllowIncludes []string
	// VerifiedPin says that the caller already ran VerifyPinned for CLI, so
	// RunConfined does not run `version` again.
	VerifiedPin bool
}

// ExportFile is one file that the CLI wrote.
type ExportFile struct {
	// Path is the path below the output directory, with "/" separators.
	Path string
	Data []byte
}

// ConfinedResult is the result of RunConfined.
type ConfinedResult struct {
	// Version is the CLI version (always PinnedStructurizrCLI).
	Version string
	// Files are the files that the export wrote, sorted by path.
	Files []ExportFile
}

// inheritedEnv are the variables that the confined environment takes from the
// caller: the program search path and the Java location. No proxy variable, no
// credential, and no JVM option (JAVA_TOOL_OPTIONS) is inherited.
var inheritedEnv = []string{"PATH", "JAVA_HOME"}

// windowsEnv are the extra variables that a Windows process needs to start.
var windowsEnv = []string{"SystemRoot", "PATHEXT", "ComSpec"}

// ConfinedEnv gives the environment of a confined CLI process: the variables in
// inheritedEnv that getenv knows, and HOME and TMPDIR set to the scratch
// directory. getenv nil reads the process environment. The result is sorted.
func ConfinedEnv(getenv func(string) string, scratch string) []string {
	if getenv == nil {
		getenv = os.Getenv
	}

	names := inheritedEnv
	if runtime.GOOS == "windows" {
		names = append(slices.Clone(names), windowsEnv...)
	}

	env := []string{"HOME=" + scratch, "TMPDIR=" + scratch}

	for _, name := range names {
		if v := getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}

	slices.Sort(env)

	return env
}

// directive is a DSL directive that loads outside content or code.
type directive struct {
	keyword string
	// args are the arguments of the directive.
	args []string
	// inline says that the directive carries an inline block (`!script groovy {`).
	inline bool
	line   int
}

var (
	directivePattern = regexp.MustCompile(`(?i)^\s*!(include|script|plugin|docs|adrs|import)(?:\s+(.*))?$`)
	urlPattern       = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:(//|[a-z]:?[\\/])`)
)

// scanDirectives finds the directives `!include`, `!script`, `!plugin`, `!docs`,
// `!adrs`, and `!import`. It reads every line, also inside comments and text
// blocks, so that nothing can hide a directive; a false match only refuses a
// DSL that the scan cannot prove to be safe.
func scanDirectives(text string) []directive {
	var out []directive

	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")

	for i := 0; i < len(lines); i++ {
		start := i
		line := lines[i]

		// A trailing backslash continues the line.
		for strings.HasSuffix(strings.TrimRight(line, " \t"), `\`) && i+1 < len(lines) {
			line = strings.TrimSuffix(strings.TrimRight(line, " \t"), `\`) + " " + lines[i+1]
			i++
		}

		m := directivePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		args, inline := splitArgs(m[2])
		out = append(out, directive{keyword: strings.ToLower(m[1]), args: args, inline: inline, line: start + 1})
	}

	return out
}

// splitArgs splits the arguments of a directive at white space. A quoted
// argument may contain white space. inline is true when an unquoted "{" is present.
func splitArgs(rest string) (args []string, inline bool) {
	var (
		cur    strings.Builder
		quoted bool
		has    bool
	)

	flush := func() {
		if has {
			args = append(args, cur.String())
		}

		cur.Reset()

		has = false
	}

	for _, char := range rest {
		switch {
		case char == '"':
			quoted = !quoted
			has = true
		case quoted:
			cur.WriteRune(char)
		case char == ' ' || char == '\t':
			flush()
		default:
			if char == '{' {
				inline = true
			}

			cur.WriteRune(char)

			has = true
		}
	}

	flush()

	return args, inline
}

// includeScan checks the directives of the DSL and of the includes that
// projection.allowIncludes allows, and collects the allowed files.
type includeScan struct {
	allowed map[string]bool
	// files are the allowed files by workspace-relative path.
	files      map[string][]byte
	seen       map[string]bool
	unavail    *IncludeUnavailableError
	root       string
	violations []string
}

func (s *includeScan) refuse(file string, d directive, why string) {
	where := ""
	if file != "" {
		where = file + ":"
	}

	s.violations = append(s.violations, fmt.Sprintf("%s%d: !%s: %s", where, d.line, d.keyword, why))
}

// scanText checks the directives of one text. dir is the workspace-relative
// directory of the file that holds it ("." for the DSL); file names the file in
// messages ("" for the DSL).
func (s *includeScan) scanText(dir, file, text string) {
	for _, d := range scanDirectives(text) {
		s.checkDirective(dir, file, d)
	}
}

func (s *includeScan) checkDirective(dir, file string, item directive) {
	if why := confinedShapeProblem(item); why != "" {
		s.refuse(file, item, why)

		return
	}

	arg := item.args[0]
	if why := confinedArgProblem(arg); why != "" {
		s.refuse(file, item, why)

		return
	}

	rel := path.Join(dir, filepath.ToSlash(arg))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		s.refuse(file, item, "the path "+arg+" is outside the workspace")

		return
	}

	if !s.allowed[rel] {
		s.refuse(file, item, "the path "+rel+" is not listed in projection.allowIncludes")

		return
	}

	s.load(rel, item.keyword == "include")
}

// confinedShapeProblem gives the refusal reason for the arguments of a directive, or "".
func confinedShapeProblem(item directive) string {
	for _, arg := range item.args {
		if urlPattern.MatchString(arg) {
			return "URL " + arg + " is never allowed"
		}
	}

	switch {
	case item.inline:
		return "inline code is not allowed"
	case len(item.args) == 0:
		return "no path"
	}

	return ""
}

// confinedArgProblem gives the refusal reason for a path argument, or "".
func confinedArgProblem(arg string) string {
	switch {
	case strings.Contains(arg, "${"):
		return "the path " + arg + " uses a substitution"
	case confinedIsAbs(arg):
		return "the path " + arg + " is absolute"
	}

	return ""
}

func confinedIsAbs(arg string) bool {
	return path.IsAbs(filepath.ToSlash(arg)) || filepath.IsAbs(arg) || filepath.VolumeName(arg) != ""
}

// load reads an allowed path (a file, or the files of a directory) and, for
// `!include`, scans the DSL that it holds.
func (s *includeScan) load(rel string, scanDSL bool) {
	if s.seen[rel] {
		return
	}

	s.seen[rel] = true

	full := filepath.Join(s.root, filepath.FromSlash(rel))

	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		s.unavailable(rel, err)

		return
	}

	if !within(s.root, resolved) {
		s.violations = append(s.violations, rel+": resolves outside the workspace")

		return
	}

	info, err := os.Stat(resolved)
	if err != nil {
		s.unavailable(rel, err)

		return
	}

	if !info.IsDir() {
		s.loadFile(rel, resolved, scanDSL)

		return
	}

	err = filepath.WalkDir(resolved, s.confinedWalker(rel, resolved, scanDSL))
	if err != nil {
		s.unavailable(rel, err)
	}
}

// confinedWalker gives the WalkDir callback that loads the files below a directory.
func (s *includeScan) confinedWalker(rel, resolved string, scanDSL bool) fs.WalkDirFunc {
	return func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		sub, relErr := filepath.Rel(resolved, file)
		if relErr != nil {
			return fmt.Errorf("relative path of %s: %w", file, relErr)
		}

		entryRel := path.Join(rel, filepath.ToSlash(sub))

		// WalkDir does not follow symlinks, but a read would: refuse every entry that is not a regular file.
		if !entry.Type().IsRegular() {
			s.violations = append(s.violations, entryRel+": not a regular file (a symlink or special file is not followed)")

			return nil
		}

		s.loadFile(entryRel, file, scanDSL)

		return nil
	}
}

func (s *includeScan) loadFile(rel, full string, scanDSL bool) {
	data, err := os.ReadFile(filepath.Clean(full))
	if err != nil {
		s.unavailable(rel, err)

		return
	}

	s.files[rel] = data

	if scanDSL {
		s.scanText(path.Dir(rel), rel, string(data))
	}
}

func (s *includeScan) unavailable(rel string, err error) {
	if s.unavail == nil {
		s.unavail = &IncludeUnavailableError{Path: rel, Err: err}
	}
}

// within says whether the resolved path is inside the root.
func within(root, resolved string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}

	rel, err := filepath.Rel(realRoot, resolved)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// CheckRenderingInput scans the DSL for directives (`!include`, `!script`,
// `!plugin`, `!docs`, `!adrs`, `!import`) and URLs. An include is accepted only
// when its path, relative to the directory of the including file, is inside the
// workspace and is listed in allowIncludes. It gives CO-DGM-003 (exit 2) for
// every refused directive, and an *IncludeUnavailableError when an allowed
// include cannot be read. It gives the content of the allowed files by
// workspace-relative path.
func CheckRenderingInput(root string, allowIncludes []string, dsl []byte) (map[string][]byte, error) {
	scan := &includeScan{
		root: root, allowed: map[string]bool{}, files: map[string][]byte{}, seen: map[string]bool{},
	}

	for _, p := range allowIncludes {
		scan.allowed[path.Clean(filepath.ToSlash(p))] = true
	}

	scan.scanText(".", "", string(dsl))

	if len(scan.violations) > 0 {
		slices.Sort(scan.violations)

		return nil, diag.Fail(diag.DGM003, diag.Detail(strings.Join(scan.violations, "; ")))
	}

	if scan.unavail != nil {
		return nil, scan.unavail
	}

	return scan.files, nil
}

// RunConfined exports a DSL with the pinned Structurizr CLI in a confined
// process:
//
//  1. The directive scan (CheckRenderingInput) runs first, so a refused DSL
//     never starts the CLI.
//  2. VerifyPinned runs, unless the caller did that (VerifiedPin).
//  3. The DSL and the allowed includes are written to a fresh scratch directory
//     that is the working directory, the environment is replaced (ConfinedEnv),
//     and the CLI writes its output below the scratch directory.
//  4. The output is read back and the scratch directory is removed.
//
// The process has no proxy variable and no inherited credential. A hard network
// denial is not available from Go; run the CLI through a wrapper that uses
// `--network none` for that.
func RunConfined(ctx context.Context, opts *ConfinedOptions) (ConfinedResult, error) {
	includes, err := CheckRenderingInput(opts.WorkspaceRoot, opts.AllowIncludes, opts.DSL)
	if err != nil {
		return ConfinedResult{}, err
	}

	version := PinnedStructurizrCLI

	if !opts.VerifiedPin {
		if version, err = VerifyPinned(ctx, opts.CLI, opts.Getenv); err != nil {
			return ConfinedResult{}, err
		}
	}

	scratch, err := os.MkdirTemp("", scratchPattern)
	if err != nil {
		return ConfinedResult{}, fmt.Errorf("create scratch directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(scratch) }()

	if err := writeScratch(scratch, opts.DSL, includes); err != nil {
		return ConfinedResult{}, err
	}

	args := []string{cmdExport, flagWorkspace, scratchDSL, "-format", string(opts.Format), "-output", scratchOut}

	if err := execCLI(ctx, opts.CLI, args, procexec.Options{
		Dir: scratch, Env: ConfinedEnv(opts.Getenv, scratch), Timeout: cliTimeout,
	}); err != nil {
		return ConfinedResult{}, err
	}

	files, err := readOutput(filepath.Join(scratch, scratchOut))
	if err != nil {
		return ConfinedResult{}, err
	}

	return ConfinedResult{Version: version, Files: files}, nil
}

func writeScratch(scratch string, dsl []byte, includes map[string][]byte) error {
	if err := os.Mkdir(filepath.Join(scratch, scratchOut), scratchDirMode); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	if err := os.WriteFile(filepath.Join(scratch, scratchDSL), dsl, scratchFileMode); err != nil {
		return fmt.Errorf("write DSL: %w", err)
	}

	for rel, data := range includes {
		target := filepath.Join(scratch, filepath.FromSlash(rel))

		if err := os.MkdirAll(filepath.Dir(target), scratchDirMode); err != nil {
			return fmt.Errorf("create directory of include %s: %w", rel, err)
		}

		if err := os.WriteFile(target, data, scratchFileMode); err != nil {
			return fmt.Errorf("write include %s: %w", rel, err)
		}
	}

	return nil
}

// readOutput reads the regular files below dir, sorted by path.
func readOutput(dir string) ([]ExportFile, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open export output: %w", err)
	}

	defer func() { _ = root.Close() }()

	var files []ExportFile

	fsys := root.FS()

	err = fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		data, readErr := fs.ReadFile(fsys, name)
		if readErr != nil {
			return fmt.Errorf("read export %s: %w", name, readErr)
		}

		files = append(files, ExportFile{Path: name, Data: data})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read export output: %w", err)
	}

	slices.SortFunc(files, func(a, b ExportFile) int { return strings.Compare(a.Path, b.Path) })

	return files, nil
}

// WriteExportFiles writes the files below dir with fsutil.WriteFileAtomic.
func WriteExportFiles(dir string, files []ExportFile) error {
	for _, f := range files {
		if err := fsutil.WriteFileAtomic(filepath.Join(dir, filepath.FromSlash(f.Path)), f.Data); err != nil {
			return fmt.Errorf("write export %s: %w", f.Path, err)
		}
	}

	return nil
}
