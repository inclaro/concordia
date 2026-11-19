package workspace

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// Assurance is the `assurance` configuration: the evidence adapters, producers, evaluators, verification results, and
// quality scenarios of release R3. Every environment named here is an id of the `environments` list, and every path
// is workspace-relative, uses "/", and stays inside the root.
type Assurance struct {
	// Rendering is nil when no source is rendered.
	Rendering *AssuranceRendering `json:"rendering,omitempty"`
	// Provenance is nil when no provenance statement is declared.
	Provenance *AssuranceProvenance `json:"provenance,omitempty"`
	// Records is nil when no deployment record or workload observation is declared.
	Records *AssuranceRecords `json:"records,omitempty"`
	// Verification is nil when no check result is declared.
	Verification *AssuranceVerification `json:"verification,omitempty"`
	// Quality is nil when no quality scenario is declared.
	Quality    *AssuranceQuality    `json:"quality,omitempty"`
	Plans      []AssurancePlan      `json:"plans,omitempty"`
	Producers  []Producer           `json:"producers,omitempty"`
	Evaluators []AssuranceEvaluator `json:"evaluators,omitempty"`
}

// AssuranceRendering holds the rendered sources and the confinement settings.
type AssuranceRendering struct {
	Sources []RenderSource  `json:"sources"`
	Limits  RenderingLimits `json:"limits"`
	// RequireOsSandbox makes the OS sandbox mandatory: without it the run stops with CO-PRV-004 (exit 5).
	RequireOsSandbox bool `json:"requireOsSandbox"`
}

// RenderingLimits bound one render. Defaults: 120 seconds, 64 MiB, 20000 resources.
type RenderingLimits struct {
	TimeoutSeconds int   `json:"timeoutSeconds"`
	MaxOutputBytes int64 `json:"maxOutputBytes"`
	MaxResources   int   `json:"maxResources"`
}

// Render tools.
const (
	RenderToolHelm      = "helm"
	RenderToolKustomize = "kustomize"
	RenderToolPlain     = "plain"
)

// RenderSource is one source rendered for one environment.
type RenderSource struct {
	Environment string `json:"environment"`
	// Tool is `helm`, `kustomize`, or `plain`.
	Tool string `json:"tool"`
	// Path is the chart directory, the overlay directory, or the directory or file of plain manifests.
	Path string `json:"path"`
	// Release and Namespace are the Helm release name and namespace. Only for `helm`.
	Release   string `json:"release,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// ValuesFiles are the Helm values files, in order. Only for `helm`.
	ValuesFiles []string `json:"valuesFiles,omitempty"`
}

// Plan tools.
const (
	PlanToolTerraform = "terraform"
	PlanToolOpenTofu  = "opentofu"
)

// AssurancePlan is one Terraform or OpenTofu plan JSON file for one environment.
type AssurancePlan struct {
	Environment string `json:"environment"`
	// Tool is `terraform` or `opentofu`.
	Tool string `json:"tool"`
	// File is the output of `show -json`.
	File string `json:"file"`
	// Statement is the in-toto statement that binds the plan to a revision. Empty: the plan is `content not verified`.
	Statement  string `json:"statement,omitempty"`
	MaxAgeDays int    `json:"maxAgeDays,omitempty"`
}

// Producer claim kinds: what a producer is trusted to state.
const (
	ProducerClaimBuildProvenance     = "build-provenance"
	ProducerClaimCheckResult         = "check-result"
	ProducerClaimMeasurement         = "measurement"
	ProducerClaimPlan                = "plan"
	ProducerClaimDeploymentRecord    = "deployment-record"
	ProducerClaimWorkloadObservation = "workload-observation"
)

// Producer is one trusted producer of statements.
type Producer struct {
	ID             string                 `json:"id"`
	Authentication ProducerAuthentication `json:"authentication"`
	// Claims are the producer claim kinds this producer is trusted for.
	Claims []string `json:"claims"`
}

// Producer authentication methods.
const (
	AuthCosignKey     = "cosign-key"
	AuthCosignKeyless = "cosign-keyless"
)

// ProducerAuthentication says how a producer signs. `cosign-key` needs Key; `cosign-keyless` needs Issuer and Identity.
type ProducerAuthentication struct {
	Method string `json:"method"`
	// Key is a cosign key reference: a workspace-relative public key file, or a KMS or `env://` reference.
	Key      string `json:"key,omitempty"`
	Issuer   string `json:"issuer,omitempty"`
	Identity string `json:"identity,omitempty"`
}

// AssuranceProvenance declares build provenance statements and the build contract they must meet.
type AssuranceProvenance struct {
	// Statements are workspace-relative globs.
	Statements []string `json:"statements"`
	// Builders are the trusted builder ids.
	Builders []string `json:"builders"`
	// SourceRepositories are the ids of the declared source repositories that a build may come from.
	SourceRepositories []string `json:"sourceRepositories"`
	MaxAgeDays         int      `json:"maxAgeDays,omitempty"`
}

// AssuranceRecords declares deployment records and workload observations (workspace-relative globs).
type AssuranceRecords struct {
	Deployments  []string `json:"deployments,omitempty"`
	Observations []string `json:"observations,omitempty"`
	MaxAgeDays   int      `json:"maxAgeDays,omitempty"`
}

// Evaluator kinds.
const (
	EvaluatorNetworkPolicy = "network-policy"
	EvaluatorRBAC          = "rbac"
)

// Enforcement assumptions that an evaluator result may rely on.
const (
	AssumeNetworkPolicyEnforced = "network-policy-enforced"
	AssumeRBACAuthorizerEnabled = "rbac-authorizer-enabled"
)

// AssuranceEvaluator enables effective-policy evaluators for one environment.
type AssuranceEvaluator struct {
	Environment string `json:"environment"`
	// Kinds are `network-policy` and `rbac`.
	Kinds []string `json:"kinds"`
	// Assumes are the declared enforcement assumptions, for example `network-policy-enforced`.
	Assumes []string `json:"assumes,omitempty"`
}

// AssuranceVerification declares check results.
type AssuranceVerification struct {
	// TrustedRoot is a workspace-relative Sigstore trusted_root.json. When set, every statement verification passes it
	// to cosign, which then verifies without network access; under --offline it is the only way to verify.
	TrustedRoot string `json:"trustedRoot,omitempty"`
	// Results are workspace-relative globs of check-result statements.
	Results    []string `json:"results"`
	MaxAgeDays int      `json:"maxAgeDays,omitempty"`
}

// AssuranceQuality declares quality scenario files, measurements, and the areas that must have a scenario.
type AssuranceQuality struct {
	// Scenarios are workspace-relative globs of QualityScenarioSet files.
	Scenarios []string `json:"scenarios"`
	// Measurements are workspace-relative globs of measurement statements.
	Measurements  []string `json:"measurements,omitempty"`
	RequiredAreas []string `json:"requiredAreas,omitempty"`
}

// Default rendering limits.
const (
	DefaultRenderTimeoutSeconds = 120
	DefaultRenderMaxOutputBytes = 64 << 20
	DefaultRenderMaxResources   = 20000
)

// assuranceOf reads the merged `assurance` object and applies the defaults. It returns nil when there is no
// `assurance` key. The schema has checked the shape, so the JSON round trip cannot lose a field.
func assuranceOf(merged object) *Assurance {
	raw, ok := merged["assurance"]
	if !ok {
		return nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}

	out := &Assurance{}
	if err := json.Unmarshal(data, out); err != nil {
		return nil
	}

	applyRenderingDefaults(out.Rendering)
	applyProvenanceDefaults(out.Provenance)

	return out
}

// setIfZero sets the value to the default when the value is zero.
func setIfZero[T comparable](value *T, def T) {
	var zero T
	if *value == zero {
		*value = def
	}
}

// applyRenderingDefaults sets the default rendering limits. It does nothing for a nil rendering.
func applyRenderingDefaults(r *AssuranceRendering) {
	if r == nil {
		return
	}

	setIfZero(&r.Limits.TimeoutSeconds, DefaultRenderTimeoutSeconds)
	setIfZero(&r.Limits.MaxOutputBytes, DefaultRenderMaxOutputBytes)
	setIfZero(&r.Limits.MaxResources, DefaultRenderMaxResources)
}

// applyProvenanceDefaults makes the lists that the provenance leaves out empty. It does nothing for nil provenance.
func applyProvenanceDefaults(p *AssuranceProvenance) {
	if p == nil {
		return
	}

	if p.Builders == nil {
		p.Builders = []string{}
	}

	if p.SourceRepositories == nil {
		p.SourceRepositories = []string{}
	}
}

// UsesRenderer says whether a declared rendering source runs the given renderer (`helm` or `kustomize`).
func (a *Assurance) UsesRenderer(tool string) bool {
	if a == nil || a.Rendering == nil {
		return false
	}

	for i := range a.Rendering.Sources {
		if a.Rendering.Sources[i].Tool == tool {
			return true
		}
	}

	return false
}

// hasRootedPrefix says whether a path starts at a root: "/" after the slashes are unified, an absolute path of the
// OS, or a volume name.
func hasRootedPrefix(p, unified string) bool {
	if strings.HasPrefix(unified, "/") {
		return true
	}

	return filepath.IsAbs(filepath.FromSlash(p)) || filepath.VolumeName(p) != ""
}

// pathEscapesRoot says whether a workspace-relative path or glob leaves the workspace root by its text: an
// absolute path, a Windows drive or backslash root, or a ".." element. Symbolic links are not looked at: the
// file may not exist yet, and the readers confine every file they open.
func pathEscapesRoot(p string) bool {
	unified := strings.ReplaceAll(p, `\`, "/")

	return hasRootedPrefix(p, unified) || slices.Contains(strings.Split(unified, "/"), "..")
}

// pathCollector collects the declared paths of the `assurance` key with the JSON Pointer of each place.
type pathCollector struct {
	paths []pointedPath
}

func (c *pathCollector) add(pointer, value string) {
	c.paths = append(c.paths, pointedPath{pointer: pointer, value: value})
}

// addIfSet adds the path unless it is empty.
func (c *pathCollector) addIfSet(pointer, value string) {
	if value != "" {
		c.add(pointer, value)
	}
}

func (c *pathCollector) addAll(pointer string, values []string) {
	for i, value := range values {
		c.add(fmt.Sprintf("%s/%d", pointer, i), value)
	}
}

func (c *pathCollector) addRendering(r *AssuranceRendering) {
	if r == nil {
		return
	}

	for i := range r.Sources {
		base := fmt.Sprintf("/assurance/rendering/sources/%d", i)
		c.add(base+"/path", r.Sources[i].Path)
		c.addAll(base+"/valuesFiles", r.Sources[i].ValuesFiles)
	}
}

func (c *pathCollector) addPlans(plans []AssurancePlan) {
	for i := range plans {
		base := fmt.Sprintf("/assurance/plans/%d", i)
		c.add(base+"/file", plans[i].File)
		c.addIfSet(base+"/statement", plans[i].Statement)
	}
}

// addProducerKeys adds the key files of the producers. A key that is a KMS or `env://` reference has a scheme: it is
// not a path.
func (c *pathCollector) addProducerKeys(producers []Producer) {
	for i := range producers {
		if key := producers[i].Authentication.Key; !strings.Contains(key, "://") {
			c.addIfSet(fmt.Sprintf("/assurance/producers/%d/authentication/key", i), key)
		}
	}
}

func (c *pathCollector) addEvidence(cfg *Assurance) {
	if p := cfg.Provenance; p != nil {
		c.addAll("/assurance/provenance/statements", p.Statements)
	}

	if r := cfg.Records; r != nil {
		c.addAll("/assurance/records/deployments", r.Deployments)
		c.addAll("/assurance/records/observations", r.Observations)
	}

	if v := cfg.Verification; v != nil {
		c.addAll("/assurance/verification/results", v.Results)
		c.addIfSet("/assurance/verification/trustedRoot", v.TrustedRoot)
	}

	if q := cfg.Quality; q != nil {
		c.addAll("/assurance/quality/scenarios", q.Scenarios)
		c.addAll("/assurance/quality/measurements", q.Measurements)
	}
}

// assurancePaths lists every declared path and glob of the `assurance` key with the JSON Pointer of its place.
// A producer key that is a KMS or `env://` reference (it has a scheme) is not a path and is left out.
func assurancePaths(cfg *Assurance) []pointedPath {
	var c pathCollector

	c.addRendering(cfg.Rendering)
	c.addPlans(cfg.Plans)
	c.addProducerKeys(cfg.Producers)
	c.addEvidence(cfg)

	return c.paths
}

type pointedPath struct {
	pointer string
	value   string
}

// checkAssurance rejects what the schema cannot: an environment that `environments` does not declare
// (CO-WRK-017), a path outside the root (CO-PRV-002), and a duplicate declaration (CO-PRV-001). All have exit 2.
func checkAssurance(space *Workspace, path string) *diag.Error {
	cfg := space.Assurance
	if cfg == nil {
		return nil
	}

	if err := checkAssuranceEnvironments(space, cfg, path); err != nil {
		return err
	}

	for _, declared := range assurancePaths(cfg) {
		if pathEscapesRoot(declared.value) {
			return diag.Fail(diag.PRV002, diag.Params{"path": declared.value},
				diag.WithSource(diag.Source{URI: path, Pointer: declared.pointer}))
		}
	}

	return checkAssuranceDuplicates(cfg, path)
}

// renderSources gives the rendering sources, or nil when no source is rendered.
func (a *Assurance) renderSources() []RenderSource {
	if a.Rendering == nil {
		return nil
	}

	return a.Rendering.Sources
}

// pointedValues gives the value of every item with the JSON Pointer of the item. The format makes the pointer from
// the index of the item.
func pointedValues[T any](items []T, format string, value func(*T) string) []pointedPath {
	out := make([]pointedPath, 0, len(items))

	for i := range items {
		out = append(out, pointedPath{pointer: fmt.Sprintf(format, i), value: value(&items[i])})
	}

	return out
}

// assuranceEnvironmentRefs lists the environment of every rendering source, plan, and evaluator with its pointer.
func assuranceEnvironmentRefs(cfg *Assurance) []pointedPath {
	refs := pointedValues(cfg.renderSources(), "/assurance/rendering/sources/%d/environment", func(s *RenderSource) string { return s.Environment })
	refs = append(refs, pointedValues(cfg.Plans, "/assurance/plans/%d/environment", func(p *AssurancePlan) string { return p.Environment })...)

	return append(refs, pointedValues(cfg.Evaluators, "/assurance/evaluators/%d/environment", func(e *AssuranceEvaluator) string { return e.Environment })...)
}

func checkAssuranceEnvironments(space *Workspace, cfg *Assurance, path string) *diag.Error {
	declared := make(map[string]struct{}, len(space.Environments))
	for _, e := range space.Environments {
		declared[e.ID] = struct{}{}
	}

	for _, ref := range assuranceEnvironmentRefs(cfg) {
		if _, ok := declared[ref.value]; !ok {
			return diag.Fail(diag.WRK017, diag.Detail(fmt.Sprintf("'%s' (declared in assurance: %s)", ref.value, strings.TrimPrefix(ref.pointer, "/assurance/"))),
				diag.WithSource(diag.Source{URI: path, Pointer: ref.pointer}))
		}
	}

	return nil
}

// duplicate is a declaration that appears two times: the JSON Pointer of the second one and the problem text.
type duplicate struct {
	pointer string
	detail  string
}

// firstDuplicate gives the index of the first item whose key an earlier item has, or -1 when all keys differ.
func firstDuplicate[T any, K comparable](items []T, keyOf func(*T) K) int {
	seen := make(map[K]struct{}, len(items))

	for i := range items {
		key := keyOf(&items[i])
		if _, dup := seen[key]; dup {
			return i
		}

		seen[key] = struct{}{}
	}

	return -1
}

func duplicateProducer(cfg *Assurance) *duplicate {
	i := firstDuplicate(cfg.Producers, func(p *Producer) string { return p.ID })
	if i < 0 {
		return nil
	}

	return &duplicate{
		pointer: fmt.Sprintf("/assurance/producers/%d/id", i),
		detail:  fmt.Sprintf("assurance.producers: id '%s' appears two times", cfg.Producers[i].ID),
	}
}

func duplicateEvaluator(cfg *Assurance) *duplicate {
	i := firstDuplicate(cfg.Evaluators, func(e *AssuranceEvaluator) string { return e.Environment })
	if i < 0 {
		return nil
	}

	return &duplicate{
		pointer: fmt.Sprintf("/assurance/evaluators/%d/environment", i),
		detail:  fmt.Sprintf("assurance.evaluators: environment '%s' appears two times", cfg.Evaluators[i].Environment),
	}
}

func duplicateRenderSource(cfg *Assurance) *duplicate {
	sources := cfg.renderSources()

	i := firstDuplicate(sources, func(s *RenderSource) [3]string { return [3]string{s.Environment, s.Tool, s.Path} })
	if i < 0 {
		return nil
	}

	src := &sources[i]
	detail := fmt.Sprintf("assurance.rendering.sources: %s source '%s' of environment '%s' appears two times", src.Tool, src.Path, src.Environment)

	return &duplicate{pointer: fmt.Sprintf("/assurance/rendering/sources/%d", i), detail: detail}
}

func duplicatePlan(cfg *Assurance) *duplicate {
	i := firstDuplicate(cfg.Plans, func(p *AssurancePlan) [2]string { return [2]string{p.Environment, p.File} })
	if i < 0 {
		return nil
	}

	return &duplicate{
		pointer: fmt.Sprintf("/assurance/plans/%d", i),
		detail:  fmt.Sprintf("assurance.plans: plan '%s' of environment '%s' appears two times", cfg.Plans[i].File, cfg.Plans[i].Environment),
	}
}

func checkAssuranceDuplicates(cfg *Assurance, path string) *diag.Error {
	for _, find := range []func(*Assurance) *duplicate{duplicateProducer, duplicateEvaluator, duplicateRenderSource, duplicatePlan} {
		if found := find(cfg); found != nil {
			return diag.Fail(diag.PRV001, diag.Detail(found.detail), diag.WithSource(diag.Source{URI: path, Pointer: found.pointer}))
		}
	}

	return nil
}
