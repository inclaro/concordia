package adr_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
)

// registerSpellings are the two spellings of the register IDs of ADR-3 and ADR-7: both have one canonical ID.
var registerSpellings = [][2]string{{"ADR-0003", "ADR-0007"}, {"ADR-3", "ADR-7"}}

// registerOf writes a register with an entry for ADR-3 and, when both is set, one for ADR-7.
func registerOf(spelling [2]string, both bool) string {
	text := `  - id: ` + spelling[0] + `
    governs: [{ref: 'calm:node:orders-db'}]
    enforcement: required
    enforcedBy: [{rule: CORE-DB-001, pack: {id: core, version: 1.0.0}}]
`
	if both {
		text += `  - id: ` + spelling[1] + `
    governs: [{ref: 'calm:node:orders-db'}]
`
	}

	return text
}

// resultOf gives the canonical JSON of the summary and the findings of an evaluation: everything a user sees except
// the register digest in the inputs, which follows the bytes of the file.
func resultOf(t *testing.T, out *conformance.DecisionEvaluation) string {
	t.Helper()

	data, err := canonjson.Marshal(map[string]any{"summary": out.Summary, "findings": out.Findings})
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// Scenario: Register spelling does not change fingerprints.
// A12 and the A13 history anomalies give the same result, fingerprints included, whether the register writes
// `ADR-0003` and `ADR-0007` or `ADR-3` and `ADR-7`: every decision is named in canonical form.
func TestFingerprintsDoNotDependOnTheRegisterSpelling(t *testing.T) {
	t.Parallel()

	setups := map[string]func(spelling [2]string) *world{
		"A12 with an unregistered successor": func(s [2]string) *world {
			return a12World(t, registerOf(s, false))
		},
		"A12 with a registered successor": func(s [2]string) *world {
			return a12World(t, registerOf(s, true))
		},
		"A13 cycle": func(s [2]string) *world {
			w := a12World(t, registerOf(s, true))
			w.supersede("1", "2")

			return w
		},
		"A13 missing target": func(s [2]string) *world {
			w := a12World(t, registerOf(s, true))
			w.edges = nil
			w.nodes["cbm:p:1"].Properties["status"] = "superseded"
			w.nodes["cbm:p:1"].Properties["superseded_by"] = "0099-unknown.md"

			return w
		},
		"A13 contradictory status": func(s [2]string) *world {
			w := a12World(t, registerOf(s, true))
			w.nodes["cbm:p:1"].Properties["status"] = "superseded"
			w.nodes["cbm:p:2"].Properties["status"] = "rejected"

			return w
		},
	}

	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var first []string

			firstResult := ""

			for i, spelling := range registerSpellings {
				out := setup(spelling).evaluate()

				got := fingerprintsOf(out)
				if len(got) == 0 {
					t.Fatalf("register %v: no findings, the setup shows no fingerprint", spelling)
				}

				if i == 0 {
					first, firstResult = got, resultOf(t, out)

					continue
				}

				if !slices.Equal(got, first) {
					t.Errorf("fingerprints with %s differ from %s:\n got %v\nwant %v",
						strings.Join(spelling[:], ", "), strings.Join(registerSpellings[0][:], ", "), got, first)
				}

				if res := resultOf(t, out); res != firstResult {
					t.Errorf("result with %s differs from %s:\n got %s\nwant %s",
						strings.Join(spelling[:], ", "), strings.Join(registerSpellings[0][:], ", "), res, firstResult)
				}
			}
		})
	}
}

// Scenario: Registered decision with leading zeros.
// The register writes `ADR-0003` and `adr-007`; every name in the summary and the findings is the canonical ID.
func TestDecisionsAreNamedByTheirCanonicalID(t *testing.T) {
	t.Parallel()

	out := a12World(t, registerOf([2]string{"ADR-0003", "adr-007"}, true)).evaluate()

	ids := make([]string, 0, len(out.Summary.Decisions))
	for _, d := range out.Summary.Decisions {
		ids = append(ids, d.ID)
	}

	evalWantSlice(t, "decision ids (ADR-3 and ADR-7)", ids, []string{"ADR-3", "ADR-7"})

	links := out.Summary.History.Links
	if len(links) != 1 {
		t.Fatalf("history links = %+v, want ADR-7 to ADR-3", links)
	}

	if [2]string{links[0].From, links[0].To} != [2]string{"ADR-7", "ADR-3"} {
		t.Errorf("history links = %+v, want ADR-7 to ADR-3", links)
	}

	for i := range out.Findings {
		if f := &out.Findings[i]; namesRegisterSpelling(f) {
			t.Errorf("finding %s names a register spelling: %q, decision %+v", f.RuleID, f.Message, f.Decision)
		}
	}
}

// namesRegisterSpelling says whether a finding shows a register spelling instead of the canonical ID.
func namesRegisterSpelling(f *conformance.Finding) bool {
	inMessage := slices.ContainsFunc([]string{"ADR-0003", "adr-007"}, func(spelling string) bool {
		return strings.Contains(f.Message, spelling)
	})

	return inMessage || !hasCanonicalDecisionID(f.Decision)
}

// hasCanonicalDecisionID says whether the decision block of a finding, if any, names ADR-3 or ADR-7.
func hasCanonicalDecisionID(decision *conformance.FindingDecision) bool {
	return decision == nil || slices.Contains([]string{"ADR-3", "ADR-7"}, decision.ID)
}
