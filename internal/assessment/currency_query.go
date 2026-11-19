package assessment

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/workspace"
)

// requiredResults evaluates the required entries against the latest version of their assessment id.
func (e *evaluator) requiredResults(out *Evaluation, latest map[string]int) {
	required := slices.Clone(e.in.Required)
	slices.SortStableFunc(required, func(a, b workspace.RequiredAssessment) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	for i := range required {
		row := &required[i]
		res := RequiredResult{
			ID: row.ID, Assessment: row.Assessment, Type: row.Type, Environment: row.Environment, Authority: row.Authority, Scope: row.Scope,
		}

		index := slices.IndexFunc(out.Assessments, func(x Result) bool { return x.ID == row.Assessment && x.Version == latest[row.Assessment] })
		if index < 0 {
			res.Currency = undocumentedCurrency(row)
			out.Required = append(out.Required, res)

			continue
		}

		found := &out.Assessments[index]
		found.Required = append(found.Required, row.ID)
		res.Version = found.Version
		res.Currency = requiredCurrency(row, found)
		out.Required = append(out.Required, res)
	}

	for i := range out.Assessments {
		slices.Sort(out.Assessments[i].Required)
	}
}

// undocumentedCurrency is the currency of a required entry whose assessment has no document.
func undocumentedCurrency(row *workspace.RequiredAssessment) Currency {
	return Currency{
		State: CurrencyReviewRequired, Reasons: []string{DetailNoApproval, DetailNoAssessment},
		Details: []string{fmt.Sprintf("no verified approval: the required assessment %s has no document", row.Assessment)},
		Causes:  []UnknownCause{}, NotApplied: []NotApplied{}, Triggers: []impact.ReviewTrigger{},
		Undisposed: []impact.Change{}, Disposed: []impact.Disposed{}, Regressions: []impact.Regression{},
		Analysis: Analysis{Complete: true},
	}
}

// requiredCurrency applies what the required entry demands on top of the result of the version: the environment,
// the type, the scope, and the authority of the approval.
func requiredCurrency(row *workspace.RequiredAssessment, found *Result) Currency {
	cur := found.Currency
	cur.Reasons, cur.Details = slices.Clone(cur.Reasons), slices.Clone(cur.Details)

	before := len(cur.Reasons)

	cur.addRequirementReasons(row, found)

	if len(cur.Reasons) > before && (cur.State == CurrencyCurrent || cur.State == CurrencyUnknown) {
		cur.State = CurrencyReviewRequired
		cur.UnknownReason = ""
	}

	return cur
}

// addRequirementReasons adds a reason for each demand of the required entry that the version does not meet.
func (c *Currency) addRequirementReasons(row *workspace.RequiredAssessment, found *Result) {
	if row.Environment != "" && row.Environment != found.Environment {
		c.addReason(DetailEnvironmentMismatch, fmt.Sprintf("%s requires environment %s and %s is for %s", row.ID, row.Environment, found.Key(), found.Environment))
	}

	if row.Type != "" && row.Type != string(found.Type) {
		c.addReason(DetailTypeMismatch, fmt.Sprintf("%s requires type %s and %s has type %s", row.ID, row.Type, found.Key(), found.Type))
	}

	if missing := missingFrom(&found.Scope, &row.Scope); missing != "" {
		c.addReason(DetailScopeMismatch, fmt.Sprintf("%s requires %s and the scope of %s does not cover it", row.ID, missing, found.Key()))
	}

	if c.approvalFromOtherAuthority(row.Authority) {
		c.addReason(DetailAuthorityMismatch, fmt.Sprintf("%s requires the authority %s and approval %s is from %s",
			row.ID, row.Authority, c.Approval.ID, c.Approval.Authority))
	}
}

// approvalFromOtherAuthority tells whether a verified approval that still counts is from another authority than the
// required one.
func (c *Currency) approvalFromOtherAuthority(required string) bool {
	if c.Approval == nil || !c.Approval.Verified || required == "" {
		return false
	}

	if c.State == CurrencyRevoked || c.State == CurrencyExpired {
		return false
	}

	return c.Approval.Authority != required
}

// missingFrom names the first element of the required scope that the assessment scope does not list.
func missingFrom(have *Scope, want *workspace.AssessmentScope) string {
	for _, c := range []struct {
		name       string
		have, want []string
	}{
		{"calm node", have.CalmNodes, want.CalmNodes},
		{"node type", have.NodeTypes, want.NodeTypes},
		{"tag", have.Tags, want.Tags},
	} {
		for _, w := range c.want {
			if !slices.Contains(c.have, w) {
				return c.name + " " + w
			}
		}
	}

	return ""
}

// SubjectCurrency is one assessment that CurrencyFor returns for a subject.
type SubjectCurrency struct {
	ID            string        `json:"id"`
	Type          Type          `json:"type"`
	Title         string        `json:"title,omitempty"`
	State         CurrencyState `json:"state"`
	ReviewState   State         `json:"reviewState"`
	Outcome       Outcome       `json:"outcome"`
	UnknownReason string        `json:"unknownReason,omitempty"`
	Approval      string        `json:"approval,omitempty"`
	File          string        `json:"file"`
	Reasons       []string      `json:"reasons"`
	Required      []string      `json:"required"`
	Version       int           `json:"version"`
}

// CurrencyFor gives the assessments that cover a subject: a CALM node or relationship id, a quality scenario id,
// or an assessment id. The latest version of each assessment id is returned, sorted by id, with type, state,
// reasons, ids, and the approval id. It reads the evaluation that `check` made and evaluates nothing. A nil
// evaluation, or a subject that no assessment covers, gives an empty list.
func (ev *Evaluation) CurrencyFor(subject string) []SubjectCurrency {
	out := []SubjectCurrency{}

	if ev == nil {
		return out
	}

	for i := range ev.Assessments {
		res := &ev.Assessments[i]
		if !res.Latest || !slices.Contains(res.Subjects, subject) {
			continue
		}

		entry := SubjectCurrency{
			ID: res.ID, Version: res.Version, Type: res.Type, Title: res.Title, State: res.Currency.State, ReviewState: res.State,
			Outcome: res.Outcome, Reasons: slices.Clone(res.Currency.Reasons), UnknownReason: res.Currency.UnknownReason,
			Required: slices.Clone(res.Required), File: res.File,
		}

		if res.Currency.Approval != nil && res.Currency.Approval.Decision == DecisionApproved {
			entry.Approval = res.Currency.Approval.ID
		}

		out = append(out, entry)
	}

	slices.SortFunc(out, func(a, b SubjectCurrency) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	return out
}

// Verdicts of the rule `REV-APR-001`. The strings are the values of the rule verdicts of the report.
const (
	VerdictPass      = "pass"
	VerdictViolation = "violation"
	VerdictUnknown   = "unknown"
)

// Verdict is the verdict of the review obligation of one assessment (rule REV-APR-001).
type Verdict struct {
	// Result is VerdictPass, VerdictViolation, or VerdictUnknown.
	Result string `json:"verdict"`
	// Reason is the one closed reason of an unknown verdict, empty otherwise.
	Reason string `json:"reason,omitempty"`
	// Detail is the sentence that says why the verdict is not a pass.
	Detail string `json:"detail,omitempty"`
}

// Verdict maps the currency to the verdict of REV-APR-001 (design D8): `current` is a pass; `review-required`,
// `expired`, `revoked`, and no approval are violations; `unknown` is unknown with one reason from the closed set.
func (c *Currency) Verdict() Verdict {
	if c.State == CurrencyCurrent {
		return Verdict{Result: VerdictPass}
	}

	if c.State == CurrencyUnknown {
		detail := ""
		if len(c.Causes) > 0 {
			detail = c.Causes[0].Detail
		}

		return Verdict{Result: VerdictUnknown, Reason: cmp.Or(c.UnknownReason, ReasonCoverageIncomplete), Detail: detail}
	}

	detail := string(c.State)
	if len(c.Reasons) > 0 {
		detail = c.Reasons[0]
	}

	if len(c.Details) > 0 {
		detail += ": " + c.Details[0]
	}

	return Verdict{Result: VerdictViolation, Detail: detail}
}

// Binding is the current binding of one assessment version: what an approval must record so that it is current.
type Binding struct {
	Assessment        AssessmentRef   `json:"assessment"`
	Subject           ApprovalSubject `json:"subject"`
	Scope             Scope           `json:"scope"`
	Environment       string          `json:"environment"`
	Assumptions       []Ref           `json:"assumptions"`
	Evidence          []Ref           `json:"evidence"`
	ContractVersion   string          `json:"contractVersion"`
	CapabilityProfile string          `json:"capabilityProfile"`
	// ProducedUnder are the normalization and evaluator versions, the policy digest, and the authority digest that
	// the run uses now. An approval records them.
	ProducedUnder ProducedUnder `json:"producedUnder"`
	// Previous and Delta are set when a verified approval of this or an earlier version is the comparison basis.
	Previous *Previous `json:"previous,omitempty"`
	Delta    *Delta    `json:"delta,omitempty"`
	// Dispositions are the dispositions that the version lists. A verified approval of the version binds them.
	Dispositions []Disposition `json:"dispositions"`
	// Unknown lists the causes that stop an approval: when not empty, a subject digest cannot be recorded.
	Unknown []UnknownCause `json:"unknown"`
}

// Bind gives the current binding of one assessment version, from the same inputs as Evaluate and without
// judging currency. `assessment approve` records it.
func Bind(ctx context.Context, in *EvalInput, id string, version int) (*Binding, error) {
	eval, err := newEvaluator(ctx, in)
	if err != nil {
		return nil, err
	}

	doc := eval.set.Assessment(id, version)
	if doc == nil {
		return nil, fmt.Errorf("assessment %s does not exist", VersionKey(id, version))
	}

	subj, err := eval.resolve(ctx, doc)
	if err != nil {
		return nil, err
	}

	pick := eval.choose(doc)
	bind := newBinding(doc, eval)
	bind.Assumptions = eval.assumptionRefs(doc)
	bind.Evidence = eval.evidenceRefs(subj)
	bind.ProducedUnder = eval.producedUnder()

	items := subjectItems(subj)
	bind.Subject = ApprovalSubject{Digest: SubjectDigest(items), Dependencies: items}
	bind.Unknown = sortCauses(slices.Concat(subj.causes, pick.unknown))
	bind.setComparison(pick, items)

	return bind, nil
}

// newBinding starts the binding of a version with what the document and the contract give.
func newBinding(doc *Assessment, eval *evaluator) *Binding {
	bind := &Binding{
		Assessment: AssessmentRef{ID: doc.ID, Version: doc.Version, Digest: doc.Digest}, Scope: doc.Scope, Environment: doc.Environment,
		Assumptions: []Ref{}, Evidence: []Ref{}, ContractVersion: eval.contract.Version, CapabilityProfile: eval.profile.ID,
		Dispositions: slices.Clone(doc.Dispositions), Unknown: []UnknownCause{},
	}

	if bind.Dispositions == nil {
		bind.Dispositions = []Disposition{}
	}

	return bind
}

// evidenceRefs gives the evidence of the subject. A digest that the document lacks and no fact source can tell is a
// cause that stops an approval.
func (e *evaluator) evidenceRefs(subj *subject) []Ref {
	out := make([]Ref, 0, len(subj.evidence))

	for _, ref := range subj.evidence {
		if ref.Digest == "" && e.in.Facts == nil {
			subj.cause(ReasonCoverageIncomplete, fmt.Sprintf("the digest of evidence %s cannot be told: the document has none and no fact source is configured", ref.ID))
		}

		out = append(out, ref)
	}

	return out
}

// subjectItems gives the sealed items of the subject without the kinds that it excludes. A sealed item that cannot
// be told is a cause that stops an approval.
func subjectItems(subj *subject) []Item {
	for i := range subj.sealed {
		if unknown := subj.sealed[i].Unknown; unknown != nil {
			subj.cause(unknown.Reason, unknown.Detail)
		}
	}

	items := ItemsOf(subj.sealed)
	for kind := range subj.excluded {
		items = slices.DeleteFunc(items, func(it Item) bool { return impact.Kind(it.Kind) == kind })
	}

	return items
}

// setComparison records the approval, or the basis, that the items are compared with and the delta to it.
func (b *Binding) setComparison(pick *choice, items []Item) {
	prev := cmp.Or(pick.approval, pick.basis)
	if prev == nil {
		return
	}

	b.Previous = &Previous{Approval: prev.ID, SubjectDigest: prev.Subject.Digest}
	delta := deltaOf(prev.Subject.Dependencies, items)
	b.Delta = &delta
}

// producedUnder gives the values the run produces under now, as an approval records them.
func (e *evaluator) producedUnder() ProducedUnder {
	out := ProducedUnder{EvaluatorVersions: map[string]string{}, PolicyDigest: e.in.PolicyDigest, AuthorityDigest: e.in.AuthorityDigest}

	if e.in.Facts == nil {
		return out
	}

	for name, version := range e.in.Facts.EvaluatorVersions() {
		if name == impact.EvaluatorNormalization {
			out.NormalizationVersion = version
		} else {
			out.EvaluatorVersions[name] = version
		}
	}

	return out
}
