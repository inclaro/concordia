package trust

import "slices"

// Dependency kinds that an assessment dependency can use (the closed list of the
// trust-context spec).
const (
	KindRuntimeIdentity        = "runtime-identity"
	KindRoleBinding            = "role-binding"
	KindSecretReference        = "secret-reference"
	KindConfigurationReference = "configuration-reference"
	KindStorageReference       = "storage-reference"
	KindPublicEntryPoint       = "public-entry-point"
	KindProtocol               = "protocol"
	KindCertificateReference   = "certificate-reference"
	KindExternalDestination    = "external-destination"
	KindTrustContext           = "trust-context"
	KindDataClassification     = "data-classification"
	KindPlacement              = "placement"
	KindBinding                = "binding"
	KindControl                = "control"
	KindImplementation         = "implementation"
	KindVerification           = "verification"
	KindQualityScenario        = "quality-scenario"
)

// DependencyKinds gives the closed list of assessment dependency kinds, in the
// order of the trust-context spec. Any other kind fails validation.
func DependencyKinds() []string {
	return []string{
		KindRuntimeIdentity, KindRoleBinding, KindSecretReference, KindConfigurationReference,
		KindStorageReference, KindPublicEntryPoint, KindProtocol, KindCertificateReference,
		KindExternalDestination, KindTrustContext, KindDataClassification, KindPlacement,
		KindBinding, KindControl, KindImplementation, KindVerification, KindQualityScenario,
	}
}

// IsDependencyKind says whether kind is in the closed list.
func IsDependencyKind(kind string) bool {
	return slices.Contains(DependencyKinds(), kind)
}

// ReferenceKinds are the kinds whose items hold identifiers and roles only, never a value.
func ReferenceKinds() []string {
	return []string{KindSecretReference, KindConfigurationReference, KindStorageReference, KindCertificateReference}
}
