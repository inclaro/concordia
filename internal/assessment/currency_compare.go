package assessment

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
)

// currency decides the currency of one assessment version (design D8, steps 3 to 6).
func (e *evaluator) currency(doc *Assessment) Currency {
	pick := e.choices[doc.Key()]
	subj := e.subjects[doc.Key()]

	cur := e.initialCurrency(doc, subj, pick)

	// Step 3: a verified revocation and an expiry time are judged on every call.
	if e.endedApproval(doc, subj, pick, &cur) {
		return cur
	}

	// Step 4: the comparisons.
	if pick.approval == nil {
		addMissingApprovalReason(doc, pick, &cur)
	} else {
		e.compareApproval(doc, subj, pick, &cur)
	}

	e.judgeItems(doc, subj, pick, &cur)

	if mayHaveBeenApproved(pick, &cur) {
		// A record that could not be judged may have been the approval: the result is unknown, not a missing
		// approval.
		cur.Reasons, cur.Details = []string{}, []string{}
	}

	return e.settle(doc, subj, pick, &cur)
}

// initialCurrency starts the currency of a version with the approval that applies or was rejected.
func (e *evaluator) initialCurrency(doc *Assessment, subj *subject, pick *choice) Currency {
	cur := Currency{
		Reasons: []string{}, Details: []string{}, Causes: []UnknownCause{}, NotApplied: e.notApplied(doc.ID),
		Triggers: []impact.ReviewTrigger{}, Undisposed: []impact.Change{}, Disposed: []impact.Disposed{},
		Regressions: e.regressions(doc, subj, pick),
	}

	if pick.basis != nil {
		cur.Basis = pick.basis.ID
	}

	switch {
	case pick.approval != nil:
		cur.Approval = e.approvalInfo(pick.approval)
	case pick.rejected != nil:
		cur.Approval = e.approvalInfo(pick.rejected)
		cur.Approval.Reason = DetailApprovalRejected
	}

	return cur
}

// endedApproval settles a currency whose approval was revoked or has expired and reports whether it did. Only the
// traversal numbers of the comparison are kept.
func (e *evaluator) endedApproval(doc *Assessment, subj *subject, pick *choice, cur *Currency) bool {
	approval := pick.approval
	if approval == nil {
		return false
	}

	switch {
	case pick.review.Revocation != nil:
		rv := pick.review.Revocation

		e.recordTraversal(doc, subj, pick, cur)
		endApproval(cur, CurrencyRevoked, DetailRevoked, fmt.Sprintf("approval %s was revoked by %s (%s): %s",
			approval.ID, rv.ID, rv.Reviewer.Identity, rv.Reason))
	case approvalExpired(approval, e.now):
		e.recordTraversal(doc, subj, pick, cur)
		endApproval(cur, CurrencyExpired, DetailExpired, fmt.Sprintf("approval %s expired at %s; the evaluation time is %s",
			approval.ID, approval.Expiry.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z"), e.now.Format("2006-01-02T15:04:05Z")))
	default:
		return false
	}

	return true
}

// endApproval sets the final state of a revoked or expired approval.
func endApproval(cur *Currency, state CurrencyState, reason, detail string) {
	cur.State = state
	cur.Reasons = []string{reason}
	cur.Details = []string{detail}
	cur.Approval.Reason = reason
}

// approvalExpired tells whether the approval has an expiry time that the evaluation time has reached.
func approvalExpired(approval *Approval, now time.Time) bool {
	if approval.Expiry == nil || approval.Expiry.ExpiresAt.IsZero() {
		return false
	}

	return !now.Before(approval.Expiry.ExpiresAt)
}

// addMissingApprovalReason records why no verified approval binds the version.
func addMissingApprovalReason(doc *Assessment, pick *choice, cur *Currency) {
	reason, detail := DetailNoApproval, "no verified approval binds "+doc.Key()+"; a reviewed flag or a note in the document is not an approval"
	if pick.rejected != nil {
		reason, detail = DetailApprovalRejected, fmt.Sprintf("approval record %s rejected %s", pick.rejected.ID, doc.Key())
	}

	cur.addReason(reason, detail)

	if pick.basis != nil {
		cur.Details = append(cur.Details, fmt.Sprintf("compared with %s, the approval of an earlier version", pick.basis.ID))
	}
}

// mayHaveBeenApproved tells whether a missing approval may be one of the records that could not be judged.
func mayHaveBeenApproved(pick *choice, cur *Currency) bool {
	if pick.approval != nil || len(pick.unknown) == 0 {
		return false
	}

	return len(cur.Reasons) > 0
}

// settle turns the collected reasons and causes into the state (design D8, steps 5 and 6).
func (e *evaluator) settle(doc *Assessment, subj *subject, pick *choice, cur *Currency) Currency {
	causes := slices.Concat(pick.unknown, subj.causes, cur.Causes)
	if cur.Analysis.Incomplete != nil {
		causes = append(causes, UnknownCause{Reason: ReasonLimitExceeded, Detail: fmt.Sprintf(
			"the traversal stopped at %s=%d after %d nodes", cur.Analysis.Incomplete.Limit, cur.Analysis.Incomplete.Configured, cur.Analysis.Incomplete.Visited)})

		e.note(diag.ASM008, diag.Detail(fmt.Sprintf("%s: %s=%d reached after %d nodes", doc.Key(),
			cur.Analysis.Incomplete.Limit, cur.Analysis.Incomplete.Configured, cur.Analysis.Incomplete.Visited)))
	}

	cur.Causes = sortCauses(causes)
	cur.Analysis.Complete = len(cur.Causes) == 0

	// A traversal that a limit stopped cannot say which assessments the changes reach: when the dependency
	// comparison is the only reason, the result is unknown (limit-exceeded), never an apparent answer.
	onlyChanges := len(cur.Reasons) == 1 && cur.Reasons[0] == DetailSubjectDigestMismatch
	if cur.Analysis.Incomplete != nil && onlyChanges {
		cur.Reasons, cur.Details = []string{}, []string{}
	}

	switch {
	case len(cur.Reasons) > 0:
		cur.State = CurrencyReviewRequired
	case len(cur.Causes) > 0:
		cur.State = CurrencyUnknown
		cur.UnknownReason = cur.Causes[0].Reason
	default:
		cur.State = CurrencyCurrent
	}

	return *cur
}

// sortCauses orders the causes by the closed reason order, then by detail, without repeats.
func sortCauses(list []UnknownCause) []UnknownCause {
	out := slices.Clone(list)
	if out == nil {
		out = []UnknownCause{}
	}

	slices.SortStableFunc(out, func(x, y UnknownCause) int {
		return cmp.Or(cmp.Compare(slices.Index(unknownOrder, x.Reason), slices.Index(unknownOrder, y.Reason)),
			canonjson.CompareUTF16(x.Detail, y.Detail))
	})

	return slices.Compact(out)
}

// addReason adds a reason once and its detail.
func (c *Currency) addReason(reason, detail string) {
	if !slices.Contains(c.Reasons, reason) {
		c.Reasons = append(c.Reasons, reason)
	}

	if detail != "" && !slices.Contains(c.Details, detail) {
		c.Details = append(c.Details, detail)
	}
}

// approvalInfo describes a verified approval for the output.
func (e *evaluator) approvalInfo(approval *Approval) *ApprovalInfo {
	info := &ApprovalInfo{
		ID: approval.ID, Decision: approval.Decision, Reviewer: approval.Reviewer.Identity, Authority: approval.Reviewer.Authority, Verified: true,
		DecidedAt: approval.DecidedAt, Conditions: []string{}, ContractVersion: approval.ContractVersion,
	}

	for _, v := range e.verified.Results {
		if v.RecordID == approval.ID && v.Kind == RecordApproval {
			info.Mechanism = v.Mechanism
			info.Authority = cmp.Or(v.Authority, info.Authority)
		}
	}

	if approval.Expiry != nil {
		info.addExpiry(approval.Expiry)
	}

	return info
}

// addExpiry shows the expiry time and the conditions of the record.
func (info *ApprovalInfo) addExpiry(expiry *Expiry) {
	if !expiry.ExpiresAt.IsZero() {
		at := expiry.ExpiresAt.UTC()
		info.ExpiresAt = &at
	}

	info.Conditions = append([]string{}, expiry.Conditions...)
}

// notApplied lists the decision records of an assessment that did not verify.
func (e *evaluator) notApplied(id string) []NotApplied {
	ids := e.decisionRecordIDs(id)
	out := []NotApplied{}

	for _, v := range e.verified.Results {
		if ids[v.RecordID] && !v.Applied {
			out = append(out, NotApplied{
				Record: v.RecordID, Kind: v.Kind, Authority: v.Authority, Mechanism: v.Mechanism, Reason: v.Reason, Detail: v.Detail,
			})
		}
	}

	return out
}

// decisionRecordIDs names the approvals of an assessment and the revocations of those approvals.
func (e *evaluator) decisionRecordIDs(id string) map[string]bool {
	ids := map[string]bool{}

	for _, ap := range e.set.Approvals {
		if ap.Assessment.ID == id {
			ids[ap.ID] = true
		}
	}

	for _, rv := range e.set.Revocations {
		if ids[rv.Approval.ID] {
			ids[rv.ID] = true
		}
	}

	return ids
}

// compareApproval compares what the approval bound with the current documents and environment (design D8,
// step 4, before the dependency items).
func (e *evaluator) compareApproval(doc *Assessment, subj *subject, pick *choice, cur *Currency) {
	approval := pick.approval

	if approval.Environment != doc.Environment {
		cur.addReason(DetailEnvironmentMismatch, fmt.Sprintf("approval %s binds environment %s and %s is for %s",
			approval.ID, approval.Environment, doc.Key(), doc.Environment))
	}

	if approval.Assessment.Digest != doc.Digest {
		cur.addReason(DetailVersionDigestMismatch, fmt.Sprintf("approval %s binds version digest %s and %s has %s",
			approval.ID, approval.Assessment.Digest, doc.Key(), doc.Digest))
	}

	if !sameScope(&approval.Scope, &doc.Scope) {
		cur.addReason(DetailScopeMismatch, fmt.Sprintf("the scope of %s differs from the scope that approval %s binds", doc.Key(), approval.ID))
	}

	e.compareRefs(doc, subj, approval, cur)

	e.compareInvalidators(approval, cur)

	if e.in.Facts != nil && approval.CapabilityProfile != e.profile.ID {
		cur.addReason(DetailProfileChanged, fmt.Sprintf("approval %s was bound to the capability profile %s and the current profile is %s",
			approval.ID, approval.CapabilityProfile, e.profile.ID))
	}

	if e.subjectDigest(approval.Subject.Dependencies) != approval.Subject.Digest {
		cur.addReason(DetailSubjectDigestMismatch, fmt.Sprintf("approval %s: the subject digest does not match the dependencies it lists", approval.ID))
	}

	e.checkIncremental(approval, cur)
}

// sameScope compares two scopes as sets.
func sameScope(x, y *Scope) bool {
	same := func(a, b []string) bool { return slices.Equal(sortedValues(a), sortedValues(b)) }

	return same(x.CalmNodes, y.CalmNodes) && same(x.CalmRelationships, y.CalmRelationships) &&
		same(x.NodeTypes, y.NodeTypes) && same(x.Tags, y.Tags)
}

// compareRefs compares the assumption and evidence digests that the approval bound with the current documents.
func (e *evaluator) compareRefs(doc *Assessment, subj *subject, approval *Approval, cur *Currency) {
	if problem := diffRefs(approval.Assumptions, e.assumptionRefs(doc), false); problem != "" {
		cur.addReason(DetailAssumptionDigestMismatch, fmt.Sprintf("approval %s: assumptions: %s", approval.ID, problem))
	}

	if problem := diffRefs(approval.Evidence, subj.evidence, true); problem != "" {
		cur.addReason(DetailEvidenceDigestMismatch, fmt.Sprintf("approval %s: evidence: %s", approval.ID, problem))
	}
}

// assumptionRefs gives the id and digest of each assumption of the assessment that exists.
func (e *evaluator) assumptionRefs(doc *Assessment) []Ref {
	refs := make([]Ref, 0, len(doc.Assumptions))

	for _, id := range doc.Assumptions {
		if asm := e.set.Assumption(id); asm != nil {
			refs = append(refs, Ref{ID: id, Digest: asm.Digest})
		}
	}

	return refs
}

// diffRefs names the first difference between what an approval bound and the current refs, "" when equal. With
// lenient set, a digest is compared only when both sides have one (an evidence entry may carry no digest).
func diffRefs(bound, current []Ref, lenient bool) string {
	was, now := refDigests(bound), refDigests(current)

	for _, key := range refIDs(was, now) {
		if problem := refDifference(key, was, now, lenient); problem != "" {
			return problem
		}
	}

	return ""
}

// refDigests maps the id of each ref to its digest.
func refDigests(list []Ref) map[string]string {
	m := make(map[string]string, len(list))
	for _, r := range list {
		m[r.ID] = r.Digest
	}

	return m
}

// refIDs lists the ids of both sets of refs, each once, in UTF-16 order.
func refIDs(was, now map[string]string) []string {
	ids := make([]string, 0, len(was)+len(now))
	for key := range was {
		ids = append(ids, key)
	}

	for key := range now {
		if _, ok := was[key]; !ok {
			ids = append(ids, key)
		}
	}

	slices.SortFunc(ids, canonjson.CompareUTF16)

	return ids
}

// refDifference names how the ref with the id differs between what an approval bound and the current refs, "" when
// it does not.
func refDifference(key string, was, now map[string]string, lenient bool) string {
	w, hadBound := was[key]
	n, hasNow := now[key]

	switch {
	case !hadBound:
		return key + " is not bound by the approval"
	case !hasNow:
		return key + " is bound by the approval and no longer listed"
	case lenient && (w == "" || n == ""):
		return ""
	case w != n:
		return key + " has another digest"
	}

	return ""
}

// usableBaseline gives the choice whose items the comparison may use. The digests of two digest contracts are not
// comparable, so an approval bound under another contract that is not equivalent gives false (the contract
// change is the reason), and a basis approval of such a contract is dropped: everything counts as new.
func (e *evaluator) usableBaseline(pick *choice) (*choice, bool) {
	bound := pick.approval
	if bound == nil {
		bound = pick.basis
	}

	if bound == nil || bound.ContractVersion == e.contract.Version {
		return pick, true
	}

	if equivalent, _ := e.registry.EquivalentFrom(e.contract.Version, bound.ContractVersion); equivalent {
		return pick, true
	}

	if pick.approval != nil {
		return pick, false
	}

	return &choice{review: pick.review, rejected: pick.rejected, unknown: pick.unknown}, true
}

// dispositionsOf gives the dispositions that count: those that the version lists, when a verified approval binds
// the version digest.
func dispositionsOf(doc *Assessment, pick *choice) []impact.Disposition {
	if pick.approval == nil || pick.approval.Assessment.Digest != doc.Digest {
		return nil
	}

	out := make([]impact.Disposition, 0, len(doc.Dispositions))

	for _, d := range doc.Dispositions {
		out = append(out, impact.Disposition{
			Identity: d.Identity, Type: impact.DispositionType(d.Disposition), MovedTo: d.MovedTo, Verified: true,
		})
	}

	return out
}

// recordItems compares the items that the approval (or the basis) lists with the current subject through
// impact.Diff, traverses from the changes, and records the traversal numbers. It gives the choice whose items were
// compared and the comparison, nil when the digests of the baseline cannot be compared.
func (e *evaluator) recordItems(doc *Assessment, subj *subject, pick *choice, cur *Currency) (*choice, *itemComparison) {
	pick, ok := e.usableBaseline(pick)
	if !ok {
		return pick, nil
	}

	cmpn := e.compareWithCache(doc, subj, pick)

	cur.Causes = append(cur.Causes, cmpn.Causes...)
	cur.Analysis = cmpn.Analysis
	cur.Undisposed = cmpn.Undisposed
	cur.Disposed = cmpn.Disposed

	e.noteCycles(cmpn.Cycles)

	return pick, &cmpn
}

// recordTraversal fills in only the traversal numbers: a revoked or expired approval keeps its result and shows what
// changed.
func (e *evaluator) recordTraversal(doc *Assessment, subj *subject, pick *choice, cur *Currency) {
	e.recordItems(doc, subj, pick, cur)
}

// judgeItems records the traversal numbers, the triggers, and the reason that the changed items give.
func (e *evaluator) judgeItems(doc *Assessment, subj *subject, pick *choice, cur *Currency) {
	pick, cmpn := e.recordItems(doc, subj, pick, cur)
	if cmpn == nil || len(cmpn.Changes) == 0 {
		return
	}

	cur.Triggers = cmpn.Triggers

	if pick.approval != nil {
		cur.addReason(DetailSubjectDigestMismatch, fmt.Sprintf("%d dependency item(s) differ from the subject that approval %s binds: %s",
			len(cmpn.Changes), pick.approval.ID, describeChanges(cmpn.Changes)))
	}
}

// evaluateComparison is the comparison that the incremental cache stores: impact.Diff of the baseline with the
// current entries, the traversal from the changes, and the triggers. It reads no clock and no verification result.
func (e *evaluator) evaluateComparison(
	doc *Assessment, subj *subject, baseline, current []impact.Entry, dispositions []impact.Disposition,
) itemComparison {
	diff := impact.Diff(baseline, current, dispositions)
	out := itemComparison{Causes: []UnknownCause{}, Triggers: []impact.ReviewTrigger{}, Changes: []impact.Change{}}

	for _, u := range diff.Unknown {
		out.Causes = append(out.Causes, UnknownCause{Reason: closedReason(u.Reason), Detail: fmt.Sprintf(
			"the digest of %s %s cannot be told (%s)", u.Kind, u.Identity, u.Reason)})
	}

	node := nodeOf(doc)
	res := e.graph.Traverse(diff.Seeds(), impact.Options{Limits: e.in.Limits, TargetTypes: []impact.NodeType{impact.NodeAssessment}})

	out.Analysis = Analysis{
		Visited: len(res.Visited), ClosureSize: len(e.graph.Closure(node)), Widened: subj.widened, Incomplete: res.Incomplete,
	}
	out.Undisposed = diff.Undisposed
	out.Disposed = diff.Disposed
	out.Cycles = res.Cycles

	if changes := diff.Triggers(); len(changes) > 0 {
		out.Changes = changes
		out.Triggers = e.triggersOf(node, &diff, &res)
	}

	return out
}

// noteCycles records the dependency cycles that a traversal saw, each once, as informational notes.
func (e *evaluator) noteCycles(cycles []impact.Cycle) {
	for _, c := range cycles {
		if len(c.Nodes) > 0 {
			e.cycles[nodesKey(c.Nodes)] = c
			e.note(diag.ASM009, diag.Detail(nodesKey(c.Nodes)))
		}
	}
}

// triggersOf gives the trigger of the assessment node from the traversal. A change that the graph did not reach is
// still a trigger: it is never dropped.
func (e *evaluator) triggersOf(node impact.NodeID, diff *impact.DiffResult, res *impact.Result) []impact.ReviewTrigger {
	out := []impact.ReviewTrigger{}

	for _, t := range res.Triggers {
		if t.Target == node {
			out = append(out, impact.NewReviewTrigger(t, e.graph))
		}
	}

	if len(out) > 0 {
		return out
	}

	seeds := diff.Seeds()
	evidence := make([]impact.NodeID, 0, len(seeds))

	for _, s := range seeds {
		evidence = append(evidence, s.Node)
	}

	return append(out, impact.NewReviewTrigger(impact.Trigger{
		Target: node, Path: []impact.NodeID{seeds[0].Node, node}, Changed: seeds, Evidence: evidence,
	}, e.graph))
}

// nodesKey names a cycle by its sorted nodes.
func nodesKey(nodes []impact.NodeID) string {
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.String())
	}

	slices.Sort(names)

	return strings.Join(names, " -> ")
}

// listedChanges is the number of changes that a reason names.
const listedChanges = 5

// describeChanges lists the first changes in words.
func describeChanges(changes []impact.Change) string {
	parts := make([]string, 0, len(changes))

	for i, c := range changes {
		if i == listedChanges {
			parts = append(parts, "and "+strconv.Itoa(len(changes)-listedChanges)+" more")

			break
		}

		parts = append(parts, fmt.Sprintf("%s %s %s", c.Op, c.Kind, c.Identity))
	}

	return strings.Join(parts, ", ")
}

// regressions finds the capability losses that touch the dependencies the approval used (A27): the kind, or a
// selector facet of the assessment, that the profile of this run no longer supports. The dependencies are then
// `unknown` and the assessment shows that a review applies.
func (e *evaluator) regressions(doc *Assessment, subj *subject, pick *choice) []impact.Regression {
	if e.in.Facts == nil || pick.approval == nil {
		return []impact.Regression{}
	}

	facets := map[impact.Kind][]string{}

	for _, d := range e.set.Declared(doc) {
		for facet := range d.Selector {
			if slices.Contains([]string{facetCalmNodes, facetIdentity, facetIdentities, facetEnvironment}, facet) {
				continue
			}

			facets[impact.Kind(d.Kind)] = append(facets[impact.Kind(d.Kind)], facet)
		}
	}

	var uses []impact.Use

	for _, it := range pick.approval.Subject.Dependencies {
		kind := impact.Kind(it.Kind)
		uses = append(uses, impact.Use{Assessment: doc.Key(), Kind: kind, Identity: it.Identity, Facets: sortedValues(facets[kind])})
	}

	out := impact.CapabilityRegressions(e.profile, uses)
	for i := range out {
		subj.cause(out[i].Reason, out[i].Detail)
	}

	return out
}

// currentBasis gives the values the run produces now, for the comparison with an approval. A value that the caller
// does not supply is taken from the approval, so it is not compared.
func (e *evaluator) currentBasis(bound impact.Basis) impact.Basis {
	current := impact.Basis{Contract: e.contract.Version, Policy: e.in.PolicyDigest, Authority: e.in.AuthorityDigest}
	if current.Policy == "" {
		current.Policy = bound.Policy
	}

	if current.Authority == "" {
		current.Authority = bound.Authority
	}

	if e.in.Facts != nil {
		current.Evaluators = e.in.Facts.EvaluatorVersions()
	} else {
		current.Evaluators = bound.Evaluators
	}

	return current
}

// boundBasis gives the values an approval was produced under.
func boundBasis(approval *Approval) impact.Basis {
	basis := impact.Basis{
		Contract: approval.ContractVersion, Policy: approval.ProducedUnder.PolicyDigest, Authority: approval.ProducedUnder.AuthorityDigest,
		Evaluators: map[string]string{},
	}

	maps.Copy(basis.Evaluators, approval.ProducedUnder.EvaluatorVersions)

	if approval.ProducedUnder.NormalizationVersion != "" {
		basis.Evaluators[impact.EvaluatorNormalization] = approval.ProducedUnder.NormalizationVersion
	}

	return basis
}

// compareInvalidators compares the digest contract, policy, trust authority, evaluator versions, and
// normalization that the approval was produced under with the current ones (change-impact, "Possible
// invalidators"). Any difference that no shipped equivalence covers makes the approval review-required.
func (e *evaluator) compareInvalidators(approval *Approval, cur *Currency) {
	bound := boundBasis(approval)
	found, invalid := impact.Invalidators(e.registry, bound, e.currentBasis(bound), nil)

	for _, inv := range found {
		detail := fmt.Sprintf("approval %s: %s (%s -> %s)", approval.ID, inv.Detail, cmp.Or(inv.Bound, "none"), cmp.Or(inv.Current, "none"))

		switch inv.Kind {
		case impact.InvalidatorDigestContract:
			detail = fmt.Sprintf("approval %s was bound under %s and the current contract is %s", approval.ID, inv.Bound, inv.Current)
			if len(invalid) > 0 {
				detail += ": " + strings.Join(invalid, "; ")
			}

			cur.addReason(DetailContractChanged, detail)
		case impact.InvalidatorPolicy:
			cur.addReason(DetailPolicyChanged, detail)
		case impact.InvalidatorTrustAuthority:
			cur.addReason(DetailAuthorityChanged, detail)
		case impact.InvalidatorNormalization:
			cur.addReason(DetailNormalizationChanged, detail)
		case impact.InvalidatorEvaluator:
			cur.addReason(DetailEvaluatorChanged, detail)
		}
	}
}
