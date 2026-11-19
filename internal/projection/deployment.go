package projection

import (
	"iter"
	"maps"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
)

// deploymentEnvironment is the name of the one deployment environment of a workspace that declares none.
const deploymentEnvironment = "Default"

// excludeEvidence excludes the evidence relationships of the dynamic view.
const excludeEvidence = `"relationship.tag==concordia:evidence"`

// DynamicPath is an evidence path that the dynamic view shows.
type DynamicPath struct {
	Key   string // "evidence-" + first 12 hex of the fingerprint
	Title string // "<ruleId>: <message>"
	Steps []DynamicStep
}

// DynamicStep is one step of a DynamicPath. FromID and ToID are CALM IDs.
type DynamicStep struct{ FromID, ToID, Label string }

// deploymentContainers gives the IDs of the nodes that are the Container of a
// deployed-in relationship.
func deploymentContainers(model *intent.ArchitectureModel) map[string]bool {
	out := map[string]bool{}

	for i := range model.Relationships {
		if r := &model.Relationships[i]; r.Kind == intent.RelDeployedIn && r.Container != "" {
			out[r.Container] = true
		}
	}

	return out
}

// deploymentWarn appends CO-PRJ-008 with detail when the deployment view was asked for and diagnostics are collected.
func deploymentWarn(opts *DSLOptions, detail string) {
	if opts.Diagnostics == nil || !opts.Views[ViewDeployment] {
		return
	}

	*opts.Diagnostics = append(*opts.Diagnostics, diag.New(diag.PRJ008, diag.Detail(detail)))
}

// writeDeployment writes one deploymentEnvironment block for each declared environment in ID order, or the one
// `Default` block when options.Environments is empty (the as-built declared placement, without status tags). It gives
// the names of the environments that it wrote, and appends CO-PRJ-008 for each environment that it skipped when the
// deployment view was asked for.
func (m *dslModel) writeDeployment(out *dslWriter, model *intent.ArchitectureModel, opts *DSLOptions) []string {
	deploying := deploymentContainers(model)

	if len(opts.Environments) == 0 {
		return m.writeDefaultDeployment(out, model, deploying, opts)
	}

	return m.writeDeclaredDeployments(out, model, deploying, opts)
}

// writeDefaultDeployment writes the `Default` environment, or appends CO-PRJ-008 when there is nothing to draw.
func (m *dslModel) writeDefaultDeployment(
	out *dslWriter, model *intent.ArchitectureModel, deploying map[string]bool, opts *DSLOptions,
) []string {
	if len(deploying) > 0 && m.writeEnvironment(out, model, deploying, nil, deploymentEnvironment, opts.Diagnostics) {
		return []string{deploymentEnvironment}
	}

	deploymentWarn(opts, "no deployed-in relationship in the architecture")

	return nil
}

// writeDeclaredDeployments writes the declared environments in ID order and gives the names of those it wrote.
func (m *dslModel) writeDeclaredDeployments(
	out *dslWriter, model *intent.ArchitectureModel, deploying map[string]bool, opts *DSLOptions,
) []string {
	var written []string

	diagnostics := opts.Diagnostics
	combined := deployment.Combine(model, opts.Placements, opts.Environments, opts.Coverage)

	for i := range combined {
		env := &combined[i]
		if env.Shown() && m.writeEnvironment(out, model, deploying, env, env.Environment, diagnostics) {
			written = append(written, env.Environment)
			diagnostics = nil // CO-PRJ-006 is a property of the model: once.

			continue
		}

		// A gap alone does not skip the environment (it is in the projection), but Structurizr drops an
		// environment without elements, so the DSL has nothing to draw.
		if len(env.Gaps) == 0 {
			deploymentWarn(opts, "no declared placement and no placement record for environment '"+env.Environment+"'")
		}
	}

	return written
}

// writeEnvironment writes the block of one environment. env is nil for the `Default` environment. It reports
// whether it wrote the block: an environment with no deployment node has nothing to draw, and Structurizr rejects
// a deployment view of an environment without elements.
func (m *dslModel) writeEnvironment(
	out *dslWriter, model *intent.ArchitectureModel, declared map[string]bool, env *deployment.EnvironmentPlacement, name string,
	diagnostics *[]diag.Diagnostic,
) bool {
	walk := &deploymentWalk{model: m, out: out, env: env, diagnostics: diagnostics, visited: map[string]bool{}}
	walk.load(model, declared)

	if env != nil {
		walk.addObserved()
	}

	roots := walk.roots()
	unresolved := walk.unresolved()

	if len(roots) == 0 && len(unresolved) == 0 {
		return false
	}

	walk.writeBlock(name, roots, unresolved)

	if env != nil {
		m.statusTagged = true
	}

	return true
}

// deploymentWalk is the state of the walk over the deployment nodes of one environment.
type deploymentWalk struct {
	model       *dslModel
	out         *dslWriter
	env         *deployment.EnvironmentPlacement // nil for the Default environment
	diagnostics *[]diag.Diagnostic
	nodes       map[string]*intent.NodeDeclaration
	deploying   map[string]bool
	targets     map[string][]string
	visited     map[string]bool
}

// load indexes the nodes of the model and fills the deployment nodes and targets from the declared relationships.
func (w *deploymentWalk) load(model *intent.ArchitectureModel, declared map[string]bool) {
	w.nodes = make(map[string]*intent.NodeDeclaration, len(model.Nodes))
	w.deploying = maps.Clone(declared)
	w.targets = map[string][]string{}

	for i := range model.Nodes {
		w.nodes[model.Nodes[i].ID] = &model.Nodes[i]
	}

	w.collectTargets(model)
}

// writeBlock writes the deploymentEnvironment block: the root deployment nodes that no other node visited, then the
// synthetic targets.
func (w *deploymentWalk) writeBlock(name string, roots []string, unresolved []unresolvedTarget) {
	w.out.line(2, "deploymentEnvironment "+DSLString(name)+" {")

	for _, id := range roots {
		if !w.visited[id] {
			w.node(id, bodyDepth)
		}
	}

	for _, target := range unresolved {
		w.unresolvedTarget(target, bodyDepth)
	}

	w.out.line(2, "}")
}

// observedInstances yields the instances of every element of the environment, in element order.
func (w *deploymentWalk) observedInstances() iter.Seq[*deployment.PlacedInstance] {
	return func(yield func(*deployment.PlacedInstance) bool) {
		for i := range w.env.Elements {
			instances := w.env.Elements[i].Instances

			for j := range instances {
				if !yield(&instances[j]) {
					return
				}
			}
		}
	}
}

// isDeploymentNode reports whether target is a node that deploys things.
func (w *deploymentWalk) isDeploymentNode(target string) bool {
	return w.deploying[target] && w.nodes[target] != nil
}

// observedPlacement reports whether an observed instance names a known container that deploys a known node.
func (w *deploymentWalk) observedPlacement(instance *deployment.PlacedInstance) bool {
	if instance.Container == "" {
		return false
	}

	return w.nodes[instance.Container] != nil && w.nodes[instance.Node] != nil
}

// addObserved adds the observed instances to the targets of their containers and makes each observed container a
// deployment node.
func (w *deploymentWalk) addObserved() {
	for instance := range w.observedInstances() {
		if !w.observedPlacement(instance) {
			continue
		}

		container, placed := instance.Container, instance.Node
		if !slices.Contains(w.targets[container], placed) {
			w.targets[container] = append(w.targets[container], placed)
		}

		w.deploying[container] = true
	}
}

// unresolvedHostingTarget names the deployment node of an instance whose hosting target is not known.
const unresolvedHostingTarget = "unresolved hosting target"

// unresolvedTarget is a synthetic deployment node that stands for a hosting target that no CALM node names: it
// holds the instances that only a gap places (a bound workload whose namespace has no `infraResource` binding).
type unresolvedTarget struct {
	name  string
	nodes []string
}

// unresolvedName gives the name of the synthetic target of an instance: the identity of the gap that names its
// hosting target, else unresolvedHostingTarget.
func unresolvedName(instance *deployment.PlacedInstance) string {
	for _, gap := range instance.Gaps {
		if gap.Side == deployment.SideHostingTarget && gap.Identity != "" {
			return gap.Identity
		}
	}

	return unresolvedHostingTarget
}

// unresolvedNodes groups the node IDs of the instances that have no container by the name of their synthetic target.
func (w *deploymentWalk) unresolvedNodes() map[string][]string {
	byName := map[string][]string{}

	for instance := range w.observedInstances() {
		if instance.Container != "" || w.model.byID[instance.Node] == nil {
			continue
		}

		name := unresolvedName(instance)
		if !slices.Contains(byName[name], instance.Node) {
			byName[name] = append(byName[name], instance.Node)
		}
	}

	return byName
}

// unresolved gives the synthetic targets of the environment, sorted by name, with their sorted node IDs. An
// instance has no container when no `deployed-in` and no record places it; a gap names the node.
func (w *deploymentWalk) unresolved() []unresolvedTarget {
	if w.env == nil {
		return nil
	}

	byName := w.unresolvedNodes()
	out := make([]unresolvedTarget, 0, len(byName))

	for name, nodes := range byName {
		slices.SortFunc(nodes, canonjson.CompareUTF16)

		out = append(out, unresolvedTarget{name: name, nodes: nodes})
	}

	slices.SortFunc(out, func(a, b unresolvedTarget) int { return canonjson.CompareUTF16(a.name, b.name) })

	return out
}

// deploymentInstanceKeyword gives the DSL keyword of an instance of the element, empty when the element cannot be
// deployed (it has no C4 level of a container or a software system).
func deploymentInstanceKeyword(element *C4Element) string {
	if element == nil {
		return ""
	}

	switch element.Level {
	case intent.C4Container:
		return "containerInstance"
	case intent.C4SoftwareSystem:
		return "softwareSystemInstance"
	case intent.C4Person, intent.C4Component:
		return ""
	}

	return ""
}

// unresolvedTarget writes the synthetic deployment node and the `unknown` instances of its nodes.
func (w *deploymentWalk) unresolvedTarget(target unresolvedTarget, depth int) {
	w.out.line(depth, "deploymentNode "+DSLString(target.name)+" "+DSLString("")+" "+DSLString("")+" {")
	w.out.line(depth+1, "tags "+DSLString("concordia:deployment,"+statusTag(deployment.PlacementUnknown)))

	for _, nodeID := range target.nodes {
		keyword := deploymentInstanceKeyword(w.model.byID[nodeID])
		if keyword == "" {
			continue
		}

		w.out.line(depth+1, keyword+" "+w.model.ident[nodeID]+" {")
		w.out.line(depth+2, "tags "+DSLString(statusTag(deployment.PlacementUnknown)))
		w.out.line(depth+1, "}")
	}

	w.out.line(depth, "}")
}

// deployedTargets gives the set of the nodes that some node deploys.
func (w *deploymentWalk) deployedTargets() map[string]bool {
	isTarget := map[string]bool{}

	for _, targets := range w.targets {
		for _, target := range targets {
			isTarget[target] = true
		}
	}

	return isTarget
}

// roots gives the sorted IDs of the deployment nodes that no node deploys.
func (w *deploymentWalk) roots() []string {
	isTarget := w.deployedTargets()
	roots := make([]string, 0, len(w.deploying))

	for nodeID := range w.deploying {
		if w.nodes[nodeID] != nil && !isTarget[nodeID] {
			roots = append(roots, nodeID)
		}
	}

	slices.SortFunc(roots, canonjson.CompareUTF16)

	return roots
}

// collectTargets fills the targets of each container from the declared relationships, in relationship ID order.
func (w *deploymentWalk) collectTargets(model *intent.ArchitectureModel) {
	rels := slices.Clone(model.Relationships)
	slices.SortStableFunc(rels, func(a, b intent.RelationshipDeclaration) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	for i := range rels {
		rel := &rels[i]
		if rel.Kind == intent.RelDeployedIn && rel.Container != "" {
			w.targets[rel.Container] = append(w.targets[rel.Container], rel.Targets...)
		}
	}
}

// node writes the deployment node id and what it deploys.
func (w *deploymentWalk) node(nodeID string, depth int) {
	w.visited[nodeID] = true
	node := w.nodes[nodeID]

	w.out.line(depth, "deploymentNode "+DSLString(node.Name)+" "+DSLString(node.Description)+" "+DSLString(node.NodeType)+" {")

	tags := "concordia:deployment"
	if w.env != nil {
		tags += "," + statusTag(w.subtreeStatus(nodeID))
	}

	w.out.line(depth+1, "tags "+DSLString(tags))
	w.out.props(depth+1, property{propUniqueID, nodeID})

	for _, target := range w.targets[nodeID] {
		w.target(nodeID, target, depth+1)
	}

	w.out.line(depth, "}")
}

// target writes one thing that a deployment node deploys.
func (w *deploymentWalk) target(container, target string, depth int) {
	if w.isDeploymentNode(target) {
		if !w.visited[target] {
			w.node(target, depth)
		}

		return
	}

	element := w.model.byID[target]
	if keyword := deploymentInstanceKeyword(element); keyword != "" {
		w.instances(keyword, container, target, depth)

		return
	}

	if w.nodes[target] != nil {
		w.warnUndeployable(target, element)
	}
}

// warnUndeployable appends CO-PRJ-006 for a node that has no C4 level that a deployment node can deploy.
func (w *deploymentWalk) warnUndeployable(target string, element *C4Element) {
	if w.diagnostics == nil {
		return
	}

	level := "node with no C4 level"
	if element != nil {
		level = string(element.Level)
	}

	*w.diagnostics = append(*w.diagnostics, diag.New(diag.PRJ006, diag.Params{"id": target, "detail": "cannot deploy a " + level}))
}

// instancesIn gives the instances of target that container deploys in the environment.
func (w *deploymentWalk) instancesIn(container, target string) []*deployment.PlacedInstance {
	var placed []*deployment.PlacedInstance

	instances := w.env.Instances(target)

	for i := range instances {
		if instances[i].Container == container {
			placed = append(placed, &instances[i])
		}
	}

	return placed
}

// instances writes the instances of target in container: one plain line without a declared environment, else one
// block with the status tag for each instance of the environment.
func (w *deploymentWalk) instances(keyword, container, target string, depth int) {
	line := keyword + " " + w.model.ident[target]

	var placed []*deployment.PlacedInstance
	if w.env != nil {
		placed = w.instancesIn(container, target)
	}

	if len(placed) == 0 {
		w.out.line(depth, line)

		return
	}

	for _, instance := range placed {
		w.out.line(depth, line+" {")
		w.out.line(depth+1, "tags "+DSLString(statusTag(instance.Status)))
		w.out.line(depth, "}")
	}
}

// statusTag is the element and relationship tag of a status.
func statusTag[S ~string](status S) string { return "concordia:" + string(status) }

// subtreeStatus gives the worst status of the instances in the deployment node nodeID and in the nodes it deploys.
func (w *deploymentWalk) subtreeStatus(nodeID string) deployment.PlacementStatus {
	return deployment.WorstPlacementStatus(w.subtreeStatuses(nodeID, map[string]bool{})...)
}

// subtreeStatuses gives the statuses of the instances in the deployment node nodeID and in the nodes it deploys,
// visiting each node once.
func (w *deploymentWalk) subtreeStatuses(nodeID string, seen map[string]bool) []deployment.PlacementStatus {
	if seen[nodeID] {
		return nil
	}

	seen[nodeID] = true

	var statuses []deployment.PlacementStatus

	for _, target := range w.targets[nodeID] {
		statuses = append(statuses, w.targetStatuses(nodeID, target, seen)...)
	}

	return statuses
}

// targetStatuses gives the statuses of one target of the deployment node nodeID.
func (w *deploymentWalk) targetStatuses(nodeID, target string, seen map[string]bool) []deployment.PlacementStatus {
	if w.isDeploymentNode(target) {
		return w.subtreeStatuses(target, seen)
	}

	var statuses []deployment.PlacementStatus

	for _, instance := range w.instancesIn(nodeID, target) {
		statuses = append(statuses, instance.Status)
	}

	return statuses
}

// dynamicStepIDs gives the element IDs of the steps of path, the source and then the target of each step.
func dynamicStepIDs(path *DynamicPath) []string {
	ids := make([]string, 0, 2*len(path.Steps))

	for _, step := range path.Steps {
		ids = append(ids, step.FromID, step.ToID)
	}

	return ids
}

// dynamicSystem gives the software-system ID of the container id when it is in the system `current` (any system
// when current is empty). It reports false when id is not a container of that system.
func (m *dslModel) dynamicSystem(id, current string) (string, bool) {
	el := m.byID[id]
	if el == nil || el.Level != intent.C4Container {
		return "", false
	}

	sys := m.systemOf(el)
	if sys == "" {
		return "", false
	}

	if current != "" && sys != current {
		return "", false
	}

	return sys, true
}

// dynamicScope gives the scope of the dynamic view: the software-system key
// when all step elements are containers of one system, else "*".
func (m *dslModel) dynamicScope(path *DynamicPath) string {
	system := ""

	for _, id := range dynamicStepIDs(path) {
		sys, ok := m.dynamicSystem(id, system)
		if !ok {
			return "*"
		}

		system = sys
	}

	if system == "" {
		return "*"
	}

	return m.ident[system]
}

func (m *dslModel) writeDynamic(out *dslWriter, path *DynamicPath) {
	out.line(2, "dynamic "+m.dynamicScope(path)+" "+DSLString(path.Key)+" "+DSLString(path.Title)+" {")

	for _, step := range path.Steps {
		out.line(bodyDepth, m.ident[step.FromID]+" -> "+m.ident[step.ToID]+" "+DSLString(step.Label))
	}

	out.line(bodyDepth, "autoLayout")
	out.line(2, "}")
}

// knowsStep reports whether the model has both elements of the step.
func (m *dslModel) knowsStep(s DynamicStep) bool {
	return m.byID[s.FromID] != nil && m.byID[s.ToID] != nil
}

// evidenceRelationships gives the relationships that the dynamic view needs and
// the model does not have, as "from\x00to" keys in step order.
func (m *dslModel) evidenceRelationships(path *DynamicPath, rels []rel) [][2]string {
	have := map[string]bool{}
	for i := range rels {
		have[rels[i].from+"\x00"+rels[i].to] = true
	}

	var out [][2]string

	for _, s := range path.Steps {
		key := s.FromID + "\x00" + s.ToID
		if have[key] || !m.knowsStep(s) {
			continue
		}

		have[key] = true

		out = append(out, [2]string{s.FromID, s.ToID})
	}

	return out
}

func (m *dslModel) writeEvidenceRelationship(out *dslWriter, pair [2]string) {
	out.line(2, m.ident[pair[0]]+" -> "+m.ident[pair[1]]+" "+DSLString("evidence")+" {")
	out.line(bodyDepth, "tags "+DSLString("concordia:evidence"))
	out.line(2, "}")
}
