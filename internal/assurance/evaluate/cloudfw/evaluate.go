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
func (e *Evaluator) evaluate(query *Query) Result {
	if e.in.Coverage != assurance.CoverageComplete {
		return unknownResult(evaluate.ReasonCoverageIncomplete, assurance.Gap{
			Kind:   evaluate.GapIncompletePlan,
			Detail: fmt.Sprintf("the plans of the environment have coverage %q", coverageText(e.in.Coverage)),
		})
	}

	source, dest, gaps := e.endpoints(query)
	if len(gaps) > 0 {
		return unknownResult(evaluate.ReasonSubjectNotFound, gaps...)
	}

	return e.dispatch(query, source, dest)
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
				Kind: evaluate.GapWorkloadMissing, Detail: end.address + " is not a resource of the plan after apply",
			})
		}
	}

	return from, to, gaps
}

// dispatch hands the query to the provider of both ends.
func (e *Evaluator) dispatch(query *Query, source, dest *plan.Resource) Result {
	sourceProvider, destProvider := providerOf(source.Type), providerOf(dest.Type)

	switch {
	case sourceProvider == "" || destProvider == "":
		return unknownResult(evaluate.ReasonUnsupportedConstruct, unsupportedGap(source, dest))
	case sourceProvider != destProvider:
		return unknownResult(evaluate.ReasonUnsupportedConstruct, assurance.Gap{
			Kind: evaluate.GapMixedProviders,
			Detail: fmt.Sprintf("%s is a %s resource and %s is a %s resource",
				source.Address, sourceProvider, dest.Address, destProvider),
		})
	case sourceProvider == providerAWS:
		return e.evaluateAWS(query, source, dest)
	case sourceProvider == providerAzure:
		return e.evaluateAzure(query, source, dest)
	default:
		return e.evaluateGCP(query, source, dest)
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
