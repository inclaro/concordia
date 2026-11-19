package observation_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/observation"
)

// plResource makes a provider `Resource` node with an obj_key in an environment, at line 1 of its file.
func plResource(id, file, objKey, env string) observation.ObservedNode {
	return observation.ObservedNode{
		ObservedID: id, Label: "Resource", FilePath: file, Environment: env, StartLine: 1,
		QualifiedName: "k8s::" + objKey, Properties: map[string]string{"obj_key": objKey},
	}
}

func plResult(nodes ...observation.ObservedNode) *observation.EvidenceQueryResult {
	result := &observation.EvidenceQueryResult{Nodes: map[string]observation.ObservedNode{}}
	for i := range nodes {
		result.Nodes[nodes[i].ObservedID] = nodes[i]
	}

	return result
}

func plPlacements(result *observation.EvidenceQueryResult) []*observation.EvidenceEdge {
	var out []*observation.EvidenceEdge

	for _, e := range result.Edges {
		if e.Kind == observation.KindPlacedIn {
			out = append(out, e)
		}
	}

	return out
}

func plClusterOf(env string) string {
	if env == "production" {
		return "prod-eu"
	}

	return ""
}

// plCheckFactEnds checks the endpoints and the environment of the one placement of the deployment in its namespace.
func plCheckFactEnds(t *testing.T, fact *observation.EvidenceEdge) {
	t.Helper()

	got := [3]string{fact.From, fact.To, fact.Environment}
	if want := [3]string{"cbm:p:1", "cbm:p:2", "production"}; got != want {
		t.Errorf("fact = %+v", fact)
	}
}

// plCheckFactQuality checks the role, the resolution, and the confidence of a parsed source fact.
func plCheckFactQuality(t *testing.T, fact *observation.EvidenceEdge) {
	t.Helper()

	got := [3]any{fact.Role, fact.Resolution, fact.Confidence}
	if want := [3]any{observation.RoleParsedSourceFact, observation.ResolutionExact, observation.ConfidenceSyntactic}; got != want {
		t.Errorf("quality = %s %s %s", fact.Role, fact.Resolution, fact.Confidence)
	}
}

// plCheckFactSpan checks that the span of the fact is the deployment file at line 3.
func plCheckFactSpan(t *testing.T, fact *observation.EvidenceEdge) {
	t.Helper()

	if fact.SourceSpan == nil {
		t.Errorf("span = %+v", fact.SourceSpan)

		return
	}

	if got := *fact.SourceSpan; got != (observation.SourceSpan{Path: "k8s/prod/deployment.yaml", Line: 3}) {
		t.Errorf("span = %+v", fact.SourceSpan)
	}
}

// plCheckFactDerivation checks that the rule, the version, and the input of the derivation are those of the placement rule.
func plCheckFactDerivation(t *testing.T, fact *observation.EvidenceEdge) {
	t.Helper()

	derivation := fact.Derivation
	if derivation == nil {
		t.Errorf("derivation = %+v", derivation)

		return
	}

	got := [2]any{derivation.Rule, derivation.Version}
	if got != [2]any{"placement-from-namespace", 1} || !slices.Equal(derivation.Inputs, []string{"cbm:p:1"}) {
		t.Errorf("derivation = %+v", derivation)
	}
}

// plCheckFactNative checks that a derived fact has no native kind and carries the normalization version and the graph generation.
func plCheckFactNative(t *testing.T, fact *observation.EvidenceEdge) {
	t.Helper()

	got := [3]string{fact.NativeKind, fact.NormalizationVersion, fact.GraphGeneration}
	if want := [3]string{"", observation.NormalizationVersion, "cbm:p:abcd"}; got != want {
		t.Errorf("native %q, version %q, generation %q", fact.NativeKind, fact.NormalizationVersion, fact.GraphGeneration)
	}

	if !observation.IsInfrastructureKind(fact.Kind) || observation.IsCodeKind(fact.Kind) {
		t.Errorf("kind %s must be an infrastructure kind", fact.Kind)
	}
}

func TestDerivePlacementWorkloadInNamespace(t *testing.T) {
	t.Parallel()

	deployment := plResource("cbm:p:1", "k8s/prod/deployment.yaml", "apps/Deployment/payments/api", "production")
	deployment.StartLine = 3
	namespace := plResource("cbm:p:2", "k8s/prod/namespace.yaml", "core/Namespace/_/payments", "production")
	result := plResult(deployment, namespace)
	result.Edges = []*observation.EvidenceEdge{{
		ID: "e1", From: "cbm:p:1", To: "cbm:p:9", Kind: observation.KindUsesImage, NativeKind: "USAGE",
		GraphGeneration: "cbm:p:abcd", Analyzer: observation.AnalyzerDescriptor{Name: "cbm", Version: "0.12.1", AdapterSchema: observation.AdapterSchema},
	}}

	if added := observation.DerivePlacement(result, plClusterOf); added != 1 {
		t.Fatalf("added = %d", added)
	}

	facts := plPlacements(result)
	if len(facts) != 1 {
		t.Fatalf("placements = %+v", facts)
	}

	fact := facts[0]
	plCheckFactEnds(t, fact)
	plCheckFactQuality(t, fact)
	plCheckFactSpan(t, fact)
	plCheckFactDerivation(t, fact)
	plCheckFactNative(t, fact)

	// The native fact is untouched and a declared Namespace gives no synthesized entity.
	if len(result.Edges) != 2 || !slices.ContainsFunc(result.Edges, func(e *observation.EvidenceEdge) bool { return e.ID == "e1" && e.Kind == observation.KindUsesImage }) {
		t.Errorf("edges = %+v", result.Edges)
	}

	if len(result.Entities) != 0 {
		t.Errorf("entities = %+v", result.Entities)
	}
}

func TestDerivePlacementDoesNotReadDeploymentActions(t *testing.T) {
	t.Parallel()

	script := observation.ObservedNode{ObservedID: "cbm:p:5", Label: "Module", FilePath: "scripts/deploy.sh", QualifiedName: "scripts.deploy"}
	file := observation.ObservedNode{ObservedID: "cbm:p:6", Label: "File", FilePath: "k8s/prod/deployment.yaml", QualifiedName: "k8s.prod.deployment.yaml"}
	result := plResult(script, file)
	result.Edges = []*observation.EvidenceEdge{{
		ID: "d1", From: "cbm:p:5", To: "cbm:p:6", Kind: observation.KindDeploysArtifact, NativeKind: "DEPLOYS",
		Properties: map[string]string{"via": "kubectl"},
	}}

	if added := observation.DerivePlacement(result, nil); added != 0 || len(result.Edges) != 1 {
		t.Fatalf("added = %d, edges = %+v", added, result.Edges)
	}
}

func TestDerivePlacementNamespaceNotStatedOrTemplated(t *testing.T) {
	t.Parallel()

	result := plResult(
		plResource("n1", "k8s/a.yaml", "apps/Deployment/?/api", "production"),
		plResource("n2", "charts/api/templates/d.yaml", "apps/Deployment/{{ .Values.ns }}/api", "production"),
		plResource("n3", "charts/api/templates/e.yaml", "apps/Deployment/payments/{{ .Release.Name }}", "production"),
	)

	if added := observation.DerivePlacement(result, nil); added != 0 || len(result.Entities) != 0 {
		t.Fatalf("added = %d, entities = %+v", added, result.Entities)
	}
}

func TestDerivePlacementClusterScopedAndOtherKinds(t *testing.T) {
	t.Parallel()

	result := plResult(
		plResource("n1", "k8s/a.yaml", "apps/Deployment/_/api", "production"),
		plResource("n2", "k8s/b.yaml", "core/Service/payments/api", "production"),
		plResource("n3", "k8s/c.yaml", "core/Namespace/_/payments", "production"),
		plResource("n4", "k8s/d.yaml", "core/ConfigMap/payments/cfg", "production"),
		observation.ObservedNode{ObservedID: "n5", Label: "Module", FilePath: "m.go", QualifiedName: "m", Environment: "production"},
	)

	if added := observation.DerivePlacement(result, nil); added != 0 {
		t.Fatalf("added = %d: %+v", added, plPlacements(result))
	}
}

func TestDerivePlacementEveryWorkloadKind(t *testing.T) {
	t.Parallel()

	kinds := []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod"}
	nodes := make([]observation.ObservedNode, len(kinds))

	for i, kind := range kinds {
		nodes[i] = plResource("n"+kind, "k8s/"+kind+".yaml", "apps/"+kind+"/shop/w", "production")
	}

	result := plResult(nodes...)
	if added := observation.DerivePlacement(result, nil); added != len(kinds) {
		t.Fatalf("added = %d", added)
	}

	// One synthesized entity serves all workloads of the namespace.
	if len(result.Entities) != 1 || result.Entities[0].ObservedID != observation.NamespaceSynthID("production", "shop") {
		t.Fatalf("entities = %+v", result.Entities)
	}
}

func TestDerivePlacementSharedBaseFileGivesOneFactForEachEnvironment(t *testing.T) {
	t.Parallel()

	first := plResource("cbm:p:1", "k8s/base/api.yaml", "apps/Deployment/payments/api", "production")
	first.StartLine = 2
	second := plResource("cbm:p:1@staging", "k8s/base/api.yaml", "apps/Deployment/payments/api", "staging")
	second.StartLine = 2
	result := plResult(first, second)

	if added := observation.DerivePlacement(result, plClusterOf); added != 2 {
		t.Fatalf("added = %d", added)
	}

	byEnv := map[string]*observation.EvidenceEdge{}
	for _, f := range plPlacements(result) {
		byEnv[f.Environment] = f
	}

	prod, staging := byEnv["production"], byEnv["staging"]
	if prod == nil || staging == nil {
		t.Fatalf("facts = %+v", byEnv)
	}

	if got := [2]string{prod.From, staging.From}; got != [2]string{"cbm:p:1", "cbm:p:1@staging"} {
		t.Fatalf("facts = %+v", byEnv)
	}

	if staging.ID != prod.ID+"@staging" {
		t.Errorf("ids = %q, %q", prod.ID, staging.ID)
	}

	wantTargets := [2]string{observation.NamespaceSynthID("production", "payments"), observation.NamespaceSynthID("staging", "payments")}
	if got := [2]string{prod.To, staging.To}; got != wantTargets {
		t.Errorf("targets = %q, %q", prod.To, staging.To)
	}

	if len(result.Entities) != 2 {
		t.Fatalf("entities = %+v", result.Entities)
	}
}

func TestDerivePlacementCompetingDeclarationsGiveTwoFacts(t *testing.T) {
	t.Parallel()

	result := plResult(
		plResource("cbm:p:1", "k8s/prod/reports.yaml", "apps/Deployment/payments/reports", "production"),
		plResource("cbm:p:2", "k8s/overrides/reports.yaml", "apps/Deployment/payments/reports", "production"),
	)

	if added := observation.DerivePlacement(result, nil); added != 2 {
		t.Fatalf("added = %d", added)
	}

	facts := plPlacements(result)
	if facts[0].ID == facts[1].ID || facts[0].From == facts[1].From {
		t.Errorf("facts = %+v, %+v", facts[0], facts[1])
	}
}

func TestDerivePlacementNamespaceOfAnotherEnvironmentIsNotTheTarget(t *testing.T) {
	t.Parallel()

	result := plResult(
		plResource("cbm:p:1", "k8s/prod/deployment.yaml", "apps/Deployment/payments/api", "production"),
		plResource("cbm:p:2", "k8s/staging/namespace.yaml", "core/Namespace/_/payments", "staging"),
	)

	observation.DerivePlacement(result, plClusterOf)

	facts := plPlacements(result)
	if len(facts) != 1 || facts[0].To != observation.NamespaceSynthID("production", "payments") {
		t.Fatalf("facts = %+v", facts)
	}

	if len(result.Entities) != 1 || result.Entities[0].Environment != "production" {
		t.Fatalf("entities = %+v", result.Entities)
	}
}

func TestDerivePlacementSynthesizedEntity(t *testing.T) {
	t.Parallel()

	deployment := plResource("cbm:p:1", "k8s/prod/deployment.yaml", "apps/Deployment/shop/orders", "production")
	deployment.StartLine = 4
	result := plResult(deployment)
	observation.DerivePlacement(result, plClusterOf)

	if len(result.Entities) != 1 {
		t.Fatalf("entities = %+v", result.Entities)
	}

	entity := result.Entities[0]

	got := [3]any{entity.ObservedID, entity.Label, entity.Resolution}
	if want := [3]any{"synth:namespace:production/shop", observation.LabelNamespace, observation.ResolutionUnbound}; got != want {
		t.Errorf("entity = %+v", entity)
	}

	if entity.Properties["obj_key"] != "core/Namespace/_/shop" || entity.Environment != "production" {
		t.Errorf("entity = %+v", entity)
	}

	scoped, err := observation.ParseScopedID(entity.ScopedID)
	if gotScoped := [3]string{scoped.Cluster, scoped.Kind, scoped.Name}; err != nil || gotScoped != [3]string{"prod-eu", "Namespace", "shop"} {
		t.Errorf("scoped id = %q (%+v, %v)", entity.ScopedID, scoped, err)
	}
}

func TestDerivePlacementEntityIsAddressedByAnInfraResourceBinding(t *testing.T) {
	t.Parallel()

	production := plResource("cbm:p:1", "k8s/prod/deployment.yaml", "apps/Deployment/shop/orders", "production")
	production.StartLine = 4
	staging := plResource("cbm:p:2", "k8s/staging/deployment.yaml", "apps/Deployment/shop/orders", "staging")
	staging.StartLine = 4
	result := plResult(production, staging)
	observation.DerivePlacement(result, plClusterOf)

	bindings := []binding.Binding{{
		ID: "shop-ns", ArchitectureRef: "prod-cluster", Status: binding.StatusActive,
		Match: []binding.Match{{Kind: binding.MatchInfraResource, Environment: "production", Address: "core/Namespace/_/shop"}},
	}}

	set := binding.Resolve(bindings, result.Entities, nil, binding.WithClusterOf(plClusterOf))

	if got := set.ByObserved[observation.NamespaceSynthID("production", "shop")]; len(got) != 1 {
		t.Errorf("production namespace maps to %v (set %+v)", got, set.ByObserved)
	}

	if got := set.ByObserved[observation.NamespaceSynthID("staging", "shop")]; len(got) != 0 {
		t.Errorf("staging namespace maps to %v", got)
	}
}
