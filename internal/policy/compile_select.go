package policy

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/edgekind"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

// Selector resolution.

type nodeIndex struct {
	ids   map[string]struct{}
	nodes []intent.NodeDeclaration
	// deployed are the CALM IDs that a `deployed-in` relationship lists as deployed nodes. Sorted, without duplicates.
	deployed []string
}

// newNodeIndex indexes the nodes of the model and finds the nodes that a `deployed-in` relationship lists. These are
// the subjects of `subject: {deployedIn: true}`.
func newNodeIndex(model *intent.ArchitectureModel) nodeIndex {
	index := nodeIndex{ids: map[string]struct{}{}, nodes: model.Nodes}

	for i := range model.Nodes {
		index.ids[model.Nodes[i].ID] = struct{}{}
	}

	for i := range model.Relationships {
		if rel := &model.Relationships[i]; rel.Kind == intent.RelDeployedIn {
			index.deployed = append(index.deployed, knownOnly(rel.Targets, index.ids)...)
		}
	}

	index.deployed = uniqSorted(index.deployed)

	return index
}

// knownOnly gives the IDs that are in the index, in order.
func knownOnly(ids []string, known map[string]struct{}) []string {
	var out []string

	for _, id := range ids {
		if _, ok := known[id]; ok {
			out = append(out, id)
		}
	}

	return out
}

func uniqSorted[T ~string](items []T) []T {
	out := slices.Clone(items)
	slices.SortFunc(out, func(a, b T) int { return canonjson.CompareUTF16(string(a), string(b)) })

	if out == nil {
		return []T{}
	}

	return slices.Compact(out)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}

func stringsOf(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))

	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

// compiler holds the input of the rules of one policy file.
type compiler struct {
	named map[string]rawSelector
	// checkIDs are the check identifiers that a `require.checkResult` may name: the `check-ids` of the controls and
	// CompileOverrides.KnownCheckIDs.
	checkIDs    map[string]struct{}
	controls    ControlOverrideMap
	diagnostics *[]diag.Diagnostic
	pack        PackRef
	uri         string
	overrides   CompileOverrides
	index       nodeIndex
	caps        ProviderCaps
	// modelFree is set when there is no architecture model (DescribePack): CALM IDs and check IDs are not
	// checked, and a selector that needs the nodes resolves to none.
	modelFree bool
}

func (c *compiler) at(pointer string) diag.Option {
	return diag.WithSource(diag.Source{URI: c.uri, Pointer: pointer})
}

func (c *compiler) knownIDs(ids []string, pointer string) *diag.Diagnostic {
	if c.modelFree {
		return nil
	}

	for _, id := range ids {
		if _, ok := c.index.ids[id]; !ok {
			d := diag.New(diag.POL005, diag.Params{idParam: id}, c.at(pointer))

			return &d
		}
	}

	return nil
}

// selectorKeysProblem rejects a selector that resolveSelector cannot resolve to CALM IDs:
// an unknown key, an implicit composition, and a dimension or operator (these are for obligation.applies).
func (c *compiler) selectorKeysProblem(sel rawSelector, pointer string) *diag.Diagnostic {
	class, problem := classifySelector(sel)
	if problem != "" {
		return c.selectorProblem(problem, pointer)
	}

	if class != classLegacy {
		return c.selectorProblem("dimensions and the operators allOf, anyOf and not are only valid in obligation.applies; "+
			"here use "+strings.Join(selectorKeys, ", "), pointer)
	}

	return nil
}

func (c *compiler) selectorAt(sel rawSelector, pointer string) ([]string, *diag.Diagnostic) {
	return c.resolveSelector(sel, pointer, nil)
}

func (c *compiler) resolveSelector(sel rawSelector, pointer string, stack []string) ([]string, *diag.Diagnostic) {
	if d := c.selectorKeysProblem(sel, pointer); d != nil {
		return nil, d
	}

	out, d := c.calmNodeIDs(sel, pointer)
	if d != nil {
		return nil, d
	}

	out = append(out, c.nodeIDsByAttribute(sel)...)

	refIDs, d := c.refIDs(sel, pointer, stack)
	if d != nil {
		return nil, d
	}

	return uniqSorted(append(out, refIDs...)), nil
}

// calmNodeIDs gives the IDs that the `calmNodes` key lists. The IDs must be in the model.
func (c *compiler) calmNodeIDs(sel rawSelector, pointer string) ([]string, *diag.Diagnostic) {
	nodes, ok := sel["calmNodes"]
	if !ok {
		return nil, nil
	}

	ids := stringsOf(nodes)

	return ids, c.knownIDs(ids, pointer+"/calmNodes")
}

// nodeIDsByAttribute gives the IDs of the nodes that the `nodeTypes` and `tags` keys choose.
func (c *compiler) nodeIDsByAttribute(sel rawSelector) []string {
	var out []string

	if types, ok := sel["nodeTypes"]; ok {
		out = append(out, nodesOfTypes(c.index.nodes, stringsOf(types))...)
	}

	if tags, ok := sel["tags"]; ok {
		out = append(out, nodesWithTags(c.index.nodes, stringsOf(tags))...)
	}

	return out
}

// refIDs gives the IDs that the named selector of the `ref` key resolves to.
func (c *compiler) refIDs(sel rawSelector, pointer string, stack []string) ([]string, *diag.Diagnostic) {
	ref, ok := sel["ref"].(string)
	if !ok {
		return nil, nil
	}

	return c.resolveRef(ref, pointer, stack)
}

// nodesOfTypes gives the IDs of the nodes whose node type is one of want, in node order.
func nodesOfTypes(nodes []intent.NodeDeclaration, want []string) []string {
	var out []string

	for i := range nodes {
		if slices.Contains(want, nodes[i].NodeType) {
			out = append(out, nodes[i].ID)
		}
	}

	return out
}

// nodesWithTags gives the IDs of the nodes that have one of the tags in want, in node order.
func nodesWithTags(nodes []intent.NodeDeclaration, want []string) []string {
	var out []string

	for i := range nodes {
		if slices.ContainsFunc(nodes[i].Tags, func(t string) bool { return slices.Contains(want, t) }) {
			out = append(out, nodes[i].ID)
		}
	}

	return out
}

// ResolveNodeSelector gives the IDs of the nodes that the selector keys `calmNodes`, `nodeTypes` and `tags` of a
// policy selector choose, with the same semantics: the union of the three. The result is sorted without duplicates
// and never nil. A CALM node ID that the model does not declare matches nothing.
func ResolveNodeSelector(model *intent.ArchitectureModel, calmNodes, nodeTypes, tags []string) []string {
	if model == nil {
		return []string{}
	}

	var out []string

	for i := range model.Nodes {
		if slices.Contains(calmNodes, model.Nodes[i].ID) {
			out = append(out, model.Nodes[i].ID)
		}
	}

	out = append(out, nodesOfTypes(model.Nodes, nodeTypes)...)
	out = append(out, nodesWithTags(model.Nodes, tags)...)

	return uniqSorted(out)
}

func (c *compiler) resolveRef(ref, pointer string, stack []string) ([]string, *diag.Diagnostic) {
	target, d := c.refTarget(ref, pointer, stack)
	if d != nil {
		return nil, d
	}

	return c.resolveSelector(target, "/selectors/"+ref, append(slices.Clone(stack), ref))
}

// refTarget gives the named selector that ref points at. An unknown name and a reference cycle give CO-POL-002.
func (c *compiler) refTarget(ref, pointer string, stack []string) (rawSelector, *diag.Diagnostic) {
	target, ok := c.named[ref]
	if !ok {
		d := diag.New(diag.POL002, diag.Detail("unknown selector reference '"+ref+"'"), c.at(pointer+"/ref"))

		return nil, &d
	}

	if slices.Contains(stack, ref) {
		chain := strings.Join(append(slices.Clone(stack), ref), " -> ")
		d := diag.New(diag.POL002, diag.Detail("selector reference cycle: "+chain), c.at(pointer+"/ref"))

		return nil, &d
	}

	return target, nil
}

func (c *compiler) resolveEdges(tokens []string, pointer string) ([]observation.CanonicalEdgeKind, *diag.Diagnostic) {
	kinds := make([]observation.CanonicalEdgeKind, 0, len(tokens))

	for _, token := range tokens {
		resolved, ok := edgekind.Resolve(token)
		if !ok {
			return nil, c.edgeKindProblem(token, pointer)
		}

		kinds = append(kinds, resolved...)
	}

	return uniqSorted(kinds), nil
}

// edgeKindProblem is the CO-POL-004 diagnostic of an edge kind token that the rule cannot use.
func (c *compiler) edgeKindProblem(token, pointer string) *diag.Diagnostic {
	d := diag.New(diag.POL004, diag.Params{"kind": token}, c.at(pointer))

	return &d
}
