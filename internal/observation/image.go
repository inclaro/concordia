package observation

import (
	"errors"
	"fmt"
	"strings"
)

// Errors of ParseImageRef.
var (
	// ErrInvalidImageRef means the text is not an image reference.
	ErrInvalidImageRef = errors.New("invalid image reference")
	// ErrTemplatedImageRef means the reference has a template or variable that
	// is not rendered, so no repository, tag, or digest can be read from it.
	ErrTemplatedImageRef = errors.New("image reference is templated")
)

// digestHexLengths are the hex lengths of the digest algorithms that Concordia checks.
var digestHexLengths = map[string]int{"sha256": sha256HexLength, "sha512": sha512HexLength}

const (
	sha256HexLength    = 64
	sha512HexLength    = 128
	minDigestHexLength = 32
	maxTagLength       = 128
	localhostRegistry  = "localhost"
)

// ImageRef is a parsed image reference: the text of the edge `image` property
// (CBM 0.12.1 observations, O3). The `Image` node is one per repository and has
// no tag or digest, so Concordia never reads these from the node.
type ImageRef struct {
	// Raw is the reference as written.
	Raw string
	// Repository is the repository including the registry host, for example
	// `registry.example.com/payments/api` or `postgres`. It is the `repository` of the `Image` node.
	Repository string
	// Registry is the registry host with its port, for example `localhost:5000`.
	// Empty for a short Docker Hub name.
	Registry string
	// Tag is the source tag, empty when the reference has none.
	Tag string
	// Digest is the immutable content digest `<algorithm>:<hex>`, empty when
	// the reference has none.
	Digest string
	// Platform is the platform of PlatformDigest, for example `linux/amd64`.
	Platform string
	// PlatformDigest is the digest of one platform manifest, when known. A
	// reference alone cannot say it; Concordia does not query a registry. See WithPlatform.
	PlatformDigest string
}

// ParseImageRef parses an image reference with a tag (`repo:1.2`), a digest
// (`repo@sha256:...`), both (`repo:1.2@sha256:...`), or neither, and a registry
// with a port (`localhost:5000/team/app:1.0`). A reference with a digest has the
// tag only when it is written. A reference with a variable or template (`${TAG}`,
// `{{ .Values.tag }}`) gives ErrTemplatedImageRef.
func ParseImageRef(ref string) (ImageRef, error) {
	if err := checkImageRefText(ref); err != nil {
		return ImageRef{}, err
	}

	name, digest, hasDigest := strings.Cut(ref, "@")
	if hasDigest {
		if err := checkDigest(digest); err != nil {
			return ImageRef{}, invalidImageRef(ref, err)
		}
	}

	name, tag, err := splitImageTag(name)
	if err != nil {
		return ImageRef{}, invalidImageRef(ref, err)
	}

	if isMalformedRepository(name) {
		return ImageRef{}, fmt.Errorf("%w: %q has no repository", ErrInvalidImageRef, ref)
	}

	registry, err := registryHostOf(name)
	if err != nil {
		return ImageRef{}, invalidImageRef(ref, err)
	}

	return ImageRef{Raw: ref, Repository: name, Registry: registry, Tag: tag, Digest: digest}, nil
}

func invalidImageRef(ref string, cause error) error {
	return fmt.Errorf("%w: %q: %w", ErrInvalidImageRef, ref, cause)
}

// checkImageRefText rejects an empty reference, a templated reference, and a
// reference with whitespace, in this order.
func checkImageRefText(ref string) error {
	if ref == "" {
		return fmt.Errorf("%w: empty", ErrInvalidImageRef)
	}

	if isTemplatedImageRef(ref) {
		return fmt.Errorf("%w: %q", ErrTemplatedImageRef, ref)
	}

	if strings.ContainsAny(ref, " \t\r\n") {
		return fmt.Errorf("%w: %q has whitespace", ErrInvalidImageRef, ref)
	}

	return nil
}

func isTemplatedImageRef(ref string) bool {
	return strings.ContainsAny(ref, "$`") || strings.Contains(ref, "{{") || strings.Contains(ref, "<.")
}

// splitImageTag splits the tag from the name and checks it. A tag follows the
// last `:` after the last `/`; a `:` before it is a registry port.
func splitImageTag(name string) (rest, tag string, err error) {
	colon := strings.LastIndex(name, ":")
	if colon <= strings.LastIndex(name, "/") {
		return name, "", nil
	}

	tag = name[colon+1:]

	return name[:colon], tag, checkTag(tag)
}

func isMalformedRepository(name string) bool {
	if name == "" || strings.Contains(name, "//") {
		return true
	}

	return strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/")
}

// registryHostOf gives the registry host of a repository name, or "" when the
// first path component is not a registry host.
func registryHostOf(name string) (string, error) {
	host, _, found := strings.Cut(name, "/")
	if !found || !isRegistryHost(host) {
		return "", nil
	}

	return host, checkRegistryPort(host)
}

// isRegistryHost applies the container-tool rule: the first path component is
// a registry host when it has a `.` or a `:`, or is `localhost`.
func isRegistryHost(component string) bool {
	return strings.ContainsAny(component, ".:") || component == localhostRegistry
}

func checkRegistryPort(host string) error {
	_, port, found := strings.Cut(host, ":")
	if !found {
		return nil
	}

	if port == "" || strings.Trim(port, "0123456789") != "" {
		return fmt.Errorf("registry port %q is not a number", port)
	}

	return nil
}

func checkTag(tag string) error {
	if tag == "" {
		return errors.New("empty tag")
	}

	if len(tag) > maxTagLength {
		return fmt.Errorf("tag longer than %d characters", maxTagLength)
	}

	for i := range len(tag) {
		if !isImageTagChar(tag[i], i == 0) {
			return fmt.Errorf("tag %q has the character %q", tag, tag[i])
		}
	}

	return nil
}

// isImageTagChar reports whether c may be in a tag: letters, digits, and `_`,
// and `.` or `-` when it is not the first character.
func isImageTagChar(c byte, first bool) bool {
	if c == '_' || isImageAlphanumeric(c) {
		return true
	}

	return !first && (c == '.' || c == '-')
}

func isImageAlphanumeric(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func checkDigest(digest string) error {
	algorithm, hex, found := strings.Cut(digest, ":")
	if !found || !hasBoth(algorithm, hex) {
		return fmt.Errorf("digest %q is not <algorithm>:<hex>", digest)
	}

	if strings.Trim(hex, "0123456789abcdefABCDEF") != "" {
		return fmt.Errorf("digest %q is not hexadecimal", digest)
	}

	return checkDigestHexLength(digest, algorithm, hex)
}

// checkDigestHexLength checks the length of the hex part: exact for a known
// algorithm, a minimum for any other.
func checkDigestHexLength(digest, algorithm, hex string) error {
	want, known := digestHexLengths[algorithm]
	if !known {
		if len(hex) < minDigestHexLength {
			return fmt.Errorf("digest %q is shorter than %d hex characters", digest, minDigestHexLength)
		}

		return nil
	}

	if len(hex) != want {
		return fmt.Errorf("digest %q has %d hex characters, %s needs %d", digest, len(hex), algorithm, want)
	}

	return nil
}

// Mutable says whether the reference is a mutable reference: it has no
// immutable digest, so a tag (or no tag, which means `latest`) can point to
// other content later. A mutable tag is never a stable artifact version.
func (r *ImageRef) Mutable() bool { return r.Digest == "" }

// SourceReference gives the tag, else the digest: the source reference that
// the manifest writes, empty for a bare repository.
func (r *ImageRef) SourceReference() string {
	if r.Tag != "" {
		return r.Tag
	}

	return r.Digest
}

// WithPlatform gives the reference with the digest of one platform manifest.
// The platform digest comes from a source other than the reference text (a
// lock file or a registry listing that a later change reads).
func (r *ImageRef) WithPlatform(platform, platformDigest string) (ImageRef, error) {
	if platform == "" {
		return ImageRef{}, fmt.Errorf("%w: empty platform", ErrInvalidImageRef)
	}

	if err := checkDigest(platformDigest); err != nil {
		return ImageRef{}, fmt.Errorf("%w: platform digest: %w", ErrInvalidImageRef, err)
	}

	out := *r
	out.Platform = platform
	out.PlatformDigest = platformDigest

	return out, nil
}

// ContainerOccurrence is one container that uses an image: the container name
// with the reference of its `image` property. Two containers that use one
// repository at different tags are two occurrences.
type ContainerOccurrence struct {
	// Container is the container name (the edge property `container`), empty if the edge has none.
	Container string
	// Stage is the Dockerfile stage index (the edge property `stage`), empty if the edge has none.
	Stage string
	// Image is the parsed `image` property.
	Image ImageRef
	// Environment is the environment of the edge.
	Environment string
	// PossiblyLossy is true when the provider may have merged this occurrence
	// with others (CBM 0.12.1 observations, O4): the occurrences of the same
	// workload and repository that the edge does not show cannot be recovered.
	PossiblyLossy bool
}

// Discriminator gives the occurrence discriminator of the edge: the container
// name, else `stage:<index>`, else empty.
func (o *ContainerOccurrence) Discriminator() string {
	switch {
	case o.Container != "":
		return o.Container
	case o.Stage != "":
		return "stage:" + o.Stage
	default:
		return ""
	}
}

// ContainerOccurrenceOf reads the occurrence of an edge that has an `image`
// property (a `uses-image` fact, a `copies-from-image` or `base-image` fact).
// The tag and digest come from the edge `image` property, never from a node.
// The bool is false when the edge has no image property or a reference that
// cannot be parsed; the error says why in the second case.
func ContainerOccurrenceOf(edge *EvidenceEdge) (ContainerOccurrence, bool, error) {
	raw := edge.Properties["image"]
	if raw == "" {
		return ContainerOccurrence{}, false, nil
	}

	ref, err := ParseImageRef(raw)
	if err != nil {
		return ContainerOccurrence{}, false, err
	}

	return ContainerOccurrence{
		Container:     edge.Properties["container"],
		Stage:         edge.Properties["stage"],
		Image:         ref,
		Environment:   edge.Environment,
		PossiblyLossy: edge.PossiblyLossy,
	}, true, nil
}
