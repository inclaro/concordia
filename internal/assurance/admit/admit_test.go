package admit_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/admit"
	"github.com/inclaro/concordia/internal/assurance/checkresult"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/workspace"
)

// now is the evaluation time that the manifest records.
var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

const revision = "0123456789abcdef0123456789abcdef01234567"

func producers() []workspace.Producer {
	return []workspace.Producer{
		{ID: "ci", Claims: []string{workspace.ProducerClaimCheckResult, workspace.ProducerClaimMeasurement}},
		{ID: "builder", Claims: []string{workspace.ProducerClaimBuildProvenance}},
	}
}

func verified(producer string) statement.Verification {
	return statement.Verification{Signature: statement.SignatureVerified, ProducerID: producer, Subject: statement.SubjectUnchecked}
}

// good is an admissible check result question; each case changes one thing.
func good() admit.Input {
	return admit.Input{
		Kind:         workspace.ProducerClaimCheckResult,
		Verification: verified("ci"),
		Producers:    producers(),
		Context:      assurance.Context{Now: now, Environment: "production", Revision: revision},
		MaxAge:       14 * 24 * time.Hour,
		Facts: admit.Facts{
			Environment: "production", Revision: revision, Time: now.Add(-24 * time.Hour),
		},
	}
}

func TestAdmitEachReasonAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*admit.Input)
		want   []assurance.Detail
	}{
		{"nothing wrong", func(*admit.Input) {}, nil},
		{"producer not declared", func(in *admit.Input) { in.Verification = verified("stranger") }, []assurance.Detail{assurance.DetailProducerNotTrusted}},
		{"no producer to verify with", func(in *admit.Input) {
			in.Verification = statement.Verification{Signature: statement.SignatureNoProducer, Subject: statement.SubjectUnchecked}
		}, []assurance.Detail{assurance.DetailProducerNotTrusted}},
		{"producer not trusted for the claim kind", func(in *admit.Input) { in.Verification = verified("builder") }, []assurance.Detail{assurance.DetailProducerNotTrustedForClaim}},
		{"signature failed", func(in *admit.Input) {
			in.Verification = statement.Verification{Signature: statement.SignatureFailed, Subject: statement.SubjectUnchecked, Failure: "x"}
		}, []assurance.Detail{assurance.DetailIntegrityFailure}},
		{"subject digest differs", func(in *admit.Input) { in.Verification.Subject = statement.SubjectDiffers }, []assurance.Detail{assurance.DetailIntegrityFailure}},
		{"unsigned", func(in *admit.Input) {
			in.Verification = statement.Verification{Signature: statement.SignatureNone, Subject: statement.SubjectUnchecked}
		}, []assurance.Detail{assurance.DetailNotAuthenticated}},
		{"offline verification unavailable", func(in *admit.Input) {
			in.Verification = statement.Verification{Signature: statement.SignatureUnavailable, Subject: statement.SubjectUnchecked}
		}, []assurance.Detail{assurance.DetailOfflineVerificationUnavailable}},
		{"different revision", func(in *admit.Input) { in.Facts.Revision = "def" }, []assurance.Detail{assurance.DetailDifferentRevision}},
		{"different environment", func(in *admit.Input) { in.Facts.Environment = "staging" }, []assurance.Detail{assurance.DetailDifferentEnvironment}},
		{"stale", func(in *admit.Input) { in.Facts.Time = now.Add(-15 * 24 * time.Hour) }, []assurance.Detail{assurance.DetailStale}},
		{"failed run", func(in *admit.Input) { in.Facts.FailedRun = true }, []assurance.Detail{assurance.DetailFailedRun}},
		{"invalid outcome", func(in *admit.Input) { in.Facts.InvalidOutcome = true }, []assurance.Detail{assurance.DetailInvalidOutcome}},
		{"uncommitted changes", func(in *admit.Input) { in.Context.DirtyTree = true }, []assurance.Detail{assurance.DetailSubjectNotEvaluatedState}},
		{"missing field", func(in *admit.Input) { in.Facts.Missing = []string{"environment"} }, []assurance.Detail{assurance.MissingField("environment")}},
		{"threshold mismatch", func(in *admit.Input) { in.Facts.Extra = []assurance.Detail{assurance.DetailThresholdMismatch} }, []assurance.Detail{assurance.DetailThresholdMismatch}},
		{"unit mismatch", func(in *admit.Input) { in.Facts.Extra = []assurance.Detail{assurance.DetailUnitMismatch} }, []assurance.Detail{assurance.DetailUnitMismatch}},
		{"no time with a maximum age", func(in *admit.Input) { in.Facts.Time = time.Time{} }, []assurance.Detail{assurance.MissingField("time")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := good()
			tt.change(&in)

			got := admit.Admit(&in)

			if want := tt.want == nil; got.Admissible != want {
				t.Errorf("admissible = %v, want %v (details %v)", got.Admissible, want, got.Details)
			}

			if !slices.Equal(got.Details, append([]assurance.Detail{}, tt.want...)) {
				t.Errorf("details = %v, want %v", got.Details, tt.want)
			}
		})
	}
}

func TestAdmitCollectsEveryReasonNotOnlyTheFirst(t *testing.T) {
	t.Parallel()

	in := good()
	in.Verification = verified("stranger")
	in.Facts.Environment = "staging"
	in.Facts.Revision = "def"
	in.Facts.Time = now.Add(-30 * 24 * time.Hour)
	in.Facts.FailedRun = true
	in.Facts.Missing = []string{"covers", "evaluator.name"}
	in.Context.DirtyTree = true

	got := admit.Admit(&in)

	want := []assurance.Detail{
		assurance.DetailDifferentEnvironment, assurance.DetailDifferentRevision, assurance.DetailFailedRun,
		assurance.MissingField("covers"), assurance.MissingField("evaluator.name"), assurance.DetailProducerNotTrusted,
		assurance.DetailStale, assurance.DetailSubjectNotEvaluatedState,
	}
	slices.Sort(want)

	if got.Admissible || !slices.Equal(got.Details, want) {
		t.Errorf("details = %v, want %v", got.Details, want)
	}
}

func TestAdmitSeveralReasonsOfASpecScenario(t *testing.T) {
	t.Parallel()

	// A result for another environment from an untrusted producer: both reasons are recorded.
	in := good()
	in.Verification = verified("stranger")
	in.Facts.Environment = "staging"

	got := admit.Admit(&in)

	want := []assurance.Detail{assurance.DetailDifferentEnvironment, assurance.DetailProducerNotTrusted}
	if !slices.Equal(got.Details, want) || got.Admissible {
		t.Errorf("details = %v, want %v", got.Details, want)
	}
}

// TestAdmitProducerTrustedForAnotherClaimKind: the producer is declared and verified, but its claims do not include
// the kind of this statement. The same producer is admissible for the kind it is trusted for.
func TestAdmitProducerTrustedForAnotherClaimKind(t *testing.T) {
	t.Parallel()

	for kind, want := range map[string]bool{
		workspace.ProducerClaimCheckResult:         true,
		workspace.ProducerClaimMeasurement:         true,
		workspace.ProducerClaimBuildProvenance:     false,
		workspace.ProducerClaimPlan:                false,
		workspace.ProducerClaimDeploymentRecord:    false,
		workspace.ProducerClaimWorkloadObservation: false,
	} {
		in := good()
		in.Kind = kind

		got := admit.Admit(&in)
		if got.Admissible != want {
			t.Errorf("%s: admissible = %v, want %v (details %v)", kind, got.Admissible, want, got.Details)
		}

		if !want && !slices.Equal(got.Details, []assurance.Detail{assurance.DetailProducerNotTrustedForClaim}) {
			t.Errorf("%s: details = %v", kind, got.Details)
		}
	}
}

func TestAdmitIntegrityLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		v    statement.Verification
		want assurance.Integrity
	}{
		{"verified signature", statement.Verification{Signature: statement.SignatureVerified, ProducerID: "ci", Subject: statement.SubjectUnchecked}, assurance.IntegrityAuthenticated},
		{"verified signature and matching subject", statement.Verification{Signature: statement.SignatureVerified, ProducerID: "ci", Subject: statement.SubjectMatches}, assurance.IntegrityAuthenticated},
		{"verified signature, subject differs", statement.Verification{Signature: statement.SignatureVerified, ProducerID: "ci", Subject: statement.SubjectDiffers}, assurance.IntegrityNotVerified},
		{"digest only", statement.Verification{Signature: statement.SignatureNone, Subject: statement.SubjectMatches}, assurance.IntegrityDigest},
		{"digest and offline", statement.Verification{Signature: statement.SignatureUnavailable, Subject: statement.SubjectMatches}, assurance.IntegrityDigest},
		{"unsigned", statement.Verification{Signature: statement.SignatureNone, Subject: statement.SubjectUnchecked}, assurance.IntegrityNotVerified},
		{"failed signature", statement.Verification{Signature: statement.SignatureFailed, Subject: statement.SubjectMatches}, assurance.IntegrityDigest},
	}

	for _, tt := range tests {
		in := good()
		in.Verification = tt.v

		if got := admit.Admit(&in).Integrity; got != tt.want {
			t.Errorf("%s: integrity = %s, want %s", tt.name, got, tt.want)
		}
	}
}

// TestAdmitTakesTheTimeFromTheContext: the same statement is stale or fresh depending on the evaluation time that
// the manifest records, never on the clock of the machine.
func TestAdmitTakesTheTimeFromTheContext(t *testing.T) {
	t.Parallel()

	in := good()
	in.Facts.Time = now.Add(-10 * 24 * time.Hour)

	for offset, want := range map[time.Duration]bool{
		0:                    true,
		3 * 24 * time.Hour:   true,
		5 * 24 * time.Hour:   false,
		-20 * 24 * time.Hour: true, // an evaluation time before the statement is not stale
	} {
		in.Context.Now = now.Add(offset)

		got := admit.Admit(&in)
		if got.Admissible != want {
			t.Errorf("evaluated %v after the manifest time: admissible = %v, want %v (%v)", offset, got.Admissible, want, got.Details)
		}
	}

	// Without a maximum age nothing is stale.
	in.MaxAge = 0
	in.Context.Now = now.Add(365 * 24 * time.Hour)

	if got := admit.Admit(&in); !got.Admissible {
		t.Errorf("no maximum age: details = %v", got.Details)
	}
}

func TestAdmitDoesNotCheckWhatTheContextOrTheStatementDoesNotState(t *testing.T) {
	t.Parallel()

	in := good()
	in.Context.Environment, in.Context.Revision = "", ""
	in.Facts.Environment, in.Facts.Revision = "staging", "def"

	if got := admit.Admit(&in); !got.Admissible {
		t.Errorf("no obligation environment or revision: details = %v", got.Details)
	}

	// A statement with no revision (provenance, records) does not fail for a dirty tree.
	in = good()
	in.Facts.Revision = ""
	in.Context.DirtyTree = true

	if got := admit.Admit(&in); !got.Admissible {
		t.Errorf("no revision on the statement: details = %v", got.Details)
	}
}

func TestAdmitDetailsAreSortedWithoutDuplicates(t *testing.T) {
	t.Parallel()

	in := good()
	in.Facts.Missing = []string{"time", "time", "check"}
	in.Facts.Time = time.Time{}

	got := admit.Admit(&in)

	want := []assurance.Detail{assurance.MissingField("check"), assurance.MissingField("time")}
	if !slices.Equal(got.Details, want) {
		t.Errorf("details = %v, want %v", got.Details, want)
	}

	admissible := good()

	data, err := json.Marshal(admit.Admit(&admissible))
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != `{"integrity":"producer-authenticated","details":[],"admissible":true}` {
		t.Errorf("an admissible result must show an empty details list: %s", data)
	}
}

// parse helpers for the statement kinds that go through the engine.

func parsed(t *testing.T, predicateType string, predicate map[string]any) *statement.Statement {
	t.Helper()

	data, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "predicateType": predicateType, "predicate": predicate,
		"subject": []map[string]any{{"digest": map[string]string{"sha256": "aaa"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := statement.Parse("x.json", data)
	if err != nil {
		t.Fatal(err)
	}

	return &loaded.Statement
}

func TestDeploymentRecordAndObservationFacts(t *testing.T) {
	t.Parallel()

	workload := map[string]any{"namespace": "shop", "kind": "Deployment", "name": "api"}
	record := map[string]any{
		"environment": "staging", "cluster": "c", "workload": workload, "container": "api", "digest": "sha256:aaa",
		"outcome": "succeeded", "time": now.Add(-time.Hour).Format(time.RFC3339), "deployer": "ci",
	}
	observation := map[string]any{
		"environment": "production", "cluster": "c", "workload": workload, "container": "api", "digest": "sha256:aaa",
		"time": now.Add(-48 * time.Hour).Format(time.RFC3339),
	}

	rec, err := statement.ParseDeploymentRecord(parsed(t, statement.PredicateDeploymentRecord, record))
	if err != nil {
		t.Fatal(err)
	}

	obs, err := statement.ParseWorkloadObservation(parsed(t, statement.PredicateWorkloadObservation, observation))
	if err != nil {
		t.Fatal(err)
	}

	ctx := assurance.Context{Now: now, Environment: "production", Revision: revision}
	both := []workspace.Producer{{ID: "deployer", Claims: []string{workspace.ProducerClaimDeploymentRecord, workspace.ProducerClaimWorkloadObservation}}}

	// Record for another environment: inadmissible with different-environment.
	got := admit.Admit(&admit.Input{
		Kind: workspace.ProducerClaimDeploymentRecord, Verification: verified("deployer"), Producers: both, Context: ctx,
		MaxAge: 7 * 24 * time.Hour, Facts: admit.DeploymentRecordFacts(&rec),
	})
	if got.Admissible || !slices.Equal(got.Details, []assurance.Detail{assurance.DetailDifferentEnvironment}) {
		t.Errorf("record for staging: %+v", got)
	}

	// The record is for the right environment and has an outcome Concordia does not know.
	rec.Environment, rec.Outcome = "production", "rolled-back"

	got = admit.Admit(&admit.Input{
		Kind: workspace.ProducerClaimDeploymentRecord, Verification: verified("deployer"), Producers: both, Context: ctx,
		MaxAge: 7 * 24 * time.Hour, Facts: admit.DeploymentRecordFacts(&rec),
	})
	if !slices.Equal(got.Details, []assurance.Detail{assurance.DetailInvalidOutcome}) {
		t.Errorf("record with an unknown outcome: %+v", got)
	}

	// An observation two days old is stale under a one-day limit and fresh under a seven-day limit.
	for maxAge, want := range map[time.Duration]bool{24 * time.Hour: false, 7 * 24 * time.Hour: true} {
		got = admit.Admit(&admit.Input{
			Kind: workspace.ProducerClaimWorkloadObservation, Verification: verified("deployer"), Producers: both, Context: ctx,
			MaxAge: maxAge, Facts: admit.WorkloadObservationFacts(&obs),
		})

		if got.Admissible != want || (!want && !slices.Equal(got.Details, []assurance.Detail{assurance.DetailStale})) {
			t.Errorf("observation with a maximum age of %v: %+v", maxAge, got)
		}
	}

	// An observation with missing fields reports each of them.
	empty, err := statement.ParseWorkloadObservation(parsed(t, statement.PredicateWorkloadObservation, map[string]any{"environment": "production"}))
	if err != nil {
		t.Fatal(err)
	}

	got = admit.Admit(&admit.Input{
		Kind: workspace.ProducerClaimWorkloadObservation, Verification: verified("deployer"), Producers: both, Context: ctx,
		Facts: admit.WorkloadObservationFacts(&empty),
	})

	want := []assurance.Detail{
		assurance.MissingField("cluster"), assurance.MissingField("container"), assurance.MissingField("digest"),
		assurance.MissingField("time"), assurance.MissingField("workload.kind"), assurance.MissingField("workload.name"),
		assurance.MissingField("workload.namespace"),
	}
	slices.Sort(want)

	if !slices.Equal(got.Details, want) {
		t.Errorf("details = %v, want %v", got.Details, want)
	}
}

func TestCheckResultAndMeasurementFacts(t *testing.T) {
	t.Parallel()

	result, err := checkresult.Parse(parsed(t, statement.PredicateCheckResult, map[string]any{
		"check": "unit-tests", "outcome": "failed", "revision": "abc", "environment": "production",
		"evaluator": map[string]any{"name": "go-test", "version": "1"}, "covers": []any{},
		"time": now.Add(-time.Hour).Format(time.RFC3339),
	}))
	if err != nil {
		t.Fatal(err)
	}

	in := good()
	in.Facts = admit.CheckResultFacts(&result)

	// A failed result for the evaluated revision is admissible (the verdict maps it to a violation), but a result for
	// revision abc is not a result for the evaluated revision.
	got := admit.Admit(&in)
	if !slices.Equal(got.Details, []assurance.Detail{assurance.DetailDifferentRevision}) {
		t.Errorf("details = %v", got.Details)
	}

	result.Revision = revision
	in.Facts = admit.CheckResultFacts(&result)

	if got = admit.Admit(&in); !got.Admissible {
		t.Errorf("a failed result must be admissible: %v", got.Details)
	}

	measurement, err := statement.ParseMeasurement(parsed(t, statement.PredicateMeasurement, map[string]any{
		"scenario": "failover", "metric": "m", "value": 42, "unit": "s", "threshold": 30, "comparison": "lte",
		"revision": revision, "environment": "staging", "evaluator": map[string]any{"name": "k6", "version": "1"},
		"outcome": "failed", "window": map[string]any{"start": "2026-10-30T10:00:00Z", "end": "2026-10-30T11:00:00Z"},
	}))
	if err != nil {
		t.Fatal(err)
	}

	in = good()
	in.Kind = workspace.ProducerClaimMeasurement
	in.MaxAge = 24 * time.Hour
	in.Facts = admit.MeasurementFacts(&measurement)

	got = admit.Admit(&in)

	want := []assurance.Detail{
		assurance.DetailDifferentEnvironment, assurance.DetailFailedRun, assurance.DetailStale, assurance.MissingField("testConditions"),
	}
	slices.Sort(want)

	if !slices.Equal(got.Details, want) {
		t.Errorf("measurement details = %v, want %v", got.Details, want)
	}
}

func TestPlanAndProvenanceFacts(t *testing.T) {
	t.Parallel()

	plan, err := statement.ParsePlan(parsed(t, statement.PredicatePlan, map[string]any{
		"environment": "production", "revision": "def", "time": now.Add(-time.Hour).Format(time.RFC3339),
	}))
	if err != nil {
		t.Fatal(err)
	}

	in := good()
	in.Kind = workspace.ProducerClaimPlan
	in.Producers = []workspace.Producer{{ID: "ci", Claims: []string{workspace.ProducerClaimPlan}}}
	in.Facts = admit.PlanFacts(&plan)

	if got := admit.Admit(&in); !slices.Equal(got.Details, []assurance.Detail{assurance.DetailDifferentRevision}) {
		t.Errorf("plan for a different revision: %v", got.Details)
	}

	provenance, err := statement.ParseSLSA(parsed(t, statement.PredicateSLSAProvenance, map[string]any{
		"buildDefinition": map[string]any{"buildType": "x"},
		"runDetails":      map[string]any{"builder": map[string]any{"id": "b"}},
	}), verified("builder"))
	if err != nil {
		t.Fatal(err)
	}

	in = good()
	in.Kind = workspace.ProducerClaimBuildProvenance
	in.Verification = provenance.Verification
	in.Facts = admit.SLSAFacts(&provenance)
	in.MaxAge = 0

	if got := admit.Admit(&in); !got.Admissible {
		t.Errorf("provenance: %v", got.Details)
	}

	// With a maximum age, a provenance statement that states no build time cannot be shown to be fresh.
	in.MaxAge = 30 * 24 * time.Hour

	if got := admit.Admit(&in); !slices.Equal(got.Details, []assurance.Detail{assurance.MissingField("time")}) {
		t.Errorf("provenance without a time: %v", got.Details)
	}
}
