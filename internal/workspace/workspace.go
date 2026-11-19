// Package workspace loads the workspace file, the lockfile and the git state
// of the workspace root.
package workspace

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/jsonschema"
)

//go:embed schemas/*.json
var schemaFS embed.FS

// SchemaID is the ID of the workspace schema.
const SchemaID = "https://concordia.io/schemas/v1alpha1/workspace.schema.json"

// DefaultFile is the default name of the workspace file.
const DefaultFile = "concordia.yaml"

// Default values of the workspace.
const (
	DefaultExecutable          = "codebase-memory-mcp"
	DefaultTimeoutSeconds      = 300.0
	DefaultIndexTimeoutSeconds = 1800.0
	DefaultRetries             = 1
	DefaultIndexMode           = IndexModeModerate
	DefaultBaseline            = "exceptions/concordia.baseline.json"
	DefaultWaivers             = "exceptions/waivers.yaml"
	DefaultProjectionOutput    = "build/concordia/structurizr"
	DefaultMinimumCoverage     = 0.95
	DefaultUnknown             = UnknownFail
	DefaultFailOn              = diag.SeverityError
	DefaultReportPath          = "build/concordia/results.json"
	providerObservation        = "codebase-memory-mcp"
	providerProjection         = "structurizr"
	defaultProjectRoot         = "."
)

// UnknownPolicy says what happens with an unknown rule result.
type UnknownPolicy string

// Unknown policies.
const (
	UnknownFail   UnknownPolicy = "fail"
	UnknownWarn   UnknownPolicy = "warn"
	UnknownIgnore UnknownPolicy = "ignore"
)

// IndexMode is the CBM index mode.
type IndexMode string

// Index modes.
const (
	IndexModeFast     IndexMode = "fast"
	IndexModeModerate IndexMode = "moderate"
	IndexModeFull     IndexMode = "full"
)

// ReportFormat is the format of a report output.
type ReportFormat string

// Report formats.
const (
	ReportJSON   ReportFormat = "json"
	ReportJUnit  ReportFormat = "junit"
	ReportSARIF  ReportFormat = "sarif"
	ReportPretty ReportFormat = "pretty"
)

// DefaultBindingsFiles gives the default binding file globs.
func DefaultBindingsFiles() []string { return []string{"bindings/concordia.bindings.yaml"} }

// DefaultPolicyFiles gives the default policy file globs.
func DefaultPolicyFiles() []string { return []string{"policies/**/*.policy.yaml"} }

// DefaultReportOutput gives the default report output (JSON).
func DefaultReportOutput() ReportOutput {
	return ReportOutput{Format: ReportJSON, Path: DefaultReportPath}
}

// Project is one observed project.
type Project struct {
	Name string `json:"name"`
	// Root is relative to the workspace root.
	Root    string   `json:"root"`
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

// Metadata is the metadata of the workspace.
type Metadata struct {
	Name string `json:"name,omitempty"`
}

// Architecture says where the CALM documents are.
type Architecture struct {
	Source     string   `json:"source,omitempty"`
	URLMapping string   `json:"urlMapping,omitempty"`
	Patterns   []string `json:"patterns"`
	Decorators []string `json:"decorators"`
}

// Observation holds the settings of the observation provider.
type Observation struct {
	Runtime             *Runtime  `json:"runtime,omitempty"`
	Provider            string    `json:"provider"`
	Executable          string    `json:"executable"`
	IndexMode           IndexMode `json:"indexMode"`
	Projects            []Project `json:"projects"`
	TimeoutSeconds      float64   `json:"timeoutSeconds"`
	IndexTimeoutSeconds float64   `json:"indexTimeoutSeconds"`
	Retries             int       `json:"retries"`
}

// Environment is a declared deployment environment. Sources are globs
// relative to the workspace root that attribute files to it.
type Environment struct {
	ID          string   `json:"id"`
	Cluster     string   `json:"cluster,omitempty"`
	Description string   `json:"description,omitempty"`
	Sources     []string `json:"sources"`
}

// Runtime configures the runtime-trace evidence.
type Runtime struct {
	Environment string   `json:"environment,omitempty"`
	Traces      []string `json:"traces"`
	MaxAgeDays  int      `json:"maxAgeDays,omitempty"`
}

// Files is a list of file globs.
type Files struct {
	Files []string `json:"files"`
}

// RuleCoverageOverride is the `coverage` block of a rule evidence override.
type RuleCoverageOverride struct {
	Files              *float64 `json:"files,omitempty"`
	Bytes              *float64 `json:"bytes,omitempty"`
	SupportedLanguages *float64 `json:"supportedLanguages,omitempty"`
}

// RuleEvidenceOverride is the partial `evidence` block of a rule override. A
// field that is not set is nil or empty.
type RuleEvidenceOverride struct {
	MinimumCoverage *float64              `json:"minimumCoverage,omitempty"`
	Coverage        *RuleCoverageOverride `json:"coverage,omitempty"`
	// EdgeKinds sets the minimum confidence of single edge kinds, keyed by edge kind.
	EdgeKinds         map[string]string `json:"edgeKinds,omitempty"`
	MinimumConfidence string            `json:"minimumConfidence,omitempty"`
	OnUnknown         string            `json:"onUnknown,omitempty"`
}

// RuleOverride changes one rule of a policy pack without a copy of the pack.
type RuleOverride struct {
	// Enabled is nil when not set. false drops the rule.
	Enabled  *bool                 `json:"enabled,omitempty"`
	Evidence *RuleEvidenceOverride `json:"evidence,omitempty"`
	Severity string                `json:"severity,omitempty"`
	Owner    string                `json:"owner,omitempty"`
}

// PolicyPackUse is one entry of `policies.packs`.
type PolicyPackUse struct {
	// Rules are the overrides, keyed by rule ID.
	Rules map[string]RuleOverride `json:"rules,omitempty"`
	// Use is `builtin:<name>`, or a workspace-relative path with "/".
	Use string `json:"use"`
	// SHA256 is `sha256:<hex>`. A file pack needs it. A built-in pack must not have it.
	SHA256 string `json:"sha256,omitempty"`
}

// Policies holds the policy sources: rule files and packs.
type Policies struct {
	Files []string        `json:"files"`
	Packs []PolicyPackUse `json:"packs"`
}

// Exceptions holds the paths of the baseline and the waiver file.
type Exceptions struct {
	Baseline string `json:"baseline"`
	Waivers  string `json:"waivers"`
	// MaxExpiryDays bounds the duration of a scoped waiver in days. 0 means not set.
	MaxExpiryDays int `json:"maxExpiryDays,omitempty"`
}

// Projection holds the projection settings.
type Projection struct {
	Provider string `json:"provider"`
	Output   string `json:"output"`
	// AllowIncludes are the workspace-relative paths that a rendered DSL may include.
	AllowIncludes []string `json:"allowIncludes,omitempty"`
}

// Check holds the check settings.
type Check struct {
	// Rollback is `check.rollback`. nil: none.
	Rollback *Rollback     `json:"rollback,omitempty"`
	Unknown  UnknownPolicy `json:"unknown"`
	FailOn   diag.Severity `json:"failOn"`
	// Enforcement is `check.enforcement`: the ordered entries that set the enforcement mode (first match wins).
	Enforcement     []EnforcementEntry `json:"enforcement,omitempty"`
	MinimumCoverage float64            `json:"minimumCoverage"`
}

// ReportOutput is one report output.
type ReportOutput struct {
	Format ReportFormat `json:"format"`
	Path   string       `json:"path"`
}

// Report holds the report settings.
type Report struct {
	Outputs []ReportOutput `json:"outputs"`
}

// Workspace is a loaded workspace with all defaults applied and the profile
// merged. Paths inside are relative to Root and use "/".
type Workspace struct {
	Exceptions Exceptions `json:"exceptions"`
	Projection Projection `json:"projection"`
	// Digest is "sha256:<hex>" of the workspace file bytes.
	Digest   string   `json:"digest"`
	Metadata Metadata `json:"metadata"`
	Profile  string   `json:"profile,omitempty"`
	// Root is the absolute OS directory of the workspace file: the workspace root.
	Root string `json:"root"`
	// Path is the absolute OS path of the workspace file.
	Path         string        `json:"path"`
	Architecture Architecture  `json:"architecture"`
	Bindings     Files         `json:"bindings"`
	Policies     Policies      `json:"policies"`
	Report       Report        `json:"report"`
	Environments []Environment `json:"environments"`
	// Views are the declared architecture views in declaration order. Nil when the workspace declares none.
	Views []View `json:"views,omitempty"`
	// ADR is the ADR governance configuration. Nil when the workspace has no `adr` key.
	ADR *ADR `json:"adr,omitempty"`
	// Trust is the trust context configuration. Nil when the workspace has no `trust` key.
	Trust *Trust `json:"trust,omitempty"`
	// Assessments is the assessment configuration. Nil when the workspace has no `assessments` key.
	Assessments *Assessments `json:"assessments,omitempty"`
	// Assurance is the assurance evidence configuration. Nil when the workspace has no `assurance` key.
	Assurance   *Assurance  `json:"assurance,omitempty"`
	Check       Check       `json:"check"`
	Observation Observation `json:"observation"`
	Limits      Limits      `json:"limits"`
}

type object = map[string]any

var (
	schemaOnce = sync.OnceValues(func() (*jsonschema.Schema, error) {
		doc, err := schemaFS.ReadFile("schemas/workspace.schema.json")
		if err != nil {
			return nil, fmt.Errorf("read embedded workspace schema: %w", err)
		}

		return jsonschema.Compile(SchemaID, doc)
	})

	yamlLine = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)
)

// deepMerge merges over into base: objects deep, arrays and scalars replaced.
// The inputs are not changed.
func deepMerge(base, over any) any {
	baseObj, baseOK := base.(object)
	overObj, overOK := over.(object)

	if !baseOK || !overOK {
		return over
	}

	out := make(object, len(baseObj)+len(overObj))
	for key, val := range baseObj {
		out[key] = val
	}

	for key, val := range overObj {
		if existing, ok := baseObj[key]; ok {
			out[key] = deepMerge(existing, val)
		} else {
			out[key] = val
		}
	}

	return out
}

// normalize changes the maps that the YAML decoder makes with non-string keys
// into maps with string keys (as JSON needs them).
func normalize(node any) any {
	switch val := node.(type) {
	case map[string]any:
		for key, item := range val {
			val[key] = normalize(item)
		}

		return val
	case map[any]any:
		out := make(object, len(val))
		for key, item := range val {
			out[fmt.Sprint(key)] = normalize(item)
		}

		return out
	case []any:
		for i, item := range val {
			val[i] = normalize(item)
		}

		return val
	default:
		return node
	}
}

type yamlProblem struct {
	msg  string
	line int
}

func yamlProblems(err error) []yamlProblem {
	var typeErr *yaml.TypeError

	texts := []string{err.Error()}
	if errors.As(err, &typeErr) {
		texts = typeErr.Errors
	}

	out := make([]yamlProblem, 0, len(texts))

	for _, text := range texts {
		first, _, _ := strings.Cut(text, "\n")

		if m := yamlLine.FindStringSubmatch(first); m != nil {
			line, _ := strconv.Atoi(m[1])
			out = append(out, yamlProblem{line: line, msg: m[2]})

			continue
		}

		out = append(out, yamlProblem{msg: strings.TrimPrefix(first, "yaml: ")})
	}

	return out
}

func invalid(detail, uri string, cause []diag.Diagnostic, line int) *diag.Error {
	return diag.Fail(diag.WRK002, diag.Detail(detail), diag.WithSource(diag.Source{URI: uri, Line: line}), diag.WithCause(cause))
}

// Load loads a workspace file (an OS path). An empty profile selects no profile.
//
// Errors are *diag.Error: CO-WRK-001 (missing), CO-WRK-002 (not valid, one
// cause for each schema error with its JSON Pointer), CO-WRK-004 (unknown
// profile). All have exit code 2.
func Load(path, profile string) (*Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, invalid("cannot resolve path: "+err.Error(), path, nil, 0)
	}

	data, err := os.ReadFile(abs) //nolint:gosec // the caller chooses the workspace path: reading it is the function.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, diag.Fail(diag.WRK001, diag.Params{"path": path})
		}

		return nil, invalid("cannot read file: "+err.Error(), path, nil, 0)
	}

	return loadBytes(path, abs, data, profile)
}

// LoadBytes loads a workspace from the content of its file. abs is the absolute OS path that the file has (or
// had) and fixes the workspace root, so the content may come from another revision of the same file (for
// example the trust anchor). The errors are those of Load, except CO-WRK-001.
func LoadBytes(abs string, data []byte, profile string) (*Workspace, error) {
	return loadBytes(abs, abs, data, profile)
}

// loadBytes loads the content of a workspace file. path names the file in diagnostics and abs is its absolute
// OS path.
func loadBytes(path, abs string, data []byte, profile string) (*Workspace, error) {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, yamlFailure(err, path)
	}

	root, ok := normalize(raw).(object)
	if !ok {
		return nil, invalid("the document must be a mapping", path, nil, 0)
	}

	if err := validate(root, path); err != nil {
		return nil, err
	}

	if err := checkProjectNames(root, path); err != nil {
		return nil, err
	}

	merged, err := mergeProfile(root, profile, path)
	if err != nil {
		return nil, err
	}

	space := applyDefaults(merged, abs, digest.OfBytes(data), profile)

	space.Environments = environmentsOf(root)
	space.Limits = limitsOf(root)
	space.Views = viewsOf(merged)
	space.ADR = adrOf(merged)
	space.Trust = trustOf(merged)
	space.Assessments = assessmentsOf(merged)
	space.Assurance = assuranceOf(merged)

	if err := checkEnvironments(space, path); err != nil {
		return nil, err
	}

	if err := checkRollback(space, path); err != nil {
		return nil, err
	}

	if err := checkAssessments(space, path); err != nil {
		return nil, err
	}

	if err := checkAssurance(space, path); err != nil {
		return nil, err
	}

	if err := checkViews(space, path); err != nil {
		return nil, err
	}

	return space, nil
}

func environmentsOf(root object) []Environment {
	list, _ := root["environments"].([]any)
	out := make([]Environment, 0, len(list))

	for _, item := range list {
		e, _ := item.(object)
		out = append(out, Environment{
			ID:          str(e, "id", ""),
			Sources:     strList(e, "sources", []string{}),
			Cluster:     str(e, "cluster", ""),
			Description: str(e, "description", ""),
		})
	}

	return out
}

// checkEnvironments rejects duplicate environment ids and a runtime
// environment that no `environments` entry declares.
func checkEnvironments(space *Workspace, path string) *diag.Error {
	seen := make(map[string]struct{}, len(space.Environments))

	for _, e := range space.Environments {
		if _, dup := seen[e.ID]; dup {
			return invalid(fmt.Sprintf("environments: id '%s' appears two times", e.ID), path, nil, 0)
		}

		seen[e.ID] = struct{}{}
	}

	if rt := space.Observation.Runtime; rt != nil && rt.Environment != "" {
		if _, ok := seen[rt.Environment]; !ok {
			return invalid(fmt.Sprintf("observation.runtime.environment: '%s' is not declared in environments", rt.Environment), path, nil, 0)
		}
	}

	return nil
}

// Attribute gives the ids of the environments whose sources match the
// workspace-relative path, in declaration order. A shared file matches
// several; an unmatched file gives none.
func (w *Workspace) Attribute(path string) []string {
	var out []string

	for _, e := range w.Environments {
		if fsutil.GlobMatcher(e.Sources)(path) {
			out = append(out, e.ID)
		}
	}

	return out
}

func yamlFailure(err error, path string) *diag.Error {
	problems := yamlProblems(err)
	cause := make([]diag.Diagnostic, len(problems))

	for i, p := range problems {
		cause[i] = diag.New(diag.WRK002, diag.Detail(p.msg), diag.WithSource(diag.Source{URI: path, Line: p.line}))
	}

	return invalid("YAML syntax error: "+cause[0].Message, path, cause, problems[0].line)
}

// checkProjectNames rejects two projects with the same name. The prefix
// @<name>/ must name one project.
func checkProjectNames(root object, path string) *diag.Error {
	obs, ok := root["observation"].(object)
	if !ok {
		return nil
	}

	list, _ := obs["projects"].([]any)
	seen := make(map[string]struct{}, len(list))

	for _, item := range list {
		p, _ := item.(object)
		name := str(p, "name", "")

		if _, dup := seen[name]; dup {
			return invalid(fmt.Sprintf("observation.projects: project name '%s' appears two times", name), path, nil, 0)
		}

		seen[name] = struct{}{}
	}

	return nil
}

func validate(root object, path string) *diag.Error {
	schema, err := schemaOnce()
	if err != nil {
		return diag.Fail(diag.WRK099, diag.Detail(err.Error()))
	}

	errs := schema.Validate(root)
	if len(errs) == 0 {
		return nil
	}

	cause := make([]diag.Diagnostic, len(errs))
	code := assuranceSchemaCode(errs)

	for i, schemaErr := range errs {
		pointer := schemaErr.InstancePath
		shown := pointer

		if shown == "" {
			shown = "/"
		}

		if problem, isView := viewSchemaProblem(root, path, pointer, schemaErr.Message); isView {
			cause[i] = problem

			continue
		}

		cause[i] = diag.New(code, diag.Detail(shown+" "+schemaErr.Message), diag.WithSource(diag.Source{URI: path, Pointer: pointer}))
	}

	return diag.Fail(code, diag.Detail(fmt.Sprintf("%d schema error(s)", len(errs))), diag.WithSource(diag.Source{URI: path}), diag.WithCause(cause))
}

// assuranceSchemaCode gives CO-PRV-001 when every schema error lies inside the `assurance` key (also inside a
// profile), else CO-WRK-002. Both have exit code 2.
func assuranceSchemaCode(errs []jsonschema.Error) string {
	for _, schemaErr := range errs {
		pointer := schemaErr.InstancePath
		inAssurance := strings.HasPrefix(pointer, "/assurance") || (strings.HasPrefix(pointer, "/profiles/") && strings.Contains(pointer, "/assurance"))

		if !inAssurance {
			return diag.WRK002
		}
	}

	return diag.PRV001
}

func mergeProfile(root object, profile, path string) (object, error) {
	base := make(object, len(root))

	for k, v := range root {
		if k != "profiles" {
			base[k] = v
		}
	}

	if profile == "" {
		return base, nil
	}

	profiles, _ := root["profiles"].(object)

	p, ok := profiles[profile]
	if !ok {
		return nil, diag.Fail(diag.WRK004, diag.Params{"profile": profile}, diag.WithSource(diag.Source{URI: path}))
	}

	merged, _ := deepMerge(base, p).(object)

	return merged, nil
}

func obj(m object, key string) object {
	if o, ok := m[key].(object); ok {
		return o
	}

	return object{}
}

func str(m object, key, fallback string) string {
	if s, ok := m[key].(string); ok {
		return s
	}

	return fallback
}

func strList(m object, key string, fallback []string) []string {
	list, ok := m[key].([]any)
	if !ok {
		return fallback
	}

	out := make([]string, 0, len(list))

	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

func num(m object, key string, fallback float64) float64 {
	switch n := m[key].(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case uint64:
		return float64(n)
	case float64:
		return n
	default:
		return fallback
	}
}

func applyDefaults(merged object, abs, fileDigest, profile string) *Workspace {
	obs, arch := obj(merged, "observation"), obj(merged, "architecture")
	chk, exc := obj(merged, "check"), obj(merged, "exceptions")

	return &Workspace{
		Path:    abs,
		Root:    filepath.Dir(abs),
		Digest:  fileDigest,
		Profile: profile,
		Metadata: Metadata{
			Name: str(obj(merged, "metadata"), "name", ""),
		},
		Architecture: Architecture{
			Source:     str(arch, "source", ""),
			URLMapping: str(arch, "urlMapping", ""),
			Patterns:   strList(arch, "patterns", []string{}),
			Decorators: strList(arch, "decorators", []string{}),
		},
		Observation: Observation{
			Provider:            providerObservation,
			Executable:          str(obs, "executable", DefaultExecutable),
			TimeoutSeconds:      num(obs, "timeoutSeconds", DefaultTimeoutSeconds),
			IndexTimeoutSeconds: num(obs, "indexTimeoutSeconds", DefaultIndexTimeoutSeconds),
			Retries:             int(num(obs, "retries", DefaultRetries)),
			IndexMode:           IndexMode(str(obs, "indexMode", string(DefaultIndexMode))),
			Projects:            projects(obs),
			Runtime:             runtimeOf(obs),
		},
		Bindings: Files{Files: strList(obj(merged, "bindings"), "files", DefaultBindingsFiles())},
		Policies: Policies{Files: strList(obj(merged, "policies"), "files", DefaultPolicyFiles()), Packs: policyPacks(obj(merged, "policies"))},
		Exceptions: Exceptions{
			Baseline: str(exc, "baseline", DefaultBaseline),
			Waivers:  str(exc, "waivers", DefaultWaivers),
			// 0 when `exceptions.maxExpiryDays` is not set.
			MaxExpiryDays: int(num(exc, "maxExpiryDays", 0)),
		},
		Projection: Projection{
			Provider: providerProjection,
			Output:   str(obj(merged, "projection"), "output", DefaultProjectionOutput),

			AllowIncludes: strList(obj(merged, "projection"), "allowIncludes", nil),
		},
		Check: Check{
			MinimumCoverage: num(chk, "minimumCoverage", DefaultMinimumCoverage),
			Unknown:         UnknownPolicy(str(chk, "unknown", string(DefaultUnknown))),
			FailOn:          diag.Severity(str(chk, "failOn", string(DefaultFailOn))),
			Enforcement:     enforcementOf(chk),
			Rollback:        rollbackOf(chk),
		},
		Report: Report{Outputs: reportOutputs(obj(merged, "report"))},
	}
}

func projects(obs object) []Project {
	list, _ := obs["projects"].([]any)
	out := make([]Project, 0, len(list))

	for _, item := range list {
		p, _ := item.(object)
		out = append(out, Project{
			Name:    str(p, "name", ""),
			Root:    str(p, "root", defaultProjectRoot),
			Include: strList(p, "include", []string{}),
			Exclude: strList(p, "exclude", []string{}),
		})
	}

	return out
}

func runtimeOf(obs object) *Runtime {
	rt, ok := obs["runtime"].(object)
	if !ok {
		return nil
	}

	return &Runtime{
		Traces:      strList(rt, "traces", []string{}),
		Environment: str(rt, "environment", ""),
		MaxAgeDays:  int(num(rt, "maxAgeDays", 0)),
	}
}

// policyPacks reads `policies.packs`. The schema has checked the shape.
func policyPacks(pol object) []PolicyPackUse {
	packs := []PolicyPackUse{}

	data, err := json.Marshal(pol["packs"])
	if err != nil || pol["packs"] == nil {
		return packs
	}

	if err := json.Unmarshal(data, &packs); err != nil {
		return []PolicyPackUse{}
	}

	return packs
}

func reportOutputs(rep object) []ReportOutput {
	list, ok := rep["outputs"].([]any)
	if !ok {
		return []ReportOutput{DefaultReportOutput()}
	}

	out := make([]ReportOutput, 0, len(list))

	for _, item := range list {
		o, _ := item.(object)
		out = append(out, ReportOutput{Format: ReportFormat(str(o, "format", "")), Path: str(o, "path", "")})
	}

	return out
}

// ResolveInRoot gives the absolute OS path of a workspace-relative path
// (written with "/"). An absolute path stays as it is.
func (w *Workspace) ResolveInRoot(p string) string {
	local := filepath.FromSlash(p)
	if filepath.IsAbs(local) {
		return local
	}

	return filepath.Join(w.Root, local)
}

// PrimaryProject gives the first project. Its paths have no prefix.
func (w *Workspace) PrimaryProject() Project {
	return w.Observation.Projects[0]
}

// ProjectRoot gives the absolute OS repository root of the primary project:
// <workspace root>/<project.root>.
func (w *Workspace) ProjectRoot() string {
	primary := w.PrimaryProject()

	return w.AbsProjectRoot(&primary)
}

// AbsProjectRoot gives the absolute OS repository root of a project.
func (w *Workspace) AbsProjectRoot(p *Project) string {
	return w.ResolveInRoot(p.Root)
}

// PathPrefix gives the prefix of the workspace paths of a project: "" for the
// primary project, else "@<name>/".
func (w *Workspace) PathPrefix(p *Project) string {
	if len(w.Observation.Projects) > 0 && w.Observation.Projects[0].Name == p.Name {
		return ""
	}

	return "@" + p.Name + "/"
}

// ProjectByName finds a project by its name.
func (w *Workspace) ProjectByName(name string) (Project, bool) {
	for _, p := range w.Observation.Projects {
		if p.Name == name {
			return p, true
		}
	}

	return Project{}, false
}

// SplitProjectPath splits "@name/rel" into the project name and rel. project is
// "" when p has no "@" prefix.
func SplitProjectPath(p string) (project, rel string) {
	if !strings.HasPrefix(p, "@") {
		return "", p
	}

	name, rest, ok := strings.Cut(p[1:], "/")
	if !ok {
		return "", p
	}

	return name, rest
}
