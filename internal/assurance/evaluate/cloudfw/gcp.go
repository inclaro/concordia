package cloudfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// GCP attribute paths of an endpoint.
const (
	gcpNetworkPath    = "network_interface.0.network"
	gcpSubnetworkPath = "network_interface.0.subnetwork"
	gcpIPPath         = "network_interface.0.network_ip"
	gcpEmailPath      = "service_account.0.email"
	gcpSubnetworkType = "google_compute_subnetwork"
	gcpNetworkType    = "google_compute_network"
	gcpAccountType    = "google_service_account"
)

// gcpEndpointTypes are the resource types that can be an end of a GCP query.
var gcpEndpointTypes = []string{"google_compute_instance", "google_compute_instance_template"}

// gcpPolicyPrefixes are the type prefixes of the firewall policies that the evaluator does not read. A policy is
// evaluated before the VPC firewall rules and can allow, deny, or pass the traffic on.
var gcpPolicyPrefixes = []string{
	"google_compute_network_firewall_policy",
	"google_compute_region_network_firewall_policy",
	"google_compute_firewall_policy",
}

// gcpEndpoint is one end of a GCP query: its network (by name), its addresses, its network tags, its service
// account, and whether the plan proves that it has IPv4 addresses only.
type gcpEndpoint struct {
	network  string
	addrs    addrSet
	tags     gcpSet
	accounts gcpSet
	ipv4Only bool
}

// gcpSet is a list of values of an attribute. gaps is not empty when a part of the list is not known.
type gcpSet struct {
	vals []string
	gaps []assurance.Gap
}

func (g *gcpSet) empty() bool { return len(g.vals) == 0 && len(g.gaps) == 0 }

// evaluateGCP answers a query between two GCP instances or instance templates from the VPC firewall rules of the
// plan: egress at the source, then ingress at the destination.
func (e *Evaluator) evaluateGCP(query *Query, source, dest *plan.Resource) Result {
	src, gaps := e.gcpEndpointOf(source)
	dst, more := e.gcpEndpointOf(dest)

	if gaps = append(gaps, more...); len(gaps) > 0 {
		return unknownFrom(gaps)
	}

	if src.network != dst.network {
		return unknownFrom([]assurance.Gap{{
			Kind: evaluate.GapPeeringNotEvaluated,
			Detail: fmt.Sprintf("%s is in network %s and %s is in network %s",
				source.Address, src.network, dest.Address, dst.network),
		}})
	}

	if gaps = e.gcpPolicyGaps(); len(gaps) > 0 {
		return unknownFrom(gaps)
	}

	return decide(
		e.gcpSide(&gcpCheck{query: query, target: src, peer: dst}),
		e.gcpSide(&gcpCheck{query: query, target: dst, peer: src, ingress: true}),
	)
}

// gcpEndpointOf reads the network, addresses, tags, and service account of an endpoint.
func (e *Evaluator) gcpEndpointOf(res *plan.Resource) (*gcpEndpoint, []assurance.Gap) {
	if !slices.Contains(gcpEndpointTypes, res.Type) {
		return nil, []assurance.Gap{unsupportedResource(res)}
	}

	point := &side{}

	network, ok := e.gcpNetworkOf(res, point)
	if !ok {
		return nil, point.gaps
	}

	return &gcpEndpoint{
		network:  network,
		addrs:    e.gcpAddrs(res),
		tags:     e.gcpSetAt(res, "tags", ""),
		accounts: e.gcpSetAt(res, gcpEmailPath, "email"),
		ipv4Only: e.gcpProvenIPv4Only(res),
	}, nil
}

// gcpNetworkOf is the network of an endpoint. When the configuration does not name the network, the network of the
// subnetwork applies.
func (e *Evaluator) gcpNetworkOf(res *plan.Resource, point *side) (string, bool) {
	_, known := attrString(res, gcpNetworkPath)
	if !known && len(res.References[planPath(gcpNetworkPath)]) == 0 {
		if sub := e.gcpSubnet(res); sub != nil {
			return e.gcpNetworkKey(sub, "network", point)
		}
	}

	return e.gcpNetworkKey(res, gcpNetworkPath, point)
}

// gcpNetworkKey is the name of the network at the path: the last segment of a known value (a name, an ID, or a self
// link), or the `name` of the network that the configuration names.
func (e *Evaluator) gcpNetworkKey(res *plan.Resource, path string, point *side) (string, bool) {
	if value, ok := attrString(res, path); ok && value != "" {
		return gcpName(value), true
	}

	target, ok := e.refOfType(res, path, point, gcpNetworkType)
	if !ok {
		return "", false
	}

	if target == nil {
		point.gap(evaluate.GapUnknownAfterApply, res.Address+"."+planPath(path))

		return "", false
	}

	name, known := attrString(target, "name")
	if !known {
		point.gap(evaluate.GapUnknownAfterApply, target.Address+".name")
	}

	return name, known
}

// gcpSubnet is the subnetwork of an endpoint, or nil when the plan does not give it.
func (e *Evaluator) gcpSubnet(res *plan.Resource) *plan.Resource {
	if value, ok := attrString(res, gcpSubnetworkPath); ok {
		return e.gcpNamed(gcpSubnetworkType, value)
	}

	target, _ := e.refOfType(res, gcpSubnetworkPath, &side{}, gcpSubnetworkType)

	return target
}

// gcpNamed is the resource of the type whose ID or name is the value (a name, an ID, or a self link), or nil when
// none or more than one resource of the plans has the name.
func (e *Evaluator) gcpNamed(typ, value string) *plan.Resource {
	if byID := e.resourceByID(value); byID != nil && byID.Type == typ {
		return byID
	}

	name := gcpName(value)

	return only(slices.DeleteFunc(e.ofType(typ), func(candidate *plan.Resource) bool {
		got, _ := attrString(candidate, "name")

		return got != name
	}))
}

// gcpName is the last segment of a GCP name, ID, or self link.
func gcpName(value string) string {
	return value[strings.LastIndex(value, "/")+1:]
}

// gcpAddrs is the address set of an endpoint: its internal IP, else the range of its subnetwork.
func (e *Evaluator) gcpAddrs(res *plan.Resource) addrSet {
	if addr, ok := attrString(res, gcpIPPath); ok && addr != "" {
		return hostSet(addr)
	}

	if sub := e.gcpSubnet(res); sub != nil {
		if cidr, ok := attrString(sub, "ip_cidr_range"); ok {
			return prefixSet(cidr)
		}
	}

	return unknownAddrs
}

// gcpSetAt reads the list at the path. An unknown part resolves through the configuration references to the field
// of the named resources; field "" means the values cannot come from a reference.
func (e *Evaluator) gcpSetAt(res *plan.Resource, path, field string) gcpSet {
	set := gcpSet{vals: attrStrings(res, path)}
	if !unknownAt(res, path) {
		return set
	}

	point := &side{}

	if field == "" {
		point.gap(evaluate.GapUnknownAfterApply, res.Address+"."+planPath(path))
	} else {
		set.vals = append(set.vals, e.gcpRefValues(res, path, field, point)...)
	}

	set.gaps = point.gaps

	return set
}

// gcpRefValues are the known values of the field of the resources that the configuration names at the path.
func (e *Evaluator) gcpRefValues(res *plan.Resource, path, field string, point *side) []string {
	var vals []string

	targets, _ := e.configRefs(res, planPath(path), point)
	targets, _ = keepTypes(attrRef{res: res, path: planPath(path)}, targets, point, resourceTypes{gcpAccountType})

	for _, target := range targets {
		if value, ok := attrString(target, field); ok {
			vals = append(vals, value)

			continue
		}

		point.gap(evaluate.GapUnknownAfterApply, target.Address+"."+field)
	}

	return vals
}

// gcpPolicyGaps are the gaps of the firewall policies in the plan.
func (e *Evaluator) gcpPolicyGaps() []assurance.Gap {
	var gaps []assurance.Gap

	for _, res := range e.live {
		if slices.ContainsFunc(gcpPolicyPrefixes, func(prefix string) bool { return strings.HasPrefix(res.Type, prefix) }) {
			gaps = append(gaps, assurance.Gap{
				Kind: evaluate.GapUnsupportedKind, Detail: fmt.Sprintf("%s: firewall policy %s", res.Address, res.Type),
			})
		}
	}

	return gaps
}
