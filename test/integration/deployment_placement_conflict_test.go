package integration_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
)

// The placement tests of the occurrence agreement of a node and of the conflicts that give unknown.

// plcContainsAll reports whether the text contains every part.
func plcContainsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}

	return true
}

// ---- Occurrence and agreement ----

// plcAgreementSpec binds both workloads of staging to api-service with the agreement: api (payments-staging, bound to
// the container) and worker (payments, bound to dev-cluster, so not placed in the container).
func plcAgreementSpec(t *testing.T, agreement string) *deploymentSpec {
	t.Helper()

	bindings := plcBindings(
		plcAgreement(workloadBinding("bind-api-staging", "api-service", "staging", "api", "payments-staging"), agreement),
		plcAgreement(workloadBinding("bind-worker-staging", "api-service", "staging", "worker", "payments"), agreement),
		plcNamespace("bind-ns-api", "prod-cluster", "staging", "payments-staging"),
		plcNamespace("bind-ns-worker", "dev-cluster", "staging", "payments"),
	)

	return &deploymentSpec{architecture: plcArchitecture(t, "dev-cluster"), bindings: bindings}
}

// plcResultsByPath gives the result of each occurrence that has a location, by the path of the location.
func plcResultsByPath(ob *conformance.ObligationResult) map[string]conformance.RuleVerdict {
	results := map[string]conformance.RuleVerdict{}

	for i := range ob.Occurrences {
		if loc := ob.Occurrences[i].Location; loc != nil {
			results[loc.Path] = ob.Occurrences[i].Result
		}
	}

	return results
}

func plcAgreementAllWorkloadsRequired(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, plcAgreementSpec(t, "all"))

	ob := plcObligation(t, d, plc001("api-service", "staging"))
	if ob.Verdict == conformance.VerdictPass {
		t.Fatalf("the aggregated verdict is pass: %+v", ob)
	}

	results := plcResultsByPath(ob)

	if results["k8s/staging/deployment.yaml"] != conformance.VerdictPass {
		t.Errorf("the api workload has result %q, want pass: %+v", results["k8s/staging/deployment.yaml"], ob.Occurrences)
	}

	if res, ok := results["k8s/staging/worker.yaml"]; !ok || res == conformance.VerdictPass {
		t.Errorf("the worker workload has result %q (present %v), want a result that is not pass: %+v", res, ok, ob.Occurrences)
	}

	// The passing workload does not hide the failing one: INFRA-PLC-002 flags the worker.
	unexpected := plcWantVerdict(t, d, plc002API("staging"), conformance.VerdictViolation)
	if occ := plcOccurrence(t, unexpected, conformance.OccurrenceMatching); !plcLocatedAt(occ.Location, "k8s/staging/worker.yaml") {
		t.Errorf("occurrence = %+v", occ)
	}
}

func plcAgreementAnyWorkloadIsEnough(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, plcAgreementSpec(t, "any"))

	plcWantVerdict(t, d, plc001("api-service", "staging"), conformance.VerdictPass)
}

func TestPlacementOccurrenceAndAgreement(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "all_workloads_required", run: plcAgreementAllWorkloadsRequired},
		{name: "any_workload_is_enough", run: plcAgreementAnyWorkloadIsEnough},
	})
}

// ---- Conflicts give unknown ----

func plcConflictAmbiguousWorkloadBinding(t *testing.T) {
	t.Helper()

	// Deployment payments/api of production matches non-exclusive bindings of the same tier for two nodes.
	bindings := plcBindings(
		plcNonExclusive(workloadBinding("bind-api-workload", "api-service", "production", "api", "payments")),
		plcNonExclusive(workloadBinding("bind-api-workload-twin", "worker-service", "production", "api", "payments")),
		plcNamespace("bind-ns-prod", "prod-cluster", "production", "payments"),
	)
	d := deploymentRun(t, &deploymentSpec{bindings: bindings})

	for _, subject := range []string{"api-service", "worker-service"} {
		for _, rule := range plcBothRules() {
			ob := plcWantUnknown(t, d, plcObligationKey{rule: rule, subject: subject, env: "production"}, conformance.ReasonAmbiguousBinding)

			if actions := plcNextActions(ob); !slices.Equal(actions, []string{plcResolveBinding}) {
				t.Errorf("%s for %s: next actions = %v", rule, subject, actions)
			}
		}
	}
}

// plcRequireCompetingDeclarations checks the obligation of the worker for the rule when two files declare its workload.
func plcRequireCompetingDeclarations(t *testing.T, d *deployment, rule string) {
	t.Helper()

	ob := plcWantUnknown(t, d, plcObligationKey{rule: rule, subject: "worker-service", env: "production"}, conformance.ReasonEvidenceConflict)

	for _, occ := range plcOccurrencesWhere(ob, plcIsPass) {
		t.Errorf("%s: a declaration was chosen: %+v", rule, occ)
	}

	if actions := plcNextActions(ob); !slices.Equal(actions, []string{plcSupplyEvidence, plcSupplyEvidence}) {
		t.Errorf("%s: next actions = %v", rule, actions)
	}

	if !plcContainsAll(ob.Detail, "k8s/overrides/reports.yaml", "k8s/prod/reports.yaml") {
		t.Errorf("%s: detail = %q, want both files", rule, ob.Detail)
	}
}

// plcIsCompetingDeclarationDiagnostic reports whether the diagnostic CO-OBS-021 names both files of the Deployment.
func plcIsCompetingDeclarationDiagnostic(x *diag.Diagnostic) bool {
	if x.Code != "CO-OBS-021" {
		return false
	}

	return plcContainsAll(x.Message, "k8s/overrides/reports.yaml:1", "k8s/prod/reports.yaml:1", "Deployment/payments/reports")
}

func plcConflictCompetingDeclarations(t *testing.T) {
	t.Helper()

	// k8s/prod/reports.yaml and k8s/overrides/reports.yaml both declare Deployment payments/reports in production.
	bindings := plcBindings(
		workloadBinding("bind-reports-workload", "worker-service", "production", "reports", "payments"),
		plcNamespace("bind-ns-prod", "prod-cluster", "production", "payments"),
	)
	d := deploymentRun(t, &deploymentSpec{bindings: bindings, environments: plcEnvironments("k8s/overrides/**")})

	for _, rule := range plcBothRules() {
		plcRequireCompetingDeclarations(t, d, rule)
	}

	if !slices.ContainsFunc(d.run.Diagnostics, func(x diag.Diagnostic) bool { return plcIsCompetingDeclarationDiagnostic(&x) }) {
		t.Errorf("no CO-OBS-021 names both files: %+v", d.run.Diagnostics)
	}
}

func TestPlacementConflicts(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "ambiguous_workload_binding", run: plcConflictAmbiguousWorkloadBinding},
		{name: "competing_declarations", run: plcConflictCompetingDeclarations},
	})
}
