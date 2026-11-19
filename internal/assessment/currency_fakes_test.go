package assessment_test

import (
	"context"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/impact"
)

// fakeFacts is a FactSource over fixed matches.
type fakeFacts struct {
	// gap answers a selector with facet restrictions for one kind, like a selector that depends on an unresolved binding.
	gap *struct {
		kind  impact.Kind
		match impact.Match
	}
	matches    map[impact.Kind]impact.Match
	files      map[string]string
	evaluators map[string]string
	profile    string
}

func (f *fakeFacts) Items(_ context.Context, sel impact.Selector) (impact.Match, error) { //nolint:gocritic // implements impact.FactSource.
	if f.answersWithGap(&sel) {
		return f.gap.match, nil
	}

	if m, ok := f.matches[sel.Kind]; ok {
		return m, nil
	}

	return impact.Match{State: impact.MatchEmpty, Items: []impact.Item{}}, nil
}

// answersWithGap reports whether the selector is the one that the gap answers: a facet restriction on its kind.
func (f *fakeFacts) answersWithGap(sel *impact.Selector) bool {
	if f.gap == nil || len(sel.Match) == 0 {
		return false
	}

	return sel.Kind == f.gap.kind
}

func (f *fakeFacts) FileDigest(_ context.Context, _, path string) (digest string, found bool, err error) {
	digest, found = f.files[path]

	return digest, found, nil
}

func (f *fakeFacts) Profile() impact.CapabilityProfile {
	return impact.CapabilityProfile{ID: f.profile, Kinds: impact.Kinds(), Selectors: map[impact.Kind][]string{impact.KindPublicEntryPoint: {"host"}}}
}

func (f *fakeFacts) EvaluatorVersions() map[string]string {
	if f.evaluators != nil {
		return f.evaluators
	}

	return map[string]string{impact.EvaluatorNormalization: "1", "cbm": "0.12.1"}
}

// entryPoint gives a public-entry-point item.
func entryPoint(identity, host string) impact.Item {
	return impact.V1().NewItem(impact.KindPublicEntryPoint, identity, impact.Facets{"host": {host}, "path": {"/"}}, nil, nil)
}

func found(items ...impact.Item) impact.Match {
	return impact.Match{State: impact.MatchFound, Items: items}
}

func newFacts(items ...impact.Item) *fakeFacts {
	return &fakeFacts{
		matches: map[impact.Kind]impact.Match{impact.KindPublicEntryPoint: found(items...)}, profile: "cbm-0.12.1",
		files: map[string]string{"docs/security/edge-review.md": digestA},
	}
}

type lossyFacts struct{ *fakeFacts }

func (f *lossyFacts) Profile() impact.CapabilityProfile {
	return impact.CapabilityProfile{ID: "cbm-0.12.1", Kinds: []impact.Kind{impact.KindRoleBinding}}
}

// factsOf gives the fake fact source of the input.
func factsOf(t *testing.T, in *assessment.EvalInput) *fakeFacts {
	t.Helper()

	f, ok := in.Facts.(*fakeFacts)
	if !ok {
		t.Fatalf("the fact source is %T", in.Facts)
	}

	return f
}
