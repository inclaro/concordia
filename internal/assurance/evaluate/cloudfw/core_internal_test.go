package cloudfw

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

const (
	typeSecurityGroup = "aws_security_group"
	sgApp             = "aws_security_group.app"
	sgBastion         = "aws_security_group.bastion"
)

func coreEvaluator(resources ...plan.Resource) *Evaluator {
	slices.SortFunc(resources, func(a, b plan.Resource) int { return strings.Compare(a.Address, b.Address) })

	return New(&Input{Coverage: assurance.CoverageComplete, Plans: []Plan{{File: "plan.json", Resources: resources}}})
}

func resourceOf(address, typ string, values map[string]any) plan.Resource {
	return plan.Resource{Address: address, Type: typ, Values: values}
}

func addresses(found []*plan.Resource) []string {
	out := make([]string, 0, len(found))
	for _, res := range found {
		out = append(out, res.Address)
	}

	return out
}

func gapKinds(s *side) []string {
	out := make([]string, 0, len(s.gaps))
	for _, gap := range s.gaps {
		out = append(out, gap.Kind)
	}

	return out
}

// dbGroup has two inline ingress blocks whose referenced groups are unknown until apply. The configuration lists the
// app block first, the plan lists the bastion block first (set order).
func dbGroup(references map[string][]string) plan.Resource {
	db := resourceOf("aws_security_group.db", typeSecurityGroup, map[string]any{
		"ingress": []any{map[string]any{"from_port": "22"}, map[string]any{"from_port": "5432"}},
	})
	db.Unknown = []string{"ingress[0].security_groups", "ingress[1].security_groups"}
	db.References = references

	return db
}

// TestBlockRefsIgnoresConfigurationOrder is finding 1: an inline-block reference must not be matched by the
// configuration index, because the plan lists set elements in hash order.
func TestBlockRefsIgnoresConfigurationOrder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		references map[string][]string
		name       string
		wantGap    string
		want       []string
		wantOK     bool
	}{
		{
			name: "blocks name different groups",
			references: map[string][]string{
				"ingress[0].security_groups": {sgApp}, "ingress[1].security_groups": {sgBastion},
			},
			wantGap: evaluate.GapUnresolvedReference,
		},
		{
			name: "every block names the same group",
			references: map[string][]string{
				"ingress[0].security_groups": {sgApp}, "ingress[1].security_groups": {sgApp},
			},
			want: []string{sgApp}, wantOK: true,
		},
		{
			name:       "fewer blocks with references than unknown blocks",
			references: map[string][]string{"ingress[0].security_groups": {sgApp}},
			wantGap:    evaluate.GapUnresolvedReference,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := coreEvaluator(dbGroup(tc.references),
				resourceOf(sgApp, typeSecurityGroup, map[string]any{}),
				resourceOf(sgBastion, typeSecurityGroup, map[string]any{}))
			check := &side{}

			found, ok := e.blockRefsOfType(e.resource("aws_security_group.db"),
				blockElement{list: "ingress", field: "security_groups", index: 0}, check, typeSecurityGroup)

			if ok != tc.wantOK || !slices.Equal(addresses(found), tc.want) {
				t.Fatalf("got %v %v, want %v %v (gaps %v)", addresses(found), ok, tc.want, tc.wantOK, check.gaps)
			}

			if tc.wantGap != "" && !slices.Contains(gapKinds(check), tc.wantGap) {
				t.Fatalf("gaps %v, want kind %s", check.gaps, tc.wantGap)
			}
		})
	}
}

// TestListHasUnknownElements is finding 1, second part: a wholly unknown element is dropped from Values.
func TestListHasUnknownElements(t *testing.T) {
	t.Parallel()

	res := resourceOf("aws_security_group.db", typeSecurityGroup, map[string]any{"ingress": []any{}})
	res.Unknown = []string{"egress[0].cidr_blocks", "ingress[1]"}

	for path, want := range map[string]bool{"ingress": true, "egress": false, "ingress[1]": false} {
		if got := listHasUnknownElements(&res, path); got != want {
			t.Errorf("listHasUnknownElements(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestRefsOfTypeRejectsOtherTypes is finding 2: `vpc_security_group_ids = [aws_vpc.main.default_security_group_id]`
// must not resolve to the VPC as if it were the group.
func TestRefsOfTypeRejectsOtherTypes(t *testing.T) {
	t.Parallel()

	instance := resourceOf("aws_instance.web", "aws_instance", map[string]any{"subnet_id": "subnet-1"})
	instance.Unknown = []string{"vpc_security_group_ids"}
	instance.References = map[string][]string{"vpc_security_group_ids": {"aws_vpc.main"}}

	e := coreEvaluator(instance,
		resourceOf("aws_vpc.main", "aws_vpc", map[string]any{}),
		resourceOf("aws_vpc.other", "aws_vpc", map[string]any{"id": "subnet-1"}))

	t.Run("list", func(t *testing.T) {
		t.Parallel()

		check := &side{}

		found, ok := e.refsOfType(e.resource("aws_instance.web"), "vpc_security_group_ids", check, typeSecurityGroup)
		requireUnresolved(t, addresses(found), ok, check)
	})

	t.Run("single by known id", func(t *testing.T) {
		t.Parallel()

		check := &side{}

		found, ok := e.refOfType(e.resource("aws_instance.web"), "subnet_id", check, "aws_subnet")
		if found != nil {
			t.Fatalf("got %s, want nil", found.Address)
		}

		requireUnresolved(t, nil, ok, check)
	})
}

// requireUnresolved fails the test unless nothing resolved, ok is false and the side has an unresolved-reference gap.
func requireUnresolved(t *testing.T, found []string, ok bool, check *side) {
	t.Helper()

	if ok || len(found) != 0 {
		t.Fatalf("got %v %v, want none and false", found, ok)
	}

	if !slices.Contains(gapKinds(check), evaluate.GapUnresolvedReference) {
		t.Fatalf("gaps %v, want %s", check.gaps, evaluate.GapUnresolvedReference)
	}
}

// TestPrefixFamilies is finding 3: an IPv6 range is never evaluated, so it never decides; IPv4 ranges follow the
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

// TestIPv6OnlyIngressIsUnknown is finding 3 end to end: the database group opens 5432 only to `::/0`. That must not
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
