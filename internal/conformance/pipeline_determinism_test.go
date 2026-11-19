package conformance_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/conformance"
)

func canonicalOf(t *testing.T, run *conformance.Run) []byte {
	t.Helper()

	text, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	return text
}

// OUT-06: two runs with the same recording, policies, and bindings give the same resultDigest and, without the
// volatile fields, the same canonical JSON.
func TestOut06RepeatedRunsGiveTheSameResultDigest(t *testing.T) {
	t.Parallel()

	s := newSetup(t, nil, nil)
	first := s.mustCheck(t, nil)
	second := s.mustCheck(t, func(o *conformance.CheckOptions) {
		o.Env.RunID = "20270101T000000Z-cafebabe"
		o.Deps.Now = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	})

	if first.Run.ID == second.Run.ID || first.Run.StartedAt == second.Run.StartedAt {
		t.Fatalf("the two runs must differ in the volatile fields: %+v %+v", first.Run, second.Run)
	}

	if !strings.HasPrefix(first.Summary.ResultDigest, "sha256:") || len(first.Summary.ResultDigest) != len("sha256:")+64 {
		t.Fatalf("resultDigest = %q", first.Summary.ResultDigest)
	}

	if first.Summary.ResultDigest != second.Summary.ResultDigest {
		t.Errorf("resultDigest differs: %s %s", first.Summary.ResultDigest, second.Summary.ResultDigest)
	}

	if stable(t, first) != stable(t, second) {
		t.Error("the canonical JSON differs after the volatile fields are removed")
	}
}

// assertDigestSame checks that an edit of a copy of the run leaves its result digest unchanged.
func assertDigestSame(t *testing.T, run *conformance.Run, name string, edit func(*conformance.Run)) {
	t.Helper()

	copied := *run
	edit(&copied)

	got, err := conformance.ResultDigest(&copied)
	if err != nil || got != run.Summary.ResultDigest {
		t.Errorf("%s changed the digest: %s (%v), want %s", name, got, err, run.Summary.ResultDigest)
	}
}

// assertDigestDifferent checks that an edit of a copy of the run changes its result digest.
func assertDigestDifferent(t *testing.T, run *conformance.Run, name string, edit func(*conformance.Run)) {
	t.Helper()

	copied := *run
	copied.Findings = append([]conformance.Finding{}, run.Findings...)
	edit(&copied)

	got, err := conformance.ResultDigest(&copied)
	if err != nil || got == run.Summary.ResultDigest {
		t.Errorf("%s did not change the digest (%v)", name, err)
	}
}

func TestResultDigestCoversTheResultButNotTheVolatileFields(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)

	same := func(name string, edit func(*conformance.Run)) {
		t.Helper()
		assertDigestSame(t, run, name, edit)
	}

	same("the digest itself", func(r *conformance.Run) { r.Summary.ResultDigest = "" })
	same("the run id", func(r *conformance.Run) { r.Run.ID = "x" })
	same("the start time", func(r *conformance.Run) { r.Run.StartedAt = "2000-01-01T00:00:00.000Z" })
	same("the finish time", func(r *conformance.Run) { r.Run.FinishedAt = "2000-01-01T00:00:00.000Z" })
	same("the host", func(r *conformance.Run) { r.Run.Host = conformance.RunHost{Platform: "plan9"} })
	same("the cache hits", func(r *conformance.Run) { r.Observability.CacheHits = 9 })

	different := func(name string, edit func(*conformance.Run)) {
		t.Helper()
		assertDigestDifferent(t, run, name, edit)
	}

	different("a finding", func(r *conformance.Run) { r.Findings[0].Message += "!" })
	different("the next action", func(r *conformance.Run) { r.Findings[0].NextAction = "correct intent" })
	different("an area", func(r *conformance.Run) { r.Areas.DeploymentTrace.Verdict = "unknown" })
	different("a counter", func(r *conformance.Run) { r.Observability.IngestedFacts++ })
	different("the tool version", func(r *conformance.Run) { r.Toolchain.Concordia = "other" })
}

// child gives the object under key of a decoded document; the test stops when it is not one.
func child(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()

	out, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %v", key, m[key])
	}

	return out
}

// firstFinding gives the first finding of a decoded results document.
func firstFinding(t *testing.T, m map[string]any) map[string]any {
	t.Helper()

	list, ok := m["findings"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("no findings: %v", m["findings"])
	}

	first, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("finding is not an object: %v", list[0])
	}

	return first
}

// schemaBreaks lists the edits of a valid results document that the schema must reject.
func schemaBreaks(t *testing.T) map[string]func(map[string]any) {
	t.Helper()

	return map[string]func(map[string]any){
		"an old schema version": func(m map[string]any) { m["schemaVersion"] = "1.0" },
		"a missing area key":    func(m map[string]any) { delete(child(t, m, "areas"), "qualityEvaluation") },
		// Scenario: Unknown area key is still rejected.
		"an unknown area key": func(m map[string]any) {
			child(t, m, "areas")["other"] = map[string]any{"verdict": "pass", "counts": map[string]any{}}
		},
		"a counter that is text":          func(m map[string]any) { child(t, m, "observability")["conflicts"] = "none" },
		"a finding without a next action": func(m map[string]any) { delete(firstFinding(t, m), "nextAction") },
		"a next action outside the vocabulary": func(m map[string]any) {
			firstFinding(t, m)["nextAction"] = "ignore"
		},
		"a finding without the expected condition": func(m map[string]any) { delete(firstFinding(t, m), "expected") },
		"a malformed result digest":                func(m map[string]any) { child(t, m, "summary")["resultDigest"] = "abc" },
	}
}

func TestResultsJSONValidatesAgainstTheEmbeddedSchema(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)
	text := canonicalOf(t, run)

	if errs := conformance.ValidateResultJSON(text); len(errs) > 0 {
		t.Fatalf("results.json does not validate against the 1.1 schema: %v", errs)
	}

	if version := decodeRunTree(t, text)["schemaVersion"]; version != "1.1" {
		t.Errorf("schemaVersion = %v", version)
	}

	for name, edit := range schemaBreaks(t) {
		copied := decodeRunTree(t, text)
		edit(copied)

		broken, err := json.Marshal(copied)
		if err != nil {
			t.Fatal(err)
		}

		if errs := conformance.ValidateResultJSON(broken); len(errs) == 0 {
			t.Errorf("%s validates against the schema", name)
		}
	}
}

// decodeRunTree decodes JSON text into a tree of maps and slices.
func decodeRunTree(t *testing.T, text []byte) map[string]any {
	t.Helper()

	var tree map[string]any
	if err := json.Unmarshal(text, &tree); err != nil {
		t.Fatal(err)
	}

	return tree
}

// assertOut02Fields checks that a finding of a pipeline run has the fields of OUT-02.
func assertOut02Fields(t *testing.T, i int, f *conformance.Finding) {
	t.Helper()

	if slices.Contains([]string{f.Expected, f.Observed, f.NextAction}, "") {
		t.Errorf("finding %d: %+v", i, f)
	}

	if f.Area != conformance.AreaConformance {
		t.Errorf("finding %d: %+v", i, f)
	}

	if f.Related == nil || f.ProviderLimits.Families == nil {
		t.Errorf("finding %d: %+v", i, f)
	}
}

func TestEveryFindingOfAPipelineRunHasTheOut02Fields(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)
	if len(run.Findings) == 0 {
		t.Fatal("no finding")
	}

	for i := range run.Findings {
		assertOut02Fields(t, i, &run.Findings[i])
	}

	// The architecture declares the pair and allows imports: the reconciliation says `conforming`, so there is no delta.
	if d := run.Findings[0].Delta; d != nil {
		t.Errorf("delta = %+v, want null for a conforming pair", d)
	}
}

func TestAreasAreNotConfiguredByDefault(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)
	area := run.Areas.Conformance

	if area.Verdict != conformance.AreaVerdict(run.Summary.Verdict) {
		t.Errorf("conformance area = %+v, summary verdict %s", area, run.Summary.Verdict)
	}

	for key, want := range map[string]int{"violation": 1, "unknown": 2, "findings": 1} {
		if area.Counts[key] != want {
			t.Errorf("conformance counts = %v", area.Counts)
		}
	}

	for _, other := range conformance.AreaOrder()[1:] {
		res := run.Areas.Get(other)
		if res.Verdict != conformance.AreaNotConfigured {
			t.Errorf("area %s = %+v, want not-configured with empty counts, not pass", other, res)
		}

		if res.Counts == nil || len(res.Counts) != 0 {
			t.Errorf("area %s = %+v, want not-configured with empty counts, not pass", other, res)
		}
	}
}

// assertSettledAreas checks the areas of a run whose assessment currency area is unknown and mandatory.
func assertSettledAreas(t *testing.T, run *conformance.Run) {
	t.Helper()

	if run.Areas.Conformance.Verdict != conformance.AreaVerdict(conformance.VerdictPass) {
		t.Errorf("areas = %+v", run.Areas)
	}

	if run.Areas.AssessmentCurrency.Counts["stale"] != 2 {
		t.Errorf("areas = %+v", run.Areas)
	}

	got := run.ConfiguredAreas()
	if len(got) != 2 {
		t.Fatalf("configured areas = %v", got)
	}

	if got[0] != conformance.AreaConformance || got[1] != conformance.AreaAssessmentCurrency {
		t.Errorf("configured areas = %v", got)
	}

	if run.Areas.ADRGovernance.Verdict != conformance.AreaNotConfigured {
		t.Errorf("adrGovernance = %+v", run.Areas.ADRGovernance)
	}
}

func TestSettleAreasKeepsTheSummaryVerdictOfAnUnknownMandatoryArea(t *testing.T) { // OUT-03
	t.Parallel()

	run := &conformance.Run{
		Summary: conformance.RunSummary{Verdict: conformance.VerdictPass},
		Rules:   []conformance.RuleResult{{RuleID: "R", Verdict: conformance.VerdictPass}},
		Areas: conformance.Areas{AssessmentCurrency: conformance.AreaResult{
			Verdict: conformance.AreaVerdict(conformance.VerdictUnknown), Counts: map[string]int{"stale": 2},
		}},
	}

	run.SettleAreas()

	if run.Summary.Verdict != conformance.VerdictUnknown {
		t.Errorf("summary verdict = %s, want unknown: the area is configured and unknown", run.Summary.Verdict)
	}

	assertSettledAreas(t, run)
}

// assertObservabilityTotals checks the relations between the counters of a run that ingested its facts.
func assertObservabilityTotals(t *testing.T, obs *conformance.Observability) {
	t.Helper()

	if obs.IngestedFacts < 1 || obs.NormalizedFacts < 1 {
		t.Errorf("observability = %+v", *obs)
	}

	if obs.NormalizedFacts > obs.IngestedFacts {
		t.Errorf("observability = %+v", *obs)
	}

	if obs.StageFailures != 0 || obs.CacheHits != 0 {
		t.Errorf("observability = %+v", *obs)
	}
}

func TestObservabilityCountersAreIntegers(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)
	assertObservabilityTotals(t, &run.Observability)

	tree := decodeRunTree(t, canonicalOf(t, run))

	counters, _ := tree["observability"].(map[string]any)
	if len(counters) != 8 {
		t.Errorf("counters = %v, want the eight fixed names", counters)
	}

	for name, value := range counters {
		if _, ok := value.(float64); !ok {
			t.Errorf("counter %s = %v is not a number", name, value)
		}
	}
}
