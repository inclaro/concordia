package cloudfw

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// awsSecurityGroup is the resource type of a security group, which can also be bound directly as an endpoint.
const awsSecurityGroup = "aws_security_group"

// awsEndpoint is one end of an AWS query: its security groups and its address set.
type awsEndpoint struct {
	groups []*plan.Resource
	addrs  addrSet
}

// awsEndpointSpec names the attributes of an endpoint type: its security groups, its subnets, and its private IP.
// An empty path means that the type has no such attribute.
type awsEndpointSpec struct {
	groups, subnets, ip string
}

// awsEndpointSpecs are the endpoint types that the evaluator supports, other than a bound security group.
var awsEndpointSpecs = map[string]awsEndpointSpec{
	"aws_instance":                      {groups: "vpc_security_group_ids", subnets: "subnet_id", ip: "private_ip"},
	"aws_db_instance":                   {groups: "vpc_security_group_ids"},
	"aws_rds_cluster":                   {groups: "vpc_security_group_ids"},
	"aws_lb":                            {groups: "security_groups", subnets: "subnets"},
	"aws_ecs_service":                   {groups: "network_configuration[0].security_groups", subnets: "network_configuration[0].subnets"},
	"aws_lambda_function":               {groups: "vpc_config[0].security_group_ids", subnets: "vpc_config[0].subnet_ids"},
	"aws_elasticache_cluster":           {groups: "security_group_ids"},
	"aws_elasticache_replication_group": {groups: "security_group_ids"},
	"aws_network_interface":             {groups: "security_groups", subnets: "subnet_id", ip: "private_ip"},
}

// evaluateAWS answers a query between two AWS resources: an egress rule of a group of the source must allow the
// destination, and an ingress rule of a group of the destination must allow the source.
func (e *Evaluator) evaluateAWS(q *Query, from, to *plan.Resource) Result {
	src, srcGaps := e.awsEndpoint(from)
	dst, dstGaps := e.awsEndpoint(to)

	if gaps := slices.Concat(srcGaps, dstGaps); len(gaps) > 0 {
		return unknownFrom(gaps)
	}

	return decide(e.awsSide(q, src, dst, &awsEgress), e.awsSide(q, dst, src, &awsIngress))
}

// awsEndpoint resolves the security groups and the address set of an endpoint. The gaps say why the groups are not
// known.
func (e *Evaluator) awsEndpoint(r *plan.Resource) (*awsEndpoint, []assurance.Gap) {
	if r.Type == awsSecurityGroup {
		return &awsEndpoint{groups: []*plan.Resource{r}, addrs: unknownAddrs}, nil
	}

	spec, ok := awsEndpointSpecs[r.Type]
	if !ok {
		return nil, []assurance.Gap{unsupportedResource(r)}
	}

	s := &side{}

	groups, resolved := e.refs(r, spec.groups, s)
	if !resolved {
		return nil, s.gaps
	}

	if len(groups) == 0 {
		s.gap(evaluate.GapUnresolvedReference, r.Address+" names no security group; the default group of the VPC is not in the plan")

		return nil, s.gaps
	}

	return &awsEndpoint{groups: groups, addrs: e.awsAddrs(r, &spec)}, nil
}

// awsAddrs is the address set of an endpoint: its private IP when it is known, else the CIDR that all its subnets
// share, else unknown.
func (e *Evaluator) awsAddrs(r *plan.Resource, spec *awsEndpointSpec) addrSet {
	// An endpoint type without an IP attribute, or an IP unknown until apply, gives no host set.
	ip, _ := attrString(r, spec.ip)
	if set := hostSet(ip); set.known {
		return set
	}

	if spec.subnets == "" {
		return unknownAddrs
	}

	// A subnet that does not resolve leaves the addresses unknown; a rule prefix that depends on them then gives
	// the gap address-dependent-range.
	subnets, ok := e.refs(r, spec.subnets, &side{})
	if !ok {
		return unknownAddrs
	}

	sets := make([]addrSet, 0, len(subnets))
	for _, subnet := range subnets {
		cidr, _ := attrString(subnet, "cidr_block")
		sets = append(sets, prefixSet(cidr))
	}

	return commonSet(sets...)
}

// hasGroup reports whether the group is one of the groups of the endpoint.
func (ep *awsEndpoint) hasGroup(group *plan.Resource) bool {
	return slices.Contains(ep.groups, group)
}
