package assessment_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
)

// Scenario: A25 manual reviewed flag without authorized approval.
// Scenario: MIG-04 diagram matches but nobody approved.
func TestNoVerifiedApprovalIsReviewRequiredAndAViolation(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	in.Set.Assessment("ASM-EDGE", 1).Notes = "reviewed by security"
	in.Set.Assessment("ASM-EDGE", 1).Reviewed = true

	ev := evaluate(t, in)
	res := edgeResult(t, ev)

	wantState(t, &res.Currency, assessment.CurrencyReviewRequired, assessment.DetailNoApproval)

	currencyAssertNotApproved(t, res)
	currencyAssertEmptyBasis(t, res)
	currencyAssertRequiredUnapproved(t, ev)

	if !slices.Contains(res.Required, "edge-exposure") {
		t.Errorf("required ids = %v", res.Required)
	}
}

// Scenario: Initial approval for a new scope.
func TestAVerifiedApprovalOfTheCurrentSubjectIsCurrent(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	ev := evaluate(t, in)
	res := edgeResult(t, ev)

	wantState(t, &res.Currency, assessment.CurrencyCurrent, "")

	currencyAssertApproval(t, &res.Currency, "ASM-EDGE-v1-0001", "protected-review")

	if res.State != assessment.StateApproved || res.Outcome != assessment.OutcomeSatisfactory {
		t.Errorf("state = %s, outcome = %s: both are shown separately", res.State, res.Outcome)
	}

	if v := res.Currency.Verdict(); v.Result != assessment.VerdictPass {
		t.Errorf("verdict = %+v", v)
	}

	if ev.Required[0].Currency.State != assessment.CurrencyCurrent {
		t.Errorf("required = %+v", ev.Required[0].Currency)
	}
}

// Scenario: A26 approval for staging used for production.
func TestAnApprovalForAnotherEnvironmentDoesNotSatisfy(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision).Environment = "staging"

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyReviewRequired, assessment.DetailEnvironmentMismatch)
}

// A required entry for another environment than the assessment's is a mismatch of the entry.
func TestARequiredEntryForAnotherEnvironment(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	in.Required[0].Environment = "staging"
	in.Required[0].Authority = "governance-board"

	ev := evaluate(t, in)
	cur := &ev.Required[0].Currency

	wantState(t, cur, assessment.CurrencyReviewRequired, assessment.DetailEnvironmentMismatch)

	if !slices.Contains(cur.Reasons, assessment.DetailAuthorityMismatch) {
		t.Errorf("reasons = %v, want an authority mismatch", cur.Reasons)
	}

	if edgeResult(t, ev).Currency.State != assessment.CurrencyCurrent {
		t.Errorf("the version itself stays current")
	}
}

// Scenario: A26 digest mismatch.
// Scenario: A21 new public route.
func TestAChangedBoundItemIsATrigger(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	in.Facts = newFacts(entryPoint("k8s:ingress/api", "api.example.com"), entryPoint("k8s:ingress/new", "new.example.com"))

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyReviewRequired, assessment.DetailSubjectDigestMismatch)

	if len(res.Currency.Triggers) != 1 {
		t.Fatalf("triggers = %+v", res.Currency.Triggers)
	}

	trig := res.Currency.Triggers[0]
	currencyAssertAddedExposure(t, &trig, "public-entry-point:k8s:ingress/new")

	if strings.Contains(trig.Text(), "vulnerable") {
		t.Errorf("trigger text claims a defect: %s", trig.Text())
	}
}

// Scenario: Unrelated change keeps currency.
func TestAnUnrelatedChangeKeepsCurrency(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	factsOf(t, in).matches[impact.KindRoleBinding] = found(
		impact.V1().NewItem(impact.KindRoleBinding, "k8s:rolebinding/other", impact.Facets{"roleReference": {"admin"}}, nil, nil))

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyCurrent, "")

	if !res.Currency.Analysis.Complete {
		t.Errorf("analysis = %+v", res.Currency.Analysis)
	}
}

// Scenario: A26 expired approval.
// Scenario: Time-only expiry.
func TestAnApprovalExpiresBetweenRunsWithoutAFileChange(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	ap := approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	ap.Expiry = &assessment.Expiry{ExpiresAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}

	in.Now = func() time.Time { return time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC) }
	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyCurrent, "")

	in.Now = func() time.Time { return time.Date(2026, 7, 1, 0, 0, 1, 0, time.UTC) }

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyExpired, assessment.DetailExpired)

	if res.State != assessment.StateExpired || res.Currency.Verdict().Result != assessment.VerdictViolation {
		t.Errorf("state = %s, verdict = %+v", res.State, res.Currency.Verdict())
	}
}

// Scenario: A26 revoked approval.
// Scenario: Revocation without source change.
func TestARevocationAppearingLaterRevokes(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	ap := approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyCurrent, "")

	in.Set.Revocations = append(in.Set.Revocations, &assessment.Revocation{
		ID: "REV-1", Approval: assessment.Ref{ID: ap.ID, Digest: ap.Digest}, DecidedAt: atDecision.Add(time.Hour), Reason: "gateway moved",
		Reviewer: assessment.Reviewer{Identity: "alice@example.com", Authority: "security-review"},
	})

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyRevoked, assessment.DetailRevoked)

	if res.State != assessment.StateRevoked || res.Currency.Approval == nil {
		t.Errorf("state = %s, approval = %+v", res.State, res.Currency.Approval)
	}

	// A revocation of another digest does not count.
	in.Set.Revocations[0].Approval.Digest = "sha256:" + strings.Repeat("b", 64)
	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyCurrent, "")
}

// Scenario: Unmapped dependency kind.
func TestAnUnsupportedKindIsUnknown(t *testing.T) {
	t.Parallel()

	facts := newFacts()
	facts.matches[impact.KindPublicEntryPoint] = impact.Match{State: impact.MatchUnsupported, Profile: "cbm-0.12.1"}

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	in.Facts = facts

	ev := evaluate(t, in)
	res := edgeResult(t, ev)
	wantState(t, &res.Currency, assessment.CurrencyUnknown, assessment.ReasonUnsupportedFamily)

	if v := res.Currency.Verdict(); v.Result != assessment.VerdictUnknown || v.Reason != assessment.ReasonUnsupportedFamily {
		t.Errorf("verdict = %+v", v)
	}

	if !slices.ContainsFunc(ev.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == diag.SEC002 }) {
		t.Errorf("diagnostics = %+v, want CO-SEC-002", ev.Diagnostics)
	}
}

// Scenario: No approval is not unknown.
func TestNoApprovalBeatsUnknownItems(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, nil)

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyReviewRequired, assessment.DetailNoApproval)

	if res.Currency.UnknownReason != "" {
		t.Errorf("unknown reason = %q", res.Currency.UnknownReason)
	}
}

func TestWithoutAFactSourceAnApprovedAssessmentIsUnknown(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	in.Facts = nil

	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyUnknown, assessment.ReasonCoverageIncomplete)
}

// Scenario: Trust anchor cannot be read.
func TestCurrencyOfAnUnreadableAnchorIsUnknown(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	in.Verifier = verifiedAll{unknown: assessment.ReasonTrustAnchorUnreadable}

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyUnknown, assessment.ReasonTrustAnchorUnreadable)

	if len(res.Currency.NotApplied) != 1 {
		t.Errorf("not applied = %+v", res.Currency.NotApplied)
	}
}

// Scenario: Verification cannot run.
// An approval whose signature cannot be verified (keyless verification offline) leaves the currency unknown with the
// reason `verification-unavailable`, never current.
func TestCurrencyOfAnUnavailableVerificationIsUnknown(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	in.Verifier = verifiedAll{unknown: assessment.ReasonVerificationUnavailable}

	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyUnknown, assessment.ReasonVerificationUnavailable)
}

// Scenario: Scope deleted does not remove the obligation.
// The service that the required assessment covers disappears from the facts: the required entry stays in the
// evaluation and is not current, and the removal is a trigger of the class removal that needs a disposition.
func TestADeletedScopeKeepsTheRequiredObligationAndTriggersRemoval(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	in.Facts = newFacts()

	ev := evaluate(t, in)

	currencyAssertRequiredNotCurrent(t, ev, "edge-exposure")

	res := edgeResult(t, ev)
	wantState(t, &res.Currency, assessment.CurrencyReviewRequired, assessment.DetailSubjectDigestMismatch)

	currencyAssertRemoval(t, &res.Currency, "k8s:ingress/api")
}

// Scenario: Record only in the candidate change.
func TestAnApprovalThatIsNotAppliedIsShownWithItsReason(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	in.Verifier = verifiedAll{rejects: map[string]bool{"ASM-EDGE-v1-0001": true}}

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyReviewRequired, assessment.DetailNoApproval)

	if len(res.Currency.NotApplied) != 1 || res.Currency.NotApplied[0].Reason != assessment.NotOnAnchor {
		t.Errorf("not applied = %+v", res.Currency.NotApplied)
	}
}

// Scenario: A28 digest contract removes a security field.
func TestAnApprovalUnderAnotherContractIsReviewRequired(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	ap := approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	ap.ContractVersion = "review-digest/0"

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyReviewRequired, impact.DetailContractChanged)

	if len(res.Currency.Triggers) != 0 {
		t.Errorf("digests of two contracts are not compared: %+v", res.Currency.Triggers)
	}
}

// Scenario: Delta matches.
// Scenario: Delta understates the change.
// Scenario: Later change is not covered.
func TestIncrementalApprovals(t *testing.T) {
	t.Parallel()

	api := entryPoint("k8s:ingress/api", "api.example.com")
	route := entryPoint("k8s:ingress/route", "route.example.com")
	role := impact.V1().NewItem(impact.KindRoleBinding, "k8s:rolebinding/web", impact.Facets{"roleReference": {"admin"}}, nil, nil)

	build := func(t *testing.T, delta func(*assessment.Delta)) (*assessment.EvalInput, *assessment.Approval) {
		t.Helper()

		files := edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "        calmNodes: [api-gateway]\nassumptions",
			"        calmNodes: [api-gateway]\n    - kind: role-binding\n      selector:\n        calmNodes: [api-gateway]\nassumptions")
		in := edgeInputOf(t, newFacts(api), files)
		first := approve(t, in, "ASM-EDGE-v1-0001", atDecision)

		in.Facts = newFacts(api, route)
		factsOf(t, in).matches[impact.KindRoleBinding] = found(role)

		second := approve(t, in, "ASM-EDGE-v1-0002", atDecision.Add(24*time.Hour))
		currencyAssertIncrementalBinding(t, second, first)

		delta(second.Delta)

		return in, second
	}

	t.Run("matches", func(t *testing.T) {
		t.Parallel()

		in, _ := build(t, func(*assessment.Delta) {})
		wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyCurrent, "")
	})

	t.Run("understated", func(t *testing.T) {
		t.Parallel()

		in, _ := build(t, func(d *assessment.Delta) {
			d.Added = slices.DeleteFunc(d.Added, func(it assessment.Item) bool { return it.Kind == assessment.DepRoleBinding })
		})

		ev := evaluate(t, in)
		res := edgeResult(t, ev)
		wantState(t, &res.Currency, assessment.CurrencyReviewRequired, assessment.DetailDeltaInvalid)

		if !slices.ContainsFunc(ev.Diagnostics, func(d diag.Diagnostic) bool {
			return d.Code == diag.ASM006 && strings.Contains(d.Message, "k8s:rolebinding/web")
		}) {
			t.Errorf("diagnostics = %+v, want CO-ASM-006 naming the unlisted item", ev.Diagnostics)
		}
	})

	t.Run("later change", func(t *testing.T) {
		t.Parallel()

		in, _ := build(t, func(*assessment.Delta) {})
		factsOf(t, in).matches[impact.KindPublicEntryPoint] = found(api, route, entryPoint("k8s:ingress/late", "late.example.com"))

		res := edgeResult(t, evaluate(t, in))
		wantState(t, &res.Currency, assessment.CurrencyReviewRequired, assessment.DetailSubjectDigestMismatch)

		if len(res.Currency.Triggers) != 1 || res.Currency.Triggers[0].Dependency != "public-entry-point:k8s:ingress/late" {
			t.Errorf("triggers = %+v", res.Currency.Triggers)
		}
	})

	t.Run("lists an item that did not change", func(t *testing.T) {
		t.Parallel()

		in, _ := build(t, func(d *assessment.Delta) {
			d.Removed = append(d.Removed, assessment.Item{Kind: assessment.DepControl, Identity: "ctl", Digest: digestA})
		})

		wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyReviewRequired, assessment.DetailDeltaInvalid)
	})
}

// Scenario: A23 selector becomes narrower.
// Scenario: Disposition recorded.
// Scenario: Disposition without authority.
func TestARemovedItemStaysUntilADispositionIsApproved(t *testing.T) {
	t.Parallel()

	api := entryPoint("k8s:ingress/api", "api.example.com")
	old := entryPoint("k8s:ingress/old", "old.example.com")

	in := edgeInput(t, newFacts(api, old))
	ap := approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	in.Facts = newFacts(api)

	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyReviewRequired, assessment.DetailSubjectDigestMismatch)

	currencyAssertRemoval(t, &res.Currency, "k8s:ingress/old")

	// The version lists the disposition and the approval binds the version digest: it counts.
	in.Set.Assessment("ASM-EDGE", 1).Dispositions = []assessment.Disposition{{
		Identity: "k8s:ingress/old", Disposition: assessment.DispositionRemovedIntentionally, Reason: "retired route",
	}}
	ap.Assessment.Digest = in.Set.Assessment("ASM-EDGE", 1).Digest

	res = edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyCurrent, "")

	if len(res.Currency.Disposed) != 1 || len(res.Currency.Undisposed) != 0 {
		t.Errorf("disposed = %+v, undisposed = %+v", res.Currency.Disposed, res.Currency.Undisposed)
	}

	// A disposition that no verified approval binds still blocks.
	ap.Assessment.Digest = digestA

	res = edgeResult(t, evaluate(t, in))
	if res.Currency.State != assessment.CurrencyReviewRequired || len(res.Currency.Undisposed) != 1 {
		t.Errorf("state = %s, undisposed = %+v", res.Currency.State, res.Currency.Undisposed)
	}
}

// Scenario: A30 traversal limit reached.
func TestALimitStopGivesUnknown(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	in.Facts = newFacts(entryPoint("k8s:ingress/api", "api.example.com"), entryPoint("k8s:ingress/new", "new.example.com"))
	in.Limits = impact.Limits{MaxVisited: 1}

	ev := evaluate(t, in)
	res := edgeResult(t, ev)
	wantState(t, &res.Currency, assessment.CurrencyUnknown, assessment.ReasonLimitExceeded)

	if res.Currency.Analysis.Incomplete == nil || res.Currency.Analysis.Incomplete.Limit != impact.LimitVisited {
		t.Errorf("analysis = %+v", res.Currency.Analysis)
	}

	if !slices.ContainsFunc(ev.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == diag.ASM008 }) {
		t.Errorf("diagnostics = %+v, want CO-ASM-008", ev.Diagnostics)
	}

	// Scenario: Limit raised.
	in.Limits = impact.Limits{}
	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyReviewRequired, assessment.DetailSubjectDigestMismatch)
}

// Scenario: SEC-07 selector not resolvable.
func TestAnUnresolvedBindingWidensTheClosure(t *testing.T) {
	t.Parallel()

	api := entryPoint("k8s:ingress/api", "api.example.com")
	files := edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "      selector:\n        calmNodes: [api-gateway]\nassumptions",
		"      selector:\n        calmNodes: [api-gateway]\n        host: [api.example.com]\nassumptions")
	in := edgeInputOf(t, newFacts(api), files)
	ap := approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	facts := newFacts(api)
	facts.gap = &struct {
		kind  impact.Kind
		match impact.Match
	}{impact.KindPublicEntryPoint, impact.Match{State: impact.MatchUnknown, Reason: "binding-gap"}}
	in.Facts = facts

	// The scope query (with calmNodes only) gives the same item: the widened comparison finds no change.
	res := edgeResult(t, evaluate(t, in))
	wantState(t, &res.Currency, assessment.CurrencyCurrent, "")

	if !res.Currency.Analysis.Widened {
		t.Errorf("analysis = %+v, want a widened closure", res.Currency.Analysis)
	}

	// Without a declared scope to widen to the currency is unknown.
	in.Set.Assessment("ASM-EDGE", 1).Scope.CalmNodes = nil
	ap.Scope = assessment.Scope{}

	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyUnknown, assessment.ReasonCoverageIncomplete)
}

// Scenario: A27 provider upgrade loses selector support.
func TestACapabilityLossIsARegression(t *testing.T) {
	t.Parallel()

	api := entryPoint("k8s:ingress/api", "api.example.com")
	in := edgeInput(t, newFacts(api))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	facts := &lossyFacts{fakeFacts: newFacts(api)}
	in.Facts = facts

	res := edgeResult(t, evaluate(t, in))

	if len(res.Currency.Regressions) == 0 || res.Currency.Regressions[0].Kind != impact.KindPublicEntryPoint {
		t.Fatalf("regressions = %+v", res.Currency.Regressions)
	}

	if res.Currency.State == assessment.CurrencyCurrent {
		t.Errorf("a capability loss is never current: %+v", res.Currency)
	}
}

// Scenario: Quality assessment is returned.
// Scenario: Currency per subject is available to views.
func TestCurrencyForReturnsTheSameResultsIncludingQuality(t *testing.T) {
	t.Parallel()

	in, facts := currencyQualityInput(t)
	ap := approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	ev := evaluate(t, in)

	edge := currencySingle(t, ev.CurrencyFor("api-gateway"), "api-gateway")
	currencyAssertEdgeView(t, edge, ap)
	currencyAssertQualityView(t, currencySingle(t, ev.CurrencyFor("checkout"), "checkout"))

	byScenario := ev.CurrencyFor("QS-LATENCY-1")
	if len(byScenario) != 1 || byScenario[0].ID != "ASM-LATENCY" {
		t.Errorf("CurrencyFor(QS-LATENCY-1) = %+v", byScenario)
	}

	currencyAssertNoViews(t, ev)

	// The same evaluation gives the same answer: nothing is evaluated again.
	facts.matches[impact.KindPublicEntryPoint] = found()

	currencyAssertViewUnchanged(t, ev, edge)
}

func TestEvaluationIsDeterministic(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)
	in.Facts = newFacts(entryPoint("k8s:ingress/api", "api.example.com"), entryPoint("k8s:ingress/b", "b.example.com"), entryPoint("k8s:ingress/c", "c.example.com"))

	first, err := jsonOf(evaluate(t, in))
	if err != nil {
		t.Fatal(err)
	}

	for range 5 {
		next, err := jsonOf(evaluate(t, in))
		if err != nil {
			t.Fatal(err)
		}

		if next != first {
			t.Fatalf("evaluation differs between runs:\n%s\n%s", first, next)
		}
	}
}

func TestARequiredEntryWithoutADocument(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts())
	in.Required = append(in.Required, currencyUnknownRequirement())

	currencyAssertRequiredWithoutDocument(t, currencyRequiredByID(evaluate(t, in), "data"))
}

func TestSubjectDigestIsOrderIndependent(t *testing.T) {
	t.Parallel()

	a := assessment.Item{Kind: assessment.DepProtocol, Identity: "a", Digest: digestA}
	b := assessment.Item{Kind: assessment.DepBinding, Identity: "b", Digest: digestB}

	if assessment.SubjectDigest([]assessment.Item{a, b}) != assessment.SubjectDigest([]assessment.Item{b, a}) {
		t.Error("the digest depends on the order")
	}

	if assessment.SubjectDigest(nil) == assessment.SubjectDigest([]assessment.Item{a}) || !strings.HasPrefix(assessment.SubjectDigest(nil), "sha256:") {
		t.Error("the empty subject needs its own digest")
	}
}

// Scenario: A28 digest contract removes a security field.
// Scenario: Normalization upgrade without equivalence.
// Scenario: Evaluator version change.
func TestAChangeOfAnInvalidatorMakesApprovalsReviewRequired(t *testing.T) {
	t.Parallel()

	api := entryPoint("k8s:ingress/api", "api.example.com")

	for _, tc := range []struct {
		name   string
		change func(in *assessment.EvalInput)
		reason string
	}{
		{"normalization", func(in *assessment.EvalInput) {
			factsOf(t, in).evaluators = map[string]string{impact.EvaluatorNormalization: "2", "cbm": "0.12.1"}
		}, assessment.DetailNormalizationChanged},
		{"evaluator", func(in *assessment.EvalInput) {
			factsOf(t, in).evaluators = map[string]string{impact.EvaluatorNormalization: "1", "cbm": "0.13.0"}
		}, assessment.DetailEvaluatorChanged},
		{"policy", func(in *assessment.EvalInput) { in.PolicyDigest = digestB }, assessment.DetailPolicyChanged},
		{"authority", func(in *assessment.EvalInput) { in.AuthorityDigest = digestB }, assessment.DetailAuthorityChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := edgeInput(t, newFacts(api))
			in.PolicyDigest, in.AuthorityDigest = digestA, digestA

			ap := approve(t, in, "ASM-EDGE-v1-0001", atDecision)
			if ap.ProducedUnder.NormalizationVersion != "1" || ap.ProducedUnder.EvaluatorVersions["cbm"] != "0.12.1" {
				t.Fatalf("produced under = %+v", ap.ProducedUnder)
			}

			wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyCurrent, "")

			tc.change(in)

			wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyReviewRequired, tc.reason)
		})
	}
}

func TestEvidenceDigestComesFromTheFileAtTheRevision(t *testing.T) {
	t.Parallel()

	in := edgeInput(t, newFacts(entryPoint("k8s:ingress/api", "api.example.com")))
	ap := approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	if len(ap.Evidence) != 1 || ap.Evidence[0].Digest != digestA {
		t.Fatalf("evidence = %+v", ap.Evidence)
	}

	factsOf(t, in).files["docs/security/edge-review.md"] = digestB

	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyReviewRequired, assessment.DetailEvidenceDigestMismatch)

	delete(factsOf(t, in).files, "docs/security/edge-review.md")

	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyUnknown, assessment.ReasonReferenceUnresolved)
}
