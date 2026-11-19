package policy

import (
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/edgekind"
	"github.com/inclaro/concordia/internal/observation"
)

// ResolveEdgeKind gives the canonical kinds of a policy token: an alias such
// as `IMPORTS` or `configures`, or a canonical kebab-case kind. The bool is
// false for an unknown token.
func ResolveEdgeKind(token string) ([]observation.CanonicalEdgeKind, bool) {
	return edgekind.Resolve(token)
}

// ParseEdgeKind is like ResolveEdgeKind, but an unknown token gives a CO-POL-004 *diag.Error.
func ParseEdgeKind(token string, source *diag.Source) ([]observation.CanonicalEdgeKind, error) {
	return edgekind.Parse(token, source)
}

// ParseEdgeKinds parses a list of tokens. The result is the union of their
// kinds, sorted and without duplicates.
func ParseEdgeKinds(tokens []string, source *diag.Source) ([]observation.CanonicalEdgeKind, error) {
	return edgekind.ParseAll(tokens, source)
}

// NativeKindsFor gives the native CBM edge types that make the canonical kind.
func NativeKindsFor(kind observation.CanonicalEdgeKind) []string {
	return edgekind.NativeKinds(kind)
}
