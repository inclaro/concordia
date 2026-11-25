package cloudfw

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
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

// Addresses and IDs of the AWS query cases.
const (
	awsWebInstance = "aws_instance.web"
	awsAppGroup    = "aws_security_group.app"
	awsDBGroup     = "aws_security_group.db"
	awsExtraGroup  = "aws_security_group.extra"
	awsExtraNIC    = "aws_network_interface.extra"
	idApp          = "sg-app"
	idDB           = "sg-db"
	idExtra        = "sg-extra"
	postgres       = 5432
)

// queryCase is one query and the verdict and gaps (`<kind>: <detail>`) that the result must have.
type queryCase struct {
	name      string
	resources []plan.Resource
	from, to  string
	want      Verdict
	gaps      []string
}

// allTraffic is an inline rule that allows every protocol to and from every IPv4 address.
func allTraffic() map[string]any {
	return map[string]any{"protocol": "-1", "from_port": "0", "to_port": "0", "cidr_blocks": []any{"0.0.0.0/0"}}
}

// postgresFrom is an inline ingress rule for port 5432 with extra fields.
func postgresFrom(fields map[string]any) map[string]any {
	rule := map[string]any{"protocol": "tcp", "from_port": "5432", "to_port": "5432"}
	for key, value := range fields {
		rule[key] = value
	}

	return rule
}

// group is a security group with its inline rules.
func group(address, id string, ingress, egress []any) plan.Resource {
	return resourceOf(address, typeSecurityGroup, map[string]any{"id": id, "ingress": ingress, "egress": egress})
}

// openDB is the database group: it allows 5432 from every IPv4 address.
func openDB() plan.Resource {
	return group(awsDBGroup, idDB, []any{postgresFrom(map[string]any{"cidr_blocks": []any{"0.0.0.0/0"}})}, []any{})
}

func runQueryCases(t *testing.T, cases []queryCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := coreEvaluator(tc.resources...).Evaluate(&Query{
				From: Endpoint{Address: tc.from}, To: Endpoint{Address: tc.to}, Protocol: ProtocolTCP, Port: postgres,
			})
			assertQueryResult(t, &res, tc.want, tc.gaps)
		})
	}
}

func assertQueryResult(t *testing.T, res *Result, want Verdict, gaps []string) {
	t.Helper()

	got := make([]string, 0, len(res.Gaps))
	for _, gap := range res.Gaps {
		got = append(got, gap.Kind+": "+gap.Detail)
	}

	if res.Verdict != want {
		t.Fatalf("verdict = %s (gaps %v), want %s", res.Verdict, got, want)
	}

	for _, gap := range gaps {
		if !slices.Contains(got, gap) {
			t.Errorf("gaps = %v, want %q", got, gap)
		}
	}
}
