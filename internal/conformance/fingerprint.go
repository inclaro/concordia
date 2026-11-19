package conformance

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
)

// Parts are the inputs of a finding fingerprint.
type Parts struct {
	RuleID               string
	SourceArchitectureID string
	TargetArchitectureID string
	// CanonicalEdgeKind is the semantic evidence kind (`observation.EvidenceEdge.Kind`, for
	// example `imports` or `routes-to`), never the provider's native kind, or a fixed word
	// (`cycle`, `path`, `unobserved`, `budget:<measure>`) for findings without one edge.
	CanonicalEdgeKind   string
	SourceQualifiedName string
	TargetQualifiedName string
	// NormalizedRepositoryPath is the source file path. Line and column are never part of the fingerprint.
	NormalizedRepositoryPath string
}

// TypedParts are the inputs of the fingerprint of a finding of a typed obligation: the
// common parts plus the environment of the obligation and the discriminator of the
// occurrence (for example a container name), so that the same rule and subject give one
// fingerprint for each environment and occurrence.
type TypedParts struct {
	Parts
	// Environment is the declared environment of the obligation (empty when it has none).
	Environment string
	// Occurrence is the occurrence discriminator of the finding (empty when it has none).
	Occurrence string
}

// Fingerprint version tags. A tag is the first hashed input, so a later change to the input
// list or to the hashing contract is visible in every fingerprint: change the tag and the
// golden vectors (`test/golden/fingerprints.json`) in the same commit.
const (
	fingerprintVersion      = "concordia-fingerprint/2"
	typedFingerprintVersion = "concordia-fingerprint/2+typed"
)

const fingerprintSeparator = "\x1f"

func commonInputs(version string, parts *Parts) []string {
	return []string{
		version,
		parts.RuleID,
		parts.SourceArchitectureID,
		parts.TargetArchitectureID,
		parts.CanonicalEdgeKind,
		parts.SourceQualifiedName,
		parts.TargetQualifiedName,
		fsutil.ToPosix(parts.NormalizedRepositoryPath),
	}
}

// Fingerprint gives `sha256:<hex>`. Stored contract: baselines and waivers depend on
// these inputs and on the separator.
func Fingerprint(parts Parts) string { //nolint:gocritic // the contract signature takes the parts by value.
	return digest.Prefix + digest.SHA256HexString(strings.Join(commonInputs(fingerprintVersion, &parts), fingerprintSeparator))
}

// TypedFingerprint gives the fingerprint of a finding of a typed obligation: the inputs of
// Fingerprint (under the typed version tag) followed by the environment and the occurrence
// discriminator.
func TypedFingerprint(parts *TypedParts) string {
	inputs := append(commonInputs(typedFingerprintVersion, &parts.Parts), parts.Environment, parts.Occurrence)

	return digest.Prefix + digest.SHA256HexString(strings.Join(inputs, fingerprintSeparator))
}

// CycleFingerprint is the fingerprint of a dependency cycle through the given CALM IDs.
func CycleFingerprint(ruleID string, cycleIDs []string) string {
	sorted := slices.Clone(cycleIDs)
	slices.SortFunc(sorted, canonjson.CompareUTF16)

	first := ""
	if len(sorted) > 0 {
		first = sorted[0]
	}

	return Fingerprint(Parts{
		RuleID:               ruleID,
		SourceArchitectureID: first,
		TargetArchitectureID: strings.Join(sorted, ","),
		CanonicalEdgeKind:    "cycle",
	})
}

// UnobservedFingerprint is the fingerprint of a declared relationship that was not observed.
func UnobservedFingerprint(ruleID, sourceID string, targetIDs []string, relationshipID string) string {
	return Fingerprint(Parts{
		RuleID:               ruleID,
		SourceArchitectureID: sourceID,
		TargetArchitectureID: strings.Join(targetIDs, ","),
		CanonicalEdgeKind:    "unobserved",
		SourceQualifiedName:  relationshipID,
	})
}
