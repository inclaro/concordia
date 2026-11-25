package cloudfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/canonjson"
)

// refs resolves the attribute at the path (one ID or a list of IDs) to the resources of the plan that it names. A
// known ID resolves through the `id` index. An attribute that is unknown until apply resolves through the
// configuration references of the resource. ok is false when a part does not resolve; the gaps say why. An absent
// attribute gives no resources and ok true.
func (e *Evaluator) refs(r *plan.Resource, path string, s *side) (found []*plan.Resource, ok bool) {
	path = planPath(path)
	found, ok = e.knownRefs(attrRef{res: r, path: path}, s)

	if unknownAt(r, path) {
		more, resolved := e.configRefs(r, path, s)
		found = append(found, more...)
		ok = ok && resolved
	}

	return sortResources(found), ok
}

// sortResources sorts the resources by address and removes repeats.
func sortResources(found []*plan.Resource) []*plan.Resource {
	slices.SortFunc(found, func(a, b *plan.Resource) int { return canonjson.CompareUTF16(a.Address, b.Address) })

	return slices.Compact(found)
}

// ref resolves an attribute that names one resource. It gives nil and ok true when the attribute is absent, and ok
// false when it does not resolve to exactly one resource.
func (e *Evaluator) ref(r *plan.Resource, path string, s *side) (*plan.Resource, bool) {
	found, ok := e.refs(r, path, s)

	switch {
	case !ok:
		return nil, false
	case len(found) > 1:
		s.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s.%s names %d resources", r.Address, planPath(path), len(found)))

		return nil, false
	case len(found) == 1:
		return found[0], true
	}

	return nil, true
}

// attrRef is one attribute of a resource, with its path in the form of plan.Resource.References.
type attrRef struct {
	res  *plan.Resource
	path string
}

// configured are the configuration addresses that the configuration names in the attribute.
func (a attrRef) configured() []string { return a.res.References[a.path] }

// knownRefs resolves the known IDs of the attribute.
func (e *Evaluator) knownRefs(a attrRef, s *side) (found []*plan.Resource, ok bool) {
	ok = true

	for _, id := range attrStrings(a.res, a.path) {
		if target := e.resourceByID(id); target != nil {
			found = append(found, target)

			continue
		}

		missingID(a, id, len(e.byID[id]), s)

		ok = false
	}

	return found, ok
}

// missingID records the gap of a known ID that not exactly one resource of the plans has: the plans of the
// environment hold more than one resource with the ID, the configuration names a data source, or the ID is not in
// the plans.
func missingID(ref attrRef, known string, holders int, into *side) {
	if holders > 1 {
		into.gap(evaluate.GapUnresolvedReference,
			fmt.Sprintf("%s.%s: %s is the id of %d resources of the plans", ref.res.Address, ref.path, known, holders))

		return
	}

	configured := ref.configured()
	if data := slices.IndexFunc(configured, isDataSource); data >= 0 {
		into.gap(evaluate.GapUnresolvedReference, fmt.Sprintf(dataSourceFormat, configured[data]))

		return
	}

	into.gap(evaluate.GapUnresolvedReference,
		fmt.Sprintf("%s.%s: %s is not the id of a resource in the plan", ref.res.Address, ref.path, known))
}

// configRefs resolves an unknown attribute through the configuration references of the resource.
func (e *Evaluator) configRefs(r *plan.Resource, path string, into *side) (found []*plan.Resource, ok bool) {
	addresses := r.References[path]
	if len(addresses) == 0 {
		into.gap(evaluate.GapUnknownAfterApply, r.Address+"."+path)

		return nil, false
	}

	return e.resolveAll(r, addresses, into)
}

// resolveAll resolves each configuration address that the resource from names to its single instance in the plan
// of from. ok is false when one does not resolve.
func (e *Evaluator) resolveAll(from *plan.Resource, addresses []string, into *side) (found []*plan.Resource, ok bool) {
	ok = true

	for _, config := range addresses {
		target, resolved := e.resolveConfig(from, config, into)
		if !resolved {
			ok = false

			continue
		}

		found = append(found, target)
	}

	return found, ok
}

// resolveConfig resolves a configuration address (without instance keys) that the resource from names to its single
// instance in the plan of from: the configuration of one plan says nothing about the resources of another. A data
// source, or an opaque `var.`, `local.`, or `each.value` entry whose value may hold any resource, does not resolve.
func (e *Evaluator) resolveConfig(from *plan.Resource, config string, into *side) (*plan.Resource, bool) {
	switch inst := e.instances(from, config); {
	case isDataSource(config):
		into.gap(evaluate.GapUnresolvedReference, fmt.Sprintf(dataSourceFormat, config))
	case plan.IsOpaqueReference(config):
		into.gap(evaluate.GapUnresolvedReference, fmt.Sprintf(opaqueFormat, config))
	case len(inst) == 1:
		return inst[0], true
	case len(inst) == 0:
		into.gap(evaluate.GapUnresolvedReference, config+" is not a resource of the plan after apply")
	default:
		into.gap(evaluate.GapUnresolvedReference, fmt.Sprintf("%s has %d instances", config, len(inst)))
	}

	return nil, false
}

// dataSourceFormat is the gap detail of a reference to a data source.
const dataSourceFormat = "%s is a data source; its rules are not in the plan"

// opaqueFormat is the gap detail of a reference through a variable, a local value, or `each.value`.
const opaqueFormat = "%s is a variable or local value; the resource it names is not known from the plan"

// isDataSource reports whether the configuration address names a data source (`data.<type>.<name>`, in any module).
func isDataSource(config string) bool {
	tokens := strings.Split(config, ".")

	i := 0
	for i+1 < len(tokens) && tokens[i] == "module" {
		i += 2
	}

	return i < len(tokens) && tokens[i] == "data"
}
