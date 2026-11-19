package integration_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// These tests check the placement rules INFRA-PLC-001 and INFRA-PLC-002 of the `infrastructure` pack against the real
// pipeline, one block for each requirement of specs/deployment-placement. The helpers are in
// deployment_placement_helpers_test.go; the scope, conflict, and finding blocks are in the files next to this one.

// ---- INFRA-PLC-001 is a typed placement rule ----

// plcIsPlacedInPlan reports whether the plan is a forAll placed-in obligation of the type from the workload.
func plcIsPlacedInPlan(plan *policy.ObligationSpec, typ policy.ObligationType) bool {
	if plan.Type != typ || plan.Multiplicity != policy.MultiplicityForAll {
		return false
	}

	return plan.Relation.Kind == observation.KindPlacedIn && plan.Relation.Direction == policy.DirectionFromWorkload
}

func TestPlacementRulesAreTypedPlacementRules(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	for _, id := range plcBothRules() {
		rule := ruleOf(t, d.run, id)

		if rule.Plan.Family != "deployment-placement" || rule.Plan.Obligation == nil {
			t.Errorf("%s: family %q, obligation %+v", id, rule.Plan.Family, rule.Plan.Obligation)
		}
	}

	if plan := ruleOf(t, d.run, plcRule1).Plan.Obligation; !plcIsPlacedInPlan(plan, policy.ObligationRequired) {
		t.Errorf("INFRA-PLC-001 obligation = %+v", plan)
	}

	if plan := ruleOf(t, d.run, plcRule2).Plan.Obligation; !plcIsPlacedInPlan(plan, policy.ObligationForbidden) {
		t.Errorf("INFRA-PLC-002 obligation = %+v", plan)
	}
}

// plcPlacesInProdCluster reports whether the placement of the obligation is the container of the declared relationship.
func plcPlacesInProdCluster(p *conformance.Placement) bool {
	if p == nil {
		return false
	}

	return p.Container == "prod-cluster" && p.RelationshipID == "services-in-prod"
}

// plcObligationSubjects gives the subject and environment of each obligation, and checks the placement of each.
func plcObligationSubjects(t *testing.T, id string, obligations []conformance.ObligationResult) map[string]bool {
	t.Helper()

	seen := map[string]bool{}

	for i := range obligations {
		ob := &obligations[i]
		seen[ob.Subject+"/"+ob.Environment] = true

		if !plcPlacesInProdCluster(ob.Placement) {
			t.Errorf("%s for %s in %s: placement = %+v", id, ob.Subject, ob.Environment, ob.Placement)
		}
	}

	return seen
}

// Scenario: obligation per node and environment.
func TestPlacementHasOneObligationForEachNodeAndEnvironment(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	for _, id := range plcBothRules() {
		obligations := plcObligations(t, d, id)
		if len(obligations) != 4 {
			t.Fatalf("%s has %d obligations, want 4: %+v", id, len(obligations), obligations)
		}

		seen := plcObligationSubjects(t, id, obligations)

		for _, key := range []string{"api-service/production", "api-service/staging", "worker-service/production", "worker-service/staging"} {
			if !seen[key] {
				t.Errorf("%s has no obligation %s: %v", id, key, seen)
			}
		}
	}

	// The container is not a subject: it is not deployed in anything.
	plcNobody(t, d, plc001("prod-cluster", "production"))
}

const (
	// An environment with no manifest in its sources has no placed-in fact. Its coverage is complete.
	plcNothingEnvironments = "environments:\n  - id: production\n    cluster: prod-eu\n    sources: [\"nothing/**\"]\n  - id: staging\n    sources: [\"nothing-either/**\"]\n"

	plcPartialNothingEnvironments = "environments:\n  - id: production\n    cluster: prod-eu\n    sources: [\"nothing/**\", \"terraform/main.tf\"]\n  - id: staging\n    sources: [\"nothing-either/**\", \"terraform/main.tf\"]\n"
)

func plcEmptyEvidenceCompleteCoverage(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{bindings: plcBindings(), environments: plcNothingEnvironments})

	obligations := plcObligations(t, d, plcRule1)
	if len(obligations) != 4 {
		t.Fatalf("obligations = %d, want 4", len(obligations))
	}

	for i := range obligations {
		ob := &obligations[i]
		if ob.Verdict != conformance.VerdictViolation {
			t.Errorf("%s in %s = %s, want violation", ob.Subject, ob.Environment, ob.Verdict)
		}

		plcOccurrence(t, ob, conformance.OccurrenceAbsent)
	}
}

func plcEmptyEvidenceIncompleteExtraction(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{bindings: plcBindings(), environments: plcPartialNothingEnvironments})

	obligations := plcObligations(t, d, plcRule1)
	for i := range obligations {
		ob := &obligations[i]
		if ob.Verdict != conformance.VerdictUnknown || ob.Reason != conformance.ReasonCoverageIncomplete {
			t.Errorf("%s in %s = %s (%s), want unknown (coverage-incomplete)", ob.Subject, ob.Environment, ob.Verdict, ob.Reason)
		}
	}
}

// Scenario: empty evidence. No fact of any workload: the obligations exist and are violations (complete coverage).
func TestPlacementObligationsExistWithEmptyEvidence(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "complete_coverage_gives_violation", run: plcEmptyEvidenceCompleteCoverage},
		{name: "incomplete_extraction_gives_unknown", run: plcEmptyEvidenceIncompleteExtraction},
	})
}

// Scenario: model without deployed-in (both rules).
func TestPlacementModelWithoutDeployedInHasNoObligation(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{
		architecture: deploymentArchitectureWithoutPlacement,
		bindings:     plcBindings(workloadBinding("bind-api-workload", "api-service", "production", "api", "payments")),
	})

	for _, id := range plcBothRules() {
		rule := ruleOf(t, d.run, id)
		if len(rule.Obligations) != 0 || rule.Verdict != conformance.VerdictNotApplicable {
			t.Errorf("%s: verdict %s with %d obligations, want not-applicable with none", id, rule.Verdict, len(rule.Obligations))
		}
	}
}

// Scenario: advisory default. A violation with no `check.enforcement` is advisory and never sets exit code 1.
func TestPlacementViolationIsAdvisoryByDefault(t *testing.T) {
	t.Parallel()

	// Both environments are bound completely, so that the only findings are the placement violations of the worker.
	bindings := plcBindings(
		workloadBinding("bind-api-workload", "api-service", "production", "api", "payments"),
		workloadBinding("bind-api-staging", "api-service", "staging", "api", "payments-staging"),
		plcNamespace("bind-ns-prod", "prod-cluster", "production", "payments"),
		plcNamespace("bind-ns-staging", "prod-cluster", "staging", "payments-staging"),
	)
	d := deploymentRun(t, &deploymentSpec{bindings: bindings})

	key := plc001("worker-service", "production")

	ob := plcWantVerdict(t, d, key, conformance.VerdictViolation)
	if ob.Enforcement != policy.EnforcementAdvisory {
		t.Errorf("obligation enforcement = %q, want advisory", ob.Enforcement)
	}

	finding := plcFinding(t, d.run, key)
	if finding.Enforcement != policy.EnforcementAdvisory || finding.Result != conformance.VerdictViolation {
		t.Errorf("finding = %+v", finding)
	}

	if res := d.cli(t, "check", "--format", "json"); res.code == 1 {
		t.Errorf("check exit code = 1 with only advisory placement findings:\n%s", res.stderr)
	}
}

// ---- Placement needs a placed-in fact and a bound hosting target ----

func plcHostingTargetIsNamespace(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	ob := plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictPass)
	occ := plcOccurrence(t, ob, conformance.OccurrenceMatching)

	if occ.Kind != observation.KindPlacedIn || !plcLocatedAt(occ.Location, "k8s/prod/deployment.yaml") {
		t.Errorf("occurrence = %+v", occ)
	}
}

// plcIsSynthesizedStagingPayments reports whether the entity is the synthesized Namespace `payments` of staging.
func plcIsSynthesizedStagingPayments(e *observation.ObservedNode) bool {
	if e.Label != observation.LabelNamespace || e.Environment != "staging" {
		return false
	}

	return strings.HasPrefix(e.ObservedID, "synth:namespace:staging/payments")
}

func plcHasSynthesizedStagingPayments(d *deployment) bool {
	entities := d.run.Evidence.Entities
	for i := range entities {
		if plcIsSynthesizedStagingPayments(&entities[i]) {
			return true
		}
	}

	return false
}

func plcHostingTargetSynthesizedNamespace(t *testing.T) {
	t.Helper()

	// The manifests of staging declare no Namespace object for `payments`: the entity is synthesized.
	d := deploymentRun(t, &deploymentSpec{extraBindings: plcNamespace("bind-ns-synthesized", "prod-cluster", "staging", "payments") +
		plcNamespace("bind-ns-staging", "prod-cluster", "staging", "payments-staging")})

	if !plcHasSynthesizedStagingPayments(d) {
		t.Fatal("the run has no synthesized Namespace entity of staging for `payments`")
	}

	ob := plcWantVerdict(t, d, plc001("worker-service", "staging"), conformance.VerdictPass)
	if occ := plcOccurrence(t, ob, conformance.OccurrenceMatching); !plcLocatedAt(occ.Location, "k8s/staging/worker.yaml") {
		t.Errorf("occurrence = %+v", occ)
	}

	plcWantVerdict(t, d, plc001("api-service", "staging"), conformance.VerdictPass)
}

// plcPlacedFactsFrom gives the placed-in facts that start at the node.
func plcPlacedFactsFrom(run *conformance.Run, from string) []*observation.EvidenceEdge {
	return slices.DeleteFunc(placementFacts(run), func(e *observation.EvidenceEdge) bool { return e.From != from })
}

// plcRequireDetailNamesUnrenderedTemplate checks that the detail of the obligation names the unrendered-template loss.
func plcRequireDetailNamesUnrenderedTemplate(t *testing.T, ob *conformance.ObligationResult) {
	t.Helper()

	if !strings.Contains(ob.Detail, observation.LossUnrenderedTemplate) {
		t.Errorf("detail = %q, want it to name %s", ob.Detail, observation.LossUnrenderedTemplate)
	}
}

func plcHostingTargetNotResolved(t *testing.T) {
	t.Helper()

	// The Helm template names the workload `<.Release.Name>-api` and has no namespace: the provider namespace is `?`.
	d := deploymentRun(t, &deploymentSpec{environments: plcEnvironments("charts/api/**")})
	chart := plcManifestNode(t, d, "charts/api/templates/deployment.yaml")

	for _, edge := range plcPlacedFactsFrom(d.run, chart) {
		t.Errorf("the template workload has a placed-in fact to %s: a default namespace was assumed", edge.To)
	}

	rec := predicateRecord(t, d.run, "placed-in", observation.CoverageScope{Environment: "production"})
	if rec.Status != observation.CoveragePartial || !rec.HasLossMode(observation.LossUnrenderedTemplate) {
		t.Fatalf("production placed-in = %+v", rec)
	}

	ob := plcWantUnknown(t, d, plc001("worker-service", "production"), conformance.ReasonCoverageIncomplete)
	plcRequireDetailNamesUnrenderedTemplate(t, ob)
}

// plcIsStagingBindingGapFinding reports whether the finding is the unknown result that asks to resolve a binding in staging.
func plcIsStagingBindingGapFinding(f *conformance.Finding) bool {
	if !plcLocatedUnder(f.Location, "k8s/staging/") {
		return false
	}

	return f.NextAction == plcResolveBinding && f.Result == conformance.VerdictUnknown
}

func plcHostingTargetHasNoBinding(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	for _, key := range []plcObligationKey{plc001("api-service", "staging"), plc002API("staging")} {
		ob := plcWantUnknown(t, d, key, conformance.ReasonBindingGap)
		occ := plcOccurrence(t, ob, conformance.OccurrenceBindingGap)

		if occ.NextAction != plcResolveBinding || !plcLocatedAt(occ.Location, "k8s/staging/namespace.yaml") {
			t.Errorf("%s: occurrence = %+v", key.rule, occ)
		}
	}

	if finding := plcFinding(t, d.run, plc001("api-service", "staging")); !plcIsStagingBindingGapFinding(finding) {
		t.Errorf("finding = %+v", finding)
	}
}

func TestPlacementHostingTarget(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "namespace_is_the_hosting_target", run: plcHostingTargetIsNamespace},
		{name: "synthesized_namespace_is_bound_by_an_infraResource_binding", run: plcHostingTargetSynthesizedNamespace},
		{name: "hosting_target_not_resolved", run: plcHostingTargetNotResolved},
		{name: "hosting_target_has_no_binding", run: plcHostingTargetHasNoBinding},
	})
}

// ---- A deployment action or selector join is not placement ----

// plcDeploysSpec is the workspace with a rule that names the `deploys` token and the scripts bound to the worker.
func plcDeploysSpec() *deploymentSpec {
	// A rule that names the `deploys` token makes the run read the deploys edges. scripts/deploy.sh runs
	// `kubectl apply -f k8s/prod/deployment.yaml` and `helm upgrade`. The scripts are bound to the worker, which has no workload.
	head := strings.Replace(deploymentWorkspaceHead, "  files: []\n", "  files: [policies/deploys.policy.yaml]\n", 1)
	policyText := "apiVersion: concordia.io/v1alpha1\nkind: PolicyPack\nmetadata:\n  id: deploys\n  version: 1.0.0\nrules:\n" +
		forbidRule("CO-DEP-100", "worker-service", "api-service", "deploys")

	return &deploymentSpec{
		files: map[string]string{
			"concordia.yaml":               head + deploymentCheck + plcEnvironments("scripts/**"),
			"policies/deploys.policy.yaml": policyText,
		},
		extraBindings: sourceRootBinding("bind-scripts", "worker-service", "scripts/**"),
	}
}

// plcDeploysTargets gives the label and file of the target of each deploys fact.
func plcDeploysTargets(run *conformance.Run, deploys []*observation.EvidenceEdge) map[string]bool {
	targets := map[string]bool{}

	for _, edge := range deploys {
		node := run.Evidence.Nodes[edge.To]
		targets[node.Label+" "+node.FilePath] = true
	}

	return targets
}

// plcIsDeploysOccurrence reports whether the occurrence is a deploys fact.
func plcIsDeploysOccurrence(occ *conformance.OccurrenceResult) bool {
	return occ.Kind == observation.KindDeploysArtifact
}

func plcDeploysFactAlone(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, plcDeploysSpec())

	deploys := evidenceEdges(d.run, observation.KindDeploysArtifact)
	if len(deploys) == 0 {
		t.Fatal("the run has no deploys-artifact fact")
	}

	for _, edge := range deploys {
		if edge.Kind == observation.KindPlacedIn {
			t.Errorf("deploys fact %s is a placement", edge.ID)
		}
	}

	// The action has a file or a chart as its target; none is a hosting target.
	if targets := plcDeploysTargets(d.run, deploys); !targets["File k8s/prod/deployment.yaml"] {
		t.Errorf("deploys targets = %v, want the file k8s/prod/deployment.yaml", targets)
	}

	ob := plcWantVerdict(t, d, plc001("worker-service", "production"), conformance.VerdictViolation)
	plcRequireFixImplementation(t, ob)

	for _, occ := range plcOccurrencesWhere(ob, plcIsDeploysOccurrence) {
		t.Errorf("a deploys fact is an occurrence of the placement: %+v", occ)
	}
}

// plcRequireNoSelectorJoin checks that no occurrence of the obligation is a selector join.
func plcRequireNoSelectorJoin(t *testing.T, ob *conformance.ObligationResult) {
	t.Helper()

	isSelector := func(occ *conformance.OccurrenceResult) bool { return occ.Kind == observation.KindSelectsWorkload }

	for _, occ := range plcOccurrencesWhere(ob, isSelector) {
		t.Errorf("%s for %s in %s: a selector join is an occurrence: %+v", ob.RuleID, ob.Subject, ob.Environment, occ)
	}
}

func plcSelectorJoinIsNotPlacement(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	// Service api of production selects Deployment api (selects-workload). The Service of the worker is in
	// the file of another workload and gives no fact (O6).
	if len(evidenceEdges(d.run, observation.KindSelectsWorkload)) == 0 {
		t.Fatal("the run has no selects-workload fact")
	}

	for _, rule := range plcBothRules() {
		obligations := plcObligations(t, d, rule)
		for i := range obligations {
			plcRequireNoSelectorJoin(t, &obligations[i])
		}
	}

	plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictPass)
	plcWantVerdict(t, d, plc001("worker-service", "production"), conformance.VerdictViolation)
}

func TestPlacementIgnoresDeploymentActionsAndSelectors(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "deploys_fact_alone", run: plcDeploysFactAlone},
		{name: "selector_join_is_not_placement", run: plcSelectorJoinIsNotPlacement},
	})
}

// ---- Satisfied placement ----

// plcIsDecisiveManifestOccurrence reports whether the occurrence is a decisive fact with the line of the production manifest.
func plcIsDecisiveManifestOccurrence(occ *conformance.OccurrenceResult) bool {
	if !plcLocatedAt(occ.Location, "k8s/prod/deployment.yaml") {
		return false
	}

	return occ.Location.Line != 0 && occ.Decisive
}

func plcBaselinePass(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	ob := plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictPass)
	occ := plcOccurrence(t, ob, conformance.OccurrenceMatching)

	if !plcIsDecisiveManifestOccurrence(occ) {
		t.Errorf("occurrence = %+v", occ)
	}

	if len(occ.Bindings) == 0 {
		t.Errorf("the result names no binding: %+v", occ)
	}
}

func plcPassWithPartialEnvironment(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{environments: plcEnvironments("charts/api/**")})

	if rec := predicateRecord(t, d.run, "placed-in", observation.CoverageScope{Environment: "production"}); rec.Status != observation.CoveragePartial {
		t.Fatalf("production placed-in = %+v, want partial", rec)
	}

	plcWantVerdict(t, d, plc001("api-service", "production"), conformance.VerdictPass)
}

func TestPlacementPassShowsTheManifestLocation(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "baseline_pass", run: plcBaselinePass},
		{name: "pass_with_a_partial_environment", run: plcPassWithPartialEnvironment},
	})
}

// ---- Absent placement ----

func plcAbsentCompleteCoverage(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	key := plc001("worker-service", "production")

	plcRequireFixImplementation(t, plcWantVerdict(t, d, key, conformance.VerdictViolation))

	if finding := plcFinding(t, d.run, key); finding.NextAction != plcFixImplementation {
		t.Errorf("finding next action = %q", finding.NextAction)
	}
}

func plcAbsentIncompleteExtraction(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{environments: plcEnvironments("charts/api/**")})

	ob := plcWantUnknown(t, d, plc001("worker-service", "production"), conformance.ReasonCoverageIncomplete)
	plcRequireDetailNamesUnrenderedTemplate(t, ob)

	if actions := plcNextActions(ob); !slices.Equal(actions, []string{plcSupplyEvidence}) {
		t.Errorf("next actions = %v", actions)
	}
}

func plcAbsentUnsupportedFamily(t *testing.T) {
	t.Helper()

	// A CBM build with no capability profile supports no family.
	d := deploymentRun(t, &deploymentSpec{recording: plcRecordingVersion(t, "0.12.0")})

	for _, env := range []string{"production", "staging"} {
		ob := plcWantUnknown(t, d, plc001("worker-service", env), conformance.ReasonUnsupportedFamily)
		if actions := plcNextActions(ob); !slices.Equal(actions, []string{"fix analysis configuration"}) {
			t.Errorf("%s: next actions = %v", env, actions)
		}
	}
}

func TestPlacementAbsent(t *testing.T) {
	t.Parallel()

	plcRunSubtests(t, []plcSubtest{
		{name: "complete_coverage_gives_violation", run: plcAbsentCompleteCoverage},
		{name: "incomplete_extraction_gives_unknown", run: plcAbsentIncompleteExtraction},
		{name: "unsupported_family", run: plcAbsentUnsupportedFamily},
	})
}
