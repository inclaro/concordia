package observation_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

const (
	digest256 = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	digest512 = "sha512:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestParseImageRef(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ref     string
		source  string
		want    observation.ImageRef
		mutable bool
	}{
		{"tag", "registry.example.com/payments/api:1.3.0", "1.3.0", observation.ImageRef{Repository: "registry.example.com/payments/api", Registry: "registry.example.com", Tag: "1.3.0"}, true},
		{"short docker hub name with tag", "postgres:16", "16", observation.ImageRef{Repository: "postgres", Tag: "16"}, true},
		{"user repository", "team/app:2", "2", observation.ImageRef{Repository: "team/app", Tag: "2"}, true},
		{"no tag is latest", "postgres", "", observation.ImageRef{Repository: "postgres"}, true},
		{"digest only", "registry.example.com/payments/worker@" + digest256, digest256, observation.ImageRef{Repository: "registry.example.com/payments/worker", Registry: "registry.example.com", Digest: digest256}, false},
		{"tag and digest", "registry.example.com/payments/worker:1.0@" + digest256, "1.0", observation.ImageRef{Repository: "registry.example.com/payments/worker", Registry: "registry.example.com", Tag: "1.0", Digest: digest256}, false},
		{"sha512 digest", "app@" + digest512, digest512, observation.ImageRef{Repository: "app", Digest: digest512}, false},
		{"registry port with tag", "localhost:5000/team/app:1.0", "1.0", observation.ImageRef{Repository: "localhost:5000/team/app", Registry: "localhost:5000", Tag: "1.0"}, true},
		{"registry port without tag", "registry.example.com:5443/payments/api", "", observation.ImageRef{Repository: "registry.example.com:5443/payments/api", Registry: "registry.example.com:5443"}, true},
		{"registry port with digest", "registry.example.com:5443/a/b@" + digest256, digest256, observation.ImageRef{Repository: "registry.example.com:5443/a/b", Registry: "registry.example.com:5443", Digest: digest256}, false},
		{"localhost without port", "localhost/app:dev", "dev", observation.ImageRef{Repository: "localhost/app", Registry: "localhost", Tag: "dev"}, true},
		{"tag with dots and dashes", "ghcr.io/org/app:v1.2.3-rc.1_x", "v1.2.3-rc.1_x", observation.ImageRef{Repository: "ghcr.io/org/app", Registry: "ghcr.io", Tag: "v1.2.3-rc.1_x"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := observation.ParseImageRef(tc.ref)
			if err != nil {
				t.Fatal(err)
			}

			tc.want.Raw = tc.ref
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}

			if got.Mutable() != tc.mutable {
				t.Errorf("Mutable() = %v, want %v", got.Mutable(), tc.mutable)
			}

			if got.SourceReference() != tc.source {
				t.Errorf("SourceReference() = %q, want %q", got.SourceReference(), tc.source)
			}
		})
	}
}

func TestParseImageRefRejects(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"", " ", "app tag", "app:", "app@", "app@sha256", "app@sha256:xyz", "app@sha256:abcd",
		"app@" + digest256[:len(digest256)-1], "app@md5:0123", "/app", "app/", "a//b", ":1.0", "reg:abc/app:1", "app:-bad",
	}
	for _, ref := range invalid {
		if _, err := observation.ParseImageRef(ref); !errors.Is(err, observation.ErrInvalidImageRef) {
			t.Errorf("%q: err = %v, want ErrInvalidImageRef", ref, err)
		}
	}

	for _, ref := range []string{"registry.example.com/api:${TAG}", "{{ .Values.image }}", "app:{{ .Values.tag }}", "$IMAGE", "<.Release.Name>/app"} {
		if _, err := observation.ParseImageRef(ref); !errors.Is(err, observation.ErrTemplatedImageRef) {
			t.Errorf("%q: err = %v, want ErrTemplatedImageRef", ref, err)
		}
	}
}

func TestImageRefPlatformDigest(t *testing.T) {
	t.Parallel()

	ref, err := observation.ParseImageRef("registry.example.com/payments/api:1.0@" + digest256)
	if err != nil {
		t.Fatal(err)
	}

	obsEqual(t, "platform digest of a reference text", ref.PlatformDigest, "")
	obsEqual(t, "platform of a reference text", ref.Platform, "")

	platformDigest := "sha256:" + strings.Repeat("ab", 32)

	withPlatform, err := ref.WithPlatform("linux/arm64", platformDigest)
	if err != nil {
		t.Fatal(err)
	}

	obsEqual(t, "platform", withPlatform.Platform, "linux/arm64")
	obsEqual(t, "platform digest", withPlatform.PlatformDigest, platformDigest)
	obsEqual(t, "digest", withPlatform.Digest, digest256)
	obsEqual(t, "receiver platform digest", ref.PlatformDigest, "")

	_, err = ref.WithPlatform("", platformDigest)
	obsRequire(t, errors.Is(err, observation.ErrInvalidImageRef), fmt.Sprintf("empty platform: %v", err))

	_, err = ref.WithPlatform("linux/amd64", "sha256:short")
	obsRequire(t, errors.Is(err, observation.ErrInvalidImageRef), fmt.Sprintf("bad platform digest: %v", err))
}

func usageEdge(container, image string) *observation.EvidenceEdge {
	return &observation.EvidenceEdge{
		ID: "e-" + container, From: "cbm:p:deploy", To: "cbm:p:image", Kind: observation.KindUsesImage, NativeKind: "USAGE",
		Properties: map[string]string{"ref": "image", "container": container, "image": image},
	}
}

func TestContainerOccurrenceOfReadsTheEdgeNotTheNode(t *testing.T) {
	t.Parallel()

	occ, ok, err := observation.ContainerOccurrenceOf(usageEdge("sidecar", "registry.example.com/payments/api:1.3.0"))
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}

	obsEqual(t, "container", occ.Container, "sidecar")
	obsEqual(t, "tag", occ.Image.Tag, "1.3.0")
	obsEqual(t, "repository", occ.Image.Repository, "registry.example.com/payments/api")
	obsRequire(t, occ.Image.Mutable(), fmt.Sprintf("%+v", occ))
	obsEqual(t, "discriminator", occ.Discriminator(), "sidecar")
}

func TestContainerOccurrenceOfAStageEdge(t *testing.T) {
	t.Parallel()

	stage := &observation.EvidenceEdge{Properties: map[string]string{"image": "postgres:16", "stage": "1"}}

	occ, ok, err := observation.ContainerOccurrenceOf(stage)
	if err != nil || !ok {
		t.Fatalf("stage: %+v %v %v", occ, ok, err)
	}

	obsEqual(t, "stage discriminator", occ.Discriminator(), "stage:1")
}

func TestContainerOccurrenceOfAnEdgeWithoutAnImage(t *testing.T) {
	t.Parallel()

	_, ok, err := observation.ContainerOccurrenceOf(&observation.EvidenceEdge{Properties: map[string]string{"ref": "env"}})
	if ok || err != nil {
		t.Errorf("an edge with no image: %v %v", ok, err)
	}
}

func TestContainerOccurrenceOfATemplatedImage(t *testing.T) {
	t.Parallel()

	_, ok, err := observation.ContainerOccurrenceOf(&observation.EvidenceEdge{Properties: map[string]string{"image": "a:${V}"}})
	obsRequire(t, !ok, "a templated image has an occurrence")
	obsRequire(t, errors.Is(err, observation.ErrTemplatedImageRef), fmt.Sprintf("templated: %v", err))
}

// A05: two occurrences with different tags are two facts.
func TestA05TwoOccurrencesAreTwoFacts(t *testing.T) {
	t.Parallel()

	app := usageEdge("app", "registry.example.com/payments/api:1.2.0")
	app.Occurrence = "app"
	sidecar := usageEdge("sidecar", "registry.example.com/payments/api:1.3.0")
	sidecar.Occurrence = "sidecar"

	if observation.EdgeKeyOf(app) == observation.EdgeKeyOf(sidecar) {
		t.Fatal("two containers of one pair must have two edge keys")
	}

	kept, dropped := observation.DedupeEdges([]*observation.EvidenceEdge{app, sidecar})
	obsEqual(t, "kept", len(kept), 2)
	obsEqual(t, "dropped", dropped, 0)

	a, _, _ := observation.ContainerOccurrenceOf(app)
	s, _, _ := observation.ContainerOccurrenceOf(sidecar)

	obsRequire(t, a.Image.Tag != s.Image.Tag, fmt.Sprintf("occurrences: %+v %+v", a, s))
	obsRequire(t, !a.PossiblyLossy && !s.PossiblyLossy, fmt.Sprintf("occurrences: %+v %+v", a, s))
}

const lossyProfile = `
id: test
provider: codebase-memory-mcp
build: {version: "9.9.9", adapterSchema: concordia-cbm/v2}
families:
  - family: uses-image
    level: supported
    maturity: advisory
    lossModes: [merged-occurrences]
  - family: references-secret
    level: supported
    maturity: advisory
    lossModes: [target-not-declared]
  - family: deploys-artifact
    level: supported
    maturity: advisory
`

// A05: a merged fact is marked lossy and no occurrence is invented.
func TestA05MergedFactIsMarkedPossiblyLossy(t *testing.T) {
	t.Parallel()

	profile, err := observation.ParseProfile([]byte(lossyProfile))
	if err != nil {
		t.Fatal(err)
	}

	merged := usageEdge("sidecar", "registry.example.com/payments/api:1.3.0")
	secret := &observation.EvidenceEdge{Kind: observation.KindReferencesSecret}
	deploys := &observation.EvidenceEdge{Kind: observation.KindDeploysArtifact}
	unlisted := &observation.EvidenceEdge{Kind: observation.KindCalls}

	edges := []*observation.EvidenceEdge{merged, secret, deploys, unlisted}
	obsEqual(t, "marked edges", observation.MarkPossiblyLossy(edges, profile), 1)

	// Only merged-occurrences marks.
	obsEqual(t, "merged lossy", merged.PossiblyLossy, true)
	obsEqual(t, "secret lossy", secret.PossiblyLossy, false)
	obsEqual(t, "deploys lossy", deploys.PossiblyLossy, false)
	obsEqual(t, "unlisted lossy", unlisted.PossiblyLossy, false)

	occ, _, _ := observation.ContainerOccurrenceOf(merged)
	obsEqual(t, "occurrence lossy", occ.PossiblyLossy, true)
	obsEqual(t, "occurrence container", occ.Container, "sidecar")

	obsDeepEqual(t, "LossModes", observation.LossModes(profile, observation.KindUsesImage), []string{observation.LossMergedOccurrences})

	// A nil profile marks nothing.
	obsRequire(t, observation.LossModes(nil, observation.KindUsesImage) == nil, "a nil profile has loss modes")
	obsEqual(t, "marked edges without a profile", observation.MarkPossiblyLossy(edges, nil), 0)
}

func TestEdgeKeyDistinguishesScopeOperationReferenceAndOccurrence(t *testing.T) {
	t.Parallel()

	base := &observation.EvidenceEdge{Kind: observation.KindCalls, From: "a", To: "b", Properties: map[string]string{}}

	variants := map[string]*observation.EvidenceEdge{
		"kind":        {Kind: observation.KindImports, From: "a", To: "b"},
		"direction":   {Kind: observation.KindCalls, From: "b", To: "a"},
		"environment": {Kind: observation.KindCalls, From: "a", To: "b", Environment: "production"},
		"operation":   {Kind: observation.KindCalls, From: "a", To: "b", Properties: map[string]string{"protocol": "https"}},
		"facet op":    {Kind: observation.KindCalls, From: "a", To: "b", Facets: map[string]string{"operation": "admin"}},
		"reference":   {Kind: observation.KindCalls, From: "a", To: "b", Properties: map[string]string{"ref": "env"}},
		"occurrence":  {Kind: observation.KindCalls, From: "a", To: "b", Occurrence: "app"},
	}

	seen := map[observation.EdgeKey]string{observation.EdgeKeyOf(base): "base"}

	for name, e := range variants {
		key := observation.EdgeKeyOf(e)
		if other, dup := seen[key]; dup {
			t.Errorf("%s has the key of %s", name, other)
		}

		seen[key] = name
	}
}

// A02: an HTTPS and an administrative connection on one pair.
func TestEdgeKeyKeepsTwoConnectionsOnOnePairApart(t *testing.T) {
	t.Parallel()

	https := &observation.EvidenceEdge{Kind: observation.KindConfiguredConnection, From: "a", To: "b", Properties: map[string]string{"protocol": "https"}}
	admin := &observation.EvidenceEdge{Kind: observation.KindConfiguredConnection, From: "a", To: "b", Properties: map[string]string{"protocol": "admin"}}

	if observation.EdgeKeyOf(https) == observation.EdgeKeyOf(admin) {
		t.Error("A02: two connections on one pair must be two facts")
	}
}

func TestDedupeEdgesKeepsTheSameOccurrenceOnce(t *testing.T) {
	t.Parallel()

	one := usageEdge("app", "x/y:1")
	one.Occurrence = "app"
	twin := usageEdge("app", "x/y:1")
	twin.Occurrence = "app"
	twin.ID = "other-id"

	kept, dropped := observation.DedupeEdges([]*observation.EvidenceEdge{one, twin})
	obsEqual(t, "kept", len(kept), 1)
	obsEqual(t, "dropped", dropped, 1)
	obsRequire(t, kept[0] == one, "the first occurrence was not kept")
}
