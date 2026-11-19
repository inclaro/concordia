package edgekind_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/edgekind"
	"github.com/inclaro/concordia/internal/observation"
)

func kindsOf(names ...string) []observation.CanonicalEdgeKind {
	out := make([]observation.CanonicalEdgeKind, len(names))
	for i, n := range names {
		out[i] = observation.CanonicalEdgeKind(n)
	}

	slices.Sort(out)

	return out
}

func TestResolveTokensToKindSets(t *testing.T) {
	t.Parallel()

	cases := map[string][]observation.CanonicalEdgeKind{
		"IMPORTS":         kindsOf("imports"),
		"imports":         kindsOf("imports"),
		"USAGE":           kindsOf("ambiguous-usage"),
		"ambiguous-usage": kindsOf("ambiguous-usage"),
		"CALLS":           kindsOf("calls"),
		"CALL_REFERENCE":  kindsOf("references-callable"),
		"HTTP_CALLS":      kindsOf("calls-service"),
		"configures": kindsOf(
			"configures-env-access", "configures-env-set", "configures-env-read", "configures-key-symbol", "configures-helm-values"),
		"CONFIGURES": kindsOf(
			"configures-env-access", "configures-env-set", "configures-env-read", "configures-key-symbol", "configures-helm-values"),
		"configures-env-set":        kindsOf("configures-env-set"),
		"configures-helm-values":    kindsOf("configures-helm-values"),
		"deploys":                   kindsOf("deploys-artifact"),
		"DEPLOYS":                   kindsOf("deploys-artifact"),
		"deploys-artifact":          kindsOf("deploys-artifact"),
		"references-secret":         kindsOf("references-secret"),
		"infrastructure-dependency": kindsOf("infrastructure-dependency"),
		"tests":                     kindsOf("tests"),
		"calls-grpc":                kindsOf("calls-grpc"),
		"similar-to":                kindsOf("similar-to"),
	}

	for token, want := range cases {
		got, ok := edgekind.Resolve(token)
		if !ok || !slices.Equal(got, want) {
			t.Errorf("Resolve(%q) = %v, %v; want %v", token, got, ok, want)
		}
	}
}

func TestEveryKindHasAKebabTokenExceptUnsupportedAndAmbiguous(t *testing.T) {
	t.Parallel()

	for _, kind := range observation.CanonicalEdgeKinds() {
		got, ok := edgekind.Resolve(string(kind))

		if kind == observation.KindUnsupported || kind == observation.KindAmbiguous {
			if ok {
				t.Errorf("Resolve(%q) = %v; want unknown", kind, got)
			}

			continue
		}

		if !ok || !slices.Equal(got, []observation.CanonicalEdgeKind{kind}) {
			t.Errorf("Resolve(%q) = %v, %v", kind, got, ok)
		}
	}
}

func TestUnknownTokensAreRejected(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"NOPE", "constructor", "unsupported", "ambiguous", "Imports", ""} {
		if got, ok := edgekind.Resolve(token); ok {
			t.Errorf("Resolve(%q) = %v; want unknown", token, got)
		}
	}

	_, err := edgekind.Parse("unsupported", nil)

	var failure *diag.Error
	if !errors.As(err, &failure) || failure.Diagnostic.Code != diag.POL004 || failure.ExitCode != diag.InvalidInput {
		t.Fatalf("err = %v", err)
	}
}

// A token that names a facet never matches ambiguous-usage: only USAGE and its own kebab token do.
func TestNoFacetTokenMatchesAmbiguousUsage(t *testing.T) {
	t.Parallel()

	for _, token := range edgekind.Tokens() {
		kinds, ok := edgekind.Resolve(token)
		if !ok {
			t.Fatalf("token %q from Tokens() does not resolve", token)
		}

		if slices.Contains(kinds, observation.KindAmbiguousUsage) && token != "USAGE" && token != "ambiguous-usage" {
			t.Errorf("token %q resolves to %v and matches ambiguous-usage", token, kinds)
		}
	}

	facets := []string{
		"uses-image", "uses-runtime-identity", "references-secret", "references-configuration", "references-storage",
		"references-certificate-secret", "route-attaches-to-gateway", "binding-refers-to-role",
		"binding-applies-to-principal", "policy-selects-workload", "disruption-policy-target", "scaling-target",
		"infrastructure-dependency",
	}

	for _, token := range facets {
		kinds, _ := edgekind.Resolve(token)
		if slices.Contains(kinds, observation.KindAmbiguousUsage) {
			t.Errorf("facet token %q matches ambiguous-usage", token)
		}
	}
}

func TestParseAllIsTheSortedUnion(t *testing.T) {
	t.Parallel()

	got, err := edgekind.ParseAll([]string{"configures", "configures-env-set", "IMPORTS", "imports"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := kindsOf("imports", "configures-env-access", "configures-env-set", "configures-env-read", "configures-key-symbol", "configures-helm-values")
	if !slices.Equal(got, want) {
		t.Fatalf("kinds = %v; want %v", got, want)
	}
}

func TestTokensAreSortedAndUnique(t *testing.T) {
	t.Parallel()

	tokens := edgekind.Tokens()
	if !slices.IsSorted(tokens) || len(slices.Compact(slices.Clone(tokens))) != len(tokens) {
		t.Fatalf("tokens = %v", tokens)
	}

	for _, token := range tokens {
		if strings.TrimSpace(token) == "" {
			t.Fatalf("empty token in %v", tokens)
		}
	}
}

func TestNativeKindsAreTheReverseOfTheNormalizationTable(t *testing.T) {
	t.Parallel()

	if got := edgekind.NativeKinds(observation.KindDeploysArtifact); !slices.Equal(got, []string{"DEPLOYS"}) {
		t.Fatalf("deploys-artifact native kinds = %v", got)
	}

	if got := edgekind.NativeKinds(observation.KindAmbiguousUsage); !slices.Equal(got, []string{"USAGE"}) {
		t.Fatalf("ambiguous-usage native kinds = %v", got)
	}
}
