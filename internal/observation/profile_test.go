package observation_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/inclaro/concordia/internal/observation"
)

const testProfileDir = "testdata/profiles"

func loadTestProfile(t *testing.T) *observation.CapabilityProfile {
	t.Helper()

	profiles, err := observation.LoadProfiles(os.DirFS("."), testProfileDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(profiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(profiles))
	}

	return profiles[0]
}

func TestLoadProfiles(t *testing.T) {
	p := loadTestProfile(t)

	obsEqual(t, "id", p.ID, "test-provider-1.2.3")
	obsEqual(t, "provider", p.Provider, "test-provider")
	obsEqual(t, "version", p.Build.Version, "1.2.3")
	obsEqual(t, "adapter schema", p.Build.AdapterSchema, "test-adapter/v1")
	obsEqual(t, "families", len(p.Families), 4)

	obsEqual(t, "level of calls", p.Level("calls"), observation.SupportSupported)
	obsEqual(t, "level of configures-key-symbol", p.Level("configures-key-symbol"), observation.SupportUnsupported)
	obsEqual(t, "level of not-listed", p.Level("not-listed"), observation.SupportUnsupported)

	_, listed := p.Family("not-listed")
	obsRequire(t, !listed, "Family found an unlisted family")

	obsRequire(t, strings.HasPrefix(p.Digest, "sha256:"), fmt.Sprintf("digest = %q", p.Digest))
	obsEqual(t, "digest length", len(p.Digest), len("sha256:")+64)
}

func TestLoadProfilesDeploysArtifactFamily(t *testing.T) {
	p := loadTestProfile(t)

	deploys, ok := p.Family("deploys-artifact")
	obsRequire(t, ok, "no deploys-artifact family")

	obsEqual(t, "level", deploys.Level, observation.SupportPartial)
	obsEqual(t, "maturity", deploys.Maturity, observation.MaturityAdvisory)
	obsDeepEqual(t, "loss modes", deploys.LossModes, []string{"deploy-target-is-file"})
	obsDeepEqual(t, "required properties", deploys.RequiredProperties, []string{"via"})

	obsEqual(t, "native edges", len(deploys.NativeEdges), 1)
	obsEqual(t, "native edge type", deploys.NativeEdges[0].Type, "DEPLOYS")
	obsEqual(t, "via discriminators", strings.Join(deploys.NativeEdges[0].Discriminators["via"], ","), "kubectl,helm,chart")
}

func TestLoadProfilesDependencyFamilyNativeEdge(t *testing.T) {
	p := loadTestProfile(t)

	dep, _ := p.Family("infrastructure-dependency")
	edge := dep.NativeEdges[0]

	obsEqual(t, "absent", edge.Absent[0], "via")
	obsEqual(t, "present", edge.Present[0], "repository")
}

func TestLoadProfilesIgnoresOtherFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"profiles/notes.txt":          {Data: []byte("not yaml: [")},
		"profiles/.gitkeep":           {},
		"profiles/sub/x.profile.yaml": {Data: []byte("bad")},
	}

	profiles, err := observation.LoadProfiles(fsys, "profiles")
	if err != nil || len(profiles) != 0 {
		t.Fatalf("profiles = %v, err = %v", profiles, err)
	}
}

const reorderedTestProfile = `id: test-provider-1.2.3
provider: test-provider
build: {adapterSchema: test-adapter/v1, version: 1.2.3}
families:
  - {family: configures-key-symbol, level: unsupported, maturity: advisory}
  - family: infrastructure-dependency
    level: supported
    maturity: advisory
    nativeEdges:
      - type: DEPENDS_ON
        present: [repository]
        absent: [via]
  - family: deploys-artifact
    level: partial
    maturity: advisory
    fixtures: [not-recorded-yet.json]
    requiredProperties: [via]
    lossModes: [deploy-target-is-file]
    nativeEdges:
      - type: DEPLOYS
        discriminators: {via: [chart, helm, kubectl]}
  - family: calls
    level: supported
    maturity: stable
    fixtures: [present.json]
    requiredProperties: [strategy]
    nativeEdges: [{type: CALLS}]
`

func testProfileText(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(testProfileDir + "/test-1.2.3.profile.yaml")
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func parseTestProfile(t *testing.T, text string) *observation.CapabilityProfile {
	t.Helper()

	p, err := observation.ParseProfile([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func TestProfileDigestIsStable(t *testing.T) {
	text := testProfileText(t)
	a, b := parseTestProfile(t, text), parseTestProfile(t, text)

	obsEqual(t, "digest of a second parse", b.Digest, a.Digest)

	recomputed, err := a.ComputeDigest()
	if err != nil {
		t.Fatal(err)
	}

	obsEqual(t, "ComputeDigest", recomputed, a.Digest)
}

func TestProfileDigestIgnoresCommentsAndOrder(t *testing.T) {
	a := parseTestProfile(t, testProfileText(t))
	c := parseTestProfile(t, reorderedTestProfile)

	obsEqual(t, "digest", c.Digest, a.Digest)
}

func TestProfileDigestChangesWithContent(t *testing.T) {
	original := testProfileText(t)
	want := parseTestProfile(t, original).Digest

	mutations := []struct{ name, old, replacement string }{
		{"level", "level: partial", "level: supported"},
		{"loss mode", "lossModes: [deploy-target-is-file]", "lossModes: [deploy-target-is-file, extra]"},
		{"version", "version: 1.2.3", "version: 1.2.4"},
		{
			"maturity",
			"family: configures-key-symbol\n    level: unsupported\n    maturity: advisory",
			"family: configures-key-symbol\n    level: unsupported\n    maturity: stable\n    fixtures: [present.json]",
		},
		{"native edge", "via: [kubectl, helm, chart]", "via: [kubectl, helm]"},
	}

	for _, m := range mutations {
		t.Run("a change of "+m.name+" changes it", func(t *testing.T) {
			text := strings.Replace(original, m.old, m.replacement, 1)
			obsRequire(t, text != original, "the mutation did not change the text")

			c := parseTestProfile(t, text)
			obsRequire(t, c.Digest != want, "digest did not change")
		})
	}
}

func TestParseProfileRefusals(t *testing.T) {
	const head = "id: x\nprovider: p\nbuild: {version: '1', adapterSchema: s}\n"

	cases := map[string]struct{ text, want string }{
		"empty":             {"", "empty"},
		"no id":             {"provider: p\nbuild: {version: '1', adapterSchema: s}\nfamilies: [{family: a, level: supported, maturity: advisory}]\n", "id is required"},
		"no families":       {head + "families: []\n", "at least one family"},
		"unknown key":       {head + "families: [{family: a, level: supported, maturity: advisory, surprise: 1}]\n", "surprise"},
		"bad level":         {head + "families: [{family: a, level: good, maturity: advisory}]\n", `level "good"`},
		"bad maturity":      {head + "families: [{family: a, level: supported, maturity: beta}]\n", `maturity "beta"`},
		"duplicate family":  {head + "families: [{family: a, level: supported, maturity: advisory}, {family: a, level: partial, maturity: advisory}]\n", "listed twice"},
		"stable no fixture": {head + "families: [{family: a, level: supported, maturity: stable}]\n", "names no fixture"},
		"digest written":    {head + "digest: sha256:00\nfamilies: [{family: a, level: supported, maturity: advisory}]\n", "digest is computed"},
		"two documents":     {head + "families: [{family: a, level: supported, maturity: advisory}]\n---\nid: y\n", "more than one"},
		"native no type":    {head + "families: [{family: a, level: supported, maturity: advisory, nativeEdges: [{present: [x]}]}]\n", "no type"},
		"syntax error":      {"id: [", "profile"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := observation.ParseProfile([]byte(tc.text))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadProfilesRefusesDuplicates(t *testing.T) {
	profile := func(id, version string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("id: " + id + "\nprovider: p\nbuild: {version: '" + version + "', adapterSchema: s}\n" +
			"families: [{family: a, level: supported, maturity: advisory}]\n")}
	}

	same := fstest.MapFS{"d/a.profile.yaml": profile("one", "1"), "d/b.profile.yaml": profile("two", "1")}
	if _, err := observation.LoadProfiles(same, "d"); err == nil || !strings.Contains(err.Error(), "another profile pins") {
		t.Fatalf("same build: err = %v", err)
	}

	sameID := fstest.MapFS{"d/a.profile.yaml": profile("one", "1"), "d/b.profile.yaml": profile("one", "2")}
	if _, err := observation.LoadProfiles(sameID, "d"); err == nil || !strings.Contains(err.Error(), "profile id one") {
		t.Fatalf("same id: err = %v", err)
	}

	bad := fstest.MapFS{"d/a.profile.yaml": {Data: []byte("id: x\n")}}
	if _, err := observation.LoadProfiles(bad, "d"); err == nil || !strings.Contains(err.Error(), "d/a.profile.yaml") {
		t.Fatalf("bad profile: err = %v", err)
	}

	if _, err := observation.LoadProfiles(fstest.MapFS{}, "missing"); err == nil {
		t.Fatal("a missing directory is not an error")
	}
}

func TestFindProfileExactMatch(t *testing.T) {
	p := loadTestProfile(t)
	profiles := []*observation.CapabilityProfile{p}

	if got, ok := observation.FindProfile(profiles, "1.2.3", "test-adapter/v1"); !ok || got != p {
		t.Fatalf("exact match = %v, %v", got, ok)
	}

	for _, tc := range []struct{ version, schema string }{
		{"1.2.4", "test-adapter/v1"},
		{"1.2", "test-adapter/v1"},
		{"1.2.3-rc1", "test-adapter/v1"},
		{"1.2.3", "test-adapter/v2"},
		{"", ""},
	} {
		if got, ok := observation.FindProfile(profiles, tc.version, tc.schema); ok {
			t.Errorf("FindProfile(%q, %q) = %s, want no match", tc.version, tc.schema, got.ID)
		}
	}
}

func TestProfileForUsesEmbeddedProfiles(t *testing.T) {
	profiles, err := observation.EmbeddedProfiles()
	if err != nil {
		t.Fatalf("embedded profiles: %v", err)
	}

	for _, p := range profiles {
		got, ok := observation.ProfileFor(p.Build.Version, p.Build.AdapterSchema)
		if !ok || got != p {
			t.Errorf("ProfileFor(%s, %s) = %v, %v", p.Build.Version, p.Build.AdapterSchema, got, ok)
		}

		if _, ok := observation.ProfileFor(p.Build.Version+".1", p.Build.AdapterSchema); ok {
			t.Errorf("ProfileFor matched a build that is not %s", p.ID)
		}
	}

	if _, ok := observation.ProfileFor("0.0.0-no-such-build", observation.AdapterSchema); ok {
		t.Error("ProfileFor found a profile for a build that does not exist")
	}
}

func TestStableFamilyNeedsItsFixture(t *testing.T) {
	p := loadTestProfile(t)

	t.Run("present fixture passes; advisory families may name missing ones", func(t *testing.T) {
		recordings := fstest.MapFS{"present.json": {Data: []byte("{}")}}
		if err := p.CheckFixtures(recordings); err != nil {
			t.Fatalf("err = %v", err)
		}

		if err := p.CheckFixtures(os.DirFS("testdata/recordings")); err != nil {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a stable family naming a missing fixture fails", func(t *testing.T) {
		err := p.CheckFixtures(fstest.MapFS{"other.json": {Data: []byte("{}")}})
		if err == nil {
			t.Fatal("a missing fixture passed")
		}

		obsContains(t, "error", err.Error(), "present.json")
		obsContains(t, "error", err.Error(), "family calls")
	})

	t.Run("a directory is not a recording", func(t *testing.T) {
		err := p.CheckFixtures(fstest.MapFS{"present.json/inner": {Data: []byte("{}")}})
		if err == nil {
			t.Fatal("a directory passed as a fixture")
		}
	})
}

// TestEmbeddedProfilesNameTheirStableFixtures runs the fixture check for
// every shipped profile against the committed recordings.
func TestEmbeddedProfilesNameTheirStableFixtures(t *testing.T) {
	profiles, err := observation.EmbeddedProfiles()
	if err != nil {
		t.Fatal(err)
	}

	recordings := os.DirFS("../../test/fixtures/cbm-recordings")

	for _, p := range profiles {
		if err := p.CheckFixtures(recordings); err != nil {
			t.Errorf("%v", err)
		}
	}
}
