package conformance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// run evaluates the obligation by its type.
func (r *obligationRun) run() {
	if occ := r.notApplicable(); occ != nil {
		r.add(*occ)

		return
	}

	anchors, others := r.anchorsOf()

	if r.hasNoImageBinding(anchors) {
		r.add(r.entity(OccurrenceAbsent, VerdictNotApplicable, "", "no image binding names the subject: a proposed binding takes no part in resolution"))

		return
	}

	switch r.ob.Type {
	case policy.ObligationRequired:
		r.required(anchors)
	case policy.ObligationForbidden, policy.ObligationAllowed:
		r.permission(anchors)
	}

	// Facts of anchors in other environments are evaluated separately and never count here.
	for _, anchor := range others {
		seen := r.scan(anchor)
		r.out = append(r.out, seen.separate...)
	}
}

// hasNoImageBinding says that a subject that no active `image` binding maps to an image has nothing to anchor a
// `from-image` obligation on.
func (r *obligationRun) hasNoImageBinding(anchors []string) bool {
	return len(anchors) == 0 && r.ob.Relation.Direction == policy.DirectionFromImage && !r.e.imageSubjects[r.ob.Subject]
}

// notApplicable gives the result of an obligation that the model proves does not apply: its target is taken from the
// model (`deployedIn`) and no `deployed-in` relationship lists the subject. It is never `unknown`.
func (r *obligationRun) notApplicable() *OccurrenceResult {
	if r.ob.Relation.TargetMode == policy.TargetExplicit || len(r.ob.Relation.Target) > 0 {
		return nil
	}

	occ := r.entity(OccurrenceAbsent, VerdictNotApplicable, "", "the model declares no placement for the subject: no deployed-in relationship lists it")

	return &occ
}

// entitySelector evaluates the applicability selector for the subject when no fact is there to evaluate it on.
// It gives a result when the rule does not apply (false) or cannot be decided (unknown), and nil when it applies.
func (r *obligationRun) entitySelector() *OccurrenceResult {
	// A selector that was false for the facts has decided where facts exist; the subject itself is not tested again.
	if r.sawInapplicable {
		return nil
	}

	sel := r.spec.Applies.Eval(r.contextFor("", nil))

	// A property that only a fact has (protocol, operation, ...) cannot be known without a fact, and no fact is
	// there to discriminate: the rule applies to the subject. A property with no source at all stays unknown.
	if sel.Value == policy.TriUnknown && !slices.ContainsFunc(sel.Unknown, func(p policy.Property) bool { return !factDimensions[p.Dimension] }) {
		return nil
	}

	switch sel.Value {
	case policy.TriTrue:
		return nil
	case policy.TriFalse:
		occ := r.entity(OccurrenceAbsent, VerdictNotApplicable, "", "the applicability selector of the rule is false for the subject")

		return &occ
	case policy.TriUnknown:
	}

	occ := r.entity(OccurrenceAbsent, VerdictUnknown, ReasonSelectorPropertyUnknown,
		"selector property unknown: "+propertyList(sel.Unknown)+" (no fact is there to give it)")

	return &occ
}

// absence makes the result of a necessary relation that has no admissible fact. By the absence rule it is a
// violation only with complete qualified coverage; otherwise it is unknown with the reason of what is missing.
func (r *obligationRun) absence(anchor string) {
	if occ := r.entitySelector(); occ != nil {
		r.locate(occ, anchor)
		r.add(*occ)

		return
	}

	var occ OccurrenceResult

	if block := r.claimBlocked(r.spec.Completeness); block != nil {
		occ = r.entity(OccurrenceAbsent, VerdictUnknown, block.reason, block.detail)
	} else {
		occ = r.entity(OccurrenceAbsent, VerdictViolation, "", fmt.Sprintf("the necessary %s relation is absent and the coverage of the predicate is complete", r.ob.Relation.Kind))
	}

	r.locate(&occ, anchor)
	r.add(occ)
}

// occurrenceSpan is the range of r.out that the results of one anchor take.
type occurrenceSpan struct{ from, to int }

// required evaluates a `required` obligation for each anchor of the subject. forAll: one satisfying entity never
// satisfies the others. exists, or agreement `any` of the bindings: one satisfying entity is enough.
func (r *obligationRun) required(anchors []string) {
	if len(anchors) == 0 {
		r.absence("")

		return
	}

	spans := make([]occurrenceSpan, len(anchors))

	for i, anchor := range anchors {
		spans[i].from = len(r.out)
		r.requiredAnchor(anchor)
		spans[i].to = len(r.out)
	}

	if r.existential() {
		r.releaseUndecidedSpans(spans)
	}
}

// releaseUndecidedSpans makes the results of the anchors that did not decide the obligation not decisive. One entity
// that satisfies the requirement decides; with none, an entity that may still satisfy it (unknown) keeps the
// obligation from being a definite violation.
func (r *obligationRun) releaseUndecidedSpans(spans []occurrenceSpan) {
	verdicts := make([]RuleVerdict, len(spans))

	for i, sp := range spans {
		verdicts[i] = r.spanVerdict(sp.from, sp.to)
	}

	decided, ok := decidingVerdict(verdicts)
	if !ok {
		return
	}

	for i, sp := range spans {
		if verdicts[i] == decided {
			continue
		}

		for j := sp.from; j < sp.to; j++ {
			r.out[j].Decisive = false
		}
	}
}

// decidingVerdict gives the verdict that decides an existential obligation: pass when an anchor passes, else unknown
// when one is unknown. It says false when neither is there.
func decidingVerdict(verdicts []RuleVerdict) (RuleVerdict, bool) {
	switch {
	case slices.Contains(verdicts, VerdictPass):
		return VerdictPass, true
	case slices.Contains(verdicts, VerdictUnknown):
		return VerdictUnknown, true
	}

	return "", false
}

func (r *obligationRun) spanVerdict(from, to int) RuleVerdict {
	var verdicts []RuleVerdict

	for i := range r.out[from:to] {
		if occ := &r.out[from+i]; occ.Decisive {
			verdicts = append(verdicts, occ.Result)
		}
	}

	return WorstVerdict(verdicts)
}

func (r *obligationRun) requiredAnchor(anchor string) {
	seen := r.scan(anchor)

	switch {
	case len(seen.valid) > 0:
		r.satisfy(anchor, seen.valid)
		r.addAll(seen.problems, false)
		r.addAll(seen.mismatch, false)
	case len(seen.problems)+len(seen.mismatch) > 0:
		r.addAll(seen.problems, true)
		r.addAll(seen.mismatch, true)
	default:
		r.absence(anchor)
	}

	r.addAll(seen.separate, false)
	r.reverse(anchor)
}

func (r *obligationRun) addAll(list []OccurrenceResult, decisive bool) {
	for i := range list {
		list[i].Decisive = decisive && list[i].Decisive
		r.out = append(r.out, list[i])
	}
}

// satisfy makes the results of the admissible facts of a required relation: a pass for each fact, unless the
// facts disagree (unknown, evidence conflict), a claim is blocked, or the provider may have merged the occurrence.
func (r *obligationRun) satisfy(anchor string, valid []*BoundEdge) {
	if conflict := r.conflictOf(anchor, valid); conflict != nil {
		r.add(*conflict)

		return
	}

	need := r.spec.PassNeed()

	for _, bound := range valid {
		r.add(r.satisfaction(anchor, bound, need))
	}
}

// satisfaction gives the result of one admissible fact of a required relation: a pass, unless a claim is blocked or the
// provider may have merged the occurrence.
func (r *obligationRun) satisfaction(anchor string, bound *BoundEdge, need policy.Completeness) OccurrenceResult {
	if block := r.claimBlocked(need); block != nil {
		return r.occurrenceOf(anchor, bound, OccurrenceMatching, VerdictUnknown, block.reason, block.detail)
	}

	if r.mayHaveMergedOccurrences(bound, need) {
		return r.occurrenceOf(anchor, bound, OccurrenceMatching, VerdictUnknown, ReasonCoverageIncomplete,
			"the provider may have merged occurrences of this fact ("+strings.Join(bound.Edge.LossModes, ", ")+"); completeness complete needs every occurrence")
	}

	return r.occurrenceOf(anchor, bound, OccurrenceMatching, VerdictPass, "", "the necessary relation is present: "+factText(bound))
}

// mayHaveMergedOccurrences says whether the provider may have merged occurrences of a fact that a complete claim needs
// one by one, and the rendered record does not cure it.
func (r *obligationRun) mayHaveMergedOccurrences(bound *BoundEdge, need policy.Completeness) bool {
	return bound.Edge.PossiblyLossy && need == policy.CompletenessComplete && !r.curedByRendering(bound.Edge.LossModes)
}

// curedByRendering says whether the complete rendered record of the environment of the obligation cures every loss
// mode of a fact: the rendered documents show each container, so a merged occurrence of the provider is not missing.
func (r *obligationRun) curedByRendering(lossModes []string) bool {
	if r.ob.Environment == "" || len(lossModes) == 0 {
		return false
	}

	family, ok := observation.FamilyForKind(r.ob.Relation.Kind)
	if !ok {
		return false
	}

	records := CoverageOf(r.e.input.Scope.Predicates, []string{family}, observation.CoverageScope{Environment: r.ob.Environment})
	if len(records) != 1 {
		return false
	}

	rec := &records[0]
	resolved := map[string]bool{}

	for i := range rec.Resolved {
		resolved[rec.Resolved[i].ID] = rec.Resolved[i].ResolvedBy != ""
	}

	return rec.Complete() && !slices.ContainsFunc(lossModes, func(id string) bool { return !resolved[id] })
}

func factText(bound *BoundEdge) string {
	return fmt.Sprintf("%s %s -> %s", bound.Edge.Kind, bound.From.QualifiedName, bound.To.QualifiedName)
}

// conflictOf finds facts of one identity and one role that disagree about a property (a value that both
// state and that differs). It gives the evidence-conflict result, which names the sources and never a value, and
// no fact wins by order. Facts that differ in the identity (another container, another environment) are other
// occurrences, not a disagreement.
func (r *obligationRun) conflictOf(anchor string, valid []*BoundEdge) *OccurrenceResult {
	groups, order := groupFactsByIdentity(valid)

	for _, key := range order {
		if occ := r.groupConflict(anchor, groups[key]); occ != nil {
			return occ
		}
	}

	return nil
}

// groupFactsByIdentity groups the facts by identity and role, and gives the keys in order of first appearance.
func groupFactsByIdentity(valid []*BoundEdge) (groups map[string][]*BoundEdge, order []string) {
	groups = map[string][]*BoundEdge{}

	for _, bound := range valid {
		key := observation.EdgeKeyOf(bound.Edge).String() + "\x1f" + string(bound.Edge.Role)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}

		groups[key] = append(groups[key], bound)
	}

	return groups, order
}

// groupConflict gives the evidence-conflict result of a group of facts that disagree about a property, or nil.
func (r *obligationRun) groupConflict(anchor string, group []*BoundEdge) *OccurrenceResult {
	if len(group) < 2 {
		return nil
	}

	name, ok := disagreement(group)
	if !ok {
		return nil
	}

	sources := make([]Location, len(group))
	for i, bound := range group {
		sources[i] = edgeLocation(bound)
	}

	slices.SortFunc(sources, compareLocations)

	occ := r.occurrenceOf(anchor, group[0], OccurrenceConflict, VerdictUnknown, ReasonEvidenceConflict,
		fmt.Sprintf("%d facts of the role %s disagree about the property %s: %s; no source wins", len(group), roleName(group[0].Edge.Role), name, locationList(sources)))
	occ.Sources = sources

	return &occ
}

// disagreement gives the first property (sorted by name) that two facts of the group state with different values.
func disagreement(group []*BoundEdge) (string, bool) {
	for _, name := range statedPropertyNames(group) {
		seen := map[string]struct{}{}

		for _, bound := range group {
			if value, ok := statedPropertyValue(bound.Edge, name); ok {
				seen[value] = struct{}{}
			}
		}

		if len(seen) > 1 {
			return name, true
		}
	}

	return "", false
}

// statedPropertyNames lists, sorted, the properties that any fact of the group states. A facet has the prefix `facet:`.
func statedPropertyNames(group []*BoundEdge) []string {
	names := map[string]struct{}{}

	for _, bound := range group {
		for k := range bound.Edge.Properties {
			names[k] = struct{}{}
		}

		for k := range bound.Edge.Facets {
			names["facet:"+k] = struct{}{}
		}
	}

	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}

	slices.SortFunc(sorted, canonjson.CompareUTF16)

	return sorted
}

// statedPropertyValue gives the value that a fact states for a property or, with the prefix `facet:`, a facet.
func statedPropertyValue(edge *observation.EvidenceEdge, name string) (string, bool) {
	if facet, isFacet := strings.CutPrefix(name, "facet:"); isFacet {
		value, ok := edge.Facets[facet]

		return value, ok
	}

	value, ok := edge.Properties[name]

	return value, ok
}

func locationList(locations []Location) string {
	parts := make([]string, len(locations))

	for i, l := range locations {
		parts[i] = l.Path
		if l.Line > 0 {
			parts[i] += fmt.Sprintf(":%d", l.Line)
		}
	}

	return strings.Join(parts, " and ")
}
