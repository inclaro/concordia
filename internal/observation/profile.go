package observation

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
)

// profileSuffix ends the file name of each capability profile.
const profileSuffix = ".profile.yaml"

// profilesDir is the directory of the embedded profiles.
const profilesDir = "profiles"

// embeddedProfileFS holds the shipped capability profiles. The `all:` prefix
// keeps the directory embeddable when it holds no profile file yet.
//
//go:embed all:profiles
var embeddedProfileFS embed.FS

// SupportLevel says how well a provider build supplies one family.
type SupportLevel string

// Support levels.
const (
	// SupportSupported means the family is supplied and Concordia relies on it.
	SupportSupported SupportLevel = "supported"
	// SupportPartial means the family is supplied with limits that the loss modes name.
	SupportPartial SupportLevel = "partial"
	// SupportUnsupported means the provider build does not supply the family.
	SupportUnsupported SupportLevel = "unsupported"
)

// FamilyMaturity says whether fixtures back a family well enough for enforcement.
type FamilyMaturity string

// Family maturities.
const (
	// MaturityAdvisory means no mandatory rule can depend on the family.
	MaturityAdvisory FamilyMaturity = "advisory"
	// MaturityStable means the fixtures of the family pass; the family names them.
	MaturityStable FamilyMaturity = "stable"
)

// NativeEdge describes the native edges that make a family: the native type
// and the discriminating properties that select it.
type NativeEdge struct {
	// Discriminators maps a native property name to the values that select the family.
	Discriminators map[string][]string `yaml:"discriminators,omitempty" json:"discriminators,omitempty"`
	// Type is the native edge type, for example `DEPLOYS`.
	Type string `yaml:"type" json:"type"`
	// Present are native properties that the edge must have, for example `repository`.
	Present []string `yaml:"present,omitempty" json:"present,omitempty"`
	// Absent are native properties that the edge must not have, for example `via`.
	Absent []string `yaml:"absent,omitempty" json:"absent,omitempty"`
	// SourceFiles are the file names that the source node must have, for example `kustomization.yaml`.
	SourceFiles []string `yaml:"sourceFiles,omitempty" json:"sourceFiles,omitempty"`
	// TargetKinds are the values of the target node `kind` that select the family.
	TargetKinds []string `yaml:"targetKinds,omitempty" json:"targetKinds,omitempty"`
}

// FamilySupport is the support that a profile states for one family: a
// relationship family (a canonical kind), a property family, or a syntax.
type FamilySupport struct {
	// Family is the family name, for example `deploys-artifact`.
	Family string `yaml:"family" json:"family"`
	// Level is the support level.
	Level SupportLevel `yaml:"level" json:"level"`
	// Maturity is `advisory` or `stable`.
	Maturity FamilyMaturity `yaml:"maturity" json:"maturity"`
	// LossModes are the known loss modes, for example `merged-occurrences`.
	LossModes []string `yaml:"lossModes,omitempty" json:"lossModes,omitempty"`
	// NativeEdges are the native edges and discriminators of the family.
	NativeEdges []NativeEdge `yaml:"nativeEdges,omitempty" json:"nativeEdges,omitempty"`
	// RequiredProperties are the native edge properties that the family needs.
	// The graph schema must have them, or the family is only partial.
	RequiredProperties []string `yaml:"requiredProperties,omitempty" json:"requiredProperties,omitempty"`
	// Fixtures are the recording files (names under `test/fixtures/cbm-recordings/`) that validate the family.
	Fixtures []string `yaml:"fixtures,omitempty" json:"fixtures,omitempty"`
}

// ProfileBuild pins a profile to a provider build and adapter schema.
type ProfileBuild struct {
	// Version is the executable version, for example `0.12.1`.
	Version string `yaml:"version" json:"version"`
	// AdapterSchema is the adapter schema, for example `concordia-cbm/v2`.
	AdapterSchema string `yaml:"adapterSchema" json:"adapterSchema"`
}

// CapabilityProfile says what evidence Concordia may rely on from one
// provider build. It is data only.
type CapabilityProfile struct {
	ID       string       `yaml:"id" json:"id"`
	Provider string       `yaml:"provider" json:"provider"`
	Build    ProfileBuild `yaml:"build" json:"build"`
	// Digest is "sha256:<hex>" of the canonical JSON of the profile without
	// the digest. The loader sets it; a value in the file is refused.
	Digest   string          `yaml:"digest,omitempty" json:"digest,omitempty"`
	Families []FamilySupport `yaml:"families" json:"families"`
}

// Family gives the support entry of a family. The bool is false when the profile has none.
func (p *CapabilityProfile) Family(name string) (*FamilySupport, bool) {
	for i := range p.Families {
		if p.Families[i].Family == name {
			return &p.Families[i], true
		}
	}

	return nil, false
}

// Level gives the support level of a family. A family that the profile does
// not list is unsupported.
func (p *CapabilityProfile) Level(name string) SupportLevel {
	if f, ok := p.Family(name); ok {
		return f.Level
	}

	return SupportUnsupported
}

// ComputeDigest gives the content digest of the profile: "sha256:<hex>" of
// the canonical JSON of the profile with the digest cleared, families sorted
// by name and every set sorted, so a change of order alone does not change it.
func (p *CapabilityProfile) ComputeDigest() (string, error) {
	c := p.canonical()
	c.Digest = ""

	data, err := canonjson.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("profile %s digest: %w", p.ID, err)
	}

	return digest.OfBytes(data), nil
}

// canonical gives a deep copy with every set sorted.
func (p *CapabilityProfile) canonical() *CapabilityProfile {
	out := *p
	out.Families = make([]FamilySupport, len(p.Families))

	for i := range p.Families {
		family := p.Families[i]
		family.LossModes = sortedCopy(family.LossModes)
		family.RequiredProperties = sortedCopy(family.RequiredProperties)
		family.Fixtures = sortedCopy(family.Fixtures)
		family.NativeEdges = canonicalEdges(family.NativeEdges)
		out.Families[i] = family
	}

	slices.SortFunc(out.Families, func(a, b FamilySupport) int { return canonjson.CompareUTF16(a.Family, b.Family) })

	return &out
}

func canonicalEdges(edges []NativeEdge) []NativeEdge {
	if len(edges) == 0 {
		return nil
	}

	out := make([]NativeEdge, len(edges))

	for i, edge := range edges {
		edge.Present = sortedCopy(edge.Present)
		edge.Absent = sortedCopy(edge.Absent)
		edge.SourceFiles = sortedCopy(edge.SourceFiles)
		edge.TargetKinds = sortedCopy(edge.TargetKinds)

		if len(edge.Discriminators) > 0 {
			discriminators := make(map[string][]string, len(edge.Discriminators))
			for key, values := range edge.Discriminators {
				discriminators[key] = sortedCopy(values)
			}

			edge.Discriminators = discriminators
		} else {
			edge.Discriminators = nil
		}

		out[i] = edge
	}

	slices.SortStableFunc(out, func(a, b NativeEdge) int {
		if c := canonjson.CompareUTF16(a.Type, b.Type); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(canonjson.MustString(a), canonjson.MustString(b))
	})

	return out
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	out := slices.Clone(in)
	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// ParseProfile reads one profile from YAML, checks it, and sets its digest.
// Unknown keys, duplicate families, an unknown level or maturity, a `digest`
// key, and a `stable` family without a fixture are errors.
func ParseProfile(data []byte) (*CapabilityProfile, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var profile CapabilityProfile
	if err := dec.Decode(&profile); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("profile is empty")
		}

		return nil, fmt.Errorf("profile: %w", err)
	}

	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("profile: more than one YAML document")
	}

	if profile.Digest != "" {
		return nil, errors.New("profile: the digest is computed, not written")
	}

	if err := profile.validate(); err != nil {
		return nil, err
	}

	sum, err := profile.ComputeDigest()
	if err != nil {
		return nil, err
	}

	profile.Digest = sum

	return &profile, nil
}

func (p *CapabilityProfile) validate() error {
	for _, field := range []struct{ name, value string }{
		{"id", p.ID}, {"provider", p.Provider}, {"build.version", p.Build.Version}, {"build.adapterSchema", p.Build.AdapterSchema},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("profile %q: %s is required", p.ID, field.name)
		}
	}

	if len(p.Families) == 0 {
		return fmt.Errorf("profile %s: at least one family is required", p.ID)
	}

	seen := map[string]bool{}

	for i := range p.Families {
		family := &p.Families[i]

		if strings.TrimSpace(family.Family) == "" {
			return fmt.Errorf("profile %s: family %d has no name", p.ID, i+1)
		}

		if seen[family.Family] {
			return fmt.Errorf("profile %s: family %s is listed twice", p.ID, family.Family)
		}

		seen[family.Family] = true

		if err := family.validate(p.ID); err != nil {
			return err
		}
	}

	return nil
}

func (f *FamilySupport) validate(profileID string) error {
	switch f.Level {
	case SupportSupported, SupportPartial, SupportUnsupported:
	default:
		return fmt.Errorf("profile %s: family %s: level %q is not supported, partial, or unsupported", profileID, f.Family, f.Level)
	}

	switch f.Maturity {
	case MaturityAdvisory, MaturityStable:
	default:
		return fmt.Errorf("profile %s: family %s: maturity %q is not advisory or stable", profileID, f.Family, f.Maturity)
	}

	if f.Maturity == MaturityStable && len(f.Fixtures) == 0 {
		return fmt.Errorf("profile %s: family %s is stable and names no fixture", profileID, f.Family)
	}

	for _, edge := range f.NativeEdges {
		if strings.TrimSpace(edge.Type) == "" {
			return fmt.Errorf("profile %s: family %s: a native edge has no type", profileID, f.Family)
		}
	}

	for _, list := range [][]string{f.LossModes, f.RequiredProperties, f.Fixtures} {
		for _, item := range list {
			if strings.TrimSpace(item) == "" {
				return fmt.Errorf("profile %s: family %s: an empty list item", profileID, f.Family)
			}
		}
	}

	return nil
}

// CheckFixtures checks that every fixture of every `stable` family exists in
// recordings, the file tree of `test/fixtures/cbm-recordings/`. Advisory
// families may name fixtures that do not exist yet. The error names each
// missing fixture.
func (p *CapabilityProfile) CheckFixtures(recordings fs.FS) error {
	var missing []string

	for i := range p.Families {
		family := &p.Families[i]
		if family.Maturity != MaturityStable {
			continue
		}

		for _, name := range family.Fixtures {
			if info, err := fs.Stat(recordings, name); err != nil || info.IsDir() {
				missing = append(missing, fmt.Sprintf("%s (family %s)", name, family.Family))
			}
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("profile %s: stable families name missing fixtures: %s", p.ID, strings.Join(missing, ", "))
	}

	return nil
}

// LoadProfiles reads every `*.profile.yaml` file of dir in fsys, in file name
// order. Two profiles for the same provider build and adapter schema, or with
// the same id, are an error.
func LoadProfiles(fsys fs.FS, dir string) ([]*CapabilityProfile, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read profiles: %w", err)
	}

	var out []*CapabilityProfile

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), profileSuffix) {
			continue
		}

		name := path.Join(dir, entry.Name())

		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read profile %s: %w", name, err)
		}

		profile, err := ParseProfile(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}

		for _, other := range out {
			if other.ID == profile.ID {
				return nil, fmt.Errorf("%s: profile id %s is already used by another file", name, profile.ID)
			}

			if other.Provider == profile.Provider && other.Build == profile.Build {
				return nil, fmt.Errorf("%s: another profile pins %s %s with adapter schema %s", name, profile.Provider, profile.Build.Version, profile.Build.AdapterSchema)
			}
		}

		out = append(out, profile)
	}

	return out, nil
}

// FindProfile gives the profile of profiles that is pinned to exactly this
// provider version and adapter schema. There is no range or prefix match.
func FindProfile(profiles []*CapabilityProfile, version, adapterSchema string) (*CapabilityProfile, bool) {
	for _, p := range profiles {
		if p.Build.Version == version && p.Build.AdapterSchema == adapterSchema {
			return p, true
		}
	}

	return nil, false
}

var embeddedProfiles = sync.OnceValues(func() ([]*CapabilityProfile, error) {
	return LoadProfiles(embeddedProfileFS, profilesDir)
})

// EmbeddedProfiles gives the capability profiles that ship with Concordia.
// The result is shared; callers MUST NOT change it. A malformed shipped
// profile is an error (a test keeps that from reaching a release).
func EmbeddedProfiles() ([]*CapabilityProfile, error) {
	return embeddedProfiles()
}

// ProfileFor gives the shipped profile for exactly this provider version and
// adapter schema. The bool is false when no profile applies. A malformed
// shipped profile panics, because it is a build defect that the profile
// tests catch.
func ProfileFor(version, adapterSchema string) (*CapabilityProfile, bool) {
	profiles, err := embeddedProfiles()
	if err != nil {
		panic(fmt.Sprintf("observation: embedded capability profiles: %v", err))
	}

	return FindProfile(profiles, version, adapterSchema)
}
