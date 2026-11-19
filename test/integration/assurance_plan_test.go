package integration_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/conformance"
	deptrace "github.com/inclaro/concordia/internal/deployment"
)

// A plan is evidence of the level rendered-or-evaluated: what would be applied. It binds to a CALM element through an
// `infraResource` binding that names its address, and it is never evidence of a deployment (spec
// rendered-infrastructure-evidence, "Evaluated plans are ingested as evaluated-level evidence").

const planBucketBinding = `  - id: bind-logs-bucket
    architectureRef: calm:node:api-service
    match:
      any:
        - {kind: infraResource, environment: production, address: aws_s3_bucket.logs}
`

const planDeployClaimPolicy = `apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: plan-claims
  version: 1.0.0
rules:
  - id: PLAN-DEP-001
    name: The deployment of production succeeded
    severity: warning
    family: assurance
    require:
      claim:
        id: deployment-succeeded
        environment: production
`

// planWorld is the A10 workspace with a production plan that holds aws_s3_bucket.logs, bound to api-service.
func planWorld(t *testing.T, withClaimRule bool) *deployment {
	t.Helper()

	plan, err := os.ReadFile("../fixtures/assurance/plans/handwritten-s3-update.json")
	if err != nil {
		t.Fatal(err)
	}

	spec := (&provWorld{noRendering: true}).spec(t)
	spec.files["build/plans/production.json"] = string(plan)
	spec.files["concordia.yaml"] += "  plans:\n    - {environment: production, tool: terraform, file: build/plans/production.json}\n"
	spec.extraBindings = planBucketBinding

	if withClaimRule {
		spec.files["policies/plan-claims.policy.yaml"] = planDeployClaimPolicy
		spec.files["concordia.yaml"] = strings.Replace(spec.files["concordia.yaml"], "  files: []\n", "  files: [policies/plan-claims.policy.yaml]\n", 1)
	}

	return deploymentRun(t, spec)
}

// isProductionTerraformPlan checks the plan entry against the declared plan file.
func isProductionTerraformPlan(plan *conformance.PlanEntry) bool {
	if plan.Environment != "production" || plan.Tool != "terraform" {
		return false
	}

	return plan.File == "build/plans/production.json" && plan.FormatVersion == "1.2"
}

// isUnadmittedEvaluatedPlan checks what the loader derives from the plan file: a digest, three resources, the level,
// and no integrity proof.
func isUnadmittedEvaluatedPlan(plan *conformance.PlanEntry) bool {
	if plan.Digest == "" || plan.ResourceCount != 3 || plan.Level != deptrace.LevelRenderedOrEvaluated {
		return false
	}

	return plan.Integrity == assurance.IntegrityNotVerified && !plan.Admissible && len(plan.Details) != 0
}

func isBoundPlannedBucket(entity *deptrace.Entity) bool {
	if entity.Kind != deptrace.KindPlannedResource || entity.Identity != "aws_s3_bucket.logs" {
		return false
	}

	return entity.Locations[0].Path == "build/plans/production.json" && entity.Bindings[0] == "bind-logs-bucket"
}

func isEvaluatedByPlan(production *deptrace.EnvironmentTrace) bool {
	if production == nil || !production.Evaluated.Evaluated() {
		return false
	}

	return production.Evaluated.Level == deptrace.LevelRenderedOrEvaluated && production.Evaluated.Sources[0].Kind == deptrace.SourcePlan
}

func TestACalmNodeBindsAPlannedResourceAtTheEvaluatedLevel(t *testing.T) {
	t.Parallel()

	d := planWorld(t, false)
	run := d.run

	if len(run.Assurance.Plans) != 1 {
		t.Fatalf("plans = %+v", run.Assurance.Plans)
	}

	plan := run.Assurance.Plans[0]
	if !isProductionTerraformPlan(&plan) || !isUnadmittedEvaluatedPlan(&plan) {
		t.Errorf("plan = %+v", plan)
	}

	// The trace lists the planned resource beside the declared chain, at the level rendered-or-evaluated.
	sub := subjectTrace(t, d, "api-service", "production")
	if sub.Level != deptrace.LevelDeclared {
		t.Errorf("subject level = %s, want declared", sub.Level)
	}

	if len(sub.Evaluated) != 1 || !isBoundPlannedBucket(&sub.Evaluated[0]) {
		t.Fatalf("evaluated = %+v", sub.Evaluated)
	}

	if production := environmentTraceNamed(&run.DeploymentTrace, "production"); !isEvaluatedByPlan(production) {
		t.Errorf("production = %+v", production)
	}
}

func isExecutionOrRuntimeLevel(level string) bool {
	return level == deptrace.LevelDeploymentExecution || level == deptrace.LevelRuntimeObservation
}

// requireNoSuccessfulDeploymentInTrace checks that the trace of the canonical results does not claim a deployment.
func requireNoSuccessfulDeploymentInTrace(t *testing.T, run *conformance.Run) {
	t.Helper()

	text, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(text, &doc); err != nil {
		t.Fatal(err)
	}

	trace, err := json.Marshal(doc["deploymentTrace"])
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(strings.ToLower(string(trace)), "deployment succeeded") || strings.Contains(string(trace), `"deployment-succeeded"`) {
		t.Error("the deployment trace claims a successful deployment")
	}
}

func TestAPlanNeverGivesADeploymentLevelOrASuccessfulDeployment(t *testing.T) {
	t.Parallel()

	d := planWorld(t, true)
	run := d.run

	for _, l := range run.DeploymentTrace.Levels {
		if isExecutionOrRuntimeLevel(l.Level) && l.Status != "not-available" {
			t.Errorf("level %s = %s, want not-available with a plan only", l.Level, l.Status)
		}
	}

	// A rule that needs the claim `deployment-succeeded` is not satisfied by a plan.
	rule := provRuleOf(t, run, "PLAN-DEP-001")
	if rule.Verdict != conformance.VerdictUnknown {
		t.Fatalf("PLAN-DEP-001 passes on a plan: %+v", rule.Obligations)
	}

	// No result says that something was deployed because of the plan: the plan is a plan entry and a planned resource.
	requireNoSuccessfulDeploymentInTrace(t, run)

	for _, entity := range subjectTrace(t, d, "api-service", "production").Evaluated {
		if entity.Kind == deptrace.KindDeploymentAction || entity.Kind == deptrace.KindBuildProvenance {
			t.Errorf("evaluated entity of kind %s from a plan", entity.Kind)
		}
	}
}
