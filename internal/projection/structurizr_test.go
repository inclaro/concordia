package projection_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/projection"
	"github.com/inclaro/concordia/internal/testutil"
)

// fakeCLI writes a shell script that stands for the Structurizr CLI. version is
// the version that `version` prints; exportBody runs for `export` with the
// output directory in $7 (the arguments are `export -workspace F -format X -output D`).
func fakeCLI(t *testing.T, dir, version, exportBody string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}

	script := "#!/bin/sh\ncase \"$1\" in\nversion)\n  printf 'structurizr-cli: " + version + "\\nstructurizr-java: 5.0.2\\n'\n  ;;\nexport)\n" + exportBody + "\n  ;;\nesac\n"
	p := filepath.Join(dir, "structurizr-fake.sh")

	testutil.WriteTree(t, dir, map[string]string{"structurizr-fake.sh": script})

	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}

	return p
}

func asDiag(t *testing.T, err error) *diag.Error {
	t.Helper()

	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want a diag error", err)
	}

	return de
}

// structurizrTestDiag is the diagnostic that a test expects. exit 0 and an
// empty contains are not checked.
type structurizrTestDiag struct {
	code     string
	contains string
	exit     int
}

func (w structurizrTestDiag) matches(de *diag.Error) bool {
	if de.Diagnostic.Code != w.code {
		return false
	}

	if w.exit != 0 && de.ExitCode != w.exit {
		return false
	}

	return strings.Contains(de.Diagnostic.Message, w.contains)
}

// structurizrTestExpectDiag fails unless err is the diagnostic want.
func structurizrTestExpectDiag(t *testing.T, err error, want structurizrTestDiag) {
	t.Helper()

	if !want.matches(asDiag(t, err)) {
		t.Errorf("err = %v", err)
	}
}

// structurizrTestNotCreated fails when the file marker exists.
func structurizrTestNotCreated(t *testing.T, marker, what string) {
	t.Helper()

	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Errorf("%s (%v)", what, statErr)
	}
}

// structurizrTestScript writes an executable shell script and gives its path.
func structurizrTestScript(t *testing.T, name, body string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{name: body})

	p := filepath.Join(dir, name)
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}

	return p
}

func TestParseCLIVersion(t *testing.T) {
	t.Parallel()

	for output, want := range map[string]string{
		"structurizr-cli: 2025.11.09\nstructurizr-java: 5.0.2\nJava: 21\n": "2025.11.09",
		"\nStructurizr CLI 2024.1\n":                                       "Structurizr CLI 2024.1",
		"  structurizr-cli:   1.2.3  \n":                                   "1.2.3",
		"":                                                                 "",
	} {
		if got := projection.ParseCLIVersion(output); got != want {
			t.Errorf("ParseCLIVersion(%q) = %q, want %q", output, got, want)
		}
	}
}

func structurizrTestVerifyPinnedMatches(t *testing.T) {
	t.Helper()

	cli := fakeCLI(t, t.TempDir(), projection.PinnedStructurizrCLI, "  :")

	version, err := projection.VerifyPinned(context.Background(), cli, os.Getenv)
	if err != nil || version != projection.PinnedStructurizrCLI {
		t.Errorf("VerifyPinned = %q, %v", version, err)
	}
}

func structurizrTestVerifyPinnedOtherVersion(t *testing.T) {
	t.Helper()

	cli := fakeCLI(t, t.TempDir(), "2024.1.0", "  :")

	version, err := projection.VerifyPinned(context.Background(), cli, os.Getenv)
	de := asDiag(t, err)
	want := structurizrTestDiag{code: diag.DGM004, exit: diag.Toolchain, contains: "found 2024.1.0, pinned " + projection.PinnedStructurizrCLI}

	if !want.matches(de) || version != "2024.1.0" {
		t.Errorf("err = %v, version = %q", err, version)
	}
}

func structurizrTestVerifyPinnedMissing(t *testing.T) {
	t.Helper()

	_, err := projection.VerifyPinned(context.Background(), filepath.Join(t.TempDir(), "missing"), os.Getenv)

	structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.PRJ001, exit: diag.Toolchain})
}

func structurizrTestVerifyPinnedNoVersionOutput(t *testing.T) {
	t.Helper()

	cli := structurizrTestScript(t, "quiet.sh", "#!/bin/sh\nexit 0\n")

	_, err := projection.VerifyPinned(context.Background(), cli, os.Getenv)

	structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.DGM004, exit: diag.Toolchain})
}

func structurizrTestVerifyPinnedVersionFails(t *testing.T) {
	t.Helper()

	cli := structurizrTestScript(t, "failing.sh", "#!/bin/sh\nexit 3\n")

	_, err := projection.VerifyPinned(context.Background(), cli, os.Getenv)

	structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.DGM004, contains: "exit code 3"})
}

func TestVerifyPinned(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		run  func(t *testing.T)
		name string
	}{
		{structurizrTestVerifyPinnedMatches, "pinned"},
		{structurizrTestVerifyPinnedOtherVersion, "other version"},
		{structurizrTestVerifyPinnedMissing, "missing"},
		{structurizrTestVerifyPinnedNoVersionOutput, "no version output"},
		{structurizrTestVerifyPinnedVersionFails, "version command fails"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t)
		})
	}
}

const simpleDSL = "workspace {\n  model {\n    a = person \"A\"\n  }\n}\n"

// structurizrTestRun is the input of confinedRun. An empty version is the
// pinned one and an empty root is a fresh directory.
type structurizrTestRun struct {
	version    string
	exportBody string
	dsl        string
	root       string
	allow      []string
}

// confinedRun runs RunConfined with a fake CLI.
func confinedRun(t *testing.T, in *structurizrTestRun) (projection.ConfinedResult, error) {
	t.Helper()

	version, root := in.version, in.root
	if version == "" {
		version = projection.PinnedStructurizrCLI
	}

	if root == "" {
		root = t.TempDir()
	}

	cli := fakeCLI(t, t.TempDir(), version, in.exportBody)

	return projection.RunConfined(context.Background(), &projection.ConfinedOptions{
		CLI: cli, Format: projection.FormatMermaid, WorkspaceRoot: root, DSL: []byte(in.dsl),
		AllowIncludes: in.allow, Getenv: os.Getenv,
	})
}

// structurizrTestPaths gives the paths of files.
func structurizrTestPaths(files []projection.ExportFile) []string {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}

	return paths
}

func TestRunConfinedExportsAndCopiesTheOutputOut(t *testing.T) {
	t.Parallel()

	res, err := confinedRun(t, &structurizrTestRun{
		exportBody: `  cp workspace.dsl "$7/b.mmd"; mkdir "$7/sub"; echo second > "$7/sub/a.mmd"; echo "$@" > "$7/args.txt"`,
		dsl:        simpleDSL,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Version != projection.PinnedStructurizrCLI {
		t.Errorf("Version = %q", res.Version)
	}

	if got, want := strings.Join(structurizrTestPaths(res.Files), ","), "args.txt,b.mmd,sub/a.mmd"; got != want {
		t.Errorf("files = %s, want %s", got, want)
	}

	if got := string(res.Files[1].Data); got != simpleDSL {
		t.Errorf("b.mmd = %q", got)
	}

	if got, want := strings.TrimSpace(string(res.Files[0].Data)), "export -workspace workspace.dsl -format mermaid -output out"; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// structurizrTestFileData gives the data of the exported file at path, or ""
// when there is none.
func structurizrTestFileData(files []projection.ExportFile, path string) string {
	for _, f := range files {
		if f.Path == path {
			return string(f.Data)
		}
	}

	return ""
}

// structurizrTestHomeIsScratch reports whether home names the scratch
// directory that pwd printed. pwd prints the physical path; HOME keeps the
// logical one (on macOS /var is a link to /private/var). The scratch directory
// is removed after the run, so only the unique base name is compared.
func structurizrTestHomeIsScratch(home, pwd string) bool {
	if home == "" || filepath.Base(home) != filepath.Base(pwd) {
		return false
	}

	return strings.HasPrefix(filepath.Base(pwd), "concordia-structurizr-")
}

func structurizrTestAssertCleanEnv(t *testing.T, env string) {
	t.Helper()

	for _, banned := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "ALL_PROXY", "AWS_SECRET", "GITHUB_TOKEN", "JAVA_TOOL_OPTIONS", "proxy.example"} {
		if strings.Contains(env, banned) {
			t.Errorf("the child environment contains %s:\n%s", banned, env)
		}
	}

	if !strings.Contains(env, "PATH="+os.Getenv("PATH")) {
		t.Errorf("the child environment has no PATH:\n%s", env)
	}
}

func TestRunConfinedChildEnvironmentHasNoProxyAndNoCredentials(t *testing.T) {
	// t.Setenv forbids t.Parallel.
	for key, value := range map[string]string{
		"HTTP_PROXY":            "http://proxy.example:3128",
		"HTTPS_PROXY":           "http://proxy.example:3128",
		"http_proxy":            "http://proxy.example:3128",
		"ALL_PROXY":             "http://proxy.example:3128",
		"AWS_SECRET_ACCESS_KEY": "secret",
		"GITHUB_TOKEN":          "secret",
		"JAVA_TOOL_OPTIONS":     "-Dhttp.proxyHost=proxy.example",
	} {
		t.Setenv(key, value)
	}

	res, err := confinedRun(t, &structurizrTestRun{exportBody: `  env > "$7/env.txt"; pwd > "$7/pwd.txt"`, dsl: simpleDSL})
	if err != nil {
		t.Fatal(err)
	}

	env := structurizrTestFileData(res.Files, "env.txt")
	pwd := strings.TrimSpace(structurizrTestFileData(res.Files, "pwd.txt"))

	structurizrTestAssertCleanEnv(t, env)

	if home := envValue(env, "HOME"); !structurizrTestHomeIsScratch(home, pwd) {
		t.Errorf("HOME %q is not the scratch directory %q:\n%s", home, pwd, env)
	}

	if _, err := os.Stat(pwd); !os.IsNotExist(err) {
		t.Errorf("the scratch directory %s was not removed (%v)", pwd, err)
	}
}

// envValue gives the value of key in the output of env.
func envValue(env, key string) string {
	for line := range strings.SplitSeq(env, "\n") {
		if value, ok := strings.CutPrefix(line, key+"="); ok {
			return value
		}
	}

	return ""
}

func TestConfinedEnvIsSortedAndMinimal(t *testing.T) {
	t.Parallel()

	got := projection.ConfinedEnv(env(map[string]string{
		"PATH": "/bin", "JAVA_HOME": "/jdk", "HTTP_PROXY": "x", "HOME": "/home/u", "JAVA_TOOL_OPTIONS": "x",
	}), "/scratch")

	if runtime.GOOS == "windows" {
		t.Skip("Windows adds variables")
	}

	if want := "HOME=/scratch,JAVA_HOME=/jdk,PATH=/bin,TMPDIR=/scratch"; strings.Join(got, ",") != want {
		t.Errorf("ConfinedEnv = %v, want %s", got, want)
	}
}

func TestRunConfinedRefusesDirectivesWithoutStartingTheCLI(t *testing.T) {
	t.Parallel()

	for name, dsl := range map[string]string{
		"include":              "workspace {\n  !include part.dsl\n}\n",
		"include uppercase":    "workspace {\n  !INCLUDE part.dsl\n}\n",
		"indented include":     "workspace {\n\t  !include \"part.dsl\"\n}\n",
		"continued":            "workspace {\n  !include \\\n  part.dsl\n}\n",
		"script file":          "workspace {\n  !script run.groovy\n}\n",
		"script inline":        "workspace {\n  !script groovy {\n    println 1\n  }\n}\n",
		"plugin":               "workspace {\n  !plugin com.example.Plugin\n}\n",
		"docs":                 "workspace {\n  !docs docs\n}\n",
		"adrs":                 "workspace {\n  !adrs decisions\n}\n",
		"import":               "workspace {\n  !import other.json\n}\n",
		"https url":            "workspace {\n  !include https://example.com/part.dsl\n}\n",
		"file url":             "workspace {\n  !include file:///etc/passwd\n}\n",
		"url in docs":          "workspace {\n  !docs https://example.com/docs\n}\n",
		"substitution":         "workspace {\n  !include ${PART}\n}\n",
		"in a comment":         "workspace {\n  /*\n  !include part.dsl\n  */\n}\n",
		"CRLF":                 "workspace {\r\n  !include part.dsl\r\n}\r\n",
		"absolute path":        "workspace {\n  !include /etc/part.dsl\n}\n",
		"parent directory":     "workspace {\n  !include ../part.dsl\n}\n",
		"missing path":         "workspace {\n  !include\n}\n",
		"not in allowIncludes": "workspace {\n  !include other.dsl\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			testutil.WriteTree(t, root, map[string]string{"part.dsl": "x\n", "other.dsl": "y\n"})

			marker := filepath.Join(t.TempDir(), "started")

			// Only other.dsl is allowed, and only some cases name it.
			_, err := confinedRun(t, &structurizrTestRun{exportBody: "  touch " + marker, dsl: dsl, allow: []string{"unused.dsl"}, root: root})

			structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.DGM003, exit: diag.InvalidInput})
			structurizrTestNotCreated(t, marker, "the CLI was started")
		})
	}
}

func TestRunConfinedDoesNotStartTheCLIForARefusedDirectiveEvenForVersion(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "ran")
	cli := structurizrTestScript(t, "cli.sh", "#!/bin/sh\ntouch "+marker+"\n")

	_, err := projection.RunConfined(context.Background(), &projection.ConfinedOptions{
		CLI: cli, Format: projection.FormatJSON, WorkspaceRoot: t.TempDir(), DSL: []byte("workspace {\n !script x.groovy\n}\n"),
	})

	structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.DGM003})
	structurizrTestNotCreated(t, marker, "the CLI started")
}

func TestRunConfinedAcceptsAnAllowedIncludeAndCopiesItIntoTheScratchDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{
		"views/part.dsl":   "!include nested.dsl\n",
		"views/nested.dsl": "// nested\n",
	})

	res, err := confinedRun(t, &structurizrTestRun{
		exportBody: `  cat views/part.dsl views/nested.dsl > "$7/copied.txt"`,
		dsl:        "workspace {\n  !include views/part.dsl\n}\n",
		allow:      []string{"views/part.dsl", "./views/nested.dsl"},
		root:       root,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := string(res.Files[0].Data), "!include nested.dsl\n// nested\n"; got != want {
		t.Errorf("copied = %q, want %q", got, want)
	}
}

func TestRunConfinedScansTheAllowedIncludesToo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{"part.dsl": "!script evil.groovy\n"})

	_, err := confinedRun(t, &structurizrTestRun{
		exportBody: "  :", dsl: "workspace {\n  !include part.dsl\n}\n", allow: []string{"part.dsl"}, root: root,
	})

	structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.DGM003, contains: "part.dsl:1: !script"})
}

// structurizrTestIsUnavailable reports whether err is an unavailable include
// at path.
func structurizrTestIsUnavailable(err error, path string) bool {
	var unavailable *projection.IncludeUnavailableError

	if !errors.Is(err, projection.ErrIncludeUnavailable) {
		return false
	}

	return errors.As(err, &unavailable) && unavailable.Path == path
}

func TestRunConfinedAllowedIncludeThatCannotBeReadIsUnavailable(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "started")

	_, err := confinedRun(t, &structurizrTestRun{
		exportBody: "  touch " + marker, dsl: "workspace {\n  !include missing.dsl\n}\n", allow: []string{"missing.dsl"},
	})
	if !structurizrTestIsUnavailable(err, "missing.dsl") {
		t.Fatalf("err = %v", err)
	}

	structurizrTestNotCreated(t, marker, "the CLI was started")
}

func TestRunConfinedRefusesAnAllowedIncludeThatLeavesTheWorkspaceBySymlink(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	testutil.WriteTree(t, outside, map[string]string{"secret.dsl": "x\n"})

	root := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret.dsl"), filepath.Join(root, "link.dsl")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	_, err := confinedRun(t, &structurizrTestRun{
		exportBody: "  :", dsl: "workspace {\n  !include link.dsl\n}\n", allow: []string{"link.dsl"}, root: root,
	})

	structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.DGM003, contains: "outside the workspace"})
}

func TestRunConfinedRefusesASymlinkInsideAnAllowedDirectory(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	testutil.WriteTree(t, outside, map[string]string{"secret.txt": "secret\n"})

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{"docs/a.md": "# A\n"})

	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "docs", "leak.md")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	marker := filepath.Join(t.TempDir(), "exported")

	_, err := confinedRun(t, &structurizrTestRun{
		exportBody: "  touch " + marker, dsl: "workspace {\n  !docs docs\n}\n", allow: []string{"docs"}, root: root,
	})

	structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.DGM003, contains: "docs/leak.md"})
	structurizrTestNotCreated(t, marker, "the CLI was started")
}

func TestRunConfinedAllowsADirectoryIncludeAndRefusesItsDirectives(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{"docs/a.md": "# A\n", "docs/b.md": "!include not-dsl\n"})

	// `!docs` content is documentation: it is copied and not scanned as DSL.
	res, err := confinedRun(t, &structurizrTestRun{
		exportBody: `  cat docs/a.md > "$7/a.txt"`, dsl: "workspace {\n  !docs docs\n}\n", allow: []string{"docs"}, root: root,
	})
	if err != nil || string(res.Files[0].Data) != "# A\n" {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestRunConfinedRefusesAnotherCLIVersionBeforeExporting(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "exported")

	_, err := confinedRun(t, &structurizrTestRun{version: "2024.1.0", exportBody: "  touch " + marker, dsl: simpleDSL})

	structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.DGM004, exit: diag.Toolchain})
	structurizrTestNotCreated(t, marker, "the export ran")
}

// structurizrTestSingleFileAtPin reports whether res has exactly one file and
// the pinned version, without an error.
func structurizrTestSingleFileAtPin(res projection.ConfinedResult, err error) bool {
	if err != nil || len(res.Files) != 1 {
		return false
	}

	return res.Version == projection.PinnedStructurizrCLI
}

func TestRunConfinedSkipsTheVersionCheckWhenTheCallerVerified(t *testing.T) {
	t.Parallel()

	cli := fakeCLI(t, t.TempDir(), "2024.1.0", `  echo ok > "$7/f.txt"`)

	res, err := projection.RunConfined(context.Background(), &projection.ConfinedOptions{
		CLI: cli, Format: projection.FormatDot, WorkspaceRoot: t.TempDir(), DSL: []byte(simpleDSL), VerifiedPin: true, Getenv: os.Getenv,
	})
	if !structurizrTestSingleFileAtPin(res, err) {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestRunConfinedExportFailureIsPRJ002(t *testing.T) {
	t.Parallel()

	_, err := confinedRun(t, &structurizrTestRun{exportBody: "  echo broken >&2; exit 4", dsl: simpleDSL})

	structurizrTestExpectDiag(t, err, structurizrTestDiag{code: diag.PRJ002, contains: "broken"})
}

func TestWriteExportFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := projection.WriteExportFiles(dir, []projection.ExportFile{{Path: "a/b.txt", Data: []byte("x")}, {Path: "c.txt", Data: []byte("y")}})
	if err != nil {
		t.Fatal(err)
	}

	if testutil.ReadFile(t, filepath.Join(dir, "a", "b.txt")) != "x" {
		t.Error("files differ")
	}

	if testutil.ReadFile(t, filepath.Join(dir, "c.txt")) != "y" {
		t.Error("files differ")
	}
}
