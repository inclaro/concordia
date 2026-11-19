package observation

import (
	"cmp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// crossCheckLossModes are the loss modes that only the manifest cross-check detects.
var crossCheckLossModes = []string{LossMergedOccurrences, LossTargetNotDeclared, LossEmptySelector, LossUnsupportedSelectorExpression}

// isEnvironmentPredicate says whether a predicate depends on the manifests of an environment:
// the infrastructure families, the occurrence predicate, and the rendering predicate.
func isEnvironmentPredicate(name string) bool {
	switch name {
	case FamilyContainerOccurrenceVersions, FamilyRenderedTemplates, FamilyDeploymentWorkloadSet:
		return true
	}

	return IsInfrastructureKind(CanonicalEdgeKind(name))
}

// isDiagramPredicate says whether a predicate is about diagrams.
func isDiagramPredicate(name string) bool {
	return name == string(KindDiagramArrow) || name == FamilyDiagramIdentity
}

// sourceGapAppliesTo says whether a gap about a source file (a skipped directory, a partly parsed
// file, an unrendered template) shows a loss in a predicate.
func sourceGapAppliesTo(gap *CoverageGap, predicate string) bool {
	if predicate == FamilyIndexResult && gap.Reason != LossUnrenderedTemplate {
		return true
	}

	// A Dockerfile gap is about the Dockerfile set only, and never about the workload set.
	if predicate == FamilyDockerfileSet {
		return gap.Reason != LossUnrenderedTemplate && IsDockerfile(gap.Path)
	}

	if predicate == FamilyDeploymentWorkloadSet && IsDockerfile(gap.Path) {
		return false
	}

	return isEnvironmentPredicate(predicate)
}

// gapAppliesTo says whether a gap shows a loss in a predicate.
func gapAppliesTo(gap *CoverageGap, predicate string, state *FamilyState) bool {
	switch gap.Reason {
	case LossDefaultExcludedDirectory, LossParsePartial, LossUnrenderedTemplate:
		return sourceGapAppliesTo(gap, predicate)
	case LossRasterNotIndexed:
		return predicate == FamilyIndexResult || isDiagramPredicate(predicate)
	default:
		return slices.Contains(state.LossModes, gap.Reason)
	}
}

type recordBuilder struct {
	in        *PredicateInput
	families  map[string]FamilyState
	failed    map[string]string
	indexOf   map[string]*IndexRecord
	envOf     map[string]*EnvironmentScope
	projectOf map[string]*ProjectScope
}

// newRecordBuilder indexes the scopes of the input, and sorts its gaps.
func newRecordBuilder(input *PredicateInput) *recordBuilder {
	families := input.Families
	if families == nil {
		families = NoProfileFamilies()
	}

	sorted := *input
	sorted.Gaps = SortGaps(slices.Clone(input.Gaps))

	builder := &recordBuilder{
		in: &sorted, families: families, failed: map[string]string{},
		indexOf: map[string]*IndexRecord{}, envOf: map[string]*EnvironmentScope{}, projectOf: map[string]*ProjectScope{},
	}

	for _, f := range sorted.Failures {
		builder.failed[f.Project] = f.Reason
	}

	for i := range sorted.Projects {
		builder.indexOf[sorted.Projects[i].Name] = sorted.Projects[i].Index
		builder.projectOf[sorted.Projects[i].Name] = &sorted.Projects[i]
	}

	for i := range sorted.Environments {
		builder.envOf[sorted.Environments[i].ID] = &sorted.Environments[i]
	}

	return builder
}

// ComputePredicateCoverage gives the coverage records of a run: one for each
// family (relationship families and property families) in each source project,
// and one for each infrastructure family in each declared environment, sorted
// by (predicate, project, environment).
//
// A record starts from the support of the family (supported: complete, partial:
// partial, unsupported: unsupported). A failed scope is `failed`. Each gap that
// applies adds its loss mode and a location, and a loss mode makes the status
// at best `partial`. An environment whose projects have no index result is
// `unknown` ("index result unavailable"), as is an environment that the
// manifest cross-check did not read when the family has a loss mode that only
// the cross-check detects.
func ComputePredicateCoverage(input *PredicateInput) []PredicateCoverage {
	builder := newRecordBuilder(input)

	names := FamilyNames()
	slices.SortFunc(names, canonjson.CompareUTF16)

	out := make([]PredicateCoverage, 0, len(names)*len(builder.in.Projects))

	for _, name := range names {
		out = append(out, builder.recordsOf(name)...)
	}

	out = append(out, builder.renderedResourceSet()...)

	slices.SortStableFunc(out, func(a, b PredicateCoverage) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Predicate, b.Predicate),
			canonjson.CompareUTF16(a.Scope.Project, b.Scope.Project),
			canonjson.CompareUTF16(a.Scope.Environment, b.Scope.Environment),
		)
	})

	return out
}

// recordsOf gives the records of one family: one for each project, and for an
// environment predicate one for each environment.
func (b *recordBuilder) recordsOf(name string) []PredicateCoverage {
	out := make([]PredicateCoverage, 0, len(b.in.Projects)+len(b.in.Environments))

	for _, p := range b.in.Projects {
		out = append(out, b.record(name, CoverageScope{Project: p.Name}))
	}

	if !isEnvironmentPredicate(name) {
		return out
	}

	for _, e := range b.in.Environments {
		out = append(out, b.record(name, CoverageScope{Environment: e.ID}))
	}

	return out
}

// scopeProjects gives the projects that a scope reads.
func (b *recordBuilder) scopeProjects(scope CoverageScope) []string {
	if scope.Environment == "" {
		return []string{scope.Project}
	}

	if env := b.envOf[scope.Environment]; env != nil {
		return env.Projects
	}

	return nil
}

func (b *recordBuilder) gapsFor(scope CoverageScope) []CoverageGap {
	var out []CoverageGap

	for i := range b.in.Gaps {
		g := &b.in.Gaps[i]

		switch {
		case scope.Environment != "" && g.Environment == scope.Environment:
			out = append(out, *g)
		case scope.Project != "" && g.Project == scope.Project:
			// A project record shows the gap once, whatever environments the file belongs to.
			shown := *g
			shown.Environment = ""
			out = append(out, shown)
		}
	}

	return out
}

// baseStatus gives the status that the family support gives, with its reasons.
func baseStatus(state *FamilyState, declared bool) (status CoverageStatus, reasons []string) {
	if len(state.MissingProperties) > 0 {
		reasons = append(reasons, reasonPropertyMissing+strings.Join(state.MissingProperties, ", "))
	}

	if state.NotPresent {
		reasons = append(reasons, ReasonNoNativeEdges)
	}

	switch {
	case !declared:
		return CoverageUnknown, append(reasons, ReasonFamilyNotDeclared)
	case state.Level == SupportUnsupported:
		return CoverageUnsupported, append(reasons, ReasonFamilyUnsupported)
	case state.Level == SupportPartial:
		return CoveragePartial, reasons
	default:
		return CoverageComplete, reasons
	}
}

// groupGapsByLossMode groups the gaps that apply to a predicate by loss mode
// (each group sorted and without duplicates). It also gives the number of gaps.
func groupGapsByLossMode(gaps []CoverageGap, predicate string, state *FamilyState) (modes map[string][]CoverageGap, unresolved int) {
	modes = map[string][]CoverageGap{}

	for i := range gaps {
		if gapAppliesTo(&gaps[i], predicate, state) {
			modes[gaps[i].Reason] = append(modes[gaps[i].Reason], gaps[i])
		}
	}

	for id, list := range modes {
		modes[id] = SortGaps(list)
		unresolved += len(modes[id])
	}

	return modes, unresolved
}

// isEnvironmentOccurrence says whether a record is the one of the occurrence predicate in an environment.
func isEnvironmentOccurrence(predicate string, scope CoverageScope) bool {
	return predicate == FamilyContainerOccurrenceVersions && scope.Environment != ""
}

// isCrossChecked says whether the manifest cross-check ran over the sources of an environment.
func isCrossChecked(env *EnvironmentScope) bool {
	return env != nil && env.CrossChecked
}

// crossCheckClears says whether the cross-check proves the occurrence predicate complete for an environment:
// every container of every workload that it read has an occurrence, and the sources are readable and indexed.
func (b *recordBuilder) crossCheckClears(predicate string, scope CoverageScope, modes map[string][]CoverageGap) bool {
	if !isEnvironmentOccurrence(predicate, scope) || len(modes) > 0 {
		return false
	}

	return isCrossChecked(b.envOf[scope.Environment])
}

// lossModesOf gives the loss modes of a predicate in a scope: the gaps that apply, grouped by loss mode (each
// group sorted and without duplicates), and the loss modes that the profile states for a partial family as a
// whole (no gap). It also gives the number of gaps.
func (b *recordBuilder) lossModesOf(predicate string, scope CoverageScope, state *FamilyState, declared bool) (modes map[string][]CoverageGap, unresolved int) {
	modes, unresolved = groupGapsByLossMode(b.gapsFor(scope), predicate, state)

	if b.crossCheckClears(predicate, scope, modes) {
		return modes, 0
	}

	if declared && state.Level == SupportPartial {
		for _, m := range state.LossModes {
			if _, ok := modes[m]; !ok {
				modes[m] = nil
			}
		}
	}

	return modes, unresolved
}

// renderedFor gives the rendered record of the environment of a scope when it can cure a loss mode of the predicate.
func (b *recordBuilder) renderedFor(predicate string, scope CoverageScope) *RenderedRecord {
	if scope.Environment == "" || len(renderingCures(predicate)) == 0 {
		return nil
	}

	return b.renderedOf(scope.Environment)
}

func (b *recordBuilder) record(predicate string, scope CoverageScope) PredicateCoverage {
	rec := b.recordAt(predicate, scope, true)

	// Beside a rendered record, the record shows both levels: the provider alone, and the rendering. The predicate is
	// complete if either is.
	if rendered := b.renderedFor(predicate, scope); rendered != nil {
		provider := b.recordAt(predicate, scope, false)
		rec.Levels = []LevelRecord{{Level: LevelDeclared, Status: provider.Status}, {Level: LevelRenderedOrEvaluated, Status: rendered.Status}}
	}

	return rec
}

// lossModesCured says whether a partial family has no loss mode left in a scope: the cross-check cleared them in
// an environment, or the rendered occurrences cured the only ones.
func lossModesCured(predicate string, scope CoverageScope, modes map[string][]CoverageGap, resolved []LossMode) bool {
	if len(modes) > 0 {
		return false
	}

	return isEnvironmentOccurrence(predicate, scope) || len(resolved) > 0
}

// recordDraft is a predicate coverage record under construction: it lowers its
// status and collects its reasons.
type recordDraft struct {
	reasons map[string]struct{}
	PredicateCoverage
}

func newRecordDraft(predicate string, scope CoverageScope, status CoverageStatus, unresolved int) *recordDraft {
	return &recordDraft{
		PredicateCoverage: PredicateCoverage{
			Predicate: predicate, Scope: scope, Status: status, Unresolved: unresolved,
			LossModes: []LossMode{}, Reasons: []string{},
		},
		reasons: map[string]struct{}{},
	}
}

func (d *recordDraft) reason(r string) {
	d.reasons[r] = struct{}{}
}

// lowerWith lowers the status to s when s limits a claim more, and adds the reason.
func (d *recordDraft) lowerWith(s CoverageStatus, reason string) {
	if s.Rank() > d.Status.Rank() {
		d.Status = s
	}

	d.reason(reason)
}

// setLossModes lists the loss modes by identifier. A loss mode makes a complete status partial.
func (d *recordDraft) setLossModes(modes map[string][]CoverageGap, state *FamilyState) {
	for id, gaps := range modes {
		d.LossModes = append(d.LossModes, LossMode{ID: id, Gaps: gaps})
	}

	slices.SortFunc(d.LossModes, func(a, c LossMode) int { return canonjson.CompareUTF16(a.ID, c.ID) })

	if len(d.LossModes) == 0 {
		return
	}

	if d.Status == CoverageComplete {
		d.Status = CoveragePartial
	}

	if state.Level == SupportPartial {
		d.reason(reasonFamilyPartialLevel)
	}
}

// finish gives the record, with its reasons sorted.
func (d *recordDraft) finish(resolved []LossMode) PredicateCoverage {
	for r := range d.reasons {
		d.Reasons = append(d.Reasons, r)
	}

	slices.SortFunc(d.Reasons, canonjson.CompareUTF16)

	d.Resolved = resolved

	return d.PredicateCoverage
}

func (b *recordBuilder) recordAt(predicate string, scope CoverageScope, useRendered bool) PredicateCoverage {
	// A placement fact exists exactly when its workload manifest is read, so the record of `placed-in` in an
	// environment is the record of the workload set, when the profile declares both.
	if b.followsWorkloadSet(predicate, scope) {
		rec := b.record(FamilyDeploymentWorkloadSet, scope)
		rec.Predicate = predicate

		return rec
	}

	state, declared := b.families[predicate]
	status, baseReasons := baseStatus(&state, declared)
	modes, unresolved := b.lossModesOf(predicate, scope, &state, declared)

	var resolved []LossMode
	if useRendered && scope.Environment != "" {
		resolved, unresolved = b.resolveByRendering(predicate, scope.Environment, modes)
	}

	draft := newRecordDraft(predicate, scope, status, unresolved)

	for _, r := range baseReasons {
		draft.reason(r)
	}

	// A partial family whose loss modes the cross-check cleared in this scope, or whose only loss modes the
	// rendered occurrences cured, is complete.
	if status == CoveragePartial && lossModesCured(predicate, scope, modes, resolved) {
		draft.Status = CoverageComplete
	}

	draft.setLossModes(modes, &state)
	b.applyScopeState(draft, scope, &state)

	return draft.finish(resolved)
}

// applyScopeState lowers the status for a failed project, an unavailable index result, and an environment that the cross-check did not read.
func (b *recordBuilder) applyScopeState(draft *recordDraft, scope CoverageScope, state *FamilyState) {
	projects := b.scopeProjects(scope)
	b.lowerForFailures(draft, projects)

	if draft.Status == CoverageUnsupported || draft.Status == CoverageFailed {
		return
	}

	if scope.Environment != "" {
		b.applyEnvironmentState(draft, scope.Environment, projects, state)

		return
	}

	b.applyProjectState(draft, scope.Project)
}

// lowerForFailures lowers the status to `failed` for each project whose query failed.
func (b *recordBuilder) lowerForFailures(draft *recordDraft, projects []string) {
	for _, p := range projects {
		if text, failed := b.failed[p]; failed {
			draft.lowerWith(CoverageFailed, reasonScopeFailed+text)
		}
	}
}

// hasCrossCheckLossMode says whether a family has a loss mode that only the cross-check detects.
func hasCrossCheckLossMode(state *FamilyState) bool {
	return slices.ContainsFunc(crossCheckLossModes, func(m string) bool { return slices.Contains(state.LossModes, m) })
}

// applyEnvironmentState lowers the status of an environment record for an unavailable index result and a cross-check that did not run.
func (b *recordBuilder) applyEnvironmentState(draft *recordDraft, environment string, projects []string, state *FamilyState) {
	if slices.ContainsFunc(projects, func(p string) bool { return b.indexOf[p] == nil }) {
		draft.lowerWith(CoverageUnknown, ReasonIndexUnavailable)
	}

	env := b.envOf[environment]
	if draft.Predicate == FamilyDeploymentWorkloadSet {
		b.workloadSetState(env, draft)

		return
	}

	if isCrossChecked(env) || !hasCrossCheckLossMode(state) {
		return
	}

	draft.lowerWith(CoverageUnknown, ReasonCrossCheckNotRun)
}

// applyProjectState lowers the status of a project record for an unavailable index result and unindexed Dockerfiles.
func (b *recordBuilder) applyProjectState(draft *recordDraft, project string) {
	switch draft.Predicate {
	case FamilyIndexResult:
		if b.indexOf[project] == nil {
			draft.lowerWith(CoverageUnknown, ReasonIndexUnavailable)
		}
	case FamilyDockerfileSet:
		b.dockerfileSetState(project, draft)
	}
}

// followsWorkloadSet says whether the record of a predicate in a scope is the one of the workload set: the
// predicate is `placed-in` in an environment, and the profile declares `placed-in` and `deployment-workload-set`
// as supported families.
func (b *recordBuilder) followsWorkloadSet(predicate string, scope CoverageScope) bool {
	if predicate != string(KindPlacedIn) || scope.Environment == "" {
		return false
	}

	placed, declaredPlaced := b.families[string(KindPlacedIn)]
	set, declaredSet := b.families[FamilyDeploymentWorkloadSet]

	if !declaredPlaced || !declaredSet {
		return false
	}

	return placed.Level != SupportUnsupported && set.Level != SupportUnsupported
}

// documentExplained says whether a workload document has a provider node, or a gap that explains why it has none.
func documentExplained(doc *WorkloadDocument, explained []CoverageGap) bool {
	if doc.ProviderNode {
		return true
	}

	return slices.ContainsFunc(explained, func(g CoverageGap) bool { return explainsDocument(&g, doc.Path) })
}

// workloadSetState lowers the workload set of an environment to `unknown` for the reasons that say that
// the manifests were not all read: the cross-check did not run, a Compose or Terraform source is in the sources,
// and a workload document has no provider node. A document in a default-excluded directory or in a partly parsed
// file has its loss mode already, so it adds no reason.
func (b *recordBuilder) workloadSetState(env *EnvironmentScope, draft *recordDraft) {
	if !isCrossChecked(env) {
		draft.lowerWith(CoverageUnknown, ReasonCrossCheckNotRun)

		return
	}

	for _, source := range env.UnsupportedSources {
		draft.lowerWith(CoverageUnknown, ReasonUnsupportedDeploymentSource+": "+source)
	}

	explained := b.gapsFor(CoverageScope{Environment: env.ID})

	for i := range env.Workloads {
		doc := &env.Workloads[i]
		if !documentExplained(doc, explained) {
			draft.lowerWith(CoverageUnknown, doc.NoProviderNodeReason())
		}
	}
}

// explainsDocument says whether a gap is the reason that the provider has no node for the manifest at path.
func explainsDocument(gap *CoverageGap, file string) bool {
	switch gap.Reason {
	case LossDefaultExcludedDirectory:
		return strings.HasSuffix(gap.Path, "/") && strings.HasPrefix(file, gap.Path)
	case LossParsePartial:
		return gap.Path == file
	default:
		return false
	}
}

// dockerfileSetState lowers the Dockerfile set of a project to `unknown` for an unavailable index result and for a
// Dockerfile that the graph does not have.
func (b *recordBuilder) dockerfileSetState(project string, draft *recordDraft) {
	if b.indexOf[project] == nil {
		draft.lowerWith(CoverageUnknown, ReasonIndexUnavailable)
	}

	if scope := b.projectOf[project]; scope != nil {
		for _, file := range scope.UnindexedDockerfiles {
			draft.lowerWith(CoverageUnknown, "Dockerfile "+file+" is not indexed")
		}
	}
}
