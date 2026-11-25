package cloudfw

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// TestPrefixFamilies: an IPv6 range is never evaluated, so it never decides; IPv4 ranges follow the
// plan's table.
func TestPrefixFamilies(t *testing.T) {
	t.Parallel()

	known := prefixSet("10.0.1.0/24")

	cases := []struct {
		name  string
		rules []string
		ep    addrSet
		want  tri
	}{
		{name: "IPv6 all against an IPv4 endpoint", rules: []string{"::/0"}, ep: known, want: triMaybe},
		{name: "IPv6 all against an unknown endpoint", rules: []string{"::/0"}, ep: unknownAddrs, want: triMaybe},
		{name: "IPv4 all against an unknown endpoint", rules: []string{"0.0.0.0/0"}, ep: unknownAddrs, want: triYes},
		{name: "IPv4 all decides before IPv6", rules: []string{"::/0", "0.0.0.0/0"}, ep: unknownAddrs, want: triYes},
		{name: "IPv4 all against an IPv4 endpoint", rules: []string{"0.0.0.0/0"}, ep: known, want: triYes},
		{name: "disjoint IPv4 range", rules: []string{"10.9.0.0/16"}, ep: known, want: triNo},
		{name: "disjoint IPv4 range and IPv6 all", rules: []string{"10.9.0.0/16", "::/0"}, ep: known, want: triMaybe},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			check := &side{}
			if got := anyPrefix(tc.rules, tc.ep, check); got != tc.want {
				t.Fatalf("anyPrefix(%v) = %v, want %v (gaps %v)", tc.rules, got, tc.want, check.gaps)
			}

			if tc.want == triMaybe && !slices.Contains(gapKinds(check), evaluate.GapAddressDependent) {
				t.Fatalf("gaps %v, want %s", check.gaps, evaluate.GapAddressDependent)
			}
		})
	}
}

// TestIPv6OnlyIngressIsUnknown: the database group opens 5432 only to `::/0`. That must not
// give `denied` (the port may be open over IPv6) nor `allowed` (the IPv4 path is not open).
func TestIPv6OnlyIngressIsUnknown(t *testing.T) {
	t.Parallel()

	all := map[string]any{
		"protocol": "-1", "from_port": json.Number("0"), "to_port": json.Number("0"), "cidr_blocks": []any{"0.0.0.0/0"},
	}
	db := map[string]any{
		"protocol": "tcp", "from_port": json.Number("5432"), "to_port": json.Number("5432"), "ipv6_cidr_blocks": []any{"::/0"},
	}

	e := coreEvaluator(
		resourceOf("aws_lb.public", "aws_lb", map[string]any{"security_groups": []any{"sg-gw"}}),
		resourceOf("aws_security_group.gw", typeSecurityGroup, map[string]any{"id": "sg-gw", "egress": []any{all}, "ingress": []any{}}),
		resourceOf("aws_db_instance.db", "aws_db_instance", map[string]any{"vpc_security_group_ids": []any{"sg-db"}}),
		resourceOf("aws_security_group.db", typeSecurityGroup, map[string]any{"id": "sg-db", "ingress": []any{db}, "egress": []any{}}),
	)

	res := e.Evaluate(&Query{
		From: Endpoint{Address: "aws_lb.public"}, To: Endpoint{Address: "aws_db_instance.db"}, Protocol: ProtocolTCP, Port: 5432,
	})

	if res.Verdict != VerdictUnknown || !slices.ContainsFunc(res.Gaps, func(g assurance.Gap) bool {
		return g.Kind == evaluate.GapAddressDependent && g.Detail == "::/0: IPv6 ranges are not evaluated"
	}) {
		t.Fatalf("got %s %v, want unknown with the IPv6 address-dependent-range gap", res.Verdict, res.Gaps)
	}
}

// TestEndpointInSeveralPrefixes: the address set of an endpoint in several subnets holds every subnet,
// so a rule prefix that contains all of them matches, one that overlaps some is unknown, and one that overlaps none
// does not match.
func TestEndpointInSeveralPrefixes(t *testing.T) {
	t.Parallel()

	two := unionSet(prefixSet("10.0.1.0/24"), prefixSet("10.0.2.0/24"))

	for rule, want := range map[string]tri{"10.0.0.0/16": triYes, "10.0.1.0/24": triMaybe, "10.9.0.0/16": triNo} {
		if got := matchPrefix(rule, two, &side{}); got != want {
			t.Errorf("matchPrefix(%s) = %v, want %v", rule, got, want)
		}
	}

	if got := unionSet(two, unknownAddrs); got.known() {
		t.Errorf("union with an unknown set = %v, want unknown", got)
	}
}

// TestMappedIPv6PrefixStaysIPv6: an IPv4-mapped IPv6 prefix is an IPv6 prefix, so it
// never decides.
func TestMappedIPv6PrefixStaysIPv6(t *testing.T) {
	t.Parallel()

	if got := matchPrefix("::ffff:10.0.0.1/24", hostSet("10.0.0.2"), &side{}); got != triMaybe {
		t.Errorf("matchPrefix(::ffff:10.0.0.1/24) = %v, want maybe", got)
	}
}

// Addresses of the interface whose secondary IPs are unknown until apply.
const (
	secondaryIPsSource = "aws_network_interface.src"
	secondaryIPsSubnet = "aws_subnet.app"
)

// secondaryIPsNIC is a network interface in the group app whose primary IP is 10.0.0.10, whose `private_ips` list is
// unknown until apply, in the subnet 10.0.0.0/24. configured are the keys that its configuration sets.
func secondaryIPsNIC(configured []string) plan.Resource {
	nic := resourceOf(secondaryIPsSource, awsNetworkInterface, map[string]any{
		"security_groups": []any{idApp}, "private_ip": "10.0.0.10", "subnet_id": "subnet-app",
	})
	nic.Unknown = []string{"private_ips"}
	nic.Configured = configured

	return nic
}

// TestUnknownPrivateIPsKeepAddressesOpen: an interface whose `private_ips` list is
// unknown may have secondary addresses, so its primary IP is not its whole address set, unless the configuration
// sets no secondary address.
func TestUnknownPrivateIPsKeepAddressesOpen(t *testing.T) {
	t.Parallel()

	world := func(configured []string) []plan.Resource {
		return []plan.Resource{
			secondaryIPsNIC(configured), group(awsAppGroup, idApp, []any{}, []any{allTraffic()}),
			group(awsDBGroup, idDB, []any{postgresFrom(map[string]any{"cidr_blocks": []any{"10.0.0.11/32"}})}, []any{}),
			resourceOf(secondaryIPsSubnet, awsSubnet, map[string]any{"id": "subnet-app", "cidr_block": "10.0.0.0/24"}),
		}
	}

	runQueryCases(t, []queryCase{
		{
			name: "configuration may set secondary addresses", resources: world([]string{"private_ips_count", "subnet_id"}),
			from: secondaryIPsSource, to: awsDBGroup, want: VerdictUnknown,
			gaps: []string{evaluate.GapAddressDependent + ": 10.0.0.11/32 covers only part of 10.0.0.0/24"},
		},
		{
			name: "configuration sets no secondary address", resources: world([]string{"subnet_id", "security_groups"}),
			from: secondaryIPsSource, to: awsDBGroup, want: VerdictDenied,
		},
	})
}
