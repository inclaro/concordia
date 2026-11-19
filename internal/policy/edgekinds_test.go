package policy_test

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

func TestEdgeKindAliasesMapPolicyTokens(t *testing.T) {
	t.Parallel()

	cases := map[string][]observation.CanonicalEdgeKind{
		"IMPORTS":        {observation.KindImports},
		"USES_TYPE":      {observation.KindUsesType},
		"USAGE":          {observation.KindAmbiguousUsage},
		"HTTP_CALLS":     {observation.KindCallsService},
		"CALL_REFERENCE": {observation.KindReferencesCallable},
		"deploys":        {observation.KindDeploysArtifact},
		"configures": {
			observation.KindConfiguresEnvAccess, observation.KindConfiguresEnvSet, observation.KindConfiguresEnvRead,
			observation.KindConfiguresKeySymbol, observation.KindConfiguresHelmValues,
		},
	}

	for token, want := range cases {
		got, ok := policy.ResolveEdgeKind(token)
		if !ok {
			t.Errorf("ResolveEdgeKind(%q) is unknown", token)

			continue
		}

		slices.Sort(want)

		if !slices.Equal(got, want) {
			t.Errorf("ResolveEdgeKind(%q) = %v, want %v", token, got, want)
		}
	}
}

func TestEdgeKindsAcceptCanonicalKebabCase(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"calls-service", "contains", "placed-in"} {
		want := []observation.CanonicalEdgeKind{observation.CanonicalEdgeKind(token)}

		got, ok := policy.ResolveEdgeKind(token)
		if !ok || !slices.Equal(got, want) {
			t.Errorf("ResolveEdgeKind(%q) = %q, %v", token, got, ok)
		}
	}
}

func TestEdgeKindsRejectOtherValues(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"NOPE", "constructor", "unsupported", "ambiguous"} {
		if got, ok := policy.ResolveEdgeKind(token); ok {
			t.Errorf("ResolveEdgeKind(%q) = %q, want unknown", token, got)
		}
	}

	_, err := policy.ParseEdgeKind("NOPE", nil)

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v", err)
	}

	if failure.Diagnostic.Code != diag.POL004 || failure.ExitCode != diag.InvalidInput {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestParseEdgeKindsSortsAndRemovesDuplicates(t *testing.T) {
	t.Parallel()

	got, err := policy.ParseEdgeKinds([]string{"IMPORTS", "CALLS", "imports"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if want := []observation.CanonicalEdgeKind{observation.KindCalls, observation.KindImports}; !slices.Equal(got, want) {
		t.Fatalf("kinds = %v", got)
	}
}

func TestNativeKindsForIsTheReverseOfTheAdapterTable(t *testing.T) {
	t.Parallel()

	cases := map[observation.CanonicalEdgeKind][]string{
		observation.KindImports:  {"IMPORTS"},
		observation.KindDefines:  {"DEFINES", "DEFINES_METHOD"},
		observation.KindUsesType: {},
		observation.KindPlacedIn: {},
	}

	for kind, want := range cases {
		if got := policy.NativeKindsFor(kind); !slices.Equal(got, want) {
			t.Errorf("NativeKindsFor(%q) = %v, want %v", kind, got, want)
		}
	}
}

// documentSection gives the lines of the section that starts with heading, up to the next "## " heading.
func documentSection(t *testing.T, path, heading string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var section []string

	inSection := false

	for line := range strings.SplitSeq(string(data), "\n") {
		switch {
		case line == heading:
			inSection = true
		case inSection && strings.HasPrefix(line, "## "):
			return section
		case inSection:
			section = append(section, line)
		}
	}

	if !inSection {
		t.Fatalf("%s: no section %q", path, heading)
	}

	return section
}

// firstCellTokens gives the backticked tokens of the first cell of each data
// row of the first table of the lines whose header starts with headerPrefix.
func firstCellTokens(t *testing.T, lines []string, headerPrefix string) []string {
	t.Helper()

	var tokens []string

	inTable := false

	for _, line := range lines {
		if !inTable {
			inTable = strings.HasPrefix(line, headerPrefix)

			continue
		}

		if strings.HasPrefix(line, "|---") {
			continue
		}

		if !strings.HasPrefix(line, "|") {
			return tokens
		}

		tokens = append(tokens, backtickedTokensOfFirstCell(line)...)
	}

	return tokens
}

// backtickedTokensOfFirstCell gives the backticked tokens of the first cell of a table row.
func backtickedTokensOfFirstCell(row string) []string {
	var tokens []string

	parts := strings.Split(strings.Split(row, "|")[1], "`")
	for i := 1; i < len(parts); i += 2 {
		tokens = append(tokens, parts[i])
	}

	return tokens
}

func TestDocumentedKindListMatchesCanonicalEdgeKinds(t *testing.T) {
	t.Parallel()

	section := documentSection(t, "../../docs/reference/architecture.md", "## Canonical edge kinds")
	documented := firstCellTokens(t, section, "| Kind |")

	want := make([]string, 0, len(observation.CanonicalEdgeKinds()))
	for _, kind := range observation.CanonicalEdgeKinds() {
		want = append(want, string(kind))
	}

	slices.Sort(documented)
	slices.Sort(want)

	if slices.Equal(documented, want) {
		return
	}

	reportEdgeKindDocMismatch(t, documented, want)
}

// reportEdgeKindDocMismatch reports every difference between the documented kinds and the canonical kinds.
func reportEdgeKindDocMismatch(t *testing.T, documented, want []string) {
	t.Helper()

	for _, kind := range edgeKindsMissingFrom(want, documented) {
		t.Errorf("canonical kind %q is not documented", kind)
	}

	for _, kind := range edgeKindsMissingFrom(documented, want) {
		t.Errorf("documented kind %q is not a canonical kind", kind)
	}

	if len(documented) != len(want) {
		t.Errorf("documented %d kinds, want %d (duplicate rows?)", len(documented), len(want))
	}
}

// edgeKindsMissingFrom gives the kinds of kinds that are not in other.
func edgeKindsMissingFrom(kinds, other []string) []string {
	return slices.DeleteFunc(slices.Clone(kinds), func(kind string) bool { return slices.Contains(other, kind) })
}

func TestDocumentedAliasTokensResolve(t *testing.T) {
	t.Parallel()

	section := documentSection(t, "../../docs/reference/architecture.md", "## Edge-kind tokens")

	tokens := firstCellTokens(t, section, "| Alias |")
	if len(tokens) == 0 {
		t.Fatal("no documented alias tokens")
	}

	for _, token := range tokens {
		if _, ok := policy.ResolveEdgeKind(token); !ok {
			t.Errorf("documented alias %q does not resolve", token)
		}
	}
}
