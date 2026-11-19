package observation

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// Property families of a profile that no canonical edge kind names.
const (
	FamilyContainerOccurrenceVersions = "container-occurrence-versions"
	FamilyRenderedTemplates           = "rendered-templates"
	FamilyIndexResult                 = "index-result"
	FamilyDeploymentWorkloadSet       = "deployment-workload-set"
	FamilyDockerfileSet               = "dockerfile-set"
	FamilyDocumentationMetadata       = "documentation-metadata"
	FamilyDiagramIdentity             = "diagram-identity"
)

// propertyFamilies are the profile families that are not canonical edge kinds.
var propertyFamilies = []string{
	FamilyContainerOccurrenceVersions,
	FamilyRenderedTemplates,
	FamilyIndexResult,
	FamilyDeploymentWorkloadSet,
	FamilyDockerfileSet,
	FamilyDocumentationMetadata,
	FamilyDiagramIdentity,
}

// FamilyForKind gives the profile family of a canonical edge kind. The family
// name equals the kind name. The bool is false for the kinds that keep
// native evidence uninterpreted.
func FamilyForKind(kind CanonicalEdgeKind) (string, bool) {
	if kind == KindUnsupported || kind == KindAmbiguous {
		return "", false
	}

	if !slices.Contains(canonicalEdgeKinds, kind) {
		return "", false
	}

	return string(kind), true
}

// FamilyNames gives every family name that a profile can state: one for each
// canonical edge kind that has a family, then the property families.
func FamilyNames() []string {
	names := make([]string, 0, len(canonicalEdgeKinds)+len(propertyFamilies))

	for _, kind := range canonicalEdgeKinds {
		if name, ok := FamilyForKind(kind); ok {
			names = append(names, name)
		}
	}

	return append(names, propertyFamilies...)
}

// ProfileRef identifies the capability profile of a run.
type ProfileRef struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Ref gives the identifier and the digest of the profile.
func (p *CapabilityProfile) Ref() *ProfileRef {
	if p == nil {
		return nil
	}

	return &ProfileRef{ID: p.ID, Digest: p.Digest}
}

// FamilyState is the support of one family after the profile is compared
// with the live graph.
type FamilyState struct {
	Level    SupportLevel   `json:"level"`
	Maturity FamilyMaturity `json:"maturity"`
	// LossModes are the known loss modes of the family.
	LossModes []string `json:"lossModes,omitempty"`
	// MissingProperties are the required properties that the graph schema lacks.
	// The family is then `partial`.
	MissingProperties []string `json:"missingProperties,omitempty"`
	// NotPresent is true when the graph has none of the native edge types of
	// the family. That is not the same as unsupported.
	NotPresent bool `json:"notPresent,omitempty"`
}

// ProfileVerification is the result of VerifyProfile.
type ProfileVerification struct {
	// Families holds one entry for each family of the profile.
	Families map[string]FamilyState
	// Diagnostics are CO-OBS-017 warnings.
	Diagnostics []diag.Diagnostic
}

// DeclaredFamilies gives the family states that the profile declares, with no
// graph comparison.
func DeclaredFamilies(profile *CapabilityProfile) map[string]FamilyState {
	out := make(map[string]FamilyState, len(profile.Families))

	for i := range profile.Families {
		f := &profile.Families[i]
		out[f.Family] = FamilyState{Level: f.Level, Maturity: f.Maturity, LossModes: slices.Clone(f.LossModes)}
	}

	return out
}

// NoProfileFamilies gives the family states of a build that has no profile:
// the code families are supplied, and every other family is unsupported.
func NoProfileFamilies() map[string]FamilyState {
	codes := map[string]bool{}

	for _, kind := range CodeKinds() {
		codes[string(kind)] = true
	}

	out := map[string]FamilyState{}

	for _, name := range FamilyNames() {
		if codes[name] {
			out[name] = FamilyState{Level: SupportSupported, Maturity: MaturityStable}
		} else {
			out[name] = FamilyState{Level: SupportUnsupported, Maturity: MaturityAdvisory}
		}
	}

	return out
}

// VerifyProfile compares the native edge types and required properties of
// the profile with the live graph schema. A family whose native edge types
// are all missing from the graph is marked NotPresent (its level stays). A
// `supported` family whose required property is missing from the graph is
// downgraded to `partial`, and a CO-OBS-017 warning names the property.
func VerifyProfile(profile *CapabilityProfile, schema GraphSchema) ProfileVerification {
	edgeProps := schemaEdgeProperties(schema)
	absentByDesign := absentPropertiesByDesign(profile)
	result := ProfileVerification{Families: make(map[string]FamilyState, len(profile.Families))}

	for i := range profile.Families {
		fam := &profile.Families[i]
		state, warning := verifyFamily(profile.ID, fam, edgeProps, absentByDesign)

		if warning != nil {
			result.Diagnostics = append(result.Diagnostics, *warning)
		}

		result.Families[fam.Family] = state
	}

	return result
}

// edgePropertySets maps an edge type to a set of property names.
type edgePropertySets map[string]map[string]bool

// add puts a property in the set of an edge type.
func (s edgePropertySets) add(edgeType, prop string) {
	if s[edgeType] == nil {
		s[edgeType] = map[string]bool{}
	}

	s[edgeType][prop] = true
}

// has says whether the set of an edge type has the property.
func (s edgePropertySets) has(edgeType, prop string) bool { return s[edgeType][prop] }

// schemaEdgeProperties maps each edge type of the schema to its property set.
func schemaEdgeProperties(schema GraphSchema) edgePropertySets {
	edgeProps := edgePropertySets{}

	for _, t := range schema.EdgeTypes {
		edgeProps[t.Type] = map[string]bool{}

		for _, p := range t.Properties {
			edgeProps.add(t.Type, p)
		}
	}

	return edgeProps
}

// absentPropertiesByDesign maps a native edge type that a stable (code)
// family lists with `absent: [prop]` to those properties. Such edges have no
// that property by design: code `USAGE` has no `ref`. A graph that lacks the
// property then has none of the edges that a family selects with that
// property, which is "not present", not a missing required property.
func absentPropertiesByDesign(profile *CapabilityProfile) edgePropertySets {
	absentByDesign := edgePropertySets{}

	for i := range profile.Families {
		if profile.Families[i].Maturity == MaturityStable {
			absentByDesign.addAbsent(profile.Families[i].NativeEdges)
		}
	}

	return absentByDesign
}

// addAbsent adds the `absent` properties of the native edges to the set of their edge types.
func (s edgePropertySets) addAbsent(edges []NativeEdge) {
	for _, e := range edges {
		for _, prop := range e.Absent {
			s.add(e.Type, prop)
		}
	}
}

// presentEdgeTypes gives the distinct native edge types of the family that the
// graph has, in profile order.
func presentEdgeTypes(fam *FamilySupport, edgeProps edgePropertySets) []string {
	var present []string

	seen := map[string]bool{}

	for _, e := range fam.NativeEdges {
		if seen[e.Type] {
			continue
		}

		seen[e.Type] = true

		if _, ok := edgeProps[e.Type]; ok {
			present = append(present, e.Type)
		}
	}

	return present
}

// verifyFamily compares one family with the graph. The diagnostic is non-nil
// when a required property is missing.
func verifyFamily(profileID string, fam *FamilySupport, edgeProps, absentByDesign edgePropertySets) (FamilyState, *diag.Diagnostic) {
	state := FamilyState{Level: fam.Level, Maturity: fam.Maturity, LossModes: slices.Clone(fam.LossModes)}
	if len(fam.NativeEdges) == 0 || fam.Level == SupportUnsupported {
		return state, nil
	}

	present := presentEdgeTypes(fam, edgeProps)
	if len(present) == 0 || isGatedByAbsentProperty(fam, present, edgeProps, absentByDesign) {
		state.NotPresent = true

		return state, nil
	}

	state.MissingProperties = missingRequiredProperties(fam, present, edgeProps)
	if len(state.MissingProperties) == 0 {
		return state, nil
	}

	if state.Level == SupportSupported {
		state.Level = SupportPartial
	}

	slices.Sort(present)

	warning := diag.New(diag.OBS017, diag.Params{
		"profile": profileID,
		paramDetail: fmt.Sprintf("family %s: required property %s is missing from %s; the family is partial",
			fam.Family, strings.Join(state.MissingProperties, ", "), strings.Join(present, ", ")),
	})

	return state, &warning
}

// isGatedByAbsentProperty says whether the family selects its edges with a discriminator property that the
// graph lacks by design: none of its edges exist.
func isGatedByAbsentProperty(fam *FamilySupport, present []string, edgeProps, absentByDesign edgePropertySets) bool {
	return slices.ContainsFunc(fam.NativeEdges, func(e NativeEdge) bool {
		return slices.Contains(present, e.Type) && hasDiscriminatorAbsentByDesign(&e, edgeProps, absentByDesign)
	})
}

// hasDiscriminatorAbsentByDesign says whether the edge is selected by a property that the graph lacks and that the edge type lacks by design.
func hasDiscriminatorAbsentByDesign(e *NativeEdge, edgeProps, absentByDesign edgePropertySets) bool {
	for prop := range e.Discriminators {
		if !edgeProps.has(e.Type, prop) && absentByDesign.has(e.Type, prop) {
			return true
		}
	}

	return false
}

// missingRequiredProperties gives the required properties of the family that none of the present edge types has.
func missingRequiredProperties(fam *FamilySupport, present []string, edgeProps edgePropertySets) []string {
	var missing []string

	for _, prop := range fam.RequiredProperties {
		if !slices.ContainsFunc(present, func(t string) bool { return edgeProps.has(t, prop) }) {
			missing = append(missing, prop)
		}
	}

	return missing
}

// MergeSchemas gives the union of graph schemas: every node label and edge
// type of any schema, with the union of the properties. Counts add up.
func MergeSchemas(schemas ...GraphSchema) GraphSchema {
	labels := map[string]*SchemaLabel{}
	types := map[string]*SchemaEdgeType{}

	for _, one := range schemas {
		for _, l := range one.NodeLabels {
			mergeSchemaLabel(labels, l)
		}

		for _, t := range one.EdgeTypes {
			mergeSchemaEdgeType(types, t)
		}
	}

	return sortedSchema(labels, types)
}

func mergeSchemaLabel(labels map[string]*SchemaLabel, l SchemaLabel) {
	got, ok := labels[l.Label]
	if !ok {
		got = &SchemaLabel{Label: l.Label}
		labels[l.Label] = got
	}

	got.Count += l.Count
	got.Properties = unionSorted(got.Properties, l.Properties)
}

func mergeSchemaEdgeType(types map[string]*SchemaEdgeType, t SchemaEdgeType) {
	got, ok := types[t.Type]
	if !ok {
		got = &SchemaEdgeType{Type: t.Type}
		types[t.Type] = got
	}

	got.Count += t.Count
	got.Properties = unionSorted(got.Properties, t.Properties)
}

// sortedSchema makes a schema of merged labels and edge types, sorted by name.
func sortedSchema(labels map[string]*SchemaLabel, types map[string]*SchemaEdgeType) GraphSchema {
	out := GraphSchema{NodeLabels: []SchemaLabel{}, EdgeTypes: []SchemaEdgeType{}}

	for _, l := range labels {
		out.NodeLabels = append(out.NodeLabels, *l)
	}

	for _, t := range types {
		out.EdgeTypes = append(out.EdgeTypes, *t)
	}

	slices.SortFunc(out.NodeLabels, func(a, b SchemaLabel) int { return strings.Compare(a.Label, b.Label) })
	slices.SortFunc(out.EdgeTypes, func(a, b SchemaEdgeType) int { return strings.Compare(a.Type, b.Type) })

	return out
}

func unionSorted(a, b []string) []string {
	out := slices.Concat(a, b)
	slices.Sort(out)

	return slices.Compact(out)
}

// CapabilityRegression is one family that the lock profile supported and the
// current profile does not.
type CapabilityRegression struct {
	Family string `json:"family"`
	// Was and Now are the support levels in the lock and in this run.
	Was SupportLevel `json:"was"`
	Now SupportLevel `json:"now"`
	// LockDigest and CurrentDigest are the profile digests of the lock and of this run.
	LockDigest    string `json:"lockDigest"`
	CurrentDigest string `json:"currentDigest"`
}

// LockedProfile gives the shipped profile that a lock names by identifier and
// digest. The bool is false when no shipped profile has both.
func LockedProfile(ref *ProfileRef) (*CapabilityProfile, bool) {
	if ref == nil {
		return nil, false
	}

	profiles, err := EmbeddedProfiles()
	if err != nil {
		return nil, false
	}

	for _, p := range profiles {
		if p.ID == ref.ID && p.Digest == ref.Digest {
			return p, true
		}
	}

	return nil, false
}

// CapabilityRegressions gives, for each family that was `supported` in the
// lock profile and is not `supported` in this run, one regression, sorted by
// family. Equal digests give none, and so does a nil lock profile (nothing
// was promised). A family that this run does not list counts as unsupported.
// current is the identity of this run's profile (nil when no profile
// applies) and currentFamilies the family states after VerifyProfile.
func CapabilityRegressions(lockProfile *CapabilityProfile, current *ProfileRef, currentFamilies map[string]FamilyState) []CapabilityRegression {
	if lockPromisesNothing(lockProfile, current) {
		return nil
	}

	check := regressionCheck{lockDigest: lockProfile.Digest, currentFamilies: currentFamilies}
	if current != nil {
		check.currentDigest = current.Digest
	}

	var out []CapabilityRegression

	for i := range lockProfile.Families {
		if regression, ok := check.of(&lockProfile.Families[i]); ok {
			out = append(out, regression)
		}
	}

	slices.SortFunc(out, func(a, b CapabilityRegression) int { return strings.Compare(a.Family, b.Family) })

	return out
}

// lockPromisesNothing says whether the lock profile cannot give a regression: there is none, or it has the digest of this run's profile.
func lockPromisesNothing(lockProfile *CapabilityProfile, current *ProfileRef) bool {
	if lockProfile == nil {
		return true
	}

	return current != nil && lockProfile.Digest == current.Digest
}

// regressionCheck compares the families of a lock profile with the family states of this run.
type regressionCheck struct {
	currentFamilies map[string]FamilyState
	lockDigest      string
	currentDigest   string
}

// of gives the regression of a lock family, false when the family was not `supported` or still is.
func (c *regressionCheck) of(was *FamilySupport) (CapabilityRegression, bool) {
	if was.Level != SupportSupported {
		return CapabilityRegression{}, false
	}

	now := SupportUnsupported
	if state, ok := c.currentFamilies[was.Family]; ok {
		now = state.Level
	}

	if now == SupportSupported {
		return CapabilityRegression{}, false
	}

	return CapabilityRegression{Family: was.Family, Was: was.Level, Now: now, LockDigest: c.lockDigest, CurrentDigest: c.currentDigest}, true
}
