package adr

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/redact"
)

// Evidence reasons of an `unknown` result that are not finding reasons of design D7: they say why a result could
// not be decided. They are the ones that Presence and the history give, plus the trigger reason below.
const (
	reasonTriggerUnknown = "trigger-unknown"
	reasonNoRegister     = "no-register"
)

// Outputs of the redaction records of this package.
const (
	outputFinding = "finding"
	outputSummary = "summary"
)

// Output kind prefix: the kind of a governance finding is `adr-<reason>`.
const findingKindPrefix = "adr-"

// FindingFingerprint gives the fingerprint of a governance finding (design D7): the rule, the source (the register
// decision ID in canonical form, the canonical label of an unregistered member of a history anomaly, the linked rule
// ID for GOV-ADR-003, the trigger ID for GOV-ADR-006), the sorted comma-joined targets (the subject, rule, and
// successor IDs of the reason), and the canonical edge kind `adr:<reason>`. The path of an ADR file, its title,
// its body and its line are not inputs: renaming the file or editing the text keeps the identity, while a change of
// status that changes the reason (`superseded` instead of nothing) gives a new one.
func FindingFingerprint(ruleID, source string, targets []string, reason string) string {
	return conformance.Fingerprint(conformance.Parts{
		RuleID: ruleID, SourceArchitectureID: source, TargetArchitectureID: joinTargets(targets),
		CanonicalEdgeKind: "adr:" + reason,
	})
}

// joinTargets gives the sorted comma-joined unique targets.
func joinTargets(targets []string) string { return strings.Join(sortedUnique(targets), ",") }

// NextActionOf gives the next-action category of a finding reason (design D7): `supply evidence` for index-only,
// absent, path-mismatch, and trigger-unsatisfied; `get review` for superseded, inactive, historical-link, and
// rule-unjustified; `correct intent` for the enforcement and history reasons; `fix analysis configuration` when
// the capability, the revision, or the extraction coverage is unavailable (or a trigger cannot be evaluated); and
// `supply evidence` for an uncommitted or unrecognized file.
func NextActionOf(reason string) string {
	switch reason {
	case string(ReasonIndexOnly), string(ReasonAbsent), string(ReasonPathMismatch), string(ReasonTriggerUnsatisfied),
		reasonUncommittedEdit, reasonNotRecognized, EvidenceNotCommitted:
		return NextActionSupplyEvidence
	case string(ReasonSuperseded), string(ReasonInactive), string(ReasonHistoricalLink), string(ReasonRuleUnjustified):
		return NextActionGetReview
	case EvidenceNoRevision, EvidenceNoCapability, EvidenceExtractionIncomplete, reasonTriggerUnknown:
		return NextActionFixAnalysis
	default:
		return NextActionCorrectIntent
	}
}

// UnknownReasonOf maps a reason of an `unknown` result to the closed unknown reason of the finding: a missing
// capability is an unsupported family, the history anomalies and duplicate IDs are conflicting evidence, and the
// rest is incomplete coverage.
func UnknownReasonOf(reason string) conformance.UnknownReason {
	switch reason {
	case EvidenceNoCapability:
		return conformance.ReasonUnsupportedFamily
	case string(ReasonCycle), string(ReasonMissingTarget), string(ReasonDuplicateID), string(ReasonContradictoryStatus),
		string(ReasonCompetingSuccessors):
		return conformance.ReasonEvidenceConflict
	default:
		return conformance.ReasonCoverageIncomplete
	}
}

// redactor removes secret values from the ADR text that a finding or the summary shows (an ADR title is untrusted
// repository text) and records each removal: output, file, line, class; never the value.
type redactor struct {
	records []redact.Record
}

// title redacts a title and neutralizes control sequences. The path chooses the redaction passes and is the File
// of the records; output names where the marker is shown.
func (r *redactor) title(path string, line int, title, output string) string {
	text, records := redact.Default().Redact(path, title)

	for i := range records {
		records[i].File, records[i].Line, records[i].Output = path, line, output
	}

	r.records = append(r.records, records...)

	return redact.SanitizeLine(text)
}

// sorted gives the records without duplicates in a fixed order; never nil.
func (r *redactor) sorted() []redact.Record {
	out := slices.Clone(r.records)
	if out == nil {
		return []redact.Record{}
	}

	slices.SortStableFunc(out, func(x, y redact.Record) int {
		for _, c := range []int{
			canonjson.CompareUTF16(x.Output, y.Output), canonjson.CompareUTF16(x.File, y.File), x.Line - y.Line,
			canonjson.CompareUTF16(x.Property, y.Property), canonjson.CompareUTF16(x.Class, y.Class),
		} {
			if c != 0 {
				return c
			}
		}

		return 0
	})

	return slices.Compact(out)
}

// blockSpec holds what the `decision` block of a finding is made from.
type blockSpec struct {
	// ID is the decision ID as the finding shows it.
	ID string
	// Evidence is the provider entity; nil when the decision has none (absent, index-only without entity).
	Evidence *EvidenceDecision
	// Path is the decision path when Evidence is nil (the path pin).
	Path                                          string
	Successors, Predecessors, Governs, EnforcedBy []string
	Candidates                                    []conformance.CandidateSuccessor
}

// block makes the `decision` block (design D7); the title is redacted.
func (r *redactor) block(spec *blockSpec) *conformance.FindingDecision {
	out := &conformance.FindingDecision{
		ID: spec.ID, Path: spec.Path, Successors: sortedUnique(spec.Successors), Predecessors: sortedUnique(spec.Predecessors),
		Governs: sortedUnique(spec.Governs), EnforcedBy: sortedUnique(spec.EnforcedBy),
		CandidateSuccessors: spec.Candidates,
	}

	if out.CandidateSuccessors == nil {
		out.CandidateSuccessors = []conformance.CandidateSuccessor{}
	}

	if ev := spec.Evidence; ev != nil {
		out.Path, out.Line = ev.Path, ev.Line
		out.Status, out.StatusText = ev.Status, ev.StatusText
		out.Title = r.title(ev.Path, ev.Line, ev.Title, outputFinding)
	}

	return out
}

// blockOf makes the `decision` block of a registered decision.
func (r *redactor) blockOf(d *Decision, candidates []conformance.CandidateSuccessor) *conformance.FindingDecision {
	return r.block(&blockSpec{
		ID: d.Entry.ID, Evidence: d.Presence.Decision, Path: d.Presence.Path, Successors: neighborIDs(d.Successors),
		Predecessors: neighborIDs(d.Predecessors), Governs: d.Subjects, EnforcedBy: d.RuleRefs(), Candidates: candidates,
	})
}

func neighborIDs(list []Neighbor) []string {
	out := make([]string, 0, len(list))
	for _, n := range list {
		out = append(out, n.ID)
	}

	return out
}

// findingSpec is one governance finding before the common fields are set.
type findingSpec struct {
	// Result is `violation` or `unknown`.
	Result conformance.RuleVerdict
	// Reason is the finding reason (design D7) of a violation, or the evidence reason of an unknown.
	Reason string
	// Source and Targets are the fingerprint inputs (see FindingFingerprint).
	Source  string
	Targets []string
	// Subject is the decision ID or rule ID that the finding is about.
	Subject                     string
	Message, Expected, Observed string
	Location                    *conformance.Location
	Decision                    *conformance.FindingDecision
	// Related are the decision IDs that the finding relates to.
	Related []string
}

// buildFinding makes a finding of the plan's rule: severity and owner of the rule, the area, the obligation, the
// next action of the reason, and the unknown reason when the result is `unknown`.
func buildFinding(plan *policy.RulePlan, spec *findingSpec) conformance.Finding {
	related := make([]conformance.RelatedRef, 0, len(spec.Related))
	for _, id := range sortedUnique(spec.Related) {
		related = append(related, conformance.RelatedRef{Kind: relatedDecision, ID: id})
	}

	finding := conformance.Finding{
		Fingerprint:      FindingFingerprint(plan.RuleID, spec.Source, spec.Targets, spec.Reason),
		RuleID:           plan.RuleID,
		Severity:         plan.Severity,
		Kind:             conformance.FindingKind(findingKindPrefix + spec.Reason),
		Message:          spec.Message,
		Source:           conformance.Endpoint{ArchitectureID: spec.Source},
		Target:           conformance.Endpoint{ArchitectureID: joinTargets(spec.Targets)},
		EvidencePath:     []conformance.EvidenceStep{},
		RelatedLocations: []conformance.Location{},
		Location:         spec.Location,
		NextAction:       NextActionOf(spec.Reason),
		ObligationID:     plan.Predicate.Decisions,
		Subject:          spec.Subject,
		Result:           spec.Result,
		Expected:         spec.Expected,
		Observed:         spec.Observed,
		ProviderLimits:   conformance.ProviderLimits{Families: []conformance.FamilyLimit{}},
		Area:             conformance.AreaADRGovernance,
		Related:          related,
		Decision:         spec.Decision,
	}

	if plan.Owner != "" {
		owner := plan.Owner
		finding.Owner = &owner
	}

	if spec.Result == conformance.VerdictUnknown {
		finding.Reason = UnknownReasonOf(spec.Reason)
	}

	return finding
}

// relatedDecision is the kind of the related reference of a finding to a decision.
const relatedDecision = "decision"
