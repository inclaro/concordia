// Package edgekind resolves the edge-kind tokens of policies and CALM documents
// to sets of canonical edge kinds. It is a separate package so that both
// internal/intent and internal/policy can use it without an import cycle.
//
// A token has one meaning: there are no views and no modes. A token is a
// kebab-case canonical kind (`references-secret`), or one of the aliases:
//
//   - `IMPORTS` is `imports`: imports of code only.
//   - `USAGE` is `ambiguous-usage`: code usage only (a native `USAGE` edge that
//     has no `ref`). Every reference of infrastructure has its own kind, and no
//     token of a facet matches `ambiguous-usage`.
//   - `configures` (or `CONFIGURES`) is the five strategy kinds
//     `configures-env-access`, `configures-env-set`, `configures-env-read`,
//     `configures-key-symbol`, and `configures-helm-values`; a rule that wants
//     one strategy names its kebab token.
//   - `deploys` (or `DEPLOYS`) is `deploys-artifact`.
//
// The kinds `unsupported` and `ambiguous` keep native evidence without an
// interpretation and are never tokens.
package edgekind

import (
	"slices"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

// aliases maps an alias token to its canonical kinds.
var aliases = map[string][]observation.CanonicalEdgeKind{
	"IMPORTS":        {observation.KindImports},
	"USES_TYPE":      {observation.KindUsesType},
	"CALLS":          {observation.KindCalls},
	"CALL_REFERENCE": {observation.KindReferencesCallable},
	"USAGE":          {observation.KindAmbiguousUsage},
	"INHERITS":       {observation.KindInherits},
	"IMPLEMENTS":     {observation.KindImplements},
	"HTTP_CALLS":     {observation.KindCallsService},
	"ASYNC_CALLS":    {observation.KindDispatchesAsync},
	"EMITS":          {observation.KindEmitsEvent},
	"LISTENS_ON":     {observation.KindListensEvent},
	"DATA_FLOWS":     {observation.KindDataFlows},
	"READS":          {observation.KindReads},
	"WRITES":         {observation.KindWrites},
	"CONFIGURES":     configuresKinds,
	"configures":     configuresKinds,
	"DEPLOYS":        {observation.KindDeploysArtifact},
	"deploys":        {observation.KindDeploysArtifact},
}

// configuresKinds are the strategy kinds that the token `configures` selects.
var configuresKinds = []observation.CanonicalEdgeKind{
	observation.KindConfiguresEnvAccess,
	observation.KindConfiguresEnvSet,
	observation.KindConfiguresEnvRead,
	observation.KindConfiguresKeySymbol,
	observation.KindConfiguresHelmValues,
}

// isTokenKind says whether a canonical kind has a kebab-case token. The two
// kinds that keep evidence uninterpreted do not.
func isTokenKind(kind observation.CanonicalEdgeKind) bool {
	return kind != observation.KindUnsupported && kind != observation.KindAmbiguous
}

// Resolve gives the canonical kinds of a token, sorted. The bool is false
// when the token is not known.
func Resolve(token string) ([]observation.CanonicalEdgeKind, bool) {
	if kinds, ok := aliases[token]; ok {
		out := slices.Clone(kinds)
		slices.Sort(out)

		return out, true
	}

	kind := observation.CanonicalEdgeKind(token)
	if observation.IsCanonicalEdgeKind(token) && isTokenKind(kind) {
		return []observation.CanonicalEdgeKind{kind}, true
	}

	return nil, false
}

// Tokens gives all tokens, sorted: the aliases and the kebab-case kinds.
func Tokens() []string {
	out := make([]string, 0, len(aliases)+len(observation.CanonicalEdgeKinds()))

	for token := range aliases {
		out = append(out, token)
	}

	for _, kind := range observation.CanonicalEdgeKinds() {
		if isTokenKind(kind) {
			out = append(out, string(kind))
		}
	}

	slices.Sort(out)

	return out
}

// Parse is like Resolve, but an unknown token gives a CO-POL-004 error
// (exit code 2). A nil source adds no source to the diagnostic.
func Parse(token string, source *diag.Source) ([]observation.CanonicalEdgeKind, error) {
	kinds, ok := Resolve(token)
	if ok {
		return kinds, nil
	}

	var opts []diag.Option
	if source != nil {
		opts = append(opts, diag.WithSource(*source))
	}

	return nil, diag.Fail(diag.POL004, diag.Params{"kind": token}, opts...)
}

// ParseAll parses a list of tokens. The result is the union of their kinds,
// sorted and without duplicates.
func ParseAll(tokens []string, source *diag.Source) ([]observation.CanonicalEdgeKind, error) {
	out := make([]observation.CanonicalEdgeKind, 0, len(tokens))

	for _, token := range tokens {
		kinds, err := Parse(token, source)
		if err != nil {
			return nil, err
		}

		out = append(out, kinds...)
	}

	slices.Sort(out)

	return slices.Compact(out), nil
}

// NativeKinds gives the native CBM edge types that make the canonical kind
// (the reverse of the normalization table), sorted. It is never nil.
func NativeKinds(kind observation.CanonicalEdgeKind) []string {
	return observation.NativeKindsOfCanonical(kind)
}
