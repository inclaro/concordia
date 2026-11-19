// Package impact computes which approved assessments a change affects. It holds the dependency
// items and their digests (the digest contract), the diff of the current items against the items
// that an approval bound, the dependency graph with its breadth-first traversal, and the limits
// of that traversal.
//
// The package takes its facts through the FactSource interface. It never imports
// internal/conformance: conformance implements FactSource and calls into this package.
package impact

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/trust"
)

// ContractVersion is the version of the digest contract that this release writes into new
// approvals. The contract lists which fields of each dependency kind enter the item digest.
const ContractVersion = "review-digest/1"

// fieldConsumer is the facet name of the consuming container of a reference item.
const (
	fieldConsumer  = "consumer"
	fieldReference = "reference"
	fieldRole      = "role"
)

// ClassOutsideContract is the class of a Redaction record for a facet that a reference item does
// not carry because the digest contract does not list it. The facet value is dropped, never kept.
const ClassOutsideContract = redact.ClassOutsideContract

// Reasons that this package names. The strings mirror the `UnknownReason` values of
// internal/conformance, which impact cannot import. A test in internal/conformance asserts that
// the two sets agree.
const (
	// ReasonReferenceUnresolved: an implementation or verification path is missing at the revision.
	ReasonReferenceUnresolved = "reference-unresolved"
	// ReasonUnsupportedFamily: the capability profile does not map the dependency kind.
	ReasonUnsupportedFamily = "unsupported-family"
	// ReasonTargetNotDeclared: a reference whose target the provider did not declare.
	ReasonTargetNotDeclared = "target-not-declared"
	// ReasonCoverageIncomplete: coverage for the kind in the scope is not complete.
	ReasonCoverageIncomplete = "coverage-incomplete"
	// ReasonLimitExceeded: a traversal limit stopped the analysis.
	ReasonLimitExceeded = "limit-exceeded"
	// ReasonNoEvidenceSupplied: the evidence source of the kind (assurance check results, quality scenarios) is not
	// read completely, so an empty match says nothing. The assessment evaluation reports it as coverage-incomplete.
	ReasonNoEvidenceSupplied = "no-evidence-supplied"
)

// Kind is a dependency kind. The list is closed (trust-context, "Dependency kinds eligible for assessments").
type Kind string

// The closed list of dependency kinds, in the order of the spec.
const (
	KindRuntimeIdentity        Kind = trust.KindRuntimeIdentity
	KindRoleBinding            Kind = trust.KindRoleBinding
	KindSecretReference        Kind = trust.KindSecretReference
	KindConfigurationReference Kind = trust.KindConfigurationReference
	KindStorageReference       Kind = trust.KindStorageReference
	KindPublicEntryPoint       Kind = trust.KindPublicEntryPoint
	KindProtocol               Kind = trust.KindProtocol
	KindCertificateReference   Kind = trust.KindCertificateReference
	KindExternalDestination    Kind = trust.KindExternalDestination
	KindTrustContext           Kind = trust.KindTrustContext
	KindDataClassification     Kind = trust.KindDataClassification
	KindPlacement              Kind = trust.KindPlacement
	KindBinding                Kind = trust.KindBinding
	KindControl                Kind = trust.KindControl
	KindImplementation         Kind = trust.KindImplementation
	KindVerification           Kind = trust.KindVerification
	KindQualityScenario        Kind = trust.KindQualityScenario
)

// kinds is the closed list in spec order; internal/trust owns the list.
var kinds = func() []Kind {
	list := trust.DependencyKinds()
	out := make([]Kind, len(list))

	for i, k := range list {
		out[i] = Kind(k)
	}

	return out
}()

// Kinds gives the closed list of dependency kinds.
func Kinds() []Kind { return slices.Clone(kinds) }

// Valid says whether the kind is in the closed list.
func (k Kind) Valid() bool { return slices.Contains(kinds, k) }

// String gives the identifier.
func (k Kind) String() string { return string(k) }

// IsReference says whether the kind is a reference kind. An item of a reference kind carries
// identifiers and roles only: it never keeps a facet outside the contract and never a secret value.
func (k Kind) IsReference() bool { return slices.Contains(trust.ReferenceKinds(), string(k)) }

// Facets are the observed facts of an item. Each facet is a set of strings: a single value is a
// one-element set. The digest sorts and de-duplicates the values, so the order of the values does
// not matter.
type Facets map[string][]string

// Item is one dependency of an assessment: a fact about the system with a stable identity.
type Item struct {
	// Kind is the dependency kind.
	Kind Kind `json:"kind"`
	// Identity is the scoped observed-entity identity or the CALM identity. It is the key of the
	// item and does not enter the digest.
	Identity string `json:"identity"`
	// Facets are the observed facts. Facets outside the digest contract are kept for explanation
	// (never on a reference kind) and do not change the digest.
	Facets Facets `json:"facets"`
	// ImplementationRefs are repository paths of the code that implements the claim.
	ImplementationRefs []string `json:"implementationRefs"`
	// VerificationRefs are repository paths of the tests or checks that verify the claim.
	VerificationRefs []string `json:"verificationRefs"`
	// Redactions record each facet value that was removed or replaced. They never hold a value.
	Redactions []redact.Record `json:"redactions"`
}

// Contract lists, for each dependency kind, the facets that enter the item digest, plus the
// versions that this contract is marked equivalent to.
type Contract struct {
	// Version is the contract version, for example `review-digest/1`.
	Version string
	// Fields maps each kind to the facet names that enter the digest.
	Fields map[Kind][]string
	// Equivalent lists the earlier versions that this contract keeps digests for. Each entry
	// needs a fixture (see Equivalence).
	Equivalent []Equivalence
}

// V1 gives the contract `review-digest/1`. Field names follow the trust-context spec.
func V1() Contract {
	return Contract{
		Version: ContractVersion,
		Fields: map[Kind][]string{
			KindRuntimeIdentity:        {"serviceAccount", "roleReferences", "identityProvider"},
			KindRoleBinding:            {"roleReference", "subjects", "scope"},
			KindSecretReference:        {fieldReference, fieldRole, fieldConsumer},
			KindConfigurationReference: {fieldReference, fieldRole, fieldConsumer},
			KindStorageReference:       {fieldReference, fieldRole, fieldConsumer},
			KindPublicEntryPoint:       {"host", "path", "backend", "exposureClass"},
			KindProtocol:               {"protocol", "port", "flow"},
			KindCertificateReference:   {fieldReference, fieldRole, fieldConsumer},
			KindExternalDestination:    {"destination", "protocol", "port"},
			KindTrustContext:           {"element", "dimensions"},
			KindDataClassification:     {"element", "classification"},
			KindPlacement:              {"workload", "environment", "platform", "region"},
			KindBinding:                {"source", "target", "mode"},
			KindControl:                {"control", "target", "configuration"},
			KindImplementation:         {"subject"},
			KindVerification:           {"subject", "result"},
			KindQualityScenario:        {"scenario", "responseMeasure"},
		},
	}
}

// FieldsOf gives the facet names that enter the digest for the kind, sorted.
func (c Contract) FieldsOf(kind Kind) []string {
	fields := slices.Clone(c.Fields[kind])
	slices.SortFunc(fields, canonjson.CompareUTF16)

	return fields
}

// NewItem builds an item under the contract. Facet values are sorted, de-duplicated, and passed
// through the redaction rules (a secret-shaped value is replaced by a marker and recorded). On a
// reference kind a facet outside the contract is dropped and recorded. Every FactSource builds its
// items with NewItem, so no output can hold a secret value that a manifest carried next to a
// reference.
func (c Contract) NewItem(kind Kind, identity string, facets Facets, implementation, verification []string) Item {
	item := Item{
		Kind:               kind,
		Identity:           identity,
		Facets:             Facets{},
		ImplementationRefs: sortedSet(implementation),
		VerificationRefs:   sortedSet(verification),
		Redactions:         []redact.Record{},
	}

	inContract := make(map[string]bool, len(c.Fields[kind]))
	for _, name := range c.Fields[kind] {
		inContract[name] = true
	}

	rules := redact.Default()

	for name, values := range facets {
		if kind.IsReference() && !inContract[name] {
			item.Redactions = append(item.Redactions, redact.Record{Property: name, Class: ClassOutsideContract})

			continue
		}

		kept := make([]string, 0, len(values))

		for _, value := range values {
			redacted, records := rules.RedactValue(name, value)
			item.Redactions = append(item.Redactions, records...)
			kept = append(kept, redacted)
		}

		if set := sortedSet(kept); len(set) > 0 {
			item.Facets[name] = set
		}
	}

	slices.SortFunc(item.Redactions, func(a, b redact.Record) int {
		return cmp.Or(canonjson.CompareUTF16(a.Property, b.Property), canonjson.CompareUTF16(a.Class, b.Class))
	})

	return item
}

// RefRole says what a referenced file is for.
type RefRole string

// Reference roles.
const (
	RefImplementation RefRole = "implementation"
	RefVerification   RefRole = "verification"
)

// RefDigest is the digest of one referenced file at a revision.
type RefDigest struct {
	Role   RefRole `json:"role"`
	Path   string  `json:"path"`
	Digest string  `json:"digest"`
}

// Unknown says why a comparison cannot run. Reason is one of the Reason constants.
type Unknown struct {
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

// Sealed is an item with its digest. When a referenced file is missing, Digest is empty and
// Unknown is set.
type Sealed struct {
	Unknown *Unknown    `json:"unknown,omitempty"`
	Digest  string      `json:"digest"`
	Item    Item        `json:"item"`
	Refs    []RefDigest `json:"refs"`
}

// Entry gives the comparison form of the sealed item.
func (s *Sealed) Entry() Entry {
	entry := Entry{Kind: s.Item.Kind, Identity: s.Item.Identity, Digest: s.Digest}
	if s.Unknown != nil {
		entry.Reason = s.Unknown.Reason
	}

	return entry
}

// ErrUnknownKind is returned for an item whose kind is not in the closed list.
var ErrUnknownKind = errors.New("impact: dependency kind is not in the closed list")

// FileDigester gives the digest of the file at a path at a fixed revision. found is false when
// the path does not exist there. An error means that the read itself failed.
type FileDigester func(path string) (digest string, found bool, err error)

// Digester computes the digest of an item from its contract fields and the digests of its files. Contract.Digest is
// the digester of a contract; the incremental cache gives one that remembers digests.
type Digester func(item Item, refs []RefDigest) string

// Seal computes the digest of the item. The digest covers the contract fields and the digests of
// the implementation and verification files at the revision that files reads. A missing file gives
// an Unknown with the reason `reference-unresolved` and no digest.
func (c Contract) Seal(item Item, files FileDigester) (Sealed, error) { //nolint:gocritic // Item is a value type that FactSources return in slices.
	return c.SealWith(item, files, nil)
}

// SealWith is Seal with the digester that computes the digest. nil means Contract.Digest.
func (c Contract) SealWith(item Item, files FileDigester, digester Digester) (Sealed, error) { //nolint:gocritic // Item is a value type that FactSources return in slices.
	if !item.Kind.Valid() {
		return Sealed{}, fmt.Errorf("%w: %q", ErrUnknownKind, item.Kind)
	}

	if digester == nil {
		digester = c.Digest
	}

	sealed := Sealed{Item: item, Refs: []RefDigest{}}

	var missing []string

	read := func(role RefRole, paths []string) error {
		for _, path := range sortedSet(paths) {
			sum, found, err := files(path)
			if err != nil {
				return fmt.Errorf("impact: read %s %s: %w", role, path, err)
			}

			if !found {
				missing = append(missing, string(role)+" "+path)

				continue
			}

			sealed.Refs = append(sealed.Refs, RefDigest{Role: role, Path: path, Digest: sum})
		}

		return nil
	}

	if err := read(RefImplementation, item.ImplementationRefs); err != nil {
		return Sealed{}, err
	}

	if err := read(RefVerification, item.VerificationRefs); err != nil {
		return Sealed{}, err
	}

	if len(missing) > 0 {
		sealed.Unknown = &Unknown{
			Reason: ReasonReferenceUnresolved,
			Detail: "path not found at the revision: " + strings.Join(missing, ", "),
		}

		return sealed, nil
	}

	sealed.Digest = digester(item, sealed.Refs)

	return sealed, nil
}

// SealAll seals every item and sorts the result by kind and identity.
func (c Contract) SealAll(items []Item, files FileDigester) ([]Sealed, error) {
	return c.SealAllWith(items, files, nil)
}

// SealAllWith is SealAll with the digester that computes the digests. nil means Contract.Digest.
func (c Contract) SealAllWith(items []Item, files FileDigester, digester Digester) ([]Sealed, error) {
	out := make([]Sealed, 0, len(items))

	for _, item := range items {
		sealed, err := c.SealWith(item, files, digester)
		if err != nil {
			return nil, err
		}

		out = append(out, sealed)
	}

	slices.SortFunc(out, func(a, b Sealed) int { return compareKey(a.Item.Kind, a.Item.Identity, b.Item.Kind, b.Item.Identity) })

	return out, nil
}

// digestInput is the canonical form that the item digest covers. The contract version is not in it:
// two contracts that are equivalent give equal digests. Empty parts are left out, so adding a field
// to a contract does not change the digest of an item that lacks the field.
type digestInput struct {
	Kind           Kind                `json:"kind"`
	Fields         map[string][]string `json:"fields,omitempty"`
	Implementation []RefDigest         `json:"implementation,omitempty"`
	Verification   []RefDigest         `json:"verification,omitempty"`
}

// Digest gives `sha256:<hex>` of the contract fields of the item and the file digests in refs.
// Facets outside the contract, the identity, and the redaction records do not enter it. refs are
// sorted and de-duplicated by role and path.
func (c Contract) Digest(item Item, refs []RefDigest) string { //nolint:gocritic // Item is a value type that FactSources return in slices.
	input := digestInput{Kind: item.Kind, Fields: map[string][]string{}}

	for _, name := range c.Fields[item.Kind] {
		if set := sortedSet(item.Facets[name]); len(set) > 0 {
			input.Fields[name] = set
		}
	}

	sorted := slices.Clone(refs)
	slices.SortFunc(sorted, func(a, b RefDigest) int {
		return cmp.Or(canonjson.CompareUTF16(string(a.Role), string(b.Role)), canonjson.CompareUTF16(a.Path, b.Path))
	})
	sorted = slices.CompactFunc(sorted, func(a, b RefDigest) bool { return a == b })

	for _, ref := range sorted {
		if ref.Role == RefVerification {
			input.Verification = append(input.Verification, ref)
		} else {
			input.Implementation = append(input.Implementation, ref)
		}
	}

	data, err := canonjson.Marshal(input)
	if err != nil {
		// digestInput holds strings and slices only; encoding cannot fail.
		panic(fmt.Errorf("impact: digest input: %w", err))
	}

	return digest.OfBytes(data)
}

// sortedSet sorts values in UTF-16 order and removes duplicates. It never returns nil.
func sortedSet(values []string) []string {
	out := slices.Clone(values)
	if out == nil {
		return []string{}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return slices.Compact(out)
}

// compareKey orders by kind, then identity, both in UTF-16 order.
func compareKey(kindA Kind, identityA string, kindB Kind, identityB string) int {
	return cmp.Or(
		canonjson.CompareUTF16(string(kindA), string(kindB)),
		canonjson.CompareUTF16(identityA, identityB),
	)
}
