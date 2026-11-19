package conformance

import (
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// infrastructurePackID is the ID of the built-in pack with the placement and trace rules. The trace of a subject
// lists the obligation results of its rules.
const infrastructurePackID = "infrastructure"

// traceInput gathers what the deployment trace reads: the normalized evidence, the binding set, the model, the
// declared and selected environments, the coverage records, and the obligation results of the `infrastructure` rules.
func (c *checker) traceInput(
	wspace *workspace.Workspace, inp *inputs, result *observation.EvidenceQueryResult, bindingSet *binding.Set, rules []RuleResult,
) *deployment.Input {
	declared := make([]deployment.Environment, 0, len(wspace.Environments))
	for i := range wspace.Environments {
		declared = append(declared, deployment.Environment{ID: wspace.Environments[i].ID, Cluster: wspace.Environments[i].Cluster})
	}

	var model *intent.ArchitectureModel
	if inp != nil {
		model = inp.model
	}

	input := &deployment.Input{
		Model: model, Evidence: result, BindingSet: bindingSet, Bindings: inp.bindings.Bindings, Declared: declared,
		Selected: c.environments.Selected, Coverage: c.runCov.Predicates, Rules: ruleLinks(rules),
	}

	if c.assurance != nil {
		input.Lineage = c.assurance.lineageStatus
		input.Evaluated = c.assurance.evaluatedEnvironments(bindingSet)
	}

	return input
}

// ruleLinks gives the obligation results of the `infrastructure` pack, one link for each obligation.
func ruleLinks(rules []RuleResult) []deployment.RuleLink {
	var links []deployment.RuleLink

	for i := range rules {
		if rules[i].Policy.ID != infrastructurePackID {
			continue
		}

		for j := range rules[i].Obligations {
			o := &rules[i].Obligations[j]
			links = append(links, deployment.RuleLink{
				RuleID: o.RuleID, ObligationID: o.ID, Verdict: string(o.Verdict), Subject: o.Subject, Environment: o.Environment,
			})
		}
	}

	return links
}

// areaOfTrace fills `areas.deploymentTrace` from the summary of the trace: the counts `subjects`, `complete`,
// `partial`, `unresolved`, and `unknown`, and the worst of `pass` (all complete), `unknown` (a partial or unknown
// trace), and `violation` (an unresolved trace). Without a declared environment the area is `not-configured`.
// The area never changes an exit code.
func areaOfTrace(trace *deployment.Trace) AreaResult {
	if !trace.Configured() {
		return AreaResult{Counts: map[string]int{}, Verdict: AreaNotConfigured}
	}

	by := trace.Summary.ByStatus
	counts := map[string]int{"subjects": trace.Summary.Subjects}

	for _, status := range deployment.Statuses() {
		counts[string(status)] = by[status]
	}

	switch {
	case trace.Summary.Subjects == 0:
		return AreaResult{Counts: counts, Verdict: AreaVerdict(VerdictNotApplicable)}
	case counts[string(deployment.StatusUnresolved)] > 0:
		return AreaResult{Counts: counts, Verdict: AreaVerdict(VerdictViolation)}
	case counts[string(deployment.StatusPartial)]+counts[string(deployment.StatusUnknown)] > 0:
		return AreaResult{Counts: counts, Verdict: AreaVerdict(VerdictUnknown)}
	default:
		return AreaResult{Counts: counts, Verdict: AreaVerdict(VerdictPass)}
	}
}
