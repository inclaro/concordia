package cli

import (
	"context"
	"strings"

	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/workspace"
)

// resolveArchitectureFiles gives the absolute OS paths of workspace pattern or decorator
// entries. res downloads and pins an https entry.
func resolveArchitectureFiles(ctx context.Context, res *remote.Resolver, wsp *workspace.Workspace, values []string) ([]string, error) {
	out := make([]string, len(values))

	for i, p := range values {
		local, _, err := remote.ResolvePath(ctx, res, p)
		if err != nil {
			return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
		}

		out[i] = local
		if !strings.HasPrefix(p, "https://") {
			out[i] = wsp.ResolveInRoot(local)
		}
	}

	return out, nil
}

// remoteArchitectureURL gives the https URL of the workspace architecture when the
// command uses architecture.source and it is remote, else "".
func remoteArchitectureURL(wsp *workspace.Workspace, override string) string {
	if override == "" && strings.HasPrefix(wsp.Architecture.Source, "https://") {
		return wsp.Architecture.Source
	}

	return ""
}
