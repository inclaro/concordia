package assessment_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/impact"
	wsp "github.com/inclaro/concordia/internal/workspace"
)

var (
	atDecision = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	atEval     = time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
)

// edgeInput loads the edge assessment and gives the input of an evaluation with the required entry.
func edgeInput(t *testing.T, facts impact.FactSource) *assessment.EvalInput {
	t.Helper()

	return edgeInputOf(t, facts, edgeFiles())
}

func edgeInputOf(t *testing.T, facts impact.FactSource, files map[string]string) *assessment.EvalInput {
	t.Helper()

	set, err := loadEdge(t, files)
	if err != nil {
		t.Fatal(err)
	}

	return &assessment.EvalInput{
		Set: set, Verifier: verifiedAll{}, Facts: facts, Now: func() time.Time { return atEval },
		Required: []wsp.RequiredAssessment{{
			ID: "edge-exposure", Assessment: "ASM-EDGE", Type: "security", Environment: "production", Authority: "security-review",
			Scope: wsp.AssessmentScope{CalmNodes: []string{"api-gateway"}},
		}},
	}
}

// approve adds an approval of the version that binds the current subject, as `assessment approve` records it.
func approve(t *testing.T, in *assessment.EvalInput, id string, at time.Time) *assessment.Approval {
	t.Helper()

	bind, err := assessment.Bind(t.Context(), in, "ASM-EDGE", 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(bind.Unknown) != 0 {
		t.Fatalf("binding is unknown: %+v", bind.Unknown)
	}

	ap := &assessment.Approval{
		ID: id, Decision: assessment.DecisionApproved, Assessment: bind.Assessment, Subject: bind.Subject, Scope: bind.Scope,
		Environment: bind.Environment, Assumptions: bind.Assumptions, Evidence: bind.Evidence,
		ContractVersion: bind.ContractVersion, CapabilityProfile: bind.CapabilityProfile, ProducedUnder: bind.ProducedUnder,
		Reviewer: assessment.Reviewer{Identity: "alice@example.com", Authority: "security-review"}, DecidedAt: at,
		Previous: bind.Previous, Delta: bind.Delta,
		Location: assessment.Location{File: "assessments/approvals/" + id + ".yaml"}, Digest: "sha256:" + strings.Repeat("a", 60) + id[len(id)-4:],
	}

	in.Set.Approvals = append(in.Set.Approvals, ap)

	return ap
}

func evaluate(t *testing.T, in *assessment.EvalInput) *assessment.Evaluation {
	t.Helper()

	ev, err := assessment.Evaluate(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	return ev
}

func edgeResult(t *testing.T, ev *assessment.Evaluation) *assessment.Result {
	t.Helper()

	for i := range ev.Assessments {
		if ev.Assessments[i].ID == "ASM-EDGE" {
			return &ev.Assessments[i]
		}
	}

	t.Fatal("no result for ASM-EDGE")

	return nil
}

func wantState(t *testing.T, cur *assessment.Currency, state assessment.CurrencyState, reason string) {
	t.Helper()

	if cur.State != state {
		t.Fatalf("state = %s (reasons %v, unknown %q, causes %+v), want %s", cur.State, cur.Reasons, cur.UnknownReason, cur.Causes, state)
	}

	if reason == "" {
		return
	}

	if state == assessment.CurrencyUnknown {
		if cur.UnknownReason != reason {
			t.Errorf("unknown reason = %q, want %q", cur.UnknownReason, reason)
		}

		return
	}

	if !slices.Contains(cur.Reasons, reason) {
		t.Errorf("reasons = %v, want %q among them", cur.Reasons, reason)
	}
}

func jsonOf(v any) (string, error) {
	data, err := canonjson.Marshal(v)

	return string(data), err
}
