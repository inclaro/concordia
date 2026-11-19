package cli

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// CbmBuild is the version of a CBM executable and the digest of its file. A field that is empty is not known.
type CbmBuild struct {
	Version string
	// SHA256 is `sha256:<hex>` of the executable file.
	SHA256 string
}

// detectCbmVersion gives the version of the CBM executable, or "" when it is
// missing or its output has no version.
func detectCbmVersion(ctx context.Context, executable string) string {
	version, err := observation.NewProcessClient(observation.ProcessOptions{Executable: executable}).Version(ctx)
	if err != nil {
		return ""
	}

	return version
}

// detectCbmBuild gives the version and the file digest of the CBM executable. Both are empty when the executable
// is missing. The digest is empty when the file cannot be read.
func detectCbmBuild(ctx context.Context, executable string) CbmBuild {
	version := detectCbmVersion(ctx, executable)
	if version == "" {
		return CbmBuild{}
	}

	identity, err := observation.ResolveExecutable(executable)
	if err != nil {
		return CbmBuild{Version: version}
	}

	return CbmBuild{Version: version, SHA256: identity.SHA256}
}

// VersionText gives the 4 lines of `--version`. The CBM executable comes from
// the workspace file when it loads. cwd is the directory that a relative
// workspace path is relative to.
func VersionText(ctx context.Context, cwd, workspaceFile string, build BuildInfo) string {
	executable := defaultCbmExecutable

	path := workspaceFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, filepath.FromSlash(path))
	}

	if wsp, err := workspace.Load(path, ""); err == nil {
		executable = wsp.Observation.Executable
	}

	cbm := detectCbmVersion(ctx, executable)
	if cbm == "" {
		cbm = "missing"
	}

	return "concordia " + build.Version + "\n" +
		"calm-schema " + strings.Join(workspace.CalmSchemaVersions, ",") + "\n" +
		"cbm-adapter " + workspace.AdapterSchema + "\n" +
		"cbm " + cbm + "\n"
}
