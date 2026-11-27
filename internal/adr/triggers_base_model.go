package adr

import (
	"context"
	"encoding/json"
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

	_, found, err := revisionTree{git: git, rev: base, root: dir}.write(ctx, source)
	if err != nil {
		return nil, err
	}

	if !found {
		return nil, fmt.Errorf("%s does not exist at %s", source, base)
	}

	mapping, err := URLMappingAt(ctx, Revision{Git: git, Rev: base}, wspace, &snapshot)
	if err != nil {
		return nil, err
	}

	model, err := loadBaseModelFile(source, &intent.LoadContext{Root: dir, URLMapping: mapping, Trust: trust.FromWorkspace(wspace)})
	if err != nil {
		return nil, fmt.Errorf("load %s at %s: %w", source, base, err)
	}

	return model, nil
}

// Revision is a git revision that a workspace is read at.
type Revision struct {
	Git workspace.GitPort
	Rev string
}

// URLMappingAt writes the URL mapping file (architecture.urlMapping) of the workspace wsp and the workspace files that
// it maps, as of the revision, under snapshot.Root, and gives the mapping with absolute paths. snapshot is a copy of
// wsp whose Root is the directory of the revision. A relative entry resolves against the directory of the mapping
// file, as intent.LoadURLMapping does:
//   - an entry inside the workspace is read from the revision into snapshot.Root;
//   - an entry that leaves the workspace (for example ../shared/req.json in a monorepo) is not in the revision, so it
//     resolves against the directory of the mapping file in wsp.Root and is read as it is on disk now;
//   - an absolute entry is used as it is, also read as it is on disk now.
//
// A mapping file that the revision does not have gives an empty mapping: the revision had no mapping, so a mapped URL
// in its architecture is reported by the loader. A mapped file that the revision does not have is not written, so the
// loader reports it as unreadable.
func URLMappingAt(ctx context.Context, revision Revision, wsp, snapshot *workspace.Workspace) (map[string]string, error) {
	configured := snapshot.Architecture.URLMapping
	if configured == "" {
		return map[string]string{}, nil
	}

	name, err := workspaceRelative(configured)
	if err != nil {
		return nil, fmt.Errorf("architecture.urlMapping %w", err)
	}

	tree := revisionTree{git: revision.Git, rev: revision.Rev, root: snapshot.Root}

	data, found, err := tree.write(ctx, name)
	if err != nil || !found {
		return map[string]string{}, err
	}

	var entries map[string]string
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse url mapping %s at %s: %w", name, revision.Rev, err)
	}

	dir := filepath.Dir(filepath.FromSlash(name))
	placer := mappingPlacer{tree: tree, revisionDir: filepath.Join(snapshot.Root, dir), diskDir: wsp.ResolveInRoot(dir)}
	mapping := make(map[string]string, len(entries))

	for url, entry := range entries {
		if mapping[url], err = placer.place(ctx, entry); err != nil {
			return nil, err
		}
	}

	return mapping, nil
}

// mappingPlacer gives the local paths of URL mapping entries: in revisionDir, the directory of the mapping file in
// the revision tree, or in diskDir, the same directory in the workspace on disk.
type mappingPlacer struct {
	tree        revisionTree
	revisionDir string
	diskDir     string
}

// place gives the absolute local path of a mapping entry (see URLMappingAt) and writes it from the revision when it
// is inside the workspace.
func (m *mappingPlacer) place(ctx context.Context, entry string) (string, error) {
	local := filepath.FromSlash(entry)
	if filepath.IsAbs(local) {
		return local, nil
	}

	inRevision := filepath.Join(m.revisionDir, local)

	rel, under := strings.CutPrefix(inRevision, m.tree.root+string(filepath.Separator))
	if !under {
		return filepath.Join(m.diskDir, local), nil
	}

	_, _, err := m.tree.write(ctx, filepath.ToSlash(rel))

	return inRevision, err
}

// revisionTree writes files of a git revision under a root directory.
type revisionTree struct {
	git  workspace.GitPort
	rev  string
	root string
}

// write copies the workspace-relative file name of the revision under the root and gives its content. It gives false
// when the revision has no such file.
func (r revisionTree) write(ctx context.Context, name string) (data []byte, found bool, err error) {
	data, found, err = r.git.ShowFile(ctx, r.rev, name)
	if err != nil {
		return nil, false, fmt.Errorf("read %s at %s: %w", name, r.rev, err)
	}

	if !found {
		return nil, false, nil
	}

	target := filepath.Join(r.root, filepath.FromSlash(name))
	if err := fsutil.WriteFileAtomic(target, data); err != nil {
		return nil, false, fmt.Errorf("write %s: %w", target, err)
	}

	return data, true, nil
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
