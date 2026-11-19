package policy

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

// Rule compilation: the plan of one rule.

var aggregations = []string{AggregationEdge, AggregationFile, AggregationSymbol, AggregationArchitecturePair}

func (c *compiler) compileRule(rule *rawRule, index int) (RulePlan, *diag.Diagnostic) {
	base := "/rules/" + strconv.Itoa(index)

	built, aggregation, problem := c.checkedShape(rule, base)
	if problem != nil {
		return RulePlan{}, problem
	}

	requirements, problem := c.evidenceFor(rule, base, built.edgeKinds)
	if problem != nil {
		return RulePlan{}, problem
	}

	unsupported := c.unsupportedKinds(built.edgeKinds)

	for _, kind := range unsupported {
		*c.diagnostics = append(*c.diagnostics,
			diag.New(diag.POL009, diag.Params{"kind": string(kind), "ruleId": rule.ID}, c.at(base)))
	}

	plan := RulePlan{
		RuleID:               rule.ID,
		Name:                 rule.Name,
		Severity:             rule.Severity,
		Pack:                 c.pack,
		Subjects:             nonNil(built.subjects),
		Targets:              nonNil(built.targets),
		Layers:               built.layers,
		Traversal:            traversalOf(&built),
		Predicate:            built.predicate,
		EvidenceRequirements: requirements,
		UnsupportedEdgeKinds: unsupported,
		Aggregation:          aggregation,
		Rationale:            rule.Rationale,
		Remediation:          rule.Remediation,
		Owner:                rule.Owner,
		Family:               rule.Family,
		Enforcement:          rule.Enforcement,
		Obligation:           built.obligation,
	}

	return plan, nil
}

// checkedShape compiles the predicate of the rule and checks the settings that go with it: the aggregation
// and the coverage names of the evidence. It gives the shape and the aggregation of the rule.
func (c *compiler) checkedShape(rule *rawRule, base string) (shape, string, *diag.Diagnostic) {
	aggregation, problem := c.aggregationOf(rule, base)
	if problem != nil {
		return shape{}, "", problem
	}

	if rule.Evidence.setsCoverageTwice() {
		d := diag.New(diag.POL001,
			diag.Detail("evidence.minimumCoverage and evidence.coverage.files both set; they are two names for one value"),
			c.at(base+"/evidence"))

		return shape{}, "", &d
	}

	built, problem := c.shapeOf(rule, base)
	if problem != nil {
		return shape{}, "", problem
	}

	if aggregation != AggregationEdge && !built.predicate.Family.EdgeFindings() {
		d := diag.New(diag.POL002,
			diag.Detail("aggregation '"+aggregation+"' applies only to rules with edge findings"), c.at(base+"/aggregation"))

		return shape{}, "", &d
	}

	return built, aggregation, nil
}

// aggregationOf gives the aggregation that the rule names, or `edge` when it names none.
func (c *compiler) aggregationOf(rule *rawRule, base string) (string, *diag.Diagnostic) {
	if rule.Aggregation == nil {
		return AggregationEdge, nil
	}

	if !slices.Contains(aggregations, *rule.Aggregation) {
		problem := diag.New(diag.POL002,
			diag.Detail("aggregation '"+*rule.Aggregation+"' is not supported; use edge, file, symbol or architecture-pair"),
			c.at(base+"/aggregation"))

		return "", &problem
	}

	return *rule.Aggregation, nil
}

// unsupportedKinds gives the edge kinds that the provider does not supply. It is never nil.
func (c *compiler) unsupportedKinds(kinds []observation.CanonicalEdgeKind) []observation.CanonicalEdgeKind {
	unsupported := []observation.CanonicalEdgeKind{}

	for _, kind := range kinds {
		if !c.kindSupported(kind) {
			unsupported = append(unsupported, kind)
		}
	}

	return unsupported
}

// traversalOf gives the traversal of a shape: one step outbound unless the shape says otherwise.
func traversalOf(built *shape) Traversal {
	traversal := Traversal{
		EdgeKinds: nonNil(built.edgeKinds), MinimumDepth: 1, MaximumDepth: 1, Direction: directionOutbound,
	}

	if built.maxDepth > 0 {
		traversal.MaximumDepth = built.maxDepth
	}

	if built.direction != "" {
		traversal.Direction = built.direction
	}

	return traversal
}

// kindSupported says whether the observation provider supplies evidence of a kind: the
// adapter has a normalization for it and, when the provider reports family support, the
// capability profile does not mark the family of the kind as unsupported. A kind whose
// family is supported or partial (for example `deploys-artifact` in profile `cbm-0.12.1`)
// is supported; "not present in this graph" is a coverage matter, not a support matter.
func (c *compiler) kindSupported(kind observation.CanonicalEdgeKind) bool {
	if !slices.Contains(c.caps.SupportedCanonicalKinds, kind) {
		return false
	}

	family, ok := observation.FamilyForKind(kind)
	if !ok {
		return true
	}

	state, reported := c.caps.Families[family]

	return !reported || state.Level != observation.SupportUnsupported
}

// edgeKindConfidence resolves `evidence.edgeKinds`. A key must be an edge kind that
// the rule traverses.
func (c *compiler) edgeKindConfidence(tokens map[string]observation.Confidence, ruleKinds []observation.CanonicalEdgeKind, base string) (map[observation.CanonicalEdgeKind]observation.Confidence, *diag.Diagnostic) {
	if len(tokens) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(tokens))
	for key := range tokens {
		keys = append(keys, key)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	out := make(map[observation.CanonicalEdgeKind]observation.Confidence, len(keys))

	for _, key := range keys {
		kinds, problem := c.resolveEdges([]string{key}, base+"/evidence/edgeKinds")
		if problem != nil {
			return nil, problem
		}

		if !slices.Contains(ruleKinds, kinds[0]) {
			d := diag.New(diag.POL001,
				diag.Detail("evidence.edgeKinds names "+key+", which the rule does not traverse"), c.at(base+"/evidence/edgeKinds"))

			return nil, &d
		}

		out[kinds[0]] = tokens[key]
	}

	return out, nil
}

func firstCoverage(values ...*float64) float64 {
	for _, v := range values {
		if v != nil {
			return *v
		}
	}

	return defaultCoverage
}

// ruleEvidenceOf gives the `evidence` of the rule and its `coverage` block. Both are empty when the rule has none.
func ruleEvidenceOf(rule *rawRule) (rawEvidence, rawCoverage) {
	if rule.Evidence == nil {
		return rawEvidence{}, rawCoverage{}
	}

	if rule.Evidence.Coverage == nil {
		return *rule.Evidence, rawCoverage{}
	}

	return *rule.Evidence, *rule.Evidence.Coverage
}

// controlSettingsOf gives the minimum coverage and the unknown disposition that the controls set for a rule.
func (c *compiler) controlSettingsOf(ruleID string) (*float64, UnknownDisposition) {
	ctl, ok := c.controls[ruleID]
	if !ok {
		return nil, ""
	}

	return ctl.MinimumCoverage, ctl.OnUnknown
}

// evidenceFor applies the precedence of the evidence settings (highest first):
// CLI flag, control configuration, rule `evidence`, workspace `check`, default.
// The workspace rule overrides are already in the rule `evidence`. The edge kinds,
// bytes and supported languages have no other source.
func (c *compiler) evidenceFor(rule *rawRule, base string, ruleKinds []observation.CanonicalEdgeKind) (EvidenceRequirements, *diag.Diagnostic) {
	ruleEv, coverage := ruleEvidenceOf(rule)
	ctlCoverage, ctlUnknown := c.controlSettingsOf(rule.ID)

	perKind, problem := c.edgeKindConfidence(ruleEv.EdgeKinds, ruleKinds, base)
	if problem != nil {
		return EvidenceRequirements{}, problem
	}

	return EvidenceRequirements{
		MinimumConfidence: cmp.Or(c.overrides.CLI.MinimumConfidence, ruleEv.MinimumConfidence,
			c.overrides.Workspace.MinimumConfidence, observation.ConfidenceSyntactic),
		MinimumCoverage: firstCoverage(
			c.overrides.CLI.MinimumCoverage, ctlCoverage, ruleEv.MinimumCoverage, coverage.Files, c.overrides.Workspace.MinimumCoverage),
		OnUnknown:               cmp.Or(c.overrides.CLI.OnUnknown, ctlUnknown, ruleEv.OnUnknown, c.overrides.Workspace.OnUnknown, UnknownFail),
		EdgeKindConfidence:      perKind,
		MinimumByteCoverage:     coverage.Bytes,
		MinimumLanguageCoverage: coverage.SupportedLanguages,
	}, nil
}
