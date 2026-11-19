package observation

import (
	"cmp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// Loss mode identifiers (CBM 0.12.1 observations, O1 to O11). They are stable
// names: policies, results, and the profile use them.
const (
	// LossDefaultExcludedDirectory (O1): an environment source is under a directory that the provider skips by default.
	LossDefaultExcludedDirectory = "default-excluded-directory"
	// LossRasterNotIndexed (O1): a raster image is not indexed.
	LossRasterNotIndexed = "raster-not-indexed"
	// LossParsePartial (O1): the provider parsed a file in part.
	LossParsePartial = "parse-partial"
	// LossTargetNotDeclared (O5): a reference has no edge because no indexed manifest declares the target.
	LossTargetNotDeclared = "target-not-declared"
	// LossSelectorJoinDirectoryScoped (O6): the provider joins selectors only between files of one directory.
	LossSelectorJoinDirectoryScoped = "selector-join-directory-scoped"
	// LossSelectorJoinOneOfCompeting (O6): the provider joins one of several competing declarations.
	LossSelectorJoinOneOfCompeting = "selector-join-one-of-competing"
	// LossEmptySelector (O6): an empty selector gives no edge.
	LossEmptySelector = "empty-selector"
	// LossUnsupportedSelectorExpression (O6): a selector expression gives no edge.
	LossUnsupportedSelectorExpression = "unsupported-selector-expression"
	// LossRouteIdentityUnscoped (O7): one Route node is shared by the environments that declare the same host and path.
	LossRouteIdentityUnscoped = "route-identity-unscoped"
	// LossDeployTargetIsFile (O8): a `kubectl apply -f` action targets a file, not the resources in it.
	LossDeployTargetIsFile = "deploy-target-is-file"
	// LossBuildLinkNeedsBuildCommand (O9): an image is linked to its Dockerfile only by a build command.
	LossBuildLinkNeedsBuildCommand = "build-link-needs-build-command"
	// LossOverrideUnsignalled (O11): a Kustomize image override gives no edge.
	LossOverrideUnsignalled = "override-unsignalled"
	// LossUnrenderedTemplate (O11): a template is not rendered, so its resources are not facts.
	LossUnrenderedTemplate = "unrendered-template"
)

// KnownLossModes gives the loss mode identifiers that a profile and a coverage record can name, sorted.
func KnownLossModes() []string {
	modes := []string{
		LossDefaultExcludedDirectory, LossRasterNotIndexed, LossParsePartial, LossMergedOccurrences,
		LossTargetNotDeclared, LossSelectorJoinDirectoryScoped, LossSelectorJoinOneOfCompeting, LossEmptySelector,
		LossUnsupportedSelectorExpression, LossRouteIdentityUnscoped, LossDeployTargetIsFile,
		LossBuildLinkNeedsBuildCommand, LossOverrideUnsignalled, LossUnrenderedTemplate,
	}

	slices.SortFunc(modes, canonjson.CompareUTF16)

	return modes
}

// CoverageStatus is the completion status of a predicate in one scope.
type CoverageStatus string

// Coverage statuses.
const (
	// CoverageComplete means the provider extracted everything that the predicate needs in the scope.
	CoverageComplete CoverageStatus = "complete"
	// CoveragePartial means the extraction is incomplete: a loss mode applies.
	CoveragePartial CoverageStatus = "partial"
	// CoverageUnsupported means the provider build does not supply the predicate.
	CoverageUnsupported CoverageStatus = "unsupported"
	// CoverageFailed means the provider failed on the scope.
	CoverageFailed CoverageStatus = "failed"
	// CoverageUnknown means Concordia cannot tell how complete the extraction is.
	CoverageUnknown CoverageStatus = "unknown"
)

// Rank orders the statuses by how much they limit a claim: complete 0, partial 1, unknown 2, unsupported 3, failed 4.
func (s CoverageStatus) Rank() int {
	order := []CoverageStatus{CoverageComplete, CoveragePartial, CoverageUnknown, CoverageUnsupported, CoverageFailed}
	if i := slices.Index(order, s); i >= 0 {
		return i
	}

	return slices.Index(order, CoverageUnknown)
}

// CoverageScope says what a coverage record covers. A record for a source
// project has a Project and no Environment; a record for the sources of a
// declared environment has an Environment and no Project.
type CoverageScope struct {
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// CoverageGap is one place where evidence is missing or cannot be trusted. It
// holds structure only: names, kinds, paths, and lines. It never holds a value
// from a manifest (an environment value, a Secret `data` entry).
type CoverageGap struct {
	// Reason is the loss mode identifier.
	Reason string `json:"reason"`
	// Path is the workspace-relative "/" path of the file (or the directory, with a trailing "/", for an excluded directory).
	Path string `json:"path,omitempty"`
	// Project is the project of the file, Environment the environment that the file is attributed to (empty for none).
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	// Workload names the resource as `<Kind>/<name>` (for a workload or a selecting resource).
	Workload string `json:"workload,omitempty"`
	// Container is the container name of a `merged-occurrences` gap.
	Container string `json:"container,omitempty"`
	// ReferenceMode, Target and TargetKind describe a `target-not-declared` gap: the reference mode (`env`, `envFrom`, `volume`, ...), the name and the kind of the target.
	ReferenceMode string `json:"referenceMode,omitempty"`
	Target        string `json:"target,omitempty"`
	TargetKind    string `json:"targetKind,omitempty"`
	// Source is the environment source glob of a `default-excluded-directory` gap.
	Source string `json:"source,omitempty"`
	// Hint says what to do, for example the `.cbmignore` line.
	Hint string `json:"hint,omitempty"`
	// Detail is a short structural note, never a value.
	Detail string `json:"detail,omitempty"`
	// Line is the 1-based line, 0 when unknown.
	Line int `json:"line,omitempty"`
}

func compareGaps(a, b *CoverageGap) int {
	return cmp.Or(
		canonjson.CompareUTF16(a.Reason, b.Reason), canonjson.CompareUTF16(a.Path, b.Path), cmp.Compare(a.Line, b.Line),
		canonjson.CompareUTF16(a.Project, b.Project), canonjson.CompareUTF16(a.Environment, b.Environment),
		canonjson.CompareUTF16(a.Workload, b.Workload), canonjson.CompareUTF16(a.Container, b.Container),
		canonjson.CompareUTF16(a.ReferenceMode, b.ReferenceMode), canonjson.CompareUTF16(a.TargetKind, b.TargetKind),
		canonjson.CompareUTF16(a.Target, b.Target), canonjson.CompareUTF16(a.Source, b.Source),
		canonjson.CompareUTF16(a.Detail, b.Detail), canonjson.CompareUTF16(a.Hint, b.Hint),
	)
}

// SortGaps sorts gaps in place by (reason, path, line, project, environment, ...) and removes duplicates.
func SortGaps(gaps []CoverageGap) []CoverageGap {
	slices.SortFunc(gaps, func(a, b CoverageGap) int { return compareGaps(&a, &b) })

	return slices.CompactFunc(gaps, func(a, b CoverageGap) bool { return compareGaps(&a, &b) == 0 })
}

// LossMode is a loss mode that applies to a predicate in a scope, with the
// gaps (each with its location) that show it. A loss mode that the profile
// states for the whole family has no gap.
type LossMode struct {
	ID string `json:"id"`
	// ResolvedBy names the level of the record that cured the loss mode (`rendered-or-evaluated`). Empty for a loss
	// mode that applies.
	ResolvedBy string        `json:"resolvedBy,omitempty"`
	Gaps       []CoverageGap `json:"gaps,omitempty"`
}

// PredicateCoverage is the coverage of one predicate (a relationship family,
// a property family, or a syntax) in one scope.
type PredicateCoverage struct {
	Scope     CoverageScope  `json:"scope"`
	Predicate string         `json:"predicate"`
	Status    CoverageStatus `json:"status"`
	// LossModes are the loss modes that apply, sorted by identifier. Never nil.
	LossModes []LossMode `json:"lossModes"`
	// Reasons are the diagnostic reasons of a status below `complete`, sorted. Never nil.
	Reasons []string `json:"reasons"`
	// Resolved are the loss modes with the gaps that the rendered record of the environment cured. They stay listed so
	// that a reader sees what the provider alone could not show, and they no longer limit the predicate. Left out when
	// no rendered record exists (the record is the one of before).
	Resolved []LossMode `json:"resolved,omitempty"`
	// Levels are the status of the predicate at the provider level and at the rendered level, listed beside a
	// rendered record only. The predicate is `complete` if either is.
	Levels []LevelRecord `json:"levels,omitempty"`
	// Unresolved counts the gaps that apply to the predicate in the scope.
	Unresolved int `json:"unresolved"`
}

// Complete says whether the status is `complete`.
func (p *PredicateCoverage) Complete() bool { return p.Status == CoverageComplete }

// HasLossMode says whether a loss mode applies.
func (p *PredicateCoverage) HasLossMode(id string) bool {
	return slices.ContainsFunc(p.LossModes, func(m LossMode) bool { return m.ID == id })
}

// Reasons of a predicate coverage record.
const (
	ReasonFamilyUnsupported = "family unsupported by the provider build"
	ReasonFamilyNotDeclared = "family not declared by the capability profile"
	ReasonIndexUnavailable  = "index result unavailable"
	ReasonNoNativeEdges     = "no native edge of this family in the graph"
	ReasonCrossCheckNotRun  = "manifest cross-check did not run for the scope"
	reasonPropertyMissing   = "required property missing: "
	// ReasonUnsupportedDeploymentSource names a Compose or Terraform file in the sources of an environment. The
	// reason is followed by `: <path>`.
	ReasonUnsupportedDeploymentSource = "unsupported deployment source"
	reasonScopeFailed                 = "scope failed: "
	reasonFamilyPartialLevel          = "family is partial in the capability profile"
)

// ScopeFailure records a query-level failure in one project. The coverage of
// the scope is `failed`; the run goes on with the other projects.
type ScopeFailure struct {
	Project string `json:"project"`
	// Reason is the diagnostic code and message of the failure.
	Reason string `json:"reason"`
}

// ProjectScope is the input for one source project.
type ProjectScope struct {
	// Index is the index result of the project. Nil means "index result unavailable".
	Index *IndexRecord
	Name  string
	// UnindexedDockerfiles are the Dockerfiles of the project (workspace paths) that the graph has no file for
	// and that no default-excluded directory explains. Each makes `dockerfile-set` unknown.
	UnindexedDockerfiles []string
}

// EnvironmentScope is the input for one declared environment.
type EnvironmentScope struct {
	ID string
	// Projects are the projects whose files the sources of the environment select. A failed one fails the environment.
	Projects []string
	// UnsupportedSources are the Compose and Terraform files in the sources (CrossCheckResult.UnsupportedSources).
	// Each makes `deployment-workload-set` unknown.
	UnsupportedSources []string
	// Workloads are the workload documents that the cross-check read in the environment. A document that has no
	// provider node makes `deployment-workload-set` unknown, unless a default-excluded directory or a partly
	// parsed file explains it.
	Workloads []WorkloadDocument
	// CrossChecked says that the manifest cross-check ran over the sources of the environment.
	CrossChecked bool
}

// PredicateInput is the input of ComputePredicateCoverage.
type PredicateInput struct {
	// Families is the support of each family: the profile compared with the graph schema (Capabilities.Families).
	// Nil means no capability profile applies: the code families are supported and all others unsupported.
	Families     map[string]FamilyState
	Projects     []ProjectScope
	Environments []EnvironmentScope
	// Failures are the projects whose query failed.
	Failures []ScopeFailure
	// Gaps are all gaps of the run (index result, cross-check, templates, facts). Project and Environment attribute each gap to a scope.
	Gaps []CoverageGap
	// Rendered are the rendered-level records of the environments that have a rendering source (the assurance
	// adapter). nil: no assurance evidence, so the records are those of the provider alone.
	Rendered []RenderedRecord
}

// ScopeProjectNames gives the names of the projects that a ScopeFailure list names, sorted.
func ScopeProjectNames(failures []ScopeFailure) []string {
	names := make([]string, len(failures))
	for i, f := range failures {
		names[i] = f.Project
	}

	slices.SortFunc(names, canonjson.CompareUTF16)

	return slices.Compact(names)
}

// concordiaAPIGroup is the API group of the Concordia control resources.
const concordiaAPIGroup = "concordia.io"

// FactGaps gives the gaps that the provider evidence itself shows:
//   - `deploy-target-is-file` for each `deploys-artifact` fact of a `kubectl` action (the target is a file; the resources in it are not claimed as deployed),
//   - `unrendered-template` for each `Resource` node whose identity is a literal template (`{{` in the name) or whose namespace is undeclared (`?`, a Helm template or Kustomize base). Resources of the `concordia.io` API group are skipped.
//
// The gaps are sorted and carry no project or environment attribution beyond the node environment.
func FactGaps(res *EvidenceQueryResult) []CoverageGap {
	if res == nil {
		return nil
	}

	return SortGaps(append(deployTargetGaps(res), templateGaps(res)...))
}

// isKubectlDeploy says whether an edge is a `deploys-artifact` fact of a `kubectl` action.
func isKubectlDeploy(e *EvidenceEdge) bool {
	return e.Kind == KindDeploysArtifact && e.Properties["via"] == "kubectl"
}

// deployGapOf gives the `deploy-target-is-file` gap of an edge, located at the edge or at its source node.
func deployGapOf(e *EvidenceEdge, nodes map[string]ObservedNode) CoverageGap {
	gap := CoverageGap{Reason: LossDeployTargetIsFile, Environment: e.Environment}

	if e.SourceSpan != nil {
		gap.Path, gap.Line = e.SourceSpan.Path, e.SourceSpan.Line

		return gap
	}

	if from, ok := nodes[e.From]; ok {
		gap.Path, gap.Line = from.FilePath, from.StartLine
	}

	return gap
}

// deployTargetGaps gives a `deploy-target-is-file` gap for each `kubectl` deploy fact.
func deployTargetGaps(res *EvidenceQueryResult) []CoverageGap {
	var gaps []CoverageGap

	for _, e := range res.Edges {
		if isKubectlDeploy(e) {
			gaps = append(gaps, deployGapOf(e, res.Nodes))
		}
	}

	return gaps
}

// templateGaps gives an `unrendered-template` gap for each Resource node with a templated identity.
func templateGaps(res *EvidenceQueryResult) []CoverageGap {
	var gaps []CoverageGap

	for id := range res.Nodes {
		node := res.Nodes[id]
		if node.Label == labelResource && isTemplatedResource(&node) {
			gaps = append(gaps, CoverageGap{Reason: LossUnrenderedTemplate, Path: node.FilePath, Line: node.StartLine, Environment: node.Environment, Detail: "templated or namespace-less resource identity"})
		}
	}

	return gaps
}

// isTemplatedResource says whether a Resource node has a literal template in its identity or an undeclared namespace.
// Concordia's own control resources (the workspace, the register) are never templates.
func isTemplatedResource(node *ObservedNode) bool {
	key, ok := ObjKeyOf(node)
	if !ok {
		return false
	}

	parsed, err := ParseObjKey(key)
	if err == nil && parsed.Group == concordiaAPIGroup {
		return false
	}

	if strings.Contains(key, "{{") {
		return true
	}

	return err == nil && parsed.NamespaceUnresolved
}

// CrossCheckEvidenceKinds are the canonical kinds that the manifest cross-check compares the manifests with: the
// container occurrences (O4) and the selector joins (O6). A run with declared environments fetches them even
// when no rule traverses them, because without the facts the cross-check would report every container as lost.
func CrossCheckEvidenceKinds() []CanonicalEdgeKind {
	return []CanonicalEdgeKind{KindUsesImage, KindSelectsWorkload, KindPolicySelectsWorkload, KindDisruptionPolicyTarget}
}
