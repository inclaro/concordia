package policy

import (
	"slices"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
)

// expectedOutcomes are the values of `require.effectiveAccess.expect` for each dimension.
var expectedOutcomes = map[string][]string{
	DimensionNetwork:       {ExpectDenied, ExpectAllowed},
	DimensionAuthorization: {ExpectNotGranted, ExpectGranted},
}

// knownCheckIDs are the check identifiers that a rule may require: the `check-ids` of the controls of the
// architecture and the identifiers that the caller found in the result sources.
func knownCheckIDs(model *intent.ArchitectureModel, fromSources []string) map[string]struct{} {
	out := map[string]struct{}{}

	for _, id := range fromSources {
		out[id] = struct{}{}
	}

	for i := range model.Controls {
		if cfg := model.Controls[i].Config; cfg != nil {
			for _, id := range cfg.CheckIDs {
				out[id] = struct{}{}
			}
		}
	}

	return out
}

// effectiveAccessShape compiles `require.effectiveAccess`. The dimension and the expected outcome are checked here
// rather than by the schema so that an unknown value gives CO-POL-002 with the list of supported values. The `from`
// selector gives the subjects and the `to` selector the targets.
func (c *compiler) effectiveAccessShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	clause := rule.Require.EffectiveAccess
	pointer := base + "/require/effectiveAccess"

	outcomes, ok := expectedOutcomes[clause.Dimension]
	if !ok {
		d := diag.New(diag.POL002,
			diag.Detail("effective access dimension '"+clause.Dimension+"' is not supported; use "+DimensionNetwork+" or "+DimensionAuthorization),
			c.at(pointer+"/dimension"))

		return shape{}, &d
	}

	if !slices.Contains(outcomes, clause.Expect) {
		d := diag.New(diag.POL002,
			diag.Detail("expected outcome '"+clause.Expect+"' is not valid for dimension "+clause.Dimension+"; use "+outcomes[0]+" or "+outcomes[1]),
			c.at(pointer+"/expect"))

		return shape{}, &d
	}

	if clause.Dimension != DimensionNetwork && len(clause.Ports) > 0 {
		d := diag.New(diag.POL002, diag.Detail("ports apply only to dimension "+DimensionNetwork), c.at(pointer+"/ports"))

		return shape{}, &d
	}

	from, problem := c.selectorAt(clause.From, pointer+"/from")
	if problem != nil {
		return shape{}, problem
	}

	to, problem := c.selectorAt(clause.To, pointer+"/to")
	if problem != nil {
		return shape{}, problem
	}

	ports := slices.Clone(clause.Ports)
	slices.Sort(ports)

	return shape{
		subjects: from,
		targets:  to,
		predicate: Predicate{
			Family: FamilyEffectiveAccess, Maturity: MaturityAdvisory,
			EffectiveAccess: &EffectiveAccess{Dimension: clause.Dimension, Expect: clause.Expect, Ports: ports},
		},
	}, nil
}

// checkResultShape compiles `require.checkResult`. A check that no control lists in `check-ids` and the caller did
// not report from a result source gives CO-POL-005: the rule could never be satisfied, so a typo must not pass as
// `unknown`. The optional `subject` selector gives the subjects.
func (c *compiler) checkResultShape(rule *rawRule, base string) (shape, *diag.Diagnostic) {
	clause := rule.Require.CheckResult
	pointer := base + "/require/checkResult"

	if _, known := c.checkIDs[clause.Check]; !known && !c.modelFree {
		d := diag.New(diag.POL005, diag.Params{idParam: clause.Check}, c.at(pointer+"/check"),
			diag.WithSuggestion("no control lists check '"+clause.Check+"' in check-ids and no result source defines it"))

		return shape{}, &d
	}

	subjects := []string{}

	if clause.Subject != nil {
		var problem *diag.Diagnostic

		if subjects, problem = c.selectorAt(clause.Subject, pointer+"/subject"); problem != nil {
			return shape{}, problem
		}
	}

	return shape{
		subjects: subjects,
		targets:  []string{},
		predicate: Predicate{
			Family: FamilyCheckResult, Maturity: MaturityAdvisory,
			CheckResult: &CheckResultRequirement{Check: clause.Check, Environment: clause.Environment},
		},
	}, nil
}

// claimShape compiles `require.claim`. The claim id is not checked against the claim registry here: the registry is
// read at evaluation time, so a claim that no loaded contract enables gives the verdict `unknown` with the reason
// `claim-not-enabled` that the specs require, not a compile error. The claim names its own subjects, so the rule has
// none.
func (c *compiler) claimShape(rule *rawRule, _ string) (shape, *diag.Diagnostic) {
	clause := rule.Require.Claim

	return shape{
		subjects: []string{},
		targets:  []string{},
		predicate: Predicate{
			Family: FamilyAssuranceClaim, Maturity: MaturityAdvisory,
			AssuranceClaim: &ClaimRequirement{ID: clause.ID, Environment: clause.Environment},
		},
	}, nil
}
