package render

import (
	"os/exec"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/workspace"
)

// GapInvalidSource is a declared source whose values cannot be passed safely to the renderer.
const GapInvalidSource = "invalid-source"

func lookPath(name string) (string, error) { return exec.LookPath(name) }

func sortStrings(list []string) { slices.SortFunc(list, canonjson.CompareUTF16) }

// toolArguments gives the fixed argument list of the renderer (design D4 layer 1). The list is a template: the
// only values taken from the source are the release name, the namespace, and the repository paths, all relative to
// the confined directory and prefixed with `./`. No flag comes from the workspace, and the flags that would fetch
// charts, start programs, or run plugins (`--dependency-update`, `--enable-helm`, `--enable-exec`,
// `--enable-alpha-plugins`, `--post-renderer`) are never in the list. Helm hooks stay in the output as resources
// because `--no-hooks` is not passed.
func toolArguments(src *workspace.RenderSource) []string {
	if src.Tool == workspace.RenderToolHelm {
		return helmArguments(src)
	}

	return kustomizeArguments(src)
}

func helmArguments(src *workspace.RenderSource) []string {
	args := []string{"template", "--include-crds"}

	if src.Namespace != "" {
		args = append(args, "--namespace", src.Namespace)
	}

	for _, v := range cleanPaths(src.ValuesFiles) {
		args = append(args, "--values", "./"+v)
	}

	return append(args, helmRelease(src), "./"+cleanPath(src.Path))
}

func kustomizeArguments(src *workspace.RenderSource) []string {
	return []string{"build", "./" + cleanPath(src.Path)}
}
