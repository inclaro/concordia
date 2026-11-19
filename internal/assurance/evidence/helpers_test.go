package evidence_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/procexec"
	"github.com/inclaro/concordia/internal/workspace"
)

const revision = "0123456789abcdef0123456789abcdef01234567"

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// repoRoot finds the directory with go.mod above the test directory.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}

		dir = parent
	}
}

// fixtures is test/fixtures/assurance.
func fixtures(t *testing.T) string {
	t.Helper()

	return filepath.Join(repoRoot(t), "test", "fixtures", "assurance")
}

func writeFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// bareStatement is an unsigned in-toto statement.
func bareStatement(t *testing.T, predicateType string, predicate any, subjectDigest string) []byte {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "predicateType": predicateType, "predicate": predicate,
		"subject": []map[string]any{{"name": "subject", "digest": map[string]string{"sha256": subjectDigest}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// bundle wraps a statement in a Sigstore bundle, the form that cosign can verify.
func bundle(t *testing.T, payload []byte) []byte {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"mediaType":            "application/vnd.dev.sigstore.bundle.v0.3+json",
		"verificationMaterial": map[string]any{"publicKey": map[string]string{"hint": "aGludA=="}},
		"dsseEnvelope": map[string]any{
			"payloadType": statement.PayloadType,
			"payload":     base64.StdEncoding.EncodeToString(payload),
			"signatures":  []map[string]string{{"sig": "c2ln"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// checkResult is the predicate of a check result.
func checkResult(check, outcome, environment string) map[string]any {
	return map[string]any{
		"check": check, "outcome": outcome, "revision": revision, "environment": environment,
		"evaluator": map[string]string{"name": "go-test", "version": "1.0.0"},
		"time":      now.Add(-time.Hour).Format(time.RFC3339), "covers": []string{"shop"},
	}
}

// fakeCosign writes a cosign stand-in. Every call is appended to the log. `version` answers like cosign 3; a call
// whose arguments contain `refuse` is refused with exit 1; any other call verifies.
func fakeCosign(t *testing.T) (exe, logPath string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake cosign is a shell script")
	}

	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	exe = filepath.Join(dir, "cosign")

	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + logPath + "\n" +
		"if [ \"$1\" = version ]; then echo 'GitVersion:    v3.0.2'; exit 0; fi\n" +
		"case \"$*\" in *refuse*) echo 'no matching signatures' >&2; exit 1;; esac\n" +
		"exit 0\n"

	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return exe, logPath
}

// calls reads the log of the fake cosign; "" when it never ran.
func calls(t *testing.T, logPath string) string {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return ""
	}

	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// producer is a keyless producer trusted for the claim kinds.
func producer(id string, kinds ...string) workspace.Producer {
	return workspace.Producer{
		ID: id, Claims: kinds,
		Authentication: workspace.ProducerAuthentication{Method: workspace.AuthCosignKeyless, Issuer: "https://issuer.example", Identity: "ci@example"},
	}
}

// options builds Options for a root and a configuration. Cosign comes from the fake, the clock is fixed.
func options(root string, cfg *workspace.Assurance, cosignExe string) *evidence.Options {
	return &evidence.Options{
		Root: root, Assurance: cfg, Environments: map[string]string{"production": "prod-cluster", "staging": ""},
		Context: assurance.Context{Now: now, Revision: revision},
		Getenv: func(name string) string {
			if name == "COSIGN" {
				return cosignExe
			}

			return ""
		},
	}
}

// forbiddenRun fails the test when a program starts.
func forbiddenRun(t *testing.T) render.Runner {
	t.Helper()

	return func(_ context.Context, name string, _ []string, _ procexec.Options) (procexec.Result, error) {
		t.Errorf("a program started: %s", name)

		return procexec.Result{}, nil
	}
}

// countingRun counts the starts and runs the program.
type countingRun struct{ n atomic.Int32 }

func (c *countingRun) run(ctx context.Context, name string, args []string, opts procexec.Options) (procexec.Result, error) {
	c.n.Add(1)

	return procexec.Run(ctx, name, args, opts)
}

// fakeRenderer writes a renderer script. It answers `version` with v4.3.0 and prints body for any other call, and gives
// the Options pin of the script and the locator that finds it as `helm`.
func fakeRenderer(t *testing.T, body string) (render.Locator, render.Pins) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake renderer is a shell script")
	}

	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh for a fake renderer")
	}

	path := filepath.Join(t.TempDir(), "helm")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo v4.3.0; exit 0; fi\n" + body + "\n"

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(script))

	locate := func(name string) (string, error) {
		if name == "helm" {
			return path, nil
		}

		return "", exec.ErrNotFound
	}

	return locate, render.Pins{"helm": {Version: "v4.3.0", SHA256: digest.Prefix + hex.EncodeToString(sum[:])}}
}

const (
	stableManifest = `printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n  namespace: shop\n'`
	// randomManifest names the resource after the process id, so two renders differ.
	randomManifest = `printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x-%s\n  namespace: shop\n' "$$"`
)

func plainSource(environment, path string) workspace.RenderSource {
	return workspace.RenderSource{Environment: environment, Tool: workspace.RenderToolPlain, Path: path}
}

func helmSource(environment string) workspace.RenderSource {
	return workspace.RenderSource{
		Environment: environment, Tool: workspace.RenderToolHelm, Path: "helm-shop/chart", Release: "shop", Namespace: "shop",
	}
}

func rendering(sources ...workspace.RenderSource) *workspace.AssuranceRendering {
	return &workspace.AssuranceRendering{Sources: sources}
}
