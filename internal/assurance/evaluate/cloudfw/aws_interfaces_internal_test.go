package cloudfw

import (
	"encoding/json"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// TestInstanceNetworkInterfaceBlocks: the `network_interface` blocks of an instance attach interfaces
// whose groups count.
func TestInstanceNetworkInterfaceBlocks(t *testing.T) {
	t.Parallel()

	instance := func(references map[string][]string) []plan.Resource {
		web := resourceOf(awsWebInstance, "aws_instance", map[string]any{
			"vpc_security_group_ids": []any{idApp}, "network_interface": []any{map[string]any{"device_index": "1"}},
		})
		web.Unknown = []string{"network_interface[0].network_interface_id"}
		web.References = references

		return []plan.Resource{
			web, group(awsAppGroup, idApp, []any{}, []any{}), group(awsExtraGroup, idExtra, []any{}, []any{allTraffic()}),
			resourceOf(awsExtraNIC, awsNetworkInterface, map[string]any{"id": "eni-extra", "security_groups": []any{idExtra}}),
			openDB(),
		}
	}

	runQueryCases(t, []queryCase{
		{
			name:      "interface resolves by configuration",
			resources: instance(map[string][]string{"network_interface[0].network_interface_id": {awsExtraNIC}}),
			from:      awsWebInstance, to: awsDBGroup, want: VerdictAllowed,
		},
		{
			name: "interface does not resolve", resources: instance(nil), from: awsWebInstance, to: awsDBGroup, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": " + awsWebInstance + ".network_interface[0].network_interface_id " +
				"does not resolve, so it may attach a security group to " + awsWebInstance},
		},
	})
}

// TestInstancePrimaryInterfaceBlock: an instance takes the groups and addresses of
// its `network_interface` blocks only when exactly one block is known to be its primary interface (device index 0).
// Otherwise its primary interface has groups that the blocks do not give.
func TestInstancePrimaryInterfaceBlock(t *testing.T) {
	t.Parallel()

	resources := func(index any) []plan.Resource {
		block := map[string]any{"network_interface_id": "eni-extra"}

		web := resourceOf(awsWebInstance, "aws_instance", map[string]any{"vpc_security_group_ids": []any{}})
		web.Configured = []string{"network_interface", "network_interface[0].device_index",
			"network_interface[0].network_interface_id"}

		if index == nil {
			web.Unknown = []string{"network_interface[0].device_index"}
		} else {
			block["device_index"] = index
		}

		web.Values["network_interface"] = []any{block}

		return []plan.Resource{
			web, group(awsExtraGroup, idExtra, []any{}, []any{allTraffic()}), openDB(),
			resourceOf(awsExtraNIC, awsNetworkInterface, map[string]any{"id": "eni-extra", "security_groups": []any{idExtra}}),
		}
	}
	noPrimary := []string{evaluate.GapUnresolvedReference + ": " + awsWebInstance + " has no network_interface block whose " +
		"device_index is known to be 0, so its primary interface is not known"}

	runQueryCases(t, []queryCase{
		{name: "primary block", resources: resources(json.Number("0")), from: awsWebInstance, to: awsDBGroup, want: VerdictAllowed},
		{
			name: "secondary block only", resources: resources(json.Number("1")), from: awsWebInstance, to: awsDBGroup,
			want: VerdictUnknown, gaps: noPrimary,
		},
		{
			name: "device index unknown until apply", resources: resources(nil), from: awsWebInstance, to: awsDBGroup,
			want: VerdictUnknown, gaps: noPrimary,
		},
	})
}

// TestAttachedInterfaceWithoutGroup: a network interface attached to an instance that names no group
// of its own gets the default group of the VPC, which is not in the plan, so a side that no rule allows is unknown.
func TestAttachedInterfaceWithoutGroup(t *testing.T) {
	t.Parallel()

	resources := func(nicGroups []any) []plan.Resource {
		return []plan.Resource{
			resourceOf(awsWebInstance, "aws_instance", map[string]any{"id": "i-web", "vpc_security_group_ids": []any{idApp}}),
			group(awsAppGroup, idApp, []any{}, []any{}),
			group(awsDBGroup, idDB, []any{}, []any{allTraffic()}),
			group(awsExtraGroup, idExtra, []any{}, []any{}),
			resourceOf(awsExtraNIC, awsNetworkInterface, map[string]any{
				"id": "eni-extra", "security_groups": nicGroups, "attachment": []any{map[string]any{"instance": "i-web"}},
			}),
		}
	}

	runQueryCases(t, []queryCase{
		{
			name: "attached interface with no group", resources: resources([]any{}), from: awsDBGroup, to: awsWebInstance,
			want: VerdictUnknown, gaps: []string{evaluate.GapUnresolvedReference + ": " + awsExtraNIC +
				" names no security group; the default group of the VPC is not in the plan"},
		},
		{
			name: "attached interface with a closed group", resources: resources([]any{idExtra}), from: awsDBGroup,
			to: awsWebInstance, want: VerdictDenied,
		},
	})
}

// TestAttachmentBlockWithUnknownInstance: a network interface whose `attachment` names an instance
// that is unknown until apply, without a configuration reference, may be attached to the endpoint.
func TestAttachmentBlockWithUnknownInstance(t *testing.T) {
	t.Parallel()

	floating := resourceOf("aws_network_interface.floating", awsNetworkInterface, map[string]any{
		"id": "eni-floating", "security_groups": []any{idExtra}, "attachment": []any{map[string]any{"device_index": "1"}},
	})
	floating.Unknown = []string{"attachment[0].instance"}

	runQueryCases(t, []queryCase{{
		name: "attachment instance unknown without reference",
		resources: []plan.Resource{
			resourceOf(awsWebInstance, "aws_instance", map[string]any{"id": "i-web", "vpc_security_group_ids": []any{idApp}}),
			group(awsAppGroup, idApp, []any{}, []any{}), group(awsExtraGroup, idExtra, []any{}, []any{allTraffic()}),
			floating, openDB(),
		},
		from: awsWebInstance, to: awsDBGroup, want: VerdictUnknown,
		gaps: []string{
			evaluate.GapUnknownAfterApply + ": aws_network_interface.floating.attachment[*].instance",
			evaluate.GapUnresolvedReference + ": aws_network_interface.floating.attachment does not resolve, so it may " +
				"attach a security group to " + awsWebInstance,
		},
	}})
}

// otherInstance is a second instance that a configuration reference of an attachment names.
const otherInstance = "aws_instance.b"

// TestKnownAttachmentInstanceWins: when the plan knows the instance of every
// `attachment` block of an interface, a configuration reference to another instance does not attach it there.
func TestKnownAttachmentInstanceWins(t *testing.T) {
	t.Parallel()

	nic := resourceOf(awsExtraNIC, awsNetworkInterface, map[string]any{
		"id": "eni-extra", "security_groups": []any{idExtra}, "attachment": []any{map[string]any{"instance": "i-b"}},
	})
	nic.References = map[string][]string{"attachment[0].instance": {awsWebInstance, otherInstance}}

	runQueryCases(t, []queryCase{{
		name: "known instance of another block", from: awsAppGroup, to: awsWebInstance, want: VerdictDenied,
		resources: []plan.Resource{
			resourceOf(awsWebInstance, awsInstance, map[string]any{"vpc_security_group_ids": []any{idApp}, "private_ip": "10.0.0.5"}),
			resourceOf(otherInstance, awsInstance, map[string]any{"id": "i-b", "vpc_security_group_ids": []any{idApp}}),
			group(awsAppGroup, idApp, []any{}, []any{allTraffic()}),
			group(awsExtraGroup, idExtra, []any{postgresFrom(map[string]any{"cidr_blocks": []any{"0.0.0.0/0"}})}, []any{}),
			nic,
		},
	}})
}

// TestAttachmentThatMayNameAnotherTarget: an attachment whose unknown target the configuration takes from one of
// several resources may attach to any of them, so it does not attach to the endpoint for certain.
func TestAttachmentThatMayNameAnotherTarget(t *testing.T) {
	t.Parallel()

	world := func(attachment plan.Resource) []plan.Resource {
		return []plan.Resource{
			resourceOf(awsWebInstance, awsInstance, map[string]any{"vpc_security_group_ids": []any{idApp}, "private_ip": "10.0.0.5"}),
			resourceOf(otherInstance, awsInstance, map[string]any{"vpc_security_group_ids": []any{idApp}}),
			resourceOf(awsExtraNIC, awsNetworkInterface, map[string]any{"id": "eni-extra", "security_groups": []any{idExtra}}),
			group(awsAppGroup, idApp, []any{}, []any{}),
			group(awsExtraGroup, idExtra, []any{}, []any{allTraffic()}),
			openDB(), attachment,
		}
	}

	nicAttachment := resourceOf("aws_network_interface_attachment.extra", awsNICAttachment, map[string]any{
		awsAttrNICID: "eni-extra", "device_index": "1",
	})
	nicAttachment.Unknown = []string{"instance_id"}
	nicAttachment.References = map[string][]string{"instance_id": {awsWebInstance, otherInstance}}

	sgAttachment := resourceOf("aws_network_interface_sg_attachment.extra", awsSGAttachment, map[string]any{
		"security_group_id": idExtra,
	})
	sgAttachment.Unknown = []string{awsAttrNICID}
	sgAttachment.References = map[string][]string{awsAttrNICID: {awsWebInstance, otherInstance}}

	runQueryCases(t, []queryCase{
		{
			name: "interface attachment to one of two instances", resources: world(nicAttachment),
			from: awsWebInstance, to: awsDBGroup, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": aws_network_interface_attachment.extra.instance_id names 2 resources"},
		},
		{
			name: "group attachment to one of two instances", resources: world(sgAttachment),
			from: awsWebInstance, to: awsDBGroup, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": aws_network_interface_sg_attachment.extra.network_interface_id " +
				"names 2 resources"},
		},
	})
}

// TestUnresolvedInterfaceKeepsAddressesOpen: an attachment that does not resolve may attach another network
// interface to the instance, whose address may be in a range that a rule allows.
func TestUnresolvedInterfaceKeepsAddressesOpen(t *testing.T) {
	t.Parallel()

	attachment := resourceOf("aws_network_interface_attachment.floating", awsNICAttachment, map[string]any{
		"instance_id": "i-web", "device_index": "1",
	})
	attachment.Unknown = []string{awsAttrNICID}

	runQueryCases(t, []queryCase{{
		name: "interface attachment that does not resolve", from: awsWebInstance, to: awsDBGroup, want: VerdictUnknown,
		resources: []plan.Resource{
			resourceOf(awsWebInstance, awsInstance, map[string]any{
				"id": "i-web", "vpc_security_group_ids": []any{idApp}, "private_ip": "10.0.1.10",
			}),
			group(awsAppGroup, idApp, []any{}, []any{allTraffic()}),
			group(awsDBGroup, idDB, []any{postgresFrom(map[string]any{"cidr_blocks": []any{"10.0.2.0/24"}})}, []any{}),
			attachment,
		},
		gaps: []string{evaluate.GapAddressDependent + ": 10.0.2.0/24 against an endpoint whose addresses are not known before apply"},
	}})
}

// TestSeveralInterfacesNeedOnePair: traffic leaves through one network interface, so the egress rule and the
// ingress rule that allow it must hold for the groups of the same interface.
func TestSeveralInterfacesNeedOnePair(t *testing.T) {
	t.Parallel()

	world := func(dbAllows string) []plan.Resource {
		return []plan.Resource{
			resourceOf(awsWebInstance, awsInstance, map[string]any{
				"id": "i-web", "vpc_security_group_ids": []any{idApp}, "private_ip": "10.0.1.10",
			}),
			resourceOf(awsExtraNIC, awsNetworkInterface, map[string]any{
				"id": "eni-extra", "security_groups": []any{idExtra}, "private_ip": "10.0.1.11",
				"attachment": []any{map[string]any{"instance": "i-web"}},
			}),
			group(awsAppGroup, idApp, []any{}, []any{}),
			group(awsExtraGroup, idExtra, []any{}, []any{allTraffic()}),
			group(awsDBGroup, idDB, []any{postgresFrom(map[string]any{"security_groups": []any{dbAllows}})}, []any{}),
		}
	}

	runQueryCases(t, []queryCase{
		{
			name: "egress on one interface and ingress for the other", resources: world(idApp),
			from: awsWebInstance, to: awsDBGroup, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnsupportedResource + ": " + awsWebInstance + " to " + awsDBGroup + ": the groups " +
				"of several network interfaces together allow the traffic, but no one pair of network interfaces is shown " +
				"to allow it"},
		},
		{
			name: "egress and ingress for the same interface", resources: world(idExtra),
			from: awsWebInstance, to: awsDBGroup, want: VerdictAllowed,
		},
	})
}
