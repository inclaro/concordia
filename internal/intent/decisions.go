package intent

import (
	"errors"
	"fmt"
	"strconv"
	"unicode/utf16"

	"github.com/inclaro/concordia/internal/diag"
)

// This file ports the decision handling of the CALM CLI: applyArchitectureOptionsToPattern,
// unansweredOneOfDecisions, and extractChoicesFromArchitecture (validation-helpers.ts),
// and selectChoices and extractOptions (generate/components/options.ts).
//
// The upstream code reads a pattern with chains of property accesses and throws
// a TypeError where a link is missing. The port keeps the access chains as they
// are (decisionGet) and turns the TypeError into errDecisionTypeError. The
// functions with the suffix Checked give that error; applyArchitectureOptionsToPattern
// and unansweredOneOfDecisions keep their plain signatures and give the
// unchanged pattern, or no finding, for it.
//
// A JSON null and a missing member are not told apart.

// errDecisionTypeError stands for a TypeError of the upstream code.
var errDecisionTypeError = errors.New("TypeError: the pattern or architecture does not have the shape that the CALM CLI reads")

type decisionPanic struct{}

// decisionRecover turns a decisionPanic into errDecisionTypeError.
func decisionRecover(err *error) { //nolint:gocritic // a deferred call sets the named error result through the pointer.
	if r := recover(); r != nil {
		if _, ok := r.(decisionPanic); !ok {
			panic(r)
		}

		*err = errDecisionTypeError
	}
}

// decisionGet is the property access `v[key]`. It panics where JavaScript throws
// a TypeError: on null.
func decisionGet(value any, key string) any {
	switch typed := value.(type) {
	case nil:
		panic(decisionPanic{})
	case obj:
		return typed[key]
	case []any:
		return decisionGetElement(typed, key)
	case string:
		return decisionGetLength(typed, key)
	}

	return nil
}

// decisionGetElement is `array[key]`: length, or a canonical index in range.
func decisionGetElement(list []any, key string) any {
	if key == "length" {
		return float64(len(list))
	}

	i, err := strconv.Atoi(key)
	if err != nil || strconv.Itoa(i) != key {
		return nil
	}

	if i < 0 || i >= len(list) {
		return nil
	}

	return list[i]
}

// decisionGetLength is `string[key]`: only the length is modelled.
func decisionGetLength(text, key string) any {
	if key == "length" {
		return float64(len(utf16.Encode([]rune(text))))
	}

	return nil
}

// decisionOptional is the optional access `v?.[key]`.
func decisionOptional(v any, key string) any {
	if v == nil {
		return nil
	}

	return decisionGet(v, key)
}

// decisionChain follows `v[k1][k2]...`.
func decisionChain(v any, keys ...string) any {
	for _, k := range keys {
		v = decisionGet(v, k)
	}

	return v
}

// decisionSet is the assignment `v[key] = value` in strict mode: it panics on a
// primitive or null. An array drops the member.
func decisionSet(v any, key string, value any) {
	switch t := v.(type) {
	case obj:
		t[key] = value
	case []any:
	default:
		panic(decisionPanic{})
	}
}

// decisionClone is structuredClone for JSON values.
func decisionClone(value any) any {
	switch typed := value.(type) {
	case obj:
		out := make(obj, len(typed))
		for k, x := range typed {
			out[k] = decisionClone(x)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, x := range typed {
			out[i] = decisionClone(x)
		}

		return out
	}

	return value
}

// decisionChoice is CalmChoice. The values are the JSON values of the
// architecture.
type decisionChoice struct {
	description any
	nodes       any
	relations   any
}

type decisionOption struct {
	optionID   any
	optionType string
}

// decisionRelationships is relationshipsOf.
func decisionRelationships(architecture any) []any {
	o, ok := architecture.(obj)
	if !ok {
		return nil
	}

	list, _ := o[keyRelationships].([]any)

	return list
}

// decisionOptions gives the `options` array of a decision relationship.
func decisionOptions(rel any) ([]any, bool) {
	list, ok := decisionOptional(decisionOptional(rel, "relationship-type"), "options").([]any)

	return list, ok
}

// decisionIsDecision is isDecision.
func decisionIsDecision(rel any) bool {
	if _, ok := rel.(obj); !ok {
		return false
	}

	_, ok := decisionOptions(rel)

	return ok
}

// extractChoicesFromArchitecture ports extractChoicesFromArchitecture.
func extractChoicesFromArchitecture(architecture any) (choices []decisionChoice, err error) {
	defer decisionRecover(&err)

	for _, rel := range decisionRelationships(architecture) {
		if !decisionIsDecision(rel) {
			continue
		}

		options, _ := decisionOptions(rel)
		if len(options) == 0 {
			continue
		}

		first := options[0]
		choice := decisionChoice{description: decisionGet(first, "description"), nodes: decisionGet(first, keyNodes), relations: decisionGet(first, keyRelationships)}

		if !truthy(choice.nodes) {
			choice.nodes = []any{}
		}

		if !truthy(choice.relations) {
			choice.relations = []any{}
		}

		choices = append(choices, choice)
	}

	return choices, nil
}

// decisionAlternatives is alternativesOf: the value of the first of oneOf and
// anyOf that is truthy.
func decisionAlternatives(item any) (any, bool) {
	for _, name := range []string{keyOneOf, keyAnyOf} {
		if v := decisionOptional(item, name); truthy(v) {
			return v, true
		}
	}

	return nil, false
}

// decisionFlatten is flattenOneOfAndAnyOf.
func decisionFlatten(item any, selected func(x any) bool) []any {
	alternatives, ok := decisionAlternatives(item)
	if !ok {
		return []any{item}
	}

	list, isArray := alternatives.([]any)
	if !isArray {
		panic(decisionPanic{}) // .filter is not a function
	}

	out := []any{}

	for _, x := range list {
		if selected(x) {
			out = append(out, x)
		}
	}

	return out
}

// decisionFlatMapIDs is `choices.flatMap(choice => choice.<field>)`.
func decisionFlatMapIDs(values []any) valueSet {
	ids := valueSet{}

	for _, v := range values {
		if list, ok := v.([]any); ok {
			for _, x := range list {
				ids.add(x)
			}

			continue
		}

		ids.add(v)
	}

	return ids
}

// decisionSelectDeclarations is selectDeclarations.
func decisionSelectDeclarations(pattern any, calmType string, ids valueSet) {
	declarations := decisionChain(pattern, "properties", calmType)
	if !truthy(declarations) {
		return
	}

	selected := func(x any) bool {
		return ids.has(decisionChain(x, "properties", "unique-id", "const"))
	}

	items := decisionGet(declarations, "items")

	var catalogue []any

	if _, ok := decisionAlternatives(items); ok {
		catalogue = []any{items}
	}

	var entries []any

	switch t := decisionGet(declarations, keyPrefixItems).(type) {
	case nil:
	case []any:
		entries = append(entries, t...)
	case string:
		for _, r := range t {
			entries = append(entries, string(r))
		}
	default:
		panic(decisionPanic{}) // not iterable
	}

	entries = append(entries, catalogue...)

	flat := []any{}
	for _, item := range entries {
		flat = append(flat, decisionFlatten(item, selected)...)
	}

	decisionSet(declarations, keyPrefixItems, flat)
}

// decisionItemsOfOptions is getItemsInOptionsRelationship.
func decisionItemsOfOptions(relationship any) any {
	return decisionChain(relationship, "properties", "relationship-type", "properties", "options", keyPrefixItems)
}

// decisionFlattenOptionsRelationship is flattenOptionsRelationship.
func decisionFlattenOptionsRelationship(relationship any, choices []decisionChoice) any {
	if !patternDeclaresOptions(relationship) {
		return relationship
	}

	descriptions := valueSet{}
	for _, c := range choices {
		descriptions.add(c.description)
	}

	selected := func(x any) bool {
		return descriptions.has(decisionChain(x, "properties", "description", "const"))
	}

	items, ok := decisionItemsOfOptions(relationship).([]any)
	if !ok {
		panic(decisionPanic{}) // .flatMap is not a function
	}

	flat := []any{}
	for _, item := range items {
		flat = append(flat, decisionFlatten(item, selected)...)
	}

	decisionSet(decisionChain(relationship, "properties", "relationship-type", "properties", "options"), keyPrefixItems, flat)

	return relationship
}

// decisionFlattenOptionsRelationships is flattenOptionsRelationships.
func decisionFlattenOptionsRelationships(pattern any, choices []decisionChoice) {
	relationships := decisionChain(pattern, "properties", keyRelationships)

	prefixItems := decisionOptional(relationships, keyPrefixItems)
	if !truthy(prefixItems) {
		return
	}

	list, ok := prefixItems.([]any)
	if !ok {
		panic(decisionPanic{}) // .map is not a function
	}

	out := make([]any, len(list))
	for i, rel := range list {
		out[i] = decisionFlattenOptionsRelationship(rel, choices)
	}

	decisionSet(relationships, keyPrefixItems, out)
}

// selectChoices ports selectChoices: a narrowed deep copy of the pattern.
func selectChoices(inputPattern any, choices []decisionChoice) (pattern any, err error) {
	defer decisionRecover(&err)

	pattern = decisionClone(inputPattern)

	nodes := make([]any, 0, len(choices))
	relationships := make([]any, 0, len(choices))

	for _, c := range choices {
		nodes = append(nodes, c.nodes)
		relationships = append(relationships, c.relations)
	}

	decisionSelectDeclarations(pattern, keyNodes, decisionFlatMapIDs(nodes))
	decisionSelectDeclarations(pattern, keyRelationships, decisionFlatMapIDs(relationships))
	decisionFlattenOptionsRelationships(pattern, choices)

	return pattern, nil
}

// decisionDropEmptyPrefixItems is dropEmptyPrefixItems.
func decisionDropEmptyPrefixItems(schema any) {
	switch t := schema.(type) {
	case obj:
		if list, ok := t[keyPrefixItems].([]any); ok && len(list) == 0 {
			delete(t, keyPrefixItems)
		}

		for _, v := range t {
			decisionDropEmptyPrefixItems(v)
		}
	case []any:
		for _, v := range t {
			decisionDropEmptyPrefixItems(v)
		}
	}
}

// applyArchitectureOptionsToPattern ports applyArchitectureOptionsToPattern: the
// answers of the architecture narrow a deep copy of the pattern (the same pattern
// value when the architecture has no decision). The error stands for a TypeError
// of the CALM CLI.
func applyArchitectureOptionsToPattern(architecture, pattern any) (any, error) {
	decided := false

	for _, rel := range decisionRelationships(architecture) {
		if decisionIsDecision(rel) {
			decided = true

			break
		}
	}

	if !decided {
		return pattern, nil
	}

	choices, err := extractChoicesFromArchitecture(architecture)
	if err != nil {
		return nil, err
	}

	narrowed, err := selectChoices(pattern, choices)
	if err != nil {
		return nil, err
	}

	decisionDropEmptyPrefixItems(narrowed)

	return narrowed, nil
}

// decisionExtractOptions is extractOptions for the oneOf options.
func decisionExtractOptions(pattern any) (options []decisionOption, err error) {
	defer decisionRecover(&err)

	items := decisionRelationshipsPrefixItems(pattern)

	if n, ok := items.([]any); ok && len(n) == 0 {
		return nil, nil
	}

	list, ok := items.([]any)
	if !ok {
		panic(decisionPanic{}) // .filter is not a function
	}

	for _, rel := range list {
		if !patternDeclaresOptions(rel) {
			continue
		}

		for _, blockType := range []string{keyOneOf, keyAnyOf} {
			options = append(options, decisionBlockOptions(rel, blockType)...)
		}
	}

	return options, nil
}

// decisionBlockOptions gives the options of one block type (oneOf or anyOf) of a
// relationship that declares options.
func decisionBlockOptions(rel any, blockType string) []decisionOption {
	prefixItems, isArray := decisionItemsOfOptions(rel).([]any)
	if !isArray {
		panic(decisionPanic{})
	}

	var options []decisionOption

	for _, prefixItem := range prefixItems {
		if !decisionIn(blockType, prefixItem) {
			continue
		}

		choices, isList := decisionGet(prefixItem, blockType).([]any)
		if !isList {
			panic(decisionPanic{}) // .map is not a function
		}

		optionID := decisionChain(rel, "properties", "unique-id", "const")
		decisionChain(rel, "properties", "description", "const")

		for _, choice := range choices {
			decisionChain(choice, "properties", "description", "const")
			decisionChain(choice, "properties", keyNodes, "const")
			decisionChain(choice, "properties", keyRelationships, "const")
		}

		options = append(options, decisionOption{optionType: blockType, optionID: optionID})
	}

	return options
}

// decisionIn is the JavaScript `key in value`: it throws for a primitive.
func decisionIn(key string, v any) bool {
	switch t := v.(type) {
	case obj:
		_, ok := t[key]

		return ok
	case []any:
		return false
	}

	panic(decisionPanic{})
}

// decisionRelationshipsPrefixItems is getRelationshipsPrefixItems.
func decisionRelationshipsPrefixItems(pattern any) any {
	direct := func(schema any) any {
		return decisionOptional(decisionOptional(decisionGet(schema, "properties"), keyRelationships), keyPrefixItems)
	}

	if v := direct(pattern); truthy(v) {
		return v
	}

	if all, ok := decisionGet(pattern, "allOf").([]any); ok {
		for _, schema := range all {
			if v := direct(schema); truthy(v) {
				return v
			}
		}
	}

	return []any{}
}

// decisionPending is a decision relationship of the architecture that has no
// answer yet, with its index among the relationships.
type decisionPending struct {
	rel   any
	index int
}

// decisionPendingList gives the decision relationships with an empty options array.
func decisionPendingList(architecture any) []decisionPending {
	var list []decisionPending

	for i, rel := range decisionRelationships(architecture) {
		if decisionIsUnanswered(rel) {
			list = append(list, decisionPending{rel, i})
		}
	}

	return list
}

func decisionIsUnanswered(rel any) bool {
	options, ok := decisionOptions(rel)

	return ok && decisionIsDecision(rel) && len(options) == 0
}

// decisionOneOfIDs gives the IDs of the oneOf options.
func decisionOneOfIDs(options []decisionOption) valueSet {
	ids := valueSet{}

	for _, o := range options {
		if o.optionType == keyOneOf {
			ids.add(o.optionID)
		}
	}

	return ids
}

// decisionUnansweredFinding is the finding of a oneOf decision without an answer.
func decisionUnansweredFinding(pending decisionPending, id any) finding {
	text := "undefined"

	relObj, _ := pending.rel.(obj)
	if _, present := relObj["unique-id"]; present {
		text = jsString(id)
	}

	message := fmt.Sprintf("The decision [%s] is a oneOf and needs exactly one answer.", text)

	return finding{
		rule:    ruleJSONSchema,
		code:    diag.INT006,
		params:  diag.Detail(message),
		path:    []string{keyRelationships, strconv.Itoa(pending.index), "relationship-type", "options"},
		message: message,
	}
}

// unansweredOneOfDecisions ports unansweredOneOfDecisions. The error stands for a
// TypeError of the CALM CLI.
func unansweredOneOfDecisions(architecture, pattern any) (out []finding, err error) {
	defer decisionRecover(&err)

	list := decisionPendingList(architecture)
	if len(list) == 0 {
		return nil, nil
	}

	options, err := decisionExtractOptions(pattern)
	if err != nil {
		return nil, err
	}

	oneOfIDs := decisionOneOfIDs(options)

	for _, pending := range list {
		id := decisionGet(pending.rel, "unique-id")
		if oneOfIDs.has(id) {
			out = append(out, decisionUnansweredFinding(pending, id))
		}
	}

	return out, nil
}
