package conformance

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// fetched is the observed evidence of a run.
type fetched struct {
	// assurance is the evidence of the rendered and evaluated level; nil without an `assurance` key.
	assurance *assuranceEvidence
	result    observation.EvidenceQueryResult
	// failures are the projects whose query failed (coverage `failed`).
	failures   []observation.ScopeFailure
	cbmFiles   []string // with prefix, sorted.
	bindingSet binding.Set
}

// projectEvidence is what the per-project queries of a run gave.
type projectEvidence struct {
	result       observation.EvidenceQueryResult
	firstFailure error
	cbmFiles     []string
	filesByProj  [][]string
	failures     []observation.ScopeFailure
}

// fetchEvidence reads the edges of the kinds that the plans need (plus DEFINES to get
// all nodes) of each project, verifies the freeze, and resolves the bindings of the
// observed nodes. Paths of other projects get the prefix @<name>/.
func (c *checker) fetchEvidence(ctx context.Context, wspace *workspace.Workspace, state *frozen, inp *inputs) (*fetched, error) {
	return c.fetchEvidenceOf(ctx, wspace, state, inp, requiredNativeKinds(wspace, inp.selected.Plans))
}

// fetchEvidenceOf is fetchEvidence for an explicit list of native edge kinds. The review facts
// (LoadReviewFacts) read it with the kinds that review dependencies need.
func (c *checker) fetchEvidenceOf(ctx context.Context, wspace *workspace.Workspace, state *frozen, inp *inputs, nativeKinds []string) (*fetched, error) {
	evidence, err := c.queryProjects(ctx, state, nativeKinds)
	if err != nil {
		return nil, err
	}

	result := &evidence.result

	c.phase("observation")
	c.normalizeEvidence(wspace, state, result)

	if err := c.addRuntimeTraces(wspace, state, inp, result); err != nil {
		return nil, err
	}

	// A binding conflict, a cardinality breach, or a required binding without a
	// match stops the run. Synthesized entities (images) and one entity for each
	// image digest that an edge references take part in resolution like nodes.
	assured := c.addAssuranceEvidence()

	observed := observedNodes(state, result.Nodes, evidence.filesByProj)
	observed = append(observed, result.Entities...)
	observed = append(observed, binding.ImageDigestNodes(result.Edges)...)

	// The planned resources take part in the resolution, so that an `infraResource` binding can name a plan address.
	// They are not facts of the graph: the ones that no binding reaches are not counted as unbound observations.
	if assured != nil {
		observed = append(observed, assured.planNodes...)
	}

	bindingSet := binding.Resolve(inp.bindings.Bindings, observed, binding.BuildFiles(evidence.cbmFiles), binding.WithClusterOf(clusterOf(wspace)))
	if assured != nil {
		bindingSet.UnboundObserved -= assured.unboundPlanNodes(&bindingSet)
	}

	problems := withoutFailedDependents(&inp.bindings, wspace, state.projectNames(), evidence.failures)
	if err := policy.ThrowOnErrors(binding.Problems(&problems, &bindingSet)); err != nil {
		return nil, err
	}

	// Non-exclusive bindings that map one entity to different CALM elements: a warning.
	c.diags = append(c.diags, binding.Warnings(&bindingSet)...)

	if c.assurance != nil {
		c.assurance.recordCandidates(result, &bindingSet)
	}

	return &fetched{bindingSet: bindingSet, result: *result, cbmFiles: evidence.cbmFiles, failures: evidence.failures, assurance: assured}, nil
}

// nodesWithHostingTargets gives the nodes that the evaluation reads: the provider nodes and the synthesized
// Namespace entities. A derived `placed-in` fact can end at such an entity, and a fact whose endpoint is not among
// the nodes is not read. Other entities stay out: an image entity is bound like its provider `Image` node and would
// give a second anchor for the same image. The evidence keeps nodes and entities apart.
func nodesWithHostingTargets(result *observation.EvidenceQueryResult) map[string]observation.ObservedNode {
	nodes := result.Nodes
	copied := false

	for i := range result.Entities {
		if result.Entities[i].Label != observation.LabelNamespace {
			continue
		}

		if !copied {
			nodes = make(map[string]observation.ObservedNode, len(result.Nodes)+len(result.Entities))
			maps.Copy(nodes, result.Nodes)

			copied = true
		}

		nodes[result.Entities[i].ObservedID] = result.Entities[i]
	}

	return nodes
}

// requiredNativeKinds gives the sorted native edge kinds that the plans need, plus DEFINES to get all nodes.
func requiredNativeKinds(wspace *workspace.Workspace, plans []policy.RulePlan) []string {
	nativeSet := map[string]struct{}{"DEFINES": {}, "DEFINES_METHOD": {}}

	for i := range plans {
		addNativeKinds(nativeSet, plans[i].Traversal.EdgeKinds)
	}

	addWorkspaceNativeKinds(nativeSet, wspace, plans)

	nativeKinds := make([]string, 0, len(nativeSet))
	for k := range nativeSet {
		nativeKinds = append(nativeKinds, k)
	}

	slices.SortFunc(nativeKinds, compareStrings)

	return nativeKinds
}

// needsCrossCheckEvidence says whether the manifest cross-check needs its evidence. It needs the occurrence and
// selector facts of the environments' manifests, and a `placed-in` relation (of a rule or of a deployment view)
// needs the workload nodes, which are provider nodes only as endpoints of these facts.
func needsCrossCheckEvidence(wspace *workspace.Workspace, plans []policy.RulePlan) bool {
	return len(wspace.Environments) > 0 || needsPlacement(plans) || viewsNeedPlacement(wspace)
}

// addWorkspaceNativeKinds adds the native edge kinds that the declarations of the workspace need.
func addWorkspaceNativeKinds(set map[string]struct{}, wspace *workspace.Workspace, plans []policy.RulePlan) {
	if needsCrossCheckEvidence(wspace, plans) {
		addNativeKinds(set, observation.CrossCheckEvidenceKinds())
	}

	// The checks of the declared views compare the code relationships and the diagram arrows.
	addNativeKinds(set, viewEvidenceKinds(wspace))

	// ADR governance reads the supersession edges, the mentions and the file references, and the ADR nodes with them.
	addNativeKinds(set, decisionEvidenceKinds(wspace, plans))

	// The review facts read the facts of the dependency kinds that assessments bind. A workspace with no `assessments`
	// key requests nothing new.
	if wspace.Assessments != nil {
		addNativeKinds(set, ReviewEvidenceKinds())
	}

	// The deployment trace shows the build definitions and the deployment actions of the declared environments.
	if len(wspace.Environments) > 0 {
		addNativeKinds(set, deployment.EvidenceKinds())
	}

	// The candidates for a check are the TESTS and TESTS_FILE edges of the code graph (assurance_candidates.go).
	if wspace.Assurance != nil && wspace.Assurance.Verification != nil {
		set["TESTS"] = struct{}{}
		set["TESTS_FILE"] = struct{}{}
	}
}

// addNativeKinds adds the native edge kinds of the canonical kinds to the set.
func addNativeKinds(set map[string]struct{}, kinds []observation.CanonicalEdgeKind) {
	for _, k := range kinds {
		for _, native := range policy.NativeKindsFor(k) {
			set[native] = struct{}{}
		}
	}
}

// needsPlacement says whether a plan has a `placed-in` relation.
func needsPlacement(plans []policy.RulePlan) bool {
	return slices.ContainsFunc(plans, func(plan policy.RulePlan) bool {
		return slices.Contains(plan.Traversal.EdgeKinds, observation.KindPlacedIn) ||
			(plan.Obligation != nil && plan.Obligation.Relation.Kind == observation.KindPlacedIn)
	})
}

// queryProjects fetches the evidence of each project and merges it. A query-level failure in one project marks its
// coverage `failed` and the run goes on; connection-level and process errors stop the run (exit 3 or 5).
func (c *checker) queryProjects(ctx context.Context, state *frozen, nativeKinds []string) (*projectEvidence, error) {
	evidence := &projectEvidence{filesByProj: make([][]string, len(state.projects))}
	digests := make([]string, 0, len(state.projects))

	for i := range state.projects {
		proj := &state.projects[i]

		part, files, err := fetchProject(ctx, state.conn.Provider, proj, nativeKinds)
		if err != nil {
			failure, scoped := scopeFailureOf(proj.project.Name, err)
			if !scoped {
				return nil, err
			}

			if evidence.firstFailure == nil {
				evidence.firstFailure = err
			}

			evidence.failures = append(evidence.failures, failure)
			c.diags = append(c.diags, diag.New(diag.OBS018, diag.Params{"project": failure.Project, "detail": failure.Reason}))

			continue
		}

		// The warnings of the fetch (unsupported and ambiguous evidence) belong to the run, not to the evidence.
		c.diags = append(c.diags, part.Diagnostics...)
		part.Diagnostics = nil

		prefixEvidence(&part, proj.prefix)

		evidence.filesByProj[i] = proj.prefixed(files)
		evidence.cbmFiles = append(evidence.cbmFiles, evidence.filesByProj[i]...)
		digests = append(digests, part.QueryPlanDigest)

		mergeEvidence(&evidence.result, &part)
	}

	if len(evidence.failures) == len(state.projects) {
		// No project answered: there is nothing to evaluate, so the failure of the provider stops the run.
		return nil, evidence.firstFailure
	}

	slices.SortFunc(evidence.cbmFiles, canonjson.CompareUTF16)

	if state.multi() {
		evidence.result.QueryPlanDigest = digest.SHA256HexString(strings.Join(digests, "\n"))
	}

	return evidence, nil
}

// prefixEvidence gives the paths of the evidence of a project other than the primary one the prefix @<name>/.
func prefixEvidence(part *observation.EvidenceQueryResult, prefix string) {
	if prefix == "" {
		return
	}

	for id := range part.Nodes {
		n := part.Nodes[id]
		n.FilePath = prefixPath(prefix, n.FilePath)
		part.Nodes[id] = n
	}

	for _, e := range slices.Concat(part.Edges, part.Unsupported) {
		if e.SourceSpan != nil {
			span := *e.SourceSpan
			span.Path = prefixPath(prefix, span.Path)
			e.SourceSpan = &span
		}
	}

	for i := range part.Entities {
		part.Entities[i].FilePath = prefixPath(prefix, part.Entities[i].FilePath)
	}
}

// normalizeEvidence resolves cross-project edges and annotates the merged evidence with the environments and the profile.
func (c *checker) normalizeEvidence(wspace *workspace.Workspace, state *frozen, result *observation.EvidenceQueryResult) {
	if state.multi() {
		c.resolveCrossProjectEdges(state, result)
	}

	// Infrastructure facts get the declared environment of their scoped
	// endpoints: one fact for each matching environment, "unresolved" for none.
	observation.AttributeEnvironments(result, wspace.Attribute)
	markPossiblyLossyEdges(state, result)
	observation.AnnotateScopedIDs(result.Nodes, clusterOf(wspace))

	if derivesPlacement(state) {
		// Placement is derived from the attributed workload nodes, so it comes last.
		observation.DerivePlacement(result, clusterOf(wspace))
	}
}

// resolveCrossProjectEdges resolves the edges between projects and records the dropped ones.
func (c *checker) resolveCrossProjectEdges(state *frozen, result *observation.EvidenceQueryResult) {
	if dropped := observation.ResolveCrossProject(result, state.projectNames()); dropped > 0 {
		c.diags = append(c.diags, diag.New(diag.OBS015, diag.Params{"count": dropped}))
	}

	observation.SortEdges(result.Edges, result.Nodes)
}

// markPossiblyLossyEdges marks the edges that the profile of the build says may be lossy.
func markPossiblyLossyEdges(state *frozen, result *observation.EvidenceQueryResult) {
	if state.caps.Profile == nil {
		return
	}

	if profile, ok := observation.ProfileFor(state.caps.ExecutableVersion, state.caps.AdapterSchema); ok {
		observation.MarkPossiblyLossy(result.Edges, profile)
	}
}

// derivesPlacement says whether the build declares the `placed-in` family as supported. A build whose profile does
// not declare the family (no profile) derives none: the rules that need it are `unknown`, `unsupported-family`.
func derivesPlacement(state *frozen) bool {
	placement, ok := state.caps.Families[string(observation.KindPlacedIn)]

	return ok && placement.Level != observation.SupportUnsupported
}

// clusterOf gives the cluster of a declared environment, "" for an unknown one.
func clusterOf(wspace *workspace.Workspace) func(env string) string {
	return func(env string) string {
		for _, e := range wspace.Environments {
			if e.ID == env {
				return e.Cluster
			}
		}

		return ""
	}
}

// mergeEvidence adds the evidence of one project to dst.
func mergeEvidence(dst, part *observation.EvidenceQueryResult) {
	if dst.Nodes == nil {
		// One project: keep the result as it is.
		*dst = *part

		return
	}

	maps.Copy(dst.Nodes, part.Nodes)

	for k, n := range part.UnmappedNativeEdges {
		if dst.UnmappedNativeEdges == nil {
			dst.UnmappedNativeEdges = map[string]int{}
		}

		dst.UnmappedNativeEdges[k] += n
	}

	dst.Edges = append(dst.Edges, part.Edges...)
	dst.Unsupported = append(dst.Unsupported, part.Unsupported...)
	dst.Entities = append(dst.Entities, part.Entities...)
}

// observedNodes adds the File nodes of the indexed files of each project to nodes.
func observedNodes(state *frozen, nodes map[string]observation.ObservedNode, filesByProj [][]string) []observation.ObservedNode {
	if len(state.projects) == 1 {
		return binding.WithFileNodes(state.projects[0].ref.Project, nodes, filesByProj[0])
	}

	merged := map[string]observation.ObservedNode{}

	for i := range state.projects {
		fileNodes := binding.WithFileNodes(state.projects[i].ref.Project, nodes, filesByProj[i])
		for j := range fileNodes {
			merged[fileNodes[j].ObservedID] = fileNodes[j]
		}
	}

	out := make([]observation.ObservedNode, 0, len(merged))
	for id := range merged {
		out = append(out, merged[id])
	}

	slices.SortFunc(out, func(a, b observation.ObservedNode) int { return canonjson.CompareUTF16(a.ObservedID, b.ObservedID) })

	return out
}

// addRuntimeTraces merges the runtime-trace evidence of observation.runtime into result.
func (c *checker) addRuntimeTraces(wspace *workspace.Workspace, state *frozen, inp *inputs, result *observation.EvidenceQueryResult) error {
	runtimeCfg := wspace.Observation.Runtime
	if runtimeCfg == nil {
		return nil
	}

	files, err := binding.ExpandFileGlobs(wspace.Root, runtimeCfg.Traces)
	if err != nil {
		return fmt.Errorf("expand observation.runtime.traces: %w", err)
	}

	if len(files) == 0 {
		return diag.Fail(diag.OBS012, diag.Detail("observation.runtime.traces matches no file"))
	}

	inp.traceFiles = files

	evidence, err := observation.LoadRuntimeTraces(&observation.RuntimeInput{
		Root: wspace.Root, Files: files, Environment: runtimeCfg.Environment,
		MaxAge: time.Duration(runtimeCfg.MaxAgeDays) * 24 * time.Hour, Now: c.now(), GraphGeneration: state.generation,
	})
	if err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	if result.Nodes == nil {
		result.Nodes = map[string]observation.ObservedNode{}
	}

	for i := range evidence.Nodes {
		result.Nodes[evidence.Nodes[i].ObservedID] = evidence.Nodes[i]
	}

	result.Edges = append(result.Edges, evidence.Edges...)
	observation.SortEdges(result.Edges, result.Nodes)

	if evidence.Skipped != "" {
		c.diags = append(c.diags, diag.New(diag.OBS013, diag.Detail(evidence.Skipped)))
	}

	return nil
}
