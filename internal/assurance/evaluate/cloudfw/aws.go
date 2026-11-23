package cloudfw

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// awsGroupTypes are the resource types of a security group. An attribute that holds security group IDs must name
// one of them, and an endpoint of one of these types is the group itself.
var awsGroupTypes = []string{"aws_security_group", "aws_default_security_group"}

// awsSubnet is the resource type of a subnet, whose `cidr_block` gives the addresses of an endpoint.
const awsSubnet = "aws_subnet"

// Attribute names that several AWS endpoint types or rule sources share.
const (
	awsAttrVPCGroups = "vpc_security_group_ids"
	awsAttrGroups    = "security_groups"
	awsAttrSubnetID  = "subnet_id"
	awsAttrPrivateIP = "private_ip"
	awsAttrGroupIDs  = "security_group_ids"
)

// awsEndpoint is one end of an AWS query: its security groups, its address set, and the pending rules of
// attachments that may add groups to it but do not resolve.
type awsEndpoint struct {
	groups  []*plan.Resource
	pending []awsRule
	addrs   addrSet
}

// awsEndpointSpec names the attributes of an endpoint type: its security groups, its subnets, and its private IP.
// An empty path means that the type has no such attribute.
type awsEndpointSpec struct {
	groups, subnets, ip string
}

// awsEndpointSpecs are the endpoint types that the evaluator supports, other than a bound security group.
var awsEndpointSpecs = map[string]awsEndpointSpec{
	"aws_instance":    {groups: awsAttrVPCGroups, subnets: awsAttrSubnetID, ip: awsAttrPrivateIP},
	"aws_db_instance": {groups: awsAttrVPCGroups},
	"aws_rds_cluster": {groups: awsAttrVPCGroups},
	"aws_lb":          {groups: awsAttrGroups, subnets: "subnets"},
	"aws_ecs_service": {
		groups: "network_configuration[0].security_groups", subnets: "network_configuration[0].subnets",
	},
	"aws_lambda_function":               {groups: "vpc_config[0].security_group_ids", subnets: "vpc_config[0].subnet_ids"},
	"aws_elasticache_cluster":           {groups: awsAttrGroupIDs},
	"aws_elasticache_replication_group": {groups: awsAttrGroupIDs},
	"aws_network_interface":             {groups: awsAttrGroups, subnets: awsAttrSubnetID, ip: awsAttrPrivateIP},
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
// known. Groups that other resources attach to the network interfaces of the endpoint are added; an attachment that
// may concern the endpoint but does not resolve becomes a pending rule that allows maybe.
func (e *Evaluator) awsEndpoint(res *plan.Resource) (*awsEndpoint, []assurance.Gap) {
	if slices.Contains(awsGroupTypes, res.Type) {
		return &awsEndpoint{groups: []*plan.Resource{res}, addrs: unknownAddrs}, nil
	}

	spec, ok := awsEndpointSpecs[res.Type]
	if !ok {
		return nil, []assurance.Gap{unsupportedResource(res)}
	}

	check := &side{}

	groups, resolved := e.refsOfType(res, spec.groups, check, awsGroupTypes...)
	if !resolved {
		return nil, check.gaps
	}

	if len(groups) == 0 {
		check.gap(evaluate.GapUnresolvedReference,
			res.Address+" names no security group; the default group of the VPC is not in the plan")

		return nil, check.gaps
	}

	attached := e.awsAttachments(res)
	endpoint := &awsEndpoint{
		groups: sortResources(slices.Concat(groups, attached.groups)), addrs: e.awsAddrs(res, &spec), pending: attached.pending,
	}

	// A second network interface gives the endpoint a second address.
	if attached.interfaces > 0 {
		endpoint.addrs = unknownAddrs
	}

	return endpoint, nil
}

// awsAddrs is the address set of an endpoint: its private IP when it is known, else the CIDR that all its subnets
// share, else unknown.
func (e *Evaluator) awsAddrs(res *plan.Resource, spec *awsEndpointSpec) addrSet {
	// An endpoint type without an IP attribute, or an IP unknown until apply, gives no host set.
	ip, _ := attrString(res, spec.ip)
	if set := hostSet(ip); set.known {
		return set
	}

	if spec.subnets == "" {
		return unknownAddrs
	}

	// A subnet that does not resolve leaves the addresses unknown; a rule prefix that depends on them then gives
	// the gap address-dependent-range.
	subnets, ok := e.refsOfType(res, spec.subnets, &side{}, awsSubnet)
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
func (endpoint *awsEndpoint) hasGroup(group *plan.Resource) bool {
	return slices.Contains(endpoint.groups, group)
}
