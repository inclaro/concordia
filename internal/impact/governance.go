package impact

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/observation"
)

// Rule IDs of the review-currency policy pack. The pack file (wave 3) and the documentation name
// the same constants, and a test keeps them in step.
const (
	// RuleApproval: a required assessment has a current verified approval.
	RuleApproval = "REV-APR-001"
	// RuleDisposition: removed or narrowed subjects have a recorded disposition.
	RuleDisposition = "REV-DSP-001"
	// RuleCapability: a capability loss has a recorded disposition and shows a coverage regression.
	RuleCapability = "REV-CAP-001"
	// RuleGovernance: a relaxation has a governance disposition.
	RuleGovernance = "REV-GOV-001"
)

// RuleIDs gives the four rule IDs of the review-currency pack, in order.
func RuleIDs() []string {
	return []string{RuleApproval, RuleDisposition, RuleCapability, RuleGovernance}
}

// ReasonTrustAnchorUnreadable is the reason of an `unknown` governance verdict when the trust
// anchor cannot be read. The string mirrors the `UnknownReason` of internal/conformance.
const ReasonTrustAnchorUnreadable = "trust-anchor-unreadable"

// Comparison is the result of Strictness: how a candidate setting relates to the setting at the
// trust anchor.
type Comparison string

// The four results of the partial order.
const (
	// Stricter: the candidate is at least as strict on every axis and stricter on one.
	Stricter Comparison = "stricter"
	// Equal: the settings are the same.
	Equal Comparison = "equal"
	// Looser: the candidate is at most as strict on every axis and looser on one.
	Looser Comparison = "looser"
	// Incomparable: Concordia cannot show the candidate to be as strict. It counts as looser.
	Incomparable Comparison = "incomparable"
)

// Relaxation says whether the comparison needs a governance disposition: looser and incomparable
// do, equal and stricter do not.
func (c Comparison) Relaxation() bool { return c == Looser || c == Incomparable }

// SettingKind says which governed setting a Setting is.
type SettingKind string

// The governed settings.
const (
	// SettingRule is the evidence and severity configuration of one review rule.
	SettingRule SettingKind = "rule"
	// SettingEnforcement is one entry of `check.enforcement` for the review rules.
	SettingEnforcement SettingKind = "enforcement"
	// SettingAuthority is one entry of `assessments.authorities`.
	SettingAuthority SettingKind = "authority"
)

// Setting is one governed configuration that a candidate could weaken: a RuleSetting, an
// EnforcementSetting, or an AuthoritySetting. This package takes them as plain data; the caller
// reads them from the trust anchor and from the candidate.
type Setting interface {
	// SettingKind says which setting this is.
	SettingKind() SettingKind
	// Subject names the setting: the rule ID, the enforcement key, or the authority ID.
	Subject() string
	compare(candidate Setting) Comparison
}

// RuleSetting is the part of a compiled review rule that a candidate can weaken. A caller fills it
// from `policy.RulePlan` (severity, evidence requirements, subjects and targets) and puts a digest
// of every other field into Other.
type RuleSetting struct {
	RuleID string `json:"ruleId"`
	// Severity is `error`, `warning`, or `info`.
	Severity string `json:"severity"`
	// OnUnknown is `fail`, `warn`, or `ignore`.
	OnUnknown string `json:"onUnknown"`
	// MinimumConfidence is `resolved`, `runtime`, `syntactic`, `heuristic`, `unknown`, or empty.
	MinimumConfidence string `json:"minimumConfidence"`
	// Other is the digest of every field not listed here (predicate, aggregation, layers, and so
	// on). A different value gives Incomparable: Concordia cannot order a changed definition.
	Other string `json:"other"`
	// Scope is the applicable scope as selectors. A wider set is stricter: a rule applies to more.
	Scope []string `json:"scope"`
	// MinimumCoverage is the required coverage ratio, 0..1.
	MinimumCoverage float64 `json:"minimumCoverage"`
}

// EnforcementSetting is one entry of `check.enforcement`. The caller passes only the entries that
// select review rules (by family, by pack, or by rule ID).
type EnforcementSetting struct {
	Family      string `json:"family,omitempty"`
	Pack        string `json:"pack,omitempty"`
	Environment string `json:"environment,omitempty"`
	// Mode is `mandatory`, `advisory`, or `shadow`.
	Mode  string   `json:"mode"`
	Rules []string `json:"rules,omitempty"`
}

// AuthoritySetting is one entry of `assessments.authorities`.
type AuthoritySetting struct {
	ID        string   `json:"id"`
	Role      string   `json:"role"`
	Mechanism string   `json:"mechanism"`
	Reviewers []string `json:"reviewers"`
	Paths     []string `json:"paths"`
	// Signers are the signer identities, each as `identity|issuer|publicKey`.
	Signers []string `json:"signers"`
}

// RoleGovernance is the role of the authorities that can accept a relaxation.
const RoleGovernance = "governance"

// Strictness compares a candidate setting with the one at the trust anchor. Both must be the same
// kind and have the same subject, otherwise the result is Incomparable.
//
// The order is conservative and per axis:
//   - severity: error > warning > info;
//   - onUnknown: fail > warn > ignore;
//   - minimum coverage: a higher number is stricter;
//   - minimum confidence: the rank of observation.Confidence, higher is stricter;
//   - scope: a superset is stricter, a subset is looser, any other difference is incomparable;
//   - enforcement mode: mandatory > advisory > shadow;
//   - everything else (the rest of a rule, any field of an authority entry) is equal or
//     incomparable.
//
// A setting is stricter or looser only if no axis points the other way. A value that Strictness
// does not know is incomparable.
func Strictness(anchor, candidate Setting) Comparison {
	if !sameSettingSubject(anchor, candidate) {
		return Incomparable
	}

	return anchor.compare(candidate)
}

// sameSettingSubject says whether both settings exist and are of the same kind and subject.
func sameSettingSubject(anchor, candidate Setting) bool {
	if anchor == nil || candidate == nil {
		return false
	}

	return anchor.SettingKind() == candidate.SettingKind() && anchor.Subject() == candidate.Subject()
}

// SettingKind implements Setting.
func (RuleSetting) SettingKind() SettingKind { return SettingRule }

// Subject implements Setting.
func (r RuleSetting) Subject() string { return r.RuleID } //nolint:gocritic // plain-data setting; the Setting interface needs value receivers.

func (r RuleSetting) compare(candidate Setting) Comparison { //nolint:gocritic // plain-data setting; the Setting interface needs value receivers.
	cand, ok := candidate.(RuleSetting)
	if !ok {
		return Incomparable
	}

	axes := []Comparison{
		rankComparison(rankIn(severityRanks, r.Severity), rankIn(severityRanks, cand.Severity)),
		rankComparison(rankIn(onUnknownRanks, r.OnUnknown), rankIn(onUnknownRanks, cand.OnUnknown)),
		numberComparison(r.MinimumCoverage, cand.MinimumCoverage),
		rankComparison(confidenceRank(r.MinimumConfidence), confidenceRank(cand.MinimumConfidence)),
		scopeComparison(r.Scope, cand.Scope),
		equalOrIncomparable(r.Other == cand.Other),
	}

	return combine(axes...)
}

// SettingKind implements Setting.
func (EnforcementSetting) SettingKind() SettingKind { return SettingEnforcement }

// Subject implements Setting. It is the selector of the entry, not its mode.
func (e EnforcementSetting) Subject() string { //nolint:gocritic // plain-data setting; the Setting interface needs value receivers.
	rules := slices.Clone(e.Rules)
	slices.SortFunc(rules, canonjson.CompareUTF16)

	return fmt.Sprintf("family=%s pack=%s environment=%s rules=%s", e.Family, e.Pack, e.Environment, strings.Join(rules, ","))
}

func (e EnforcementSetting) compare(candidate Setting) Comparison { //nolint:gocritic // plain-data setting; the Setting interface needs value receivers.
	c, ok := candidate.(EnforcementSetting)
	if !ok {
		return Incomparable
	}

	return rankComparison(rankIn(modeRanks, e.Mode), rankIn(modeRanks, c.Mode))
}

// SettingKind implements Setting.
func (AuthoritySetting) SettingKind() SettingKind { return SettingAuthority }

// Subject implements Setting.
func (a AuthoritySetting) Subject() string { return a.ID } //nolint:gocritic // plain-data setting; the Setting interface needs value receivers.

// compare gives Equal or Incomparable. An edit to an authority or reviewer is a relaxation
// (spec, "Relaxation kinds"): removing a reviewer can lock the approvers out, and Concordia
// cannot show that a changed list still lets the same people approve.
func (a AuthoritySetting) compare(candidate Setting) Comparison { //nolint:gocritic // plain-data setting; the Setting interface needs value receivers.
	c, ok := candidate.(AuthoritySetting)
	if !ok {
		return Incomparable
	}

	same := a.ID == c.ID && a.Role == c.Role && a.Mechanism == c.Mechanism &&
		sameSet(a.Reviewers, c.Reviewers) && sameSet(a.Paths, c.Paths) && sameSet(a.Signers, c.Signers)

	return equalOrIncomparable(same)
}

func equalOrIncomparable(same bool) Comparison {
	if same {
		return Equal
	}

	return Incomparable
}

// Ranks of the ordered values. A value that is not in the order has the rank -1.
const (
	rankStrictest = 3
	rankMiddle    = 2
	rankLoosest   = 1
	rankUnknown   = -1
)

const (
	modeMandatory = "mandatory"
	modeAdvisory  = "advisory"
	modeShadow    = "shadow"
)

var (
	severityRanks  = map[string]int{"error": rankStrictest, "warning": rankMiddle, "info": rankLoosest}
	onUnknownRanks = map[string]int{"fail": rankStrictest, "warn": rankMiddle, "ignore": rankLoosest}
	modeRanks      = map[string]int{modeMandatory: rankStrictest, modeAdvisory: rankMiddle, modeShadow: rankLoosest}
)

// rankIn is the rank of the value in the order, or rankUnknown when it is not in the order.
func rankIn(order map[string]int, value string) int {
	if rank, ok := order[value]; ok {
		return rank
	}

	return rankUnknown
}

// confidenceRank is the rank of observation.Confidence; the empty value is the rank of unknown.
// A value that is not a confidence is -1 and compares as incomparable.
func confidenceRank(s string) int {
	switch c := observation.Confidence(s); c {
	case observation.ConfidenceResolved, observation.ConfidenceSyntactic, observation.ConfidenceHeuristic,
		observation.ConfidenceRuntime, observation.ConfidenceUnknown:
		return c.Rank()
	default:
		if s == "" {
			return 0
		}

		return -1
	}
}

func rankComparison(anchor, candidate int) Comparison {
	switch {
	case anchor < 0 || candidate < 0:
		return Incomparable
	case candidate > anchor:
		return Stricter
	case candidate < anchor:
		return Looser
	default:
		return Equal
	}
}

func numberComparison(anchor, candidate float64) Comparison {
	switch cmp.Compare(candidate, anchor) {
	case 1:
		return Stricter
	case -1:
		return Looser
	default:
		return Equal
	}
}

func scopeComparison(anchor, candidate []string) Comparison {
	missingFromCandidate := hasMissingElement(anchor, candidate)
	extraInCandidate := hasMissingElement(candidate, anchor)

	switch {
	case !missingFromCandidate && !extraInCandidate:
		return Equal
	case !missingFromCandidate:
		return Stricter
	case !extraInCandidate:
		return Looser
	default:
		return Incomparable
	}
}

// hasMissingElement says whether some element of list is not in other.
func hasMissingElement(list, other []string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return !slices.Contains(other, s) })
}

func sameSet(a, b []string) bool {
	return scopeComparison(a, b) == Equal
}

// combine joins the axes: any incomparable axis, or axes that point both ways, give Incomparable.
func combine(axes ...Comparison) Comparison {
	stricter := slices.Contains(axes, Stricter)
	looser := slices.Contains(axes, Looser)

	switch {
	case slices.Contains(axes, Incomparable), stricter && looser:
		return Incomparable
	case stricter:
		return Stricter
	case looser:
		return Looser
	default:
		return Equal
	}
}

// GovernanceState is the governed configuration, read from the trust anchor or from the candidate.
type GovernanceState struct {
	Rules       []RuleSetting        `json:"rules"`
	Enforcement []EnforcementSetting `json:"enforcement"`
	Authorities []AuthoritySetting   `json:"authorities"`
}

// SettingChange is one difference between the anchor and the candidate.
type SettingChange struct {
	Kind    SettingKind `json:"kind"`
	Subject string      `json:"subject"`
	// Op is `added`, `removed`, or `modified`.
	Op         ChangeKind `json:"change"`
	Comparison Comparison `json:"comparison"`
	// Digest is the digest of the changed configuration: of the candidate setting, or of the
	// removal for a removed setting. A governance disposition binds it.
	Digest string `json:"digest"`
	// Detail says what changed, without values that could be secret.
	Detail string `json:"detail"`
}

// Relaxation says whether the change needs a governance disposition.
func (c SettingChange) Relaxation() bool { return c.Comparison.Relaxation() } //nolint:gocritic // plain-data value; the changes are slices of values.

// orderSubject is the subject of the change that reports a reordering of enforcement entries.
const orderSubject = "order"

// Changes compares the candidate with the anchor and gives every difference, sorted by kind and
// subject. A stricter difference is listed too, because it is a possible invalidator, but it is
// not a relaxation. The classification of each difference:
//   - a removed rule is looser; an added rule is stricter; a modified rule follows Strictness;
//   - a removed enforcement entry is looser when it was mandatory and incomparable otherwise (what
//     the entry set is not known); an added entry is stricter when mandatory and looser otherwise
//     (it can shadow a later entry); the order of the entries that both sides have must not change;
//   - an added, removed, or modified authority is incomparable.
func Changes(anchor, candidate GovernanceState) []SettingChange {
	var out []SettingChange

	out = append(out, diffSettings(settingsOf(anchor.Rules), settingsOf(candidate.Rules), SettingRule, rulePresence)...)
	out = append(out, diffSettings(settingsOf(anchor.Enforcement), settingsOf(candidate.Enforcement), SettingEnforcement, enforcementPresence)...)
	out = append(out, diffSettings(settingsOf(anchor.Authorities), settingsOf(candidate.Authorities), SettingAuthority, authorityPresence)...)

	if enforcementReordered(anchor.Enforcement, candidate.Enforcement) {
		out = append(out, newChange(&SettingChange{
			Kind: SettingEnforcement, Subject: orderSubject, Op: ChangeModified, Comparison: Incomparable,
			Detail: "the order of the check.enforcement entries changed; the first match wins",
		}, candidate.Enforcement))
	}

	slices.SortFunc(out, func(a, b SettingChange) int {
		return cmp.Or(
			canonjson.CompareUTF16(string(a.Kind), string(b.Kind)),
			canonjson.CompareUTF16(a.Subject, b.Subject),
		)
	})

	return out
}

// Relaxations gives the changes that need a governance disposition.
func Relaxations(changes []SettingChange) []SettingChange {
	out := []SettingChange{}

	for _, c := range changes {
		if c.Relaxation() {
			out = append(out, c)
		}
	}

	return out
}

func settingsOf[T Setting](list []T) []Setting {
	out := make([]Setting, len(list))
	for i, s := range list {
		out[i] = s
	}

	return out
}

// presenceOutcome is what the comparison of a setting that only one side has is, for a mandatory
// enforcement entry and for any other setting.
type presenceOutcome struct {
	mandatory, other Comparison
}

func (o presenceOutcome) of(s Setting) Comparison {
	if e, ok := s.(EnforcementSetting); ok && e.Mode == modeMandatory {
		return o.mandatory
	}

	return o.other
}

// presencePolicy classifies a setting that is removed from, or added to, the anchor.
type presencePolicy struct {
	removed, added presenceOutcome
}

var (
	// A removed rule is looser; an added rule is stricter.
	rulePresence = presencePolicy{
		removed: presenceOutcome{mandatory: Looser, other: Looser},
		added:   presenceOutcome{mandatory: Stricter, other: Stricter},
	}
	// A removed enforcement entry is looser when mandatory, an added one stricter when mandatory.
	enforcementPresence = presencePolicy{
		removed: presenceOutcome{mandatory: Looser, other: Incomparable},
		added:   presenceOutcome{mandatory: Stricter, other: Looser},
	}
	// An added or removed authority is incomparable.
	authorityPresence = presencePolicy{
		removed: presenceOutcome{mandatory: Incomparable, other: Incomparable},
		added:   presenceOutcome{mandatory: Incomparable, other: Incomparable},
	}
)

// settingDiff holds what the comparison of one kind of setting shares: the kind, how a presence
// change counts, the first candidate entry of each subject, and the subjects that were seen.
type settingDiff struct {
	policy      presencePolicy
	inCandidate map[string]Setting
	seen        map[string]bool
	kind        SettingKind
}

func diffSettings(anchor, candidate []Setting, kind SettingKind, policy presencePolicy) []SettingChange {
	diff := settingDiff{policy: policy, inCandidate: firstBySubject(candidate), seen: map[string]bool{}, kind: kind}
	out := diff.anchorChanges(anchor)

	return append(out, diff.addedChanges(candidate)...)
}

// firstBySubject indexes the settings by subject; the first setting of a subject wins.
func firstBySubject(list []Setting) map[string]Setting {
	bySubject := map[string]Setting{}

	for _, s := range list {
		if _, dup := bySubject[s.Subject()]; !dup {
			bySubject[s.Subject()] = s
		}
	}

	return bySubject
}

// anchorChanges lists the removed and modified entries of the anchor, each subject once. It marks
// the subjects as seen.
func (d *settingDiff) anchorChanges(anchor []Setting) []SettingChange {
	var out []SettingChange

	for _, entry := range anchor {
		if !firstSight(d.seen, entry.Subject()) {
			continue
		}

		if change, differs := anchorEntryChange(d.kind, d.policy, entry, d.inCandidate); differs {
			out = append(out, change)
		}
	}

	return out
}

// addedChanges lists the candidate entries that no anchor entry has.
func (d *settingDiff) addedChanges(candidate []Setting) []SettingChange {
	var out []SettingChange

	for _, added := range candidate {
		if !firstSight(d.seen, added.Subject()) {
			continue
		}

		out = append(out, newChange(&SettingChange{
			Kind: d.kind, Subject: added.Subject(), Op: ChangeAdded, Comparison: d.policy.added.of(added),
			Detail: fmt.Sprintf("the %s %s was added", d.kind, added.Subject()),
		}, added))
	}

	return out
}

// firstSight records the subject and says whether it was new.
func firstSight(seen map[string]bool, subject string) bool {
	if seen[subject] {
		return false
	}

	seen[subject] = true

	return true
}

// anchorEntryChange is the change of one anchor entry: removed, or modified. The result is false
// when the candidate keeps the entry as it is.
func anchorEntryChange(kind SettingKind, policy presencePolicy, entry Setting, inCandidate map[string]Setting) (SettingChange, bool) {
	subject := entry.Subject()

	kept, still := inCandidate[subject]
	if !still {
		return newChange(&SettingChange{
			Kind: kind, Subject: subject, Op: ChangeRemoved, Comparison: policy.removed.of(entry),
			Detail: fmt.Sprintf("the %s %s was removed", kind, subject),
		}, nil), true
	}

	result := Strictness(entry, kept)
	if result == Equal {
		return SettingChange{}, false
	}

	return newChange(&SettingChange{
		Kind: kind, Subject: subject, Op: ChangeModified, Comparison: result,
		Detail: fmt.Sprintf("the %s %s is %s than at the trust anchor", kind, subject, result),
	}, kept), true
}

// enforcementReordered says whether the entries that both sides have appear in another order.
func enforcementReordered(anchor, candidate []EnforcementSetting) bool {
	left := enforcementSubjectsIn(anchor, enforcementSubjectSet(candidate))
	right := enforcementSubjectsIn(candidate, enforcementSubjectSet(anchor))

	return !slices.Equal(left, right)
}

func enforcementSubjectSet(list []EnforcementSetting) map[string]bool {
	set := map[string]bool{}
	for _, e := range list {
		set[e.Subject()] = true
	}

	return set
}

// enforcementSubjectsIn gives the subjects of the entries whose subject is in the set, in order.
func enforcementSubjectsIn(list []EnforcementSetting, set map[string]bool) []string {
	var subjects []string

	for _, e := range list {
		if set[e.Subject()] {
			subjects = append(subjects, e.Subject())
		}
	}

	return subjects
}

// newChange completes a change with the digest over its candidate configuration.
func newChange(change *SettingChange, candidate any) SettingChange {
	change.Digest = settingDigest(change.Kind, change.Subject, change.Op, candidate)

	return *change
}

// settingDigest binds the kind, the subject, the kind of change, and the candidate configuration.
func settingDigest(kind SettingKind, subject string, op ChangeKind, candidate any) string {
	// A setting is plain data: MustString panics only on a programming error, and a panic is better
	// than a digest that two different changes could share.
	return digest.OfBytes([]byte(canonjson.MustString(settingBasis{Kind: kind, Subject: subject, Change: op, Candidate: candidate})))
}

// settingBasis is what the digest of a change covers.
type settingBasis struct {
	Candidate any         `json:"candidate"`
	Kind      SettingKind `json:"kind"`
	Subject   string      `json:"subject"`
	Change    ChangeKind  `json:"change"`
}

// Anchor is the trust anchor as the caller read it: the governed configuration at the anchor
// revision, or the reason that it could not be read.
type Anchor struct {
	// Revision is the anchor revision, for reports.
	Revision string `json:"revision,omitempty"`
	// Detail says why the anchor is unreadable.
	Detail string `json:"detail,omitempty"`
	// State is the governed configuration at the anchor. Authorities with the role `governance`
	// here are the only authorities whose dispositions count.
	State GovernanceState `json:"state"`
	// Unreadable is true when the anchor cannot be read (missing ref, not an ancestor of HEAD, a
	// read error). The verdict is then `unknown`.
	Unreadable bool `json:"unreadable,omitempty"`
}

// GovernanceRecord is a governance disposition that the caller has read. It binds the digest of one
// changed configuration. Verified is set by the caller after it verified the record against the
// authority of the anchor (protected review or signature); an unverified record never counts.
type GovernanceRecord struct {
	// ID is the record id, for reports.
	ID string `json:"id,omitempty"`
	// Authority is the id of the authority that the record names.
	Authority string `json:"authority"`
	// Digest is the digest of the changed configuration that the record binds (SettingChange.Digest).
	Digest string `json:"digest"`
	// Verified says that the caller verified the record.
	Verified bool `json:"verified"`
}

// GovernanceVerdict is the verdict of a governance obligation.
type GovernanceVerdict string

// Governance verdicts. They mirror the verdicts of the typed obligations.
const (
	GovernancePass      GovernanceVerdict = "pass"
	GovernanceViolation GovernanceVerdict = "violation"
	GovernanceUnknown   GovernanceVerdict = "unknown"
)

// GovernanceObligation is the obligation `REV-GOV-001` for one relaxation.
type GovernanceObligation struct {
	// Rule is RuleGovernance.
	Rule string `json:"rule"`
	// Change is the relaxation.
	Change SettingChange `json:"change"`
	// Verdict is `violation` until a verified governance disposition binds the change.
	Verdict GovernanceVerdict `json:"verdict"`
	// Disposition is the record that accepted the relaxation. nil when none did.
	Disposition *GovernanceRecord `json:"disposition,omitempty"`
	// Reason is ReasonTrustAnchorUnreadable for an unknown verdict.
	Reason string `json:"reason,omitempty"`
	// Detail says what is missing.
	Detail string `json:"detail"`
}

// GovernanceResult is the outcome of CheckGovernance.
type GovernanceResult struct {
	// Verdict is `unknown` when the anchor is unreadable, `violation` when a relaxation lacks a
	// disposition, and `pass` otherwise.
	Verdict GovernanceVerdict `json:"verdict"`
	// Reason is ReasonTrustAnchorUnreadable when the verdict is unknown.
	Reason string `json:"reason,omitempty"`
	// Changes are every difference from the anchor, relaxations or not. A change that is not a
	// relaxation is still a possible invalidator. Never nil.
	Changes []SettingChange `json:"changes"`
	// Obligations are one `REV-GOV-001` obligation per relaxation. Never nil.
	Obligations []GovernanceObligation `json:"obligations"`
}

// CheckGovernance decides the governance obligations of a candidate. It compares the candidate with
// the anchor; each relaxation becomes an obligation with the verdict `violation` until a verified
// record binds its digest and the record's authority is an authority with the role `governance`
// in the anchor. The candidate's own authorities never accept a relaxation: only the anchor's
// are consulted. An unreadable anchor gives `unknown` with ReasonTrustAnchorUnreadable.
func CheckGovernance(anchor Anchor, candidate GovernanceState, records []GovernanceRecord) GovernanceResult { //nolint:gocritic // the anchor is plain data that the caller builds once.
	if anchor.Unreadable {
		return unreadableAnchorResult(anchor.Detail)
	}

	governance := governanceAuthorityIDs(anchor.State)
	result := GovernanceResult{Verdict: GovernancePass, Changes: Changes(anchor.State, candidate), Obligations: []GovernanceObligation{}}

	for _, change := range Relaxations(result.Changes) {
		obligation := governanceObligationFor(change, records, governance)
		if obligation.Verdict == GovernanceViolation {
			result.Verdict = GovernanceViolation
		}

		result.Obligations = append(result.Obligations, obligation)
	}

	return result
}

// unreadableAnchorResult is the `unknown` outcome of an anchor that cannot be read.
func unreadableAnchorResult(detail string) GovernanceResult {
	return GovernanceResult{
		Verdict: GovernanceUnknown, Reason: ReasonTrustAnchorUnreadable, Changes: []SettingChange{},
		Obligations: []GovernanceObligation{{
			Rule: RuleGovernance, Verdict: GovernanceUnknown, Reason: ReasonTrustAnchorUnreadable,
			Change: SettingChange{Kind: SettingAuthority, Subject: "trust-anchor", Op: ChangeModified, Comparison: Incomparable},
			Detail: cmp.Or(detail, "the trust anchor cannot be read"),
		}},
	}
}

// governanceAuthorityIDs is the set of ids of the anchor's authorities with the role governance.
func governanceAuthorityIDs(state GovernanceState) map[string]bool {
	ids := map[string]bool{}

	for _, a := range state.Authorities {
		if a.Role == RoleGovernance {
			ids[a.ID] = true
		}
	}

	return ids
}

// governanceObligationFor is the obligation of one relaxation: a violation until a record that
// binds the change digest is verified and names a governance authority.
func governanceObligationFor(change SettingChange, records []GovernanceRecord, governance map[string]bool) GovernanceObligation { //nolint:gocritic // the change is plain data that the caller built once.
	obligation := GovernanceObligation{Rule: RuleGovernance, Change: change, Verdict: GovernanceViolation}
	obligation.Detail = "needs a governance disposition from an authority with the role governance at the trust anchor"

	for i := range records {
		if records[i].Digest != change.Digest {
			continue
		}

		applyGovernanceRecord(&obligation, &records[i], governance)

		if obligation.Verdict == GovernancePass {
			break
		}
	}

	return obligation
}

// applyGovernanceRecord updates the obligation with what a record that binds its digest decides.
func applyGovernanceRecord(obligation *GovernanceObligation, record *GovernanceRecord, governance map[string]bool) {
	switch {
	case !record.Verified:
		obligation.Detail = fmt.Sprintf("the record %s is not verified", cmp.Or(record.ID, record.Authority))
	case !governance[record.Authority]:
		obligation.Detail = fmt.Sprintf("the authority %s is not a governance authority at the trust anchor", record.Authority)
	default:
		obligation.Verdict = GovernancePass
		obligation.Disposition = record
		obligation.Detail = "accepted by a governance disposition"
	}
}
