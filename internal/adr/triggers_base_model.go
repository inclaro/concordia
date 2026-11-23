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
// normal intent loader, in a temporary copy of the workspace. The base document is loaded like the current model:
// with the trust vocabulary of the current workspace, so that both sides of the comparison use one configuration,
// and with the URL mapping (architecture.urlMapping) and the files it maps as of base (see URLMappingAt).
// It gives an error when the source is not a workspace-relative file, the base has no such file, or the document
// cannot be loaded.
func LoadBaseModel(ctx context.Context, wspace *workspace.Workspace, git workspace.GitPort, base string) (*intent.ArchitectureModel, error) {
	source, err := baseModelSource(wspace)
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "concordia-adr-base-")
	if err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	snapshot := *wspace
	snapshot.Root = dir

	found, err := revisionTree{git: git, rev: base, root: dir}.write(ctx, source)
	if err != nil {
		return nil, err
	}

	if !found {
		return nil, fmt.Errorf("%s does not exist at %s", source, base)
	}

	mapping, err := URLMappingAt(ctx, git, base, &snapshot)
	if err != nil {
		return nil, err
	}

	model, err := loadBaseModelFile(source, &intent.LoadContext{Root: dir, URLMapping: mapping, Trust: trust.FromWorkspace(wspace)})
	if err != nil {
		return nil, fmt.Errorf("load %s at %s: %w", source, base, err)
	}

	return model, nil
}

// URLMappingAt writes the URL mapping file (architecture.urlMapping) of snapshot and the workspace files that it maps,
// as of rev, under snapshot.Root, and gives the mapping with absolute paths under that root (see
// intent.LoadURLMapping). snapshot is a copy of the workspace whose Root is the directory of the revision.
//
// A mapping file that rev does not have gives an empty mapping: the revision had no mapping, so a mapped URL in its
// architecture is reported by the loader. A mapped file that rev does not have is not written, so the loader reports
// it as unreadable. A mapped path outside the workspace is not read from git and stays as the mapping gives it.
func URLMappingAt(ctx context.Context, git workspace.GitPort, rev string, snapshot *workspace.Workspace) (map[string]string, error) {
	configured := snapshot.Architecture.URLMapping
	if configured == "" {
		return map[string]string{}, nil
	}

	name, err := workspaceRelative(configured)
	if err != nil {
		return nil, fmt.Errorf("architecture.urlMapping %w", err)
	}

	tree := revisionTree{git: git, rev: rev, root: snapshot.Root}

	found, err := tree.write(ctx, name)
	if err != nil || !found {
		return map[string]string{}, err
	}

	mapping, err := intent.LoadURLMapping(snapshot)
	if err != nil {
		return nil, fmt.Errorf("load url mapping at %s: %w", rev, err)
	}

	for _, local := range mapping {
		if err := tree.writeMapped(ctx, local); err != nil {
			return nil, err
		}
	}

	return mapping, nil
}

// revisionTree writes files of a git revision under a root directory.
type revisionTree struct {
	git  workspace.GitPort
	rev  string
	root string
}

// write copies the workspace-relative file name of the revision under the root. It gives false when the revision
// has no such file.
func (r revisionTree) write(ctx context.Context, name string) (bool, error) {
	data, found, err := r.git.ShowFile(ctx, r.rev, name)
	if err != nil {
		return false, fmt.Errorf("read %s at %s: %w", name, r.rev, err)
	}

	if !found {
		return false, nil
	}

	target := filepath.Join(r.root, filepath.FromSlash(name))
	if err := fsutil.WriteFileAtomic(target, data); err != nil {
		return false, fmt.Errorf("write %s: %w", target, err)
	}

	return true, nil
}

// writeMapped copies the mapped file at the absolute path local when it is under the root.
func (r revisionTree) writeMapped(ctx context.Context, local string) error {
	rel, under := strings.CutPrefix(local, r.root+string(filepath.Separator))
	if !under {
		return nil
	}

	_, err := r.write(ctx, filepath.ToSlash(rel))

	return err
}

// baseModelSource gives the slash-separated workspace-relative path of architecture.source, or an error when the
// source is empty, remote, or outside the workspace.
func baseModelSource(wspace *workspace.Workspace) (string, error) {
	configured := wspace.Architecture.Source
	if configured == "" || strings.HasPrefix(configured, "https://") {
		return "", errors.New("the base model needs a workspace-relative architecture.source")
	}

	source, err := workspaceRelative(configured)
	if err != nil {
		return "", fmt.Errorf("architecture.source %w", err)
	}

	return source, nil
}

// workspaceRelative gives the slash-separated form of a configured path, or an error when it is outside the
// workspace.
func workspaceRelative(configured string) (string, error) {
	name := filepath.ToSlash(filepath.Clean(configured))
	if strings.HasPrefix(name, "/") || !filepath.IsLocal(filepath.FromSlash(name)) {
		return "", fmt.Errorf("'%s' is outside the workspace", configured)
	}

	return name, nil
}

// loadBaseModelFile loads the base document that is written at source under lc.Root. It fails on an error
// diagnostic.
func loadBaseModelFile(source string, lc *intent.LoadContext) (*intent.ArchitectureModel, error) {
	result := intent.LoadArchitecture(filepath.Join(lc.Root, filepath.FromSlash(source)), *lc)
	if result.Model == nil {
		return nil, errors.New(firstDiagnosticMessage(result.Diagnostics))
	}

	for i := range result.Diagnostics {
		if result.Diagnostics[i].Severity == diag.SeverityError {
			return nil, errors.New(result.Diagnostics[i].Message)
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
