package cloudfw

import (
	"fmt"
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

// awsEndpoint is one end of an AWS query: its security groups, its address set, the pending rules of attachments
// that may add groups to it but do not resolve, and a view of each of its network interfaces (see
// awsInterfaceViews).
type awsEndpoint struct {
	groups  []*plan.Resource
	pending []awsRule
	ifaces  []*awsEndpoint
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
// destination, and an ingress rule of a group of the destination must allow the source. Between endpoints with
// several network interfaces, `allowed` also needs one pair of interfaces that allows the traffic.
func (e *Evaluator) evaluateAWS(query *Query, source, target *plan.Resource) Result {
	src, srcGaps := e.awsEndpoint(source)
	dst, dstGaps := e.awsEndpoint(target)

	if gaps := slices.Concat(srcGaps, dstGaps); len(gaps) > 0 {
		return unknownFrom(gaps)
	}

	res := decide(e.awsSide(query, src, dst, &awsEgress), e.awsSide(query, dst, src, &awsIngress))
	if res.Verdict != VerdictAllowed || awsOneInterfaceEach(src, dst) {
		return res
	}

	if pair, found := e.awsInterfacePair(query, src, dst); found {
		return pair
	}

	return unknownFrom([]assurance.Gap{awsNoInterfacePair(source, target)})
}

// awsOneInterfaceEach reports whether neither endpoint has more than one network interface.
func awsOneInterfaceEach(src, dst *awsEndpoint) bool {
	return !src.severalInterfaces() && !dst.severalInterfaces()
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

	viaInterfaces := awsGroupsFromInterfaces(res)

	groups, gaps := e.awsOwnGroups(res, &spec, viaInterfaces)
	if len(gaps) > 0 {
		return nil, gaps
	}

	attached := e.awsAttachments(res)
	endpoint := &awsEndpoint{
		groups: sortResources(slices.Concat(groups, attached.groups)), pending: attached.pending,
		addrs:  e.awsEndpointAddrs(res, &spec, attached, viaInterfaces),
		ifaces: e.awsInterfaceViews(res, &spec, groups, attached, viaInterfaces),
	}

	if viaInterfaces && endpoint.namesNothing() {
		return nil, []assurance.Gap{awsNoGroup(res)}
	}

	return endpoint, nil
}

// awsOwnGroups resolves the groups that the endpoint names itself. An instance whose groups come from its
// `network_interface` blocks names none, and needs exactly one block that is known to be its primary interface.
// Otherwise an endpoint that names no group is a gap: the default group of the VPC is not in the plan.
func (e *Evaluator) awsOwnGroups(res *plan.Resource, spec *awsEndpointSpec, viaInterfaces bool) ([]*plan.Resource, []assurance.Gap) {
	if viaInterfaces && !awsPrimaryBlock(res) {
		return nil, []assurance.Gap{{
			Kind: evaluate.GapUnresolvedReference,
			Detail: res.Address + " has no network_interface block whose device_index is known to be 0, so its " +
				"primary interface is not known",
		}}
	}

	if viaInterfaces {
		return nil, nil
	}

	check := &side{}

	groups, resolved := e.refsOfType(res, spec.groups, check, awsGroupTypes...)
	if !resolved {
		return nil, check.gaps
	}

	if len(groups) == 0 {
		return nil, []assurance.Gap{awsNoGroup(res)}
	}

	return groups, nil
}

// awsNoGroup is the gap of an endpoint that names no security group.
func awsNoGroup(res *plan.Resource) assurance.Gap {
	return assurance.Gap{
		Kind:   evaluate.GapUnresolvedReference,
		Detail: res.Address + " names no security group; the default group of the VPC is not in the plan",
	}
}

// awsPrimaryBlock reports whether exactly one `network_interface` block of the instance has a known `device_index`
// of 0. Without it, the primary interface has groups and an address that the blocks do not give.
func awsPrimaryBlock(res *plan.Resource) bool {
	primaries := 0

	for i := range blockCount(res, awsInstanceNICs.list) {
		if index, ok := attrInt(res, fmt.Sprintf("%s[%d].device_index", awsInstanceNICs.list, i)); ok && index == 0 {
			primaries++
		}
	}

	return primaries == 1
}

// awsGroupsFromInterfaces reports whether the groups of an instance are those of the network interfaces of its
// `network_interface` blocks: the plan lists those blocks, and the configuration, which covers the instance, does not
// set `vpc_security_group_ids`. That attribute is then computed from the primary interface, and the plan shows it
// unknown or empty.
func awsGroupsFromInterfaces(res *plan.Resource) bool {
	if res.Type != awsInstance || !leavesUnset(res, awsAttrVPCGroups) {
		return false
	}

	return !listUnknown(res, awsInstanceNICs.list) && blockCount(res, awsInstanceNICs.list) > 0
}

// namesNothing reports whether the endpoint has no group and no attachment that may add one.
func (endpoint *awsEndpoint) namesNothing() bool {
	return len(endpoint.groups) == 0 && len(endpoint.pending) == 0
}

// hasGroup reports whether the group is one of the groups of the endpoint.
func (endpoint *awsEndpoint) hasGroup(group *plan.Resource) bool {
	return slices.Contains(endpoint.groups, group)
}
