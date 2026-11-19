package render

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/assurance/renderer"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/procexec"
	"github.com/inclaro/concordia/internal/workspace"
)

// Runner starts a program. The default is procexec.Run, the one place that starts programs; a test can count or
// record the starts.
type Runner func(ctx context.Context, name string, args []string, opts procexec.Options) (procexec.Result, error)

// Locator finds a program by name, as exec.LookPath does. The default is the lookup of `lock update` and `doctor`
// (renderer.Path: the `HELM` and `KUSTOMIZE` variables, else PATH), so the program that is pinned is the one that runs.
type Locator func(name string) (string, error)

const (
	// probeTimeout bounds the version probe of a renderer.
	probeTimeout = 30 * time.Second
	// probeMaxBuffer bounds the output of the version probe.
	probeMaxBuffer = 1 << 20
)

// defaultLocator finds a renderer as `lock update` does, and any other program (bubblewrap) on PATH.
func defaultLocator(name string) (string, error) { return LocatorWith(os.Getenv)(name) }

// LocatorWith is the default Locator over an environment reader: the renderers by their `HELM` and `KUSTOMIZE`
// variables read from getenv, any other program on PATH. The CLI passes its own environment, not the process one.
func LocatorWith(getenv func(string) string) Locator {
	return func(name string) (string, error) {
		for _, tool := range renderer.Tools() {
			if tool == name {
				return renderer.Path(name, getenv)
			}
		}

		return lookPath(name)
	}
}

// NormalizeVersion gives a version for comparison: the semantic version without a leading `v` and without build
// metadata (`v4.3.0+gbec5b06` is `4.3.0`).
func NormalizeVersion(version string) string {
	version, _, _ = strings.Cut(strings.TrimSpace(version), "+")

	return strings.TrimPrefix(version, "v")
}

// normalizeDigest gives a digest for comparison: lower case, without the `sha256:` prefix.
func normalizeDigest(sum string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(sum)), digest.Prefix)
}

// versionArgs are the arguments that print the version: the ones of renderer.Probe, which writes the pin.
func versionArgs(tool string) []string {
	if tool == workspace.RenderToolHelm {
		return []string{"version", "--short"}
	}

	return []string{"version"}
}

// Identify gives the identity of the renderer executable at path in the form of the lockfile pin: the version that
// the tool reports (`v4.3.1`, as renderer.ParseVersion reads it) and `sha256:<hex>` of the executable file. The
// program starts with the scrubbed environment and no input.
func Identify(ctx context.Context, run Runner, tool, executable string) (Identity, error) {
	if run == nil {
		run = procexec.Run
	}

	sum, err := digest.OfFile(executable)
	if err != nil {
		return Identity{}, fmt.Errorf("read %s: %w", tool, err)
	}

	home, err := os.MkdirTemp("", "concordia-probe-")
	if err != nil {
		return Identity{}, fmt.Errorf("probe %s: %w", tool, err)
	}

	defer func() { _ = os.RemoveAll(home) }()

	res, err := run(ctx, executable, versionArgs(tool), procexec.Options{
		Dir: home, Env: childEnv(home), Timeout: probeTimeout, MaxBuffer: probeMaxBuffer,
	})
	if err != nil {
		return Identity{}, fmt.Errorf("probe %s: %w", tool, err)
	}

	if res.Code != 0 || res.TimedOut {
		return Identity{}, fmt.Errorf("probe %s: `version` did not finish with exit code 0", tool)
	}

	version, ok := renderer.ParseVersion(res.Stdout + "\n" + res.Stderr)
	if !ok {
		return Identity{}, fmt.Errorf("probe %s: no version in the output of `version`", tool)
	}

	return Identity{Name: tool, Version: version, SHA256: sum}, nil
}

// ResolveRenderer finds the renderer, reads its identity, and compares it with the lockfile pin. A renderer that is
// missing, not pinned, or different from the pin gives a CO-PRV-003 error (exit 5) that names the expected and the
// found identity: an unpinned render would break the determinism of the output. The result is the path of the
// executable (symbolic links resolved, so the digest is the digest of the file that runs) and its identity. A pin
// may be written with or without the `v` of the version and the `sha256:` of the digest.
func ResolveRenderer(ctx context.Context, run Runner, locate Locator, tool string, pins Pins) (string, Identity, error) {
	if locate == nil {
		locate = defaultLocator
	}

	found, err := locate(tool)
	if err != nil {
		return "", Identity{}, diag.Fail(diag.PRV003, diag.Detail(tool+" was not found on PATH"))
	}

	if resolved, rerr := filepath.EvalSymlinks(found); rerr == nil {
		found = resolved
	}

	ident, err := Identify(ctx, run, tool, found)
	if err != nil {
		if ctx.Err() != nil {
			return "", Identity{}, ctx.Err()
		}

		var spawn *procexec.SpawnError
		if errors.As(err, &spawn) || errors.Is(err, os.ErrNotExist) {
			return "", Identity{}, diag.Fail(diag.PRV003, diag.Detail(tool+" cannot be started: "+err.Error()))
		}

		return "", Identity{}, diag.Fail(diag.PRV003, diag.Detail(err.Error()))
	}

	pin, ok := pins[tool]
	if !ok || pin.SHA256 == "" {
		return "", Identity{}, diag.Fail(diag.PRV003, diag.Detail(fmt.Sprintf(
			"%s %s (%s) is not pinned in the lockfile; run `concordia lock update`", tool, ident.Version, ident.SHA256)))
	}

	if normalizeDigest(pin.SHA256) != normalizeDigest(ident.SHA256) || NormalizeVersion(pin.Version) != NormalizeVersion(ident.Version) {
		return "", Identity{}, diag.Fail(diag.PRV003, diag.Detail(fmt.Sprintf(
			"expected %s %s (%s), found %s %s (%s)", tool, pin.Version, pin.SHA256, tool, ident.Version, ident.SHA256)))
	}

	return found, ident, nil
}
