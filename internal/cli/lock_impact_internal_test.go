package cli

import (
	"maps"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
)

func impactProfile(t *testing.T, families string) *observation.CapabilityProfile {
	t.Helper()

	profile, err := observation.ParseProfile([]byte("id: test-profile\nprovider: cbm\nbuild: {version: 9.9.9, adapterSchema: concordia-cbm/v2}\nfamilies:\n" + families))
	if err != nil {
		t.Fatal(err)
	}

	return profile
}

func TestCapabilityImpactListsTheFamiliesAProfileGainsWithTheirFixtures(t *testing.T) {
	t.Parallel()

	locked := impactProfile(t, `  - {family: imports, level: supported, maturity: stable, fixtures: [commerce.json]}
  - {family: deploys-artifact, level: partial, maturity: advisory}
`)
	current := impactProfile(t, `  - {family: imports, level: supported, maturity: stable, fixtures: [commerce.json]}
  - {family: deploys-artifact, level: supported, maturity: stable, fixtures: [infra.json]}
  - {family: uses-image, level: partial, maturity: advisory, fixtures: [infra.json, infra-reinclude-deploy.json]}
`)

	impact := capabilityImpactBetween(locked, current)

	if !impact.Comparable || len(impact.Regressions) != 0 {
		t.Fatalf("comparable = %v, regressions = %v", impact.Comparable, impact.Regressions)
	}

	got := map[string]string{}
	for _, f := range impact.Families {
		got[f.Family] = f.Change
	}

	want := map[string]string{"deploys-artifact": changeImproved, "uses-image": changeAdded}
	if !maps.Equal(got, want) {
		t.Fatalf("families = %v, want %v", got, want)
	}

	if !slices.Equal(impact.Fixtures, []string{"infra-reinclude-deploy.json", "infra.json"}) {
		t.Errorf("fixtures = %v", impact.Fixtures)
	}

	if !slices.Equal(impact.ProfileFixtures, []string{"commerce.json", "infra-reinclude-deploy.json", "infra.json"}) {
		t.Errorf("profile fixtures = %v", impact.ProfileFixtures)
	}
}

func TestCapabilityImpactListsRegressionsAndRefusesEquivalence(t *testing.T) {
	t.Parallel()

	wide := impactProfile(t, "  - {family: imports, level: supported, maturity: advisory}\n  - {family: calls, level: supported, maturity: advisory}\n")
	narrow := impactProfile(t, "  - {family: imports, level: supported, maturity: advisory}\n  - {family: calls, level: partial, maturity: advisory}\n")

	impact := capabilityImpactBetween(wide, narrow)
	if len(impact.Regressions) != 1 {
		t.Fatalf("regressions = %+v", impact.Regressions)
	}

	if regression := impact.Regressions[0]; regression.Family != "calls" || regression.Now != observation.SupportPartial {
		t.Fatalf("regressions = %+v", impact.Regressions)
	}

	if len(impact.Families) != 1 || impact.Families[0].Change != changeReduced {
		t.Fatalf("families = %+v", impact.Families)
	}

	verdict := judgeEquivalence(impact, nil)
	if verdict.Shown || verdict.Statement != statementNotShown {
		t.Fatalf("equivalence = %+v", verdict)
	}
}

// equivalenceRecorded gives a recorded run and a rerun of it that differs only in its graph.
func equivalenceRecorded() (recorded, same *runSnapshot) {
	recorded = &runSnapshot{
		RunID: "r1", Architecture: "sha256:arch", Verdicts: map[string]string{"CO-A": "pass"},
		Findings: []findingRef{{RuleID: "CO-A", Fingerprint: "sha256:1"}},
		Policies: []conformance.InputRef{{Path: "p.yaml", Digest: "sha256:p"}},
	}

	rerun := *recorded
	rerun.Graph = "sha256:graph"

	return recorded, &rerun
}

func TestEquivalenceIsShownOnlyForAnUnchangedLatestRun(t *testing.T) {
	t.Parallel()

	recorded, same := equivalenceRecorded()

	latest, err := compareRuns(recorded, same)
	if err != nil {
		t.Fatal(err)
	}

	if verdict := judgeEquivalence(nil, latest); !verdict.Shown || verdict.Statement != statementShown {
		t.Fatalf("unchanged run: %+v", verdict)
	}

	changed := *same
	changed.Findings = []findingRef{{RuleID: "CO-A", Fingerprint: "sha256:2"}}
	changed.Verdicts = map[string]string{"CO-A": "violation"}

	latest, err = compareRuns(recorded, &changed)
	if err != nil {
		t.Fatal(err)
	}

	counts := [3]int{len(latest.FindingsRemoved), len(latest.FindingsAdded), len(latest.VerdictChanges)}
	if counts != [3]int{1, 1, 1} {
		t.Fatalf("latest = %+v", latest)
	}

	verdict := judgeEquivalence(nil, latest)
	if verdict.Shown || len(verdict.Reasons) != 2 {
		t.Fatalf("changed run: %+v", verdict)
	}

	if verdict.Statement != statementNotShown {
		t.Fatalf("changed run: %+v", verdict)
	}
}
