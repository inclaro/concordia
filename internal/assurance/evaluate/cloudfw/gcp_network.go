package cloudfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// gcpProject identifies the project of a GCP resource: its known name, else the plan and provider configuration whose
// default project applies because the configuration does not set the project. Both are "" when the project is set to
// a value that is unknown until apply, or the plan does not give enough: such a project can be any project.
type gcpProject struct {
	value string
	scope string
}

// same is yes when the two are one project, no when their known names differ, and maybe otherwise.
func (p gcpProject) same(other gcpProject) tri {
	switch {
	case p.value != "" && other.value != "":
		return azureIf(p.value == other.value)
	case p.value == "" && other.value == "" && p.scope != "" && p.scope == other.scope:
		return triYes
	}

	return triMaybe
}

// gcpProjectOfResource is the project of the resource: its known `project`, or the default project of the provider
// configuration when the configuration does not set `project`. A `project` that the configuration sets to a value
// unknown until apply overrides the provider default, so it gives no project.
func (e *Evaluator) gcpProjectOfResource(res *plan.Resource) gcpProject {
	if value, ok := attrString(res, gcpProjectAttr); ok && value != "" {
		return gcpProject{value: value}
	}

	if leavesUnset(res, gcpProjectAttr) {
		return gcpProject{scope: e.gcpScope(res)}
	}

	return gcpProject{}
}

// gcpProviderProject is the default project of the provider configuration of the resource: the planned `project`
// when the configuration does not set it, else only the provider configuration.
func (e *Evaluator) gcpProviderProject(res *plan.Resource) gcpProject {
	if leavesUnset(res, gcpProjectAttr) {
		return e.gcpProjectOfResource(res)
	}

	return gcpProject{scope: e.gcpScope(res)}
}

// gcpProjectAttr is the project attribute of a GCP resource.
const gcpProjectAttr = "project"

// gcpNet identifies a GCP network. A network is the pair of a project and a name, so two networks with one name in
// two projects differ.
type gcpNet struct {
	// res is the network resource of the plans that the configuration names, nil when a value names the network.
	res *plan.Resource
	// name is the name of the network.
	name string
	// project is the project of the network.
	project gcpProject
}

// String names the network, with its project when it is known.
func (n gcpNet) String() string {
	if n.project.value == "" {
		return n.name
	}

	return "projects/" + n.project.value + "/global/networks/" + n.name
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

	return n.project.same(other.project)
}

// gcpNetOfValue identifies the network that a known value of the resource names: a self link or an ID names its
// project, and a bare name is in the project of the resource.
func (e *Evaluator) gcpNetOfValue(res *plan.Resource, value string) gcpNet {
	if project := gcpProjectOf(value); project != "" {
		return gcpNet{name: gcpName(value), project: gcpProject{value: project}}
	}

	return gcpNet{name: gcpName(value), project: e.gcpProjectOfResource(res)}
}

// gcpNetOfResource identifies a network resource of the plans.
func (e *Evaluator) gcpNetOfResource(network *plan.Resource, name string) gcpNet {
	for _, path := range gcpLinkAttrs {
		if link, ok := attrString(network, path); ok && gcpProjectOf(link) != "" {
			return gcpNet{res: network, name: name, project: gcpProject{value: gcpProjectOf(link)}}
		}
	}

	return gcpNet{res: network, name: name, project: e.gcpProjectOfResource(network)}
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
// with that name in the subnetwork project of the interface and the region of the resource: a subnetwork is
// identified by its project, region, and name.
func (e *Evaluator) gcpSubnetNamed(res *plan.Resource, value string) *plan.Resource {
	candidates := e.ofType(gcpSubnetworkType)
	if strings.Contains(value, "/") {
		return only(slices.DeleteFunc(candidates, func(sub *plan.Resource) bool { return !gcpLinks(sub, value) }))
	}

	project := e.gcpSubnetProject(res)

	return only(slices.DeleteFunc(candidates, func(sub *plan.Resource) bool {
		name, _ := attrString(sub, "name")

		return name != value || project.same(e.gcpProjectOfResource(sub)) != triYes || !gcpSameRegion(res, sub)
	}))
}

// gcpSubnetProjectPath is the project of the subnetwork of the first interface (a Shared VPC host project).
const gcpSubnetProjectPath = "network_interface.0.subnetwork_project"

// gcpSubnetProject is the project of the subnetwork that a bare name on the first interface of the resource names:
// its known `subnetwork_project`; else, when the configuration does not set it, the default project of the provider
// configuration of the resource; else no project, because a value unknown until apply can name any project.
func (e *Evaluator) gcpSubnetProject(res *plan.Resource) gcpProject {
	if value, ok := attrString(res, gcpSubnetProjectPath); ok && value != "" {
		return gcpProject{value: value}
	}

	if leavesUnset(res, gcpSubnetProjectPath) {
		return e.gcpProviderProject(res)
	}

	return gcpProject{}
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
