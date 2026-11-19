package plan_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

const (
	revision      = "0123456789abcdef0123456789abcdef01234567"
	otherRevision = "fedcba9876543210fedcba9876543210fedcba98"
)

// planStatement is a `plan` statement for the subject digest with the predicate fields.
func planStatement(t *testing.T, subjectDigest string, predicate map[string]any) *statement.Loaded {
	t.Helper()

	_, hex, _ := strings.Cut(subjectDigest, ":")

	data, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "predicateType": statement.PredicatePlan, "predicate": predicate,
		"subject": []map[string]any{{"name": "plans/terraform-1.2-create.json", "digest": map[string]string{"sha256": hex}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := statement.Parse("plans/terraform-1.2-create.statement.json", data)
	if err != nil {
		t.Fatal(err)
	}

	return &loaded
}

func goodPredicate() map[string]any {
	return map[string]any{
		"environment": "production", "revision": revision, "tool": "terraform",
		"time": now.Add(-time.Hour).Format(time.RFC3339),
	}
}

func verified(producer string) statement.Verification {
	return statement.Verification{Signature: statement.SignatureVerified, ProducerID: producer, Subject: statement.SubjectUnchecked}
}

func bindInput(t *testing.T) *plan.BindInput {
	t.Helper()

	p := read(t, "terraform-1.2-create.json")

	return &plan.BindInput{
		Plan: p, File: "plans/terraform-1.2-create.json",
		Statement:    planStatement(t, p.Digest, goodPredicate()),
		Verification: verified("ci"),
		Context:      assurance.Context{Now: now, Environment: "production", Revision: revision},
		Producers:    []workspace.Producer{{ID: "ci", Claims: []string{workspace.ProducerClaimPlan}}},
		MaxAge:       14 * 24 * time.Hour,
	}
}

func TestABoundPlanIsAdmissible(t *testing.T) {
	t.Parallel()

	got := plan.Bind(bindInput(t))

	if !got.Admissible() || got.Admission.Integrity != assurance.IntegrityAuthenticated {
		t.Errorf("admission = %+v, want admissible with producer authenticated", got.Admission)
	}

	if got.UnknownReason() != "" || len(got.Diagnostics) != 0 {
		t.Errorf("reason = %q, diagnostics = %v", got.UnknownReason(), got.Diagnostics)
	}

	if got.Statement == nil || got.Statement.Environment != "production" {
		t.Errorf("statement facts = %+v", got.Statement)
	}
}

func TestAPlanWithoutAStatementIsContentNotVerified(t *testing.T) {
	t.Parallel()

	in := bindInput(t)
	in.Statement = nil
	in.Verification = statement.Verification{}

	got := plan.Bind(in)

	if got.Admission.Integrity != assurance.IntegrityNotVerified || got.Admission.Integrity.Text() != "content not verified" {
		t.Errorf("integrity = %s", got.Admission.Integrity)
	}

	// The plan is shown, but a mandatory claim that needs it is unknown.
	if got.Admissible() || got.UnknownReason() != "no-evidence-supplied" {
		t.Errorf("admissible = %v, reason = %q, want inadmissible with no-evidence-supplied", got.Admissible(), got.UnknownReason())
	}

	if !slices.Equal(got.Admission.Details, []assurance.Detail{assurance.DetailNotAuthenticated}) {
		t.Errorf("details = %v", got.Admission.Details)
	}

	if len(got.Diagnostics) != 0 {
		t.Errorf("a missing statement is not a warning: %v", got.Diagnostics)
	}
}

func TestABindingCollectsEveryReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*plan.BindInput, map[string]any)
		want   []assurance.Detail
	}{
		{"different revision", func(_ *plan.BindInput, p map[string]any) { p["revision"] = otherRevision },
			[]assurance.Detail{assurance.DetailDifferentRevision}},
		{"different environment", func(_ *plan.BindInput, p map[string]any) { p["environment"] = "staging" },
			[]assurance.Detail{assurance.DetailDifferentEnvironment}},
		{"too old", func(_ *plan.BindInput, p map[string]any) {
			p["time"] = now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
		},
			[]assurance.Detail{assurance.DetailStale}},
		{"uncommitted changes", func(in *plan.BindInput, _ map[string]any) { in.Context.DirtyTree = true },
			[]assurance.Detail{assurance.DetailSubjectNotEvaluatedState}},
		{"producer trusted for other claims", func(in *plan.BindInput, _ map[string]any) {
			in.Producers = []workspace.Producer{{ID: "ci", Claims: []string{workspace.ProducerClaimCheckResult}}}
		}, []assurance.Detail{assurance.DetailProducerNotTrustedForClaim}},
		{"statement not signed", func(in *plan.BindInput, _ map[string]any) {
			in.Verification = statement.Verification{Signature: statement.SignatureNone}
		}, []assurance.Detail{assurance.DetailNotAuthenticated}},
		{"missing time", func(_ *plan.BindInput, p map[string]any) { delete(p, "time") },
			[]assurance.Detail{assurance.MissingField("time")}},
		{"several at once", func(in *plan.BindInput, p map[string]any) {
			p["revision"] = otherRevision
			p["environment"] = "staging"
			in.Verification = verified("stranger")
		}, []assurance.Detail{assurance.DetailDifferentEnvironment, assurance.DetailDifferentRevision, assurance.DetailProducerNotTrusted}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			in := bindInput(t)
			predicate := goodPredicate()
			test.change(in, predicate)
			in.Statement = planStatement(t, in.Plan.Digest, predicate)

			got := plan.Bind(in)

			want := slices.Clone(test.want)
			slices.Sort(want)

			if !slices.Equal(got.Admission.Details, want) {
				t.Errorf("details = %v, want %v", got.Admission.Details, want)
			}

			if got.Admissible() || got.UnknownReason() != "no-evidence-supplied" {
				t.Errorf("admissible = %v, reason = %q", got.Admissible(), got.UnknownReason())
			}
		})
	}
}

func TestADigestThatDiffersIsAnIntegrityFailureAndAWarning(t *testing.T) {
	t.Parallel()

	in := bindInput(t)
	in.Statement = planStatement(t, "sha256:"+strings.Repeat("0", 64), goodPredicate())

	got := plan.Bind(in)

	if got.Admissible() || !slices.Contains(got.Admission.Details, assurance.DetailIntegrityFailure) {
		t.Fatalf("admission = %+v, want integrity-failure", got.Admission)
	}

	// A verified signature does not make a statement about other bytes `producer authenticated`.
	if got.Admission.Integrity == assurance.IntegrityAuthenticated {
		t.Errorf("integrity = %s", got.Admission.Integrity)
	}

	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != diag.PRV009 || got.Diagnostics[0].Severity != diag.SeverityWarning {
		t.Errorf("diagnostics = %v, want one CO-PRV-009 warning", got.Diagnostics)
	}
}

// The caller may leave the subject of the verification unchecked: the binding compares it itself.
func TestBindComparesTheSubjectEvenWhenTheVerificationDidNot(t *testing.T) {
	t.Parallel()

	in := bindInput(t)
	in.Verification.Subject = statement.SubjectMatches
	in.Statement = planStatement(t, "sha256:"+strings.Repeat("1", 64), goodPredicate())

	if got := plan.Bind(in); !slices.Contains(got.Admission.Details, assurance.DetailIntegrityFailure) {
		t.Errorf("details = %v, want integrity-failure", got.Admission.Details)
	}
}

// A failed signature makes the binding inadmissible. Its warning comes from the verification of the statement (the
// loader reports it), so Bind adds none and one file gives one CO-PRV-009.
func TestAFailedSignatureIsInadmissibleAndNotReportedTwice(t *testing.T) {
	t.Parallel()

	in := bindInput(t)
	in.Verification = statement.Verification{Signature: statement.SignatureFailed, Failure: "no declared producer verified it"}

	got := plan.Bind(in)

	if !slices.Contains(got.Admission.Details, assurance.DetailIntegrityFailure) || got.Admissible() {
		t.Errorf("details = %v", got.Admission.Details)
	}

	if len(got.Diagnostics) != 0 {
		t.Errorf("diagnostics = %v, want none (the verification reports the signature)", got.Diagnostics)
	}
}

func TestAStatementThatIsNotAPlanBindingBindsNothing(t *testing.T) {
	t.Parallel()

	in := bindInput(t)
	in.Statement.Statement.Predicate = json.RawMessage(`{"environment": 7}`)

	got := plan.Bind(in)

	if got.Admissible() || got.Admission.Integrity != assurance.IntegrityNotVerified || got.Statement != nil {
		t.Errorf("binding = %+v", got)
	}

	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != diag.PRV009 {
		t.Errorf("diagnostics = %v", got.Diagnostics)
	}
}

func TestTheSubjectDigestIsTheDigestOfThePlanFile(t *testing.T) {
	t.Parallel()

	p := read(t, "terraform-1.2-create.json")

	got, ok := p.SubjectDigest()
	if !ok || got.String() != p.Digest || got.Algorithm != "sha256" {
		t.Errorf("SubjectDigest() = %v, %v, want %s", got, ok, p.Digest)
	}
}
