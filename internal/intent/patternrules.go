package intent

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/inclaro/concordia/internal/diag"
)

// Rule IDs of the CALM CLI pattern ruleset (shared/src/spectral/rules-pattern.ts).
const (
	rulePatternHasNodesRelationships  = "pattern-has-nodes-relationships"
	rulePatternNoEmpty                = "pattern-has-no-empty-properties"
	rulePatternNumerical              = "pattern-has-no-placeholder-properties-numerical"
	rulePatternString                 = "pattern-has-no-placeholder-properties-string"
	rulePatternConnectsNodes          = "connects-relationship-references-existing-nodes-in-pattern"
	rulePatternGroupNodes             = "group-relationship-references-existing-nodes-in-pattern"
	rulePatternGroupConstNodes        = "group-relationship-with-const-nodes-references-existing-nodes-in-pattern"
	rulePatternAvoidBoolean           = "avoid-boolean-properties"
	rulePatternRelationshipNodes      = "relationship-references-existing-nodes-in-pattern"
	rulePatternInterfacesDefined      = "referenced-interfaces-defined-in-pattern"
	rulePatternInterfacesOnNode       = "referenced-interfaces-defined-on-correct-node-in-pattern"
	rulePatternNodesReferenced        = "pattern-nodes-must-be-referenced"
	rulePatternUniqueIDs              = "unique-ids-must-be-unique-in-pattern"
	rulePatternDecisionNodesInChoice  = "nodes-referenced-in-pattern-decision-must-be-in-oneof-or-anyof-block"
	rulePatternDecisionRelsInChoice   = "relationships-referenced-in-pattern-decision-must-be-in-oneof-or-anyof-block"
	rulePatternOptionOneKeyword       = "pattern-option-relationship-must-only-have-oneof-or-anyof-items"
	rulePatternChoiceOneKeyword       = "pattern-choice-must-declare-one-keyword"
	rulePatternItemsFit               = "pattern-items-must-fit-within-max-items"
	rulePatternDecisionInPrefixItems  = "pattern-decision-must-be-declared-in-prefix-items"
	rulePatternOptionsInPrefixItems   = "pattern-decision-options-must-be-declared-in-prefix-items"
	rulePatternOptionMaxOneItem       = "pattern-option-relationship-must-have-max-one-item"
	patternMessageNoEmpty             = "All properties must be set to a nonempty, nonzero value."
	patternMessageNumerical           = "Numerical placeholder (-1) detected in architecture."
	patternMessageStringPlaceholder   = "String placeholder detected in architecture."
	patternMessageBoolean             = "Boolean property detected. Booleans should ideally be avoided as they are closed for extension; have you considered using an enum instead?"
	patternMessageTopLevel            = "Should have nodes and relationships as top level properties on the CALM document"
	patternMessageBothKeywords        = "A choice declares both 'oneOf' and 'anyOf'. An element must satisfy both, so some alternatives can never be selected. Declare one keyword."
	patternMessageOptionsInItems      = "Declare decision options in options prefixItems. calm generate reads prefixItems only, so an options block declared in items is never built."
	patternIDPath                     = "properties.unique-id.const"
	patternProperties                 = "properties"
	patternOptionsPath                = "properties.relationship-type.properties.options"
	patternMaxOneItem                 = 1
	patternPlaceholderStringRegexText = `^\[\[` + jsSpaceClass + `*[A-Z_]+` + jsSpaceClass + `*\]\]$`
)

var (
	patternPlaceholderString = regexp.MustCompile(patternPlaceholderStringRegexText)

	// patternDeclaration and patternEntryAlternative are DECLARATION and
	// ENTRY_ALTERNATIVE of declaration-paths.ts.
	patternDeclaration      = regexp.MustCompile(`^/properties/(?:nodes|relationships)/(?:prefixItems/\d+(?:/(?:oneOf|anyOf)/\d+)?|items/(?:oneOf|anyOf)/\d+)`)
	patternEntryAlternative = regexp.MustCompile(`^(/properties/(?:nodes|relationships)/prefixItems/\d+)/(?:oneOf|anyOf)/\d+$`)
	patternDigits           = regexp.MustCompile(`\d+`)
)

// patternRules ports the CALM CLI pattern ruleset. Like the CALM CLI, it lints
// the pattern in the form of stripRefs. The findings paths are relative to
// the pattern document.
func patternRules(pattern any) *findings {
	doc := stripRefs(pattern)
	results := newFindings(doc)
	root := match{path: []string{}, value: doc}

	patternCheckTopLevel(results, root)
	patternCheckValues(results, root)
	patternCheckNodeReferences(results, root)
	patternCheckBoolean(results, root)
	patternCheckInterfaces(results, root)
	patternCheckNodesReferenced(results, root)
	patternCheckUniqueIDs(results, root)
	patternCheckDecisionReferences(results, root)
	patternCheckOptions(results, root)
	patternCheckChoiceKeywords(results, root)
	patternCheckItemsFit(results, root)
	patternCheckDecisionDeclaration(results, root)

	return results
}

// patternRecursive is the JSONPath `..key` selection: the member `key` of the
// root and of every value below it.
func patternRecursive(root match, key string) []match {
	out := []match{}

	if p, ok := prop(root, key); ok {
		out = append(out, p)
	}

	for _, m := range descendants(root) {
		if p, ok := prop(m, key); ok {
			out = append(out, p)
		}
	}

	return out
}

// patternDescendantsOrSelf is the JSONPath `..` selection before a name.
func patternDescendantsOrSelf(m match) []match {
	return append([]match{m}, descendants(m)...)
}

// patternRelationshipTypes is the JSONPath `$..relationship-type` selection.
func patternRelationshipTypes(root match) []match {
	return patternRecursive(root, "relationship-type")
}

func patternCheckTopLevel(f *findings, root match) {
	for _, properties := range selectPath(root, patternProperties) {
		for _, field := range []string{keyNodes, keyRelationships} {
			target := lintTarget(properties, field)
			if !truthy(target.value) {
				f.add(rulePatternHasNodesRelationships, diag.INT014, target.path, patternMessageTopLevel)
			}
		}
	}
}

// patternCheckValues ports the three `$..*` rules on single values.
func patternCheckValues(results *findings, root match) {
	for _, m := range descendants(root) {
		if !truthy(m.value) {
			results.add(rulePatternNoEmpty, diag.INT014, m.path, patternMessageNoEmpty)
		}

		if looseEqualsMinusOne(m.value) {
			results.add(rulePatternNumerical, diag.INT015, m.path, patternMessageNumerical)
		}

		if text, ok := m.value.(string); ok && patternPlaceholderString.MatchString(text) {
			results.add(rulePatternString, diag.INT015, m.path, patternMessageStringPlaceholder)
		}
	}
}

// patternSites gives the steps of the JSONPath declarationPaths(calmType) of
// declaration-paths.ts, in its order.
func patternSites(calmType string) [][]string {
	return [][]string{
		{patternProperties, calmType, keyPrefixItems, "*"},
		{patternProperties, calmType, keyPrefixItems, "*", keyOneOf, "*"},
		{patternProperties, calmType, keyPrefixItems, "*", keyAnyOf, "*"},
		{patternProperties, calmType, "items", keyOneOf, "*"},
		{patternProperties, calmType, "items", keyAnyOf, "*"},
	}
}

func patternIDSteps() []string { return []string{patternProperties, "unique-id", "const"} }

// patternDeclaredIDs is the flatMap of declaredIdPaths(calmType) selections.
func patternDeclaredIDs(root match, calmType string) []match {
	out := make([]match, 0, len(patternSites(calmType)))

	for _, site := range patternSites(calmType) {
		out = append(out, selectPath(root, append(append([]string{}, site...), patternIDSteps()...)...)...)
	}

	return out
}

// patternGet is lodash `get` on an object path: found is false when a step is
// missing or the value before it is not an object.
func patternGet(value any, keys ...string) (any, bool) {
	for _, key := range keys {
		members, ok := value.(obj)
		if !ok {
			return nil, false
		}

		if value, ok = members[key]; !ok {
			return nil, false
		}
	}

	return value, true
}

// patternDeclaresOptions is declaresOptions of declaration-paths.ts.
func patternDeclaresOptions(relationship any) bool {
	_, found := patternGet(relationship, strings.Split(patternOptionsPath, ".")...)

	return found
}

// patternDeclaredID is declaredId of declaration-paths.ts.
func patternDeclaredID(declaration any) (any, bool) {
	return patternGet(declaration, strings.Split(patternIDPath, ".")...)
}

// patternNodeIDExists ports node-id-exists.ts.
func patternNodeIDExists(results *findings, rule string, root, target match) {
	id, ok := target.value.(string)
	if !ok || id == "" {
		return
	}

	if newValueSet(patternDeclaredIDs(root, keyNodes)).has(id) {
		return
	}

	results.add(rule, diag.INT014, target.path, fmt.Sprintf("'%s' does not refer to the unique-id of an existing node.", id))
}

func patternCheckNodeReferences(results *findings, root match) {
	types := patternRelationshipTypes(root)

	patternCheckConnectsNodes(results, root)
	patternCheckGroupNodes(results, root, types)
	patternCheckRelationshipEnds(results, root, types)
}

// patternCheckConnectsNodes ports connects-relationship-references-existing-nodes-in-pattern:
// $..connects.source.node.
func patternCheckConnectsNodes(results *findings, root match) {
	for _, connects := range patternRecursive(root, "connects") {
		for _, source := range props([]match{connects}, "source") {
			for _, node := range props([]match{source}, "node") {
				patternNodeIDExists(results, rulePatternConnectsNodes, root, node)
			}
		}
	}
}

// patternCheckGroupNodes ports the two group rules:
// group-relationship-references-existing-nodes-in-pattern: $..relationship-type..nodes[*]
// group-relationship-with-const-nodes-...: $..relationship-type..nodes.const[*].
func patternCheckGroupNodes(results *findings, root match, types []match) {
	for _, relType := range types {
		for _, candidate := range patternDescendantsOrSelf(relType) {
			nodes, ok := prop(candidate, keyNodes)
			if !ok {
				continue
			}

			for _, node := range children(nodes) {
				patternNodeIDExists(results, rulePatternGroupNodes, root, node)
			}

			for _, consts := range props([]match{nodes}, "const") {
				for _, node := range children(consts) {
					patternNodeIDExists(results, rulePatternGroupConstNodes, root, node)
				}
			}
		}
	}
}

// patternCheckRelationshipEnds ports relationship-references-existing-nodes-in-pattern:
// $..relationship-type.*.*.
func patternCheckRelationshipEnds(results *findings, root match, types []match) {
	for _, relType := range types {
		for _, item := range childrenOf(children(relType)) {
			for _, field := range []string{"actor", "container"} {
				target := lintTarget(item, field)
				patternNodeIDExists(results, rulePatternRelationshipNodes, root, target)
			}
		}
	}
}

// patternCheckBoolean ports avoid-boolean-properties: `$..type` with the
// spectral `pattern` function (notMatch /boolean/), which needs a string.
func patternCheckBoolean(f *findings, root match) {
	for _, m := range patternRecursive(root, "type") {
		if s, ok := m.value.(string); ok && strings.Contains(s, "boolean") {
			f.add(rulePatternAvoidBoolean, diag.INT015, m.path, patternMessageBoolean)
		}
	}
}

func patternCheckInterfaces(results *findings, root match) {
	interfaceIDs := patternDefinedInterfaceIDs(root)

	for _, relType := range patternRelationshipTypes(root) {
		ends := selectPath(relType, "const", "connects", "*")

		patternCheckInterfaceRefs(results, interfaceIDs, ends)

		for _, end := range ends {
			patternInterfaceOnNode(results, root, end)
		}
	}
}

// patternDefinedInterfaceIDs collects the interface unique-id consts that the pattern declares.
func patternDefinedInterfaceIDs(root match) valueSet {
	interfaceIDs := valueSet{}

	for _, ifaces := range patternRecursive(root, "interfaces") {
		for _, id := range selectPath(ifaces, keyPrefixItems, "*", patternProperties, "unique-id", "const") {
			interfaceIDs.add(id.value)
		}
	}

	return interfaceIDs
}

// patternCheckInterfaceRefs ports referenced-interfaces-defined-in-pattern.
func patternCheckInterfaceRefs(results *findings, interfaceIDs valueSet, ends []match) {
	for _, item := range childrenOf(props(ends, "interfaces")) {
		if truthy(item.value) && !interfaceIDs.has(item.value) {
			results.add(rulePatternInterfacesDefined, diag.INT014, item.path, fmt.Sprintf("'%s' does not refer to the unique-id of an existing interface.", jsString(item.value)))
		}
	}
}

// patternFindNodeDeclaration gives the first node declaration of the pattern
// whose unique-id const is the same value as nodeID.
func patternFindNodeDeclaration(root match, nodeID any) (match, bool) {
	wanted, wantedOK := valueKey(nodeID)
	if !wantedOK {
		return match{}, false
	}

	for _, site := range patternSites(keyNodes) {
		for _, declaration := range selectPath(root, site...) {
			id, found := patternDeclaredID(declaration.value)
			if !found {
				continue
			}

			if declared, declaredOK := valueKey(id); declaredOK && declared == wanted {
				return declaration, true
			}
		}
	}

	return match{}, false
}

// patternInterfaceOnNode ports interface-id-exists-on-node.ts.
func patternInterfaceOnNode(results *findings, root, end match) {
	input, ok := end.value.(obj)
	if !ok || !truthy(input["interfaces"]) {
		return
	}

	if !truthy(input["node"]) {
		results.add(rulePatternInterfacesOnNode, diag.INT014, end.path, "Invalid connects relationship - no node defined.")

		return
	}

	if node, found := patternFindNodeDeclaration(root, input["node"]); found {
		patternCheckNodeInterfaces(results, end, input, node)
	}
}

// patternCheckNodeInterfaces checks the interfaces of a connects end against
// the unique-id consts of the interfaces that its node declaration defines.
func patternCheckNodeInterfaces(results *findings, end match, input obj, node match) {
	nodeID := jsString(input["node"])
	have := selectPath(node, patternProperties, "interfaces", keyPrefixItems, "*", patternProperties, "unique-id", "const")

	if len(have) == 0 {
		results.add(rulePatternInterfacesOnNode, diag.INT014, end.path,
			fmt.Sprintf("Node with unique-id %s has no interfaces defined, expected interfaces [%s]", nodeID, jsString(input["interfaces"])))

		return
	}

	// lodash difference: a first argument that is not an array gives no result.
	desired, isArray := input["interfaces"].([]any)
	if !isArray {
		return
	}

	haveSet := newValueSet(have)

	for _, want := range desired {
		if !haveSet.has(want) {
			results.add(rulePatternInterfacesOnNode, diag.INT014, end.path,
				fmt.Sprintf("Referenced interface with ID '%s' was not defined on the node with ID '%s'.", jsString(want), nodeID))
		}
	}
}

func patternCheckNodesReferenced(results *findings, root match) {
	referenced := patternReferencedStrings(root)

	for _, id := range patternDeclaredIDs(root, keyNodes) {
		if !referenced.has(id.value) {
			results.add(rulePatternNodesReferenced, diag.INT015, id.path, fmt.Sprintf("Node with ID '%s' is not referenced by any relationships.", jsString(id.value)))
		}
	}
}

// patternReferencedStrings collects the string values below the relationship types.
func patternReferencedStrings(root match) valueSet {
	referenced := valueSet{}

	for _, relType := range patternRelationshipTypes(root) {
		for _, m := range descendants(relType) {
			if _, isString := m.value.(string); isString {
				referenced.add(m.value)
			}
		}
	}

	return referenced
}

func patternPointer(m match) string { return pointerOf(m.path) }

func patternContainingDeclaration(pointer string) string {
	if d := patternDeclaration.FindString(pointer); d != "" {
		return d
	}

	return pointer
}

func patternExclusiveGroup(pointer string) string {
	declaration := patternContainingDeclaration(pointer)
	if m := patternEntryAlternative.FindStringSubmatch(declaration); m != nil {
		return m[1]
	}

	return declaration
}

func patternIsAlternative(pointer string) bool {
	return patternEntryAlternative.MatchString(patternContainingDeclaration(pointer))
}

func patternDeclarationIndices(pointer string) []int {
	declaration := patternContainingDeclaration(pointer)
	first := 0

	if strings.Contains(declaration, "/items/") {
		first = 1
	}

	digits := patternDigits.FindAllString(declaration, -1)
	out := make([]int, 0, len(digits)+1)
	out = append(out, first)

	for _, d := range digits {
		n, _ := strconv.Atoi(d)
		out = append(out, n)
	}

	return out
}

// patternByBuildOrder is byBuildOrder of declaration-paths.ts.
func patternByBuildOrder(left, right string) int {
	a, b := patternDeclarationIndices(left), patternDeclarationIndices(right)

	for depth := range max(len(a), len(b)) {
		x, y := -1, -1
		if depth < len(a) {
			x = a[depth]
		}

		if depth < len(b) {
			y = b[depth]
		}

		if x != y {
			return x - y
		}
	}

	return 0
}

// patternInBuildOrder is a stable sort of the matches by byBuildOrder.
func patternInBuildOrder(matches []match) []match {
	out := append([]match{}, matches...)
	sort.SliceStable(out, func(i, j int) bool {
		return patternByBuildOrder(patternPointer(out[i]), patternPointer(out[j])) < 0
	})

	return out
}

// patternGroupMatches is lodash groupBy followed by Object.values: the groups in
// the order of their first match.
func patternGroupMatches(matches []match, key func(pointer string) string) [][]match {
	var groups [][]match

	index := map[string]int{}

	for _, entry := range matches {
		k := key(patternPointer(entry))
		i, ok := index[k]

		if !ok {
			i = len(groups)
			index[k] = i

			groups = append(groups, nil)
		}

		groups[i] = append(groups[i], entry)
	}

	return groups
}

// patternCheckUniqueIDs ports ids-are-unique.ts.
func patternCheckUniqueIDs(results *findings, root match) {
	nodeIDs := patternInBuildOrder(patternDeclaredIDs(root, keyNodes))
	relationshipIDs := patternInBuildOrder(patternDeclaredIDs(root, keyRelationships))
	interfaceIDs := patternInBuildOrder(patternInterfaceIDMatches(root))

	seen := valueSet{}

	detectDuplicates(results, rulePatternUniqueIDs, diag.INT014, nodeIDs, seen)
	detectDuplicates(results, rulePatternUniqueIDs, diag.INT014, relationshipIDs, seen)

	for _, group := range patternGroupMatches(interfaceIDs, patternExclusiveGroup) {
		patternCheckExclusiveGroup(results, group, seen)
	}
}

// patternInterfaceIDMatches selects the interface unique-id consts of every node declaration.
func patternInterfaceIDMatches(root match) []match {
	sites := patternSites(keyNodes)
	interfaceIDs := make([]match, 0, len(sites))

	for _, site := range sites {
		steps := append(append([]string{}, site...), patternProperties, "interfaces", keyPrefixItems, "*")
		interfaceIDs = append(interfaceIDs, selectPath(root, append(steps, patternIDSteps()...)...)...)
	}

	return interfaceIDs
}

// patternCheckExclusiveGroup looks for duplicate IDs in a group of interface IDs. The
// choices of the group exclude each other, so each one is checked on its own copy of seen.
func patternCheckExclusiveGroup(results *findings, group []match, seen valueSet) {
	choices, fixed := patternSplitAlternatives(group)

	detectDuplicates(results, rulePatternUniqueIDs, diag.INT014, fixed, seen)

	for _, choice := range patternGroupMatches(choices, patternContainingDeclaration) {
		detectDuplicates(results, rulePatternUniqueIDs, diag.INT014, choice, maps.Clone(seen))
	}

	for _, m := range choices {
		seen.add(m.value)
	}
}

// patternSplitAlternatives splits matches into those in a oneOf or anyOf alternative and the others.
func patternSplitAlternatives(group []match) (choices, fixed []match) {
	for _, m := range group {
		if patternIsAlternative(patternPointer(m)) {
			choices = append(choices, m)
		} else {
			fixed = append(fixed, m)
		}
	}

	return choices, fixed
}

// patternCheckDecisionReferences ports is-defined-in-oneof-or-anyof.ts and its two rules.
func patternCheckDecisionReferences(results *findings, root match) {
	for _, calmType := range []string{keyNodes, keyRelationships} {
		rule := rulePatternDecisionNodesInChoice
		if calmType == keyRelationships {
			rule = rulePatternDecisionRelsInChoice
		}

		fixed, inChoices := patternDecisionIDSets(root, calmType)

		for _, target := range patternOptionTargets(root, calmType) {
			id, isString := target.value.(string)
			if !isString || id == "" {
				continue
			}

			if fixed.has(id) && !inChoices.has(id) {
				results.add(rule, diag.INT014, target.path, fmt.Sprintf("'%s' is part of a pattern option and must be defined in a oneOf or anyOf block.", id))
			}
		}
	}
}

// patternDecisionIDSets gives the ids declared in prefixItems entries and the
// ids declared in oneOf or anyOf alternatives, of nodes or relationships.
func patternDecisionIDSets(root match, calmType string) (fixed, inChoices valueSet) {
	sites := patternSites(calmType)
	fixed = newValueSet(selectPath(root, append(append([]string{}, sites[0]...), patternIDSteps()...)...))
	inChoices = valueSet{}

	for _, site := range sites[1:] {
		for _, id := range selectPath(root, append(append([]string{}, site...), patternIDSteps()...)...) {
			inChoices.add(id.value)
		}
	}

	return fixed, inChoices
}

// patternOptionTargets gives the values of `const` members of nodes or
// relationships below the options prefixItems of each relationship type.
func patternOptionTargets(root match, calmType string) []match {
	var targets []match

	for _, relType := range patternRelationshipTypes(root) {
		for _, item := range selectPath(relType, patternProperties, "options", keyPrefixItems, "*") {
			targets = append(targets, patternItemConsts(item, calmType)...)
		}
	}

	return targets
}

// patternItemConsts gives the children of the `const` members of nodes or
// relationships at or below an options item.
func patternItemConsts(item match, calmType string) []match {
	var targets []match

	for _, candidate := range patternDescendantsOrSelf(item) {
		holder, found := prop(candidate, calmType)
		if !found {
			continue
		}

		for _, consts := range props([]match{holder}, "const") {
			targets = append(targets, children(consts)...)
		}
	}

	return targets
}

// patternJSLength is the `length` of an array or a string, as JavaScript counts it.
func patternJSLength(value any) (int, bool) {
	switch typed := value.(type) {
	case []any:
		return len(typed), true
	case string:
		return len(utf16.Encode([]rune(typed))), true
	}

	return 0, false
}

// patternJSNumber is the JavaScript number conversion of a JSON value.
func patternJSNumber(value any) float64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case float64:
		return typed
	case bool:
		if typed {
			return 1
		}

		return 0
	case string:
		return jsNumber(typed)
	case []any:
		return jsNumber(jsString(typed))
	}

	return math.NaN()
}

// patternCheckOptions ports the rules on `relationship-type.properties.options`.
func patternCheckOptions(results *findings, root match) {
	for _, relType := range patternRelationshipTypes(root) {
		patternCheckOptionKeywords(results, relType)
		patternCheckOptionsInItems(results, relType)
		patternCheckOptionMaxOneItem(results, relType)
	}
}

// patternCheckOptionKeywords ports pattern-option-relationship-must-only-have-oneof-or-anyof-items:
// spectral xor on an object.
func patternCheckOptionKeywords(results *findings, relType match) {
	for _, item := range selectPath(relType, patternProperties, "options", keyPrefixItems, "*") {
		fields, ok := item.value.(obj)
		if !ok {
			continue
		}

		var present []string

		for _, keyword := range []string{keyOneOf, keyAnyOf} {
			if _, has := fields[keyword]; has {
				present = append(present, keyword)
			}
		}

		switch {
		case len(present) == 0:
			results.add(rulePatternOptionOneKeyword, diag.INT014, item.path, `At least one of "oneOf" or "anyOf" must be defined`)
		case len(present) > 1:
			results.add(rulePatternOptionOneKeyword, diag.INT014, item.path, `Just one of "`+strings.Join(present, `" and "`)+`" must be defined`)
		}
	}
}

// patternCheckOptionsInItems ports pattern-decision-options-must-be-declared-in-prefix-items:
// `items` falsy.
func patternCheckOptionsInItems(results *findings, relType match) {
	for _, options := range selectPath(relType, patternProperties, "options") {
		target := lintTarget(options, "items")
		if truthy(target.value) {
			results.add(rulePatternOptionsInPrefixItems, diag.INT014, target.path, patternMessageOptionsInItems)
		}
	}
}

// patternCheckOptionMaxOneItem ports pattern-option-relationship-must-have-max-one-item:
// spectral length, max 1.
func patternCheckOptionMaxOneItem(results *findings, relType match) {
	for _, items := range selectPath(relType, patternProperties, "options", keyPrefixItems) {
		var size float64

		switch typed := items.value.(type) {
		case obj:
			size = float64(len(typed))
		case []any, string:
			length, _ := patternJSLength(typed)
			size = float64(length)
		case float64:
			size = typed
		default:
			continue
		}

		if size > patternMaxOneItem {
			name := items.path[len(items.path)-1]
			results.add(rulePatternOptionMaxOneItem, diag.INT014, items.path, fmt.Sprintf("%s property must be shorter than %d", doubleQuoted(name), patternMaxOneItem))
		}
	}
}

// patternCheckChoiceKeywords ports pattern-choice-must-declare-one-keyword
// (twoKeywordSites of declaration-paths.ts with spectral falsy).
func patternCheckChoiceKeywords(results *findings, root match) {
	for _, calmType := range []string{keyNodes, keyRelationships} {
		for _, entry := range selectPath(root, patternProperties, calmType, keyPrefixItems, "*") {
			patternReportBothKeywords(results, entry)
		}

		for _, holder := range selectPath(root, patternProperties, calmType) {
			if items, found := prop(holder, "items"); found {
				patternReportBothKeywords(results, items)
			}
		}
	}
}

// patternReportBothKeywords reports a choice that declares both oneOf and anyOf.
func patternReportBothKeywords(results *findings, entry match) {
	fields, isObj := entry.value.(obj)
	if !isObj {
		return
	}

	if truthy(fields[keyOneOf]) && truthy(fields[keyAnyOf]) {
		results.add(rulePatternChoiceOneKeyword, diag.INT014, entry.path, patternMessageBothKeywords)
	}
}

// patternCheckItemsFit ports items-fit-within-max-items.ts.
func patternCheckItemsFit(results *findings, root match) {
	for _, calmType := range []string{keyNodes, keyRelationships} {
		for _, target := range selectPath(root, patternProperties, calmType) {
			array, ok := target.value.(obj)
			if !ok {
				continue
			}

			if message, full := patternItemsFitMessage(array); full {
				results.add(rulePatternItemsFit, diag.INT014, target.path, message)
			}
		}
	}
}

// patternItemsFitMessage gives the message for an array schema that offers a
// choice in items, while maxItems is already filled by prefixItems.
func patternItemsFitMessage(array obj) (message string, full bool) {
	maxItems, hasMax := array["maxItems"]
	if !hasMax || !patternOffersChoice(array) {
		return "", false
	}

	positions, _ := patternJSLength(array[keyPrefixItems])
	if patternJSNumber(maxItems) > float64(positions) {
		return "", false
	}

	return fmt.Sprintf("maxItems is %s and %d prefixItems entries already fill it, so no items member can ever be built. Raise maxItems or remove items.",
		jsString(maxItems), positions), true
}

// patternOffersChoice tells whether the items of an array schema have a
// non-empty oneOf or anyOf.
func patternOffersChoice(array obj) bool {
	items, isObj := array["items"].(obj)
	if !isObj {
		return false
	}

	offersChoice := false

	for _, keyword := range []string{keyOneOf, keyAnyOf} {
		if length, hasLen := patternJSLength(items[keyword]); hasLen && length > 0 {
			offersChoice = true
		}
	}

	return offersChoice
}

// patternCheckDecisionDeclaration ports decision-is-declared-in-prefix-items.ts.
func patternCheckDecisionDeclaration(results *findings, root match) {
	for _, keyword := range []string{keyOneOf, keyAnyOf} {
		for _, relationship := range selectPath(root, patternProperties, keyRelationships, "items", keyword, "*") {
			if !truthy(relationship.value) || !patternDeclaresOptions(relationship.value) {
				continue
			}

			id := "unknown"
			if declared, found := patternDeclaredID(relationship.value); found && declared != nil {
				id = jsString(declared)
			}

			results.add(rulePatternDecisionInPrefixItems, diag.INT014, relationship.path, fmt.Sprintf("The decision '%s' is declared in items. Declare a decision in relationships prefixItems, so that an answer can decline it.", id))
		}
	}
}
