package cloudfw

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// blockElement is one attribute of one element of a set-typed nested block list, for example
// `ingress[0].security_groups` is {list: "ingress", field: "security_groups", index: 0}. Terraform lists the elements
// of a set in `after` in hash order, so index is the position in the plan values and not the position in the
// configuration, whose order the References keys keep.
type blockElement struct {
	list  string
	field string
	index int
}

// path is the path of the attribute in the form of plan.Resource.Unknown.
func (b blockElement) path() string {
	return fmt.Sprintf("%s[%d].%s", planPath(b.list), b.index, planPath(b.field))
}

// wildcard names the attribute over all elements, for gap details.
func (b blockElement) wildcard() string { return planPath(b.list) + "[*]." + planPath(b.field) }

// at is the same attribute of element i.
func (b blockElement) at(i int) blockElement {
	return blockElement{list: b.list, field: b.field, index: i}
}

// matches reports whether a References key is the attribute of some element of the list.
func (b blockElement) matches(key string) bool {
	steps, list, field := pathTokens(key), pathTokens(b.list), pathTokens(b.field)
	if len(steps) != len(list)+1+len(field) {
		return false
	}

	_, err := strconv.Atoi(steps[len(list)])

	return err == nil && slices.Equal(steps[:len(list)], list) && slices.Equal(steps[len(list)+1:], field)
}

// blockRefsOfType resolves the attribute of one element of a set-typed block list to resources of the expected
// types. Known IDs resolve through the `id` index. An unknown attribute resolves through the configuration
// references of that attribute over all elements of the list, because the element order of the plan is not the
// configuration order: it resolves only when every element with references names the same set and as many elements
// have references as have the attribute unknown. Otherwise the gap is unresolved-reference and ok is false.
// The caller must first check listHasUnknownElements on the list: a wholly unknown element shifts the indexes.
func (e *Evaluator) blockRefsOfType(r *plan.Resource, el blockElement, into *side, types ...string) ([]*plan.Resource, bool) {
	at := attrRef{res: r, path: el.path()}
	found, ok := e.knownRefs(at, into)

	if unknownAt(r, at.path) {
		more, resolved := e.blockConfigRefs(r, el, into)
		found = append(found, more...)
		ok = ok && resolved
	}

	kept, typed := keepTypes(at, sortResources(found), into, types)

	return kept, ok && typed
}

// blockConfigRefs resolves an unknown attribute of a block element through the references of all elements.
func (e *Evaluator) blockConfigRefs(r *plan.Resource, el blockElement, into *side) ([]*plan.Resource, bool) {
	addresses, ok := blockReferences(r, el, into)
	if !ok {
		return nil, false
	}

	return e.resolveAll(addresses, into)
}

// blockReferences is the one reference set that every element of the list with references names for the attribute.
func blockReferences(r *plan.Resource, el blockElement, into *side) ([]string, bool) {
	sets := referenceSets(r, el)
	where := r.Address + "." + el.wildcard()

	switch unknown := unknownElements(r, el); {
	case len(sets) == 0:
		into.gap(evaluate.GapUnknownAfterApply, r.Address+"."+el.path())
	case !allEqual(sets):
		into.gap(evaluate.GapUnresolvedReference, where+": the blocks name different resources, and the plan does not "+
			"keep the configuration order of the blocks")
	case len(sets) != unknown:
		into.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s: %d blocks have the attribute unknown and %d have "+
			"configuration references", where, unknown, len(sets)))
	default:
		return sets[0], true
	}

	return nil, false
}

// referenceSets are the reference lists of the attribute of every element of the list, in no particular order.
func referenceSets(r *plan.Resource, el blockElement) [][]string {
	var sets [][]string

	for key, addresses := range r.References {
		if el.matches(key) {
			sets = append(sets, addresses)
		}
	}

	return sets
}

// allEqual reports whether all the reference lists (sorted, as the plan reader writes them) are the same.
func allEqual(sets [][]string) bool {
	return !slices.ContainsFunc(sets, func(set []string) bool { return !slices.Equal(set, sets[0]) })
}

// unknownElements counts the elements of the list whose attribute is unknown until apply.
func unknownElements(r *plan.Resource, el blockElement) int {
	count := 0

	for i := range blockCount(r, el.list) {
		if unknownAt(r, el.at(i).path()) {
			count++
		}
	}

	return count
}
