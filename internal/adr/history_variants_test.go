package adr_test

import (
	"os"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/adr"
)

type historyVariant struct {
	ID        string `yaml:"id"`
	Decisions []struct {
		Path         string `yaml:"path"`
		AdrID        string `yaml:"adrId"`
		Title        string `yaml:"title"`
		Status       string `yaml:"status"`
		StatusText   string `yaml:"statusText"`
		Date         string `yaml:"date"`
		SupersededBy string `yaml:"supersededBy"`
		Line         int    `yaml:"line"`
	} `yaml:"decisions"`
	Supersedes []struct {
		From string `yaml:"from"`
		To   string `yaml:"to"`
		Line int    `yaml:"line"`
	} `yaml:"supersedes"`
	Expect struct {
		Links []struct {
			Successor, Predecessor, Provenance, Effect string
		} `yaml:"links"`
		Anomalies []struct {
			Kind  string   `yaml:"kind"`
			Value string   `yaml:"value"`
			Paths []string `yaml:"paths"`
		} `yaml:"anomalies"`
		Applicability map[string]string `yaml:"applicability"`
		Matches       map[string]string `yaml:"matches"`
		Heads         []string          `yaml:"heads"`
		StatusLags    []string          `yaml:"statusLags"`
		Ambiguous     bool              `yaml:"ambiguous"`
	} `yaml:"expect"`
}

func loadHistoryVariants(t *testing.T) []historyVariant {
	t.Helper()

	data, err := os.ReadFile("testdata/variants.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var file struct {
		Variants []historyVariant `yaml:"variants"`
	}

	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}

	return file.Variants
}

func (v *historyVariant) evidence() (evidence *adr.DecisionEvidence, paths []string) {
	evidence = &adr.DecisionEvidence{
		Supported: true, ExtractionComplete: true, Supersedes: []adr.SupersedesEdge{}, Mentions: []adr.Mention{}, Stale: []string{},
	}

	for _, d := range v.Decisions {
		evidence.Decisions = append(evidence.Decisions, adr.EvidenceDecision{
			Path: d.Path, AdrID: d.AdrID, Label: adr.CanonicalID(d.AdrID), Title: d.Title, Status: d.Status,
			StatusText: d.StatusText, Date: d.Date, SupersededBy: d.SupersededBy, Line: d.Line,
		})
		paths = append(paths, d.Path)
	}

	for _, e := range v.Supersedes {
		evidence.Supersedes = append(evidence.Supersedes, adr.SupersedesEdge{From: e.From, To: e.To, Line: e.Line})
	}

	return evidence, paths
}

func TestHistoryVariants(t *testing.T) {
	variants := loadHistoryVariants(t)
	if len(variants) != 17 {
		t.Fatalf("variants = %d, want SUP-V01 to SUP-V17", len(variants))
	}

	for i := range variants {
		variant := &variants[i]

		t.Run(variant.ID, func(t *testing.T) {
			evidence, paths := variant.evidence()
			hist := adr.BuildHistory(evidence, nil, nil, paths)

			variant.wantLinks(t, hist)
			variant.wantAnomalies(t, hist)

			heads, lags := variant.wantApplicability(t, hist, paths)

			slices.Sort(heads)

			if !slices.Equal(heads, variant.Expect.Heads) || !slices.Equal(lags, variant.Expect.StatusLags) {
				t.Fatalf("heads %v lags %v, want heads %v lags %v", heads, lags, variant.Expect.Heads, variant.Expect.StatusLags)
			}
		})
	}
}

// wantLinks wants the links of the history to be the expected ones.
func (v *historyVariant) wantLinks(t *testing.T, hist *adr.History) {
	t.Helper()

	links := make([]string, 0, len(hist.Links))
	for _, l := range hist.Links {
		links = append(links, l.FromPath+">"+l.ToPath+":"+string(l.Provenance)+":"+string(l.Effect))
	}

	wantLinks := make([]string, 0, len(v.Expect.Links))
	for _, l := range v.Expect.Links {
		wantLinks = append(wantLinks, l.Successor+">"+l.Predecessor+":"+l.Provenance+":"+l.Effect)
	}

	slices.Sort(wantLinks)

	if !slices.Equal(links, wantLinks) {
		t.Fatalf("links = %v, want %v", links, wantLinks)
	}
}

// wantAnomalies wants the anomalies of the history to be the expected ones.
func (v *historyVariant) wantAnomalies(t *testing.T, hist *adr.History) {
	t.Helper()

	if len(hist.Anomalies) != len(v.Expect.Anomalies) {
		t.Fatalf("anomalies = %+v, want %+v", hist.Anomalies, v.Expect.Anomalies)
	}

	for j, want := range v.Expect.Anomalies {
		got := hist.Anomalies[j]
		if string(got.Kind) != want.Kind || got.Value != want.Value || !slices.Equal(got.Paths, want.Paths) {
			t.Fatalf("anomaly %d = %+v, want %+v", j, got, want)
		}
	}
}

// wantApplicability wants the applicability and the ambiguity of every path to be the expected ones. It gives the
// unsorted heads (paths nothing supersedes) and the paths whose status lags.
func (v *historyVariant) wantApplicability(t *testing.T, hist *adr.History, paths []string) (heads, lags []string) {
	t.Helper()

	for _, p := range paths {
		state, _, lag := hist.Applicability(p)
		if string(state) != v.Expect.Applicability[p] {
			t.Fatalf("%s = %s, want %s", p, state, v.Expect.Applicability[p])
		}

		if hist.Ambiguous(p) != v.Expect.Ambiguous {
			t.Fatalf("Ambiguous(%s) = %v, want %v", p, hist.Ambiguous(p), v.Expect.Ambiguous)
		}

		if lag {
			lags = append(lags, p)
		}

		if v.isHead(hist, p, state) {
			heads = append(heads, p)
		}
	}

	return heads, lags
}

// isHead says whether no effective link supersedes the path, in a history without ambiguity.
func (v *historyVariant) isHead(hist *adr.History, p string, state adr.ApplicabilityState) bool {
	return !v.Expect.Ambiguous && state != adr.ApplicabilitySuperseded && !hasEffectiveSuccessor(hist, p)
}

// hasEffectiveSuccessor says whether an effective link names the path as its predecessor.
func hasEffectiveSuccessor(hist *adr.History, path string) bool {
	for _, l := range hist.Links {
		if l.ToPath == path && l.Effect == adr.EffectEffective {
			return true
		}
	}

	return false
}

func TestHistoryVariantsRegisterMatchByCanonicalLabel(t *testing.T) {
	for _, variant := range loadHistoryVariants(t) {
		for id, want := range variant.Expect.Matches {
			evidence, _ := variant.evidence()
			entry := adr.Entry{ID: adr.CanonicalID(id)}
			git := factsFrom(t, nil, "", "", fakeGit(testRev, pathsOfEvidence(evidence)))

			got := adr.EvaluatePresence([]adr.Entry{entry}, evidence, git)[adr.EntryKey(&entry)]
			if got.State != adr.PresenceCommitted || got.Path != want {
				t.Fatalf("%s: %s = %+v, want committed at %s", variant.ID, id, got, want)
			}
		}
	}
}

func pathsOfEvidence(evidence *adr.DecisionEvidence) []string {
	out := make([]string, 0, len(evidence.Decisions))
	for i := range evidence.Decisions {
		out = append(out, evidence.Decisions[i].Path)
	}

	return out
}

func TestHistoryVariantsAreDeterministicAcrossInputOrder(t *testing.T) {
	for _, variant := range loadHistoryVariants(t) {
		evidence, paths := variant.evidence()
		want := canonicalHistory(t, adr.BuildHistory(evidence, nil, nil, paths), paths)

		slices.Reverse(evidence.Decisions)
		slices.Reverse(evidence.Supersedes)
		slices.Reverse(paths)

		if got := canonicalHistory(t, adr.BuildHistory(evidence, nil, nil, paths), paths); got != want {
			t.Fatalf("%s: reversed input changed the output", variant.ID)
		}
	}
}
