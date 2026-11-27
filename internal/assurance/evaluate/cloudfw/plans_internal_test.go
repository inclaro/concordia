package cloudfw

import (
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// Addresses of the several-plans scenarios.
const (
	plansLedger = "aws_security_group.ledger"
	plansShared = "aws_security_group.shared"
	plansWeb    = "aws_instance.web"
)

// plansEvaluator is the evaluator over the plans of the environment `production`, each a list of resources.
func plansEvaluator(plans ...[]plan.Resource) *Evaluator {
	in := &Input{Environment: "production", Coverage: assurance.CoverageComplete}
	for _, resources := range plans {
		in.Plans = append(in.Plans, Plan{File: "plan.json", Resources: resources})
	}

	return New(in)
}

// TestSeveralPlansOfOneEnvironment: the plans of an environment are keyed by plan. An address
// that two plans hold is ambiguous only for the queries that name it; a known ID resolves across the plans; a
// configuration reference resolves only inside the plan of the resource that holds it.
func TestSeveralPlansOfOneEnvironment(t *testing.T) {
	t.Parallel()

	app := group(awsAppGroup, idApp, []any{}, []any{allTraffic()})
	ledgerFromApp := group(plansLedger, "sg-ledger", []any{postgresFrom(map[string]any{"security_groups": []any{idApp}})}, []any{})
	unused := group(plansShared, "sg-shared-a", []any{}, []any{})
	unusedCopy := group(plansShared, "sg-shared-b", []any{}, []any{})

	cases := []struct {
		evaluator *Evaluator
		name      string
		from, to  string
		want      Verdict
		gaps      []string
	}{
		{
			name:      "an address in two plans is ambiguous for a query that names it",
			evaluator: plansEvaluator([]plan.Resource{app, openDB()}, []plan.Resource{group(awsDBGroup, "sg-db-b", []any{}, []any{})}),
			from:      awsAppGroup, to: awsDBGroup, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": " + awsDBGroup + " is in 2 plans of environment production"},
		},
		{
			name:      "an address in two plans does not block a query that does not name it; a known ID resolves across plans",
			evaluator: plansEvaluator([]plan.Resource{app, unused}, []plan.Resource{ledgerFromApp, unusedCopy}),
			from:      awsAppGroup, to: plansLedger, want: VerdictAllowed,
		},
		{
			name:      "a configuration reference resolves inside the plan of the resource that holds it",
			evaluator: plansEvaluator(plansWebPlan(), plansLedgerPlan(true)),
			from:      plansWeb, to: plansLedger, want: VerdictDenied,
		},
		{
			name:      "a configuration reference does not resolve to a resource of another plan",
			evaluator: plansEvaluator(plansWebPlan(), plansLedgerPlan(false)),
			from:      plansWeb, to: plansLedger, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": " + awsAppGroup + " is not a resource of the plan after apply"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := tc.evaluator.Evaluate(&Query{
				From: Endpoint{Address: tc.from}, To: Endpoint{Address: tc.to}, Protocol: ProtocolTCP, Port: postgres,
			})
			assertQueryResult(t, &res, tc.want, tc.gaps)
		})
	}
}

// plansWebPlan is a plan with an instance in the group `aws_security_group.app` of that plan.
func plansWebPlan() []plan.Resource {
	return []plan.Resource{
		group(awsAppGroup, idApp, []any{}, []any{allTraffic()}),
		resourceOf(plansWeb, "aws_instance", map[string]any{"vpc_security_group_ids": []any{idApp}}),
	}
}

// plansLedgerPlan is a plan whose ledger group allows 5432 from the group `aws_security_group.app` that its
// configuration names; the ID is unknown until apply. withApp adds that group to this plan, with another ID.
func plansLedgerPlan(withApp bool) []plan.Resource {
	ledger := group(plansLedger, "sg-ledger", []any{postgresFrom(map[string]any{})}, []any{})
	ledger.Unknown = []string{"ingress[0].security_groups"}
	ledger.References = map[string][]string{"ingress[0].security_groups": {awsAppGroup}}

	resources := []plan.Resource{ledger}
	if withApp {
		resources = append(resources, group(awsAppGroup, "sg-app-other-state", []any{}, []any{allTraffic()}))
	}

	return resources
}
