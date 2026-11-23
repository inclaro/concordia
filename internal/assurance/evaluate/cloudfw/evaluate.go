// Package cloudfw evaluates the network paths between cloud resources from the firewall rules of a Terraform or
// OpenTofu plan: AWS security groups, Azure network security groups, and GCP VPC firewall rules. It reads the planned
// values of the plan (package plan) and never runs a tool. A result is `allowed`, `denied`, or `unknown` with gaps.
// A `denied` result rests on the assumption that no rule for the resources exists outside the plan.
package cloudfw

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// Providers, selected by the prefix of the resource type.
const (
	providerAWS   = "aws"
	providerAzure = "azure"
	providerGCP   = "gcp"
)

// providerPrefixes maps a resource type prefix to its provider.
var providerPrefixes = []struct{ prefix, provider string }{
	{prefix: "aws_", provider: providerAWS},
	{prefix: "azurerm_", provider: providerAzure},
	{prefix: "google_", provider: providerGCP},
}

// providerOf is the provider of a resource type, or "" for a type of another provider.
func providerOf(typ string) string {
	for _, p := range providerPrefixes {
		if strings.HasPrefix(typ, p.prefix) {
			return p.provider
		}
	}

	return ""
}

// Evaluate answers one query.
func (e *Evaluator) Evaluate(q *Query) Result {
	res := e.evaluate(q)

	return finish(&res)
}

// evaluate applies the gates in order (plan coverage, endpoints, providers) and then asks the provider.
func (e *Evaluator) evaluate(q *Query) Result {
	if e.in.Coverage != assurance.CoverageComplete {
		return unknownResult(evaluate.ReasonCoverageIncomplete, assurance.Gap{
			Kind:   evaluate.GapIncompletePlan,
			Detail: fmt.Sprintf("the plans of the environment have coverage %q", coverageText(e.in.Coverage)),
		})
	}

	from, to, gaps := e.endpoints(q)
	if len(gaps) > 0 {
		return unknownResult(evaluate.ReasonSubjectNotFound, gaps...)
	}

	return e.dispatch(q, from, to)
}

// endpoints looks both ends of the query up in the input.
func (e *Evaluator) endpoints(q *Query) (from, to *plan.Resource, gaps []assurance.Gap) {
	from, to = e.resource(q.From.Address), e.resource(q.To.Address)

	for _, end := range []struct {
		res     *plan.Resource
		address string
	}{{from, q.From.Address}, {to, q.To.Address}} {
		if end.res == nil {
			gaps = append(gaps, assurance.Gap{
				Kind: evaluate.GapWorkloadMissing, Detail: fmt.Sprintf("%s is not a resource of the plan after apply", end.address),
			})
		}
	}

	return from, to, gaps
}

// dispatch hands the query to the provider of both ends.
func (e *Evaluator) dispatch(q *Query, from, to *plan.Resource) Result {
	fromProvider, toProvider := providerOf(from.Type), providerOf(to.Type)

	switch {
	case fromProvider == "" || toProvider == "":
		return unknownResult(evaluate.ReasonUnsupportedConstruct, unsupportedGap(from, to))
	case fromProvider != toProvider:
		return unknownResult(evaluate.ReasonUnsupportedConstruct, assurance.Gap{
			Kind: evaluate.GapMixedProviders,
			Detail: fmt.Sprintf("%s is a %s resource and %s is a %s resource",
				from.Address, fromProvider, to.Address, toProvider),
		})
	case fromProvider == providerAWS:
		return e.evaluateAWS(q, from, to)
	case fromProvider == providerAzure:
		return e.evaluateAzure(q, from, to)
	default:
		return e.evaluateGCP(q, from, to)
	}
}

// unsupportedGap names the end whose type no provider of the evaluator supports.
func unsupportedGap(from, to *plan.Resource) assurance.Gap {
	end := from
	if providerOf(from.Type) != "" {
		end = to
	}

	return unsupportedResource(end)
}

// unsupportedResource is the gap of an endpoint whose resource type the evaluator does not support.
func unsupportedResource(r *plan.Resource) assurance.Gap {
	return assurance.Gap{
		Kind: evaluate.GapUnsupportedResource, Detail: fmt.Sprintf("%s: resource type %s is not supported", r.Address, r.Type),
	}
}

func coverageText(c assurance.Coverage) string {
	if c == "" {
		return "not-stated"
	}

	return string(c)
}

// unknownResult is an `unknown` result with the reason and gaps.
func unknownResult(reason string, gaps ...assurance.Gap) Result {
	return Result{Verdict: VerdictUnknown, Reason: reason, Gaps: gaps}
}

// finish completes a result: the limits, the assumption of a `denied` result, sorted rules and gaps, and no reason
// unless the result is `unknown`. Applying it twice gives the same result.
func finish(res *Result) Result {
	res.Limits = slices.Clone(Limits)
	res.Assumptions = []string{}

	if res.Verdict == VerdictDenied {
		res.Assumptions = []string{assumptionComplete}
	}

	if res.Verdict != VerdictUnknown {
		res.Reason = ""
	}

	res.Rules = sortRules(res.Rules)
	res.Gaps = sortGaps(res.Gaps)

	return *res
}
