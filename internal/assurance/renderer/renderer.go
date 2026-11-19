// Package renderer finds the Helm and Kustomize executables and gives their identity: the version they report and
// the digest of the executable file. `lock update` writes this identity as the pin, `doctor` and the render
// package compare it with the pin of the lockfile. Keeping the probe here gives one definition of "the identity of
// a renderer", so the pin that `lock update` writes is the one the render step later compares.
//
// The package only reads: it starts the renderer with `version` and nothing else, never a chart or an overlay.
package renderer

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/procexec"
	"github.com/inclaro/concordia/internal/workspace"
)

// versionTimeout bounds one `version` call.
const versionTimeout = 30 * time.Second

// versionPattern finds the semantic version in the output of `helm version --short` (`v4.3.1+gc0d08d0`) and of
// `kustomize version` (`v5.8.3`, or `{Version:kustomize/v5.0.0 ...}` from older builds).
var versionPattern = regexp.MustCompile(`v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?`)

// Tools are the renderers that need a pin. `plain` runs no program.
func Tools() []string { return []string{workspace.RenderToolHelm, workspace.RenderToolKustomize} }

// EnvVar names the environment variable that overrides the executable of a renderer tool (`HELM`, `KUSTOMIZE`),
// as `COSIGN` does for cosign.
func EnvVar(tool string) string { return strings.ToUpper(tool) }

// Found is a renderer that was found: where it is and what it is.
type Found struct {
	// Path is the executable that was resolved (an OS path).
	Path string
	// Pin is the identity of the executable.
	Pin workspace.RendererPin
}

// versionArgs gives the arguments that print the version of a tool.
func versionArgs(tool string) ([]string, bool) {
	switch tool {
	case workspace.RenderToolHelm:
		return []string{"version", "--short"}, true
	case workspace.RenderToolKustomize:
		return []string{"version"}, true
	}

	return nil, false
}

// Path gives the executable of a renderer tool: the environment variable of EnvVar, else the one on PATH (the
// repository puts `.bin/` on PATH through `mise`, where `tools:install` installs the pinned build). A tool that is not
// found is a CO-PRV-003 error (exit 5).
func Path(tool string, getenv func(string) string) (string, error) {
	if _, ok := versionArgs(tool); !ok {
		return "", diag.Fail(diag.PRV003, diag.Detail(tool+" is not a renderer that needs a pin"))
	}

	if path := getenv(EnvVar(tool)); path != "" {
		return path, nil
	}

	path, err := exec.LookPath(tool)
	if err != nil {
		return "", diag.Fail(diag.PRV003, diag.Detail(tool+" was not found on PATH"))
	}

	return path, nil
}

// Probe finds a renderer tool and gives its identity. The version is the semantic version that the tool reports
// (build metadata after "+" is dropped, because the digest already names the build); the digest is
// `sha256:<hex>` of the executable file. A tool that is missing, cannot run, or reports no version is a CO-PRV-003
// error (exit 5).
func Probe(ctx context.Context, tool string, getenv func(string) string) (Found, error) {
	path, err := Path(tool, getenv)
	if err != nil {
		return Found{}, err
	}

	args, _ := versionArgs(tool)

	res, err := procexec.Run(ctx, path, args, procexec.Options{Timeout: versionTimeout})

	var spawn *procexec.SpawnError

	switch {
	case errors.As(err, &spawn):
		return Found{}, diag.Fail(diag.PRV003, diag.Detail(spawn.Error()))
	case err != nil:
		return Found{}, diag.Fail(diag.PRV003, diag.Detail(tool+" version failed: "+err.Error()))
	case res.TimedOut:
		return Found{}, diag.Fail(diag.PRV003, diag.Detail(tool+" version timed out after "+versionTimeout.String()))
	case res.Code != 0:
		return Found{}, diag.Fail(diag.PRV003, diag.Detail(tool+" version exited with code "+strconv.Itoa(res.Code)))
	}

	version, ok := ParseVersion(res.Stdout)
	if !ok {
		return Found{}, diag.Fail(diag.PRV003, diag.Detail("cannot read the version of "+tool+" from: "+strings.TrimSpace(res.Stdout)))
	}

	sum, err := digest.OfFile(path)
	if err != nil {
		return Found{}, diag.Fail(diag.PRV003, diag.Detail("cannot read "+path+": "+err.Error()))
	}

	return Found{Path: path, Pin: workspace.RendererPin{Tool: tool, Version: version, SHA256: sum}}, nil
}

// ParseVersion reads the version of the output of `helm version --short` or `kustomize version`, with a "v"
// prefix. ok is false when the text has no semantic version.
func ParseVersion(output string) (string, bool) {
	match := versionPattern.FindString(output)
	if match == "" {
		return "", false
	}

	return "v" + strings.TrimPrefix(match, "v"), true
}
