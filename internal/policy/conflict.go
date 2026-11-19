package policy

import (
	"cmp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// Polarity says whether an obligation type asks for the relation or rules it out.
type Polarity string

// Polarities. `required` and `allowed` are affirmative: they say that the
// relation is needed or permitted. `forbidden` is negative.
const (
	PolarityAffirmative Polarity = "affirmative"
	PolarityNegative    Polarity = "negative"
)

// Polarity gives the polarity of the obligation type. An unknown type is affirmative.
func (t ObligationType) Polarity() Polarity {
	if t == ObligationForbidden {
		return PolarityNegative
	}

	return PolarityAffirmative
}

// RelationKey gives the text that identifies a relation: the evidence kind,
// the direction, the sorted target CALM IDs, and, for a target that the rule
// takes from the model (`deployedIn`), the target mode. The same relation gives
// the same key, and a rule with an explicit target keeps the key that it always had.
func RelationKey(rel *RelationSpec) string {
	targets := slices.Clone(rel.Target)
	slices.SortFunc(targets, canonjson.CompareUTF16)

	key := string(rel.Kind) + "|" + string(rel.Direction) + "|" + strings.Join(targets, ",")
	if rel.TargetMode != TargetExplicit {
		key += "|" + string(rel.TargetMode)
	}

	return key
}

// ObligationRef is what conflict detection needs to know about an obligation:
// the rule, the subject, the environment, and the relation with its polarity.
// Applies tells that two rules look at different occurrences.
type ObligationRef struct {
	// Applies is the applicability selector of the rule. nil applies to every occurrence.
	Applies      *SelectorExpr
	ObligationID string
	RuleID       string
	Subject      string
	Environment  string
	Type         ObligationType
	Relation     RelationSpec
}

// NamedPrecedence is the versioned precedence list of a policy pack, with the pack ID that names it.
type NamedPrecedence struct {
	// ID is the ID of the pack that declares the list.
	ID string `json:"id"`
	Precedence
}

// Supersession says that a rule lost a conflict to another rule because an
// explicit, versioned precedence list ranks the other rule higher.
type Supersession struct {
	// PrecedenceID is the ID of the pack whose `precedence` decided.
	PrecedenceID string `json:"precedenceId"`
	// RuleID is the rule that won.
	RuleID string `json:"ruleId"`
	// PrecedenceVersion is the `version` of the precedence list.
	PrecedenceVersion int `json:"precedenceVersion"`
}

// Conflict is a pair of contradicting rules for one subject and environment
// that no precedence entry resolves.
type Conflict struct {
	Subject     string `json:"subject"`
	Environment string `json:"environment,omitempty"`
	// Relation is the relation key of the first obligation.
	Relation string `json:"relation"`
	// Affirmative is the rule that requires or allows the relation, Negative the rule that forbids it.
	Affirmative string `json:"affirmative"`
	Negative    string `json:"negative"`
	// AffirmativeObligation and NegativeObligation are the IDs of the two obligations.
	AffirmativeObligation string `json:"affirmativeObligation"`
	NegativeObligation    string `json:"negativeObligation"`
}

// Loss is an obligation that lost to a rule with a higher precedence.
type Loss struct {
	// ObligationID is the obligation of the losing rule.
	ObligationID string
	// RuleID is the losing rule.
	RuleID string
	By     Supersession
}

// ConflictReport is the result of DetectConflicts.
type ConflictReport struct {
	// Conflicts are the contradictions that no precedence resolves, sorted. A run with one stops before evaluation.
	Conflicts []Conflict
	// Losses are the obligations that a precedence entry superseded, sorted. The loser stays visible.
	Losses []Loss
}

// pairCollector gathers the conflicts and the losses of the obligation pairs that it is given.
type pairCollector struct {
	report      ConflictReport
	seenLoss    map[string]struct{}
	precedences []NamedPrecedence
}

// DetectConflicts finds the rules that contradict each other for one
// obligation or one evidence occurrence: two obligations with the same
// subject and environment, relations of the same kind and direction whose
// targets overlap (an empty target list overlaps any), opposite polarity
// (`required` or `allowed` against `forbidden`), and applicability selectors
// that are not provably disjoint. No rule has implicit priority and the first
// match never wins.
//
// A pair that a precedence list ranks (both rule IDs are in the list) is not a
// conflict: the rule that is listed first wins and the other is a Loss. The
// first list in the order given that names both rules decides. A pair that no
// list ranks is a Conflict.
func DetectConflicts(refs []ObligationRef, precedences []NamedPrecedence) ConflictReport {
	collector := pairCollector{seenLoss: map[string]struct{}{}, precedences: precedences}

	for _, group := range groupByRelation(refs) {
		for i := range group {
			for _, second := range group[i+1:] {
				collector.record(group[i], second)
			}
		}
	}

	sortReport(&collector.report)

	return collector.report
}

// record adds the conflict or the loss that two obligations of one group give, if they contradict each other.
func (c *pairCollector) record(first, second *ObligationRef) {
	if !contradicts(first, second) {
		return
	}

	winner, loser, supersession, ranks := ranked(first, second, c.precedences)
	if !ranks {
		c.report.Conflicts = append(c.report.Conflicts, conflictOf(first, second))

		return
	}

	lossKey := loser.ObligationID + "\x00" + winner.RuleID + "\x00" + supersession.PrecedenceID
	if _, dup := c.seenLoss[lossKey]; dup {
		return
	}

	c.seenLoss[lossKey] = struct{}{}

	c.report.Losses = append(c.report.Losses, Loss{ObligationID: loser.ObligationID, RuleID: loser.RuleID, By: supersession})
}

// sortReport puts the conflicts and the losses of the report in their canonical order.
func sortReport(report *ConflictReport) {
	slices.SortFunc(report.Conflicts, func(x, y Conflict) int {
		return cmp.Or(
			canonjson.CompareUTF16(x.Subject, y.Subject), canonjson.CompareUTF16(x.Environment, y.Environment),
			canonjson.CompareUTF16(x.Affirmative, y.Affirmative), canonjson.CompareUTF16(x.Negative, y.Negative),
			canonjson.CompareUTF16(x.Relation, y.Relation),
		)
	})
	slices.SortFunc(report.Losses, func(x, y Loss) int {
		return cmp.Or(canonjson.CompareUTF16(x.ObligationID, y.ObligationID), canonjson.CompareUTF16(x.By.RuleID, y.By.RuleID))
	})
}

// groupByRelation groups the obligations by subject, environment, relation kind, and direction, in order of first appearance.
func groupByRelation(refs []ObligationRef) [][]*ObligationRef {
	index := map[string]int{}

	var groups [][]*ObligationRef

	for i := range refs {
		ref := &refs[i]
		key := ref.Subject + "\x00" + ref.Environment + "\x00" + string(ref.Relation.Kind) + "\x00" + string(ref.Relation.Direction)

		at, ok := index[key]
		if !ok {
			at = len(groups)
			index[key] = at

			groups = append(groups, nil)
		}

		groups[at] = append(groups[at], ref)
	}

	return groups
}

// contradicts says whether two obligations of one group contradict each other: two rules, opposite polarity,
// targets that can be the same, and applicability selectors that are not provably disjoint.
func contradicts(first, second *ObligationRef) bool {
	return first.RuleID != second.RuleID && first.Type.Polarity() != second.Type.Polarity() &&
		relationsOverlap(&first.Relation, &second.Relation) && !Disjoint(first.Applies, second.Applies)
}

// relationsOverlap says whether two relations of one kind and direction can refer to the same target. Targets that
// the rules name overlap as targetsOverlap says. A target taken from the model (`deployedIn`) is, for each obligation,
// the containers of its subject (declared) or every other hosting target (the complement of the containers):
//   - an obligation whose subject has no declared placement is not applicable and contradicts nothing;
//   - two complements always overlap, because the other hosting targets are unbounded;
//   - a complement and a set overlap when the set has a target outside the complement, so the containers of a subject and
//     the other hosting targets of the same subject never do;
//   - a complement and a relation with no target do not contradict each other: a relation with no target is met inside the
//     containers that the complement leaves out.
func relationsOverlap(first, second *RelationSpec) bool {
	fromModel := func(rel *RelationSpec) bool { return rel.TargetMode != TargetExplicit }

	switch {
	case fromModel(first) && len(first.Target) == 0, fromModel(second) && len(second.Target) == 0:
		return false
	case first.TargetMode == TargetOtherHostingTargets && second.TargetMode == TargetOtherHostingTargets:
		return true
	case first.TargetMode == TargetOtherHostingTargets:
		return outside(second.Target, first.Target)
	case second.TargetMode == TargetOtherHostingTargets:
		return outside(first.Target, second.Target)
	}

	return targetsOverlap(first.Target, second.Target)
}

// outside says whether the set has a target that the complement leaves out. An empty set (a relation with no target) has none.
func outside(set, complement []string) bool {
	return slices.ContainsFunc(set, func(id string) bool { return !slices.Contains(complement, id) })
}

func conflictOf(first, second *ObligationRef) Conflict {
	affirmative, negative := first, second
	if first.Type.Polarity() == PolarityNegative {
		affirmative, negative = second, first
	}

	return Conflict{
		Subject: first.Subject, Environment: first.Environment, Relation: RelationKey(&affirmative.Relation),
		Affirmative: affirmative.RuleID, Negative: negative.RuleID,
		AffirmativeObligation: affirmative.ObligationID, NegativeObligation: negative.ObligationID,
	}
}

// ranked says which of the two rules wins by an explicit precedence list. ok is false when no list names both.
func ranked(first, second *ObligationRef, precedences []NamedPrecedence) (winner, loser *ObligationRef, by Supersession, ok bool) {
	for i := range precedences {
		list := &precedences[i]
		rankFirst, rankSecond := list.Rank(first.RuleID), list.Rank(second.RuleID)

		if rankFirst < 0 || rankSecond < 0 {
			continue
		}

		winner, loser = first, second
		if rankSecond < rankFirst {
			winner, loser = second, first
		}

		return winner, loser, Supersession{PrecedenceID: list.ID, PrecedenceVersion: list.Version, RuleID: winner.RuleID}, true
	}

	return nil, nil, Supersession{}, false
}

// targetsOverlap says whether two relations can refer to the same target. A relation with no target refers to any.
func targetsOverlap(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return true
	}

	return slices.ContainsFunc(left, func(id string) bool { return slices.Contains(right, id) })
}

// Disjoint says whether Concordia can prove that no occurrence satisfies both
// selectors. It is conservative: false means "may overlap". A nil selector
// applies to everything and is never disjoint.
//
// The proof cases are: two leaves of one dimension that accept no common value
// for one property; an `allOf` with one operand that is disjoint; an `anyOf`
// whose operands are all disjoint; and `not` of a leaf against a leaf that
// implies it.
func Disjoint(left, right *SelectorExpr) bool {
	if left == nil || right == nil {
		return false
	}

	if disjointRank(right) > disjointRank(left) {
		return Disjoint(right, left)
	}

	switch left.Op {
	case OpAnyOf:
		return allDisjoint(left.Operands, right)
	case OpAllOf:
		return slices.ContainsFunc(left.Operands, func(o *SelectorExpr) bool { return Disjoint(o, right) })
	case OpNot:
		return len(left.Operands) == 1 && implies(right, left.Operands[0])
	case OpMatch:
		return leavesDisjoint(left, right)
	}

	return false
}

// allDisjoint says whether there is at least one operand and each operand is disjoint from the selector.
func allDisjoint(operands []*SelectorExpr, other *SelectorExpr) bool {
	return len(operands) > 0 && !slices.ContainsFunc(operands, func(o *SelectorExpr) bool { return !Disjoint(o, other) })
}

// The ranks of the selector operators, in the order in which Disjoint takes them apart.
const (
	rankLeaf = iota
	rankNot
	rankAllOf
	rankAnyOf
)

// disjointRank orders the selector operators by the order in which Disjoint takes them apart:
// `anyOf`, then `allOf`, then `not`, then the leaves.
func disjointRank(expr *SelectorExpr) int {
	switch expr.Op {
	case OpAnyOf:
		return rankAnyOf
	case OpAllOf:
		return rankAllOf
	case OpNot:
		return rankNot
	case OpMatch:
		return rankLeaf
	}

	return rankLeaf
}

// leavesDisjoint says whether two leaves of one dimension accept no common value for some property.
func leavesDisjoint(a, b *SelectorExpr) bool {
	if !leavesOfOneDimension(a, b) {
		return false
	}

	for name, left := range a.Match {
		if right, ok := b.Match[name]; ok && !shareValue(left, right) {
			return true
		}
	}

	return false
}

// shareValue says whether the two value lists have a value in common.
func shareValue(left, right []string) bool {
	return slices.ContainsFunc(left, func(v string) bool { return slices.Contains(right, v) })
}

// implies says whether every occurrence that matches leaf `from` also matches the leaf `to`.
func implies(from, to *SelectorExpr) bool {
	if !leavesOfOneDimension(from, to) || len(to.Match) == 0 {
		return false
	}

	for name, accepted := range to.Match {
		if !acceptsOnly(from.Match[name], accepted) {
			return false
		}
	}

	return true
}

// acceptsOnly says whether a property that accepts own, and accepts at least one value, accepts nothing outside accepted.
func acceptsOnly(own, accepted []string) bool {
	return len(own) > 0 && !slices.ContainsFunc(own, func(v string) bool { return !slices.Contains(accepted, v) })
}

// leavesOfOneDimension says whether both selectors are leaves of one dimension.
func leavesOfOneDimension(a, b *SelectorExpr) bool {
	return a.Op == OpMatch && b.Op == OpMatch && a.Dimension == b.Dimension
}
