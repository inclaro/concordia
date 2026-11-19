package conformance_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/checkresult"
	"github.com/inclaro/concordia/internal/assurance/checkresult/verdict"
	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/impact"
)

// The kinds `verification` and `quality-scenario` come from the assurance evidence (task 10.1, design D27).

const (
	rvResultDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	rvOtherDigest  = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// rvCheck is an admitted result of the check `unit-tests` in production.
func rvCheck(outcome, digest string, admissible bool) verdict.Admitted {
	return verdict.Admitted{
		Result: &checkresult.Result{
			Check: "unit-tests", Outcome: outcome, Environment: "production", Covers: []string{"api-service"},
		},
		Path: "results/unit-tests.json", Digest: digest, Admission: assurance.Admission{Admissible: admissible},
	}
}

// rvScenario is a measured scenario with the threshold.
func rvScenario(threshold float64, line int) *quality.Scenario {
	return &quality.Scenario{
		ID: "QS-FAILOVER", Mode: quality.ModeMeasured, Enforcement: quality.EnforcementMandatory, Area: quality.AreaAvailability,
		Environment: "production", Scope: []string{"api-service"}, Owner: "platform",
		Criterion: quality.Criterion{Metric: "failover-seconds", Unit: "s", Comparison: quality.CompareLTE, Threshold: threshold},
		Location:  quality.Location{File: "quality/a.yaml", Line: line},
	}
}

func rvAssurance(results []verdict.Admitted, scenarios ...*quality.Scenario) *conformance.ReviewAssurance {
	return &conformance.ReviewAssurance{CheckResults: results, Scenarios: &quality.Set{Scenarios: scenarios}}
}

// sealed gives the digest of the only item of a match.
func sealed(t *testing.T, match *impact.Match) string {
	t.Helper()

	if len(match.Items) != 1 {
		t.Fatalf("items = %v (state %s)", identities(match), match.State)
	}

	got, err := impact.V1().Seal(match.Items[0], noFilesForFacts)
	if err != nil {
		t.Fatal(err)
	}

	return got.Digest
}

func noFilesForFacts(string) (digest string, found bool, err error) { return "", false, nil }

// Scenario: A verification item per admitted result. The item has the identity `check:<check>@<environment>`, the
// fields `subject` and `result`, and the selector facets `check` and `subject`.
func TestReviewFactsVerificationItemMatches(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{assurance: rvAssurance([]verdict.Admitted{rvCheck("passed", rvResultDigest, true)})}).facts(t)

	for name, sel := range map[string]impact.Selector{
		"all":         {Kind: impact.KindVerification},
		"by check":    {Kind: impact.KindVerification, Match: map[string][]string{"check": {"unit-tests"}}},
		"by subject":  {Kind: impact.KindVerification, Match: map[string][]string{"subject": {"api-service"}}},
		"by identity": {Kind: impact.KindVerification, Identities: []string{"check:unit-tests@production"}},
		"by element":  {Kind: impact.KindVerification, CalmNodes: []string{"api-service"}},
		"production":  {Kind: impact.KindVerification, Environment: "production"},
	} {
		match := items(t, facts, &sel)
		if match.State != impact.MatchFound || !slices.Equal(identities(&match), []string{"check:unit-tests@production"}) {
			t.Fatalf("%s: %+v", name, match)
		}
	}

	item := items(t, facts, &impact.Selector{Kind: impact.KindVerification}).Items[0]
	if !slices.Equal(item.Facets["subject"], []string{"api-service"}) || !slices.Equal(item.Facets["result"], []string{"passed " + rvResultDigest}) {
		t.Errorf("facets = %v", item.Facets)
	}
}

// Scenario: Empty match under complete loading. The check results were read completely and none matches.
func TestReviewFactsVerificationEmptyMatchUnderCompleteLoading(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{assurance: rvAssurance([]verdict.Admitted{rvCheck("passed", rvResultDigest, true)})}).facts(t)

	for name, sel := range map[string]impact.Selector{
		"another check":       {Kind: impact.KindVerification, Match: map[string][]string{"check": {"integration-tests"}}},
		"another environment": {Kind: impact.KindVerification, Environment: "staging"},
		"another subject":     {Kind: impact.KindVerification, Match: map[string][]string{"subject": {"a-service"}}},
	} {
		match := items(t, facts, &sel)
		if match.State != impact.MatchEmpty || len(match.Items) != 0 || match.Reason != "" {
			t.Errorf("%s: %+v", name, match)
		}
	}
}

// Scenario: Unreadable evidence source. A source that is not read completely makes the match unknown with
// `no-evidence-supplied`: an empty match would say that the item was removed.
func TestReviewFactsUnreadableSourcesGiveUnknown(t *testing.T) {
	t.Parallel()

	in := rvAssurance([]verdict.Admitted{rvCheck("passed", rvResultDigest, true)}, rvScenario(30, 4))
	in.ChecksUnreadable = "check result file could not be read: results/broken.json"
	in.ScenariosUnreadable = "no scenario source"

	facts := (&rvWorld{assurance: in}).facts(t)

	for _, kind := range []impact.Kind{impact.KindVerification, impact.KindQualityScenario} {
		match := items(t, facts, &impact.Selector{Kind: kind, Match: nil, Identities: []string{"nothing-here"}})
		if match.State != impact.MatchUnknown || match.Reason != impact.ReasonNoEvidenceSupplied || match.Detail == "" || len(match.Items) != 0 {
			t.Errorf("%s: %+v", kind, match)
		}
	}

	// The items that were read stay in the match.
	match := items(t, facts, &impact.Selector{Kind: impact.KindVerification})
	if match.State != impact.MatchUnknown || len(match.Items) != 1 {
		t.Errorf("verification with a partly read source: %+v", match)
	}
}

// Scenario: Inadmissible result is not an item. Evidence that is not admissible cannot stand behind an approval.
func TestReviewFactsInadmissibleResultIsNotAnItem(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{assurance: rvAssurance([]verdict.Admitted{rvCheck("passed", rvResultDigest, false)})}).facts(t)

	match := items(t, facts, &impact.Selector{Kind: impact.KindVerification})
	if match.State != impact.MatchEmpty || len(match.Items) != 0 {
		t.Errorf("match = %+v", match)
	}
}

// A changed outcome or a new result statement changes the digest of the item, so an approval of a dependent assessment
// is no longer current.
func TestReviewFactsVerificationDigestFollowsTheResult(t *testing.T) {
	t.Parallel()

	digestOf := func(outcome, statement string) string {
		facts := (&rvWorld{assurance: rvAssurance([]verdict.Admitted{rvCheck(outcome, statement, true)})}).facts(t)
		match := items(t, facts, &impact.Selector{Kind: impact.KindVerification})

		return sealed(t, &match)
	}

	base := digestOf("passed", rvResultDigest)
	if again := digestOf("passed", rvResultDigest); again != base {
		t.Errorf("the digest is not stable: %s then %s", base, again)
	}

	if digestOf("failed", rvResultDigest) == base || digestOf("passed", rvOtherDigest) == base {
		t.Error("a changed result did not change the digest")
	}
}

// Scenario: A quality scenario item per scenario. The item has the identity `scenario:<id>`, the fields `scenario` and
// `responseMeasure`, and is selected by the `identity` facet as assessment.Set.Declared writes it: the bare id.
func TestReviewFactsQualityScenarioItemMatches(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{assurance: rvAssurance(nil, rvScenario(30, 4))}).facts(t)

	for name, sel := range map[string]impact.Selector{
		"all":                   {Kind: impact.KindQualityScenario},
		"by bare id":            {Kind: impact.KindQualityScenario, Identities: []string{"QS-FAILOVER"}},
		"by identity":           {Kind: impact.KindQualityScenario, Identities: []string{"scenario:QS-FAILOVER"}},
		"by identity facet":     {Kind: impact.KindQualityScenario, Match: map[string][]string{"identity": {"QS-FAILOVER"}}},
		"by element":            {Kind: impact.KindQualityScenario, CalmNodes: []string{"api-service"}},
		"in the scenario's env": {Kind: impact.KindQualityScenario, Environment: "production"},
	} {
		match := items(t, facts, &sel)
		if match.State != impact.MatchFound || !slices.Equal(identities(&match), []string{"scenario:QS-FAILOVER"}) {
			t.Fatalf("%s: %+v", name, match)
		}
	}

	item := items(t, facts, &impact.Selector{Kind: impact.KindQualityScenario}).Items[0]
	if got := item.Facets["responseMeasure"]; !slices.Equal(got, []string{"failover-seconds lte 30 s"}) {
		t.Errorf("responseMeasure = %v", got)
	}

	if got := item.Facets["scenario"]; len(got) != 1 || !strings.HasPrefix(got[0], "sha256:") {
		t.Errorf("scenario = %v", got)
	}
}

// Scenario: Empty match under complete loading for scenarios.
func TestReviewFactsQualityScenarioEmptyMatchUnderCompleteLoading(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{assurance: rvAssurance(nil, rvScenario(30, 4))}).facts(t)

	match := items(t, facts, &impact.Selector{Kind: impact.KindQualityScenario, Identities: []string{"QS-OTHER"}})
	if match.State != impact.MatchEmpty || len(match.Items) != 0 {
		t.Errorf("match = %+v", match)
	}

	none := (&rvWorld{assurance: rvAssurance(nil)}).facts(t)

	if match := items(t, none, &impact.Selector{Kind: impact.KindQualityScenario}); match.State != impact.MatchEmpty {
		t.Errorf("no scenarios: %+v", match)
	}
}

// A changed response measure changes the digest; moving the scenario inside its file does not.
func TestReviewFactsScenarioDigestFollowsTheDefinition(t *testing.T) {
	t.Parallel()

	digestOf := func(sc *quality.Scenario) string {
		facts := (&rvWorld{assurance: rvAssurance(nil, sc)}).facts(t)
		match := items(t, facts, &impact.Selector{Kind: impact.KindQualityScenario})

		return sealed(t, &match)
	}

	base := digestOf(rvScenario(30, 4))

	if moved := digestOf(rvScenario(30, 40)); moved != base {
		t.Error("moving the scenario in its file changed the digest")
	}

	if changed := digestOf(rvScenario(60, 4)); changed == base {
		t.Error("a changed threshold did not change the digest")
	}

	other := rvScenario(30, 4)
	other.ExpectedResponse = "a replica takes over within a minute"

	if changed := digestOf(other); changed == base {
		t.Error("a changed definition did not change the digest")
	}
}
