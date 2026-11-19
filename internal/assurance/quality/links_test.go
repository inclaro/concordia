package quality_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/diag"
)

// linkSet loads the fixture with the `addressedBy` entries of a scenario and a finding.
func linkSet(t *testing.T, file string) *quality.Set {
	t.Helper()

	got, errs := loadFixtures(t, file)
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}

	return got
}

// resolveFixtureLinks resolves the links of a fixture against the decision states and fails the test on any error.
func resolveFixtureLinks(t *testing.T, file string, states map[string]quality.DecisionState) []quality.Link {
	t.Helper()

	links, errs := quality.ResolveLinks(linkSet(t, file), states, adr.CanonicalID)
	if len(errs) > 0 {
		t.Fatalf("%s: errors: %v", file, errs)
	}

	return links
}

func expectScenarioLink(t *testing.T, scenario *quality.Link) {
	t.Helper()

	expectLoaded(t, "scenario link owner kind", scenario.OwnerKind, "scenario")
	expectLoaded(t, "scenario link owner", scenario.Owner, "QS-FAILOVER")
	expectLoaded(t, "scenario link ADR", scenario.ADR, "ADR-0007")
	expectLoaded(t, "scenario link canonical", scenario.Canonical, "ADR-7")
	expectLoaded(t, "scenario link status", scenario.Status, quality.LinkActive)
	expectLoaded(t, "scenario link counts as active", scenario.CountsAsActive(), true)
	expectLoaded(t, "scenario link review rationale", scenario.ReviewRationale, false)

	if scenario.Location.Line == 0 {
		t.Errorf("scenario link = %+v", scenario)
	}
}

func TestResolveLinksActiveDecisionMatchesByCanonicalID(t *testing.T) {
	t.Parallel()

	// valid.yaml writes ADR-0007 in a scenario and ADR-7 in a finding; the register holds ADR-7.
	links := resolveFixtureLinks(t, "valid.yaml", map[string]quality.DecisionState{"ADR-7": quality.DecisionActive})
	if len(links) != 2 {
		t.Fatalf("links = %+v", links)
	}

	expectScenarioLink(t, &links[0])

	finding := &links[1]
	expectLoaded(t, "finding link owner kind", finding.OwnerKind, "finding")
	expectLoaded(t, "finding link status", finding.Status, quality.LinkActive)
}

// linkOutcome is what the resolution of a link decides: the fields that the state of the decision determines.
type linkOutcome struct {
	status   quality.LinkStatus
	detail   string
	state    quality.DecisionState
	review   bool
	isActive bool
}

func outcomeOf(l *quality.Link) linkOutcome {
	return linkOutcome{status: l.Status, detail: l.Detail, state: l.State, review: l.ReviewRationale, isActive: l.CountsAsActive()}
}

func TestResolveLinksDecisionStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state quality.DecisionState
		want  linkOutcome
	}{
		{quality.DecisionActive, linkOutcome{
			status: quality.LinkActive, state: quality.DecisionActive, isActive: true}},
		{quality.DecisionSuperseded, linkOutcome{
			status: quality.LinkNotActive, state: quality.DecisionSuperseded, review: true}},
		{quality.DecisionInactive, linkOutcome{
			status: quality.LinkNotActive, state: quality.DecisionInactive, review: true}},
		{quality.DecisionAmbiguous, linkOutcome{
			status: quality.LinkUnknown, state: quality.DecisionAmbiguous, detail: "ambiguous"}},
		{quality.DecisionUnknown, linkOutcome{
			status: quality.LinkUnknown, state: quality.DecisionUnknown, detail: "unknown"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			t.Parallel()

			links := resolveFixtureLinks(t, "valid.yaml", map[string]quality.DecisionState{"ADR-7": tt.state})
			for i := range links {
				if got := outcomeOf(&links[i]); got != tt.want {
					t.Errorf("link = %+v", links[i])
				}
			}
		})
	}
}

func TestResolveLinksWithoutDecisionSummary(t *testing.T) {
	t.Parallel()

	// The file loads, and with no decision summary every link, even one of an unregistered decision, is unknown.
	want := linkOutcome{status: quality.LinkUnknown, detail: "adr-governance-not-available"}

	for _, file := range []string{"valid.yaml", "adr-unregistered.yaml"} {
		links := resolveFixtureLinks(t, file, nil)
		if len(links) == 0 {
			t.Fatalf("%s: no links", file)
		}

		for i := range links {
			if got := outcomeOf(&links[i]); got != want {
				t.Errorf("%s: link = %+v", file, links[i])
			}
		}
	}
}

func expectUnregisteredDecisionRejected(t *testing.T, name string, states map[string]quality.DecisionState) {
	t.Helper()

	links, errs := quality.ResolveLinks(linkSet(t, "adr-unregistered.yaml"), states, adr.CanonicalID)
	if len(errs) != 1 || len(links) != 0 {
		t.Fatalf("%s: errors=%v links=%v", name, errs, links)
	}

	got := errs[0]
	expectLoaded(t, name+": code", got.Diagnostic.Code, diag.QUA002)
	expectLoaded(t, name+": exit code", got.ExitCode, diag.InvalidInput)

	src := got.Diagnostic.Source
	if src == nil {
		t.Fatalf("%s: error = %+v, want a source", name, got)
	}

	expectLoaded(t, name+": source file", src.URI, "adr-unregistered.yaml")
	expectLoaded(t, name+": source line", src.Line, 15)
}

func TestResolveLinksUnregisteredDecisionIsInvalidWithSummary(t *testing.T) {
	t.Parallel()

	for name, states := range map[string]map[string]quality.DecisionState{
		"register with other decisions": {"ADR-7": quality.DecisionActive},
		"empty register":                {},
	} {
		expectUnregisteredDecisionRejected(t, name, states)
	}
}

func TestMalformedADRIdentifierIsInvalidWhetherOrNotGovernanceIsEvaluated(t *testing.T) {
	t.Parallel()

	// The form is checked by the loader, before any decision summary exists: `ADR_7` never reaches ResolveLinks.
	_, errs := loadFixtures(t, "adr-malformed.yaml")
	if len(errs) != 1 {
		t.Fatalf("errors = %v", errs)
	}

	expectLoaded(t, "code", errs[0].Diagnostic.Code, diag.QUA001)
	expectLoaded(t, "exit code", errs[0].ExitCode, diag.InvalidInput)
}
