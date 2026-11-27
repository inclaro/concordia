package conformance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/evaluate/rbac"
	"github.com/inclaro/concordia/internal/assurance/render"
)

// The authorization dimension of `require.effectiveAccess`: the rbac evaluator and the answers of its queries.

// accessVerbs are the verbs that an authorization rule asks about for each target: the verbs by which an object can be
// read or changed. An element is accessible when any of them is granted.
var accessVerbs = []string{"get", "list", "watch", "create", "update", "patch", "delete"}

// serviceAccountOf gives the ServiceAccount that a rendered resource acts as: the account that a workload runs as, or
// the account itself.
func serviceAccountOf(resource *render.Resource) (rbac.Subject, bool) {
	switch {
	case resource.Workload != nil:
		return rbac.Subject{Kind: rbac.KindServiceAccount, Name: resource.Workload.ServiceAccountName, Namespace: resource.ID.Namespace}, true
	case resource.ID.Kind == "ServiceAccount":
		return rbac.Subject{Kind: rbac.KindServiceAccount, Name: resource.ID.Name, Namespace: resource.ID.Namespace}, true
	}

	return rbac.Subject{}, false
}

// authorization answers the rule for the rbac evaluator. The subject is the ServiceAccount of the `from` element and
// the object is each rendered resource that the `to` element is bound to (the `workload` binding names any kind, for
// example a Secret); the verbs are accessVerbs. The outcome is one for each pair of elements.
func (a *accessAsk) authorization(pairs [][2]string) []ExternalOutcome {
	evaluator := rbac.New(a.model.rbacInput())
	asker := &rbacAsker{ask: a, evaluator: evaluator, digest: evaluator.InputDigest()}

	outcomes := make([]ExternalOutcome, 0, len(pairs))

	for _, pair := range pairs {
		outcomes = append(outcomes, asker.pair(pair))
	}

	return outcomes
}

// rbacAsker holds the evaluator of one environment for the queries of one rule.
type rbacAsker struct {
	ask       *accessAsk
	evaluator *rbac.Evaluator
	digest    string
}

// pair asks the queries of one pair of elements and gives its outcome.
func (r *rbacAsker) pair(pair [2]string) ExternalOutcome {
	subjects := r.ask.subjectsOf(pair[0])
	if len(subjects) == 0 {
		return r.ask.bindingGap(pair, unboundElement{pair[0], "a rendered workload or ServiceAccount"})
	}

	targets := r.ask.model.resourcesOf(r.ask.run.bindings, pair[1])
	if len(targets) == 0 {
		return r.ask.bindingGap(pair, unboundElement{pair[1], "a rendered resource"})
	}

	var probes []probe

	for _, subject := range subjects {
		for _, target := range targets {
			for _, verb := range accessVerbs {
				probes = append(probes, r.query(subject, target, verb))
			}
		}
	}

	out := r.ask.baseOutcome(pair)
	out.Verdict, out.Reason, out.Detail = r.ask.judge(probes)

	return out
}

// subjectsOf gives the ServiceAccounts that the resources of a CALM element act as.
func (a *accessAsk) subjectsOf(calmID string) []rbac.Subject {
	var subjects []rbac.Subject

	for _, resource := range a.model.resourcesOf(a.run.bindings, calmID) {
		if subject, ok := serviceAccountOf(resource); ok {
			subjects = append(subjects, subject)
		}
	}

	return subjects
}

// query asks the evaluator whether the subject may do the verb to the object and records the answer.
func (r *rbacAsker) query(subject rbac.Subject, target *render.Resource, verb string) probe {
	request := rbac.Request{
		Verb: verb, APIGroup: target.ID.Group, Resource: pluralOf(target.ID.Kind), Name: target.ID.Name, Namespace: target.ID.Namespace,
	}
	result := r.evaluator.Evaluate(&rbac.Query{Request: request, Subject: subject})

	text := rbacQueryText(&request, subject)
	names := rbacEvidenceNames(&result)
	limits := append(slices.Clone(result.Limits), "the plural resource name is derived from the kind of the bound resource")

	r.ask.run.recordEvaluation(r.ask.entryOf(&answerFacts{
		query: text, verdict: string(result.Verdict), reason: result.Reason, policies: slices.Clone(names),
		assumptions: result.Assumptions, limits: limits, gaps: result.Gaps,
		evaluator: result.Evaluator, version: result.Version, digest: r.digest,
	}))

	found := probe{label: text, assumptions: result.Assumptions, evidence: strings.Join(slices.Compact(names), ", ")}

	switch result.Verdict {
	case rbac.Granted:
		found.state = stateOpen
	case rbac.NotGranted:
		found.state = stateClosed
	case rbac.Unknown:
		found.state, found.reason = stateUnknown, reasonOf(result.Reason)
		found.evidence = joinDetails(found.evidence, gapText(result.Gaps))
	}

	return found
}

// rbacQueryText names one rbac query inside the outcome.
func rbacQueryText(request *rbac.Request, subject rbac.Subject) string {
	where := "all namespaces"
	if request.Namespace != "" {
		where = request.Namespace
	}

	return fmt.Sprintf("%s %s/%s in %s for ServiceAccount %s/%s", request.Verb, request.Resource, request.Name, where, subject.Namespace, subject.Name)
}

// rbacEvidenceNames names the bindings and roles that decided an rbac answer, then the roles that are missing.
func rbacEvidenceNames(result *rbac.Result) []string {
	names := make([]string, 0, len(result.Grants)+len(result.Missing))

	for i := range result.Grants {
		grant := &result.Grants[i]
		names = append(names, fmt.Sprintf("%s %s grants %s %s", grant.Binding.Kind, refName(grant.Binding), grant.Role.Kind, refName(grant.Role)))
	}

	for _, missing := range result.Missing {
		names = append(names, fmt.Sprintf("missing %s %s", missing.Kind, refName(missing)))
	}

	return names
}

// refName is `namespace/name`, or the name of a cluster-scoped object.
func refName(ref rbac.Ref) string {
	if ref.Namespace == "" {
		return ref.Name
	}

	return ref.Namespace + "/" + ref.Name
}
