// Package adr evaluates architecture decision governance: the decision register (GOVERNS and ENFORCED_BY
// declarations), committed presence of ADR files, the lifecycle profile, the supersession history, replacement
// review, rule justification, enforcement coverage, and decision-scope triggers. Decision facts come from the
// provider evidence (CBM `ADR` nodes); governance declarations come only from the register. ADR text is data:
// nothing in it is executed, fetched, or followed. The package never writes a file.
package adr

import (
	"regexp"
	"strconv"
	"strings"
)

// Expect is the expected lifecycle of a registered decision.
type Expect string

// Expected lifecycles.
const (
	ExpectActive     Expect = "active"
	ExpectHistorical Expect = "historical"
)

// EnforcementMode is the `enforcement` setting of a register entry.
type EnforcementMode string

// Enforcement modes. The default is EnforcementNone.
const (
	EnforcementRequired EnforcementMode = "required"
	EnforcementNone     EnforcementMode = "none"
)

// Register is the merged content of the decision register files.
type Register struct {
	// Files are the register files, sorted by path ("/" path relative to the workspace root).
	Files []RegisterFile
	// Entries are the decisions of every file, sorted by canonical ID, then project.
	Entries []Entry
}

// RegisterFile is one register file with its digest.
type RegisterFile struct {
	Path   string
	Digest string
}

// Entry is one registered decision.
type Entry struct {
	// ID is the decision ID in canonical form (see CanonicalID): `ADR-0007` in the register is `ADR-7` here. It is
	// the one name of the decision in every result, and the fingerprint input.
	ID string
	// Path is the optional repository-relative path pin; "" when not pinned.
	Path string
	// Project is the optional project scope as written in the register; "" when not scoped. It is part of the
	// identity of the entry (EntryKey, fingerprints); evidence matching uses EvidenceScope.
	Project     string
	Expect      Expect
	Enforcement EnforcementMode
	// File and Line locate the entry in its register file.
	File       string
	Categories []string
	Governs    []GovernsRef
	EnforcedBy []RuleRef
	Released   []Release
	Line       int
	// PrimaryScope is set when Project names the primary project, whose decisions and Git facts have the
	// project key "" in the evidence. The entry is still scoped to that project.
	PrimaryScope bool
}

// EvidenceScope gives the project key of the scope of the entry in the evidence and the Git facts ("" for the
// primary project) and whether the entry is scoped to a project at all. An unscoped entry matches every project.
func (e *Entry) EvidenceScope() (project string, scoped bool) {
	switch {
	case e.Project == "":
		return "", false
	case e.PrimaryScope:
		return "", true
	default:
		return e.Project, true
	}
}

// GovernsRef is one `governs` item: exactly one of Ref and Select is set.
type GovernsRef struct {
	Select *Selector
	// Ref is `calm:node:<id>`, `calm:relationship:<id>`, or `calm:interface:<node-id>/<interface-id>`.
	Ref  string
	Line int
}

// Selector is a bounded `select` over the CALM nodes, with the union semantics of the policy selectors.
type Selector struct {
	CalmNodes []string
	NodeTypes []string
	Tags      []string
}

// RuleRef is a link to a rule of a pack version.
type RuleRef struct {
	Rule        string
	PackID      string
	PackVersion string
	Line        int
}

// String gives the rule ref form `<rule-id>@<pack-id>/<pack-version>`.
func (r RuleRef) String() string { return r.Rule + "@" + r.PackID + "/" + r.PackVersion }

// ruleRefPattern matches `<rule-id>@<pack-id>/<pack-version>`.
var ruleRefPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]*(?:-[A-Z0-9]+)+)@([^@/\s]+)/([^@/\s]+)$`)

// ParseRuleRef reads `<rule-id>@<pack-id>/<pack-version>`. The bool is false when the text has another form.
func ParseRuleRef(text string) (RuleRef, bool) {
	m := ruleRefPattern.FindStringSubmatch(text)
	if m == nil {
		return RuleRef{}, false
	}

	return RuleRef{Rule: m[1], PackID: m[2], PackVersion: m[3]}, true
}

// Release is one `released` item of a historical decision: exactly one of Ref and Rule is set.
type Release struct {
	// Ref is a `calm:` subject ref.
	Ref    string
	Rule   *RuleRef
	Reason string
	Line   int
}

// Key gives the released ref: the subject ref, or the rule ref form.
func (r *Release) Key() string {
	if r.Rule != nil {
		return r.Rule.String()
	}

	return r.Ref
}

// canonicalIDPattern splits a decision ID into a prefix and a number.
var canonicalIDPattern = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*)-0*(\d+)$`)

// CanonicalID gives the canonical form of a decision ID: the prefix upper-cased and the number without leading
// zeros, so `ADR-0003` and `adr-3` both give `ADR-3`. An ID of another form is returned upper-cased and trimmed.
func CanonicalID(id string) string {
	id = strings.TrimSpace(id)

	m := canonicalIDPattern.FindStringSubmatch(id)
	if m == nil {
		return strings.ToUpper(id)
	}

	n, err := strconv.ParseUint(m[2], 10, 64)
	if err != nil {
		return strings.ToUpper(m[1]) + "-" + m[2]
	}

	return strings.ToUpper(m[1]) + "-" + strconv.FormatUint(n, 10)
}

// LifecycleClass is the class that the lifecycle profile gives a status.
type LifecycleClass string

// Lifecycle classes. Pending is `proposed` or `draft`: a successor in that class does not supersede.
const (
	ClassActive   LifecycleClass = "active"
	ClassPending  LifecycleClass = "pending"
	ClassInactive LifecycleClass = "inactive"
)

// CBM-normalized statuses (CBM 0.12.1, O12).
const (
	StatusAccepted    = "accepted"
	StatusImplemented = "implemented"
	StatusProposed    = "proposed"
	StatusDraft       = "draft"
	StatusRejected    = "rejected"
	StatusDeprecated  = "deprecated"
	StatusSuperseded  = "superseded"
	StatusAbandoned   = "abandoned"
	StatusUnknown     = "unknown"
)

// EvidenceDecision is one decision entity of the evidence: a CBM `ADR` node. Path is the identity; Label (the
// canonical `adr_id`) is a label only. Properties are verbatim.
type EvidenceDecision struct {
	// Path is the repository-relative path, with the project prefix in a multi-project run.
	Path string
	// Project is the workspace project name; "" for the primary project.
	Project string
	// ObservedID is the observed ID of the node.
	ObservedID string
	AdrID      string
	// Label is CanonicalID(AdrID).
	Label        string
	Title        string
	Status       string
	StatusText   string
	Date         string
	Deciders     string
	SupersededBy string
	Line         int
}

// SupersedesEdge is one `supersedes` edge of the evidence: From (the new decision) supersedes To (the old one),
// both by path.
type SupersedesEdge struct {
	From string
	To   string
	Line int
}

// Mention is a `mentions` or `references-file` edge from a decision file: candidate evidence only.
type Mention struct {
	// From is the path of the decision file that mentions; Subject the mentioned file or qualified name.
	From    string
	Subject string
	Kind    string
}

// DecisionEvidence is the decision evidence of a run, built from the normalized evidence.
type DecisionEvidence struct {
	// Decisions are sorted by path.
	Decisions []EvidenceDecision
	// Supersedes are sorted by from, then to.
	Supersedes []SupersedesEdge
	// Mentions are sorted by from, subject, kind.
	Mentions []Mention
	// Stale are the indexed files that changed after indexing (with project prefix), sorted.
	Stale []string
	// Supported is false when the capability profile has no decision evidence or no `supersedes` family:
	// then every presence, applicability, and history result is unknown.
	Supported bool
	// UnresolvedSupersedesObservable says whether a "supersedes X" statement with no committed X is visible.
	// False for CBM 0.12.1 (O12).
	UnresolvedSupersedesObservable bool
	// ExtractionComplete is false when the ADR extraction coverage is not complete (absence cannot be shown).
	ExtractionComplete bool
}

// PresenceState is the committed-presence state of a registered decision (design D3).
type PresenceState string

// Presence states.
const (
	PresenceCommitted       PresenceState = "committed"
	PresenceIndexOnly       PresenceState = "index-only"
	PresenceUncommittedEdit PresenceState = "uncommitted-edit"
	PresenceAbsent          PresenceState = "absent"
	PresenceNotRecognized   PresenceState = "not-recognized"
	PresencePathMismatch    PresenceState = "path-mismatch"
	PresenceUnverifiable    PresenceState = "unverifiable"
)

// ApplicabilityState is the applicability state of a registered decision. Later changes
// (add-assurance-adapters) read it to decide whether a decision counts as active.
type ApplicabilityState string

// Applicability states.
const (
	ApplicabilityActive     ApplicabilityState = "active"
	ApplicabilityInactive   ApplicabilityState = "inactive"
	ApplicabilitySuperseded ApplicabilityState = "superseded"
	ApplicabilityAmbiguous  ApplicabilityState = "ambiguous"
	ApplicabilityUnknown    ApplicabilityState = "unknown"
)

// EnforcementState is the enforcement coverage of a registered decision.
type EnforcementState string

// Enforcement states.
const (
	EnforcementEnforced      EnforcementState = "enforced"
	EnforcementAdvisoryOnly  EnforcementState = "advisory-only"
	EnforcementUnlinked      EnforcementState = "unlinked"
	EnforcementNotApplicable EnforcementState = "not-applicable"
)

// Provenance is where a history link came from.
type Provenance string

// Provenances.
const (
	ProvenanceEdge     Provenance = "edge"
	ProvenanceMetadata Provenance = "metadata"
	ProvenanceBoth     Provenance = "both"
)

// LinkEffect says whether a history link takes effect.
type LinkEffect string

// Link effects: a link whose successor is pending (`proposed`, `draft`) does not supersede.
const (
	EffectEffective LinkEffect = "effective"
	EffectPending   LinkEffect = "pending"
)

// Reason is a stable finding reason (design D7).
type Reason string

// Finding reasons. The list is documented in docs/reference/adr-governance.md; a docs test compares them.
const (
	ReasonIndexOnly           Reason = "index-only"
	ReasonAbsent              Reason = "absent"
	ReasonPathMismatch        Reason = "path-mismatch"
	ReasonInactive            Reason = "inactive"
	ReasonSuperseded          Reason = "superseded"
	ReasonHistoricalLink      Reason = "historical-link"
	ReasonRuleUnjustified     Reason = "rule-unjustified"
	ReasonEnforcementGap      Reason = "enforcement-gap"
	ReasonAdvisoryOnly        Reason = "advisory-only"
	ReasonCycle               Reason = "cycle"
	ReasonMissingTarget       Reason = "missing-target"
	ReasonDuplicateID         Reason = "duplicate-id"
	ReasonContradictoryStatus Reason = "contradictory-status"
	ReasonCompetingSuccessors Reason = "competing-successors"
	ReasonTriggerUnsatisfied  Reason = "trigger-unsatisfied"
)

// Reasons lists every finding reason in the documented order.
var Reasons = []Reason{
	ReasonIndexOnly, ReasonAbsent, ReasonPathMismatch, ReasonInactive, ReasonSuperseded, ReasonHistoricalLink,
	ReasonRuleUnjustified, ReasonEnforcementGap, ReasonAdvisoryOnly, ReasonCycle, ReasonMissingTarget,
	ReasonDuplicateID, ReasonContradictoryStatus, ReasonCompetingSuccessors, ReasonTriggerUnsatisfied,
}

// Presence and applicability evidence reasons for unknown results (not finding reasons).
const (
	EvidenceNoRevision           = "no-revision"
	EvidenceNoCapability         = "no-capability"
	EvidenceExtractionIncomplete = "extraction-incomplete"
	EvidenceNotCommitted         = "not-committed"
)

// AnomalyKind is the kind of a history anomaly: the history finding reasons.
type AnomalyKind = Reason

// Anomaly is one history anomaly of a component that contains a registered decision.
type Anomaly struct {
	Kind AnomalyKind
	// Value is the raw `superseded_by` value of a missing target, or the shared label of a duplicate ID.
	Value string
	// Paths are the decision paths that the anomaly names, sorted.
	Paths []string
}

// Next-action categories of the evaluation reporting contract.
const (
	NextActionSupplyEvidence = "supply evidence"
	NextActionGetReview      = "get review"
	NextActionCorrectIntent  = "correct intent"
	NextActionFixAnalysis    = "fix analysis configuration"
)

// Rule IDs of the `adr-governance` pack and their obligation names (the `decisions.obligation` key).
const (
	RulePresence      = "GOV-ADR-001"
	RuleApplicability = "GOV-ADR-002"
	RuleJustification = "GOV-ADR-003"
	RuleEnforcement   = "GOV-ADR-004"
	RuleHistory       = "GOV-ADR-005"
	RuleScope         = "GOV-ADR-006"

	ObligationPresence      = "presence"
	ObligationApplicability = "applicability"
	ObligationJustification = "justification"
	ObligationEnforcement   = "enforcement"
	ObligationHistory       = "history"
	ObligationScope         = "scope"
)

// Candidate kinds.
const (
	CandidateUnregistered = "unregistered-decision"
	CandidateMention      = "mention"
	CandidateCalmADRs     = "calm-adrs-entry"
)
