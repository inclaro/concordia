package integration_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
)

// The placement tests of the workload, environment, and hosting-target scope of an obligation: unbound workloads, the
// direction of a placement, the environment of a placement, and the unexpected placement of INFRA-PLC-002.

// plcNobodyForSubjects requires that neither rule has an obligation for the subjects in the environment.
func plcNobodyForSubjects(t *testing.T, d *deployment, env string, subjects ...string) {
	t.Helper()

	for _, rule := range plcBothRules() {
		for _, subject := range subjects {
			plcNobody(t, d, plcObligationKey{rule: rule, subject: subject, env: env})
		}
	}
}

// ---- Unbound workloads stay visible and are not attached by name ----

func TestPlacementUnboundWorkloadIsNotEvidence(t *testing.T) {
	t.Parallel()

	// Deployment payments/api of production is in the namespace that is bound to the container, but no binding links it to a node.
	bindings := plcBindings(
		workloadBinding("bind-api-staging", "api-service", "staging", "api", "payments-staging"),
		plcNamespace("bind-ns-prod", "prod-cluster", "production", "payments"),
	)
	d := deploymentRun(t, &deploymentSpec{bindings: bindings})

	deployment := plcWorkloadNode(t, d, "k8s:production/prod-eu/apps/Deployment/payments/api")
	anchoredAtDeployment := func(occ *conformance.OccurrenceResult) bool { return occ.Anchor == deployment }

	for _, rule := range plcBothRules() {
		ob := plcObligation(t, d, plcObligationKey{rule: rule, subject: "api-service", env: "production"})

		for _, occ := range plcOccurrencesWhere(ob, anchoredAtDeployment) {
			t.Errorf("%s: the unbound Deployment is an occurrence: %+v", rule, occ)
		}
	}

	plcRequireFixImplementation(t, plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictViolation))

	// The facts keep the Deployment: it has a placed-in fact and no binding.
	if len(plcPlacedFactsFrom(d.run, deployment)) == 0 {
		t.Error("the Deployment has no placed-in fact")
	}
}

// ---- Reverse direction does not satisfy ----

func plcReverseDoesNotSatisfyPLC001(t *testing.T, d *deployment) {
	t.Helper()

	ob := plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictViolation)

	for _, occ := range plcOccurrencesWhere(ob, plcIsPass) {
		t.Errorf("a result is a pass: %+v", occ)
	}

	plcRequireFixImplementation(t, ob)
}

// plcIsReportsWrongDirection reports whether the occurrence is the violation of the reports workload of production.
func plcIsReportsWrongDirection(occ *conformance.OccurrenceResult) bool {
	if occ.Result != conformance.VerdictViolation || !plcLocatedAt(occ.Location, "k8s/prod/reports.yaml") {
		return false
	}

	return occ.NextAction == plcFixImplementation && strings.Contains(occ.Detail, "reports")
}

func plcReverseIsUnexpectedInPLC002(t *testing.T, d *deployment) {
	t.Helper()

	ob := plcWantVerdict(t, d, plc002API("production"), conformance.VerdictViolation)
	occ := plcOccurrence(t, ob, conformance.OccurrenceWrongDirection)

	if !plcIsReportsWrongDirection(occ) {
		t.Errorf("occurrence = %+v", occ)
	}
}

func plcReverseNoPairResult(t *testing.T, d *deployment) {
	t.Helper()

	// The pair of the model (api-service, prod-cluster) has no entry of its own: the results are obligation results.
	plcRequireNoContainerPair(t, d.run)
}

func TestPlacementReverseDirection(t *testing.T) {
	t.Parallel()

	// Namespace payments is bound to api-service. Deployment payments/reports is bound to prod-cluster: its placed-in
	// fact runs from the container side to the node side. The Deployment api of the node is not bound.
	bindings := plcBindings(
		workloadBinding("bind-reports-workload", "prod-cluster", "production", "reports", "payments"),
		plcNamespace("bind-ns-prod", "api-service", "production", "payments"),
	)
	d := deploymentRun(t, &deploymentSpec{bindings: bindings})

	plcRunSharedSubtests(t, d, []plcSharedSubtest{
		{name: "does_not_satisfy_plc_001", run: plcReverseDoesNotSatisfyPLC001},
		{name: "is_unexpected_in_plc_002", run: plcReverseIsUnexpectedInPLC002},
		{name: "no_pair_result", run: plcReverseNoPairResult},
	})
}

// ---- Wrong environment does not satisfy ----

// plcStagingOnlyBindings binds the api workload of staging and the namespace of staging to the target.
func plcStagingOnlyBindings(target string) string {
	return plcBindings(
		workloadBinding("bind-api-staging", "api-service", "staging", "api", "payments-staging"),
		plcNamespace("bind-ns-staging", target, "staging", "payments-staging"),
	)
}

// plcUsesStagingPlacement reports whether the occurrence is a pass or names the staging namespace.
func plcUsesStagingPlacement(occ *conformance.OccurrenceResult) bool {
	return plcIsPass(occ) || strings.Contains(occ.Detail, "payments-staging")
}

// plcIsPlacement reports whether the occurrence is not an absence.
func plcIsPlacement(occ *conformance.OccurrenceResult) bool {
	return occ.Class != conformance.OccurrenceAbsent
}

func plcWrongEnvironmentOnlyInStaging(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{bindings: plcStagingOnlyBindings("prod-cluster")})

	production := plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictViolation)
	plcWantVerdict(t, d, plc001("api-service", "staging"), conformance.VerdictPass)
	plcWantVerdict(t, d, plc002API("staging"), conformance.VerdictPass)

	for _, occ := range plcOccurrencesWhere(production, plcUsesStagingPlacement) {
		t.Errorf("the staging placement is used by the production obligation: %+v", occ)
	}
}

func plcWrongEnvironmentAndWrongTarget(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{architecture: plcArchitecture(t, "dev-cluster"), bindings: plcStagingOnlyBindings("dev-cluster")})

	production := plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictViolation)
	for _, occ := range plcOccurrencesWhere(production, plcIsPlacement) {
		t.Errorf("the production obligation uses a placement: %+v", occ)
	}

	unexpected := plcWantVerdict(t, d, plc002API("staging"), conformance.VerdictViolation)
	plcRequireOccurrenceAt(t, unexpected, plcOccurrenceWant{conformance.OccurrenceMatching, "k8s/staging/deployment.yaml", plcFixImplementation})

	// Separate from the INFRA-PLC-001 results.
	if staging := plcObligation(t, d, plc001("api-service", "staging")); staging.ID == unexpected.ID || staging.Verdict == conformance.VerdictPass {
		t.Errorf("INFRA-PLC-001 for staging = %+v", staging)
	}
}

func plcWrongEnvironmentStagingNotSelected(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{
		bindings: plcStagingOnlyBindings("prod-cluster"),
		edit:     func(opts *conformance.CheckOptions) { opts.Environments = []string{"production"} },
	})

	plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictViolation)
	plcNobodyForSubjects(t, d, "staging", "api-service", "worker-service")

	if got := d.run.Inputs.Environments; !slices.Equal(got.Selected, []string{"production"}) || !slices.Equal(got.Excluded, []string{"staging"}) {
		t.Errorf("environments = %+v", got)
	}
}

func plcWrongEnvironmentSameNamespaceName(t *testing.T) {
	t.Helper()

	// Namespace `payments` exists in production (declared) and in staging (synthesized for the worker). Only the staging one is bound.
	bindings := plcBindings(
		workloadBinding("bind-api-workload", "api-service", "production", "api", "payments"),
		workloadBinding("bind-worker-staging", "worker-service", "staging", "worker", "payments"),
		plcNamespace("bind-ns-staging", "prod-cluster", "staging", "payments"),
	)
	d := deploymentRun(t, &deploymentSpec{bindings: bindings})

	plcWantVerdict(t, d, plc001("worker-service", "staging"), conformance.VerdictPass)

	ob := plcWantUnknown(t, d, plc001("api-service", "production"), conformance.ReasonBindingGap)
	if occ := plcOccurrence(t, ob, conformance.OccurrenceBindingGap); !plcLocatedUnder(occ.Location, "k8s/prod/") {
		t.Errorf("occurrence = %+v", occ)
	}
}

func TestPlacementWrongEnvironment(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "only_in_staging", run: plcWrongEnvironmentOnlyInStaging},
		{name: "wrong_environment_and_wrong_target", run: plcWrongEnvironmentAndWrongTarget},
		{name: "staging_not_selected", run: plcWrongEnvironmentStagingNotSelected},
		{name: "same_namespace_name_in_both_environments", run: plcWrongEnvironmentSameNamespaceName},
	})
}

// ---- INFRA-PLC-002 is a typed unexpected-placement rule ----

func plcUnexpectedWrongHostingTarget(t *testing.T) {
	t.Helper()

	// Namespace payments of production is bound to dev-cluster, which is not the declared container.
	bindings := plcAPIBindings(plcNamespace("bind-ns-prod", "dev-cluster", "production", "payments"))
	d := deploymentRun(t, &deploymentSpec{architecture: plcArchitecture(t, "dev-cluster"), bindings: bindings})

	ob := plcWantVerdict(t, d, plc002API("production"), conformance.VerdictViolation)
	plcRequireOccurrenceAt(t, ob, plcOccurrenceWant{conformance.OccurrenceMatching, "k8s/prod/deployment.yaml", plcFixImplementation})

	// INFRA-PLC-001 is evaluated separately and the placement does not satisfy it.
	plc1 := plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictViolation)
	if plc1.ID == ob.ID {
		t.Error("the two rules share one obligation")
	}
}

func plcUnexpectedPlacementInDeclaredContainer(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	plcWantVerdict(t, d, plc002API("production"), conformance.VerdictPass)
}

func plcUnexpectedHostingTargetNotBound(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	ob := plcWantUnknown(t, d, plc002API("staging"), conformance.ReasonBindingGap)
	if actions := plcNextActions(ob); !slices.Equal(actions, []string{plcResolveBinding}) {
		t.Errorf("next actions = %v", actions)
	}
}

func plcUnexpectedNodeWithoutPlacement(t *testing.T) {
	t.Helper()

	// batch-service takes part in no deployed-in relationship.
	d := deploymentRun(t, &deploymentSpec{architecture: plcArchitecture(t, "batch-service")})

	for _, env := range []string{"production", "staging"} {
		plcNobodyForSubjects(t, d, env, "batch-service")
	}
}

func plcUnexpectedModelWithoutDeployedIn(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{
		architecture: deploymentArchitectureWithoutPlacement,
		bindings:     plcBindings(workloadBinding("bind-api-workload", "api-service", "production", "api", "payments")),
	})

	rule := ruleOf(t, d.run, plcRule2)
	if len(rule.Obligations) != 0 || rule.Verdict != conformance.VerdictNotApplicable {
		t.Errorf("INFRA-PLC-002: verdict %s with %d obligations", rule.Verdict, len(rule.Obligations))
	}
}

func TestPlacementUnexpected(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "wrong_hosting_target_in_the_same_environment", run: plcUnexpectedWrongHostingTarget},
		{name: "placement_in_the_declared_container", run: plcUnexpectedPlacementInDeclaredContainer},
		{name: "hosting_target_not_bound", run: plcUnexpectedHostingTargetNotBound},
		{name: "node_without_a_declared_placement", run: plcUnexpectedNodeWithoutPlacement},
		{name: "model_without_deployed_in", run: plcUnexpectedModelWithoutDeployedIn},
	})
}
