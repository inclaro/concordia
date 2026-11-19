package projection

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/procexec"
)

// ExportFormat is a format of `structurizr export`.
type ExportFormat string

// Export formats.
const (
	FormatPlantUML ExportFormat = "plantuml"
	FormatMermaid  ExportFormat = "mermaid"
	FormatDot      ExportFormat = "dot"
	FormatJSON     ExportFormat = "json"
	FormatStatic   ExportFormat = "static"
)

// ExportFormats are all export formats.
var ExportFormats = []ExportFormat{FormatPlantUML, FormatMermaid, FormatDot, FormatJSON, FormatStatic}

// EnvStructurizrCLI is the environment variable that names the Structurizr CLI.
const EnvStructurizrCLI = "STRUCTURIZR_CLI"

const (
	cliTimeout = 5 * time.Minute
	// maxDetail is the number of characters of process output that a diagnostic keeps.
	maxDetail = 2000
	// executableBits are the permission bits that make a file executable.
	executableBits = 0o111
)

// Arguments of the Structurizr CLI.
const (
	cmdValidate   = "validate"
	cmdExport     = "export"
	flagWorkspace = "-workspace"
)

// cliNames are the program names that are looked up on the PATH, in this order.
var cliNames = []string{"structurizr-cli", "structurizr.sh", "structurizr"}

func executable(p string) bool {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return false
	}

	// Windows has no execute permission bit.
	return runtime.GOOS == "windows" || info.Mode().Perm()&executableBits != 0
}

func onPath(name, pathEnv string) (string, bool) {
	if filepath.IsAbs(name) || strings.Contains(name, "/") {
		return name, executable(name)
	}

	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}

		if p := filepath.Join(dir, name); executable(p) {
			return p, true
		}
	}

	return "", false
}

// FindCLI finds the Structurizr CLI: the program that STRUCTURIZR_CLI names, then
// `structurizr-cli`, `structurizr.sh` and `structurizr` on the PATH. getenv gives
// the environment value for a key ("" when it is not set).
func FindCLI(getenv func(string) string) (string, bool) {
	pathEnv := getenv("PATH")

	if fromEnv := getenv(EnvStructurizrCLI); fromEnv != "" {
		if p, ok := onPath(fromEnv, pathEnv); ok {
			return p, true
		}
	}

	for _, name := range cliNames {
		if p, ok := onPath(name, pathEnv); ok {
			return p, true
		}
	}

	return "", false
}

// RequireCLI finds the Structurizr CLI or gives CO-PRJ-001 (exit 5). need names
// what needs the CLI, for the message.
func RequireCLI(need string, getenv func(string) string) (string, error) {
	cli, ok := FindCLI(getenv)
	if !ok {
		return "", diag.Fail(diag.PRJ001, diag.Params{"format": need})
	}

	return cli, nil
}

// tail gives the last n characters of s.
func tail(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	runes := []rune(s)

	return string(runes[len(runes)-n:])
}

// execCLI runs the Structurizr CLI. A program that cannot start or a non-zero
// exit code gives CO-PRJ-002; a process that stops for another reason gives a
// plain error.
func execCLI(ctx context.Context, cli string, args []string, opts procexec.Options) error {
	res, err := procexec.Run(ctx, cli, args, opts)

	var spawn *procexec.SpawnError

	switch {
	case errors.As(err, &spawn):
		return diag.Fail(diag.PRJ002, diag.Detail(spawn.Error()))
	case err != nil:
		return fmt.Errorf("run %s: %w", cli, err)
	case res.Code == 0:
		return nil
	}

	detail := tail(strings.TrimSpace(firstNonEmpty(res.Stderr, res.Stdout)), maxDetail)
	if detail == "" {
		detail = "exit code " + strconv.Itoa(res.Code)
	}

	return diag.Fail(diag.PRJ002, diag.Detail(detail))
}

func runCLI(ctx context.Context, cli string, args []string) error {
	return execCLI(ctx, cli, args, procexec.Options{Timeout: cliTimeout})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

// Validate runs `structurizr validate`. A failure gives CO-PRJ-002.
func Validate(ctx context.Context, cli, dslPath string) error {
	return runCLI(ctx, cli, []string{cmdValidate, flagWorkspace, dslPath})
}

// Export runs `structurizr export`. A failure gives CO-PRJ-002.
func Export(ctx context.Context, cli, dslPath string, format ExportFormat, outDir string) error {
	return runCLI(ctx, cli, []string{cmdExport, flagWorkspace, dslPath, "-format", string(format), "-output", outDir})
}

// PinnedStructurizrCLI is the Structurizr CLI version that committed exports
// are generated and verified with. The ad-hoc `render --format` flow accepts any
// version. A new pin is a Concordia release: the recorded exports in
// testdata/exports and the export canonicalization rule must be reviewed.
const PinnedStructurizrCLI = "2025.11.09"

// NeedViewExports names the exports of declared views in a CO-PRJ-001 message.
const NeedViewExports = "the exports of declared views"

// cliVersionLine matches the line of `structurizr version` that names the CLI.
var cliVersionLine = regexp.MustCompile(`(?m)^\s*structurizr-cli:\s*(\S+)\s*$`)

// ParseCLIVersion gives the version in the output of `structurizr version`:
// the value of the `structurizr-cli:` line, else the first non-empty line. It
// gives "" when the output is empty.
func ParseCLIVersion(output string) string {
	if m := cliVersionLine.FindStringSubmatch(output); m != nil {
		return m[1]
	}

	for line := range strings.SplitSeq(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

// VerifyPinned runs `version` on the CLI (with the confined environment) and
// compares the version with PinnedStructurizrCLI. It gives the version that it
// read. A program that cannot start gives CO-PRJ-001 (exit 5); a CLI whose
// version cannot be read or differs from the pin gives CO-DGM-004 (exit 5).
// getenv supplies PATH for the confined environment; nil reads the process
// environment.
func VerifyPinned(ctx context.Context, cli string, getenv func(string) string) (string, error) {
	scratch, err := os.MkdirTemp("", scratchPattern)
	if err != nil {
		return "", fmt.Errorf("create scratch directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(scratch) }()

	res, err := procexec.Run(ctx, cli, []string{"version"}, procexec.Options{
		Dir: scratch, Env: ConfinedEnv(getenv, scratch), Timeout: cliTimeout,
	})

	var spawn *procexec.SpawnError

	switch {
	case errors.As(err, &spawn):
		return "", diag.Fail(diag.PRJ001, diag.Params{"format": NeedViewExports})
	case err != nil:
		return "", fmt.Errorf("run %s: %w", cli, err)
	case res.Code != 0:
		return "", diag.Fail(diag.DGM004, diag.Detail(fmt.Sprintf("`%s version` failed with exit code %d", cli, res.Code)))
	}

	version := ParseCLIVersion(res.Stdout)
	if version == "" {
		return "", diag.Fail(diag.DGM004, diag.Detail(fmt.Sprintf("cannot read a version from `%s version`", cli)))
	}

	if version != PinnedStructurizrCLI {
		return version, diag.Fail(diag.DGM004, diag.Detail(fmt.Sprintf("found %s, pinned %s", version, PinnedStructurizrCLI)))
	}

	return version, nil
}
