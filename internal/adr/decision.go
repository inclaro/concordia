package adr

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

// Neighbor is a decision on the other end of a history link: a successor or a predecessor.
type Neighbor struct {
	// Path is the repository-relative path of the decision (with the project prefix).
	Path string
	// ID is the canonical ID of the decision: the register ID of the entry that matched it, else its label.
	ID string
	// Effective is false for a pending link (the successor is `proposed` or `draft`).
	Effective bool
}

// Decision is the evaluated state of one registered decision: the input of the obligation checks of replacement,
// justification, and enforcement. The three states (presence, applicability, enforcement) stay separate: nothing
// here derives one from another.
type Decision struct {
	Entry *Entry
	// Subjects are the resolved governed subjects (`calm:` refs), sorted.
	Subjects []string
	// Successors and Predecessors are the neighbors in the history, sorted by path.
	Successors   []Neighbor
	Predecessors []Neighbor
	// Reason is the evidence reason of the applicability.
	Reason string
	// Mapped is the status that the lifecycle `statusMap` gave; "" when no mapping applied.
	Mapped string
	// Applicability is the state from the history.
	Applicability ApplicabilityState
	Presence      PresenceResult
	// StatusLags says that the decision is superseded while its own status still says active.
	StatusLags bool
}

// ID gives the canonical register ID of the decision.
func (d *Decision) ID() string { return d.Entry.ID }

// Expected gives the expected lifecycle of the entry; an entry that does not set it expects `active`.
func (d *Decision) Expected() Expect {
	if d.Entry.Expect == "" {
		return ExpectActive
	}

	return d.Entry.Expect
}

// Historical says whether the register expects the decision to be historical.
func (d *Decision) Historical() bool { return d.Expected() == ExpectHistorical }

// Justifies says whether the decision counts as an active declaration: its applicability is `active` and the
// register expects it to be active. The links of a historical decision never justify a rule or transfer a subject.
func (d *Decision) Justifies() bool {
	return !d.Historical() && d.Applicability == ApplicabilityActive
}

// Unresolved says whether the active expectation of the decision cannot be decided: its applicability is
// `ambiguous` or `unknown`. A historical decision is never unresolved: it does not justify in any case.
func (d *Decision) Unresolved() bool {
	return !d.Historical() && (d.Applicability == ApplicabilityAmbiguous || d.Applicability == ApplicabilityUnknown)
}

// EffectiveSuccessors are the successors whose link takes effect.
func (d *Decision) EffectiveSuccessors() []Neighbor {
	return slices.DeleteFunc(slices.Clone(d.Successors), func(n Neighbor) bool { return !n.Effective })
}

// RuleRefs gives the linked rules (`enforcedBy`) in the ref form `<rule>@<pack>/<version>`, sorted and unique.
func (d *Decision) RuleRefs() []string {
	out := make([]string, 0, len(d.Entry.EnforcedBy))
	for _, link := range d.Entry.EnforcedBy {
		out = append(out, link.String())
	}

	return sortedUnique(out)
}

// Classifier tells which linked rules are mandatory (PRD §0.3): severity at or above `check.failOn` and effective
// `onUnknown: fail`. It reads the selected rule plans, whose `onUnknown` is already the effective one (the rule,
// the control, the workspace and `--unknown` are applied by the policy compilation).
type Classifier struct {
	FailOn diag.Severity
	Plans  []policy.RulePlan
}

// Plan gives the selected plan of the rule of the link: the same rule ID in the same pack and version. nil when the
// rule is not selected (a `--rule` filter removed it): the link stays valid, but the rule is not judged.
func (c Classifier) Plan(ref RuleRef) *policy.RulePlan {
	for i := range c.Plans {
		plan := &c.Plans[i]
		if plan.RuleID == ref.Rule && plan.Pack.ID == ref.PackID && plan.Pack.Version == ref.PackVersion {
			return plan
		}
	}

	return nil
}

// Mandatory says whether the plan is mandatory: its severity is at or above `check.failOn` and its effective
// `onUnknown` is `fail`.
func (c Classifier) Mandatory(plan *policy.RulePlan) bool {
	return severityRank(string(plan.Severity)) >= severityRank(string(c.FailOn)) &&
		plan.EvidenceRequirements.OnUnknown == policy.UnknownFail
}

// severityRank orders the severities: error above warning above info. An unset threshold is `error`.
func severityRank(severity string) int {
	switch severity {
	case string(diag.SeverityError), "":
		return 2
	case string(diag.SeverityWarning):
		return 1
	default:
		return 0
	}
}

// sortedUnique gives the sorted list without duplicates; never nil.
func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	if out == nil {
		out = []string{}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return slices.Compact(out)
}
