package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// otherPin is a digest that no fake executable has.
const otherPin = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

// fileClient is a fake CBM client that has an executable file, so that Connect and `doctor` hash it.
type fileClient struct {
	observation.Client
	path string
}

func (c fileClient) Executable() string { return c.path }

// fakeExecutable writes a file that stands for the CBM executable and gives its resolved path and its digest.
func fakeExecutable(t *testing.T) (path, sum string) {
	t.Helper()

	file := filepath.Join(t.TempDir(), cbmBinaryName())
	if err := os.WriteFile(file, []byte("#!/bin/sh\necho 0.12.1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}

	sum, err = digest.OfFile(resolved)
	if err != nil {
		t.Fatal(err)
	}

	return resolved, sum
}

// fileDeps are the dependencies of a healthy CBM whose executable is the file.
func fileDeps(file string) cli.Deps {
	return clientDeps(fileClient{Client: fakeCbm("0.12.1", "doctor-repo"), path: file}, okProbe)
}

// cbmBuildDeps are the dependencies that find CBM 0.12.1 with an executable of the digest.
func cbmBuildDeps(sum string) cli.Deps {
	return cli.Deps{Cbm: &cli.CbmDeps{Find: func(context.Context) cli.CbmBuild { return cli.CbmBuild{Version: "0.12.1", SHA256: sum} }}}
}

// initPinned runs `init` with a CBM whose executable has the digest, and gives the directory.
func initPinned(t *testing.T, sum string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "doctor-repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	deps := cbmBuildDeps(sum)

	if res := initIn(t, dir, deps); res.Code != 0 {
		t.Fatalf("init: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	return dir
}

func TestDoctorCbmShowsThePathAndTheDigestOfTheExecutable(t *testing.T) {
	t.Parallel()

	file, sum := fakeExecutable(t)
	dir := initialized(t)

	res := runWith(t, &runConfig{Dir: dir, Deps: fileDeps(file)}, "doctor", "--json")
	requireExit0(t, &res)

	cbm := doctorCbmItem(t, res.Stdout)
	if cbm["sha256"] != sum || cbm["path"] != file {
		t.Errorf("cbm = %v, want path %s and sha256 %s", cbm, file, sum)
	}

	text := runWith(t, &runConfig{Dir: dir, Deps: fileDeps(file)}, "doctor")
	expectStdoutHas(t, text.Stdout, "  executable "+file+" "+sum+"\n")
}

func TestDoctorCbmWithoutAnExecutableHasNoDigest(t *testing.T) {
	t.Parallel()

	res := doctorRun(t, initialized(t), nil, "--json")

	cbm := doctorCbmItem(t, res.Stdout)
	if _, has := cbm["sha256"]; has {
		t.Errorf("cbm = %v, want no sha256", cbm)
	}
}

func TestDoctorLockDigestMismatchDegradesTheLock(t *testing.T) {
	t.Parallel()

	file, sum := fakeExecutable(t)
	dir := initPinned(t, otherPin)

	res := runWith(t, &runConfig{Dir: dir, Deps: fileDeps(file)}, "doctor", "--json")
	requireExit0(t, &res)

	lock := capabilities(t, res.Stdout)["lock"]
	if !lock.hasStatus("degraded", "lock has cbm sha256 "+otherPin, "found "+sum, file) {
		t.Errorf("lock = %+v", lock)
	}

	pinned := initPinned(t, sum)
	res = runWith(t, &runConfig{Dir: pinned, Deps: fileDeps(file)}, "doctor", "--json")

	if lock := capabilities(t, res.Stdout)["lock"]; lock.Status != "available" {
		t.Errorf("a lock with the digest of the executable: lock = %+v", lock)
	}
}

func TestInitWritesTheCbmDigestOnlyWhenItIsKnown(t *testing.T) {
	t.Parallel()

	pinned := readFile(t, initPinned(t, otherPin), ".concordia.lock")
	if !strings.Contains(pinned, "    sha256: "+otherPin+"\n") {
		t.Errorf("lock lacks the digest:\n%s", pinned)
	}

	dir := initialized(t)
	if unpinned := readFile(t, dir, ".concordia.lock"); strings.Contains(unpinned, "\n    sha256:") {
		t.Errorf("a lock of an unknown executable has a digest:\n%s", unpinned)
	}
}

func TestLockUpdatePinsTheCbmDigestAndReportsTheChange(t *testing.T) {
	t.Parallel()

	setup := newChkSetup(t, map[string]string{".concordia.lock": lockText(observation.NormalizationVersion, "")}, nil)
	setup.cfg.Deps.Cbm = cbmBuildDeps(otherPin).Cbm

	res := setup.run(t, "lock", "update")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	wantLockStdout(t, res.Stdout, "cbm-executable: unrecorded -> "+otherPin)

	if lock := readFile(t, setup.dir, ".concordia.lock"); !strings.Contains(lock, "    sha256: "+otherPin+"\n") {
		t.Errorf("lock lacks the digest:\n%s", lock)
	}

	again := setup.run(t, "lock", "update")
	if again.Code != 0 || strings.Contains(again.Stdout, "cbm-executable") {
		t.Errorf("second update: exit code = %d, stdout = %q", again.Code, again.Stdout)
	}
}

// pinnedLockText is the text of a lock that records the digest of the CBM executable.
func pinnedLockText(sum string) string {
	return strings.Replace(lockText(observation.NormalizationVersion, ""), "adapterSchema: concordia-cbm/v2\n", "adapterSchema: concordia-cbm/v2\n    sha256: "+sum+"\n", 1)
}

// checkWithExecutable runs `check` on a workspace with the lock text and a CBM client that has the executable file.
// It gives the decoded results.json.
func checkWithExecutable(t *testing.T, lock, file string) map[string]any {
	t.Helper()

	setup := newChkSetup(t, map[string]string{".concordia.lock": lock}, nil)
	graph := setup.graph
	setup.cfg.Deps.NewClient = func(*workspace.Workspace, *cli.Context) observation.Client {
		return fileClient{Client: observation.NewFakeClient(graph.Handler, ""), path: file}
	}

	if res := setup.run(t, "check"); res.Code == 2 || res.Code == 3 || res.Code == 5 {
		t.Fatalf("check: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var results map[string]any
	if err := json.Unmarshal([]byte(readFile(t, setup.dir, "build/concordia/results.json")), &results); err != nil {
		t.Fatal(err)
	}

	return results
}

// resultsDiagnosticText gives the messages of the diagnostics of results.json in one text.
func resultsDiagnosticText(results map[string]any) string {
	list, _ := results["diagnostics"].([]any)

	var out strings.Builder

	for _, item := range list {
		entry, _ := item.(map[string]any)
		code, _ := entry["code"].(string)
		message, _ := entry["message"].(string)

		out.WriteString(code + " " + message + "\n")
	}

	return out.String()
}

func TestCheckRecordsTheCbmDigestInTheToolchain(t *testing.T) {
	t.Parallel()

	file, sum := fakeExecutable(t)
	results := checkWithExecutable(t, lockText(observation.NormalizationVersion, ""), file)

	toolchain, _ := results["toolchain"].(map[string]any)
	cbm, _ := toolchain["cbm"].(map[string]any)

	if cbm["sha256"] != sum {
		t.Errorf("toolchain.cbm = %v, want sha256 %s", cbm, sum)
	}

	if text := resultsDiagnosticText(results); strings.Contains(text, "CBM executable") {
		t.Errorf("a lock without a digest gave a warning:\n%s", text)
	}
}

func TestCheckWarnsWhenTheExecutableDiffersFromTheLockDigest(t *testing.T) {
	t.Parallel()

	file, sum := fakeExecutable(t)
	text := resultsDiagnosticText(checkWithExecutable(t, pinnedLockText(otherPin), file))

	for _, want := range []string{"CO-WRK-003", otherPin, sum, file, "concordia lock update"} {
		if !strings.Contains(text, want) {
			t.Errorf("the diagnostics lack %q:\n%s", want, text)
		}
	}

	if equal := resultsDiagnosticText(checkWithExecutable(t, pinnedLockText(sum), file)); strings.Contains(equal, "CBM executable") {
		t.Errorf("a lock with the digest of the executable gave a warning:\n%s", equal)
	}
}
