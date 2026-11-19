package workspace_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	codeWRK002    = "CO-WRK-002"
	workspaceFile = "concordia.yaml"
	srcA          = "src/a.ts"
	extPath       = "schemas/ext.json"
	uriA          = "https://a/x"
	srcNew        = "src/new.ts"
)

const baseWorkspace = `
apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: demo
architecture:
  source: architecture/a.calm.json
observation:
  projects:
    - name: demo
      root: .
      include: [services/**]
      exclude: [vendor/**]
  indexMode: fast
check:
  minimumCoverage: 0.9
  unknown: warn
profiles:
  ci:
    check:
      minimumCoverage: 0.99
    observation:
      projects:
        - name: demo-ci
          include: [packages/**]
`

func writeWorkspace(t *testing.T, yaml string) string {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{workspaceFile: yaml})

	return filepath.Join(dir, workspaceFile)
}

func failure(t *testing.T, err error) *diag.Error {
	t.Helper()

	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("expected a *diag.Error, got %v", err)
	}

	return de
}

func TestLoadAppliesDefaults(t *testing.T) {
	t.Parallel()

	path := writeWorkspace(t, baseWorkspace)

	w, err := workspace.Load(path, "")
	if err != nil {
		t.Fatal(err)
	}

	expectEqual(t, "root", w.Root, filepath.Dir(path))
	expectEqual(t, "path", w.Path, path)
	expectEqual(t, "name", w.Metadata.Name, "demo")
	expectObservationDefaults(t, &w.Observation)
	expectEqual(t, "check", w.Check, workspace.Check{MinimumCoverage: 0.9, Unknown: workspace.UnknownWarn, FailOn: diag.SeverityError})
	expectEqual(t, "report outputs", w.Report.Outputs, []workspace.ReportOutput{{Format: workspace.ReportJSON, Path: "build/concordia/results.json"}})

	if !strings.HasPrefix(w.Digest, "sha256:") || len(w.Digest) != len("sha256:")+64 {
		t.Errorf("digest %q", w.Digest)
	}

	expectEqual(t, "architecture source", w.Architecture.Source, "architecture/a.calm.json")
	expectEqual(t, "architecture patterns", len(w.Architecture.Patterns), 0)
	expectDefaultedFiles(t, w)
}

// expectObservationDefaults checks the observation defaults that the base workspace leaves to the loader.
func expectObservationDefaults(t *testing.T, o *workspace.Observation) {
	t.Helper()

	expectEqual(t, "index mode", o.IndexMode, workspace.IndexModeFast)
	expectEqual(t, "executable", o.Executable, "codebase-memory-mcp")
	expectEqual(t, "retries", o.Retries, 1)
	expectEqual(t, "timeout seconds", o.TimeoutSeconds, 300)
	expectEqual(t, "index timeout seconds", o.IndexTimeoutSeconds, 1800)
	expectEqual(t, "provider", o.Provider, "codebase-memory-mcp")
}

// expectDefaultedFiles checks the default file locations of bindings, policies, exceptions and projection.
func expectDefaultedFiles(t *testing.T, w *workspace.Workspace) {
	t.Helper()

	expectEqual(t, "bindings file", w.Bindings.Files[0], "bindings/concordia.bindings.yaml")
	expectEqual(t, "policies file", w.Policies.Files[0], "policies/**/*.policy.yaml")
	expectEqual(t, "exceptions baseline", w.Exceptions.Baseline, "exceptions/concordia.baseline.json")
	expectEqual(t, "exceptions waivers", w.Exceptions.Waivers, "exceptions/waivers.yaml")
	expectEqual(t, "projection output", w.Projection.Output, "build/concordia/structurizr")
	expectEqual(t, "projection provider", w.Projection.Provider, "structurizr")
}

func TestLoadDefaultCheckValues(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, "observation:\n  projects:\n    - name: x\n", "")

	expectEqual(t, "check", w.Check, workspace.Check{MinimumCoverage: 0.95, Unknown: workspace.UnknownFail, FailOn: diag.SeverityError})

	p := w.Observation.Projects[0]
	expectEqual(t, "project name", p.Name, "x")
	expectEqual(t, "project root", p.Root, ".")
	expectEqual(t, "project include count", len(p.Include), 0)
	expectEqual(t, "project exclude count", len(p.Exclude), 0)
}

func TestLoadProfileReplacesValuesObjectsMergeArraysReplaced(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, baseWorkspace, "ci")

	expectEqual(t, "profile", w.Profile, "ci")
	expectEqual(t, "minimum coverage", w.Check.MinimumCoverage, 0.99)
	// The unknown mode is kept from the root: deep merge.
	expectEqual(t, "unknown", w.Check.Unknown, workspace.UnknownWarn)
	// The index mode is kept from the root.
	expectEqual(t, "index mode", w.Observation.IndexMode, workspace.IndexModeFast)

	projects := w.Observation.Projects
	requireEqual(t, "project count", len(projects), 1)
	expectEqual(t, "project name", projects[0].Name, "demo-ci")
	expectEqual(t, "project root", projects[0].Root, ".")
	expectEqual(t, "project include", projects[0].Include, []string{"packages/**"})
	expectEqual(t, "project exclude count", len(projects[0].Exclude), 0)
}

func TestLoadUnknownProfileGivesWRK004(t *testing.T) {
	t.Parallel()

	_, err := workspace.Load(writeWorkspace(t, baseWorkspace), "nope")

	expectInvalidInputCode(t, failure(t, err), "CO-WRK-004")
}

func TestLoadMissingFileGivesWRK001(t *testing.T) {
	t.Parallel()

	_, err := workspace.Load(filepath.Join(t.TempDir(), workspaceFile), "")

	expectInvalidInputCode(t, failure(t, err), "CO-WRK-001")
}

func TestLoadSchemaErrorGivesWRK002WithPointerForEachError(t *testing.T) {
	t.Parallel()

	de := loadFailure(t, "observation:\n  projects:\n    - name: x\ncheck:\n  unknown: maybe\n  minimumCoverage: 2\n")

	expectInvalidInputCode(t, de, codeWRK002)

	pointers := make([]string, 0, len(de.Diagnostic.Cause))

	for _, c := range de.Diagnostic.Cause {
		pointers = append(pointers, c.Source.Pointer)
	}

	for _, want := range []string{"/check/unknown", "/check/minimumCoverage"} {
		if !slices.Contains(pointers, want) {
			t.Errorf("pointer %s not in %v", want, pointers)
		}
	}
}

func TestLoadUnknownKeyIsASchemaError(t *testing.T) {
	t.Parallel()

	de := loadFailure(t, "observation:\n  projects:\n    - name: x\nbogus: 1\n")

	requireEqual(t, "cause is given", len(de.Diagnostic.Cause) > 0, true)
	expectContains(t, "first cause", de.Diagnostic.Cause[0].Message, "bogus")
}

func TestLoadTwoProjectsPass(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, "observation:\n  projects:\n    - name: a\n    - name: b\n      root: ../b\n", "")

	projects := w.Observation.Projects
	requireEqual(t, "project count", len(projects), 2)
	expectEqual(t, "path prefix of the first project", w.PathPrefix(&projects[0]), "")
	expectEqual(t, "path prefix of the second project", w.PathPrefix(&projects[1]), "@b/")

	project, rel := workspace.SplitProjectPath("@b/src/x.go")
	expectEqual(t, "split project", project, "b")
	expectEqual(t, "split path", rel, "src/x.go")
}

func TestLoadDuplicateProjectNameGivesWRK002(t *testing.T) {
	t.Parallel()

	de := loadFailure(t, "observation:\n  projects:\n    - name: a\n    - name: a\n")

	expectEqual(t, "code", de.Diagnostic.Code, codeWRK002)
	expectContains(t, "message", de.Diagnostic.Message, "appears two times")
}

func TestLoadYAMLSyntaxErrorGivesWRK002WithLine(t *testing.T) {
	t.Parallel()

	de := loadFailure(t, "observation: [unclosed\n")

	expectEqual(t, "code", de.Diagnostic.Code, codeWRK002)
	expectContains(t, "message", de.Diagnostic.Message, "YAML syntax error")
	expectSourceLine(t, &de.Diagnostic)
	expectEqual(t, "cause is given", len(de.Diagnostic.Cause) > 0, true)
}

func TestLoadDuplicateKeyIsAnError(t *testing.T) {
	t.Parallel()

	de := loadFailure(t, "observation:\n  projects:\n    - name: x\ncheck:\n  unknown: warn\ncheck:\n  unknown: fail\n")

	expectEqual(t, "code", de.Diagnostic.Code, codeWRK002)
	expectSourceLine(t, &de.Diagnostic)
	expectContains(t, "message", de.Diagnostic.Message, "already defined")
}

func TestLoadNonMappingDocumentIsAnError(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"", "- a\n- b\n", "just text\n"} {
		de := loadFailure(t, text)

		expectEqual(t, "code of "+text, de.Diagnostic.Code, codeWRK002)
		expectContains(t, "message of "+text, de.Diagnostic.Message, "must be a mapping")
	}
}

func TestResolveHelpers(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, "observation:\n  projects:\n    - name: x\n      root: sub/dir\n", "")

	expectEqual(t, "relative path", w.ResolveInRoot("a/b.txt"), filepath.Join(w.Root, "a", "b.txt"))

	abs := filepath.Join(t.TempDir(), "x")
	expectEqual(t, "absolute path", w.ResolveInRoot(filepath.ToSlash(abs)), abs)
	expectEqual(t, "project root", w.ProjectRoot(), filepath.Join(w.Root, "sub", "dir"))
	expectEqual(t, "primary project name", w.PrimaryProject().Name, "x")
}

const completeView = `
views:
  - id: payments-container
    kind: container
    origin: generated
    owner: team-payments
    scope: [payments-system]
    abstraction: container
    environment: prod
    coverage: {elements: [orders-service], relationships: [orders-to-db]}
    output:
      path: docs/architecture/views/payments-container.dsl
      exports: [{format: mermaid, path: docs/architecture/views/payments-container.mmd}]
    projection: {merge: binding, edgeKinds: [calls], selfEdges: omit, parallelEdges: merge, semantic: [name]}
    comparison: {against: [declared, observed], minimumConfidence: syntactic, severity: warning}
  - id: payments-sketch
    kind: system-context
    origin: authored
    owner: team-payments
    scope: [payments-system]
    abstraction: system
    source: {path: docs/arch/payments.md, format: mermaid, selector: {title: "Payments containers"}}
    mapping: {elements: {orders-api: orders-service}}
projection:
  allowIncludes: [docs/arch/common.dsl]
`

func TestLoadCompleteViewLoads(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, baseWorkspace+completeView, "")

	requireEqual(t, "view count", len(w.Views), 2)
	expectEqual(t, "first view id", w.Views[0].ID, "payments-container")
	expectEqual(t, "second view selector title", w.Views[1].Source.Selector.Title, "Payments containers")
	expectEqual(t, "export format", w.Views[0].Output.Exports[0].Format, "mermaid")
	expectEqual(t, "mapping", w.Views[1].Mapping.Elements["orders-api"], "orders-service")
	expectEqual(t, "allowIncludes", w.Projection.AllowIncludes, []string{"docs/arch/common.dsl"})
}

func TestLoadWorkspaceWithoutViewsLoadsAsBefore(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, baseWorkspace, "")

	if w.Views != nil || w.Projection.AllowIncludes != nil {
		t.Fatalf("views %v, allowIncludes %v", w.Views, w.Projection.AllowIncludes)
	}
}

func TestLoadRejectsViewDefinitionsWithExit2(t *testing.T) {
	t.Parallel()

	const head = "\nviews:\n  - id: v1\n    kind: container\n    origin: generated\n    abstraction: container\n    scope: [s]\n    output: {path: docs/v.dsl}\n"

	tests := []struct {
		name, yaml, want string
	}{
		{"missing owner", baseWorkspace + head, "owner"},
		{"unknown key", baseWorkspace + head + "    owner: t\n    colour: red\n", "colour"},
		{"selector index", baseWorkspace + "\nviews:\n  - id: v1\n    kind: system-context\n    origin: authored\n    owner: t\n    abstraction: system\n    scope: [s]\n" +
			"    source: {path: docs/a.md, format: mermaid, selector: {index: 1}}\n    mapping: {elements: {a: s}}\n", "title or an anchor"},
		{"bad id", baseWorkspace + strings.Replace(head, "id: v1", "id: V_1", 1) + "    owner: t\n", "V_1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			de := loadFailure(t, tc.yaml)

			expectEqual(t, "exit code", de.ExitCode, diag.InvalidInput)
			expectContains(t, "diagnostic", diag.FormatDiagnostic(de.Diagnostic), tc.want)
		})
	}
}

func TestLoadRejectsViewsInAProfile(t *testing.T) {
	t.Parallel()

	yaml := baseWorkspace + `
  nightly:
    views:
      - id: v1
`

	expectEqual(t, "exit code", loadFailure(t, yaml).ExitCode, diag.InvalidInput)
}

func TestLoadNamesTheViewOfAMissingKey(t *testing.T) {
	t.Parallel()

	yaml := baseWorkspace + "\nviews:\n  - id: payments-container\n    kind: container\n    origin: generated\n    abstraction: container\n    scope: [s]\n    output: {path: docs/v.dsl}\n"

	de := loadFailure(t, yaml)

	if !hasViewOfMissingOwner(de.Diagnostic.Cause) {
		t.Fatalf("no CO-DGM-001 cause for owner: %s", diag.FormatDiagnostic(de.Diagnostic))
	}
}

// hasViewOfMissingOwner is true when a CO-DGM-001 cause names the payments-container view and the owner key.
func hasViewOfMissingOwner(causes []diag.Diagnostic) bool {
	for i := range causes {
		c := &causes[i]
		if c.Code == diag.DGM001 && strings.Contains(c.Message, "payments-container") && strings.Contains(c.Message, "owner") {
			return true
		}
	}

	return false
}

const adrWorkspace = `observation:
  projects:
    - name: demo
adr:
  files: [docs/adr/register.yaml]
  triggers:
    - id: t1
      architecture: [node-added]
profiles:
  ci:
    adr:
      files: [ci/register.yaml]
`

func TestLoadADRDefaults(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, adrWorkspace, "")

	if w.ADR == nil {
		t.Fatal("adr is nil")
	}

	expectEqual(t, "adr files", w.ADR.Files, []string{"docs/adr/register.yaml"})
	expectEqual(t, "active statuses", w.ADR.Lifecycle.Active, []string{"accepted"})
	expectEqual(t, "status map is given", w.ADR.Lifecycle.StatusMap != nil, true)
	requireEqual(t, "trigger count", len(w.ADR.Triggers), 1)
	expectEqual(t, "trigger architecture", w.ADR.Triggers[0].Architecture[0], "node-added")
}

func TestLoadProfileReplacesADRFiles(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, adrWorkspace, "ci")

	if w.ADR == nil {
		t.Fatal("adr is nil")
	}

	expectEqual(t, "adr files", w.ADR.Files, []string{"ci/register.yaml"})
}

// Scenario: Unknown key is rejected.
func TestLoadUnknownADRKeyGivesWRK002(t *testing.T) {
	t.Parallel()

	de := loadFailure(t, "observation:\n  projects:\n    - name: demo\nadr:\n  bogus: 1\n")

	expectInvalidInputCode(t, de, codeWRK002)
}

func TestLoadWithoutADRKeyLeavesADRNil(t *testing.T) {
	t.Parallel()

	w := mustLoadWorkspace(t, baseWorkspace, "")

	if w.ADR != nil {
		t.Errorf("adr %+v", w.ADR)
	}
}
