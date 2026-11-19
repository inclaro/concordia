package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
)

// The placement tests of the independence from pair reconciliation, of the findings, and of the next actions.

// ---- Placement is independent of pair reconciliation ----

// plcSamePair reports whether two reconciliation entries are the same pair with the same classification.
func plcSamePair(a, b *conformance.ReconciliationEntry) bool {
	if a.From != b.From || a.To != b.To {
		return false
	}

	return a.Classification == b.Classification
}

func plcPlacementFactsCreateNoPair(t *testing.T) {
	t.Helper()

	with := deploymentRun(t, &deploymentSpec{})
	without := deploymentRun(t, &deploymentSpec{withoutPack: true})

	if len(placementFacts(with.run)) == 0 {
		t.Fatal("the run has no placed-in fact")
	}

	got, want := with.run.Reconciliation, without.run.Reconciliation

	if len(got) != len(want) {
		t.Fatalf("reconciliation has %d entries with the placement rules and %d without them", len(got), len(want))
	}

	for i := range got {
		if !plcSamePair(&got[i], &want[i]) {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func plcCorrectAndReversePlacementAreSeparate(t *testing.T) {
	t.Helper()

	// api workload of production is placed correctly; reports is bound to the container and placed in the namespace of the node.
	// Namespace payments is bound to prod-cluster for the correct placement and the reverse one needs a second namespace: use staging.
	bindings := plcBindings(
		workloadBinding("bind-api-staging", "api-service", "staging", "api", "payments-staging"),
		workloadBinding("bind-worker-staging", "prod-cluster", "staging", "worker", "payments"),
		plcNamespace("bind-ns-api", "prod-cluster", "staging", "payments-staging"),
		plcNamespace("bind-ns-worker", "api-service", "staging", "payments"),
	)
	d := deploymentRun(t, &deploymentSpec{bindings: bindings})

	plcWantVerdict(t, d, plc001("api-service", "staging"), conformance.VerdictPass)

	reverse := plcWantVerdict(t, d, plc002API("staging"), conformance.VerdictViolation)
	if occ := plcOccurrence(t, reverse, conformance.OccurrenceWrongDirection); !plcLocatedAt(occ.Location, "k8s/staging/worker.yaml") {
		t.Errorf("occurrence = %+v", occ)
	}

	plcRequireNoContainerPair(t, d.run)
}

func plcRationaleIsDocumented(t *testing.T) {
	t.Helper()

	text := string(mustRead(t, "../../docs/reference/infrastructure-rules.md"))

	for _, want := range []string{"not evaluated by pair reconciliation", "accepts `deployed-in` in both directions", "has a direction", "an environment"} {
		if !strings.Contains(text, want) {
			t.Errorf("infrastructure-rules.md lacks %q", want)
		}
	}
}

func TestPlacementIsIndependentOfPairReconciliation(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "placement_facts_create_no_pair", run: plcPlacementFactsCreateNoPair},
		{name: "one_correct_and_one_reverse_placement_give_separate_results", run: plcCorrectAndReversePlacementAreSeparate},
		{name: "rationale_is_documented", run: plcRationaleIsDocumented},
	})
}

// ---- Placement findings use the typed finding path ----

// plcAbsentAPIBindings leaves api-service without a workload in either environment: INFRA-PLC-001 is a violation in both.
func plcAbsentAPIBindings() string {
	return plcBindings(workloadBinding("bind-worker-staging", "worker-service", "staging", "worker", "payments"))
}

// plcIsAPIProductionViolation reports whether the finding is the violation of api-service in production for the obligation.
func plcIsAPIProductionViolation(f *conformance.Finding, ob *conformance.ObligationResult) bool {
	if f.ObligationID != ob.ID || f.Result != conformance.VerdictViolation {
		return false
	}

	return f.Subject == "api-service" && f.Environment == "production"
}

// plcHaveDistinctIdentity reports whether the findings have a fingerprint and differ in fingerprint and obligation.
func plcHaveDistinctIdentity(a, b *conformance.Finding) bool {
	if a.Fingerprint == "" || a.Fingerprint == b.Fingerprint {
		return false
	}

	return a.ObligationID != b.ObligationID
}

// plcShareIdentity reports whether two findings share the rule, the obligation, or the fingerprint.
func plcShareIdentity(a, b *conformance.Finding) bool {
	if a.RuleID == b.RuleID || a.ObligationID == b.ObligationID {
		return true
	}

	return a.Fingerprint == b.Fingerprint
}

func plcFindingContent(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{bindings: plcAbsentAPIBindings()})

	production := plcFinding(t, d.run, plc001("api-service", "production"))
	staging := plcFinding(t, d.run, plc001("api-service", "staging"))
	ob := plcObligation(t, d, plc001("api-service", "production"))

	if !plcIsAPIProductionViolation(production, ob) {
		t.Errorf("finding = %+v, obligation %s", production, ob.ID)
	}

	if !plcHaveDistinctIdentity(production, staging) {
		t.Errorf("fingerprints %s and %s, obligations %s and %s", production.Fingerprint, staging.Fingerprint, production.ObligationID, staging.ObligationID)
	}
}

func plcSeparateFindingsOfTheTwoRules(t *testing.T) {
	t.Helper()

	// The Deployment api of production is bound to the node and its namespace to dev-cluster: both rules report a violation.
	bindings := plcAPIBindings(plcNamespace("bind-ns-prod", "dev-cluster", "production", "payments"))
	d := deploymentRun(t, &deploymentSpec{architecture: plcArchitecture(t, "dev-cluster"), bindings: bindings})

	first := plcFinding(t, d.run, plc001("api-service", "production"))
	second := plcFinding(t, d.run, plc002API("production"))

	if first.Result != conformance.VerdictViolation || second.Result != conformance.VerdictViolation {
		t.Fatalf("findings = %+v and %+v", first, second)
	}

	if plcShareIdentity(first, second) {
		t.Errorf("findings share an identity: %+v and %+v", first, second)
	}
}

// plcCheckDocument is the part of the JSON results that the baseline test reads.
type plcCheckDocument struct {
	Findings []struct {
		RuleID      string `json:"ruleId"`
		Subject     string `json:"subject"`
		Environment string `json:"environment"`
		Disposition string `json:"disposition"`
	} `json:"findings"`
}

// plcAPIDispositions gives the disposition of the INFRA-PLC-001 findings of api-service by environment.
func plcAPIDispositions(doc *plcCheckDocument) map[string]string {
	dispositions := map[string]string{}

	for _, f := range doc.Findings {
		if f.RuleID == plcRule1 && f.Subject == "api-service" {
			dispositions[f.Environment] = f.Disposition
		}
	}

	return dispositions
}

func plcBaselineOfOneEnvironment(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{bindings: plcAbsentAPIBindings()})

	// The baseline is made from a run of production only.
	if res := d.cli(t, "check", "--environment", "production"); res.code > 1 {
		t.Fatalf("check --environment production exit code %d: %s", res.code, res.stderr)
	}

	if res := d.cli(t, "baseline", "create", "--from", resultsFile); res.code != 0 {
		t.Fatalf("baseline create exit code %d: %s", res.code, res.stderr)
	}

	res := d.cli(t, "check", "--format", "json")

	var doc plcCheckDocument
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("results: %v (exit code %d, stderr %s)", err, res.code, res.stderr)
	}

	dispositions := plcAPIDispositions(&doc)
	if dispositions["production"] != "baselined" || dispositions["staging"] != "active" {
		t.Errorf("dispositions = %v, want production baselined and staging active", dispositions)
	}
}

func TestPlacementFindings(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "finding_content", run: plcFindingContent},
		{name: "separate_findings_of_the_two_rules", run: plcSeparateFindingsOfTheTwoRules},
		{name: "baseline_of_one_environment", run: plcBaselineOfOneEnvironment},
	})
}

// ---- Placement next actions ----

func TestPlacementNextActionByCase(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	cases := []struct {
		name   string
		key    plcObligationKey
		want   plcOutcome
		action string
	}{
		{
			"hosting target without a binding", plc001("api-service", "staging"),
			plcOutcome{verdict: conformance.VerdictUnknown, reason: conformance.ReasonBindingGap},
			plcResolveBinding,
		},
		{
			"definite absence", plc001("worker-service", "production"),
			plcOutcome{verdict: conformance.VerdictViolation},
			plcFixImplementation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ob := plcWant(t, d, tc.key, tc.want)
			if actions := plcNextActions(ob); len(actions) == 0 || actions[0] != tc.action {
				t.Errorf("next actions = %v, want %q", actions, tc.action)
			}

			if finding := plcFinding(t, d.run, tc.key); finding.NextAction != tc.action {
				t.Errorf("finding next action = %q, want %q", finding.NextAction, tc.action)
			}
		})
	}
}
