package cloudfw

import (
	"strings"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// nameKey finds an Azure resource by its type, `name`, and `resource_group_name`. Azure compares names without case,
// so the name and the group are lower case.
type nameKey struct {
	typ, name, group string
}

func newNameKey(typ, name, group string) nameKey {
	return nameKey{typ: typ, name: strings.ToLower(name), group: strings.ToLower(group)}
}

// indexName indexes a resource with a known `name` and `resource_group_name`.
func (e *Evaluator) indexName(res *plan.Resource) {
	name, hasName := attrString(res, "name")
	group, hasGroup := attrString(res, "resource_group_name")

	if hasName && hasGroup {
		key := newNameKey(res.Type, name, group)
		e.byName[key] = append(e.byName[key], res)
	}
}

// named are the resources of the plans with the key: the type, the known `name`, and the known
// `resource_group_name`.
func (e *Evaluator) named(key nameKey) []*plan.Resource { return e.byName[key] }
