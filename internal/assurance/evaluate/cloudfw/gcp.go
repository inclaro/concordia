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

// gcpEndpoint is one end of a GCP query: its network (by name), its addresses, its network tags, and its service
// account.
type gcpEndpoint struct {
	network  string
	addrs    addrSet
	tags     gcpSet
	accounts gcpSet
}

// gcpSet is a list of values of an attribute. gaps is not empty when a part of the list is not known.
type gcpSet struct {
	vals []string
	gaps []assurance.Gap
}

func (g *gcpSet) empty() bool { return len(g.vals) == 0 && len(g.gaps) == 0 }

// evaluateGCP answers a query between two GCP instances or instance templates from the VPC firewall rules of the
// plan: egress at the source, then ingress at the destination.
func (e *Evaluator) evaluateGCP(q *Query, from, to *plan.Resource) Result {
	src, gaps := e.gcpEndpointOf(from)
	dst, more := e.gcpEndpointOf(to)

	if gaps = append(gaps, more...); len(gaps) > 0 {
		return unknownFrom(gaps)
	}

	if src.network != dst.network {
		return unknownFrom([]assurance.Gap{{
			Kind: evaluate.GapPeeringNotEvaluated,
			Detail: fmt.Sprintf("%s is in network %s and %s is in network %s",
				from.Address, src.network, to.Address, dst.network),
		}})
	}

	if gaps = e.gcpPolicyGaps(); len(gaps) > 0 {
		return unknownFrom(gaps)
	}

	return decide(
		e.gcpSide(&gcpCheck{q: q, target: src, peer: dst}),
		e.gcpSide(&gcpCheck{q: q, target: dst, peer: src, ingress: true}),
	)
}

// gcpEndpointOf reads the network, addresses, tags, and service account of an endpoint.
func (e *Evaluator) gcpEndpointOf(r *plan.Resource) (*gcpEndpoint, []assurance.Gap) {
	if !slices.Contains(gcpEndpointTypes, r.Type) {
		return nil, []assurance.Gap{unsupportedResource(r)}
	}

	s := &side{}

	network, ok := e.gcpNetworkOf(r, s)
	if !ok {
		return nil, s.gaps
	}

	return &gcpEndpoint{
		network:  network,
		addrs:    e.gcpAddrs(r),
		tags:     e.gcpSetAt(r, "tags", ""),
		accounts: e.gcpSetAt(r, gcpEmailPath, "email"),
	}, nil
}

// gcpNetworkOf is the network of an endpoint. When the configuration does not name the network, the network of the
// subnetwork applies.
func (e *Evaluator) gcpNetworkOf(r *plan.Resource, s *side) (string, bool) {
	_, known := attrString(r, gcpNetworkPath)
	if !known && len(r.References[planPath(gcpNetworkPath)]) == 0 {
		if sub := e.gcpSubnet(r); sub != nil {
			return e.gcpNetworkKey(sub, "network", s)
		}
	}

	return e.gcpNetworkKey(r, gcpNetworkPath, s)
}

// gcpNetworkKey is the name of the network at the path: the last segment of a known value (a name, an ID, or a self
// link), or the `name` of the network that the configuration names.
func (e *Evaluator) gcpNetworkKey(r *plan.Resource, path string, s *side) (string, bool) {
	if v, ok := attrString(r, path); ok && v != "" {
		return gcpName(v), true
	}

	target, ok := e.ref(r, path, s)
	if !ok {
		return "", false
	}

	if target == nil {
		s.gap(evaluate.GapUnknownAfterApply, r.Address+"."+planPath(path))

		return "", false
	}

	name, known := attrString(target, "name")
	if !known {
		s.gap(evaluate.GapUnknownAfterApply, target.Address+".name")
	}

	return name, known
}

// gcpSubnet is the subnetwork of an endpoint, or nil when the plan does not give it.
func (e *Evaluator) gcpSubnet(r *plan.Resource) *plan.Resource {
	if v, ok := attrString(r, gcpSubnetworkPath); ok {
		return e.gcpNamed(gcpSubnetworkType, v)
	}

	target, _ := e.ref(r, gcpSubnetworkPath, &side{})

	return target
}

// gcpNamed is the resource of the type whose ID or name is the value (a name, an ID, or a self link), or nil.
func (e *Evaluator) gcpNamed(typ, value string) *plan.Resource {
	if r := e.resourceByID(value); r != nil && r.Type == typ {
		return r
	}

	name := gcpName(value)
	candidates := e.ofType(typ)

	if i := slices.IndexFunc(candidates, func(c *plan.Resource) bool {
		n, _ := attrString(c, "name")

		return n == name
	}); i >= 0 {
		return candidates[i]
	}

	return nil
}

// gcpName is the last segment of a GCP name, ID, or self link.
func gcpName(value string) string {
	return value[strings.LastIndex(value, "/")+1:]
}

// gcpAddrs is the address set of an endpoint: its internal IP, else the range of its subnetwork.
func (e *Evaluator) gcpAddrs(r *plan.Resource) addrSet {
	if ip, ok := attrString(r, gcpIPPath); ok && ip != "" {
		return hostSet(ip)
	}

	if sub := e.gcpSubnet(r); sub != nil {
		if cidr, ok := attrString(sub, "ip_cidr_range"); ok {
			return prefixSet(cidr)
		}
	}

	return unknownAddrs
}

// gcpSetAt reads the list at the path. An unknown part resolves through the configuration references to the field
// of the named resources; field "" means the values cannot come from a reference.
func (e *Evaluator) gcpSetAt(r *plan.Resource, path, field string) gcpSet {
	set := gcpSet{vals: attrStrings(r, path)}
	if !unknownAt(r, path) {
		return set
	}

	s := &side{}

	if field == "" {
		s.gap(evaluate.GapUnknownAfterApply, r.Address+"."+planPath(path))
	} else {
		set.vals = append(set.vals, e.gcpRefValues(r, path, field, s)...)
	}

	set.gaps = s.gaps

	return set
}

// gcpRefValues are the known values of the field of the resources that the configuration names at the path.
func (e *Evaluator) gcpRefValues(r *plan.Resource, path, field string, s *side) []string {
	var vals []string

	targets, _ := e.configRefs(r, planPath(path), s)
	for _, t := range targets {
		if v, ok := attrString(t, field); ok {
			vals = append(vals, v)

			continue
		}

		s.gap(evaluate.GapUnknownAfterApply, t.Address+"."+field)
	}

	return vals
}

// gcpPolicyGaps are the gaps of the firewall policies in the plan.
func (e *Evaluator) gcpPolicyGaps() []assurance.Gap {
	var gaps []assurance.Gap

	for _, r := range e.live {
		if slices.ContainsFunc(gcpPolicyPrefixes, func(p string) bool { return strings.HasPrefix(r.Type, p) }) {
			gaps = append(gaps, assurance.Gap{
				Kind: evaluate.GapUnsupportedKind, Detail: fmt.Sprintf("%s: firewall policy %s", r.Address, r.Type),
			})
		}
	}

	return gaps
}
