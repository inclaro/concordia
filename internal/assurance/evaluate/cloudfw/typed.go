package cloudfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// resourceTypes are the resource types that an attribute may name, for example `aws_security_group`.
type resourceTypes []string

// refsOfType is refs for an attribute that must name resources of the expected types, for example
// `aws_security_group`. A resolved resource of another type (an `aws_vpc` whose `default_security_group_id` the
// configuration names, say) is left out and gives an unresolved-reference gap and ok false: its rules are not the
// rules of the resource that the attribute holds.
func (e *Evaluator) refsOfType(r *plan.Resource, path string, into *side, types ...string) ([]*plan.Resource, bool) {
	found, ok := e.refs(r, path, into)
	kept, typed := keepTypes(attrRef{res: r, path: planPath(path)}, found, into, types)

	return kept, ok && typed
}

// refOfType is ref for an attribute that must name one resource of the expected types. It gives nil and ok true
// when the attribute is absent, and ok false when it does not resolve to exactly one resource of those types.
func (e *Evaluator) refOfType(r *plan.Resource, path string, into *side, types ...string) (*plan.Resource, bool) {
	found, ok := e.ref(r, path, into)
	if !ok || found == nil {
		return found, ok
	}

	if _, typed := keepTypes(attrRef{res: r, path: planPath(path)}, []*plan.Resource{found}, into, types); !typed {
		return nil, false
	}

	return found, true
}

// keepTypes keeps the resources of the expected types. Each other resource gives an unresolved-reference gap, and
// ok is false when there is one.
func keepTypes(attribute attrRef, found []*plan.Resource, into *side, types resourceTypes) (kept []*plan.Resource, ok bool) {
	ok = true

	for _, res := range found {
		if slices.Contains(types, res.Type) {
			kept = append(kept, res)

			continue
		}

		into.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s.%s names %s, a %s, not one of %s",
			attribute.res.Address, attribute.path, res.Address, res.Type, strings.Join(types, ", ")))

		ok = false
	}

	return kept, ok
}
