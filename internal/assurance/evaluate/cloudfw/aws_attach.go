package cloudfw

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// Resource types that attach security groups or network interfaces to an endpoint from outside it.
const (
	awsInstance         = "aws_instance"
	awsNetworkInterface = "aws_network_interface"
	awsSGAttachment     = "aws_network_interface_sg_attachment"
	awsNICAttachment    = "aws_network_interface_attachment"
)

// awsAttached are the groups that other resources attach to the network interfaces of an endpoint, the number of
// network interfaces attached besides the endpoint's own, and a pending rule for each attachment that may concern
// the endpoint but does not resolve.
type awsAttached struct {
	groups     []*plan.Resource
	pending    []awsRule
	interfaces int
}

// awsLink asks whether an attribute of an attachment names one of the targets: by a configuration reference to a
// target, or by a known value among ids.
type awsLink struct {
	res     *plan.Resource
	subject string
	path    string
	targets []*plan.Resource
	ids     []string
}

// awsAttachments finds what other resources attach to an instance or a network interface endpoint.
func (e *Evaluator) awsAttachments(endpoint *plan.Resource) *awsAttached {
	out := &awsAttached{}
	if endpoint.Type != awsInstance && endpoint.Type != awsNetworkInterface {
		return out
	}

	interfaces := e.awsInterfaces(endpoint, out)
	out.interfaces = len(interfaces) - 1

	for _, nic := range interfaces[1:] {
		e.awsInterfaceGroups(nic, endpoint, out)
	}

	e.awsGroupAttachments(endpoint, interfaces, out)

	return out
}

// awsInterfaces are the endpoint (which stands for its primary network interface) and the network interfaces that
// are attached to an instance endpoint.
func (e *Evaluator) awsInterfaces(endpoint *plan.Resource, out *awsAttached) []*plan.Resource {
	interfaces := []*plan.Resource{endpoint}
	if endpoint.Type != awsInstance {
		return interfaces
	}

	link := &awsLink{subject: endpoint.Address, targets: interfaces[:1:1], ids: awsIDs(endpoint)}
	interfaces = e.awsAttachmentInterfaces(link, interfaces, out)
	interfaces = e.awsInstanceBlockInterfaces(endpoint, interfaces, out)

	return e.awsBlockInterfaces(link, interfaces, out)
}

// awsInstanceNICs is the `network_interface` block list of an instance, which attaches network interfaces by ID.
var awsInstanceNICs = blockElement{list: "network_interface", field: "network_interface_id"}

// awsInstanceBlockInterfaces adds the network interfaces of the `network_interface` blocks of the instance. A block
// list that is unknown until apply, or a block whose interface does not resolve, makes the instance pending.
func (e *Evaluator) awsInstanceBlockInterfaces(instance *plan.Resource, interfaces []*plan.Resource, out *awsAttached) []*plan.Resource {
	link := &awsLink{res: instance, subject: instance.Address}

	if listUnknown(instance, awsInstanceNICs.list) {
		out.pend(link, awsInstanceNICs.list, []assurance.Gap{
			{Kind: evaluate.GapUnknownAfterApply, Detail: instance.Address + "." + awsInstanceNICs.list},
		})

		return interfaces
	}

	for i := range blockCount(instance, awsInstanceNICs.list) {
		check := &side{}

		nics, ok := e.blockRefsOfType(instance, awsInstanceNICs.at(i), check, awsNetworkInterface)
		if !ok {
			out.pend(link, awsInstanceNICs.at(i).path(), check.gaps)
		}

		for _, nic := range nics {
			interfaces = appendUnique(interfaces, nic)
		}
	}

	return interfaces
}

// awsAttachmentInterfaces adds the network interfaces that an aws_network_interface_attachment attaches to the
// instance.
func (e *Evaluator) awsAttachmentInterfaces(link *awsLink, interfaces []*plan.Resource, out *awsAttached) []*plan.Resource {
	for _, attachment := range e.ofType(awsNICAttachment) {
		link.res, link.path = attachment, "instance_id"
		if e.awsConcerns(link, out) {
			interfaces = e.awsAttachedInterface(link, interfaces, out)
		}
	}

	return interfaces
}

// awsBlockInterfaces adds the network interfaces whose `attachment` block names the instance. An interface whose
// attachment names an instance that the plan does not give makes the instance pending.
func (e *Evaluator) awsBlockInterfaces(link *awsLink, interfaces []*plan.Resource, out *awsAttached) []*plan.Resource {
	for _, nic := range e.ofType(awsNetworkInterface) {
		link.res = nic
		interfaces = e.awsBlockInterface(link, interfaces, out)
	}

	return interfaces
}

// awsBlockInterface adds the network interface of the link when its `attachment` block names the instance, and
// makes the instance pending when the block names an instance that the plan does not give.
func (e *Evaluator) awsBlockInterface(link *awsLink, interfaces []*plan.Resource, out *awsAttached) []*plan.Resource {
	switch {
	case awsAttachedByID(link) || e.awsAttachedByReference(link, out):
		return appendUnique(interfaces, link.res)
	case awsAttachmentUnknown(link.res):
		out.pend(link, awsNICAttachments.list, []assurance.Gap{
			{Kind: evaluate.GapUnknownAfterApply, Detail: link.res.Address + "." + awsNICAttachments.wildcard()},
		})
	}

	return interfaces
}

// awsNICAttachments is the `attachment` block list of a network interface, which names the instance it is attached to.
var awsNICAttachments = blockElement{list: "attachment", field: "instance"}

// awsAttachmentUnknown reports whether an `attachment` block of the network interface names an instance that is
// unknown until apply and that no configuration reference names: the list, or a whole block, is unknown, or more
// blocks have an unknown instance than have references.
func awsAttachmentUnknown(nic *plan.Resource) bool {
	if listUnknown(nic, awsNICAttachments.list) {
		return true
	}

	return unknownElements(nic, awsNICAttachments) > len(referenceSets(nic, awsNICAttachments))
}

// awsAttachedInterface adds the network interface of an aws_network_interface_attachment that concerns the
// endpoint.
func (e *Evaluator) awsAttachedInterface(link *awsLink, interfaces []*plan.Resource, out *awsAttached) []*plan.Resource {
	check := &side{}

	nic, ok := e.refOfType(link.res, "network_interface_id", check, awsNetworkInterface)
	if !ok || nic == nil {
		out.pend(link, "network_interface_id", check.gaps)

		return interfaces
	}

	return appendUnique(interfaces, nic)
}

// awsAttachedByID reports whether a known `instance` of an `attachment` block of the network interface is the
// instance.
func awsAttachedByID(link *awsLink) bool {
	for i := range blockCount(link.res, "attachment") {
		if id, ok := attrString(link.res, fmt.Sprintf("attachment[%d].instance", i)); ok && slices.Contains(link.ids, id) {
			return true
		}
	}

	return false
}

// awsAttachedByReference reports whether the configuration of an `attachment` block of the network interface names
// the instance. The blocks are a set, so every block's references count.
func (e *Evaluator) awsAttachedByReference(link *awsLink, out *awsAttached) bool {
	for _, key := range slices.Sorted(maps.Keys(link.res.References)) {
		if !strings.HasPrefix(key, "attachment[") || !strings.HasSuffix(key, "].instance") {
			continue
		}

		link.path = key
		if e.awsNamesTarget(link, link.res.References[key], out) {
			return true
		}
	}

	return false
}

// awsInterfaceGroups adds the groups of a network interface that is attached to the endpoint.
func (e *Evaluator) awsInterfaceGroups(nic, endpoint *plan.Resource, out *awsAttached) {
	check := &side{}

	groups, ok := e.refsOfType(nic, awsAttrGroups, check, awsGroupTypes...)
	if !ok {
		out.pend(&awsLink{res: nic, subject: endpoint.Address}, awsAttrGroups, check.gaps)
	}

	out.groups = append(out.groups, groups...)
}

// awsGroupAttachments adds the group of each aws_network_interface_sg_attachment that names a network interface of
// the endpoint.
func (e *Evaluator) awsGroupAttachments(endpoint *plan.Resource, interfaces []*plan.Resource, out *awsAttached) {
	link := &awsLink{subject: endpoint.Address, targets: interfaces, path: "network_interface_id"}
	for _, nic := range interfaces {
		link.ids = append(link.ids, awsIDs(nic)...)
	}

	for _, attachment := range e.ofType(awsSGAttachment) {
		link.res = attachment
		if !e.awsConcerns(link, out) {
			continue
		}

		check := &side{}

		group, ok := e.refOfType(attachment, "security_group_id", check, awsGroupTypes...)
		if !ok || group == nil {
			out.pend(link, "security_group_id", check.gaps)

			continue
		}

		out.groups = append(out.groups, group)
	}
}

// awsIDs are the known IDs by which an attachment can name the resource: its `id`, and the
// `primary_network_interface_id` of an instance.
func awsIDs(res *plan.Resource) []string {
	var ids []string

	for _, path := range []string{"id", "primary_network_interface_id"} {
		if id, ok := attrString(res, path); ok && id != "" {
			ids = append(ids, id)
		}
	}

	return ids
}

// awsConcerns reports whether the attribute of the link names a target. A known value names a target when it is one
// of the ids: a resource that the plan creates has no known ID yet, so a known value never names it. An unknown value
// names a target through its configuration references; when they do not resolve, the attachment is pending.
func (e *Evaluator) awsConcerns(link *awsLink, out *awsAttached) bool {
	if value, ok := attrString(link.res, link.path); ok {
		return slices.Contains(link.ids, value)
	}

	if !unknownAt(link.res, link.path) {
		return false
	}

	return e.awsNamesTarget(link, link.res.References[planPath(link.path)], out)
}

// awsNamesTarget resolves configuration addresses and reports whether one is a target. Addresses that do not
// resolve, or no addresses at all, make the attachment pending.
func (e *Evaluator) awsNamesTarget(link *awsLink, addresses []string, out *awsAttached) bool {
	check := &side{}

	if len(addresses) == 0 {
		check.gap(evaluate.GapUnknownAfterApply, link.res.Address+"."+planPath(link.path))
		out.pend(link, link.path, check.gaps)

		return false
	}

	found, ok := e.resolveAll(addresses, check)
	if slices.ContainsFunc(found, func(res *plan.Resource) bool { return slices.Contains(link.targets, res) }) {
		return true
	}

	if !ok {
		out.pend(link, link.path, check.gaps)
	}

	return false
}

// pend records an attachment that may add a group to the endpoint but does not resolve: a rule that applies maybe in
// both directions.
func (out *awsAttached) pend(link *awsLink, path string, gaps []assurance.Gap) {
	gaps = append(slices.Clip(gaps), assurance.Gap{
		Kind: evaluate.GapUnresolvedReference,
		Detail: fmt.Sprintf("%s.%s does not resolve, so it may attach a security group to %s",
			link.res.Address, planPath(path), link.subject),
	})
	out.pending = append(out.pending, awsRule{res: link.res, name: "attachment", applies: triMaybe, gaps: gaps})
}

// mayHaveGroup is maybe when an unresolved attachment may add a group to the endpoint, with the gaps of the
// attachments, and no otherwise.
func (endpoint *awsEndpoint) mayHaveGroup(check *side) tri {
	if len(endpoint.pending) == 0 {
		return triNo
	}

	for _, rule := range endpoint.pending {
		check.gaps = append(check.gaps, rule.gaps...)
	}

	return triMaybe
}
