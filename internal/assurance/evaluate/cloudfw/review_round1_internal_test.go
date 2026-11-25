package cloudfw

import (
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// Addresses of the review round 1 scenarios.
const (
	r1Source   = "aws_network_interface.src"
	r1Subnet   = "aws_subnet.app"
	r1Instance = "aws_instance.b"
)

// r1SourceNIC is a network interface in the group app whose primary IP is 10.0.0.10, whose `private_ips` list is
// unknown until apply, in the subnet 10.0.0.0/24. configured are the keys that its configuration sets.
func r1SourceNIC(configured []string) plan.Resource {
	nic := resourceOf(r1Source, awsNetworkInterface, map[string]any{
		"security_groups": []any{idApp}, "private_ip": "10.0.0.10", "subnet_id": "subnet-app",
	})
	nic.Unknown = []string{"private_ips"}
	nic.Configured = configured

	return nic
}

// TestUnknownPrivateIPsKeepAddressesOpen is review round 1, finding 4: an interface whose `private_ips` list is
// unknown may have secondary addresses, so its primary IP is not its whole address set, unless the configuration
// sets no secondary address.
func TestUnknownPrivateIPsKeepAddressesOpen(t *testing.T) {
	t.Parallel()

	world := func(configured []string) []plan.Resource {
		return []plan.Resource{
			r1SourceNIC(configured), group(reviewApp, idApp, []any{}, []any{allTraffic()}),
			group(reviewDB, idDB, []any{postgresFrom(map[string]any{"cidr_blocks": []any{"10.0.0.11/32"}})}, []any{}),
			resourceOf(r1Subnet, awsSubnet, map[string]any{"id": "subnet-app", "cidr_block": "10.0.0.0/24"}),
		}
	}

	runReviewCases(t, []reviewCase{
		{
			name: "configuration may set secondary addresses", resources: world([]string{"private_ips_count", "subnet_id"}),
			from: r1Source, to: reviewDB, want: VerdictUnknown,
			gaps: []string{evaluate.GapAddressDependent + ": 10.0.0.11/32 covers only part of 10.0.0.0/24"},
		},
		{
			name: "configuration sets no secondary address", resources: world([]string{"subnet_id", "security_groups"}),
			from: r1Source, to: reviewDB, want: VerdictDenied,
		},
	})
}

// TestRuleResourceWithUnknownType is review round 1, finding 5: an aws_security_group_rule whose `type` is unknown
// until apply may be a rule of either direction.
func TestRuleResourceWithUnknownType(t *testing.T) {
	t.Parallel()

	rule := resourceOf("aws_security_group_rule.any", awsGroupRule, map[string]any{
		"security_group_id": idDB, "protocol": "tcp", "from_port": "5432", "to_port": "5432", "cidr_blocks": []any{"0.0.0.0/0"},
	})
	rule.Unknown = []string{"type"}

	runReviewCases(t, []reviewCase{{
		name: "unknown type", from: reviewApp, to: reviewDB, want: VerdictUnknown,
		resources: []plan.Resource{
			group(reviewApp, idApp, []any{}, []any{allTraffic()}), group(reviewDB, idDB, []any{}, []any{}), rule,
		},
		gaps: []string{evaluate.GapUnknownAfterApply + ": aws_security_group_rule.any.type"},
	}})
}

// TestInlineRuleWithUnknownSelf is review round 1, finding 6: an inline rule whose `self` is unknown until apply may
// select its own group.
func TestInlineRuleWithUnknownSelf(t *testing.T) {
	t.Parallel()

	db := group(reviewDB, idDB, []any{postgresFrom(map[string]any{})}, []any{allTraffic()})
	db.Unknown = []string{"ingress[0].self"}

	runReviewCases(t, []reviewCase{{
		name: "unknown self", resources: []plan.Resource{db}, from: reviewDB, to: reviewDB, want: VerdictUnknown,
		gaps: []string{evaluate.GapUnknownAfterApply + ": " + reviewDB + ".ingress[0].self"},
	}})
}

// TestKnownAttachmentInstanceWins is review round 1, finding 26: when the plan knows the instance of every
// `attachment` block of an interface, a configuration reference to another instance does not attach it there.
func TestKnownAttachmentInstanceWins(t *testing.T) {
	t.Parallel()

	nic := resourceOf(reviewNIC, awsNetworkInterface, map[string]any{
		"id": "eni-extra", "security_groups": []any{idExtra}, "attachment": []any{map[string]any{"instance": "i-b"}},
	})
	nic.References = map[string][]string{"attachment[0].instance": {reviewWeb, r1Instance}}

	runReviewCases(t, []reviewCase{{
		name: "known instance of another block", from: reviewApp, to: reviewWeb, want: VerdictDenied,
		resources: []plan.Resource{
			resourceOf(reviewWeb, awsInstance, map[string]any{"vpc_security_group_ids": []any{idApp}, "private_ip": "10.0.0.5"}),
			resourceOf(r1Instance, awsInstance, map[string]any{"id": "i-b", "vpc_security_group_ids": []any{idApp}}),
			group(reviewApp, idApp, []any{}, []any{allTraffic()}),
			group(reviewExtra, idExtra, []any{postgresFrom(map[string]any{"cidr_blocks": []any{"0.0.0.0/0"}})}, []any{}),
			nic,
		},
	}})
}

// TestPortRangeOutsideTheValidPorts is review round 1, finding 31: a port range beyond 65535 does not parse.
func TestPortRangeOutsideTheValidPorts(t *testing.T) {
	t.Parallel()

	for spec, want := range map[string]tri{"1-65536": triMaybe, "0-65535": triYes, "5432": triYes, "*": triYes} {
		if got := matchPortSpec(spec, postgres, &side{}); got != want {
			t.Errorf("matchPortSpec(%q) = %v, want %v", spec, got, want)
		}
	}
}

// TestMappedIPv6PrefixStaysIPv6 is review round 1, finding 25: an IPv4-mapped IPv6 prefix is an IPv6 prefix, so it
// never decides.
func TestMappedIPv6PrefixStaysIPv6(t *testing.T) {
	t.Parallel()

	if got := matchPrefix("::ffff:10.0.0.1/24", hostSet("10.0.0.2"), &side{}); got != triMaybe {
		t.Errorf("matchPrefix(::ffff:10.0.0.1/24) = %v, want maybe", got)
	}
}

// TestQualifiedSubnetworkMatchesItsIdentity is review round 1, finding 9: a self link of a subnetwork matches only a
// subnetwork with that link, and a bare name only one in the project and region of the instance.
func TestQualifiedSubnetworkMatchesItsIdentity(t *testing.T) {
	t.Parallel()

	sub := resourceOf("google_compute_subnetwork.app", gcpSubnetworkType, map[string]any{
		"name": "app", "region": "europe-west1", "project": "p",
	})

	cases := map[string]struct {
		subnetwork, zone string
		found            bool
	}{
		"self link of another project": {subnetwork: "projects/q/regions/europe-west1/subnetworks/app", zone: "europe-west1-b"},
		"bare name in another region":  {subnetwork: "app", zone: "us-central1-a"},
		"bare name in its region":      {subnetwork: "app", zone: "europe-west1-b", found: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			instance := resourceOf("google_compute_instance.web", "google_compute_instance", map[string]any{
				"zone": tc.zone, "project": "p", "network_interface": []any{map[string]any{"subnetwork": tc.subnetwork}},
			})
			e := coreEvaluator(instance, sub)

			if got := e.gcpSubnet(e.resource("google_compute_instance.web")); (got != nil) != tc.found {
				t.Errorf("subnetwork found = %v, want %v", got != nil, tc.found)
			}
		})
	}
}
