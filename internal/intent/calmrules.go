package intent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// Rule IDs of the CALM CLI architecture ruleset
// (shared/src/spectral/rules-architecture.ts).
const (
	ruleJSONSchema             = "json-schema"
	ruleHasNodesRelationships  = "architecture-has-nodes-relationships"
	ruleNoEmptyString          = "architecture-has-no-empty-string-properties"
	ruleNumericalPlaceholder   = "architecture-has-no-placeholder-properties-numerical"
	ruleStringPlaceholder      = "architecture-has-no-placeholder-properties-string"
	ruleBooleanPlaceholder     = "architecture-has-no-placeholder-properties-boolean"
	ruleRelationshipNodes      = "relationship-references-existing-nodes-in-architecture"
	ruleConnectsNodes          = "connects-relationship-references-existing-nodes-in-architecture"
	ruleInterfacesDefined      = "referenced-interfaces-defined-in-architecture"
	ruleInterfacesOnNode       = "referenced-interfaces-defined-on-correct-node-in-architecture"
	ruleCompositionNodes       = "composition-relationships-reference-existing-nodes-in-architecture"
	ruleNodesReferenced        = "architecture-nodes-must-be-referenced"
	ruleUniqueIDs              = "unique-ids-must-be-unique-in-architecture"
	ruleFlowRelationshipExists = "flow-transitions-references-existing-relationship-in-architecture"
	ruleFlowSequenceUnique     = "flow-transitions-have-unique-sequence-numbers"
)

// jsSpaceClass is the JavaScript regular expression class \s.
const jsSpaceClass = `[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var (
	stringPlaceholder  = regexp.MustCompile(`^\[\[` + jsSpaceClass + `*[A-Z_]+` + jsSpaceClass + `*\]\]$`)
	booleanPlaceholder = regexp.MustCompile(`^\[\[` + jsSpaceClass + `*BOOLEAN_[A-Z_]+` + jsSpaceClass + `*\]\]$`)
)

// architectureRules runs the ported CALM CLI architecture ruleset on a document.
func architectureRules(doc obj) *findings {
	res := newFindings(doc)
	root := match{path: []string{}, value: doc}

	checkTopLevel(res, root)
	checkStringValues(res, root, &stringValueRules{
		empty:     ruleNoEmptyString,
		numerical: ruleNumericalPlaceholder,
		str:       ruleStringPlaceholder,
		boolean:   ruleBooleanPlaceholder,
		noun:      "architecture",
	})
	checkNodeReferences(res, root)
	checkInterfaces(res, root)
	checkNodesReferenced(res, root)
	checkUniqueIDs(res, root)
	checkFlows(res, root)

	return res
}

func checkTopLevel(res *findings, root match) {
	for _, field := range []string{keyNodes, keyRelationships} {
		target := lintTarget(root, field)
		if !truthy(target.value) {
			res.add(ruleHasNodesRelationships, diag.INT014, target.path, "Should have nodes and relationships as top level properties on the CALM document")
		}
	}
}

// stringValueRules holds the rule IDs of the `$..*` rules on single values. An
// empty rule ID skips that rule. noun is "architecture" or "pattern" for the
// messages.
type stringValueRules struct {
	empty     string
	numerical string
	str       string
	boolean   string
	noun      string
}

// checkStringValues ports the `$..*` rules on single values: the empty string
// and the numerical, string, and boolean placeholders.
func checkStringValues(res *findings, root match, rules *stringValueRules) {
	for _, entry := range descendants(root) {
		rules.checkEntry(res, entry)
	}
}

func (r *stringValueRules) checkEntry(res *findings, entry match) {
	if r.numerical != "" && looseEqualsMinusOne(entry.value) {
		res.add(r.numerical, diag.INT015, entry.path, "Value was equal to -1 - placeholder property detected")
	}

	if text, isStr := entry.value.(string); isStr {
		r.checkText(res, entry, text)
	}
}

func (r *stringValueRules) checkText(res *findings, entry match, text string) {
	if r.empty != "" && text == "" {
		res.add(r.empty, diag.INT014, entry.path, "All properties must be set to a nonempty value.")
	}

	if r.str != "" && stringPlaceholder.MatchString(text) {
		res.add(r.str, diag.INT015, entry.path, "String placeholder detected in "+r.noun+".")
	}

	if r.boolean != "" && booleanPlaceholder.MatchString(text) {
		res.add(r.boolean, diag.INT015, entry.path, "Boolean placeholder detected in "+r.noun+".")
	}
}

// nodeIDExists ports node-id-exists.ts. It keeps the Concordia code CO-INT-005.
func nodeIDExists(res *findings, rule string, ids valueSet, target match) {
	if !truthy(target.value) || ids.has(target.value) {
		return
	}

	text := jsString(target.value)
	res.record(&finding{
		rule:    rule,
		code:    diag.INT005,
		path:    target.path,
		message: fmt.Sprintf("'%s' does not refer to the unique-id of an existing node.", text),
		params:  diag.Params{"id": text},
	})
}

func checkNodeReferences(res *findings, root match) {
	ids := newValueSet(selectPath(root, keyNodes, "*", "unique-id"))
	relTypes := selectPath(root, keyRelationships, "*", "relationship-type")

	for _, kind := range childrenOf(relTypes) {
		for _, field := range []string{"actor", "container"} {
			target := lintTarget(kind, field)
			nodeIDExists(res, ruleRelationshipNodes, ids, target)
		}
	}

	for _, end := range childrenOf(props(relTypes, "connects")) {
		target := lintTarget(end, "node")
		nodeIDExists(res, ruleConnectsNodes, ids, target)
	}

	for _, node := range childrenOf(props(childrenOf(relTypes), keyNodes)) {
		nodeIDExists(res, ruleCompositionNodes, ids, node)
	}
}

func checkInterfaces(res *findings, root match) {
	interfaceIDs := newValueSet(selectPath(root, keyNodes, "*", "interfaces", "*", "unique-id"))
	ends := selectPath(root, keyRelationships, "*", "relationship-type", "connects", "*")

	for _, item := range childrenOf(props(ends, "interfaces")) {
		if truthy(item.value) && !interfaceIDs.has(item.value) {
			res.add(ruleInterfacesDefined, diag.INT014, item.path, fmt.Sprintf("'%s' does not refer to the unique-id of an existing interface.", jsString(item.value)))
		}
	}

	nodes := selectPath(root, keyNodes, "*")

	for _, end := range ends {
		interfaceOnNode(res, ruleInterfacesOnNode, nodes, end)
	}
}

// desiredInterfaces collects the interfaces and interface values of a connects end, dropping duplicates.
func desiredInterfaces(input obj) []any {
	var desired []any

	seen := valueSet{}
	addDesired := func(v any) {
		if seen.has(v) {
			return
		}

		seen.add(v)

		desired = append(desired, v)
	}

	list, _ := input["interfaces"].([]any)
	for _, v := range list {
		addDesired(v)
	}

	if single, ok := input["interface"].(string); ok {
		addDesired(single)
	}

	return desired
}

// findNodeByUniqueID returns the first node whose unique-id loosely equals nodeID, or nil.
func findNodeByUniqueID(nodes []match, nodeID string) *match {
	for i := range nodes {
		if nodeHasUniqueID(nodes[i], nodeID) {
			return &nodes[i]
		}
	}

	return nil
}

func nodeHasUniqueID(node match, nodeID string) bool {
	n, ok := node.value.(obj)
	if !ok {
		return false
	}

	uid, has := n["unique-id"]

	return has && looseEqualsString(uid, nodeID)
}

// interfaceOnNode ports interface-id-exists-on-node.ts for one connects end.
func interfaceOnNode(res *findings, rule string, nodes []match, end match) {
	input, isObj := end.value.(obj)
	if !isObj {
		return
	}

	desired := desiredInterfaces(input)
	if len(desired) == 0 {
		return
	}

	if !truthy(input["node"]) {
		res.add(rule, diag.INT014, end.path, "Invalid connects relationship - no node defined.")

		return
	}

	nodeID := jsString(input["node"])

	node := findNodeByUniqueID(nodes, nodeID)
	if node == nil {
		return
	}

	for _, message := range missingInterfaceMessages(*node, desired, nodeID) {
		res.add(rule, diag.INT014, end.path, message)
	}
}

// missingInterfaceMessages gives the messages for the desired interfaces that
// the node of a connects end does not define.
func missingInterfaceMessages(node match, desired []any, nodeID string) []string {
	nodeInterfaces := selectPath(node, "interfaces", "*", "unique-id")
	if len(nodeInterfaces) == 0 {
		return []string{fmt.Sprintf("Node with unique-id %s has no interfaces defined, expected interfaces [%s].",
			nodeID, strings.Join(interfaceTexts(desired), ","))}
	}

	have := newValueSet(nodeInterfaces)

	var messages []string

	for _, v := range desired {
		if !have.has(v) {
			messages = append(messages, fmt.Sprintf("Referenced interface with ID '%s' was not defined on the node with ID '%s'.",
				jsString(v), nodeID))
		}
	}

	return messages
}

// interfaceTexts converts each interface value as JavaScript string conversion does.
func interfaceTexts(values []any) []string {
	texts := make([]string, len(values))
	for i, v := range values {
		texts[i] = jsString(v)
	}

	return texts
}

func checkNodesReferenced(res *findings, root match) {
	labels := relationshipLabels(root)

	for _, id := range selectPath(root, keyNodes, "*", "unique-id") {
		if !labels.has(id.value) {
			res.add(ruleNodesReferenced, diag.INT015, id.path, fmt.Sprintf("Node with ID '%s' is not referenced by any relationships.", jsString(id.value)))
		}
	}
}

// relationshipLabels collects every string inside the relationship types.
func relationshipLabels(root match) valueSet {
	labels := valueSet{}

	for _, relType := range selectPath(root, keyRelationships, "*", "relationship-type") {
		for _, m := range descendants(relType) {
			if _, ok := m.value.(string); ok {
				labels.add(m.value)
			}
		}
	}

	return labels
}

// detectDuplicates ports detectDuplicates of helper-functions.ts.
func detectDuplicates(res *findings, rule, code string, matches []match, seen valueSet) {
	for _, entry := range matches {
		if !seen.has(entry.value) {
			seen.add(entry.value)

			continue
		}

		text := jsString(entry.value)
		res.record(&finding{
			rule:    rule,
			code:    code,
			path:    entry.path,
			message: fmt.Sprintf("Duplicate unique-id detected. ID: %s, path: %s", text, pointerOf(entry.path)),
			params:  duplicateIDParams(code, text),
		})
	}
}

// duplicateIDParams gives the diagnostic parameters of a duplicate unique-id:
// only CO-INT-004 carries the id.
func duplicateIDParams(code, text string) diag.Params {
	if code == diag.INT004 {
		return diag.Params{"id": text}
	}

	return nil
}

func checkUniqueIDs(res *findings, root match) {
	seen := valueSet{}

	detectDuplicates(res, ruleUniqueIDs, diag.INT004, selectPath(root, keyNodes, "*", "unique-id"), seen)
	detectDuplicates(res, ruleUniqueIDs, diag.INT004, selectPath(root, keyRelationships, "*", "unique-id"), seen)
	detectDuplicates(res, ruleUniqueIDs, diag.INT004, selectPath(root, keyNodes, "*", "interfaces", "*", "unique-id"), seen)
}

func checkFlows(res *findings, root match) {
	transitions := selectPath(root, "flows", "*", "transitions")

	checkFlowRelationships(res, root, transitions)
	checkFlowSequenceNumbers(res, transitions)
}

func checkFlowRelationships(res *findings, root match, transitions []match) {
	relIDs := newValueSet(selectPath(root, keyRelationships, "*", "unique-id"))

	for _, t := range childrenOf(transitions) {
		target := lintTarget(t, "relationship-unique-id")
		if truthy(target.value) && !relIDs.has(target.value) {
			res.add(ruleFlowRelationshipExists, diag.INT014, target.path, fmt.Sprintf("'%s' does not refer to the unique-id of an existing relationship.", jsString(target.value)))
		}
	}
}

func checkFlowSequenceNumbers(res *findings, transitions []match) {
	for _, list := range transitions {
		if truthy(list.value) {
			checkSequenceNumbersOfList(res, list)
		}
	}
}

func checkSequenceNumbersOfList(res *findings, list match) {
	seen := valueSet{}

	for _, n := range props(children(list), "sequence-number") {
		if !seen.has(n.value) {
			seen.add(n.value)

			continue
		}

		res.add(ruleFlowSequenceUnique, diag.INT014, list.path, fmt.Sprintf("Duplicate sequence-number %s detected. path: %s", jsString(n.value), pointerOf(n.path[len(list.path):])))
	}
}
