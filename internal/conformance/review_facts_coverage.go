package conformance

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/observation"
)

// The native `ref` values of the reference facts (CBM 0.12.1 observations, O5).
const (
	refEnv            = "env"
	refEnvFrom        = "envFrom"
	refVolume         = "volume"
	refTLS            = "tls"
	refParentRef      = "parentRef"
	refRoleRef        = "roleRef"
	refServiceAccount = "serviceAccountName"
	refSubject        = "subject"
)

// The target kinds of the reference facts.
const (
	targetSecret    = "Secret"
	targetConfigMap = "ConfigMap"
	targetClaim     = "PersistentVolumeClaim"
)

// kindFamilies are the evidence families that a dependency kind reads. The kind is supported when the profile
// declares each of them above `unsupported`, and its coverage is the coverage of these predicates.
var kindFamilies = map[impact.Kind][]string{
	impact.KindRuntimeIdentity:        {string(observation.KindUsesRuntimeIdentity), string(observation.KindBindingAppliesToPrincipal), string(observation.KindBindingRefersToRole)},
	impact.KindRoleBinding:            {string(observation.KindBindingRefersToRole), string(observation.KindBindingAppliesToPrincipal)},
	impact.KindSecretReference:        {string(observation.KindReferencesSecret)},
	impact.KindConfigurationReference: {string(observation.KindReferencesConfiguration)},
	impact.KindStorageReference:       {string(observation.KindReferencesStorage)},
	impact.KindPublicEntryPoint:       {string(observation.KindRoutesTo), string(observation.KindRouteAttachesToGateway)},
	impact.KindCertificateReference:   {string(observation.KindReferencesCertificateSecret)},
	impact.KindExternalDestination:    {string(observation.KindCallsService), string(observation.KindDispatchesAsync)},
	impact.KindPlacement:              {string(observation.KindPlacedIn)},
}

// imageFamilies are the families of the image of a container occurrence (the `implementation` items of a workload).
var imageFamilies = []string{string(observation.KindUsesImage), observation.FamilyContainerOccurrenceVersions}

// mitigatedLossModes are the loss modes of a family that the item of a kind does not suffer from. The identity of a
// public entry point is the scoped Ingress or HTTPRoute resource, not the project-global `Route` node that merges
// equal host and path of two environments (O7).
var mitigatedLossModes = map[impact.Kind][]string{
	impact.KindPublicEntryPoint: {observation.LossRouteIdentityUnscoped},
}

// modelKinds are the kinds that the CALM model and the bindings supply: no observation family is needed.
var modelKinds = []impact.Kind{
	impact.KindProtocol, impact.KindTrustContext, impact.KindDataClassification, impact.KindBinding, impact.KindControl,
	impact.KindImplementation,
}

// reviewKindCount is the number of kinds that ReviewEvidenceKinds lists besides the cross-check kinds, plus room for those.
const reviewKindCount = 16

// ReviewEvidenceKinds are the canonical kinds that the review facts read, so that a run fetches them even when no
// rule traverses them. The workload nodes come with the cross-check kinds (`uses-image`).
func ReviewEvidenceKinds() []observation.CanonicalEdgeKind {
	kinds := make([]observation.CanonicalEdgeKind, 0, reviewKindCount)
	kinds = append(kinds,
		observation.KindUsesRuntimeIdentity, observation.KindBindingAppliesToPrincipal, observation.KindBindingRefersToRole,
		observation.KindReferencesSecret, observation.KindReferencesConfiguration, observation.KindReferencesStorage,
		observation.KindReferencesCertificateSecret, observation.KindRouteAttachesToGateway, observation.KindRoutesTo,
		observation.KindCallsService, observation.KindDispatchesAsync,
	)

	return append(kinds, observation.CrossCheckEvidenceKinds()...)
}

// assuranceSelectors are the selector facets of the assurance kinds besides the contract fields.
var assuranceSelectors = map[impact.Kind][]string{
	impact.KindVerification: {facetCheck}, impact.KindQualityScenario: {facetIdentity},
}

// reviewProfile derives the capability profile of the run from the provider profile: a kind is supported when
// every family it reads is declared and not unsupported. A run with no profile supports the model-based kinds only.
// The assurance kinds depend on the assurance evidence, not on the provider: they are supported exactly when the
// workspace has an `assurance` key.
func reviewProfile(profile *observation.CapabilityProfile, contract *impact.Contract, assurance bool) impact.CapabilityProfile {
	out := impact.CapabilityProfile{ID: reviewProfileNone, Kinds: []impact.Kind{}, Selectors: map[impact.Kind][]string{}}
	if profile != nil {
		out.ID = profile.ID
	}

	for _, kind := range impact.Kinds() {
		if !kindSupported(profile, kind, assurance) {
			continue
		}

		out.Kinds = append(out.Kinds, kind)

		names := contract.FieldsOf(kind)

		if kind == impact.KindImplementation {
			names = append(names, facetPath, facetRepository, facetTag, facetDigest)
		}

		names = append(names, assuranceSelectors[kind]...)

		slices.SortFunc(names, canonjson.CompareUTF16)
		out.Selectors[kind] = names
	}

	return out
}

func kindSupported(profile *observation.CapabilityProfile, kind impact.Kind, assurance bool) bool {
	if kind == impact.KindVerification || kind == impact.KindQualityScenario {
		return assurance
	}

	if slices.Contains(modelKinds, kind) {
		return true
	}

	families, ok := kindFamilies[kind]
	if !ok || profile == nil {
		return false
	}

	return !slices.ContainsFunc(families, func(name string) bool {
		return profile.Level(name) == "" || profile.Level(name) == observation.SupportUnsupported
	})
}
