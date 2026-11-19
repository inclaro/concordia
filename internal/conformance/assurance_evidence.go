package conformance

import (
	"path"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
)

// assuranceEvidence is what the assurance evidence adds to the evidence of a run, at the level `rendered-or-evaluated`:
// the planned resources as observed entities (so that an `infraResource` binding can resolve to a plan address) and the
// rendered-level coverage record of each environment that has a rendering source. The facts of the code graph are
// not touched: the two levels stay apart, and a rule that needs one level is never satisfied by the other.
type assuranceEvidence struct {
	// planNodes are the planned resources, one entity for each address and environment. They take part in binding
	// resolution only: they are not nodes of the graph and no rule reads them as facts.
	planNodes []observation.ObservedNode
	// rendered are the rendered-level coverage records, one for each environment with a rendering source, sorted.
	rendered []observation.RenderedRecord
}

// planNodeLabel is the label of a planned resource entity. The binding resolution reads no label other than `File`,
// `Image` and `Resource`, so the entity matches only an `infraResource` binding by its address.
const planNodeLabel = "PlannedResource"

// addAssuranceEvidence collects the evidence of the rendered and evaluated level of the run, beside addRuntimeTraces.
// Without an `assurance` key it adds nothing.
func (c *checker) addAssuranceEvidence() *assuranceEvidence {
	a := c.assurance
	if a == nil {
		return nil
	}

	return &assuranceEvidence{planNodes: planNodes(a.ev.Plans), rendered: renderedRecords(a.ev.Renders)}
}

// planNodes gives one entity for each resource of each plan that was read. A plan that is missing, unreadable, or not
// supported has no resource (it has a gap in the report): no partial plan is used. Entities of one environment are
// sorted by address.
func planNodes(plans []evidence.Plan) []observation.ObservedNode {
	var nodes []observation.ObservedNode

	for i := range plans {
		declared := &plans[i]
		if declared.Plan == nil {
			continue
		}

		env := declared.Declared.Environment

		for j := range declared.Plan.Resources {
			res := &declared.Plan.Resources[j]
			if res.Mode != planManaged {
				continue
			}

			nodes = append(nodes, observation.ObservedNode{
				ObservedID: planObservedID(env, res.Address), Label: planNodeLabel, QualifiedName: res.Address,
				FilePath: declared.Declared.File, Environment: env, Kind: res.Type,
				Properties: map[string]string{"address": res.Address, "type": res.Type},
			})
		}
	}

	slices.SortFunc(nodes, func(a, b observation.ObservedNode) int { return canonjson.CompareUTF16(a.ObservedID, b.ObservedID) })

	return slices.CompactFunc(nodes, func(a, b observation.ObservedNode) bool { return a.ObservedID == b.ObservedID })
}

// planManaged is the mode of a managed resource of a plan. A data source (`data`) reads and is never a binding target.
const planManaged = "managed"

func planObservedID(environment, address string) string { return "plan:" + environment + ":" + address }

// renderedRecords combines the renders of each environment into one record: complete only when every source rendered
// completely, failed when one failed, else partial.
func renderedRecords(renders []*render.Result) []observation.RenderedRecord {
	byEnv := map[string]*observation.RenderedRecord{}

	for _, res := range renders {
		rec := byEnv[res.Environment]
		if rec == nil {
			rec = &observation.RenderedRecord{Environment: res.Environment, Status: observation.CoverageComplete}
			byEnv[res.Environment] = rec
		}

		rec.Status = worseCoverage(rec.Status, coverageStatusOf(res.Coverage))
		rec.Gaps = append(rec.Gaps, renderGaps(res)...)
	}

	out := make([]observation.RenderedRecord, 0, len(byEnv))

	for _, rec := range byEnv {
		rec.Gaps = observation.SortGaps(rec.Gaps)
		out = append(out, *rec)
	}

	slices.SortFunc(out, func(a, b observation.RenderedRecord) int { return canonjson.CompareUTF16(a.Environment, b.Environment) })

	return out
}

func coverageStatusOf(c assurance.Coverage) observation.CoverageStatus {
	switch c {
	case assurance.CoverageComplete:
		return observation.CoverageComplete
	case assurance.CoveragePartial:
		return observation.CoveragePartial
	case assurance.CoverageFailed:
		return observation.CoverageFailed
	}

	return observation.CoverageFailed
}

// worseCoverage gives the status that limits a claim more.
func worseCoverage(a, b observation.CoverageStatus) observation.CoverageStatus {
	if b.Rank() > a.Rank() {
		return b
	}

	return a
}

// renderGaps gives the gaps of a render that did not complete as gaps with the loss mode `unrendered-template`: the
// rendered record reuses the loss mode of the `rendered-templates` family. A failed render with no recorded gap still
// has one, so that the failure is never an empty record.
func renderGaps(res *render.Result) []observation.CoverageGap {
	if res.Coverage == assurance.CoverageComplete {
		return nil
	}

	gaps := make([]observation.CoverageGap, 0, len(res.Gaps)+1)

	for i := range res.Gaps {
		file := res.Gaps[i].Path
		if file == "" {
			file = res.Path
		}

		gaps = append(gaps, observation.CoverageGap{
			Reason: observation.LossUnrenderedTemplate, Path: file, Environment: res.Environment,
			Detail: res.Gaps[i].Kind + ": " + res.Gaps[i].Detail,
		})
	}

	if len(gaps) == 0 {
		gaps = append(gaps, observation.CoverageGap{
			Reason: observation.LossUnrenderedTemplate, Path: res.Path, Environment: res.Environment, Detail: "the source did not render",
		})
	}

	return gaps
}

// unboundPlanNodes counts the planned resources that no binding reached and that are not in conflict: the ones that
// Resolve counted as unbound observations, which they are not.
func (e *assuranceEvidence) unboundPlanNodes(set *binding.Set) int {
	conflicted := map[string]struct{}{}
	for i := range set.Conflicts {
		conflicted[set.Conflicts[i].ObservedID] = struct{}{}
	}

	count := 0

	for i := range e.planNodes {
		id := e.planNodes[i].ObservedID
		if _, ok := conflicted[id]; ok {
			continue
		}

		if len(set.ByObserved[id]) == 0 {
			count++
		}
	}

	return count
}

// evaluatedEnvironments gives the evidence of the level `rendered-or-evaluated` of each environment for the deployment
// trace: the rendered sources and plans with their coverage, and the rendered workloads and planned resources that a
// binding maps to a CALM element. A workload comes from the `workload` and `image` bindings, a planned resource from
// an `infraResource` binding that names its address. Neither is ever shown as deployed.
func (a *assuranceRun) evaluatedEnvironments(bindingSet *binding.Set) []deployment.EvaluatedEnvironment {
	byEnv := map[string]*deployment.EvaluatedEnvironment{}

	envOf := func(id string) *deployment.EvaluatedEnvironment {
		if byEnv[id] == nil {
			byEnv[id] = &deployment.EvaluatedEnvironment{Environment: id, Entities: map[string][]deployment.Entity{}}
		}

		return byEnv[id]
	}

	rendersOf := map[string][]*render.Result{}

	for _, res := range a.ev.Renders {
		env := envOf(res.Environment)
		env.Sources = append(env.Sources, deployment.EvaluatedSource{
			Kind: deployment.SourceRender, Tool: res.Tool, Path: res.Path, Coverage: string(res.Coverage), Digest: res.OutputDigest,
		})
		rendersOf[res.Environment] = append(rendersOf[res.Environment], res)
	}

	for i := range a.ev.Plans {
		declared := &a.ev.Plans[i]
		source := deployment.EvaluatedSource{
			Kind: deployment.SourcePlan, Tool: declared.Declared.Tool, Path: declared.Declared.File, Coverage: string(declared.Coverage),
		}

		if declared.Plan != nil {
			source.Digest = declared.Plan.Digest
		}

		env := envOf(declared.Declared.Environment)
		env.Sources = append(env.Sources, source)
	}

	for id, renders := range rendersOf {
		a.addRenderedWorkloads(envOf(id), newEnvModel(id, renders, a.cfg))
	}

	a.addPlannedResources(bindingSet, envOf)

	for _, env := range byEnv {
		for calmID, list := range env.Entities {
			slices.SortFunc(list, func(x, y deployment.Entity) int { return canonjson.CompareUTF16(x.Identity, y.Identity) })
			env.Entities[calmID] = list
		}
	}

	out := make([]deployment.EvaluatedEnvironment, 0, len(byEnv))
	for _, env := range byEnv {
		out = append(out, *env)
	}

	slices.SortFunc(out, func(x, y deployment.EvaluatedEnvironment) int {
		return canonjson.CompareUTF16(x.Environment, y.Environment)
	})

	return out
}

// addRenderedWorkloads maps the rendered workloads of an environment to the CALM elements whose bindings reach them.
func (a *assuranceRun) addRenderedWorkloads(env *deployment.EvaluatedEnvironment, model *envModel) {
	calmIDs := map[string]struct{}{}

	for i := range a.bindings {
		b := &a.bindings[i]
		if b.Status == binding.StatusProposed {
			continue
		}

		for j := range b.Match {
			if k := b.Match[j].Kind; k == binding.MatchWorkload || k == binding.MatchImage {
				calmIDs[b.ArchitectureRef] = struct{}{}
			}
		}
	}

	for calmID := range calmIDs {
		for _, res := range model.resourcesOf(a.bindings, calmID) {
			if res.Workload == nil {
				continue
			}

			env.Entities[calmID] = append(env.Entities[calmID], deployment.Entity{
				Identity: "k8s:" + res.ID.String(), Kind: deployment.KindRenderedWorkload, Resolution: observation.ResolutionExact,
				Locations: []deployment.Location{{Path: res.Location}},
				Properties: map[string]string{
					"apiGroup": res.ID.Group, propKind: res.ID.Kind, propName: res.ID.Name, propNamespace: res.ID.Namespace, "digest": res.Digest,
				},
			})
		}
	}
}

// addPlannedResources maps the planned resources that an `infraResource` binding reached to their CALM elements.
func (a *assuranceRun) addPlannedResources(bindingSet *binding.Set, envOf func(string) *deployment.EvaluatedEnvironment) {
	nodes := planNodes(a.ev.Plans)

	for i := range nodes {
		node := &nodes[i]
		for _, calmID := range bindingSet.ByObserved[node.ObservedID] {
			env := envOf(node.Environment)

			env.Entities[calmID] = append(env.Entities[calmID], deployment.Entity{
				Identity: node.QualifiedName, Kind: deployment.KindPlannedResource, Resolution: observation.ResolutionExact,
				Locations:  []deployment.Location{{Path: node.FilePath}},
				Properties: map[string]string{propKind: node.Kind, propName: node.QualifiedName},
				Bindings:   bindingSet.UsedBy[node.ObservedID],
			})
		}
	}
}

// locate gives the repository location of the first rendered source of an environment (the sources are sorted). A
// path that is absolute or leaves the repository is no location: a rendered finding never points at a temporary path.
func (a *assuranceRun) locate(environment string) *Location {
	for _, res := range a.ev.Renders {
		if res.Environment != environment || !repositoryPath(res.Path) {
			continue
		}

		return &Location{Path: strings.TrimPrefix(res.Path, "./")}
	}

	return nil
}

// repositoryPath says whether a path is relative and stays inside the repository.
func repositoryPath(p string) bool {
	if p == "" || path.IsAbs(p) || strings.Contains(p, ":") {
		return false
	}

	return !strings.HasPrefix(path.Clean(p), "..")
}

// propKind is the entity property that holds the kind of a resource.
const propKind = "kind"

// propName is the entity property that holds the name of a resource.
const propName = "name"

// propNamespace is the entity property that holds the namespace of a resource.
const propNamespace = "namespace"
