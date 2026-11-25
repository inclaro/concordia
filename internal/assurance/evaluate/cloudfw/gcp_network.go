package cloudfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// gcpNet identifies a GCP network. A network is the pair of a project and a name, so two networks with one name in
// two projects differ.
type gcpNet struct {
	// res is the network resource of the plans that the configuration names, nil when a value names the network.
	res *plan.Resource
	// name is the name of the network, project its project, "" when the plan does not give it.
	name, project string
	// scope is the plan and provider configuration whose default project applies when the project is not known, ""
	// when the plan does not give the provider configuration.
	scope string
}

// String names the network, with its project when it is known.
func (n gcpNet) String() string {
	if n.project == "" {
		return n.name
	}

	return "projects/" + n.project + "/global/networks/" + n.name
}

// same is yes when the two identify one network, no when their names or known projects differ, and maybe when a
// project is not known and nothing else shows that the projects are one.
func (n gcpNet) same(other gcpNet) tri {
	switch {
	case n.name != other.name:
		return triNo
	case n.res != nil && n.res == other.res:
		return triYes
	}

	return n.sameProject(other)
}

// sameProject compares the projects of two networks of one name: their known projects, else the one plan and
// provider configuration that gives both their default project.
func (n gcpNet) sameProject(other gcpNet) tri {
	switch {
	case n.project != "" && other.project != "":
		return azureIf(n.project == other.project)
	case n.project != "" || other.project != "":
		return triMaybe
	case n.scope != "" && n.scope == other.scope:
		return triYes
	}

	return triMaybe
}

// gcpNetOfValue identifies the network that a known value of the resource names: a self link or an ID names its
// project, and a bare name is in the project of the resource.
func (e *Evaluator) gcpNetOfValue(res *plan.Resource, value string) gcpNet {
	project := gcpProjectOf(value)
	if project == "" {
		project, _ = attrString(res, "project")
	}

	return gcpNet{name: gcpName(value), project: project, scope: e.gcpScope(res)}
}

// gcpNetOfResource identifies a network resource of the plans.
func (e *Evaluator) gcpNetOfResource(network *plan.Resource, name string) gcpNet {
	project, _ := attrString(network, "project")
	for _, path := range gcpLinkAttrs {
		if link, ok := attrString(network, path); ok && project == "" {
			project = gcpProjectOf(link)
		}
	}

	return gcpNet{res: network, name: name, project: project, scope: e.gcpScope(network)}
}

// gcpScope is the plan and provider configuration of the resource, "" when the plan does not give the provider
// configuration.
func (e *Evaluator) gcpScope(res *plan.Resource) string {
	if res.ProviderConfig == "" {
		return ""
	}

	return fmt.Sprintf("plan %d, provider %s", e.planOf[res], res.ProviderConfig)
}

// gcpProjectOf is the project of a self link or an ID (`.../projects/<project>/...`), "" for a bare name.
func gcpProjectOf(value string) string {
	parts := strings.Split(value, "/")
	if i := slices.Index(parts, "projects"); i >= 0 && i+1 < len(parts) {
		return parts[i+1]
	}

	return ""
}

// gcpSubnetNamed is the subnetwork that a known value of the resource names, or nil. A self link or an ID matches
// the `self_link` or the `id` of one subnetwork of the plans. A bare name matches the one subnetwork of the plans
// with that name in the project and the region of the resource: a subnetwork is identified by its project, region,
// and name.
func (e *Evaluator) gcpSubnetNamed(res *plan.Resource, value string) *plan.Resource {
	candidates := e.ofType(gcpSubnetworkType)
	if strings.Contains(value, "/") {
		return only(slices.DeleteFunc(candidates, func(sub *plan.Resource) bool { return !gcpLinks(sub, value) }))
	}

	return only(slices.DeleteFunc(candidates, func(sub *plan.Resource) bool {
		name, _ := attrString(sub, "name")

		return name != value || !e.gcpSameScope(res, sub) || !gcpSameRegion(res, sub)
	}))
}

// gcpLinkAttrs are the attributes that hold the self link or the ID of a GCP resource.
var gcpLinkAttrs = []string{"self_link", attrID}

// gcpLinks reports whether the self link or the ID of the resource is the value.
func gcpLinks(res *plan.Resource, value string) bool {
	return slices.ContainsFunc(gcpLinkAttrs, func(path string) bool {
		link, ok := attrString(res, path)

		return ok && link == value
	})
}

// gcpSameScope reports whether two resources are in one project: their known projects are equal, or both projects
// are not known and one plan and provider configuration gives them.
func (e *Evaluator) gcpSameScope(a, b *plan.Resource) bool {
	projectA, knownA := attrString(a, "project")
	projectB, knownB := attrString(b, "project")

	if knownA && knownB {
		return projectA == projectB
	}

	scope := e.gcpScope(a)

	return !knownA && !knownB && scope != "" && scope == e.gcpScope(b)
}

// gcpSameRegion reports whether the known region of the subnetwork is the region of the resource: its `region`, or
// the region of its `zone`.
func gcpSameRegion(res, sub *plan.Resource) bool {
	region, ok := attrString(sub, "region")
	if !ok {
		return false
	}

	if own, known := attrString(res, "region"); known {
		return own == gcpName(region)
	}

	zone, known := attrString(res, "zone")

	return known && strings.HasPrefix(gcpName(zone), gcpName(region)+"-")
}
