package deployment

import (
	"cmp"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// Environment is a declared environment of the workspace.
type Environment struct {
	ID string
	// Cluster is the declared cluster of the environment, empty when the workspace declares none.
	Cluster string
}

// Input is everything that the extraction and the trace read. Nothing in it is changed.
type Input struct {
	// Model is the CALM model. A nil model has no subject.
	Model *intent.ArchitectureModel
	// Evidence is the normalized evidence of the run: the nodes, the facts (with the derived `placed-in` facts), and
	// the synthesized entities.
	Evidence *observation.EvidenceQueryResult
	// BindingSet is the resolution of the bindings against the evidence.
	BindingSet *binding.Set
	// Bindings are the loaded bindings of the workspace.
	Bindings []binding.Binding
	// Declared are the environments of the workspace, in declaration order.
	Declared []Environment
	// Selected are the IDs of the environments of the run (`check --environment`). nil selects every declared one.
	Selected []string
	// Coverage are the predicate coverage records of the run.
	Coverage []observation.PredicateCoverage
	// Rules are the obligation results of the `infrastructure` rules, for the `rules` list of each trace.
	Rules []RuleLink
	// Lineage gives the exact-lineage state of a container occurrence from the assurance evidence (see LineageFunc).
	// nil: no assurance evidence, so the trace shows static links only.
	Lineage LineageFunc
	// Evaluated is the evidence of the level `rendered-or-evaluated` of each environment (rendered sources and plans).
	// nil: no assurance evidence.
	Evaluated []EvaluatedEnvironment
}

func (input *Input) clusterOf(environment string) string {
	for i := range input.Declared {
		if input.Declared[i].ID == environment {
			return input.Declared[i].Cluster
		}
	}

	return ""
}

// environments gives the environments of the trace: the selected ones, in the order of the declaration.
func (input *Input) environments() []Environment {
	if input.Selected == nil {
		return input.Declared
	}

	out := make([]Environment, 0, len(input.Declared))

	for _, env := range input.Declared {
		if slices.Contains(input.Selected, env.ID) {
			out = append(out, env)
		}
	}

	return out
}

// ContainerOccurrence is one container of a workload with its image reference, read from a `uses-image` fact.
// A reference that is a template, or that is not a reference, is Unresolved and has no image.
type ContainerOccurrence struct {
	// Container is the container name, empty when the fact has none.
	Container string
	// Image is the parsed reference of the `image` property of the fact. Zero when Unresolved.
	Image observation.ImageRef
	// FactID is the ID of the `uses-image` fact.
	FactID   string
	Location Location
	// Unresolved is true for a reference that has a template or variable, or that does not parse.
	Unresolved bool
	// Lossy is true when the provider may have merged this occurrence with others (O4).
	Lossy bool
}

// HostingTarget is the hosting target of a workload: the namespace that a `placed-in` fact names.
type HostingTarget struct {
	// Identity is the scoped identity of the namespace in the environment of the workload.
	Identity string
	// Name is the namespace name.
	Name string
	// FactID is the ID of the `placed-in` fact.
	FactID string
	// Locations are the manifests that declare the Namespace. Empty for a synthesized target.
	Locations []Location
	// Subjects are the CALM IDs that the bindings map the target to (a container node, for example).
	Subjects []string
	// Declared is true when a manifest of the environment declares the Namespace.
	Declared bool
}

// Workload is a workload of the manifests: the scoped identity of `observed-entity-identity` (with the declared
// cluster), its locations (several for competing declarations), and its fact IDs.
type Workload struct {
	// Identity is the canonical scoped identity (`k8s:<env>/<cluster>/<group>/<Kind>/<namespace>/<name>`).
	Identity string
	// Environment is the declared environment, or `unresolved` for a workload that no environment source holds.
	Environment string
	// Cluster is the declared cluster of the environment, empty when there is none.
	Cluster   string
	APIGroup  string
	Kind      string
	Namespace string
	Name      string
	// Locations are the manifest locations, sorted. More than one is a competing declaration.
	Locations []Location
	// NodeIDs are the observed IDs of the nodes of the declarations.
	NodeIDs []string
	// FactIDs are the IDs of the `uses-image` and `placed-in` facts of the workload.
	FactIDs     []string
	Occurrences []ContainerOccurrence
	Placements  []HostingTarget
	// Subjects are the CALM IDs that the bindings map the workload to.
	Subjects []string
	// BindingIDs are the bindings that mapped it.
	BindingIDs []string
	// Ambiguous is true when non-exclusive bindings map the workload to different CALM nodes (`ambiguous-binding`).
	Ambiguous bool
	// Competing is true when several files of the environment declare the identity (`evidence-conflict`).
	Competing bool
	// Templated is true when the name is a template or the namespace is not resolved: the manifest is not rendered.
	Templated bool
}

// EnvironmentResolved says whether the workload belongs to a declared environment.
func (w *Workload) EnvironmentResolved() bool {
	return w.Environment != observation.EnvironmentUnresolved
}

// BaseImage is a base image of a Dockerfile stage (a `base-image` fact).
type BaseImage struct {
	Stage string
	Image observation.ImageRef
	// FactID is the ID of the fact.
	FactID   string
	Location Location
	// Unresolved is true for a `FROM` that uses a build argument (or a template) with no default: a visible gap.
	Unresolved bool
}

// ExternalSource is an external image that a stage copies from (a `copies-from-image` fact). A stage that
// `COPY --from` names is not an external source: the provider has no fact for it.
type ExternalSource struct {
	Stage      string
	Image      observation.ImageRef
	FactID     string
	Location   Location
	Unresolved bool
}

// BuildCommand is the command that names an image and a Dockerfile (a `built-from` fact). Without it the provider
// has no link between the image and the Dockerfile (O9).
type BuildCommand struct {
	Tool     string
	FactID   string
	Location Location
}

// BuildDefinition is a Dockerfile that a build command links to an image repository, with its base images, stage
// indexes, and external copy sources, taken from the `base-image` and `copies-from-image` facts.
type BuildDefinition struct {
	Repository string
	Path       string
	Commands   []BuildCommand
	Bases      []BaseImage
	Copies     []ExternalSource
	Line       int
}

// Action is a deployment action: a `deploys-artifact` fact with its tool, location, and target. A script that
// applies manifests targets a file, and a Helm command targets a chart (O8). An action is never a placement.
type Action struct {
	// Tool is the tool of the action (`kubectl`, `helm`).
	Tool string
	// Target is the file or the chart directory that the action names.
	Target string
	// TargetKind is `file` or `chart`.
	TargetKind string
	FactID     string
	// Environment is the environment that the location of the action is attributed to; empty or `unresolved` for none.
	Environment string
	Location    Location
}

// The kinds of an action target.
const (
	TargetFile  = "file"
	TargetChart = "chart"
)

// Covers says whether the action names the manifest file: the file itself, or a file under the chart directory.
func (a *Action) Covers(file string) bool {
	if a.TargetKind == TargetChart {
		return strings.HasPrefix(file, strings.TrimSuffix(path.Clean(a.Target), "/")+"/")
	}

	return path.Clean(a.Target) == path.Clean(file)
}

// EvidenceKinds are the canonical kinds that the trace reads: the image references of the workloads, the build links
// and the Dockerfile stages, and the deployment actions. The `placed-in` fact is derived from the workload nodes and
// is not fetched. A run with declared environments fetches them even when no rule traverses them.
func EvidenceKinds() []observation.CanonicalEdgeKind {
	return []observation.CanonicalEdgeKind{
		observation.KindUsesImage, observation.KindBuiltFrom, observation.KindBaseImage, observation.KindCopiesFromImage,
		observation.KindDeploysArtifact,
	}
}

// View is the extraction view: the workloads, the build definitions, and the actions of the evidence, with source
// locations. It is built without I/O. Every item stays in the view, whatever its environment or binding is.
type View struct {
	builds    map[string][]BuildDefinition
	consumers map[string]int
	Workloads []Workload
	Actions   []Action
}

// BuildDefinitions gives the Dockerfiles that a build command links to the image repository, sorted by path.
func (v *View) BuildDefinitions(repository string) []BuildDefinition { return v.builds[repository] }

// Consumers gives the number of workloads of all declared environments that use the image repository.
func (v *View) Consumers(repository string) int { return v.consumers[repository] }

// Extract builds the view of the input. It reads nodes, facts, entities, and the binding set; it does not change them.
func Extract(input *Input) *View {
	view := &View{builds: map[string][]BuildDefinition{}, consumers: map[string]int{}}
	if input == nil || input.Evidence == nil {
		return view
	}

	index := indexFacts(input.Evidence)
	view.Actions = index.actions()
	view.builds = index.buildDefinitions(input.Evidence)
	view.Workloads = workloadsOf(input, index)
	view.consumers = consumersOf(view.Workloads)

	return view
}

// workloadsOf reads the workload of each declaration group, sorted by identity.
func workloadsOf(input *Input, index *factIndex) []Workload {
	decls := observation.DeclarationsOfValid(input.Evidence.Nodes, input.clusterOf)
	groups := observation.GroupDeclarations(decls)

	var out []Workload

	for i := range groups {
		if observation.IsWorkloadKind(groups[i].ID.Kind) {
			out = append(out, workloadOf(input, index, &groups[i]))
		}
	}

	slices.SortFunc(out, func(a, b Workload) int { return canonjson.CompareUTF16(a.Identity, b.Identity) })

	return out
}

// consumersOf counts, for each image repository, the workloads of a resolved environment that use it.
func consumersOf(workloads []Workload) map[string]int {
	pairs := map[[2]string]struct{}{}

	for i := range workloads {
		workload := &workloads[i]
		if !workload.EnvironmentResolved() {
			continue
		}

		for _, repository := range resolvedRepositories(workload) {
			pairs[[2]string{repository, workload.Identity}] = struct{}{}
		}
	}

	consumers := map[string]int{}
	for pair := range pairs {
		consumers[pair[0]]++
	}

	return consumers
}

// resolvedRepositories gives the image repositories of the occurrences of the workload that were read.
func resolvedRepositories(workload *Workload) []string {
	var out []string

	for i := range workload.Occurrences {
		if !workload.Occurrences[i].Unresolved {
			out = append(out, workload.Occurrences[i].Image.Repository)
		}
	}

	return out
}

// factIndex indexes the facts that the view reads.
type factIndex struct {
	byFrom  map[string][]*observation.EvidenceEdge
	builtBy []*observation.EvidenceEdge
	deploys []*observation.EvidenceEdge
}

func indexFacts(evidence *observation.EvidenceQueryResult) *factIndex {
	index := &factIndex{byFrom: map[string][]*observation.EvidenceEdge{}}

	for _, edge := range evidence.Edges {
		switch string(edge.Kind) {
		case string(observation.KindUsesImage), string(observation.KindPlacedIn), string(observation.KindBaseImage), string(observation.KindCopiesFromImage):
			index.byFrom[edge.From] = append(index.byFrom[edge.From], edge)
		case string(observation.KindBuiltFrom):
			index.builtBy = append(index.builtBy, edge)
		case string(observation.KindDeploysArtifact):
			index.deploys = append(index.deploys, edge)
		}
	}

	return index
}

func locationOf(edge *observation.EvidenceEdge) Location {
	if edge.SourceSpan == nil {
		return Location{}
	}

	return Location{Path: edge.SourceSpan.Path, Line: edge.SourceSpan.Line}
}

func nodeLocation(node *observation.ObservedNode) Location {
	return Location{Path: node.FilePath, Line: node.StartLine}
}

func compareLocations(a, b Location) int {
	return cmp.Or(canonjson.CompareUTF16(a.Path, b.Path), cmp.Compare(a.Line, b.Line))
}

// sortedLocations sorts the locations, drops those with no path, and removes duplicates. Never nil.
func sortedLocations(locations []Location) []Location {
	out := slices.DeleteFunc(slices.Clone(locations), func(l Location) bool { return l.Path == "" })

	slices.SortFunc(out, compareLocations)

	return slices.CompactFunc(out, func(a, b Location) bool { return a == b })
}

func sortedStrings(values []string) []string {
	out := slices.Clone(values)

	slices.SortFunc(out, canonjson.CompareUTF16)

	out = slices.Compact(out)
	if out == nil {
		return []string{}
	}

	return out
}

const (
	templateMarker    = "{{"
	templateDot       = "<."
	templateVariable  = "${"
	imageNodeQNPrefix = "__image__"
	propertyVia       = "via"
	propertyChart     = "chart"
	propertyPath      = "path"
	propertyLine      = "line"
	propertyFile      = "file"
)

func isTemplated(s string) bool {
	return strings.Contains(s, templateMarker) || strings.Contains(s, templateDot) || strings.Contains(s, templateVariable)
}

func workloadOf(input *Input, index *factIndex, group *observation.DeclarationGroup) Workload {
	id := group.ID
	workload := Workload{
		Identity: id.String(), Cluster: "", APIGroup: id.APIGroup, Kind: id.Kind, Namespace: id.Namespace, Name: id.Name,
		Competing: group.Competing(), Environment: observation.EnvironmentUnresolved,
		Templated: isTemplated(id.Name) || id.Namespace == observation.UnresolvedPart,
	}

	if id.EnvironmentResolved() {
		workload.Environment = id.Environment
		workload.Cluster = input.clusterOf(id.Environment)
	}

	locations := make([]Location, 0, len(group.Declarations))

	for i := range group.Declarations {
		decl := &group.Declarations[i]

		workload.NodeIDs = append(workload.NodeIDs, decl.NodeID)
		locations = append(locations, Location{Path: decl.Path, Line: decl.Line})

		absorbBindings(&workload, input.BindingSet, decl.NodeID)
		absorbFacts(&workload, input, index.byFrom[decl.NodeID])
	}

	workload.Locations = sortedLocations(locations)
	workload.NodeIDs = sortedStrings(workload.NodeIDs)
	workload.FactIDs = sortedStrings(workload.FactIDs)
	workload.Subjects = sortedStrings(workload.Subjects)
	workload.BindingIDs = sortedStrings(workload.BindingIDs)
	workload.Occurrences = uniqueOccurrences(workload.Occurrences)
	workload.Placements = uniquePlacements(workload.Placements)

	return workload
}

// absorbBindings adds the CALM nodes, the bindings and the ambiguity that the binding set reads for an observed node.
func absorbBindings(workload *Workload, set *binding.Set, nodeID string) {
	if set == nil {
		return
	}

	workload.Subjects = append(workload.Subjects, set.ByObserved[nodeID]...)
	workload.BindingIDs = append(workload.BindingIDs, set.UsedBy[nodeID]...)

	if _, ambiguous := set.Ambiguous[nodeID]; ambiguous {
		workload.Ambiguous = true
	}
}

// absorbFacts adds the container occurrences and the hosting targets of the `uses-image` and `placed-in` facts.
func absorbFacts(workload *Workload, input *Input, edges []*observation.EvidenceEdge) {
	for _, edge := range edges {
		switch string(edge.Kind) {
		case string(observation.KindUsesImage):
			workload.Occurrences = append(workload.Occurrences, occurrenceOf(edge))
			workload.FactIDs = append(workload.FactIDs, edge.ID)
		case string(observation.KindPlacedIn):
			workload.Placements = append(workload.Placements, hostingTargetOf(input, edge))
			workload.FactIDs = append(workload.FactIDs, edge.ID)
		}
	}
}

// occurrenceOf reads a `uses-image` fact. The tag and the digest come from the `image` property of the fact, never
// from the `Image` node (O3).
func occurrenceOf(edge *observation.EvidenceEdge) ContainerOccurrence {
	occurrence := ContainerOccurrence{
		Container: edge.Properties[propContainer], FactID: edge.ID, Location: locationOf(edge), Lossy: edge.PossiblyLossy,
	}

	parsed, ok, err := observation.ContainerOccurrenceOf(edge)
	if err != nil || !ok {
		occurrence.Unresolved = true

		return occurrence
	}

	occurrence.Image = parsed.Image

	return occurrence
}

func uniqueOccurrences(list []ContainerOccurrence) []ContainerOccurrence {
	slices.SortFunc(list, func(a, b ContainerOccurrence) int {
		return cmp.Or(canonjson.CompareUTF16(a.Container, b.Container), canonjson.CompareUTF16(a.Image.Raw, b.Image.Raw),
			compareLocations(a.Location, b.Location), canonjson.CompareUTF16(a.FactID, b.FactID))
	})

	return slices.CompactFunc(list, func(a, b ContainerOccurrence) bool {
		return a.Container == b.Container && a.Image.Raw == b.Image.Raw && a.Unresolved == b.Unresolved
	})
}

func hostingTargetOf(input *Input, edge *observation.EvidenceEdge) HostingTarget {
	target := HostingTarget{FactID: edge.ID, Locations: []Location{}}

	node, isNode := evidenceNodeOf(input.Evidence, edge.To)
	if key, ok := hostingObjKey(&node); ok {
		target.Identity, target.Name = hostingIdentity(input, edge.Environment, key)
	}

	if isNode && isDeclaredResource(&node) {
		target.Declared = true
		target.Locations = sortedLocations([]Location{nodeLocation(&node)})
	}

	if input.BindingSet != nil {
		target.Subjects = sortedStrings(input.BindingSet.ByObserved[edge.To])
	}

	return target
}

// evidenceNodeOf finds the node of the evidence by ID. isNode is true for a provider node and false for a
// synthesized entity or when there is none (a zero node).
func evidenceNodeOf(evidence *observation.EvidenceQueryResult, id string) (node observation.ObservedNode, isNode bool) {
	if node, isNode = evidence.Nodes[id]; isNode {
		return node, true
	}

	for i := range evidence.Entities {
		if evidence.Entities[i].ObservedID == id {
			return evidence.Entities[i], false
		}
	}

	return node, false
}

// hostingObjKey gives the object key of a hosting node: the one that the observation reads, else its `obj_key` property.
func hostingObjKey(node *observation.ObservedNode) (string, bool) {
	if key, ok := observation.ObjKeyOf(node); ok {
		return key, true
	}

	key := node.Properties["obj_key"]

	return key, key != ""
}

func isDeclaredResource(node *observation.ObservedNode) bool {
	return node.Label == "Resource" && node.FilePath != ""
}

func hostingIdentity(input *Input, environment, objKey string) (identity, name string) {
	scoped, err := observation.ScopedIDFromObjKey(environment, input.clusterOf(environment), objKey)
	if err != nil {
		return "", ""
	}

	return scoped.String(), scoped.Name
}

func uniquePlacements(list []HostingTarget) []HostingTarget {
	slices.SortFunc(list, func(a, b HostingTarget) int {
		return cmp.Or(canonjson.CompareUTF16(a.Identity, b.Identity), canonjson.CompareUTF16(a.FactID, b.FactID))
	})

	return slices.CompactFunc(list, func(a, b HostingTarget) bool { return a.Identity == b.Identity })
}

// actions reads the `deploys-artifact` facts that a tool run makes: a `kubectl` action names a file and a `helm`
// action names a chart. The facts that run from a chart to its templates (`via` chart) are no action and are left out.
func (index *factIndex) actions() []Action {
	out := make([]Action, 0, len(index.deploys))

	for _, edge := range index.deploys {
		action := Action{
			Tool: edge.Properties[propertyVia], FactID: edge.ID, Environment: edge.Environment, Location: locationOf(edge),
		}

		var ok bool
		if action.Target, action.TargetKind, ok = actionTargetOf(edge, action.Tool); ok {
			out = append(out, action)
		}
	}

	slices.SortFunc(out, func(a, b Action) int {
		return cmp.Or(compareLocations(a.Location, b.Location), canonjson.CompareUTF16(a.Target, b.Target), canonjson.CompareUTF16(a.FactID, b.FactID))
	})

	return slices.CompactFunc(out, func(a, b Action) bool { return a.FactID == b.FactID })
}

// actionTargetOf gives the chart or the file that a `deploys-artifact` fact names; not ok when it names none.
func actionTargetOf(edge *observation.EvidenceEdge, tool string) (target, kind string, ok bool) {
	if chart := edge.Properties[propertyChart]; chart != "" && tool != "chart" {
		return chart, TargetChart, true
	}

	if file := edge.Properties[propertyPath]; file != "" {
		return file, TargetFile, true
	}

	return "", "", false
}

// buildDefinitions reads the Dockerfiles that a `built-from` fact links to an image repository, with the base
// images and the external copy sources of each Dockerfile.
func (index *factIndex) buildDefinitions(evidence *observation.EvidenceQueryResult) map[string][]BuildDefinition {
	return groupBuildDefinitions(index.definitionsByKey(evidence))
}

// definitionsByKey collects one definition for each pair of an image repository and a Dockerfile path.
func (index *factIndex) definitionsByKey(evidence *observation.EvidenceQueryResult) map[[2]string]*BuildDefinition {
	byKey := map[[2]string]*BuildDefinition{}

	for _, edge := range index.builtBy {
		image, dockerfile, ok := builtFromEnds(evidence, edge)
		if !ok {
			continue
		}

		repository := imageRepositoryOf(&image)
		key := [2]string{repository, dockerfile.FilePath}

		def := byKey[key]
		if def == nil {
			def = &BuildDefinition{Repository: repository, Path: dockerfile.FilePath, Line: dockerfile.StartLine}
			def.Bases, def.Copies = stagesOf(index.byFrom[edge.To])
			byKey[key] = def
		}

		def.Commands = append(def.Commands, BuildCommand{
			Tool: edge.Properties[propTool], FactID: edge.ID, Location: buildLocation(edge),
		})
	}

	return byKey
}

// builtFromEnds gives the `Image` node and the Dockerfile node of a `built-from` fact; not ok when an end is missing,
// the source is no image, or the Dockerfile has no path.
func builtFromEnds(evidence *observation.EvidenceQueryResult, edge *observation.EvidenceEdge) (image, dockerfile observation.ObservedNode, ok bool) {
	image, ok = evidence.Nodes[edge.From]
	if !ok || image.Label != observation.LabelImage {
		return image, dockerfile, false
	}

	dockerfile, ok = evidence.Nodes[edge.To]

	return image, dockerfile, ok && dockerfile.FilePath != ""
}

// groupBuildDefinitions sorts the commands of each definition and groups the definitions by repository, sorted by path.
func groupBuildDefinitions(byKey map[[2]string]*BuildDefinition) map[string][]BuildDefinition {
	out := map[string][]BuildDefinition{}

	for _, def := range byKey {
		slices.SortFunc(def.Commands, func(a, b BuildCommand) int {
			return cmp.Or(compareLocations(a.Location, b.Location), canonjson.CompareUTF16(a.FactID, b.FactID))
		})

		if def.Line == 0 && len(def.Bases) > 0 {
			def.Line = def.Bases[0].Location.Line
		}

		out[def.Repository] = append(out[def.Repository], *def)
	}

	for repository := range out {
		slices.SortFunc(out[repository], func(a, b BuildDefinition) int { return canonjson.CompareUTF16(a.Path, b.Path) })
	}

	return out
}

// buildLocation gives the place of the build command: the `file` and `line` properties of the fact, else its span.
func buildLocation(edge *observation.EvidenceEdge) Location {
	if file := edge.Properties[propertyFile]; file != "" {
		return Location{Path: file, Line: atoi(edge.Properties[propertyLine])}
	}

	return locationOf(edge)
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}

	return n
}

// stagesOf reads the base images and the external copy sources from the facts of a Dockerfile node.
func stagesOf(facts []*observation.EvidenceEdge) (bases []BaseImage, copies []ExternalSource) {
	for _, edge := range facts {
		occurrence, ok, err := observation.ContainerOccurrenceOf(edge)
		unresolved := err != nil || !ok

		switch string(edge.Kind) {
		case string(observation.KindBaseImage):
			bases = append(bases, BaseImage{
				Stage: edge.Properties[propStage], Image: occurrence.Image, FactID: edge.ID, Location: locationOf(edge), Unresolved: unresolved,
			})
		case string(observation.KindCopiesFromImage):
			copies = append(copies, ExternalSource{
				Stage: edge.Properties[propStage], Image: occurrence.Image, FactID: edge.ID, Location: locationOf(edge), Unresolved: unresolved,
			})
		}
	}

	slices.SortFunc(bases, func(a, b BaseImage) int {
		return cmp.Or(cmp.Compare(atoi(a.Stage), atoi(b.Stage)), compareLocations(a.Location, b.Location), canonjson.CompareUTF16(a.FactID, b.FactID))
	})
	slices.SortFunc(copies, func(a, b ExternalSource) int {
		return cmp.Or(cmp.Compare(atoi(a.Stage), atoi(b.Stage)), compareLocations(a.Location, b.Location), canonjson.CompareUTF16(a.FactID, b.FactID))
	})

	return bases, copies
}

// imageRepositoryOf gives the repository of an `Image` node or entity.
func imageRepositoryOf(node *observation.ObservedNode) string {
	if repository := node.Properties[propRepository]; repository != "" {
		return repository
	}

	return strings.TrimPrefix(node.QualifiedName, imageNodeQNPrefix)
}
