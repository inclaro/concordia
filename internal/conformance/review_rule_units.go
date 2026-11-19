package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/policy"
)

// ---------------------------------------------------------------------------------------------
// REV-APR-001

// reasonOfCurrency gives the finding reason of a violation currency.
func reasonOfCurrency(cur *assessment.Currency) string {
	switch cur.State {
	case assessment.CurrencyExpired:
		return reasonExpired
	case assessment.CurrencyRevoked:
		return reasonRevoked
	case assessment.CurrencyReviewRequired:
		if slices.Contains(cur.Reasons, assessment.DetailNoApproval) || slices.Contains(cur.Reasons, assessment.DetailNoAssessment) {
			return reasonNoApproval
		}

		return reasonReviewRequired
	case assessment.CurrencyCurrent, assessment.CurrencyUnknown:
	}

	return reasonReviewRequired
}

// approvalUnits are the units of REV-APR-001: one for each required assessment.
func (c *checker) approvalUnits(run *reviewRun, plan *policy.RulePlan) []reviewUnit {
	byKey := map[string]*assessment.Result{}

	for i := range run.eval.Assessments {
		r := &run.eval.Assessments[i]
		byKey[r.Key()] = r
	}

	units := make([]reviewUnit, 0, len(run.eval.Required))

	for i := range run.eval.Required {
		req := &run.eval.Required[i]
		verdict := req.Currency.Verdict()
		unit := reviewUnit{subject: cmp.Or(req.Assessment, req.ID), env: req.Environment, key: req.ID}

		if verdict.Result == assessment.VerdictPass {
			unit.verdict = VerdictPass
			units = append(units, unit)

			continue
		}

		file, line := "", 0
		if res := byKey[assessment.VersionKey(req.Assessment, req.Version)]; res != nil {
			file, line = res.File, res.Line
		}

		spec := reviewSpec{
			Result: RuleVerdict(verdict.Result), Source: req.ID, Target: req.Assessment, Subject: unit.subject, Environment: req.Environment,
			Key: req.ID, Location: assessmentLocation(file, line), Related: []string{req.Assessment},
			Expected: fmt.Sprintf("the required assessment %s has a current verified approval", req.ID),
		}

		if verdict.Result == assessment.VerdictUnknown {
			spec.Reason = verdict.Reason
			spec.Observed = cmp.Or(verdict.Detail, "the currency of the assessment cannot be told")
			spec.Message = fmt.Sprintf("the currency of the required assessment %s is unknown (%s): %s", req.ID, verdict.Reason, spec.Observed)
			unit.verdict, unit.code, unit.message = VerdictUnknown, verdict.Reason, spec.Message
		} else {
			spec.Reason = reasonOfCurrency(&req.Currency)
			spec.Observed = cmp.Or(verdict.Detail, string(req.Currency.State))
			spec.Message = reviewMessage(req.ID, &req.Currency, spec.Observed)
			unit.verdict, unit.code, unit.message = VerdictViolation, spec.Reason, spec.Message
		}

		spec.NextAction = nextActionOfReview(unit.verdict, spec.Reason)
		f := c.reviewFinding(plan, &spec)
		unit.finding = &f
		units = append(units, unit)
	}

	return units
}

// reviewMessage says why a required assessment needs review. A trigger gives the sentence of the report: it names
// the changed dependency, the class, the assessment, and the action, and never says vulnerable or compliant.
func reviewMessage(id string, cur *assessment.Currency, observed string) string {
	if len(cur.Triggers) > 0 {
		text := cur.Triggers[0].Text()
		if more := len(cur.Triggers) - 1; more > 0 {
			text += " (and " + strconv.Itoa(more) + " more)"
		}

		return text
	}

	return fmt.Sprintf("the required assessment %s needs review: %s; next action: %s", id, observed, NextActionGetReview)
}

// requiredResults gives the latest assessment versions that a required entry resolves to, sorted by id.
func requiredResults(eval *assessment.Evaluation) []*assessment.Result {
	var out []*assessment.Result

	for i := range eval.Assessments {
		r := &eval.Assessments[i]
		if r.Latest && len(r.Required) > 0 {
			out = append(out, r)
		}
	}

	return out
}

// dispositionUnits are the units of REV-DSP-001: one for each removed item that no verified disposition accounts
// for (violation); an assessment with none is one passing unit.
func (c *checker) dispositionUnits(run *reviewRun, plan *policy.RulePlan) []reviewUnit {
	var units []reviewUnit

	for _, res := range requiredResults(run.eval) {
		cur := &res.Currency

		for i := range cur.Disposed {
			d := &cur.Disposed[i]
			run.dispositions = append(run.dispositions, DispositionEntry{
				Assessment: res.ID, Kind: string(d.Change.Kind), Identity: d.Change.Identity, State: "disposed",
				Disposition: string(d.Disposition.Type), MovedTo: d.Disposition.MovedTo,
			})
		}

		if len(cur.Undisposed) == 0 {
			units = append(units, reviewUnit{verdict: VerdictPass, subject: res.ID, env: res.Environment, key: res.ID})

			continue
		}

		for i := range cur.Undisposed {
			ch := &cur.Undisposed[i]
			name := impact.DependencyName(ch.Kind, ch.Identity)
			run.dispositions = append(run.dispositions, DispositionEntry{
				Assessment: res.ID, Kind: string(ch.Kind), Identity: ch.Identity, State: "undisposed",
			})

			spec := reviewSpec{
				Result: VerdictViolation, Reason: reasonUndisposed, Source: res.ID, Target: name, Subject: res.ID, Environment: res.Environment,
				Key: name, Location: assessmentLocation(res.File, res.Line), Related: []string{res.ID}, NextAction: NextActionGetReview,
				Expected: fmt.Sprintf("the removed or narrowed dependency %s of %s has a verified disposition", name, res.Key()),
				Observed: name + " was approved and is no longer in the evaluated subject; no verified disposition covers it",
			}
			spec.Message = fmt.Sprintf("%s was removed from the subject of %s without a recorded disposition; next action: %s", name, res.Key(), NextActionGetReview)

			f := c.reviewFinding(plan, &spec)
			units = append(units, reviewUnit{
				verdict: VerdictViolation, subject: res.ID, env: res.Environment, code: reasonUndisposed, message: spec.Message, finding: &f,
			})
		}
	}

	return units
}

// capabilityUnits are the units of REV-CAP-001: one for each capability loss that touches an approved dependency and
// that no verified disposition accounts for (unknown, with the reason of the loss); an assessment with none is one
// passing unit.
func (c *checker) capabilityUnits(run *reviewRun, plan *policy.RulePlan) []reviewUnit {
	var units []reviewUnit

	for _, res := range requiredResults(run.eval) {
		cur := &res.Currency
		open := 0

		for i := range cur.Regressions {
			reg := &cur.Regressions[i]
			if slices.ContainsFunc(cur.Disposed, func(d impact.Disposed) bool { return d.Change.Kind == reg.Kind && d.Change.Identity == reg.Identity }) {
				continue
			}

			open++
			name := impact.DependencyName(reg.Kind, reg.Identity)
			key := name + "#" + reg.Facet

			spec := reviewSpec{
				Result: VerdictUnknown, Reason: reg.Reason, Source: res.ID, Target: key, Subject: res.ID, Environment: res.Environment,
				Key: key, Location: assessmentLocation(res.File, res.Line), Related: []string{res.ID},
				Expected: fmt.Sprintf("the capability profile %s still supports %s, which %s depends on", reg.Profile, name, res.Key()),
				Observed: reg.Detail,
			}
			spec.Message = fmt.Sprintf("%s: %s; %s needs review; next action: %s", name, reg.Detail, res.Key(), NextActionGetReview)
			spec.NextAction = nextActionOfReview(VerdictUnknown, reg.Reason)

			f := c.reviewFinding(plan, &spec)
			units = append(units, reviewUnit{
				verdict: VerdictUnknown, subject: res.ID, env: res.Environment, code: reg.Reason, message: spec.Message, finding: &f,
			})
		}

		if open == 0 {
			units = append(units, reviewUnit{verdict: VerdictPass, subject: res.ID, env: res.Environment, key: res.ID})
		}
	}

	return units
}

// ---------------------------------------------------------------------------------------------
// REV-GOV-001

// governanceComparable says whether the review configuration can be compared with the trust anchor.
func (r *reviewRun) governanceComparable() bool {
	return r.anchor != nil && r.anchor.Readable() && r.governanceFailure == ""
}

// anchorUnreadableDetail says why the review configuration cannot be compared with the trust anchor.
func (r *reviewRun) anchorUnreadableDetail() string {
	switch {
	case r.governanceFailure != "":
		return r.governanceFailure
	case r.anchor != nil && r.anchor.Unreadable != nil:
		return r.anchor.Unreadable.Detail
	default:
		return "the trust anchor cannot be read"
	}
}

// governanceUnits are the units of REV-GOV-001: one for each relaxation of the review configuration against the
// trust anchor that no record of a governance authority binds (violation). With an unreadable anchor nothing can be
// compared: one unknown unit with the reason `trust-anchor-unreadable`. With no relaxation the rule passes.
func (c *checker) governanceUnits(run *reviewRun, plan *policy.RulePlan) []reviewUnit {
	if !run.governanceComparable() {
		return []reviewUnit{c.anchorUnreadableUnit(run, plan)}
	}

	var units []reviewUnit

	for i := range run.governance {
		if change := &run.governance[i]; change.Relaxation() {
			units = append(units, c.relaxationUnit(run, plan, change))
		}
	}

	if len(units) == 0 {
		units = append(units, reviewUnit{verdict: VerdictPass, subject: "review-configuration", key: "review-configuration"})
	}

	return units
}

// anchorUnreadableUnit is the one unknown unit of a run whose trust anchor cannot be compared.
func (c *checker) anchorUnreadableUnit(run *reviewRun, plan *policy.RulePlan) reviewUnit {
	detail := run.anchorUnreadableDetail()

	spec := reviewSpec{
		Result: VerdictUnknown, Reason: string(ReasonTrustAnchorUnreadable), Source: reviewAnchorSubject, Target: "governance", Subject: reviewAnchorSubject,
		Key: reviewAnchorSubject, Related: nil, NextAction: nextActionConfig,
		Expected: "the review configuration is compared with the trust anchor",
		Observed: detail,
	}
	spec.Message = "the review configuration cannot be compared with the trust anchor: " + detail

	f := c.reviewFinding(plan, &spec)

	return reviewUnit{
		verdict: VerdictUnknown, subject: spec.Subject, code: string(ReasonTrustAnchorUnreadable), message: spec.Message, finding: &f,
	}
}

// relaxationUnit is the unit of one relaxation: a pass when a governance record binds it, else a violation.
func (c *checker) relaxationUnit(run *reviewRun, plan *policy.RulePlan, change *impact.SettingChange) reviewUnit {
	subject := string(change.Kind) + ":" + change.Subject
	if run.disposed[change.Digest] {
		return reviewUnit{verdict: VerdictPass, subject: subject, key: change.Digest}
	}

	spec := reviewSpec{
		Result: VerdictViolation, Reason: reasonRelaxation, Source: subject, Target: change.Digest, Subject: subject, Key: change.Digest,
		NextAction: NextActionGetReview,
		Expected:   "a record of an authority with role governance at the trust anchor binds the change of " + subject,
		Observed:   fmt.Sprintf("%s (%s): %s; the change is %s than the trust anchor", subject, change.Op, change.Detail, change.Comparison),
	}
	spec.Message = fmt.Sprintf("the candidate relaxes %s (%s) without a governance disposition; next action: %s", subject, change.Op, NextActionGetReview)

	f := c.reviewFinding(plan, &spec)

	return reviewUnit{
		verdict: VerdictViolation, subject: subject, code: reasonRelaxation, message: spec.Message, finding: &f,
	}
}
