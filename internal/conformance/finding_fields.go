package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
)

// FindingDelta is the difference between the declared and the observed architecture that a finding relates
// to: the reconciliation entry of the pair of the finding (OUT-02 "related delta").
type FindingDelta struct {
	// Class is the reconciliation class of the pair: `undeclared`, `unobserved`, `contradiction`, or `unknown`.
	Class Classification `json:"class"`
	From  string         `json:"from"`
	To    string         `json:"to"`
	// RelationshipID is the CALM relationship of the pair, when the architecture declares one.
	RelationshipID string `json:"relationshipId,omitempty"`
	// ObservedKinds are the canonical kinds that were observed between the two elements, sorted, never nil.
	ObservedKinds []observation.CanonicalEdgeKind `json:"observedKinds"`
}

// FamilyLimit is the support of one evidence family that a finding depends on.
type FamilyLimit struct {
	Family   string                     `json:"family"`
	Level    observation.SupportLevel   `json:"level"`
	Maturity observation.FamilyMaturity `json:"maturity"`
	// LossModes are the known loss modes of the family, sorted, never nil.
	LossModes []string `json:"lossModes"`
}

// ProviderLimits names the capability profile of the run and the families (with their loss modes) that a
// finding depends on (OUT-02 "provider limits").
type ProviderLimits struct {
	Profile CbmProfile `json:"profile"`
	// Families are sorted by name, never nil. Empty when the finding depends on no family of the profile.
	Families []FamilyLimit `json:"families"`
}

// RelatedRef is a decision, view, or assessment that a finding relates to.
type RelatedRef struct {
	// Kind is `decision`, `view`, or `assessment`.
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Next actions (OUT-02). Findings use exactly these values.
const (
	nextActionIntent = "correct intent"
)

// nextActionOfKind gives the next action of an as-built finding kind: a violation of a rule is fixed in the
// implementation; a difference between the declared and the observed architecture is settled in the intent.
func nextActionOfKind(kind FindingKind) string {
	switch kind {
	case KindUndeclared, KindUnobserved, KindContradiction, KindOwnershipViolation:
		return nextActionIntent
	case KindBindingGap:
		return NextActionResolveBinding
	case KindForbiddenDependency, KindLayerViolation, KindCycle, KindForbiddenPath, KindDisallowedDep, KindPeerDependency,
		KindNonPublicAPI, KindBudgetExceeded, KindMissingEdge, KindMissingEntity, KindObligationViolation, KindObligationAbsent:
		return nextActionFix
	case KindObligationUnknown:
		return nextActionEvidence
	}

	return nextActionFix
}

// completeFindings sets the OUT-02 fields that the families do not set themselves: the area, the expected
// condition, the observed evidence or gap, the delta, the provider limits, the related list, and the next action.
func completeFindings(findings []Finding, rules []RuleResult, input *EvaluateInput, reconciliation []ReconciliationEntry) {
	ruleByID := make(map[string]*RuleResult, len(rules))
	for i := range rules {
		ruleByID[rules[i].RuleID] = &rules[i]
	}

	pairs := nonConformingPairs(reconciliation)

	for i := range findings {
		finding := &findings[i]
		completeFinding(finding, ruleByID[finding.RuleID], pairs, input)
	}
}

// nonConformingPairs indexes the reconciliation entries that are not `conforming` by their (from, to) pair.
func nonConformingPairs(reconciliation []ReconciliationEntry) map[[2]string]*ReconciliationEntry {
	pairs := make(map[[2]string]*ReconciliationEntry, len(reconciliation))

	for i := range reconciliation {
		if entry := &reconciliation[i]; entry.Classification != ClassConforming {
			pairs[[2]string{entry.From, entry.To}] = entry
		}
	}

	return pairs
}

// completeFinding fills the OUT-02 fields of one finding that are still empty.
func completeFinding(finding *Finding, rule *RuleResult, pairs map[[2]string]*ReconciliationEntry, input *EvaluateInput) {
	if finding.Area == "" {
		finding.Area = AreaConformance
		if rule != nil {
			finding.Area = AreaOfRule(rule)
		}
	}

	if finding.NextAction == "" {
		finding.NextAction = nextActionOfKind(finding.Kind)
	}

	if finding.Expected == "" {
		finding.Expected = expectedOf(finding, rule)
	}

	if finding.Observed == "" {
		finding.Observed = observedOf(finding)
	}

	if finding.Delta == nil {
		finding.Delta = deltaOf(finding, pairs)
	}

	if finding.Related == nil {
		finding.Related = []RelatedRef{}
	}

	finding.ProviderLimits = providerLimitsOf(finding, rule, input)
}

// obligationHoldsTemplate is what the rule of a finding of an obligation kind requires.
const obligationHoldsTemplate = "the obligation {obligation} of rule {rule} holds for {subject}"

// expectedTemplates holds what the rule of a finding of each kind requires. The placeholders {from} and {to}
// are the architecture ids of the endpoints, {element} is the target or else the source, and {obligation},
// {rule}, and {subject} come from the finding.
var expectedTemplates = map[FindingKind]string{
	KindForbiddenDependency: "no dependency from {from} to {to}",
	KindLayerViolation:      "{from} depends only on the layers that the architecture allows; {to} is not one of them",
	KindUndeclared:          "an observed relationship from {from} to {to} is declared in the architecture",
	KindContradiction:       "the observed relationship from {from} to {to} agrees with the declared relationship",
	KindUnobserved:          "the declared relationship from {from} to {to} is observed in the code",
	KindCycle:               "no dependency cycle through {from}",
	KindForbiddenPath:       "no transitive path from {from} to {to}",
	KindDisallowedDep:       "{from} depends only on the targets that the rule allows; {to} is not allowed",
	KindPeerDependency:      "no dependency between the peers {from} and {to}",
	KindNonPublicAPI:        "{from} uses only the public API of {to}",
	KindOwnershipViolation:  "{from} has the owner that the rule requires",
	KindBudgetExceeded:      "{from} stays within the budget of the rule",
	KindMissingEdge:         "a required relationship from {from} to {to} is observed",
	KindMissingEntity:       "the required element {element} is observed",
	KindBindingGap:          "every observed endpoint that the rule reads is bound to an architecture element",
	KindObligationViolation: obligationHoldsTemplate,
	KindObligationAbsent:    obligationHoldsTemplate,
	KindObligationUnknown:   obligationHoldsTemplate,
}

// expectedOf says what the rule of the finding requires.
func expectedOf(finding *Finding, rule *RuleResult) string {
	fromID, toID := finding.Source.ArchitectureID, finding.Target.ArchitectureID

	if template, ok := expectedTemplates[finding.Kind]; ok {
		return strings.NewReplacer(
			"{from}", fromID, "{to}", toID, "{element}", cmp.Or(toID, fromID),
			"{obligation}", finding.ObligationID, "{rule}", finding.RuleID, "{subject}", finding.Subject,
		).Replace(template)
	}

	if rule != nil {
		return "rule " + rule.RuleID + " holds"
	}

	return "the rule " + finding.RuleID + " holds"
}

// observedOf says what was observed, or what the gap is.
func observedOf(finding *Finding) string {
	switch finding.Kind { //nolint:exhaustive // the kinds of the other cases read the edge of the finding.
	case KindUnobserved, KindMissingEdge:
		return "no matching edge observed between the two elements"
	case KindMissingEntity:
		return "no matching element observed"
	case KindObligationAbsent, KindObligationUnknown:
		return finding.Message
	}

	if finding.Edge == nil {
		return finding.Message
	}

	text := fmt.Sprintf("%s edge (native %s, confidence %s)", finding.Edge.Kind, finding.Edge.NativeKind, finding.Edge.Confidence)

	if finding.Source.Observed != nil && finding.Target.Observed != nil {
		text += fmt.Sprintf(" from %s to %s", finding.Source.Observed.QualifiedName, finding.Target.Observed.QualifiedName)
	}

	if finding.Location != nil {
		text += " at " + locationText(finding.Location)
	}

	return text
}

// locationText gives `path` or `path:line`.
func locationText(loc *Location) string {
	if loc.Line > 0 {
		return fmt.Sprintf("%s:%d", loc.Path, loc.Line)
	}

	return loc.Path
}

// deltaOf gives the reconciliation entry of the pair of the finding, when there is one that is not `conforming`.
func deltaOf(finding *Finding, pairs map[[2]string]*ReconciliationEntry) *FindingDelta {
	pair := [2]string{finding.Source.ArchitectureID, finding.Target.ArchitectureID}
	if !isSingleEndpoint(pair[0]) || !isSingleEndpoint(pair[1]) {
		return nil
	}

	entry, ok := pairs[pair]
	if !ok {
		return nil
	}

	return &FindingDelta{
		Class: entry.Classification, From: entry.From, To: entry.To, RelationshipID: entry.RelationshipID,
		ObservedKinds: slices.Clone(entry.ObservedKinds),
	}
}

// isSingleEndpoint says whether an architecture id names exactly one element.
func isSingleEndpoint(id string) bool {
	return id != "" && !strings.Contains(id, ",")
}

// findingKinds gives the canonical edge kinds that the finding depends on: its edge, the edges of its evidence
// path, and the relation of its obligation.
func findingKinds(finding *Finding, rule *RuleResult) []observation.CanonicalEdgeKind {
	var kinds []observation.CanonicalEdgeKind

	if finding.Edge != nil {
		kinds = append(kinds, finding.Edge.Kind)
	}

	for i := range finding.EvidencePath {
		if edge := finding.EvidencePath[i].Edge; edge != nil {
			kinds = append(kinds, edge.Kind)
		}
	}

	if rule != nil && rule.Plan.Obligation != nil {
		kinds = append(kinds, rule.Plan.Obligation.Relation.Kind)
	}

	return kinds
}

// providerLimitsOf names the profile of the run and the families of the finding with their loss modes.
func providerLimitsOf(finding *Finding, rule *RuleResult, input *EvaluateInput) ProviderLimits {
	limits := ProviderLimits{Profile: NewCbmProfile(input.Profile), Families: []FamilyLimit{}}

	seen := map[string]struct{}{}

	for _, kind := range findingKinds(finding, rule) {
		family, ok := observation.FamilyForKind(kind)
		if !ok {
			continue
		}

		if _, dup := seen[family]; dup {
			continue
		}

		seen[family] = struct{}{}

		if state, known := input.Typed.Families[family]; known {
			limits.Families = append(limits.Families, familyLimitOf(family, &state))
		}
	}

	slices.SortFunc(limits.Families, func(a, b FamilyLimit) int { return canonjson.CompareUTF16(a.Family, b.Family) })

	return limits
}

// familyLimitOf gives the support of one family with its loss modes sorted and never nil.
func familyLimitOf(family string, state *observation.FamilyState) FamilyLimit {
	modes := slices.Clone(state.LossModes)
	if modes == nil {
		modes = []string{}
	}

	slices.SortFunc(modes, canonjson.CompareUTF16)

	return FamilyLimit{Family: family, Level: state.Level, Maturity: state.Maturity, LossModes: modes}
}
