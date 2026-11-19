package integration_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// adrCLI is a copy of the decision fixture that the CLI runs on, with a fake Git revision and the recording of the
// provider.
type adrCLI struct {
	dir  string
	deps cli.Deps
}

// newADRCLI copies the fixture and the recording to a temporary directory. edit changes the text of the recording.
func newADRCLI(t *testing.T, edit func(recording string) string) *adrCLI {
	t.Helper()

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(adrFixtureDir)); err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}

	recording := adrRecordingFile

	if edit != nil {
		data, err := os.ReadFile(adrRecordingFile)
		if err != nil {
			t.Fatal(err)
		}

		recording = filepath.Join(t.TempDir(), "adr.json")
		if err := os.WriteFile(recording, []byte(edit(string(data))), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	client, err := observation.LoadReplayClient(recording)
	if err != nil {
		t.Fatalf("load the recording: %v", err)
	}

	var (
		files   []string
		content = map[string]string{}
	)

	err = fs.WalkDir(os.DirFS(dir), ".", func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		data, readErr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		files = append(files, p)
		content[p] = string(data)

		return readErr
	})
	if err != nil {
		t.Fatalf("list the fixture: %v", err)
	}

	rev := client.HeadSHA()

	return &adrCLI{dir: dir, deps: cli.Deps{
		NewClient: func(*workspace.Workspace, *cli.Context) observation.Client { return client },
		NewGit: func(string) workspace.GitPort {
			return &workspace.FakeGit{Rev: rev, Files: files, FilesAt: map[string]map[string]string{rev: content}}
		},
	}}
}

// run runs a concordia command in the copy and gives the exit code and the two output streams.
func (c *adrCLI) run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	stdio := cli.IO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, Env: map[string]string{}, Cwd: c.dir}
	code = cli.Run(t.Context(), args, &stdio, c.deps, cli.BuildInfo{Version: "test"})

	return code, out.String(), errOut.String()
}

// extract unpacks the bundle of the copy and gives its manifest and its directory.
func (c *adrCLI) extract(t *testing.T) (manifest *bundle.Manifest, dir string) {
	t.Helper()

	dir = t.TempDir()

	manifest, err := bundle.Open(filepath.Join(c.dir, "build", "concordia", "conformance.tar.gz"), dir)
	if err != nil {
		t.Fatalf("open the bundle: %v", err)
	}

	return manifest, dir
}

// TestADRExportBundleHoldsTheRegister runs `check` and `export bundle` on the decision fixture: the register is an
// input of the bundle (`inputs/governance/decisions.yaml`) and the digest of the manifest is the digest of the file.
func TestADRExportBundleHoldsTheRegister(t *testing.T) {
	c := newADRCLI(t, nil)

	if code, stdout, stderr := c.run(t, "check"); code != 1 {
		t.Fatalf("check exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	if code, stdout, stderr := c.run(t, "export", "bundle"); code != 0 {
		t.Fatalf("export bundle exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	manifest, dir := c.extract(t)

	register, err := os.ReadFile(filepath.Join(c.dir, "governance", "decisions.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	inBundle, err := os.ReadFile(filepath.Join(dir, "inputs", "governance", "decisions.yaml"))
	if err != nil {
		t.Fatalf("the register is not under inputs/: %v", err)
	}

	if !bytes.Equal(inBundle, register) {
		t.Errorf("the register in the bundle differs from the file")
	}

	want := digest.OfBytes(register)
	if got := manifest.Integrity.Files["inputs/governance/decisions.yaml"]; got != want {
		t.Errorf("manifest digest of the register = %q, want %q", got, want)
	}

	// The input digest of results.json is the same digest.
	results, err := os.ReadFile(filepath.Join(dir, "results", "results.json"))
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Inputs struct {
			Decisions []struct {
				Path   string `json:"path"`
				Digest string `json:"digest"`
			} `json:"decisions"`
		} `json:"inputs"`
	}

	if err := json.Unmarshal(results, &parsed); err != nil {
		t.Fatal(err)
	}

	found := false

	for _, in := range parsed.Inputs.Decisions {
		if in.Path == "governance/decisions.yaml" {
			found = true

			if in.Digest != want {
				t.Errorf("inputs.decisions digest = %q, want %q", in.Digest, want)
			}
		}
	}

	if !found {
		t.Errorf("inputs.decisions = %+v, want governance/decisions.yaml", parsed.Inputs.Decisions)
	}
}

// adrTitleSecret is a token-shaped value (`ghp_` and 36 characters) that the title of ADR-0003 holds in the recording.
const adrTitleSecret = "ghp_aB3dE5gH7jK9mN1pQ3rS5tU7vW9xY1zA3bC5"

// TestADRTitleSecretIsRedactedAndReported puts a token in the title of ADR-0003: `check` reports the redaction
// (CO-REP-005 on stderr, no value), results.json holds the marker and no secret, and `export bundle` lists the
// redaction in the manifest and holds no secret in any file.
func TestADRTitleSecretIsRedactedAndReported(t *testing.T) {
	c := newADRCLI(t, func(recording string) string {
		return strings.ReplaceAll(recording, "ADR-0003: Orders owns its database", "ADR-0003: Use token "+adrTitleSecret+" for the registry")
	})

	code, stdout, stderr := c.run(t, "check")
	if code != 1 {
		t.Fatalf("check exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	if !strings.Contains(stderr, "CO-REP-005") {
		t.Errorf("stderr has no CO-REP-005 notice:\n%s", stderr)
	}

	for _, output := range []string{"finding", "summary"} {
		if !strings.Contains(stderr, output) {
			t.Errorf("stderr has no notice for the output %q:\n%s", output, stderr)
		}
	}

	if strings.Contains(stdout+stderr, adrTitleSecret) || strings.Contains(stdout+stderr, adrTitleSecret[:8]) {
		t.Errorf("the secret is in the output of check")
	}

	results, err := os.ReadFile(filepath.Join(c.dir, "build", "concordia", "results.json"))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(results), adrTitleSecret[:8]) {
		t.Errorf("the secret is in results.json")
	}

	if !strings.Contains(string(results), "ADR-0003: Use token ") {
		t.Errorf("results.json lost the title around the secret")
	}

	code, stdout, stderr = c.run(t, "export", "bundle")
	if code != 0 {
		t.Fatalf("export bundle exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	if !strings.Contains(stderr, "CO-REP-005") || strings.Contains(stdout+stderr, adrTitleSecret[:8]) {
		t.Errorf("export bundle stderr = %q", stderr)
	}

	manifest, dir := c.extract(t)

	redacted := 0

	for _, r := range manifest.Redactions {
		if r.Path == "results/results.json" && r.Class == "token" {
			redacted += r.Count
		}
	}

	if redacted == 0 {
		t.Errorf("manifest redactions = %+v, want the title token in results/results.json", manifest.Redactions)
	}

	err = filepath.WalkDir(dir, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		data, readErr := os.ReadFile(p)
		if readErr == nil && bytes.Contains(data, []byte(adrTitleSecret[:8])) {
			t.Errorf("the secret is in the bundle file %s", p)
		}

		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
}
