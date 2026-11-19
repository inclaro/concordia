package views

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/edgekind"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/workspace"
)

// ManifestSuffix is appended to `output.path` to give the path of the view manifest.
const ManifestSuffix = workspace.ViewManifestSuffix

// ValidateAgainst checks the definitions of the workspace against the inputs that
// ValidateDefinitions does not have:
//
//   - the environment of a view against the `environments` of the workspace;
//   - the CALM IDs in `scope`, `coverage` and `mapping.elements` against the model (skipped
//     when model is nil, for example when the architecture did not load);
//   - the edge kinds of `projection.edgeKinds` against the edge vocabulary;
//   - the paths of a generated view (`output.path`, the exports and the manifest): inside the
//     workspace, not under `projection.output`, not written by another view, and not matched by
//     a Git ignore rule (skipped when git is nil).
//
// Each problem is a CO-DGM-001 (definition) or CO-DGM-002 (output path) diagnostic with exit
// code 2, sorted. The error is a Git failure (CO-WRK-012).
func ValidateAgainst(ctx context.Context, wsp *workspace.Workspace, model *intent.ArchitectureModel, git workspace.GitPort) ([]diag.Diagnostic, error) {
	var out []diag.Diagnostic

	nodes, relationships := calmIDs(model)

	for i := range wsp.Views {
		view := &wsp.Views[i]
		problem := func(detail string) {
			out = append(out, diag.New(diag.DGM001, diag.Params{"view": view.ID, "detail": detail},
				diag.WithSource(diag.Source{URI: workspaceURI(wsp), Pointer: fmt.Sprintf("/views/%d", i)})))
		}

		for _, detail := range environmentProblems(view, wsp.Environments) {
			problem(detail)
		}

		for _, detail := range edgeKindProblems(view) {
			problem(detail)
		}

		for _, detail := range semanticProblems(view) {
			problem(detail)
		}

		if model != nil {
			for _, detail := range calmProblems(view, nodes, relationships) {
				problem(detail)
			}
		}
	}

	pathProblems, err := outputPathProblems(ctx, wsp, git)
	if err != nil {
		return nil, err
	}

	out = append(out, pathProblems...)

	return diag.Sort(out), nil
}

// Diagnose gives the diagnostics of the views for `model validate`, `check` and `render --views`:
// the problems of ValidateAgainst and the suggestions of SuggestDefaults, sorted. A workspace
// that declares neither views nor environments has not adopted them and gets no suggestion,
// so that its `check` output stays as it was. It matches conformance.ViewDiagnosticsFunc.
func Diagnose(ctx context.Context, wsp *workspace.Workspace, model *intent.ArchitectureModel, git workspace.GitPort) ([]diag.Diagnostic, error) {
	problems, err := ValidateAgainst(ctx, wsp, model, git)
	if err != nil {
		return nil, err
	}

	if len(wsp.Views) == 0 && len(wsp.Environments) == 0 {
		return diag.Sort(problems), nil
	}

	return diag.Sort(append(problems, SuggestDefaults(model, wsp)...)), nil
}

// workspaceURI is the workspace file relative to the workspace root, so that diagnostics do not
// depend on where the workspace is checked out.
func workspaceURI(wsp *workspace.Workspace) string {
	rel, err := filepath.Rel(wsp.Root, wsp.Path)
	if err != nil {
		return filepath.Base(wsp.Path)
	}

	return filepath.ToSlash(rel)
}

func calmIDs(model *intent.ArchitectureModel) (nodes, relationships map[string]bool) {
	nodes, relationships = map[string]bool{}, map[string]bool{}

	if model == nil {
		return nodes, relationships
	}

	for i := range model.Nodes {
		nodes[model.Nodes[i].ID] = true
	}

	for i := range model.Relationships {
		relationships[model.Relationships[i].ID] = true
	}

	return nodes, relationships
}

func environmentProblems(view *Definition, environments []workspace.Environment) []string {
	if view.Environment == "" || slices.ContainsFunc(environments, func(e workspace.Environment) bool { return e.ID == view.Environment }) {
		return nil
	}

	declared := make([]string, len(environments))
	for i, e := range environments {
		declared[i] = e.ID
	}

	listed := "the workspace declares no environment"
	if len(declared) > 0 {
		listed = "declared environments: " + strings.Join(declared, ", ")
	}

	return []string{fmt.Sprintf("environment '%s' is not declared (%s)", view.Environment, listed)}
}

func edgeKindProblems(v *Definition) []string {
	if v.Projection == nil {
		return nil
	}

	var problems []string

	for _, token := range v.Projection.EdgeKinds {
		if _, ok := edgekind.Resolve(token); !ok {
			problems = append(problems, fmt.Sprintf("projection.edgeKinds names the kind '%s', which the edge vocabulary does not contain", token))
		}
	}

	return problems
}

func semanticProblems(v *Definition) []string {
	if v.Projection == nil {
		return nil
	}

	var problems []string

	for _, token := range v.Projection.Semantic {
		switch token {
		case SemanticName, SemanticDescription, SemanticTags:
		default:
			problems = append(problems, fmt.Sprintf("projection.semantic names '%s', which is not one of %s, %s, %s", token, SemanticName, SemanticDescription, SemanticTags))
		}
	}

	return problems
}

func calmProblems(view *Definition, nodes, relationships map[string]bool) []string {
	var problems []string

	for _, id := range view.Scope {
		if !nodes[id] {
			problems = append(problems, fmt.Sprintf("scope names '%s', which is not a CALM node ID", id))
		}
	}

	problems = append(problems, coverageProblems(view.Coverage, nodes, relationships)...)

	return append(problems, mappingProblems(view.Mapping, nodes)...)
}

func coverageProblems(coverage *Coverage, nodes, relationships map[string]bool) []string {
	if coverage == nil {
		return nil
	}

	var problems []string

	for _, id := range coverage.Elements {
		if !nodes[id] {
			problems = append(problems, fmt.Sprintf("coverage.elements names '%s', which is not a CALM node ID", id))
		}
	}

	for _, id := range coverage.Relationships {
		if !relationships[id] {
			problems = append(problems, fmt.Sprintf("coverage.relationships names '%s', which is not a CALM relationship ID", id))
		}
	}

	return problems
}

func mappingProblems(mapping *Mapping, nodes map[string]bool) []string {
	if mapping == nil {
		return nil
	}

	keys := make([]string, 0, len(mapping.Elements))
	for key := range mapping.Elements {
		keys = append(keys, key)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	var problems []string

	for _, key := range keys {
		if id := mapping.Elements[key]; !nodes[id] {
			problems = append(problems, fmt.Sprintf("mapping.elements key '%s' maps to '%s', which is not a CALM node ID", key, id))
		}
	}

	return problems
}

// viewPath is one path that a generated view writes.
type viewPath struct {
	view, path string
}

// refusedPaths gives a CO-DGM-002 for each path of view i that a view may not write.
func refusedPaths(wsp *workspace.Workspace, i int, paths []string, projectionOutput string) []diag.Diagnostic {
	var out []diag.Diagnostic

	for _, p := range paths {
		if detail := pathRefusal(p, projectionOutput); detail != "" {
			out = append(out, pathDiagnostic(wsp, i, p, detail))
		}
	}

	return out
}

// outputPathProblems checks the paths of the generated views.
func outputPathProblems(ctx context.Context, wsp *workspace.Workspace, git workspace.GitPort) ([]diag.Diagnostic, error) {
	var (
		out     []diag.Diagnostic
		allowed []viewPath
		claimed = map[string]string{}
	)

	projectionOutput := cleanRelative(wsp.Projection.Output)

	for i := range wsp.Views {
		view := &wsp.Views[i]

		paths := generatedPaths(view)
		if len(paths) == 0 {
			continue
		}

		if refusals := refusedPaths(wsp, i, paths, projectionOutput); len(refusals) > 0 {
			out = append(out, refusals...)

			continue
		}

		for _, p := range append(paths, manifestPath(view)) {
			clean := cleanRelative(p)

			if owner, taken := claimed[clean]; taken {
				out = append(out, pathDiagnostic(wsp, i, p, "the path is already written by view '"+owner+"'"))

				continue
			}

			claimed[clean] = view.ID
			allowed = append(allowed, viewPath{view: view.ID, path: clean})
		}
	}

	if git == nil || len(allowed) == 0 {
		return out, nil
	}

	paths := make([]string, len(allowed))
	for i, a := range allowed {
		paths[i] = a.path
	}

	ignored, err := git.IsIgnored(ctx, paths)
	if err != nil {
		return nil, fmt.Errorf("git ignore check of the view outputs: %w", err)
	}

	for _, a := range allowed {
		if !ignored[a.path] {
			continue
		}

		index := slices.IndexFunc(wsp.Views, func(v Definition) bool { return v.ID == a.view })
		out = append(out, pathDiagnostic(wsp, index, a.path, "the path is ignored by Git: a committed view needs a tracked path"))
	}

	return out, nil
}

// generatedPaths are the output and the exports of a generated view.
func generatedPaths(v *Definition) []string {
	if v.Origin != OriginGenerated || v.Output == nil || v.Output.Path == "" {
		return nil
	}

	return v.OutputPaths()
}

// manifestPath is the manifest of a generated view with an output.
func manifestPath(v *Definition) string {
	return v.Output.Path + ManifestSuffix
}

func pathDiagnostic(wsp *workspace.Workspace, index int, p, detail string) diag.Diagnostic {
	return diag.New(diag.DGM002, diag.Params{"view": wsp.Views[index].ID, "path": p, "detail": detail},
		diag.WithSource(diag.Source{URI: workspaceURI(wsp), Pointer: fmt.Sprintf("/views/%d/output", index)}))
}

// cleanRelative gives the "/" form of a workspace-relative path with "." and ".." resolved.
func cleanRelative(p string) string {
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
}

// pathRefusal says why a path may not be written by a view, or "" when it may.
func pathRefusal(given, projectionOutput string) string {
	slash := strings.ReplaceAll(given, `\`, "/")
	clean := path.Clean(slash)

	switch {
	case path.IsAbs(slash) || filepath.IsAbs(given) || strings.HasPrefix(slash, "~") || (len(slash) > 1 && slash[1] == ':'):
		return "the path is outside the workspace: it must be relative to the workspace root"
	case clean == ".." || strings.HasPrefix(clean, "../"):
		return "the path is outside the workspace"
	case clean == ".":
		return "the path names the workspace root"
	case clean == projectionOutput || strings.HasPrefix(clean, projectionOutput+"/"):
		return "the path is under projection.output (" + projectionOutput + "), which Git ignores by default: use a tracked directory"
	case clean != given:
		return "the path is not in canonical form, use " + clean
	}

	return ""
}
