package observation_test

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

var kebabKind = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)

// wantCanonicalKinds is the pinned list of canonical kinds. A change to the
// list is a change of the evidence contract and must change this list too.
var wantCanonicalKinds = []string{
	// Code.
	"contains", "defines", "imports", "uses-type", "inherits", "implements", "calls",
	"references-callable", "ambiguous-usage", "constructs", "reads", "writes",
	"handles-route", "calls-service", "dispatches-async", "emits-event", "listens-event",
	"data-flows", "calls-grpc", "calls-graphql", "calls-trpc",
	// Infrastructure: PRD 6.2.
	"deploys-artifact", "selects-workload", "routes-to", "configured-connection",
	"uses-image", "built-from", "base-image", "copies-from-image", "launches",
	"overrides", "includes-infrastructure", "infrastructure-dependency",
	// Infrastructure: derived by Concordia, no native edge.
	"placed-in",
	// Infrastructure: PRD 6.3 strategies and the helm values strategy.
	"configures-env-access", "configures-env-set", "configures-env-read",
	"configures-key-symbol", "configures-helm-values",
	// Infrastructure: PRD 6.4 facets.
	"references-secret", "references-configuration", "references-storage",
	"references-certificate-secret", "uses-runtime-identity", "route-attaches-to-gateway",
	"binding-refers-to-role", "binding-applies-to-principal", "policy-selects-workload",
	"disruption-policy-target", "scaling-target",
	// Neither code nor infrastructure.
	"supersedes", "mentions", "references-file", "diagram-arrow", "tests", "tests-file",
	"similar-to", "semantically-related", "file-changes-with",
	"unsupported", "ambiguous",
}

func kindStrings(kinds []observation.CanonicalEdgeKind) []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}

	return out
}

func TestCanonicalKindList(t *testing.T) {
	got := kindStrings(observation.CanonicalEdgeKinds())

	if !slices.Equal(got, wantCanonicalKinds) {
		t.Fatalf("canonical kinds = %v\nwant %v", got, wantCanonicalKinds)
	}

	requireCanonicalKindsWellFormed(t, got)

	for _, native := range []string{"CALLS", "IMPORTS", "", "Deploys-Artifact", "deploys_artifact"} {
		if observation.IsCanonicalEdgeKind(native) {
			t.Errorf("IsCanonicalEdgeKind(%q) = true", native)
		}
	}
}

// requireCanonicalKindsWellFormed reports each kind that is listed twice, is not kebab-case, or is not canonical.
func requireCanonicalKindsWellFormed(t *testing.T, kinds []string) {
	t.Helper()

	seen := map[string]bool{}

	for _, k := range kinds {
		if seen[k] {
			t.Errorf("kind %s is listed twice", k)
		}

		seen[k] = true

		if !kebabKind.MatchString(k) {
			t.Errorf("kind %s is not kebab-case", k)
		}

		if !observation.IsCanonicalEdgeKind(k) {
			t.Errorf("IsCanonicalEdgeKind(%s) = false", k)
		}
	}
}

func TestCanonicalEdgeKindsGivesACopy(t *testing.T) {
	kinds := observation.CanonicalEdgeKinds()
	kinds[0] = "changed"

	if observation.CanonicalEdgeKinds()[0] != observation.KindContains {
		t.Fatal("CanonicalEdgeKinds shares its backing array")
	}

	code := observation.CodeKinds()
	code[0] = "changed"

	infra := observation.InfrastructureKinds()
	infra[0] = "changed"

	if observation.CodeKinds()[0] != observation.KindContains || observation.InfrastructureKinds()[0] != observation.KindDeploysArtifact {
		t.Fatal("CodeKinds or InfrastructureKinds shares its backing array")
	}
}

// kindClassification says how the package classifies one canonical kind.
type kindClassification struct {
	code, infrastructure, canonical bool
}

func classificationOfKind(k observation.CanonicalEdgeKind) kindClassification {
	return kindClassification{
		code:           observation.IsCodeKind(k),
		infrastructure: observation.IsInfrastructureKind(k),
		canonical:      observation.IsCanonicalEdgeKind(string(k)),
	}
}

func TestCodeAndInfrastructureKinds(t *testing.T) {
	code := observation.CodeKinds()
	infra := observation.InfrastructureKinds()

	if len(code) != 21 || len(infra) != 29 {
		t.Fatalf("code kinds = %d, infrastructure kinds = %d, want 21 and 29", len(code), len(infra))
	}

	for _, k := range code {
		if classificationOfKind(k) != (kindClassification{code: true, canonical: true}) {
			t.Errorf("code kind %s is classified wrongly", k)
		}
	}

	for _, k := range infra {
		if classificationOfKind(k) != (kindClassification{infrastructure: true, canonical: true}) {
			t.Errorf("infrastructure kind %s is classified wrongly", k)
		}
	}

	requireNamedCodeKinds(t)
	requireNeitherCodeNorInfrastructureKinds(t)

	if observation.IsCodeKind("") || observation.IsInfrastructureKind("not-a-kind") {
		t.Error("an unknown kind is classified")
	}
}

func requireNamedCodeKinds(t *testing.T) {
	t.Helper()

	wantCode := []observation.CanonicalEdgeKind{
		observation.KindImports, observation.KindAmbiguousUsage, observation.KindCalls,
		observation.KindCallsService, observation.KindCallsGRPC, observation.KindCallsGraphQL,
		observation.KindCallsTRPC, observation.KindContains,
	}

	for _, k := range wantCode {
		if !observation.IsCodeKind(k) {
			t.Errorf("%s must be a code kind", k)
		}
	}
}

func requireNeitherCodeNorInfrastructureKinds(t *testing.T) {
	t.Helper()

	neither := []observation.CanonicalEdgeKind{
		observation.KindSupersedes, observation.KindMentions, observation.KindReferencesFile,
		observation.KindDiagramArrow, observation.KindTests, observation.KindTestsFile,
		observation.KindSimilarTo, observation.KindSemanticallyRelated, observation.KindFileChangesWith,
		observation.KindUnsupported, observation.KindAmbiguous,
	}

	for _, k := range neither {
		if observation.IsCodeKind(k) || observation.IsInfrastructureKind(k) {
			t.Errorf("%s must be neither a code nor an infrastructure kind", k)
		}
	}
}

func TestEvidenceRoles(t *testing.T) {
	want := []string{
		"declared-intent", "parsed-source-fact", "inferred-association", "runtime-observation",
		"measured-result", "reviewed-judgment", "candidate-verification-artifact",
	}

	got := make([]string, 0, len(want))
	for _, r := range observation.EvidenceRoles() {
		got = append(got, string(r))

		if !observation.IsEvidenceRole(string(r)) {
			t.Errorf("IsEvidenceRole(%s) = false", r)
		}
	}

	if !slices.Equal(got, want) {
		t.Fatalf("roles = %v, want %v", got, want)
	}

	if observation.IsEvidenceRole("resolved") || observation.IsEvidenceRole("") {
		t.Error("a confidence or an empty value is a role")
	}
}

func TestResolution(t *testing.T) {
	requireResolutionList(t)
	requireResolutionRankOrder(t)
	requireResolutionAtLeast(t)

	if observation.Resolution("nonsense").Rank() != 0 || observation.ResolutionUnbound.Rank() != 0 {
		t.Error("unbound and unknown values must have rank 0")
	}

	if observation.IsResolution("syntactic") {
		t.Error("a confidence is a resolution")
	}
}

func requireResolutionList(t *testing.T) {
	t.Helper()

	want := []string{"exact", "explicitly-bound", "inferred", "ambiguous", "unresolved", "unbound"}

	got := make([]string, 0, len(want))
	for _, r := range observation.Resolutions() {
		got = append(got, string(r))

		if !observation.IsResolution(string(r)) {
			t.Errorf("IsResolution(%s) = false", r)
		}
	}

	if !slices.Equal(got, want) {
		t.Fatalf("resolutions = %v, want %v", got, want)
	}
}

func requireResolutionRankOrder(t *testing.T) {
	t.Helper()

	order := []observation.Resolution{
		observation.ResolutionExact, observation.ResolutionExplicitlyBound, observation.ResolutionInferred,
		observation.ResolutionAmbiguous, observation.ResolutionUnresolved,
	}

	for i := 0; i+1 < len(order); i++ {
		if order[i].Rank() <= order[i+1].Rank() {
			t.Errorf("Rank(%s) = %d is not above Rank(%s) = %d", order[i], order[i].Rank(), order[i+1], order[i+1].Rank())
		}
	}
}

func requireResolutionAtLeast(t *testing.T) {
	t.Helper()

	cases := []struct {
		value, minimum observation.Resolution
		want           bool
	}{
		{observation.ResolutionExact, observation.ResolutionExact, true},
		{observation.ResolutionExplicitlyBound, observation.ResolutionInferred, true},
		{observation.ResolutionInferred, observation.ResolutionExplicitlyBound, false},
		{observation.ResolutionAmbiguous, observation.ResolutionInferred, false},
	}

	for _, c := range cases {
		if c.value.AtLeast(c.minimum) != c.want {
			t.Error("AtLeast is wrong")
		}
	}
}

func TestIntegrity(t *testing.T) {
	got := observation.Integrities()
	if !slices.Equal(got, []observation.Integrity{observation.IntegrityVerified, observation.IntegrityUnverified}) {
		t.Fatalf("integrities = %v", got)
	}

	if !observation.IsIntegrity("verified") || observation.IsIntegrity("exact") {
		t.Error("IsIntegrity is wrong")
	}
}

// optionalEvidenceKeys are the JSON keys of the dimensions that an unset edge omits.
var optionalEvidenceKeys = []string{
	"role", "resolution", "completeness", "integrity", "normalizationVersion", "occurrence",
	"possiblyLossy", "sourceLabel", "targetLabel", "environment", "facets", "derivation",
}

// marshalJSONOrFail encodes v and fails the test on an error.
func marshalJSONOrFail(t *testing.T, v any) []byte {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// unmarshalEdgeOrFail decodes an evidence edge and fails the test on an error.
func unmarshalEdgeOrFail(t *testing.T, data []byte) observation.EvidenceEdge {
	t.Helper()

	var back observation.EvidenceEdge
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}

	return back
}

func fullEdge() *observation.EvidenceEdge {
	return &observation.EvidenceEdge{
		ID:         "e1",
		From:       "cbm:p:1",
		To:         "cbm:p:2",
		Kind:       observation.KindReferencesSecret,
		NativeKind: "USAGE",
		Confidence: observation.ConfidenceSyntactic,
		Properties: map[string]string{"ref": "envFrom", "container": "app"},
		SourceSpan: &observation.SourceSpan{Path: "k8s/prod/deployment.yaml", Line: 12},
		Analyzer: observation.AnalyzerDescriptor{
			Name: "codebase-memory-mcp", Version: "0.12.1", AdapterSchema: observation.AdapterSchema,
		},
		GraphGeneration:      "cbm:p:0123456789abcdef",
		ResolutionMethod:     "native",
		Role:                 observation.RoleParsedSourceFact,
		Resolution:           observation.ResolutionExact,
		Completeness:         "references-secret",
		Integrity:            observation.IntegrityVerified,
		NormalizationVersion: "2.0",
		Occurrence:           "app",
		PossiblyLossy:        true,
		SourceLabel:          "Resource",
		TargetLabel:          "Resource",
		Environment:          "production",
		Facets:               map[string]string{"referenceMode": "envFrom", "targetKind": "Secret"},
		Derivation:           &observation.Derivation{Rule: "placement-from-namespace", Version: 1, Inputs: []string{"cbm:p:1"}},
	}
}

func TestEvidenceEdgeJSONRoundTrip(t *testing.T) {
	edge := fullEdge()

	data := marshalJSONOrFail(t, edge)

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	for _, key := range optionalEvidenceKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("JSON has no key %q: %s", key, data)
		}
	}

	wantDimensions := map[string]string{"role": "parsed-source-fact", "resolution": "exact", "integrity": "verified", "confidence": "syntactic"}
	for key, want := range wantDimensions {
		if raw[key] != want {
			t.Errorf("dimension values are wrong: %s", data)
		}
	}

	back := unmarshalEdgeOrFail(t, data)
	if !reflect.DeepEqual(&back, edge) {
		t.Fatalf("round trip changed the edge:\n got %+v\nwant %+v", back, *edge)
	}
}

func TestEvidenceEdgeJSONOmitsUnsetDimensions(t *testing.T) {
	edge := &observation.EvidenceEdge{
		ID: "e1", From: "cbm:p:1", To: "cbm:p:2", Kind: observation.KindCalls, NativeKind: "CALLS",
		Confidence: observation.ConfidenceResolved, Properties: map[string]string{},
	}

	data := marshalJSONOrFail(t, edge)

	for _, key := range optionalEvidenceKeys {
		if strings.Contains(string(data), `"`+key+`"`) {
			t.Errorf("an unset %s is in the JSON: %s", key, data)
		}
	}

	for _, key := range []string{"id", "from", "to", "kind", "nativeKind", "confidence", "properties", "resolutionMethod", "graphGeneration", "analyzer"} {
		if !strings.Contains(string(data), `"`+key+`"`) {
			t.Errorf("the JSON has no %s: %s", key, data)
		}
	}
}

func TestConfidenceIsIndependentOfTheQualityDimensions(t *testing.T) {
	for _, confidence := range []observation.Confidence{
		observation.ConfidenceResolved, observation.ConfidenceHeuristic, observation.ConfidenceUnknown,
	} {
		requireConfidenceSurvivesRoundTrips(t, confidence)
	}

	ranks := map[observation.Confidence]int{
		observation.ConfidenceResolved: 4, observation.ConfidenceHeuristic: 1, observation.ConfidenceUnknown: 0,
	}
	for confidence, rank := range ranks {
		if confidence.Rank() != rank {
			t.Error("confidence ranks changed")
		}
	}
}

// requireConfidenceSurvivesRoundTrips fails when a role or a resolution changes the confidence of an edge that goes through JSON.
func requireConfidenceSurvivesRoundTrips(t *testing.T, confidence observation.Confidence) {
	t.Helper()

	rank := confidence.Rank()

	for _, role := range observation.EvidenceRoles() {
		for _, res := range observation.Resolutions() {
			data := marshalJSONOrFail(t, &observation.EvidenceEdge{Confidence: confidence, Role: role, Resolution: res})
			back := unmarshalEdgeOrFail(t, data)

			if back.Confidence != confidence || back.Confidence.Rank() != rank {
				t.Fatalf("role %s and resolution %s changed the confidence %s to %s", role, res, confidence, back.Confidence)
			}
		}
	}
}

func TestObservedNodeEnvironmentJSON(t *testing.T) {
	data := marshalJSONOrFail(t, observation.ObservedNode{ObservedID: "cbm:p:1", Label: "Resource", QualifiedName: "a", FilePath: "k8s/a.yaml"})

	if strings.Contains(string(data), "environment") {
		t.Errorf("an empty environment is in the JSON: %s", data)
	}

	node := observation.ObservedNode{ObservedID: "cbm:p:1", Label: "Resource", QualifiedName: "a", FilePath: "k8s/a.yaml", Environment: "production"}

	data = marshalJSONOrFail(t, node)

	var back observation.ObservedNode
	if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back, node) {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
}

func TestEvidenceQueryResultUnsupported(t *testing.T) {
	res := observation.EvidenceQueryResult{
		UnmappedNativeEdges: map[string]int{"HAS_BRANCH": 1},
		Unsupported:         []*observation.EvidenceEdge{{Kind: observation.KindUnsupported, NativeKind: "HAS_BRANCH"}},
	}

	if len(res.Unsupported) != 1 || res.UnmappedNativeEdges["HAS_BRANCH"] != 1 {
		t.Fatalf("result = %+v", res)
	}

	if res.Unsupported[0].Kind != observation.KindUnsupported {
		t.Fatalf("result = %+v", res)
	}
}

func TestPlacedInIsAnInfrastructureKindWithAFamily(t *testing.T) {
	if !observation.IsInfrastructureKind(observation.KindPlacedIn) || observation.IsCodeKind(observation.KindPlacedIn) {
		t.Fatal("placed-in must be an infrastructure kind and no code kind")
	}

	if observation.KindPlacedIn != "placed-in" || !observation.IsCanonicalEdgeKind("placed-in") {
		t.Fatalf("kind = %q", observation.KindPlacedIn)
	}

	requirePlacedInFamily(t)

	if got := observation.NativeKindsOfCanonical(observation.KindPlacedIn); len(got) != 0 {
		t.Fatalf("native kinds = %v", got)
	}
}

func requirePlacedInFamily(t *testing.T) {
	t.Helper()

	if name, ok := observation.FamilyForKind(observation.KindPlacedIn); !ok || name != "placed-in" {
		t.Fatalf("family = %q, %v", name, ok)
	}

	if !slices.Contains(observation.FamilyNames(), "placed-in") {
		t.Fatal("FamilyNames has no placed-in")
	}
}

func TestDerivationIsOmittedWhenEmpty(t *testing.T) {
	data := marshalJSONOrFail(t, &observation.EvidenceEdge{ID: "e1", Kind: observation.KindCalls, Properties: map[string]string{}})

	if strings.Contains(string(data), "derivation") {
		t.Fatalf("an unset derivation is in the JSON: %s", data)
	}
}
