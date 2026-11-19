package observation

import (
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/workspace"
)

// AdapterSchema is the name of the adapter schema (the native-to-canonical table below).
const AdapterSchema = workspace.AdapterSchema

// NormalizationVersion is the version of the normalization table (the rule
// table below). It changes whenever a rule changes, and each normalized fact
// records it.
const NormalizationVersion = "2.1"

const (
	// callsConfidenceThreshold is the numeric confidence from which a CALLS edge is syntactic.
	callsConfidenceThreshold = 0.5
	edgeIDHexLength          = 16
	callsKind                = "CALLS"
	nativeUsage              = "USAGE"
	nativeImports            = "IMPORTS"
	nativeDependsOn          = "DEPENDS_ON"
	nativeConfigures         = "CONFIGURES"
	// propKind is the native property `kind` of an `INFRA_MAPS` edge, and the node property `kind`.
	propKind = "kind"
	// facetAmbiguity is the facet of an ambiguous fact that says why it is ambiguous.
	facetAmbiguity = "ambiguity"
)

// LabelClass is the class of a source node: the kind of file or entity that
// the provider found it in. A rule can need a class because several native
// edge types have one shape for code and for infrastructure (CBM 0.12.1
// observations, O11).
type LabelClass string

// Label classes.
const (
	// ClassCode is a node of a source code file (and every node that no other class claims).
	ClassCode LabelClass = "code"
	// ClassInfrastructure is a node of a manifest, Dockerfile, Terraform, shell, or CI file,
	// or a node of an infrastructure label (`Resource`, `Chart`, `Image`, `EnvVar`, `Route`).
	ClassInfrastructure LabelClass = "infrastructure"
	// ClassDocumentation is a documentation section, a decision, or a node of a markup file.
	ClassDocumentation LabelClass = "documentation"
	// ClassDiagram is a diagram or a diagram element.
	ClassDiagram LabelClass = "diagram"
)

// infrastructureLabels are the node labels that only infrastructure files produce.
var infrastructureLabels = []string{"Resource", "Chart", "Image", "EnvVar", "Route"}

// infrastructureFileTypes are the file types (see FileTypeOf) of infrastructure files.
var infrastructureFileTypes = []string{
	".tf", ".tfvars", ".hcl", extYAML, extYML, dockerfileName, "Makefile", ".mk", ".sh", ".bash",
}

// documentationFileTypes are the file types of documentation files.
var documentationFileTypes = []string{".md", ".mdx", ".rst", ".adoc", ".txt"}

// FileTypeOf gives the file type of a "/" path: the extension, or the base
// name for a file without extension (`Makefile`) and for kustomization files.
// A Dockerfile (`Dockerfile`, `Dockerfile.dev`, `api.dockerfile`) has the type
// `Dockerfile`. An empty path has the empty type.
func FileTypeOf(filePath string) string {
	if filePath == "" {
		return ""
	}

	base := filePath
	if slash := strings.LastIndexByte(filePath, '/'); slash != -1 {
		base = filePath[slash+1:]
	}

	lower := strings.ToLower(base)

	switch {
	case isDockerfileName(lower):
		return "Dockerfile"
	case strings.HasPrefix(base, "kustomization."):
		return base
	}

	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		return base[dot:]
	}

	return base
}

func isDockerfileName(lower string) bool {
	return lower == "dockerfile" || strings.HasPrefix(lower, "dockerfile.") || strings.HasSuffix(lower, ".dockerfile")
}

// LabelClassOf gives the class of a node from its label and file path.
func LabelClassOf(label, filePath string) LabelClass {
	switch label {
	case LabelDiagram, LabelDiagramElement:
		return ClassDiagram
	case "ADR", "Section":
		return ClassDocumentation
	}

	if slices.Contains(infrastructureLabels, label) {
		return ClassInfrastructure
	}

	fileType := FileTypeOf(filePath)

	switch {
	case slices.Contains(infrastructureFileTypes, fileType), strings.HasPrefix(fileType, "kustomization."):
		return ClassInfrastructure
	case slices.Contains(documentationFileTypes, fileType):
		return ClassDocumentation
	default:
		return ClassCode
	}
}

// Discriminators say when a rule applies: the native properties, the
// presence of properties, and the source and target of the edge. A
// discriminator that a rule does not name does not matter to it, and the
// properties that a rule does not use stay native properties of the fact.
type Discriminators struct {
	// Properties maps a native property to the values that select the rule.
	// The edge must have the property with one of the values.
	Properties map[string][]string
	// Present are native properties that the edge must have.
	Present []string
	// Absent are native properties that the edge must not have.
	Absent []string
	// SourceClasses are the label classes (see LabelClassOf) that the source node must have.
	SourceClasses []LabelClass
	// SourceFiles are the file types (see FileTypeOf) that the source node must have.
	SourceFiles []string
	// TargetKinds are the values of the target node `kind` property that select the rule.
	TargetKinds []string
}

// RuleResult is the semantic fact that a rule gives.
type RuleResult struct {
	// Facets are fixed facet values of the kind, for example `family=chart`.
	Facets map[string]string
	// FacetProperties copies native properties to facets: the facet name to the property name.
	FacetProperties map[string]string
	Kind            CanonicalEdgeKind
	Role            EvidenceRole
	Resolution      Resolution
	// Confidence is the fixed confidence. It is not used when UsesCallsRule is true.
	Confidence Confidence
	// OccurrenceProperty is the native property that holds the occurrence
	// discriminator of the fact (for example `container`). Empty for none.
	OccurrenceProperty string
	// TargetKindFacet stores the target node `kind` as the facet `targetKind`.
	TargetKindFacet bool
	// UsesCallsRule means: use the CALLS rule (strategy and numeric confidence).
	UsesCallsRule bool
}

// NormalizationRule maps one shape of a native edge to a semantic fact. The
// table normalizationRules holds the rules in order (adapter schema
// `concordia-cbm/v2`). The first matching rule decides, unless matching
// rules disagree or discriminators conflict: then the fact is ambiguous.
type NormalizationRule struct {
	// Name identifies the rule in diagnostics and in tests.
	Name string
	// Native is the native edge type.
	Native         string
	Discriminators Discriminators
	Result         RuleResult
}

func parsedFact(kind CanonicalEdgeKind, confidence Confidence) RuleResult {
	return RuleResult{Kind: kind, Role: RoleParsedSourceFact, Resolution: ResolutionExact, Confidence: confidence}
}

// inferredFact is an association that the provider or Concordia inferred: the confidence is heuristic.
func inferredFact(kind CanonicalEdgeKind) RuleResult {
	return RuleResult{Kind: kind, Role: RoleInferredAssociation, Resolution: ResolutionInferred, Confidence: ConfidenceHeuristic}
}

func candidateFact(kind CanonicalEdgeKind) RuleResult {
	return RuleResult{
		Kind: kind, Role: RoleCandidateVerificationArtifact, Resolution: ResolutionInferred, Confidence: ConfidenceHeuristic,
	}
}

// withFacets gives a copy of r with fixed facets (name, value, ...).
func (r RuleResult) withFacets(pairs ...string) RuleResult { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	facets := make(map[string]string, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		facets[pairs[i]] = pairs[i+1]
	}

	r.Facets = facets

	return r
}

// withFacetProperty gives a copy of r that copies native properties to facets (facet, property, ...).
func (r RuleResult) withFacetProperty(pairs ...string) RuleResult { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	props := make(map[string]string, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		props[pairs[i]] = pairs[i+1]
	}

	r.FacetProperties = props

	return r
}

// withOccurrence gives a copy of r whose occurrence discriminator is a native property.
func (r RuleResult) withOccurrence(property string) RuleResult { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	r.OccurrenceProperty = property

	return r
}

// withTargetKind gives a copy of r that stores the target node kind as a facet.
func (r RuleResult) withTargetKind() RuleResult { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	r.TargetKindFacet = true

	return r
}

// on makes the discriminators for one native property and its values.
func on(property string, values ...string) Discriminators {
	return Discriminators{Properties: map[string][]string{property: values}}
}

// onBoth makes the discriminators for two native properties.
func onBoth(first string, firstValues []string, second string, secondValues []string) Discriminators {
	return Discriminators{Properties: map[string][]string{first: firstValues, second: secondValues}}
}

func values(v ...string) []string { return v }

func (d Discriminators) from(classes ...LabelClass) Discriminators { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	d.SourceClasses = classes

	return d
}

func (d Discriminators) files(types ...string) Discriminators { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	d.SourceFiles = types

	return d
}

func (d Discriminators) targets(kinds ...string) Discriminators { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	d.TargetKinds = kinds

	return d
}

func (d Discriminators) absent(names ...string) Discriminators { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	d.Absent = names

	return d
}

func (d Discriminators) present(names ...string) Discriminators { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	d.Present = names

	return d
}

// kubernetesReference is a rule of a USAGE edge with a `ref` property.
func kubernetesReference(name, ref string, result RuleResult) NormalizationRule { //nolint:gocritic // a builder of the rule table: it copies a small literal.
	return NormalizationRule{
		Name: name, Native: nativeUsage,
		Discriminators: on("ref", ref),
		Result:         result.withFacetProperty("referenceMode", "ref"),
	}
}

// normalizationRules is the normalization table, in order. The rows of the
// kinds of CBM 0.12.1 come from the observations (O2 to O12) and the golden of
// the infra recording (test/golden/infra/native-discriminators.json).
var normalizationRules = []NormalizationRule{
	// Code evidence.
	{
		Name: "imports-code", Native: nativeImports,
		Discriminators: Discriminators{}.absent("via").from(ClassCode),
		Result:         parsedFact(KindImports, ConfidenceSyntactic),
	},
	{
		Name: "imports-terraform-module", Native: nativeImports,
		Discriminators: on("via", "terraform").from(ClassInfrastructure),
		Result:         parsedFact(KindIncludesInfrastructure, ConfidenceSyntactic),
	},
	{
		Name: "imports-kustomization", Native: nativeImports,
		Discriminators: Discriminators{}.absent("via").files("kustomization.yaml", "kustomization.yml"),
		Result:         parsedFact(KindIncludesInfrastructure, ConfidenceSyntactic),
	},
	{Name: "calls", Native: callsKind, Result: RuleResult{
		Kind: KindCalls, Role: RoleParsedSourceFact, Resolution: ResolutionExact, UsesCallsRule: true,
	}},
	{Name: "call-reference", Native: "CALL_REFERENCE", Result: parsedFact(KindReferencesCallable, ConfidenceResolved)},
	{
		Name: "usage-code", Native: nativeUsage,
		Discriminators: Discriminators{}.absent("ref", "via").from(ClassCode),
		Result:         inferredFact(KindAmbiguousUsage),
	},
	{Name: "inherits", Native: "INHERITS", Result: parsedFact(KindInherits, ConfidenceResolved)},
	{Name: "implements", Native: "IMPLEMENTS", Result: parsedFact(KindImplements, ConfidenceResolved)},
	{Name: "defines", Native: "DEFINES", Result: parsedFact(KindDefines, ConfidenceResolved)},
	{Name: "defines-method", Native: "DEFINES_METHOD", Result: parsedFact(KindDefines, ConfidenceResolved)},
	{Name: "contains-file", Native: "CONTAINS_FILE", Result: parsedFact(KindContains, ConfidenceResolved)},
	{Name: "contains-folder", Native: "CONTAINS_FOLDER", Result: parsedFact(KindContains, ConfidenceResolved)},
	{Name: "http-calls", Native: "HTTP_CALLS", Result: inferredFact(KindCallsService)},
	{Name: "async-calls", Native: "ASYNC_CALLS", Result: inferredFact(KindDispatchesAsync)},
	{Name: "emits", Native: "EMITS", Result: inferredFact(KindEmitsEvent)},
	{Name: "listens-on", Native: "LISTENS_ON", Result: inferredFact(KindListensEvent)},
	{Name: "data-flows", Native: "DATA_FLOWS", Result: inferredFact(KindDataFlows)},
	{Name: "reads", Native: "READS", Result: parsedFact(KindReads, ConfidenceSyntactic)},
	{Name: "writes", Native: "WRITES", Result: parsedFact(KindWrites, ConfidenceSyntactic)},
	{Name: "handles", Native: "HANDLES", Result: parsedFact(KindHandlesRoute, ConfidenceHeuristic)},
	// Cross-project edges of the CBM `cross-repo-intelligence` pass.
	{Name: "cross-http-calls", Native: "CROSS_HTTP_CALLS", Result: inferredFact(KindCallsService)},
	{Name: "cross-async-calls", Native: "CROSS_ASYNC_CALLS", Result: inferredFact(KindDispatchesAsync)},
	{Name: "cross-channel", Native: "CROSS_CHANNEL", Result: inferredFact(KindEmitsEvent)},
	// Protocol calls between code entities (§6.5).
	{Name: "grpc-calls", Native: "GRPC_CALLS", Result: inferredFact(KindCallsGRPC)},
	{Name: "graphql-calls", Native: "GRAPHQL_CALLS", Result: inferredFact(KindCallsGraphQL)},
	{Name: "trpc-calls", Native: "TRPC_CALLS", Result: inferredFact(KindCallsTRPC)},
	{Name: "cross-grpc-calls", Native: "CROSS_GRPC_CALLS", Result: inferredFact(KindCallsGRPC)},
	{Name: "cross-graphql-calls", Native: "CROSS_GRAPHQL_CALLS", Result: inferredFact(KindCallsGraphQL)},
	{Name: "cross-trpc-calls", Native: "CROSS_TRPC_CALLS", Result: inferredFact(KindCallsTRPC)},

	// Deployment and build evidence (§6.2).
	{
		Name: "deploys-script-or-chart", Native: "DEPLOYS",
		Discriminators: on("via", "kubectl", "helm", "chart"),
		Result:         parsedFact(KindDeploysArtifact, ConfidenceSyntactic),
	},
	{
		Name: "depends-on-from", Native: nativeDependsOn,
		Discriminators: on("via", "from"),
		Result:         parsedFact(KindBaseImage, ConfidenceSyntactic),
	},
	{
		Name: "depends-on-copy-from", Native: nativeDependsOn,
		Discriminators: on("via", "copy-from"),
		Result:         parsedFact(KindCopiesFromImage, ConfidenceSyntactic),
	},
	{
		Name: "depends-on-build", Native: nativeDependsOn,
		Discriminators: on("via", "build"),
		Result:         parsedFact(KindBuiltFrom, ConfidenceSyntactic),
	},
	{
		Name: "depends-on-compose", Native: nativeDependsOn,
		Discriminators: on("via", "depends_on"),
		Result:         parsedFact(KindInfrastructureDependency, ConfidenceSyntactic).withFacets("family", "compose"),
	},
	{
		Name: "depends-on-chart", Native: nativeDependsOn,
		Discriminators: Discriminators{}.absent("via").present("repository").from(ClassInfrastructure),
		Result:         parsedFact(KindInfrastructureDependency, ConfidenceSyntactic).withFacets("family", "chart"),
	},
	{
		Name: "infra-maps-selector", Native: "INFRA_MAPS",
		Discriminators: on(propKind, "selector"),
		Result:         parsedFact(KindSelectsWorkload, ConfidenceSyntactic),
	},
	{
		Name: "infra-maps-route", Native: "INFRA_MAPS",
		Discriminators: on(propKind, "ingress", "route", "httproute"),
		Result:         parsedFact(KindRoutesTo, ConfidenceSyntactic),
	},
	{
		Name: "connects-to-env", Native: "CONNECTS_TO",
		Discriminators: on("via", "env"),
		Result:         parsedFact(KindConfiguredConnection, ConfidenceSyntactic),
	},
	{
		Name: "spawns", Native: "SPAWNS",
		Discriminators: on("api", "shell", "dockerfile"),
		Result:         parsedFact(KindLaunches, ConfidenceSyntactic),
	},
	{
		Name: "override", Native: "OVERRIDE",
		Discriminators: on("via", "tfvars", "patch"),
		Result:         parsedFact(KindOverrides, ConfidenceSyntactic),
	},

	// References of workloads and Kubernetes objects (§6.4), told apart by `ref` and the target `kind`.
	kubernetesReference("usage-image", "image", parsedFact(KindUsesImage, ConfidenceSyntactic).withOccurrence("container")),
	kubernetesReference("usage-service-account", "serviceAccountName", parsedFact(KindUsesRuntimeIdentity, ConfidenceSyntactic)),
	{
		Name: "usage-env-secret", Native: nativeUsage,
		Discriminators: on("ref", "env", "envFrom", "volume").targets("Secret"),
		Result: parsedFact(KindReferencesSecret, ConfidenceSyntactic).
			withFacetProperty("referenceMode", "ref").withTargetKind(),
	},
	{
		Name: "usage-env-configmap", Native: nativeUsage,
		Discriminators: on("ref", "env", "envFrom", "volume").targets("ConfigMap"),
		Result: parsedFact(KindReferencesConfiguration, ConfidenceSyntactic).
			withFacetProperty("referenceMode", "ref").withTargetKind(),
	},
	{
		Name: "usage-volume-claim", Native: nativeUsage,
		Discriminators: on("ref", "volume").targets("PersistentVolumeClaim"),
		Result: parsedFact(KindReferencesStorage, ConfidenceSyntactic).
			withFacetProperty("referenceMode", "ref").withTargetKind(),
	},
	kubernetesReference("usage-tls", "tls", parsedFact(KindReferencesCertificateSecret, ConfidenceSyntactic)),
	kubernetesReference("usage-parent-ref", "parentRef", parsedFact(KindRouteAttachesToGateway, ConfidenceSyntactic)),
	kubernetesReference("usage-role-ref", "roleRef", parsedFact(KindBindingRefersToRole, ConfidenceSyntactic)),
	kubernetesReference("usage-subject", "subject", parsedFact(KindBindingAppliesToPrincipal, ConfidenceSyntactic)),
	kubernetesReference("usage-pod-selector", "podSelector", parsedFact(KindPolicySelectsWorkload, ConfidenceSyntactic)),
	kubernetesReference("usage-selector", "selector", parsedFact(KindDisruptionPolicyTarget, ConfidenceSyntactic)),
	kubernetesReference("usage-scale-target", "scaleTarget", parsedFact(KindScalingTarget, ConfidenceSyntactic)),
	{
		Name: "usage-terraform", Native: nativeUsage,
		Discriminators: onBoth("via", values("terraform"), "ref", values("var", "module", "output", "resource")),
		Result: parsedFact(KindInfrastructureDependency, ConfidenceSyntactic).
			withFacets("family", "terraform").withFacetProperty("terraformRef", "ref"),
	},

	// Configuration evidence (§6.3). The provider stores no values.
	{
		Name: "configures-env-access", Native: nativeConfigures,
		Discriminators: on("strategy", "env_access"),
		Result:         parsedFact(KindConfiguresEnvAccess, ConfidenceSyntactic),
	},
	{
		Name: "configures-env-set", Native: nativeConfigures,
		Discriminators: on("strategy", "env_set"),
		Result:         parsedFact(KindConfiguresEnvSet, ConfidenceSyntactic).withOccurrence("container"),
	},
	{
		Name: "configures-env-read", Native: nativeConfigures,
		Discriminators: on("strategy", "env_read"),
		Result:         parsedFact(KindConfiguresEnvRead, ConfidenceSyntactic),
	},
	{
		Name: "configures-key-symbol", Native: nativeConfigures,
		Discriminators: on("strategy", "key_symbol"),
		Result:         inferredFact(KindConfiguresKeySymbol),
	},
	{
		Name: "configures-helm-values", Native: nativeConfigures,
		Discriminators: on("strategy", "helm_values"),
		Result:         parsedFact(KindConfiguresHelmValues, ConfidenceSyntactic),
	},

	// Documentation, diagram, test, and advisory evidence (§6.5).
	{
		Name: "supersedes", Native: "SUPERSEDES",
		Discriminators: onBoth("via", values("markdown"), "syntax", values("supersedes")),
		Result:         parsedFact(KindSupersedes, ConfidenceSyntactic),
	},
	{
		Name: "mentions", Native: "MENTIONS",
		Discriminators: on("syntax", "link", "code_path", "path"),
		Result:         inferredFact(KindMentions),
	},
	{
		Name: "references-file", Native: "REFERENCES_FILE",
		Discriminators: on("strategy", "md_inline_link", "md_backtick_path", "md_bare_mention"),
		Result:         inferredFact(KindReferencesFile),
	},
	{Name: "diagram-arrow", Native: "DIAGRAM_ARROW", Result: parsedFact(KindDiagramArrow, ConfidenceSyntactic)},
	{Name: "tests", Native: "TESTS", Result: candidateFact(KindTests)},
	{Name: "tests-file", Native: "TESTS_FILE", Result: candidateFact(KindTestsFile)},
	{Name: "similar-to", Native: "SIMILAR_TO", Result: inferredFact(KindSimilarTo)},
	{Name: "semantically-related", Native: "SEMANTICALLY_RELATED", Result: inferredFact(KindSemanticallyRelated)},
	{Name: "file-changes-with", Native: "FILE_CHANGES_WITH", Result: inferredFact(KindFileChangesWith)},
}

// discriminatorProperties are the native properties that tell the meaning of
// an edge. Two of them that point to different facts make the fact ambiguous.
var discriminatorProperties = []string{"via", "ref", propKind, "strategy", "syntax", "api"}

// rulesByNative groups the rules by native edge type, in table order.
var rulesByNative = sync.OnceValue(func() map[string][]*NormalizationRule {
	out := map[string][]*NormalizationRule{}

	for i := range normalizationRules {
		rule := &normalizationRules[i]
		out[rule.Native] = append(out[rule.Native], rule)
	}

	return out
})

// NormalizationRules gives the rules of the normalization table, in order.
func NormalizationRules() []NormalizationRule {
	return slices.Clone(normalizationRules)
}

// IsMappedNative says whether the table has at least one rule for a native edge type.
func IsMappedNative(native string) bool {
	return len(rulesByNative()[native]) > 0
}

// supportedCanonicalKinds is the set of canonical kinds that this adapter can
// supply: the kinds of the rules and the derived kind `placed-in` (it has no native edge). CBM has no native kind for `uses-type` or `constructs`.
var supportedCanonicalKinds = sync.OnceValue(func() []CanonicalEdgeKind {
	set := map[CanonicalEdgeKind]struct{}{KindPlacedIn: {}}
	for i := range normalizationRules {
		set[normalizationRules[i].Result.Kind] = struct{}{}
	}

	out := make([]CanonicalEdgeKind, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
})

// SupportedCanonicalKinds gives the canonical kinds that this adapter can supply, sorted.
func SupportedCanonicalKinds() []CanonicalEdgeKind {
	return slices.Clone(supportedCanonicalKinds())
}

// NativeKindsOfCanonical gives the native CBM edge types that make a
// canonical kind (the reverse of the rule table), sorted.
func NativeKindsOfCanonical(kind CanonicalEdgeKind) []string {
	out := []string{}

	for i := range normalizationRules {
		rule := &normalizationRules[i]
		if rule.Result.Kind == kind && !slices.Contains(out, rule.Native) {
			out = append(out, rule.Native)
		}
	}

	slices.Sort(out)

	return out
}

// CallsConfidence is the confidence of a CALLS edge. A strategy that starts
// with `lsp_` is resolved; a numeric confidence of 0.5 or more is syntactic;
// all other edges are heuristic.
func CallsConfidence(strategy, numeric string) Confidence {
	if strings.HasPrefix(strategy, "lsp_") {
		return ConfidenceResolved
	}

	if isSyntacticConfidence(numeric) {
		return ConfidenceSyntactic
	}

	return ConfidenceHeuristic
}

// isSyntacticConfidence says whether a numeric confidence is a finite number of 0.5 or more.
func isSyntacticConfidence(numeric string) bool {
	n, err := strconv.ParseFloat(strings.TrimSpace(numeric), 64)
	if err != nil {
		return false
	}

	return isFinite(n) && n >= callsConfidenceThreshold
}

func isFinite(n float64) bool { return !math.IsInf(n, 0) && !math.IsNaN(n) }

func edgeConfidence(r *RuleResult, props map[string]string) Confidence {
	if r.UsesCallsRule {
		return CallsConfidence(props["strategy"], props["confidence"])
	}

	return r.Confidence
}

func resolutionMethodOf(nativeKind string, props map[string]string) string {
	if nativeKind == callsKind {
		if s := props["strategy"]; s != "" {
			return "cbm:CALLS:" + s
		}
	}

	return "cbm:" + nativeKind
}

// NormalizeInput is the input of NormalizeEdge.
type NormalizeInput struct {
	// Properties are the edge property columns as strings. Empty values are removed.
	Properties      map[string]string
	NativeKind      string
	GraphGeneration string
	Analyzer        AnalyzerDescriptor
	From            ObservedNode
	To              ObservedNode
}

// NormalizeEdge converts one native edge to normalized evidence with the rule
// table. The bool is false for a native type that has no rule. A native edge
// of a mapped type that no rule decides gets the kind `ambiguous`, keeps the
// native kind and properties, and has the facet `ambiguity` with the reason.
func NormalizeEdge(input *NormalizeInput) (*EvidenceEdge, bool) {
	rules := rulesByNative()[input.NativeKind]
	if len(rules) == 0 {
		return nil, false
	}

	props := nonEmptyProperties(input.Properties)

	match := &matchInput{
		props:       props,
		sourceClass: LabelClassOf(input.From.Label, input.From.FilePath),
		sourceFile:  FileTypeOf(input.From.FilePath),
		targetKind:  input.To.Kind,
	}

	edge := newEdge(input, props)
	outcome := decide(rules, match)

	if outcome.rule == nil {
		edge.Kind = KindAmbiguous
		edge.Role = RoleParsedSourceFact
		edge.Resolution = ResolutionAmbiguous
		edge.Confidence = ConfidenceUnknown
		edge.Facets = map[string]string{facetAmbiguity: outcome.ambiguity}

		return edge, true
	}

	result := &outcome.rule.Result

	edge.Kind = result.Kind
	edge.Role = result.Role
	edge.Resolution = result.Resolution
	edge.Confidence = edgeConfidence(result, props)
	edge.Facets = facetsOf(result, props, input.To.Kind)

	if result.OccurrenceProperty != "" {
		edge.Occurrence = props[result.OccurrenceProperty]
	}

	return edge, true
}

// NormalizeUnsupported makes the fact of a native edge of a type that has no
// rule: kind `unsupported`, with the native kind, endpoints, location and
// properties. It is never complete coverage and has no resolution.
func NormalizeUnsupported(input *NormalizeInput) *EvidenceEdge {
	props := nonEmptyProperties(input.Properties)
	edge := newEdge(input, props)

	edge.Kind = KindUnsupported
	edge.Role = RoleParsedSourceFact
	edge.Resolution = ResolutionUnresolved
	edge.Confidence = ConfidenceUnknown

	return edge
}

func nonEmptyProperties(raw map[string]string) map[string]string {
	props := make(map[string]string, len(raw))

	for k, v := range raw {
		if v != "" {
			props[k] = v
		}
	}

	return props
}

// newEdge makes the parts of a fact that do not depend on the rule.
func newEdge(input *NormalizeInput, props map[string]string) *EvidenceEdge {
	line, _ := parseIntPrefix(props["line"])
	if line == 0 {
		line = input.From.StartLine
	}

	edge := &EvidenceEdge{
		ID:                   edgeID(input.NativeKind, input.From.ObservedID, input.To.ObservedID, props),
		From:                 input.From.ObservedID,
		To:                   input.To.ObservedID,
		NativeKind:           input.NativeKind,
		ResolutionMethod:     resolutionMethodOf(input.NativeKind, props),
		Analyzer:             input.Analyzer,
		GraphGeneration:      input.GraphGeneration,
		Properties:           props,
		SourceLabel:          input.From.Label,
		TargetLabel:          input.To.Label,
		NormalizationVersion: NormalizationVersion,
	}

	if input.From.FilePath != "" {
		edge.SourceSpan = &SourceSpan{Path: input.From.FilePath, Line: line}
	}

	return edge
}

// facetsOf gives the facets of a fact: the fixed facets of the rule, the
// native properties that it copies, and the target node kind.
func facetsOf(result *RuleResult, props map[string]string, targetKind string) map[string]string {
	if result.hasNoFacets() {
		return nil
	}

	facets := make(map[string]string, len(result.Facets)+len(result.FacetProperties)+1)
	maps.Copy(facets, result.Facets)

	for facet, property := range result.FacetProperties {
		if v := props[property]; v != "" {
			facets[facet] = v
		}
	}

	if result.TargetKindFacet && targetKind != "" {
		facets["targetKind"] = targetKind
	}

	return facets
}

func (r *RuleResult) hasNoFacets() bool {
	return len(r.Facets) == 0 && len(r.FacetProperties) == 0 && !r.TargetKindFacet
}

func edgeID(nativeKind, from, to string, props map[string]string) string {
	pairs := make([]string, 0, len(props))
	for _, k := range sortedKeys(props) {
		pairs = append(pairs, k+"="+props[k])
	}

	body := strings.Join(pairs, "\x1f")
	sum := digest.SHA256HexString(strings.Join([]string{nativeKind, from, to, body}, "\x00"))

	return "cbm-edge:" + sum[:edgeIDHexLength]
}
