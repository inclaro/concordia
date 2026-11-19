package observation_test

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

const (
	cbmProfileID     = "cbm-0.12.1"
	recordingsDir    = "../../test/fixtures/cbm-recordings"
	discriminatorsGo = "../../test/golden/infra/native-discriminators.json"
)

func cbmProfile(t *testing.T) *observation.CapabilityProfile {
	t.Helper()

	p, ok := observation.ProfileFor("0.12.1", observation.AdapterSchema)
	if !ok || p.ID != cbmProfileID {
		t.Fatalf("no embedded profile %s for the adapter schema %s", cbmProfileID, observation.AdapterSchema)
	}

	return p
}

// discriminatorGoldenEdgeType is an edge type of the discriminator golden file.
type discriminatorGoldenEdgeType struct {
	Discriminators map[string]map[string]int `json:"discriminators"`
	Properties     []string                  `json:"properties"`
}

// readDiscriminatorGolden reads the edge types of the discriminator golden file of the infra recording.
func readDiscriminatorGolden(t *testing.T) map[string]discriminatorGoldenEdgeType {
	t.Helper()

	data, err := os.ReadFile(discriminatorsGo)
	if err != nil {
		t.Fatal(err)
	}

	var golden struct {
		EdgeTypes map[string]discriminatorGoldenEdgeType `json:"edgeTypes"`
	}

	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}

	return golden.EdgeTypes
}

// discriminatorValuesOf lists the observed values of each discriminator property.
func discriminatorValuesOf(discriminators map[string]map[string]int) map[string][]string {
	out := map[string][]string{}

	for prop, vs := range discriminators {
		for v := range vs {
			out[prop] = append(out[prop], v)
		}
	}

	return out
}

// goldenSchema reads the observed native edge types and properties of the infra recording.
func goldenSchema(t *testing.T) (schema observation.GraphSchema, values map[string]map[string][]string) {
	t.Helper()

	schema = observation.GraphSchema{}
	values = map[string]map[string][]string{}

	for name, et := range readDiscriminatorGolden(t) {
		schema.EdgeTypes = append(schema.EdgeTypes, observation.SchemaEdgeType{Type: name, Properties: et.Properties, Count: 1})
		values[name] = discriminatorValuesOf(et.Discriminators)
	}

	return schema, values
}

func TestCbmProfileHasEveryFamily(t *testing.T) {
	p := cbmProfile(t)

	for _, name := range observation.FamilyNames() {
		if _, ok := p.Family(name); !ok {
			t.Errorf("profile has no family %s", name)
		}
	}

	for _, f := range p.Families {
		if !slices.Contains(observation.FamilyNames(), f.Family) {
			t.Errorf("profile family %s is no canonical kind or property family", f.Family)
		}
	}

	requireKindFamiliesInProfile(t, p)

	for _, kind := range []observation.CanonicalEdgeKind{observation.KindUnsupported, observation.KindAmbiguous} {
		if _, ok := observation.FamilyForKind(kind); ok {
			t.Errorf("kind %s must have no family", kind)
		}
	}
}

// requireKindFamiliesInProfile fails for a canonical kind that has a family which the profile lacks.
func requireKindFamiliesInProfile(t *testing.T, p *observation.CapabilityProfile) {
	t.Helper()

	for _, kind := range observation.CanonicalEdgeKinds() {
		name, ok := observation.FamilyForKind(kind)
		if !ok {
			continue
		}

		if _, found := p.Family(name); !found {
			t.Errorf("kind %s: family %s is not in the profile", kind, name)
		}
	}
}

// wantCbmLossModes are the loss modes that the profile declares for a family.
var wantCbmLossModes = map[string][]string{
	"index-result":                  {"default-excluded-directory", "raster-not-indexed", "parse-partial"},
	"uses-image":                    {"merged-occurrences"},
	"container-occurrence-versions": {"merged-occurrences"},
	"references-secret":             {"target-not-declared"},
	"selects-workload":              {"selector-join-directory-scoped", "selector-join-one-of-competing", "empty-selector", "unsupported-selector-expression"},
	"policy-selects-workload":       {"selector-join-directory-scoped", "selector-join-one-of-competing", "empty-selector", "unsupported-selector-expression"},
	"disruption-policy-target":      {"selector-join-directory-scoped", "selector-join-one-of-competing", "empty-selector", "unsupported-selector-expression"},
	"routes-to":                     {"route-identity-unscoped"},
	"deploys-artifact":              {"deploy-target-is-file"},
	"built-from":                    {"build-link-needs-build-command"},
	"base-image":                    {"build-link-needs-build-command"},
	"copies-from-image":             {"build-link-needs-build-command"},
	"overrides":                     {"override-unsignalled"},
	"rendered-templates":            {"unrendered-template"},
	"deployment-workload-set":       {"default-excluded-directory", "parse-partial", "unrendered-template", "override-unsignalled"},
	"dockerfile-set":                {"default-excluded-directory", "parse-partial"},
}

func TestCbmProfileLossModesAndMaturity(t *testing.T) {
	p := cbmProfile(t)

	for family, modes := range wantCbmLossModes {
		requireFamilyLossModes(t, p, family, modes)
	}

	requireEveryObservedLossModeUsed(t, p)

	if p.Level("configures-key-symbol") != observation.SupportUnsupported {
		t.Errorf("configures-key-symbol level = %s", p.Level("configures-key-symbol"))
	}

	for _, name := range []string{"uses-type", "constructs", "calls-grpc", "calls-graphql", "calls-trpc"} {
		if p.Level(name) != observation.SupportUnsupported {
			t.Errorf("%s must be unsupported, is %s", name, p.Level(name))
		}
	}

	requireOnlyCodeFamiliesNonAdvisory(t, p)
}

// requireFamilyLossModes fails unless the family declares each of the loss modes.
func requireFamilyLossModes(t *testing.T, p *observation.CapabilityProfile, family string, modes []string) {
	t.Helper()

	f, ok := p.Family(family)
	if !ok {
		t.Fatalf("no family %s", family)
	}

	for _, mode := range modes {
		if !slices.Contains(f.LossModes, mode) {
			t.Errorf("family %s lacks loss mode %s (has %v)", family, mode, f.LossModes)
		}
	}
}

// requireEveryObservedLossModeUsed fails for an observed loss mode that no family of the profile declares.
func requireEveryObservedLossModeUsed(t *testing.T, p *observation.CapabilityProfile) {
	t.Helper()

	for _, mode := range []string{
		"default-excluded-directory", "raster-not-indexed", "parse-partial", "merged-occurrences", "target-not-declared",
		"selector-join-directory-scoped", "selector-join-one-of-competing", "empty-selector", "unsupported-selector-expression",
		"route-identity-unscoped", "deploy-target-is-file", "build-link-needs-build-command", "override-unsignalled", "unrendered-template",
	} {
		if !slices.ContainsFunc(p.Families, func(f observation.FamilySupport) bool { return slices.Contains(f.LossModes, mode) }) {
			t.Errorf("loss mode %s appears in no family", mode)
		}
	}
}

// requireOnlyCodeFamiliesNonAdvisory fails for a family that is stable without being a code family.
func requireOnlyCodeFamiliesNonAdvisory(t *testing.T, p *observation.CapabilityProfile) {
	t.Helper()

	codes := map[string]bool{}
	for _, k := range observation.CodeKinds() {
		codes[string(k)] = true
	}

	for i := range p.Families {
		f := &p.Families[i]
		if !codes[f.Family] && f.Maturity != observation.MaturityAdvisory {
			t.Errorf("family %s: only code families may be stable, got %s", f.Family, f.Maturity)
		}
	}
}

func TestCbmProfileFixturesExist(t *testing.T) {
	if err := cbmProfile(t).CheckFixtures(os.DirFS(recordingsDir)); err != nil {
		t.Fatal(err)
	}

	// Advisory families name their fixtures too; those must exist in this repository.
	for _, f := range cbmProfile(t).Families {
		for _, fx := range f.Fixtures {
			if _, err := os.Stat(recordingsDir + "/" + fx); err != nil {
				t.Errorf("family %s: fixture %s: %v", f.Family, fx, err)
			}
		}
	}
}

// The native edges and discriminator values of a family that the infra
// recording covers are the ones that the recording shows.
func TestCbmProfileMatchesObservedDiscriminators(t *testing.T) {
	_, observed := goldenSchema(t)

	for _, f := range cbmProfile(t).Families {
		if !slices.Contains(f.Fixtures, "infra.json") {
			continue
		}

		for _, e := range f.NativeEdges {
			requireNativeEdgeObserved(t, f.Family, &e, observed)
		}
	}
}

// requireNativeEdgeObserved fails unless the recording shows the native edge and each discriminator value of it.
func requireNativeEdgeObserved(t *testing.T, family string, e *observation.NativeEdge, observed map[string]map[string][]string) {
	t.Helper()

	props, ok := observed[e.Type]
	if !ok {
		t.Errorf("family %s: native edge %s is not in the recording", family, e.Type)

		return
	}

	for prop, values := range e.Discriminators {
		for _, v := range values {
			if !slices.Contains(props[prop], v) {
				t.Errorf("family %s: %s.%s=%s is not observed (%v)", family, e.Type, prop, v, props[prop])
			}
		}
	}
}

func TestVerifyProfile(t *testing.T) {
	p := cbmProfile(t)
	schema, _ := goldenSchema(t)

	t.Run("graph that matches the profile keeps every level", func(t *testing.T) { verifyProfileCaseMatchingGraph(t, p, schema) })
	t.Run("a graph without DEPLOYS: family not present in this graph, not unsupported", func(t *testing.T) { verifyProfileCaseWithoutDeploys(t, p, schema) })
	t.Run("a code-only graph gives no diagnostic and no infrastructure family is present", func(t *testing.T) { verifyProfileCaseCodeOnly(t, p) })
	t.Run("a missing required property downgrades the family to partial with a CO-OBS-017 warning", func(t *testing.T) { verifyProfileCaseMissingProperty(t, p, schema) })
}

func verifyProfileCaseMatchingGraph(t *testing.T, p *observation.CapabilityProfile, schema observation.GraphSchema) {
	t.Helper()

	got := observation.VerifyProfile(p, schema)
	if len(got.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", got.Diagnostics)
	}

	for i := range p.Families {
		f := &p.Families[i]

		state := got.Families[f.Family]
		if state.Level != f.Level || len(state.MissingProperties) != 0 {
			t.Errorf("%s = %+v, declared %s", f.Family, state, f.Level)
		}

		infra := slices.ContainsFunc(f.NativeEdges, func(e observation.NativeEdge) bool { return e.Type == "DEPLOYS" })
		if infra && state.NotPresent {
			t.Errorf("%s must be present", f.Family)
		}
	}
}

// schemaWithoutEdgeType is the schema without the edge type.
func schemaWithoutEdgeType(schema observation.GraphSchema, edgeType string) observation.GraphSchema {
	var trimmed observation.GraphSchema

	for _, et := range schema.EdgeTypes {
		if et.Type != edgeType {
			trimmed.EdgeTypes = append(trimmed.EdgeTypes, et)
		}
	}

	return trimmed
}

// schemaWithoutProperty is the schema without the property of the edge type.
func schemaWithoutProperty(schema observation.GraphSchema, edgeType, property string) observation.GraphSchema {
	var changed observation.GraphSchema

	for _, et := range schema.EdgeTypes {
		if et.Type == edgeType {
			et.Properties = slices.DeleteFunc(slices.Clone(et.Properties), func(s string) bool { return s == property })
		}

		changed.EdgeTypes = append(changed.EdgeTypes, et)
	}

	return changed
}

func verifyProfileCaseWithoutDeploys(t *testing.T, p *observation.CapabilityProfile, schema observation.GraphSchema) {
	t.Helper()

	got := observation.VerifyProfile(p, schemaWithoutEdgeType(schema, "DEPLOYS"))

	state := got.Families["deploys-artifact"]
	if !state.NotPresent || state.Level != observation.SupportSupported {
		t.Fatalf("deploys-artifact = %+v", state)
	}

	if len(got.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", got.Diagnostics)
	}
}

func verifyProfileCaseCodeOnly(t *testing.T, p *observation.CapabilityProfile) {
	t.Helper()

	code := observation.GraphSchema{EdgeTypes: []observation.SchemaEdgeType{
		{Type: "CALLS", Count: 5, Properties: []string{"callee", "confidence", "line", "strategy"}},
		{Type: "USAGE", Count: 3, Properties: []string{"callee", "line"}},
		{Type: "IMPORTS", Count: 2, Properties: []string{"line", "local_name"}},
		{Type: "DEFINES", Count: 9},
	}}

	got := observation.VerifyProfile(p, code)
	if len(got.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", got.Diagnostics)
	}

	for _, name := range []string{"uses-image", "references-secret", "scaling-target", "container-occurrence-versions", "infrastructure-dependency"} {
		state := got.Families[name]
		intact := len(state.MissingProperties) == 0 && state.Level == p.Level(name)

		if !state.NotPresent || !intact {
			t.Errorf("%s = %+v", name, state)
		}
	}
}

func verifyProfileCaseMissingProperty(t *testing.T, p *observation.CapabilityProfile, schema observation.GraphSchema) {
	t.Helper()

	got := observation.VerifyProfile(p, schemaWithoutProperty(schema, "DEPENDS_ON", "via"))

	for _, name := range []string{"base-image", "built-from", "copies-from-image"} {
		state := got.Families[name]
		if state.Level != observation.SupportPartial || !slices.Equal(state.MissingProperties, []string{"via"}) {
			t.Errorf("%s = %+v", name, state)
		}
	}

	if len(got.Diagnostics) == 0 {
		t.Fatal("no diagnostic")
	}

	for _, d := range got.Diagnostics {
		if d.Code != "CO-OBS-017" || !strings.Contains(d.Message, "via") {
			t.Errorf("diagnostic = %+v", d)
		}
	}
}

func TestCapabilityRegressions(t *testing.T) {
	p := cbmProfile(t)
	declared := observation.DeclaredFamilies(p)

	if got := observation.CapabilityRegressions(p, p.Ref(), declared); len(got) != 0 {
		t.Fatalf("equal profile gave %v", got)
	}

	if got := observation.CapabilityRegressions(nil, p.Ref(), declared); len(got) != 0 {
		t.Fatalf("no lock profile gave %v", got)
	}

	requireRoutesToRegression(t, p, declared)

	// A run with no profile loses every supported family.
	none := observation.CapabilityRegressions(p, nil, observation.NoProfileFamilies())
	if len(none) == 0 || slices.ContainsFunc(none, func(r observation.CapabilityRegression) bool { return r.CurrentDigest != "" }) {
		t.Fatalf("no-profile regressions = %d", len(none))
	}
}

// regressionFields lists the family, the levels, and the digests of a regression.
func regressionFields(r *observation.CapabilityRegression) []any {
	return []any{r.Family, r.Was, r.Now, r.LockDigest, r.CurrentDigest}
}

// requireRoutesToRegression fails unless routes-to, supported in a lock profile and partial now, is the one regression.
func requireRoutesToRegression(t *testing.T, p *observation.CapabilityProfile, declared map[string]observation.FamilyState) {
	t.Helper()

	locked := *p
	locked.Digest = "sha256:lock"
	locked.Families = slices.Clone(p.Families)

	i := slices.IndexFunc(locked.Families, func(f observation.FamilySupport) bool { return f.Family == "routes-to" })
	locked.Families[i].Level = observation.SupportSupported

	got := observation.CapabilityRegressions(&locked, p.Ref(), declared)
	want := []any{"routes-to", observation.SupportSupported, observation.SupportPartial, "sha256:lock", p.Digest}

	if len(got) != 1 || !reflect.DeepEqual(regressionFields(&got[0]), want) {
		t.Fatalf("regressions = %+v", got)
	}
}

// carriesCbmProfile says whether the capabilities carry the embedded profile and no diagnostic.
func carriesCbmProfile(caps *observation.Capabilities) bool {
	if caps.Profile == nil || caps.Profile.ID != cbmProfileID {
		return false
	}

	return strings.HasPrefix(caps.Profile.Digest, "sha256:") && len(caps.Diagnostics) == 0
}

// isProfilelessCaps says whether the capabilities have no profile, the one
// CO-OBS-017 diagnostic, the code families supported, and the infrastructure families unsupported.
func isProfilelessCaps(caps *observation.Capabilities) bool {
	if caps.Profile != nil || len(caps.Diagnostics) != 1 || caps.Diagnostics[0].Code != "CO-OBS-017" {
		return false
	}

	return caps.Families["calls"].Level == observation.SupportSupported && caps.Families["uses-image"].Level == observation.SupportUnsupported
}

func TestProviderCapabilitiesCarryTheProfile(t *testing.T) {
	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: observation.NewFakeClient(nil, "0.12.1"), ExecutableVersion: "0.12.1"})

	caps := provider.Capabilities(t.Context())
	if !carriesCbmProfile(&caps) {
		t.Fatalf("caps = %+v", caps)
	}

	if caps.Families["routes-to"].Level != observation.SupportPartial || caps.Families["configures-key-symbol"].Level != observation.SupportUnsupported {
		t.Fatalf("families = %+v", caps.Families)
	}

	bare := observation.NewCbmProvider(observation.CbmProviderOptions{Client: observation.NewFakeClient(nil, "0.12.0"), ExecutableVersion: "0.12.0"})

	caps = bare.Capabilities(t.Context())
	if !isProfilelessCaps(&caps) {
		t.Fatalf("no-profile caps = %+v", caps)
	}
}

// isAdvisoryDeploymentFamily says whether the family is supported, advisory, and recorded by infra.json alone.
func isAdvisoryDeploymentFamily(f *observation.FamilySupport) bool {
	if f.Level != observation.SupportSupported || f.Maturity != observation.MaturityAdvisory {
		return false
	}

	return slices.Equal(f.Fixtures, []string{"infra.json"})
}

// The deployment families are declared in the one profile of CBM 0.12.1: `placed-in` is derived (no native edge),
// and the two property families are supported, with their loss modes applying through gaps.
func TestCbmProfileDeclaresTheDeploymentFamilies(t *testing.T) {
	p := cbmProfile(t)

	for _, name := range []string{"placed-in", "deployment-workload-set", "dockerfile-set"} {
		f, ok := p.Family(name)
		if !ok {
			t.Fatalf("no family %s", name)
		}

		if !isAdvisoryDeploymentFamily(f) {
			t.Errorf("family %s = level %s, maturity %s, fixtures %v", name, f.Level, f.Maturity, f.Fixtures)
		}
	}

	if f, _ := p.Family("placed-in"); len(f.NativeEdges) != 0 || len(f.LossModes) != 0 {
		t.Errorf("placed-in is derived: native edges %v, loss modes %v", f.NativeEdges, f.LossModes)
	}

	// container-occurrence-versions is as the foundation declared it.
	occurrences, _ := p.Family(observation.FamilyContainerOccurrenceVersions)
	if occurrences.Level != observation.SupportPartial || !isMergedOccurrencesOnly(occurrences) {
		t.Errorf("container-occurrence-versions = %+v", occurrences)
	}

	// The digest is the one of the content: it differs from the digest of the profile before the deployment families.
	if p.Digest == "sha256:1ec34e18f13f92e6629cbb7ed313eccb46583a6e2ac035208b9003ab2a87110f" {
		t.Error("the profile digest did not change with the new families")
	}
}

// isMergedOccurrencesOnly says whether the family is advisory with the loss mode merged-occurrences alone.
func isMergedOccurrencesOnly(f *observation.FamilySupport) bool {
	return f.Maturity == observation.MaturityAdvisory && slices.Equal(f.LossModes, []string{"merged-occurrences"})
}

// capabilityPageRows reads the table rows of the capability profiles page by family.
func capabilityPageRows(t *testing.T) map[string]string {
	t.Helper()

	page, err := os.ReadFile("../../docs/reference/capability-profiles.md")
	if err != nil {
		t.Fatal(err)
	}

	rows := map[string]string{}

	for line := range strings.SplitSeq(string(page), "\n") {
		if cells := strings.Split(line, "|"); len(cells) > 5 && strings.HasPrefix(strings.TrimSpace(cells[1]), "`") {
			rows[strings.Trim(strings.TrimSpace(cells[1]), "`")] = line
		}
	}

	return rows
}

// The page docs/reference/capability-profiles.md has a row for every family of the profile, and the row names
// every loss mode that the profile declares for the family.
func TestCapabilityProfilesPageListsEveryFamilyAndLossMode(t *testing.T) {
	rows := capabilityPageRows(t)

	for _, f := range cbmProfile(t).Families {
		row, ok := rows[f.Family]
		if !ok {
			t.Errorf("the page has no row for family %s", f.Family)

			continue
		}

		for _, mode := range f.LossModes {
			if !strings.Contains(row, "`"+mode+"`") {
				t.Errorf("the row of %s does not name the loss mode %s: %s", f.Family, mode, row)
			}
		}
	}
}
