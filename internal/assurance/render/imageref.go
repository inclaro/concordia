// Package render turns the declared rendering sources of an environment (plain manifests, Helm charts,
// Kustomize overlays) into rendered-or-evaluated evidence: scoped resources, container occurrences, and typed
// facts for the effective-policy evaluators. It reads the rendered documents itself and never asks the code graph
// (design D3), runs renderers only under the confinement layers of design D4, and redacts secrets before anything
// is kept (design D17).
package render

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ImageRef is a container image reference with its parts kept apart. The repository is everything before the tag
// and the digest, as written (a registry host and port stay part of it, no registry is assumed), so two containers
// with one repository and different tags are told apart by Tag.
type ImageRef struct {
	// Raw is the reference as it was written.
	Raw string `json:"image"`
	// Registry is the host (with the port) when the first path component is a host name, else empty.
	Registry string `json:"registry,omitempty"`
	// Repository is the reference without tag and digest, for example `registry.example:5000/shop/api`.
	Repository string `json:"repository"`
	// Tag is empty when the reference has none. A reference with neither tag nor digest means `latest` to a
	// container runtime, but it is recorded as no tag: Concordia does not invent one.
	Tag string `json:"tag,omitempty"`
	// Digest is `<algorithm>:<hex>`, empty when the reference has none.
	Digest string `json:"digest,omitempty"`
}

var digestPattern = regexp.MustCompile(`^[a-z0-9]+(?:[+._-][a-z0-9]+)*:[0-9a-fA-F]{32,}$`)

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

// ParseImageRef splits an image reference. A tag is the text after the last colon when no slash follows it (so
// `registry:5000/app` has a registry port and no tag); a digest follows `@`; a reference may have both, and the
// digest is then the identity.
func ParseImageRef(raw string) (ImageRef, error) {
	ref := ImageRef{Raw: raw}

	text := raw
	if text == "" || strings.ContainsAny(text, " \t\r\n") {
		return ref, errors.New("image reference is empty or has white space")
	}

	if strings.Contains(text, "{{") || strings.Contains(text, "${") {
		return ref, errors.New("image reference is a template that was not resolved")
	}

	if before, digest, ok := strings.Cut(text, "@"); ok {
		if !digestPattern.MatchString(digest) {
			return ref, fmt.Errorf("image digest %q is not <algorithm>:<hex>", digest)
		}

		text, ref.Digest = before, digest
	}

	if i := strings.LastIndex(text, ":"); i >= 0 && !strings.Contains(text[i:], "/") {
		tag := text[i+1:]
		if !tagPattern.MatchString(tag) {
			return ref, fmt.Errorf("image tag %q is not valid", tag)
		}

		text, ref.Tag = text[:i], tag
	}

	if text == "" || strings.HasPrefix(text, "/") || strings.HasSuffix(text, "/") || strings.Contains(text, "//") {
		return ref, errors.New("image repository is empty or malformed")
	}

	ref.Repository = text

	if first, _, ok := strings.Cut(text, "/"); ok && isRegistryHost(first) {
		ref.Registry = first
	}

	return ref, nil
}

// isRegistryHost is the Docker rule for the first path component: a host name has a dot or a port, or is
// `localhost`.
func isRegistryHost(first string) bool {
	return strings.ContainsAny(first, ".:") || first == "localhost"
}

// Immutable says that the reference names one content: it has a digest. A tag can move, so a tag alone is never a
// stable version.
func (r *ImageRef) Immutable() bool { return r.Digest != "" }

// Identity is the digest when there is one (also when a tag is present), else `repository:tag`, else the
// repository. Only an identity with a digest is stable.
func (r *ImageRef) Identity() string {
	switch {
	case r.Digest != "":
		return r.Repository + "@" + r.Digest
	case r.Tag != "":
		return r.Repository + ":" + r.Tag
	default:
		return r.Repository
	}
}
