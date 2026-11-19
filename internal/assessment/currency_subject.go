package assessment

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/impact"
)

// subject is the current dependency subject of one assessment version: the sealed items that its declared
// selectors match now, and what could not be compared.
type subject struct {
	origins     map[itemKey][]string
	excluded    map[impact.Kind]bool
	sealed      []impact.Sealed
	causes      []UnknownCause
	assumptions []string
	// evidence are the evidence refs of the assessment with their digests: the authored digest, else the digest
	// of the file at the revision.
	evidence []Ref
	widened  bool
}

// The selector facets that have a field of impact.Selector instead of a facet restriction.
const (
	facetCalmNodes     = "calmNodes"
	facetIdentity      = "identity"
	facetIdentities    = "identities"
	facetEnvironment   = "environment"
	facetCalmRelations = "calmRelationships"
)

// itemKey is the key of an item: its kind and identity.
type itemKey struct {
	kind     impact.Kind
	identity string
}

// entries gives the current entries of the subject without the excluded kinds.
func (s *subject) entries() []impact.Entry {
	out := make([]impact.Entry, 0, len(s.sealed))

	for i := range s.sealed {
		if !s.excluded[s.sealed[i].Item.Kind] {
			out = append(out, s.sealed[i].Entry())
		}
	}

	return out
}

// cause records one cause once.
func (s *subject) cause(reason, detail string) {
	c := UnknownCause{Reason: reason, Detail: detail}
	if !slices.Contains(s.causes, c) {
		s.causes = append(s.causes, c)
	}
}

// closedReason maps a reason of a fact source to the closed set of review obligations. A reason outside the set
// (binding-gap, evidence-conflict) is coverage-incomplete: the spec lists exactly seven reasons.
func closedReason(reason string) string {
	if slices.Contains(unknownOrder, reason) {
		return reason
	}

	return ReasonCoverageIncomplete
}

// selectorOf turns a declared selector into a fact-source selector. The facets calmNodes, identity, identities,
// and environment have their own fields; every other facet restricts by value.
func selectorOf(d Dependency, environment string) impact.Selector {
	sel := impact.Selector{Kind: impact.Kind(d.Kind), Environment: environment}

	for facet, values := range d.Selector {
		values = sortedValues(values)

		switch facet {
		case facetCalmNodes:
			sel.CalmNodes = values
		case facetIdentity, facetIdentities:
			sel.Identities = append(sel.Identities, values...)
		case facetEnvironment:
			if len(values) == 1 {
				sel.Environment = values[0]
			}
		default:
			if sel.Match == nil {
				sel.Match = map[string][]string{}
			}

			sel.Match[facet] = values
		}
	}

	slices.Sort(sel.Identities)

	return sel
}

// items asks the fact source, once per distinct selector.
func (e *evaluator) items(ctx context.Context, sel *impact.Selector) (impact.Match, error) {
	data, err := json.Marshal(sel)
	if err != nil {
		return impact.Match{}, fmt.Errorf("encode the selector: %w", err)
	}

	key := string(data)
	if m, ok := e.matches[key]; ok {
		return m, nil
	}

	m, err := e.in.Facts.Items(ctx, *sel)
	if err != nil {
		return impact.Match{}, fmt.Errorf("select %s items: %w", sel.Kind, err)
	}

	e.matches[key] = m

	return m, nil
}

// resolution is the state of resolving the declared selectors of one assessment version.
type resolution struct {
	subj *subject
	// found are the matched items by key.
	found map[itemKey]impact.Item
	// unresolved are the kinds with a selector that depends on an unresolved binding.
	unresolved map[impact.Kind]bool
}

// add records a matched item and the node that brings it into the closure.
func (r *resolution) add(item *impact.Item, from string) {
	key := itemKey{item.Kind, item.Identity}
	if _, ok := r.found[key]; !ok {
		r.found[key] = *item
	}

	origin := strings.TrimPrefix(from, "assumption:")
	if from == "subject" || from == "scenario" {
		origin = ""
	}

	if !slices.Contains(r.subj.origins[key], origin) {
		r.subj.origins[key] = append(r.subj.origins[key], origin)
	}
}

// resolve computes the current subject of an assessment version: every declared selector (the subject's own, the
// scenarios, and those of the listed assumptions) is asked of the fact source and the matched items are sealed.
func (e *evaluator) resolve(ctx context.Context, doc *Assessment) (*subject, error) {
	res := &resolution{
		subj:  &subject{origins: map[itemKey][]string{}, excluded: map[impact.Kind]bool{}, assumptions: slices.Clone(doc.Assumptions)},
		found: map[itemKey]impact.Item{}, unresolved: map[impact.Kind]bool{},
	}

	for _, d := range e.set.Declared(doc) {
		if err := e.selectOne(ctx, doc, d, res); err != nil {
			return nil, err
		}
	}

	if err := e.widen(ctx, doc, res); err != nil {
		return nil, err
	}

	items := make([]impact.Item, 0, len(res.found))
	for _, it := range res.found {
		items = append(items, it)
	}

	sealed, err := e.sealAll(ctx, items)
	if err != nil {
		return nil, fmt.Errorf("seal the items of %s: %w", doc.Key(), err)
	}

	res.subj.sealed = sealed

	if err := e.resolveEvidence(ctx, doc, res.subj); err != nil {
		return nil, err
	}

	return res.subj, nil
}

// selectOne asks the fact source for one declared selector and records what came back.
func (e *evaluator) selectOne(ctx context.Context, doc *Assessment, decl DeclaredDependency, res *resolution) error {
	kind := impact.Kind(decl.Kind)
	subj := res.subj

	if e.in.Facts == nil {
		subj.excluded[kind] = true
		subj.cause(ReasonCoverageIncomplete, unknownReasonDetailNoFacts)

		return nil
	}

	sel := selectorOf(decl.Dependency, doc.Environment)

	match, err := e.items(ctx, &sel)
	if err != nil {
		return err
	}

	if match.State == impact.MatchUnsupported {
		e.recordUnsupported(doc, kind, &match, subj)

		return nil
	}

	for i := range match.Items {
		res.add(&match.Items[i], decl.From)
	}

	res.recordUntold(kind, &match)

	return nil
}

// recordUnsupported excludes a kind that the capability profile does not map and reports it.
func (e *evaluator) recordUnsupported(doc *Assessment, kind impact.Kind, match *impact.Match, subj *subject) {
	subj.excluded[kind] = true
	profile := cmp.Or(match.Profile, e.profile.ID)
	subj.cause(ReasonUnsupportedFamily, cmp.Or(match.Detail, fmt.Sprintf("dependency kind %s is not mapped by the capability profile %s", kind, profile)))
	e.note(diag.SEC002, diag.Detail(fmt.Sprintf("%s of %s is not mapped by the capability profile %s", kind, doc.Key(), profile)))
}

// recordUntold records what a match that told no items means for the kind: a binding gap is widened later, any
// other reason excludes the kind and leaves a cause.
func (r *resolution) recordUntold(kind impact.Kind, match *impact.Match) {
	if matchComplete(match) {
		return
	}

	if match.Reason == reasonBindingGap {
		r.unresolved[kind] = true

		return
	}

	// A reference whose target declaration was removed leaves a baseline item without a current one. The kind stays
	// in the diff so that the item shows as removed: the missing edge is never read as no change (D5). The cause
	// still makes the result unknown when no trigger decides.
	if match.Reason != ReasonTargetNotDeclared {
		r.subj.excluded[kind] = true
	}

	r.subj.cause(closedReason(match.Reason), cmp.Or(match.Detail, fmt.Sprintf("%s items cannot be told (%s)", kind, cmp.Or(match.Reason, "no reason given"))))
}

// widen handles the selectors that depend on an unresolved binding (SEC-07): the closure becomes the full declared
// scope of the assessment for the kind. When that scope cannot be computed either, the kind is excluded and the
// currency is `unknown` with a reason.
func (e *evaluator) widen(ctx context.Context, doc *Assessment, res *resolution) error {
	kinds := make([]impact.Kind, 0, len(res.unresolved))
	for kind := range res.unresolved {
		kinds = append(kinds, kind)
	}

	slices.Sort(kinds)

	for _, kind := range kinds {
		if err := e.widenKind(ctx, doc, kind, res); err != nil {
			return err
		}
	}

	return nil
}

// widenKind widens one kind to the declared scope of the assessment.
func (e *evaluator) widenKind(ctx context.Context, doc *Assessment, kind impact.Kind, res *resolution) error {
	subj := res.subj
	detail := fmt.Sprintf("a selector of %s depends on an unresolved binding", kind)

	if len(doc.Scope.CalmNodes) == 0 {
		subj.excluded[kind] = true
		subj.cause(ReasonCoverageIncomplete, detail+", and the assessment declares no CALM node to widen to")

		return nil
	}

	scope := impact.Selector{Kind: kind, Environment: doc.Environment, CalmNodes: sortedValues(doc.Scope.CalmNodes)}

	match, err := e.items(ctx, &scope)
	if err != nil {
		return err
	}

	if !matchComplete(&match) {
		subj.excluded[kind] = true
		subj.cause(closedReason(match.Reason), detail+", and the declared scope cannot be computed")

		return nil
	}

	subj.widened = true

	for i := range match.Items {
		if _, ok := res.found[itemKey{match.Items[i].Kind, match.Items[i].Identity}]; !ok {
			res.add(&match.Items[i], "scope")
		}
	}

	return nil
}

// matchComplete reports whether the fact source told the items of a selector, with or without a hit.
func matchComplete(match *impact.Match) bool {
	return match.State == impact.MatchFound || match.State == impact.MatchEmpty
}

// sealAll seals the items with the current contract and the file digests at the revision.
func (e *evaluator) sealAll(ctx context.Context, items []impact.Item) ([]impact.Sealed, error) {
	if len(items) == 0 {
		return []impact.Sealed{}, nil
	}

	files := func(string) (string, bool, error) { return "", false, nil }
	if e.in.Facts != nil {
		files = impact.FileDigesterAt(ctx, e.in.Facts, e.in.Revision)
	}

	sealed, err := e.contract.SealAllWith(items, files, e.digester())
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}

	return sealed, nil
}

// resolveEvidence gives each evidence entry its digest: the one the document authors, else the digest of the file
// that Ref names at the revision. A path that does not exist is `reference-unresolved`.
func (e *evaluator) resolveEvidence(ctx context.Context, doc *Assessment, subj *subject) error {
	subj.evidence = make([]Ref, 0, len(doc.Evidence))

	for _, entry := range doc.Evidence {
		sum := entry.Digest

		if sum == "" && e.in.Facts != nil {
			found, ok, err := e.in.Facts.FileDigest(ctx, e.in.Revision, entry.Ref)
			if err != nil {
				return fmt.Errorf("digest the evidence %s of %s: %w", entry.ID, doc.Key(), err)
			}

			if ok {
				sum = found
			} else {
				subj.cause(ReasonReferenceUnresolved, fmt.Sprintf("evidence %s of %s: path not found at the revision: %s", entry.ID, doc.Key(), entry.Ref))
			}
		}

		subj.evidence = append(subj.evidence, Ref{ID: entry.ID, Digest: sum})
	}

	return nil
}

// choice is the decision record that decides the currency of one version.
type choice struct {
	review Review
	// approval is the verified approval that applies, nil when none does.
	approval *Approval
	// rejected is a verified rejection that decides instead of an approval.
	rejected *Approval
	// basis is the approval of an earlier version that the comparison uses when the version has none.
	basis *Approval
	// unknown lists the records of the assessment that verification could not judge.
	unknown []UnknownCause
}

// choose picks the decision record of a version from the verified records (design D8, step 2).
func (e *evaluator) choose(doc *Assessment) *choice {
	picked := &choice{review: ReviewOf(doc, e.verified.Approvals, e.verified.Revocations)}

	switch {
	case picked.review.Approval == nil:
	case picked.review.Approval.Decision == DecisionRejected:
		picked.rejected = picked.review.Approval
	default:
		picked.approval = picked.review.Approval
	}

	if picked.approval == nil {
		for _, earlier := range slices.Backward(e.set.Versions(doc.ID)) {
			if earlier.Version >= doc.Version {
				continue
			}

			r := ReviewOf(earlier, e.verified.Approvals, e.verified.Revocations)
			if r.State == StateApproved && r.Approval != nil {
				picked.basis = r.Approval

				break
			}
		}
	}

	picked.unknown = e.unjudged(doc.ID)

	return picked
}

// unjudged lists the causes of the records of the assessment id that verification could not judge.
func (e *evaluator) unjudged(id string) []UnknownCause {
	ids := e.recordIDsOf(id)

	var out []UnknownCause

	for _, v := range e.verified.Results {
		if !ids[v.RecordID] || !unjudgedResult(&v) {
			continue
		}

		c := UnknownCause{Reason: v.Reason, Detail: cmp.Or(v.Detail, "record "+v.RecordID+" could not be verified")}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}

	return out
}

// recordIDsOf gives the ids of the approvals of the assessment id and of the revocations of those approvals.
func (e *evaluator) recordIDsOf(id string) map[string]bool {
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

// unjudgedResult reports whether a verification result is one that could not be judged.
func unjudgedResult(v *Verification) bool {
	if v.Applied {
		return false
	}

	return v.Reason == ReasonTrustAnchorUnreadable || v.Reason == ReasonVerificationUnavailable
}

// baseline gives the entries that the comparison starts from: the dependencies of the approval, else of the basis
// approval, else none (SEC-04: no approval means an empty basis and the whole subject counts as new).
func (c *choice) baseline() []Item {
	switch {
	case c.approval != nil:
		return c.approval.Subject.Dependencies
	case c.basis != nil:
		return c.basis.Subject.Dependencies
	default:
		return nil
	}
}

// addToGraph registers an assessment version, the assumptions it lists, and the items they select, plus the
// items of its baseline that no longer exist. A removed item then still reaches its assessment (SEC-05).
func (e *evaluator) addToGraph(doc *Assessment) {
	subj := e.subjects[doc.Key()]
	node := nodeOf(doc)

	e.graph.AddNode(node, impact.NodeAssessment)

	for _, id := range subj.assumptions {
		asm := impact.NodeID{Identity: id}
		e.graph.AddNode(asm, impact.NodeAssumption)
		e.graph.AddDependency(node, asm, impact.EdgeDependsOn)
	}

	current := e.addCurrentItems(node, subj)

	for _, it := range e.choices[doc.Key()].baseline() {
		k := itemKey{impact.Kind(it.Kind), it.Identity}
		if !current[k] {
			e.graph.AddDependency(node, impact.ItemNodeID(k.kind, k.identity), impact.EdgeDependsOn)
		}
	}
}

// addCurrentItems links every current item of the subject to the nodes that bring it in and gives the keys of
// the items.
func (e *evaluator) addCurrentItems(node impact.NodeID, subj *subject) map[itemKey]bool {
	current := map[itemKey]bool{}

	for i := range subj.sealed {
		sealed := &subj.sealed[i]
		k := itemKey{sealed.Item.Kind, sealed.Item.Identity}
		current[k] = true
		item := impact.ItemNodeID(k.kind, k.identity)

		for _, origin := range subj.origins[k] {
			e.graph.AddDependency(originNode(node, origin), item, impact.EdgeDependsOn)
		}
	}

	return current
}

// originNode gives the node that an item origin names: the assessment itself for its own selectors and the
// widened scope, else the assumption.
func originNode(assessment impact.NodeID, origin string) impact.NodeID {
	if origin == "" || origin == "scope" {
		return assessment
	}

	return impact.NodeID{Identity: origin}
}

// SubjectDigest gives the digest of a list of dependency items: `sha256:` of the canonical JSON of the items
// sorted by kind and identity. An approval binds it as `subject.digest`, and an approver computes the same value
// from the current subject. The identity is part of it, so a moved item changes it.
func SubjectDigest(items []Item) string {
	sorted := slices.Clone(items)
	if sorted == nil {
		sorted = []Item{}
	}

	slices.SortFunc(sorted, func(a, b Item) int {
		return cmp.Or(canonjson.CompareUTF16(string(a.Kind), string(b.Kind)), canonjson.CompareUTF16(a.Identity, b.Identity))
	})

	data, err := canonjson.Marshal(sorted)
	if err != nil {
		// Item holds strings only, so encoding cannot fail; an empty digest never matches a recorded one.
		return ""
	}

	return digest.OfBytes(data)
}

// ItemsOf gives the dependency items of an approval for the sealed items of a subject, skipping those whose
// digest cannot be told.
func ItemsOf(sealed []impact.Sealed) []Item {
	out := make([]Item, 0, len(sealed))

	for i := range sealed {
		if sealed[i].Digest == "" {
			continue
		}

		out = append(out, Item{Kind: DependencyKind(sealed[i].Item.Kind), Identity: sealed[i].Item.Identity, Digest: sealed[i].Digest})
	}

	return out
}

// entriesOf converts approval items into comparison entries, leaving out the excluded kinds.
func entriesOf(items []Item, excluded map[impact.Kind]bool) []impact.Entry {
	out := make([]impact.Entry, 0, len(items))

	for _, it := range items {
		kind := impact.Kind(it.Kind)
		if !excluded[kind] {
			out = append(out, impact.Entry{Kind: kind, Identity: it.Identity, Digest: it.Digest})
		}
	}

	return out
}

// sortedValues gives the values sorted in UTF-16 order without repeats. The input is not changed.
func sortedValues(values []string) []string {
	out := slices.Clone(values)
	slices.SortFunc(out, canonjson.CompareUTF16)

	return slices.Compact(out)
}
