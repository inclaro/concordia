package cloudfw_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/workspace"
)

// awsFixture is the handwritten `terraform show -json` plan of the AWS scenarios.
const awsFixture = "../../../../test/fixtures/assurance/plans/cloudfw-aws.json"

// awsScenario is one query against the AWS fixture plan and its expected result.
type awsScenario struct {
	name     string
	from, to string
	protocol string
	want     cloudfw.Verdict
	reason   string
	// rules are the deciding rules, `<address> <rule>` joined by ", ".
	rules string
	// gaps are the gaps, `<kind>: <detail>` joined by "; ".
	gaps string
	port int
}

// awsReadPlan reads plan bytes through the plan reader.
func awsReadPlan(t *testing.T, data []byte) *plan.Plan {
	t.Helper()

	p, diags := plan.Read(data, "cloudfw-aws.json")
	if p == nil {
		t.Fatalf("plan not read: %v", diags)
	}

	return p
}

// awsFixtureBytes are the bytes of the fixture plan.
func awsFixtureBytes(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile(awsFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	return data
}

// awsEvaluator is the evaluator over the resources of one plan.
func awsEvaluator(p *plan.Plan) *cloudfw.Evaluator {
	return cloudfw.New(&cloudfw.Input{Resources: p.Resources, Coverage: p.Coverage, Digest: p.Digest})
}

// awsQuery is the query of a scenario; the protocol is TCP unless the scenario names one.
func awsQuery(sc *awsScenario) *cloudfw.Query {
	return &cloudfw.Query{
		From: cloudfw.Endpoint{Address: sc.from}, To: cloudfw.Endpoint{Address: sc.to}, Protocol: sc.protocol, Port: sc.port,
	}
}

// awsAssert checks a result against the scenario: the verdict, the reason, the deciding rules, the gaps, the
// assumption of a denied result, and the limits.
func awsAssert(t *testing.T, sc *awsScenario, res *cloudfw.Result) {
	t.Helper()

	if res.Verdict != sc.want || res.Reason != sc.reason {
		t.Fatalf("verdict = %s (reason %q, gaps %v), want %s (reason %q)", res.Verdict, res.Reason, res.Gaps, sc.want, sc.reason)
	}

	if got := awsRules(res); got != sc.rules {
		t.Errorf("rules = %q, want %q", got, sc.rules)
	}

	if got := awsGaps(res); got != sc.gaps {
		t.Errorf("gaps = %q, want %q", got, sc.gaps)
	}

	awsAssertAssumptions(t, res)

	if !slices.Equal(res.Limits, cloudfw.Limits) {
		t.Errorf("limits = %v", res.Limits)
	}
}

// awsAssertAssumptions checks that exactly a denied result rests on the completeness assumption.
func awsAssertAssumptions(t *testing.T, res *cloudfw.Result) {
	t.Helper()

	want := []string{}
	if res.Verdict == cloudfw.VerdictDenied {
		want = []string{workspace.AssumeFirewallRulesInPlan}
	}

	if !slices.Equal(res.Assumptions, want) {
		t.Errorf("assumptions = %v, want %v", res.Assumptions, want)
	}
}

func awsRules(res *cloudfw.Result) string {
	out := make([]string, 0, len(res.Rules))
	for _, r := range res.Rules {
		out = append(out, r.Address+" "+r.Rule)
	}

	return strings.Join(out, ", ")
}

func awsGaps(res *cloudfw.Result) string {
	out := make([]string, 0, len(res.Gaps))
	for _, g := range res.Gaps {
		out = append(out, g.Kind+": "+g.Detail)
	}

	return strings.Join(out, "; ")
}

// awsUnsupported is the reason of an unknown result that only unsupported constructs explain.
const awsUnsupported = evaluate.ReasonUnsupportedConstruct

// awsIncomplete is the reason of an unknown result that missing evidence explains.
const awsIncomplete = evaluate.ReasonCoverageIncomplete
