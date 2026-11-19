package integration_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
)

// These tests check the deployment evidence of the infra recording (O1 to O11): the placement facts that a
// check derives, and the coverage of `deployment-workload-set`, `placed-in`, and `dockerfile-set`.

func keepEvidence(opts *conformance.CheckOptions) { opts.KeepEvidence = true }

func placementFacts(run *conformance.Run) []*observation.EvidenceEdge {
	return slices.DeleteFunc(slices.Clone(run.Evidence.Edges), func(e *observation.EvidenceEdge) bool {
		return e.Kind != observation.KindPlacedIn
	})
}

// evidenceNodeRef addresses one observed node by its source file, object key, and environment.
type evidenceNodeRef struct {
	file, objKey, env string
}

func nodeOf(t *testing.T, run *conformance.Run, ref evidenceNodeRef) observation.ObservedNode {
	t.Helper()

	for id := range run.Evidence.Nodes {
		node := run.Evidence.Nodes[id]
		if node.FilePath != ref.file || node.Environment != ref.env {
			continue
		}

		if key, ok := observation.ObjKeyOf(&node); ok && key == ref.objKey {
			return node
		}
	}

	t.Fatalf("no node %s of %s in %s", ref.objKey, ref.file, ref.env)

	return observation.ObservedNode{}
}

// hasParsedSourceShape reports whether the fact is an exact placed-in fact parsed from a source.
func hasParsedSourceShape(f *observation.EvidenceEdge) bool {
	return f.Kind == observation.KindPlacedIn && f.Role == observation.RoleParsedSourceFact &&
		f.Resolution == observation.ResolutionExact
}

// hasSyntacticProvenance reports whether the fact is derived by the namespace rule and carries its source span.
func hasSyntacticProvenance(f *observation.EvidenceEdge) bool {
	if f.Derivation == nil || f.SourceSpan == nil {
		return false
	}

	return f.Confidence == observation.ConfidenceSyntactic && f.Derivation.Rule == "placement-from-namespace" &&
		f.NormalizationVersion == observation.NormalizationVersion
}

func assertPlacementFactShape(t *testing.T, run *conformance.Run, f *observation.EvidenceEdge) {
	t.Helper()

	if !hasParsedSourceShape(f) || !hasSyntacticProvenance(f) {
		t.Errorf("fact = %+v", f)
	}

	// A templated name or an unstated namespace gives no fact.
	node := run.Evidence.Nodes[f.From]
	if strings.Contains(node.QualifiedName, "{{") || strings.Contains(node.QualifiedName, "/?/") {
		t.Errorf("fact for an unrendered resource: %+v", node)
	}
}

func factFrom(facts []*observation.EvidenceEdge, from string) *observation.EvidenceEdge {
	var found *observation.EvidenceEdge

	for _, f := range facts {
		if f.From == from {
			found = f
		}
	}

	return found
}

func TestInfraCheckDerivesPlacementFromTheWorkloadNodes(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraRecordingFile, true, keepEvidence)
	facts := placementFacts(got.run)

	if len(facts) == 0 {
		t.Fatal("no placed-in fact")
	}

	api := nodeOf(t, got.run, evidenceNodeRef{infraProdDeploy, "apps/Deployment/payments/api", "production"})
	namespace := nodeOf(t, got.run, evidenceNodeRef{"k8s/prod/namespace.yaml", "core/Namespace/_/payments", "production"})

	for _, f := range facts {
		assertPlacementFactShape(t, got.run, f)
	}

	assertAPIFactTargetsNamespace(t, factFrom(facts, api.ObservedID), &namespace)

	// The staging worker is in a namespace that only the production sources declare: its target is a
	// synthesized entity of staging, never the production Namespace node.
	staging := nodeOf(t, got.run, evidenceNodeRef{"k8s/staging/worker.yaml", "apps/Deployment/payments/worker", "staging"})

	for _, f := range facts {
		if f.From == staging.ObservedID && (f.To == namespace.ObservedID || f.Environment != "staging") {
			t.Errorf("staging fact = %+v", f)
		}
	}
}

func assertAPIFactTargetsNamespace(t *testing.T, apiFact *observation.EvidenceEdge, namespace *observation.ObservedNode) {
	t.Helper()

	if apiFact == nil {
		t.Fatalf("api fact = %+v, want an edge to %s", apiFact, namespace.ObservedID)
	}

	if apiFact.To != namespace.ObservedID || apiFact.Environment != "production" || apiFact.SourceSpan.Path != infraProdDeploy {
		t.Fatalf("api fact = %+v, want an edge to %s", apiFact, namespace.ObservedID)
	}
}

func synthesizedNamespaces(run *conformance.Run) []observation.ObservedNode {
	var synthesized []observation.ObservedNode

	for i := range run.Evidence.Entities {
		if run.Evidence.Entities[i].Label == observation.LabelNamespace {
			synthesized = append(synthesized, run.Evidence.Entities[i])
		}
	}

	return synthesized
}

func infraResourceBinding(entity *observation.ObservedNode) []binding.Binding {
	return []binding.Binding{{
		ID: "ns", ArchitectureRef: "container", Status: binding.StatusActive,
		Match: []binding.Match{{Kind: binding.MatchInfraResource, Environment: entity.Environment, Address: entity.Properties["obj_key"]}},
	}}
}

func TestInfraCheckSynthesizedNamespaceIsAddressedByABinding(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraRecordingFile, true, keepEvidence)
	synthesized := synthesizedNamespaces(got.run)

	if len(synthesized) == 0 {
		t.Fatal("no synthesized Namespace entity: every namespace of the recording is declared in its environment")
	}

	for i := range synthesized {
		entity := &synthesized[i]
		if entity.Resolution != observation.ResolutionUnbound || !strings.HasPrefix(entity.ObservedID, "synth:namespace:") {
			t.Fatalf("entity = %+v", entity)
		}

		set := binding.Resolve(infraResourceBinding(entity), synthesized, nil)
		if !slices.Contains(set.ByObserved[entity.ObservedID], "container") {
			t.Errorf("%s is not addressed by its infraResource binding", entity.ObservedID)
		}
	}
}

func assertProductionWorkloadSetGaps(t *testing.T, set *observation.PredicateCoverage) {
	t.Helper()

	if set.Status != observation.CoveragePartial {
		t.Fatalf("production deployment-workload-set = %+v", set)
	}

	for _, mode := range []string{observation.LossParsePartial, observation.LossUnrenderedTemplate} {
		if len(gapsOf(set, mode)) == 0 {
			t.Errorf("production deployment-workload-set lacks %s: %+v", mode, set.LossModes)
		}
	}

	gaps := gapsOf(set, observation.LossParsePartial)
	if len(gaps) == 0 {
		return
	}

	if gaps[0].Path != "charts/api/templates/deployment.yaml" || gaps[0].Line != 1 || !strings.Contains(gaps[0].Detail, "1-1") {
		t.Errorf("parse-partial = %+v", gaps)
	}
}

func assertPlacedInMatchesWorkloadSet(t *testing.T, run *conformance.Run, env string) {
	t.Helper()

	scope := observation.CoverageScope{Environment: env}
	set := predicateRecord(t, run, observation.FamilyDeploymentWorkloadSet, scope)
	placed := predicateRecord(t, run, "placed-in", scope)

	sameStatus := placed.Status == set.Status && slices.Equal(placed.Reasons, set.Reasons)
	sameLosses := placed.Unresolved == set.Unresolved && len(placed.LossModes) == len(set.LossModes)

	if !sameStatus || !sameLosses {
		t.Errorf("%s: placed-in = %+v, workload set = %+v", env, placed, set)
	}
}

func TestInfraCheckDeploymentCoverage(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraRecordingFile, true)

	// O11: the Helm chart and its partly parsed template limit the production workload set.
	production := predicateRecord(t, got.run, observation.FamilyDeploymentWorkloadSet, observation.CoverageScope{Environment: "production"})
	assertProductionWorkloadSetGaps(t, production)

	// O1: the edge sources are under deploy/.
	edge := predicateRecord(t, got.run, observation.FamilyDeploymentWorkloadSet, observation.CoverageScope{Environment: "edge"})
	if edge.Status != observation.CoveragePartial || len(gapsOf(edge, observation.LossDefaultExcludedDirectory)) != 1 {
		t.Errorf("edge deployment-workload-set = %+v", edge)
	}

	// The placed-in record of each environment is the record of its workload set.
	for _, env := range []string{"production", "staging", "edge"} {
		assertPlacedInMatchesWorkloadSet(t, got.run, env)
	}

	// The workloads of staging all have a provider node, and its manifests are plain.
	staging := predicateRecord(t, got.run, observation.FamilyDeploymentWorkloadSet, observation.CoverageScope{Environment: "staging"})
	if staging.Status != observation.CoverageComplete {
		t.Errorf("staging deployment-workload-set = %+v", staging)
	}
}

func TestInfraCheckDockerfileSetIsCompleteForTheRecording(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraRecordingFile, true)

	rec := predicateRecord(t, got.run, observation.FamilyDockerfileSet, observation.CoverageScope{Project: infraProject})
	if rec.Status != observation.CoverageComplete || len(rec.LossModes)+len(rec.Reasons) != 0 {
		t.Errorf("dockerfile-set = %+v", rec)
	}

	// The set says nothing about image links.
	built := predicateRecord(t, got.run, "built-from", observation.CoverageScope{Project: infraProject})
	if !built.HasLossMode(observation.LossBuildLinkNeedsBuildCommand) {
		t.Errorf("built-from = %+v", built)
	}
}

func TestInfraCheckWithoutAnIndexResultLeavesTheDeploymentSetsUnknown(t *testing.T) {
	t.Parallel()

	got := runInfra(t, infraRecordingFile, false)

	for _, env := range []string{"production", "staging", "edge"} {
		for _, family := range []string{observation.FamilyDeploymentWorkloadSet, "placed-in"} {
			rec := predicateRecord(t, got.run, family, observation.CoverageScope{Environment: env})
			if rec.Status != observation.CoverageUnknown || !slices.Contains(rec.Reasons, observation.ReasonIndexUnavailable) {
				t.Errorf("%s %s = %+v", env, family, rec)
			}
		}
	}

	if rec := predicateRecord(t, got.run, observation.FamilyDockerfileSet, observation.CoverageScope{Project: infraProject}); rec.Status != observation.CoverageUnknown {
		t.Errorf("dockerfile-set = %+v", rec)
	}
}

// A Terraform (or Compose) file in the sources makes the placement of the environment unknown, never a pass or a
// violation, and the obligation names the predicate, its status, the scope, and the reason.
func TestInfraCheckUnsupportedSourceMakesPlacementUnknown(t *testing.T) {
	t.Parallel()

	environments := `environments:
  - id: staging
    cluster: stg-eu
    sources: ["k8s/staging/**", "terraform/main.tf"]
`
	policies := typedRule("CO-PLC-001", "payments-worker", "required", "placed-in", "from-workload", "", "      scope: {environments: [staging]}\n")
	bindings := foundationDefaultBindings + workloadBinding("bind-worker", "payments-worker", "staging", "worker", "payments")

	f := foundationRun(t, &foundationSpec{index: true, environments: environments, bindings: bindings, policies: policies})

	record := predicateRecord(t, f.run, "placed-in", observation.CoverageScope{Environment: "staging"})
	if record.Status != observation.CoverageUnknown || !slices.Contains(record.Reasons, "unsupported deployment source: terraform/main.tf") {
		t.Fatalf("placed-in in staging = %+v", record)
	}

	rule := ruleOf(t, f.run, "CO-PLC-001")
	worker := obligationFor(t, rule, "payments-worker", "staging")

	if worker.Verdict != conformance.VerdictUnknown || worker.Reason != conformance.ReasonCoverageIncomplete {
		t.Fatalf("worker obligation = %+v", worker)
	}

	want := "placed-in is unknown in staging (unsupported deployment source: terraform/main.tf)"
	if worker.Detail != want {
		t.Errorf("detail = %q, want %q", worker.Detail, want)
	}
}

// Without the unsupported source the same placement obligation passes in staging.
func TestInfraCheckPlacementPassesWhenTheWorkloadSetIsComplete(t *testing.T) {
	t.Parallel()

	environments := "environments:\n  - id: staging\n    cluster: stg-eu\n    sources: [\"k8s/staging/**\"]\n"
	policies := typedRule("CO-PLC-001", "payments-worker", "required", "placed-in", "from-workload", "", "      scope: {environments: [staging]}\n")
	bindings := foundationDefaultBindings + workloadBinding("bind-worker", "payments-worker", "staging", "worker", "payments")

	f := foundationRun(t, &foundationSpec{index: true, environments: environments, bindings: bindings, policies: policies})

	worker := obligationFor(t, ruleOf(t, f.run, "CO-PLC-001"), "payments-worker", "staging")
	if worker.Verdict != conformance.VerdictPass {
		t.Fatalf("worker obligation = %+v", worker)
	}
}
