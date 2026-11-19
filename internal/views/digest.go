package views

import (
	"slices"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// DigestContract is the schema tag of the semantic digest (design D5). A change of what the digest covers, or of
// how it is calculated, is a new contract: a manifest with another contract is stale (`contractChanged`), and a
// prior verdict is never kept by dropping a field silently.
const DigestContract = "concordia-view-semantic/1"

// DefinitionVersion is the version of the view definition contract that the manifest records.
const DefinitionVersion = "concordia-view-definition/1"

// DigestInput is the input of ComputeDigests.
type DigestInput struct {
	View *Definition
	// Model is the architecture model: it gives the descendants of the depicted elements.
	Model *intent.ArchitectureModel
	// ModelProjection is required. ObservedProjection is nil when the view compares only against the declared
	// projection: its digest is then an explicit missing value.
	ModelProjection    *Projection
	ObservedProjection *Projection
	// Bindings, Controls and Policies are the full sets of the run. Only the slice that selects a depicted
	// subject enters the digest.
	Bindings []binding.Binding
	Controls []intent.ControlAttachment
	Policies []policy.RulePlan
}

// Digests are the input digests of a view and its semantic digest. Observed is empty when the observed
// projection is not part of the view (the manifest records that as a missing value, never as an empty digest).
type Digests struct {
	Definition, Model, Observed string
	Binding, Control, Policy    string
	Normalization               string
	// Semantic is the semantic digest: the digest of the canonical JSON of the others and of the contracts.
	Semantic string
}

// semanticDocument is what the semantic digest covers (design D5).
type semanticDocument struct {
	ObservedProjectionDigest *string `json:"observedProjectionDigest"`
	DigestContract           string  `json:"digestContract"`
	DefinitionDigest         string  `json:"definitionDigest"`
	ModelProjectionDigest    string  `json:"modelProjectionDigest"`
	BindingDigest            string  `json:"bindingDigest"`
	ControlDigest            string  `json:"controlDigest"`
	PolicyDigest             string  `json:"policyDigest"`
	NormalizationVersion     string  `json:"normalizationVersion"`
}

// ComputeDigests calculates the input digests and the semantic digest of a view. They cover the two projections
// and the view-scoped slices of bindings, controls and policies; they do not cover layout, colors, timestamps,
// function bodies, source lines, edge counts or any text that the view does not declare significant, so an
// internal refactor, a layout edit or an unrelated policy edit leaves them unchanged.
func ComputeDigests(input *DigestInput) (Digests, error) {
	subject := depictedSubject(input)
	elements := subjectWithDescendants(input.Model, subject.Elements)

	out := Digests{Normalization: observation.NormalizationVersion}

	for _, step := range inputDigestSteps(input, &out, subject, elements) {
		var err error
		if *step.dst, err = step.compute(); err != nil {
			return Digests{}, err
		}
	}

	data, err := canonjson.Marshal(semanticDocumentOf(&out))
	if err != nil {
		return Digests{}, err
	}

	out.Semantic = digest.OfBytes(data)

	return out, nil
}

// digestStep is the calculation of one input digest and the place where its result goes.
type digestStep struct {
	dst     *string
	compute func() (string, error)
}

// inputDigestSteps lists, in calculation order, the input digests that the view has. The observed projection
// has a step only when it is part of the view.
func inputDigestSteps(input *DigestInput, out *Digests, subject Subject, elements map[string]bool) []digestStep {
	steps := []digestStep{
		{&out.Definition, func() (string, error) { return DefinitionDigest(input.View) }},
		{&out.Model, func() (string, error) { return input.ModelProjection.Digest() }},
	}

	if input.ObservedProjection != nil {
		steps = append(steps, digestStep{&out.Observed, func() (string, error) { return input.ObservedProjection.Digest() }})
	}

	return append(steps,
		digestStep{&out.Binding, func() (string, error) { return BindingDigest(input.Bindings, elements) }},
		digestStep{&out.Control, func() (string, error) { return ControlDigest(input.Controls, subject) }},
		digestStep{&out.Policy, func() (string, error) { return PolicyDigest(input.Policies, subject) }},
	)
}

// semanticDocumentOf is the document that the semantic digest covers, built from the input digests.
func semanticDocumentOf(out *Digests) semanticDocument {
	doc := semanticDocument{
		DigestContract: DigestContract, DefinitionDigest: out.Definition, ModelProjectionDigest: out.Model,
		BindingDigest: out.Binding, ControlDigest: out.Control, PolicyDigest: out.Policy,
		NormalizationVersion: out.Normalization,
	}
	if out.Observed != "" {
		doc.ObservedProjectionDigest = &out.Observed
	}

	return doc
}

// depictedSubject is the subject that selects the slices: the elements of both projections, the scope and the
// necessary coverage, and the declared relationships that the view shows.
func depictedSubject(input *DigestInput) Subject {
	elements := map[string]struct{}{}
	relationships := map[string]struct{}{}

	add := func(target map[string]struct{}, ids []string) {
		for _, id := range ids {
			target[id] = struct{}{}
		}
	}

	add(elements, input.View.Scope)

	for _, p := range []*Projection{input.ModelProjection, input.ObservedProjection} {
		if p == nil {
			continue
		}

		add(elements, p.Depicts.Elements)
		add(elements, p.Coverage.Required.Elements)
		add(relationships, p.Depicts.Relationships)
		add(relationships, p.Coverage.Required.Relationships)
	}

	return Subject{Elements: sortedKeys(elements), Relationships: sortedKeys(relationships)}
}

// subjectWithDescendants adds the nodes that the elements compose (`composed-of`), at any depth: a binding of a
// component selects the container that shows it.
func subjectWithDescendants(model *intent.ArchitectureModel, elements []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range elements {
		out[id] = true
	}

	if model == nil {
		return out
	}

	for changed := true; changed; {
		changed = composeDescendants(model, out)
	}

	return out
}

// composeDescendants adds, in one pass, the targets of the `composed-of` relationships of the nodes in out. It
// says whether it added any node.
func composeDescendants(model *intent.ArchitectureModel, out map[string]bool) bool {
	changed := false

	for i := range model.Relationships {
		rel := &model.Relationships[i]
		if rel.Kind != intent.RelComposedOf || !out[rel.Container] {
			continue
		}

		for _, target := range rel.Targets {
			if !out[target] {
				out[target], changed = true, true
			}
		}
	}

	return changed
}

// definitionDocument is the part of a view definition that decides what the view shows and what it is
// compared with. The owner, the severity and the paths are not in it: they change who is told and where the
// files are, not the architecture.
type definitionDocument struct {
	Projection  definitionProjection `json:"projection"`
	Mapping     *Mapping             `json:"mapping"`
	Coverage    *Coverage            `json:"coverage"`
	Comparison  definitionComparison `json:"comparison"`
	ID          string               `json:"id"`
	Kind        string               `json:"kind"`
	Origin      string               `json:"origin"`
	Abstraction string               `json:"abstraction"`
	Environment *string              `json:"environment"`
	Scope       []string             `json:"scope"`
}

type definitionProjection struct {
	Merge         string   `json:"merge"`
	SelfEdges     string   `json:"selfEdges"`
	ParallelEdges string   `json:"parallelEdges"`
	EdgeKinds     []string `json:"edgeKinds"`
	Semantic      []string `json:"semantic"`
}

type definitionComparison struct {
	MinimumConfidence string   `json:"minimumConfidence"`
	Against           []string `json:"against"`
}

func sortedCoverage(c *Coverage) *Coverage {
	if c == nil {
		return nil
	}

	return &Coverage{Elements: sortedCopy(c.Elements), Relationships: sortedCopy(c.Relationships)}
}

// DefinitionDigest gives the digest of the effective definition of a view: the defaults are filled in, so an
// explicit default and an omitted one give the same digest.
func DefinitionDigest(view *Definition) (string, error) {
	doc := definitionDocument{
		ID: view.ID, Kind: string(view.Kind), Origin: string(view.Origin), Abstraction: string(view.Abstraction),
		Environment: stringPtr(view.Environment), Scope: sortedCopy(view.Scope), Mapping: view.Mapping, Coverage: sortedCoverage(view.Coverage),
		Projection: definitionProjection{
			Merge: "binding", SelfEdges: view.EffectiveSelfEdges(), ParallelEdges: view.EffectiveParallelEdges(),
			EdgeKinds: []string{}, Semantic: []string{},
		},
		Comparison: definitionComparison{Against: sortedCopy(view.EffectiveAgainst())},
	}

	if p := view.Projection; p != nil {
		if p.Merge != "" {
			doc.Projection.Merge = p.Merge
		}

		doc.Projection.EdgeKinds, doc.Projection.Semantic = sortedCopy(p.EdgeKinds), sortedCopy(p.Semantic)
	}

	if view.Comparison != nil {
		doc.Comparison.MinimumConfidence = view.Comparison.MinimumConfidence
	}

	return marshalDigest(doc)
}

// bindingDocument is a binding without its ID and its source: both depend on the place of the declaration, and a
// move of a declaration is no change of the binding.
type bindingDocument struct {
	Projection      *binding.Projection `json:"projection"`
	ArchitectureRef string              `json:"architectureRef"`
	Cardinality     string              `json:"cardinality"`
	Agreement       string              `json:"agreement"`
	Status          string              `json:"status"`
	Authority       string              `json:"authority"`
	Match           []binding.Match     `json:"match"`
	Exclusive       bool                `json:"exclusive"`
	Required        bool                `json:"required"`
}

// BindingDigest gives the digest of the bindings that name one of the elements (the depicted ones and the nodes
// that they compose), as canonical strings in sorted order.
func BindingDigest(bindings []binding.Binding, elements map[string]bool) (string, error) {
	var docs []string

	for i := range bindings {
		b := &bindings[i]
		if !elements[b.ArchitectureRef] {
			continue
		}

		text, err := canonjson.Marshal(bindingDocument{
			Projection: b.Projection, ArchitectureRef: b.ArchitectureRef, Cardinality: b.Cardinality, Agreement: b.Agreement,
			Status: b.Status, Authority: b.Authority, Match: nonNil(b.Match), Exclusive: b.Exclusive, Required: b.Required,
		})
		if err != nil {
			return "", err
		}

		docs = append(docs, string(text))
	}

	return digestOfSorted(docs)
}

// controlDocument is a control attachment without its place in the document.
type controlDocument struct {
	Config    *controlConfigDocument `json:"config"`
	ControlID string                 `json:"controlId"`
	Level     string                 `json:"level"`
	ScopeID   string                 `json:"scopeId"`
	Engine    string                 `json:"engine"`
}

type controlConfigDocument struct {
	MinimumPolicyCoverage *float64 `json:"minimumPolicyCoverage"`
	Engine                string   `json:"engine"`
	PolicyRef             string   `json:"policyRef"`
	UnknownDisposition    string   `json:"unknownDisposition"`
	RuleIDs               []string `json:"ruleIds"`
}

// ControlDigest gives the digest of the CALM controls that apply to the depicted subject: the controls of a
// depicted node or relationship, and the controls of the whole architecture (they apply to every subject).
func ControlDigest(controls []intent.ControlAttachment, subject Subject) (string, error) {
	var docs []string

	for i := range controls {
		control := &controls[i]
		if !controlApplies(control, subject) {
			continue
		}

		text, err := canonjson.Marshal(controlDocumentOf(control))
		if err != nil {
			return "", err
		}

		docs = append(docs, string(text))
	}

	return digestOfSorted(docs)
}

// controlApplies says whether a control attachment applies to the depicted subject.
func controlApplies(control *intent.ControlAttachment, subject Subject) bool {
	switch control.Scope.Level {
	case intent.ScopeArchitecture:
		return true
	case intent.ScopeNode:
		return slices.Contains(subject.Elements, control.Scope.ID)
	case intent.ScopeRelationship:
		return slices.Contains(subject.Relationships, control.Scope.ID)
	case intent.ScopeFlow:
		return false
	}

	return true
}

// controlDocumentOf is the canonical document of a control attachment.
func controlDocumentOf(control *intent.ControlAttachment) controlDocument {
	doc := controlDocument{
		ControlID: control.ControlID, Level: string(control.Scope.Level), ScopeID: control.Scope.ID, Engine: control.Engine,
	}

	if cfg := control.Config; cfg != nil {
		doc.Config = &controlConfigDocument{
			MinimumPolicyCoverage: cfg.MinimumPolicyCoverage, Engine: cfg.Engine, PolicyRef: cfg.PolicyRef,
			UnknownDisposition: cfg.UnknownDisposition, RuleIDs: nonNil(cfg.RuleIDs),
		}
	}

	return doc
}

// PolicyDigest gives the digest of the compiled rules that select a depicted element (as subject, target,
// layer member or obligation target) or whose scope is a depicted element. A rule that selects none of them
// is not in the digest, so an edit of it leaves the digest unchanged. The pack, the name and the texts of a rule
// are not in it: a rewording is no change of the rule.
func PolicyDigest(rules []policy.RulePlan, subject Subject) (string, error) {
	var docs []string

	for i := range rules {
		rule := rules[i]
		if !selects(&rule, subject.Elements) {
			continue
		}

		rule.Pack, rule.Name, rule.Remediation, rule.Rationale, rule.Owner = policy.PackRef{}, "", "", "", ""

		text, err := canonjson.Marshal(rule)
		if err != nil {
			return "", err
		}

		docs = append(docs, string(text))
	}

	return digestOfSorted(docs)
}

// selects says whether a rule names one of the elements.
func selects(rule *policy.RulePlan, elements []string) bool {
	named := func(ids []string) bool {
		return slices.ContainsFunc(ids, func(id string) bool { return slices.Contains(elements, id) })
	}

	if named(rule.Subjects) || named(rule.Targets) {
		return true
	}

	for _, layer := range rule.Layers {
		if named(layer) {
			return true
		}
	}

	return rule.Obligation != nil && named(rule.Obligation.Relation.Target)
}

// digestOfSorted gives the digest of a list of canonical documents in sorted order. No document is a missing
// value: the digest of no document is the digest of the empty list.
func digestOfSorted(docs []string) (string, error) {
	slices.SortFunc(docs, canonjson.CompareUTF16)

	return marshalDigest(nonNil(docs))
}

func marshalDigest(v any) (string, error) {
	data, err := canonjson.Marshal(v)
	if err != nil {
		return "", err
	}

	return digest.OfBytes(data), nil
}
