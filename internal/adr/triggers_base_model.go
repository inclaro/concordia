package adr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

// LoadBaseModel loads the architecture file (architecture.source) as of base through GitPort.ShowFile and the
// normal intent loader. The base document is validated against the trust vocabulary of the current workspace, the
// same vocabulary that the current model is loaded with, so that both sides of the comparison use one configuration.
// It gives an error when the source is not a workspace-relative file, the base has no such file, or the document
// cannot be loaded.
func LoadBaseModel(ctx context.Context, wspace *workspace.Workspace, git workspace.GitPort, base string) (*intent.ArchitectureModel, error) {
	source, err := baseModelSource(wspace.Architecture.Source)
	if err != nil {
		return nil, err
	}

	data, found, err := git.ShowFile(ctx, base, source)
	if err != nil {
		return nil, fmt.Errorf("read %s at %s: %w", source, base, err)
	}

	if !found {
		return nil, fmt.Errorf("%s does not exist at %s", source, base)
	}

	return loadBaseModelData(source, base, data, trust.FromWorkspace(wspace))
}

// baseModelSource gives the slash-separated workspace-relative path of architecture.source, or an error when the
// source is empty, remote, or outside the workspace.
func baseModelSource(configured string) (string, error) {
	if configured == "" || strings.HasPrefix(configured, "https://") {
		return "", errors.New("the base model needs a workspace-relative architecture.source")
	}

	source := filepath.ToSlash(filepath.Clean(configured))
	if strings.HasPrefix(source, "/") || !filepath.IsLocal(filepath.FromSlash(source)) {
		return "", fmt.Errorf("architecture.source '%s' is outside the workspace", configured)
	}

	return source, nil
}

// loadBaseModelData writes the base document into a temporary workspace and loads it with the intent loader and the
// trust vocabulary.
func loadBaseModelData(source, base string, data []byte, vocabulary trust.Vocabulary) (*intent.ArchitectureModel, error) {
	dir, err := os.MkdirTemp("", "concordia-adr-base-")
	if err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	target := filepath.Join(dir, filepath.FromSlash(source))
	if err = fsutil.WriteFileAtomic(target, data); err != nil {
		return nil, fmt.Errorf("write %s: %w", target, err)
	}

	result := intent.LoadArchitecture(target, intent.LoadContext{Root: dir, URLMapping: map[string]string{}, Trust: vocabulary})
	if result.Model == nil {
		return nil, fmt.Errorf("load %s at %s: %s", source, base, firstDiagnosticMessage(result.Diagnostics))
	}

	for i := range result.Diagnostics {
		if result.Diagnostics[i].Severity == diag.SeverityError {
			return nil, fmt.Errorf("load %s at %s: %s", source, base, result.Diagnostics[i].Message)
		}
	}

	return result.Model, nil
}

// firstDiagnosticMessage is the message of the first diagnostic, or a fixed text when there is none.
func firstDiagnosticMessage(diagnostics []diag.Diagnostic) string {
	if len(diagnostics) == 0 {
		return "architecture not loaded"
	}

	return diagnostics[0].Message
}
