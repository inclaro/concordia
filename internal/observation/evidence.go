// Package observation holds the normalized evidence model, the port to
// codebase-memory-mcp (CBM) and the observation provider on top of it.
package observation

import (
	"context"
	"slices"

	"github.com/inclaro/concordia/internal/diag"
)

// Confidence is the strength of one piece of evidence.
type Confidence string

// Confidence values. A higher Rank is stronger evidence.
const (
	ConfidenceResolved  Confidence = "resolved"
	ConfidenceSyntactic Confidence = "syntactic"
	ConfidenceHeuristic Confidence = "heuristic"
	ConfidenceRuntime   Confidence = "runtime"
	ConfidenceUnknown   Confidence = "unknown"
)

// Rank gives the rank of a confidence: unknown 0, heuristic 1, syntactic 2,
// runtime 3, resolved 4. An unknown value has rank 0.
func (c Confidence) Rank() int {
	switch c {
	case ConfidenceResolved:
		return rankResolved
	case ConfidenceRuntime:
		return rankRuntime
	case ConfidenceSyntactic:
		return rankSyntactic
	case ConfidenceHeuristic:
		return rankHeuristic
	case ConfidenceUnknown:
		return 0
	default:
		return 0
	}
}

const (
	rankHeuristic = 1
	rankSyntactic = 2
	rankRuntime   = 3
	rankResolved  = 4
)

// CanonicalEdgeKind is the kind of an edge in the normalized evidence model.
type CanonicalEdgeKind string

// Canonical edge kinds. The code kinds come first (see CodeKinds), then the
// infrastructure kinds (see InfrastructureKinds), then documentation,
// diagram, test, advisory, and the two kinds that keep native evidence
// without an interpretation.
const (
	KindContains           CanonicalEdgeKind = "contains"
	KindDefines            CanonicalEdgeKind = "defines"
	KindImports            CanonicalEdgeKind = "imports"
	KindUsesType           CanonicalEdgeKind = "uses-type"
	KindInherits           CanonicalEdgeKind = "inherits"
	KindImplements         CanonicalEdgeKind = "implements"
	KindCalls              CanonicalEdgeKind = "calls"
	KindReferencesCallable CanonicalEdgeKind = "references-callable"
	KindAmbiguousUsage     CanonicalEdgeKind = "ambiguous-usage"
	KindConstructs         CanonicalEdgeKind = "constructs"
	KindReads              CanonicalEdgeKind = "reads"
	KindWrites             CanonicalEdgeKind = "writes"
	KindHandlesRoute       CanonicalEdgeKind = "handles-route"
	KindCallsService       CanonicalEdgeKind = "calls-service"
	KindDispatchesAsync    CanonicalEdgeKind = "dispatches-async"
	KindEmitsEvent         CanonicalEdgeKind = "emits-event"
	KindListensEvent       CanonicalEdgeKind = "listens-event"
	KindDataFlows          CanonicalEdgeKind = "data-flows"
	// KindCallsGRPC is a gRPC call between code entities.
	KindCallsGRPC CanonicalEdgeKind = "calls-grpc"
	// KindCallsGraphQL is a GraphQL call between code entities.
	KindCallsGraphQL CanonicalEdgeKind = "calls-graphql"
	// KindCallsTRPC is a tRPC call between code entities.
	KindCallsTRPC CanonicalEdgeKind = "calls-trpc"

	// KindDeploysArtifact is a deployment action: a controller, task, script,
	// or chart acts on an artifact. It is never placement.
	KindDeploysArtifact CanonicalEdgeKind = "deploys-artifact"
	// KindSelectsWorkload is a Service selecting a workload by selector.
	KindSelectsWorkload CanonicalEdgeKind = "selects-workload"
	// KindRoutesTo is an Ingress, HTTPRoute, or route sending traffic to a Service.
	KindRoutesTo CanonicalEdgeKind = "routes-to"
	// KindConfiguredConnection is a Compose service configured to connect to another service.
	KindConfiguredConnection CanonicalEdgeKind = "configured-connection"
	// KindUsesImage is a workload container that runs an image (tag or digest in the properties).
	KindUsesImage CanonicalEdgeKind = "uses-image"
	// KindBuiltFrom is an image built from a Dockerfile.
	KindBuiltFrom CanonicalEdgeKind = "built-from"
	// KindBaseImage is a Dockerfile (build stage) starting from a base image.
	KindBaseImage CanonicalEdgeKind = "base-image"
	// KindCopiesFromImage is a Dockerfile copying files from an external image.
	KindCopiesFromImage CanonicalEdgeKind = "copies-from-image"
	// KindLaunches is a CI job, script, or Dockerfile instruction launching a program.
	KindLaunches CanonicalEdgeKind = "launches"
	// KindOverrides is a variable or patch overriding a Terraform variable or base resource.
	KindOverrides CanonicalEdgeKind = "overrides"
	// KindIncludesInfrastructure is a Terraform module or Kustomization including other infrastructure files.
	KindIncludesInfrastructure CanonicalEdgeKind = "includes-infrastructure"
	// KindInfrastructureDependency is a typed dependency between infrastructure
	// entities. The facet `family` is `chart`, `compose`, or `terraform`; the
	// three families are never merged.
	KindInfrastructureDependency CanonicalEdgeKind = "infrastructure-dependency"
	// KindPlacedIn is a derived placement: a workload runs in a hosting target
	// (the namespace). Concordia derives it from a workload node; the provider
	// has no native edge for it. It is never a code kind and never deployment
	// action (see DerivePlacement).
	KindPlacedIn CanonicalEdgeKind = "placed-in"

	// KindConfiguresEnvAccess is code reading a project-wide environment variable.
	KindConfiguresEnvAccess CanonicalEdgeKind = "configures-env-access"
	// KindConfiguresEnvSet is a workload, Compose service, or Dockerfile setting a scoped environment variable.
	KindConfiguresEnvSet CanonicalEdgeKind = "configures-env-set"
	// KindConfiguresEnvRead is a scoped environment variable read by code.
	KindConfiguresEnvRead CanonicalEdgeKind = "configures-env-read"
	// KindConfiguresKeySymbol is a configuration key associated with a code symbol.
	// It is an inferred association and not observed in the 0.12.1 fixture.
	KindConfiguresKeySymbol CanonicalEdgeKind = "configures-key-symbol"
	// KindConfiguresHelmValues is a `values.yaml` configuring a Chart.
	KindConfiguresHelmValues CanonicalEdgeKind = "configures-helm-values"

	// KindReferencesSecret is a workload reference to a Secret (reference mode `env` or `envFrom`).
	KindReferencesSecret CanonicalEdgeKind = "references-secret"
	// KindReferencesConfiguration is a workload reference to a ConfigMap (reference mode `env` or `envFrom`).
	KindReferencesConfiguration CanonicalEdgeKind = "references-configuration"
	// KindReferencesStorage is a workload reference to a PersistentVolumeClaim.
	KindReferencesStorage CanonicalEdgeKind = "references-storage"
	// KindReferencesCertificateSecret is an Ingress reference to a TLS Secret.
	KindReferencesCertificateSecret CanonicalEdgeKind = "references-certificate-secret"
	// KindUsesRuntimeIdentity is a workload using a ServiceAccount.
	KindUsesRuntimeIdentity CanonicalEdgeKind = "uses-runtime-identity"
	// KindRouteAttachesToGateway is an HTTPRoute attached to a Gateway.
	KindRouteAttachesToGateway CanonicalEdgeKind = "route-attaches-to-gateway"
	// KindBindingRefersToRole is a RoleBinding referring to a Role.
	KindBindingRefersToRole CanonicalEdgeKind = "binding-refers-to-role"
	// KindBindingAppliesToPrincipal is a RoleBinding applying to a ServiceAccount.
	KindBindingAppliesToPrincipal CanonicalEdgeKind = "binding-applies-to-principal"
	// KindPolicySelectsWorkload is a NetworkPolicy selecting a workload. Selection is not isolation.
	KindPolicySelectsWorkload CanonicalEdgeKind = "policy-selects-workload"
	// KindDisruptionPolicyTarget is a PodDisruptionBudget targeting a workload.
	KindDisruptionPolicyTarget CanonicalEdgeKind = "disruption-policy-target"
	// KindScalingTarget is a HorizontalPodAutoscaler scaling a workload.
	KindScalingTarget CanonicalEdgeKind = "scaling-target"

	// KindSupersedes is a decision superseding another one (new to old).
	KindSupersedes CanonicalEdgeKind = "supersedes"
	// KindMentions is a documentation section mentioning a file. It is a candidate only.
	KindMentions CanonicalEdgeKind = "mentions"
	// KindReferencesFile is a file referring to another file by a link or path. It is a candidate only.
	KindReferencesFile CanonicalEdgeKind = "references-file"
	// KindDiagramArrow is an arrow between two diagram elements.
	KindDiagramArrow CanonicalEdgeKind = "diagram-arrow"
	// KindTests is a test entity associated with a tested entity. It is a
	// candidate verification artifact, not a passed test.
	KindTests CanonicalEdgeKind = "tests"
	// KindTestsFile is a test file associated with a tested file. It is a
	// candidate verification artifact, not a passed test.
	KindTestsFile CanonicalEdgeKind = "tests-file"

	// KindSimilarTo is advisory similarity evidence. No mandatory rule uses it.
	KindSimilarTo CanonicalEdgeKind = "similar-to"
	// KindSemanticallyRelated is advisory semantic relatedness evidence. No mandatory rule uses it.
	KindSemanticallyRelated CanonicalEdgeKind = "semantically-related"
	// KindFileChangesWith is advisory co-change evidence. No mandatory rule uses it.
	KindFileChangesWith CanonicalEdgeKind = "file-changes-with"

	// KindUnsupported is the kind of a native edge that Concordia does not map.
	// The fact keeps the native kind, endpoints, location, and properties, and
	// does not count as complete coverage.
	KindUnsupported CanonicalEdgeKind = "unsupported"
	// KindAmbiguous is the kind of a native edge of a mapped type whose
	// discriminators are missing, unlisted, or conflicting. The fact keeps
	// the native kind and has resolution `ambiguous`.
	KindAmbiguous CanonicalEdgeKind = "ambiguous"
)

var codeKinds = []CanonicalEdgeKind{
	KindContains,
	KindDefines,
	KindImports,
	KindUsesType,
	KindInherits,
	KindImplements,
	KindCalls,
	KindReferencesCallable,
	KindAmbiguousUsage,
	KindConstructs,
	KindReads,
	KindWrites,
	KindHandlesRoute,
	KindCallsService,
	KindDispatchesAsync,
	KindEmitsEvent,
	KindListensEvent,
	KindDataFlows,
	KindCallsGRPC,
	KindCallsGraphQL,
	KindCallsTRPC,
}

var infrastructureKinds = []CanonicalEdgeKind{
	KindDeploysArtifact,
	KindSelectsWorkload,
	KindRoutesTo,
	KindConfiguredConnection,
	KindUsesImage,
	KindBuiltFrom,
	KindBaseImage,
	KindCopiesFromImage,
	KindLaunches,
	KindOverrides,
	KindIncludesInfrastructure,
	KindInfrastructureDependency,
	KindPlacedIn,
	KindConfiguresEnvAccess,
	KindConfiguresEnvSet,
	KindConfiguresEnvRead,
	KindConfiguresKeySymbol,
	KindConfiguresHelmValues,
	KindReferencesSecret,
	KindReferencesConfiguration,
	KindReferencesStorage,
	KindReferencesCertificateSecret,
	KindUsesRuntimeIdentity,
	KindRouteAttachesToGateway,
	KindBindingRefersToRole,
	KindBindingAppliesToPrincipal,
	KindPolicySelectsWorkload,
	KindDisruptionPolicyTarget,
	KindScalingTarget,
}

// otherKinds are the canonical kinds that are neither code nor infrastructure:
// documentation, diagram, test, and advisory kinds, and the kinds that keep
// native evidence uninterpreted.
var otherKinds = []CanonicalEdgeKind{
	KindSupersedes,
	KindMentions,
	KindReferencesFile,
	KindDiagramArrow,
	KindTests,
	KindTestsFile,
	KindSimilarTo,
	KindSemanticallyRelated,
	KindFileChangesWith,
	KindUnsupported,
	KindAmbiguous,
}

var canonicalEdgeKinds = slices.Concat(codeKinds, infrastructureKinds, otherKinds)

// CanonicalEdgeKinds gives all canonical edge kinds, in the order of the evidence model.
func CanonicalEdgeKinds() []CanonicalEdgeKind {
	return slices.Clone(canonicalEdgeKinds)
}

// IsCanonicalEdgeKind says whether s is a canonical edge kind.
func IsCanonicalEdgeKind(s string) bool {
	return slices.Contains(canonicalEdgeKinds, CanonicalEdgeKind(s))
}

// CodeKinds gives the canonical kinds of code evidence: structure, imports,
// calls, usage, data flow, and the protocol calls between code entities.
// Reconciliation of architecture pairs considers only these kinds.
func CodeKinds() []CanonicalEdgeKind {
	return slices.Clone(codeKinds)
}

// IsCodeKind says whether kind is a code kind (see CodeKinds).
func IsCodeKind(kind CanonicalEdgeKind) bool {
	return slices.Contains(codeKinds, kind)
}

// InfrastructureKinds gives the canonical kinds of infrastructure evidence:
// deployment, workload, image, build, configuration, and reference kinds.
func InfrastructureKinds() []CanonicalEdgeKind {
	return slices.Clone(infrastructureKinds)
}

// IsInfrastructureKind says whether kind is an infrastructure kind (see InfrastructureKinds).
func IsInfrastructureKind(kind CanonicalEdgeKind) bool {
	return slices.Contains(infrastructureKinds, kind)
}

// lineageKinds are the build lineage kinds: an image and the definition that builds it. They connect an
// image, which has no environment, and a Dockerfile, which no environment source normally contains.
var lineageKinds = []CanonicalEdgeKind{KindBuiltFrom, KindBaseImage, KindCopiesFromImage}

// IsLineageKind says whether kind is a build lineage kind (`built-from`, `base-image`, `copies-from-image`).
// A lineage fact is not specific to an environment.
func IsLineageKind(kind CanonicalEdgeKind) bool {
	return slices.Contains(lineageKinds, kind)
}

// EvidenceRole says what sort of statement an evidence fact makes. It is
// independent of Confidence.
type EvidenceRole string

// Evidence roles.
const (
	// RoleDeclaredIntent is something a person declared as intended (CALM, policy, binding).
	RoleDeclaredIntent EvidenceRole = "declared-intent"
	// RoleParsedSourceFact is a fact that the provider parsed from a committed file.
	RoleParsedSourceFact EvidenceRole = "parsed-source-fact"
	// RoleInferredAssociation is an association that the provider or Concordia inferred.
	RoleInferredAssociation EvidenceRole = "inferred-association"
	// RoleRuntimeObservation is something that was observed in a running system.
	RoleRuntimeObservation EvidenceRole = "runtime-observation"
	// RoleMeasuredResult is the result of a measurement or a test run.
	RoleMeasuredResult EvidenceRole = "measured-result"
	// RoleReviewedJudgment is a judgment that a person reviewed and recorded.
	RoleReviewedJudgment EvidenceRole = "reviewed-judgment"
	// RoleCandidateVerificationArtifact is an artifact that may verify something (a
	// test association) but is not a result. No rule can use it as a passed test.
	RoleCandidateVerificationArtifact EvidenceRole = "candidate-verification-artifact"
)

var evidenceRoles = []EvidenceRole{
	RoleDeclaredIntent,
	RoleParsedSourceFact,
	RoleInferredAssociation,
	RoleRuntimeObservation,
	RoleMeasuredResult,
	RoleReviewedJudgment,
	RoleCandidateVerificationArtifact,
}

// EvidenceRoles gives all evidence roles.
func EvidenceRoles() []EvidenceRole {
	return slices.Clone(evidenceRoles)
}

// IsEvidenceRole says whether s is an evidence role.
func IsEvidenceRole(s string) bool {
	return slices.Contains(evidenceRoles, EvidenceRole(s))
}

// Resolution says how firmly the endpoints of an evidence fact are identified.
// It is independent of Confidence.
type Resolution string

// Resolution values.
const (
	// ResolutionExact means the content was parsed exactly (no correspondence with
	// anything else is implied).
	ResolutionExact Resolution = "exact"
	// ResolutionExplicitlyBound means a binding names the endpoint.
	ResolutionExplicitlyBound Resolution = "explicitly-bound"
	// ResolutionInferred means the endpoint or the association was inferred.
	ResolutionInferred Resolution = "inferred"
	// ResolutionAmbiguous means discriminators are missing, unlisted, or conflicting.
	ResolutionAmbiguous Resolution = "ambiguous"
	// ResolutionUnresolved means the endpoint could not be identified.
	ResolutionUnresolved Resolution = "unresolved"
	// ResolutionUnbound is the state of a supporting entity that no binding refers to.
	ResolutionUnbound Resolution = "unbound"
)

var resolutions = []Resolution{
	ResolutionExact,
	ResolutionExplicitlyBound,
	ResolutionInferred,
	ResolutionAmbiguous,
	ResolutionUnresolved,
	ResolutionUnbound,
}

// Resolutions gives all resolution values, strongest first.
func Resolutions() []Resolution {
	return slices.Clone(resolutions)
}

// IsResolution says whether s is a resolution value.
func IsResolution(s string) bool {
	return slices.Contains(resolutions, Resolution(s))
}

// Rank gives the rank of a resolution: exact 4, explicitly-bound 3, inferred
// 2, ambiguous 1, unresolved 0. `unbound` and an unknown value have rank 0.
func (r Resolution) Rank() int {
	switch r {
	case ResolutionExact:
		return resolutionRankExact
	case ResolutionExplicitlyBound:
		return resolutionRankBound
	case ResolutionInferred:
		return resolutionRankInferred
	case ResolutionAmbiguous:
		return resolutionRankAmbiguous
	case ResolutionUnresolved, ResolutionUnbound:
		return 0
	default:
		return 0
	}
}

const (
	resolutionRankAmbiguous = 1
	resolutionRankInferred  = 2
	resolutionRankBound     = 3
	resolutionRankExact     = 4
)

// AtLeast says whether r is at least as strong as min.
func (r Resolution) AtLeast(minimum Resolution) bool {
	return r.Rank() >= minimum.Rank()
}

// Integrity says whether the content of an evidence fact is known to be
// unchanged since the provider produced it.
type Integrity string

// Integrity values.
const (
	// IntegrityVerified means the fact comes from a graph generation that was
	// frozen and verified again (CO-OBS-008 did not fire).
	IntegrityVerified Integrity = "verified"
	// IntegrityUnverified means no such verification covers the fact.
	IntegrityUnverified Integrity = "unverified"
)

var integrities = []Integrity{IntegrityVerified, IntegrityUnverified}

// Integrities gives all integrity values.
func Integrities() []Integrity {
	return slices.Clone(integrities)
}

// IsIntegrity says whether s is an integrity value.
func IsIntegrity(s string) bool {
	return slices.Contains(integrities, Integrity(s))
}

// SourceSpan is a place in a source file.
type SourceSpan struct {
	// Path is a "/" path relative to the repository root.
	Path string `json:"path"`
	// Line is the 1-based line. 0 means no line.
	Line int `json:"line,omitempty"`
}

// Derivation records how Concordia derived a fact that has no native edge.
type Derivation struct {
	// Rule is the derivation rule name, for example `placement-from-namespace`.
	Rule string `json:"rule"`
	// Inputs are the observed IDs of the nodes that the rule read.
	Inputs []string `json:"inputs,omitempty"`
	// Version is the version of the rule.
	Version int `json:"version"`
}

// AnalyzerDescriptor says which analyzer made a piece of evidence.
type AnalyzerDescriptor struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	AdapterSchema string `json:"adapterSchema"`
}

// EvidenceEdge is one normalized edge. From and To are observed IDs
// (`cbm:<project>:<native id>`).
//
// Role, Resolution, Completeness, and Integrity are separate evidence quality
// dimensions. They are never derived from Confidence, which keeps its meaning.
type EvidenceEdge struct {
	Properties map[string]string `json:"properties"`
	// Facets are the facet values of the kind, for example `family`,
	// `referenceMode`, `targetKind`, or `terraformRef`. Facets hold no secret values.
	Facets     map[string]string `json:"facets,omitempty"`
	SourceSpan *SourceSpan       `json:"sourceSpan,omitempty"`
	// Derivation is set for a fact that Concordia derived from other evidence. Omitted when empty.
	Derivation *Derivation       `json:"derivation,omitempty"`
	ID         string            `json:"id"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	Kind       CanonicalEdgeKind `json:"kind"`
	NativeKind string            `json:"nativeKind"`
	Confidence Confidence        `json:"confidence"`
	// Role is the evidence role of the fact.
	Role EvidenceRole `json:"role,omitempty"`
	// Resolution is how firmly the endpoints are identified.
	Resolution Resolution `json:"resolution,omitempty"`
	// Completeness is a reference key into the coverage predicate records.
	Completeness string `json:"completeness,omitempty"`
	// Integrity says whether the fact is known to be unchanged.
	Integrity Integrity `json:"integrity,omitempty"`
	// NormalizationVersion is the version of the normalization table that made the fact.
	NormalizationVersion string `json:"normalizationVersion,omitempty"`
	// Occurrence is the discriminator of one occurrence, for example a container name.
	Occurrence string `json:"occurrence,omitempty"`
	// SourceLabel and TargetLabel are the native labels of the endpoints.
	SourceLabel string `json:"sourceLabel,omitempty"`
	TargetLabel string `json:"targetLabel,omitempty"`
	// Environment is the declared environment that the fact is attributed to.
	Environment      string             `json:"environment,omitempty"`
	ResolutionMethod string             `json:"resolutionMethod"`
	GraphGeneration  string             `json:"graphGeneration"`
	Analyzer         AnalyzerDescriptor `json:"analyzer"`
	// LossModes are the loss modes that the profile states for the family of
	// the fact. Set with PossiblyLossy.
	LossModes []string `json:"lossModes,omitempty"`
	// PossiblyLossy marks a fact that the provider may have merged with others
	// (the profile states the loss mode).
	PossiblyLossy bool `json:"possiblyLossy,omitempty"`
}

// ObservedNode is one node of the observed graph.
type ObservedNode struct {
	// Properties are provider properties that the adapter kept verbatim on the
	// node, for example `adr_id` and `superseded_by` of an `ADR`, or `element_id`
	// of a `DiagramElement`. Empty for nodes with no such properties.
	Properties map[string]string `json:"properties,omitempty"`
	// ObservedID is `cbm:<project>:<native id>`.
	ObservedID string `json:"observedId"`
	// Label is the native label, for example `File`, `Module`, `Function`.
	Label string `json:"label"`
	// QualifiedName is the qualified name without the `<project>.` prefix.
	QualifiedName string `json:"qualifiedName"`
	// FilePath is a "/" path relative to the repository root. Empty when CBM gives none.
	FilePath string `json:"filePath"`
	// Environment is the declared environment that the node is attributed to.
	// Empty when the node has no attribution.
	Environment string `json:"environment,omitempty"`
	// Kind is the provider `kind` property of the node, for example `Secret` or
	// `Deployment` for a `Resource`. Empty when the provider gives none or the
	// adapter did not ask for it.
	Kind string `json:"kind,omitempty"`
	// Resolution is the resolution state of a synthesized supporting entity
	// (`unbound` until a binding refers to it). Empty for provider nodes.
	Resolution Resolution `json:"resolution,omitempty"`
	// ScopedID is the identity of a `Resource` node (`k8s:<env>/...`). Empty
	// for a node that has none.
	ScopedID string `json:"scopedId,omitempty"`
	// StartLine is the 1-based start line. 0 means no line.
	StartLine int `json:"startLine,omitempty"`
}

// SchemaLabel is a node label of the CBM graph schema.
type SchemaLabel struct {
	Label      string   `json:"label"`
	Properties []string `json:"properties,omitempty"`
	Count      float64  `json:"count"`
}

// SchemaEdgeType is an edge type of the CBM graph schema.
type SchemaEdgeType struct {
	Type       string   `json:"type"`
	Properties []string `json:"properties,omitempty"`
	Count      float64  `json:"count"`
}

// GraphSchema holds the node labels and edge types of the CBM graph.
type GraphSchema struct {
	NodeLabels []SchemaLabel    `json:"node_labels"`
	EdgeTypes  []SchemaEdgeType `json:"edge_types"`
}

// GraphSnapshotRef is the reference to a frozen graph generation.
type GraphSnapshotRef struct {
	Project string `json:"project"`
	// GraphGeneration is `cbm:<project>:<16 hex>`.
	GraphGeneration string `json:"graphGeneration"`
	// HeadSHA is the git head of the index. Empty when CBM gives none.
	HeadSHA string `json:"headSha"`
	// Schema holds the edge types and node labels of the graph.
	Schema GraphSchema `json:"schema"`
	// Nodes and Edges are the counts at freeze time.
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

// Capabilities says what a provider can do.
type Capabilities struct {
	ExecutableVersion       string              `json:"executableVersion"`
	AdapterSchema           string              `json:"adapterSchema"`
	SupportedCanonicalKinds []CanonicalEdgeKind `json:"supportedCanonicalKinds"`
	// Profile identifies the capability profile of the build. Nil when no profile applies.
	Profile *ProfileRef `json:"profile,omitempty"`
	// Families is the support of each family: the profile compared with the
	// graph schemas that the provider has frozen so far.
	Families map[string]FamilyState `json:"families,omitempty"`
	// Diagnostics are CO-OBS-017 warnings: no profile for the build, or a
	// family with a missing required property.
	Diagnostics []diag.Diagnostic `json:"-"`
}

// FreezeRequest is the input of Provider.Freeze.
type FreezeRequest struct {
	Project string
	// GitRevision is the git revision of the workspace. Empty when the
	// workspace is not a git repository: the revision check is then skipped.
	GitRevision string
	// IndexedRevision is the git revision that the last `concordia index`
	// recorded, when known. It takes precedence over the `head_sha` of CBM,
	// because CBM reports the live git head and not the head at index time.
	IndexedRevision string
}

// Scope is the input for file coverage.
type Scope struct {
	WorkspaceFiles []string
	Include        []string
	Exclude        []string
	DirtyFiles     []string
	// Sizes are the sizes in bytes of the workspace files, by "/" path.
	Sizes map[string]int64
	// ScopeGlobs, when not nil, keeps only the files that also match one of
	// these globs. An empty, non-nil list keeps no file.
	ScopeGlobs []string
}

// LanguageCoverage is the coverage of the files with one extension.
type LanguageCoverage struct {
	Total   int `json:"total"`
	Covered int `json:"covered"`
}

// ByteCoverage is the byte dimension of the file coverage: the sizes of the files in scope.
type ByteCoverage struct {
	Total   int64   `json:"total"`
	Covered int64   `json:"covered"`
	Ratio   float64 `json:"ratio"`
}

// FileCoverage is the file dimension of the coverage model.
type FileCoverage struct {
	Languages map[string]LanguageCoverage `json:"languages"`
	Excluded  []string                    `json:"excluded"`
	Stale     []string                    `json:"stale"`
	Unindexed []string                    `json:"unindexed"`
	// Unsupported lists the files in scope, not excluded, whose language CBM has no File node for.
	Unsupported []string     `json:"unsupported"`
	Bytes       ByteCoverage `json:"bytes"`
	Ratio       float64      `json:"ratio"`
	Total       int          `json:"total"`
	Covered     int          `json:"covered"`
	EmptyScope  bool         `json:"emptyScope,omitempty"`
}

// EdgeQueryPlan says which native edge types to fetch.
type EdgeQueryPlan struct {
	// NativeKinds are native edge types, for example `IMPORTS`.
	NativeKinds []string
}

// EvidenceQueryResult is the result of Provider.FetchEdges.
type EvidenceQueryResult struct {
	// Nodes are the observed nodes by observed ID.
	Nodes map[string]ObservedNode
	// UnmappedNativeEdges counts the native edges of types that Concordia does
	// not map, per native type.
	UnmappedNativeEdges map[string]int
	// Unsupported are the facts of the native edge types that Concordia does
	// not map. Each has kind `unsupported`, keeps the native kind, endpoints,
	// location, and properties, and is not part of Edges. UnmappedNativeEdges
	// keeps the counts.
	Unsupported []*EvidenceEdge
	// Entities are the synthesized supporting entities (images, CI tasks,
	// decisions, secret references, diagram elements) of the fetched edges.
	// Each has an id with the prefix `synth:` and resolution `unbound`. They
	// are not part of Nodes, because their ids are not provider ids.
	Entities []ObservedNode
	// Diagnostics are the warnings of the fetch: unmapped native kinds kept as
	// unsupported evidence and facts kept as ambiguous.
	Diagnostics []diag.Diagnostic
	// QueryPlanDigest is the sha256 of the sorted list of the query digests.
	QueryPlanDigest string
	// Edges are sorted by (nativeKind, from QN, to QN, line, id).
	Edges []*EvidenceEdge
}

// Provider is the observation provider interface.
type Provider interface {
	Capabilities(ctx context.Context) Capabilities
	Freeze(ctx context.Context, req FreezeRequest) (*GraphSnapshotRef, error)
	// VerifyFrozen checks again that the graph generation did not change.
	// It gives CO-OBS-008 when it did.
	VerifyFrozen(ctx context.Context, ref *GraphSnapshotRef) error
	Coverage(ctx context.Context, ref *GraphSnapshotRef, scope *Scope) (FileCoverage, error)
	// ListFiles gives all File node paths of the graph.
	ListFiles(ctx context.Context, ref *GraphSnapshotRef) ([]string, error)
	Query(ctx context.Context, ref *GraphSnapshotRef, plan EdgeQueryPlan) ([]*EvidenceEdge, error)
	FetchEdges(ctx context.Context, ref *GraphSnapshotRef, nativeKinds []string) (EvidenceQueryResult, error)
}
