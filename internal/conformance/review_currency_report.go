package conformance

import (
	"cmp"
	"slices"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/impact"
)

// ---------------------------------------------------------------------------------------------
// The report sections

// currencyReport makes the `assessmentCurrency` object from the evaluation.
func currencyReport(eval *assessment.Evaluation) *AssessmentCurrencyReport {
	report := &AssessmentCurrencyReport{
		EvaluatedAt: clock.ISO(eval.At), ContractVersion: eval.ContractVersion, CapabilityProfile: eval.CapabilityProfile,
		Assessments: make([]AssessmentEntry, 0, len(eval.Assessments)), Required: make([]RequiredEntry, 0, len(eval.Required)),
	}

	for i := range eval.Assessments {
		report.Assessments = append(report.Assessments, assessmentEntry(&eval.Assessments[i]))
	}

	for i := range eval.Required {
		report.Required = append(report.Required, requiredEntry(&eval.Required[i]))
	}

	return report
}

// assessmentEntry makes the report entry of one assessment version.
func assessmentEntry(r *assessment.Result) AssessmentEntry {
	cur := &r.Currency

	return AssessmentEntry{
		ID: r.ID, Version: r.Version, Type: string(r.Type), Title: r.Title, Environment: r.Environment, Outcome: string(r.Outcome),
		State: string(r.State), Currency: string(cur.State), UnknownReason: cur.UnknownReason, Reasons: nonNil(cur.Reasons),
		Details: nonNil(cur.Details), Causes: currencyCauses(cur), Basis: cur.Basis,
		NotApplied: currencyNotApplied(cur), Triggers: nonNilTriggers(cur.Triggers),
		Required: nonNil(r.Required), File: r.File, Line: r.Line, Digest: r.Digest, Latest: r.Latest,
		Analysis: CurrencyAnalysis{
			Complete: cur.Analysis.Complete, Visited: cur.Analysis.Visited, ClosureSize: cur.Analysis.ClosureSize,
			Widened: cur.Analysis.Widened, Incomplete: cur.Analysis.Incomplete,
		},
		Approval: currencyApprovalOf(cur),
	}
}

// currencyCauses gives the causes of an unknown currency; never nil.
func currencyCauses(cur *assessment.Currency) []CurrencyCause {
	causes := make([]CurrencyCause, 0, len(cur.Causes))
	for _, cause := range cur.Causes {
		causes = append(causes, CurrencyCause{Reason: cause.Reason, Detail: cause.Detail})
	}

	return causes
}

// currencyNotApplied gives the records that did not apply to the currency; never nil.
func currencyNotApplied(cur *assessment.Currency) []CurrencyNotApplied {
	notApplied := make([]CurrencyNotApplied, 0, len(cur.NotApplied))
	for _, n := range cur.NotApplied {
		notApplied = append(notApplied, CurrencyNotApplied{
			Record: n.Record, Kind: n.Kind, Authority: n.Authority, Mechanism: n.Mechanism, Reason: n.Reason, Detail: n.Detail,
		})
	}

	return notApplied
}

// currencyApprovalOf gives the approval that the currency rests on, nil when it has none.
func currencyApprovalOf(cur *assessment.Currency) *CurrencyApproval {
	a := cur.Approval
	if a == nil {
		return nil
	}

	ap := &CurrencyApproval{
		ID: a.ID, Decision: string(a.Decision), Reviewer: a.Reviewer, Authority: a.Authority, Mechanism: a.Mechanism,
		Verified: a.Verified, DecidedAt: clock.ISO(a.DecidedAt), Conditions: nonNil(a.Conditions), Reason: a.Reason,
	}
	if a.ExpiresAt != nil {
		ap.ExpiresAt = clock.ISO(*a.ExpiresAt)
	}

	return ap
}

// requiredEntry makes the report entry of one required assessment.
func requiredEntry(req *assessment.RequiredResult) RequiredEntry {
	verdict := req.Currency.Verdict()

	return RequiredEntry{
		ID: req.ID, Assessment: req.Assessment, Type: req.Type, Environment: req.Environment, Authority: req.Authority,
		Version: req.Version, Currency: string(req.Currency.State), UnknownReason: req.Currency.UnknownReason,
		Reasons: nonNil(req.Currency.Reasons), Verdict: RuleVerdict(verdict.Result),
	}
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

func nonNilTriggers(in []impact.ReviewTrigger) []impact.ReviewTrigger {
	if in == nil {
		return []impact.ReviewTrigger{}
	}

	return in
}

// governanceReport makes the `governance` object: every change of the review configuration against the anchor, and
// the dispositions of the removed subjects.
func (r *reviewRun) governanceReport() *GovernanceReport {
	out := &GovernanceReport{Relaxations: []GovernanceChange{}, Dispositions: []DispositionEntry{}}

	for _, ch := range r.governance {
		out.Relaxations = append(out.Relaxations, GovernanceChange{
			Kind: string(ch.Kind), Subject: ch.Subject, Change: string(ch.Op), Comparison: string(ch.Comparison),
			Relaxation: ch.Relaxation() && !r.disposed[ch.Digest], Digest: ch.Digest, Detail: ch.Detail,
		})
	}

	disp := slices.Clone(r.dispositions)
	slices.SortStableFunc(disp, func(a, b DispositionEntry) int {
		return cmp.Or(canonjson.CompareUTF16(a.Assessment, b.Assessment), canonjson.CompareUTF16(a.Kind, b.Kind),
			canonjson.CompareUTF16(a.Identity, b.Identity))
	})

	out.Dispositions = append(out.Dispositions, disp...)

	return out
}

// analysisReport makes the `analysis` object. A full evaluation has no cache counters.
func analysisReport(eval *assessment.Evaluation) *AnalysisReport {
	out := &AnalysisReport{Mode: AnalysisFull, Complete: true, Visited: eval.Visited, Invalidations: eval.Invalidations}

	if inc := eval.Incremental; inc != nil {
		out.Mode, out.CacheHits, out.CacheMisses = AnalysisIncremental, inc.Hits, inc.Misses
	}

	for i := range eval.Assessments {
		cur := &eval.Assessments[i].Currency

		out.ClosureSize += cur.Analysis.ClosureSize

		if !cur.Analysis.Complete {
			out.Complete = false
			out.IncompleteAnalyses++
		}

		if out.Incomplete == nil && cur.Analysis.Incomplete != nil {
			out.Incomplete = cur.Analysis.Incomplete
		}
	}

	return out
}

// reviewArea gives `areas.assessmentCurrency`: the worst verdict of the review rules and the counts of the
// assessment versions by currency. Without required assessments the area is `not-configured`, never `pass`.
func (r *reviewRun) reviewArea(rules []RuleResult, findings []Finding) AreaResult {
	if r.eval == nil || len(r.eval.Required) == 0 {
		return AreaResult{Verdict: AreaNotConfigured, Counts: map[string]int{}}
	}

	verdicts, counts := reviewRuleVerdicts(rules)

	// The currency of every required assessment counts, also when no review rule is selected.
	for i := range r.eval.Required {
		verdicts = append(verdicts, RuleVerdict(r.eval.Required[i].Currency.Verdict().Result))
	}

	out := map[string]int{
		string(VerdictPass): counts.Pass, string(VerdictViolation): counts.Violation, string(VerdictUnknown): counts.Unknown,
		countNotApplicable: counts.NotApplicable, string(VerdictError): counts.Error, countFindings: countReviewFindings(findings),
		"assessments": len(r.eval.Assessments), "required": len(r.eval.Required),
		"current": 0, "reviewRequired": 0, "expired": 0, "revoked": 0, "currencyUnknown": 0,
	}

	for i := range r.eval.Required {
		if key := currencyCountKey(r.eval.Required[i].Currency.State); key != "" {
			out[key]++
		}
	}

	return AreaResult{Verdict: AreaVerdict(WorstVerdict(verdicts)), Counts: out}
}

// reviewRuleVerdicts gives the verdicts of the rules of the review area and their count by verdict.
func reviewRuleVerdicts(rules []RuleResult) ([]RuleVerdict, RuleCounts) {
	var (
		verdicts []RuleVerdict
		counts   RuleCounts
	)

	for i := range rules {
		if AreaOfFamily(rules[i].Family) != AreaAssessmentCurrency {
			continue
		}

		verdicts = append(verdicts, rules[i].Verdict)
		countReviewVerdict(&counts, rules[i].Verdict)
	}

	return verdicts, counts
}

// countReviewVerdict adds one verdict to the counts.
func countReviewVerdict(counts *RuleCounts, verdict RuleVerdict) {
	switch verdict {
	case VerdictPass:
		counts.Pass++
	case VerdictViolation:
		counts.Violation++
	case VerdictUnknown:
		counts.Unknown++
	case VerdictNotApplicable:
		counts.NotApplicable++
	case VerdictError:
		counts.Error++
	}
}

// countReviewFindings counts the findings of the review area.
func countReviewFindings(findings []Finding) int {
	open := 0

	for i := range findings {
		if findings[i].Area == AreaAssessmentCurrency {
			open++
		}
	}

	return open
}

// currencyCountKey gives the key of the area count of a currency state, "" for a state that has none.
func currencyCountKey(state assessment.CurrencyState) string {
	switch state {
	case assessment.CurrencyCurrent:
		return "current"
	case assessment.CurrencyReviewRequired:
		return "reviewRequired"
	case assessment.CurrencyExpired:
		return "expired"
	case assessment.CurrencyRevoked:
		return "revoked"
	case assessment.CurrencyUnknown:
		return "currencyUnknown"
	default:
		return ""
	}
}
