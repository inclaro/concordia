package rbac

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/canonjson"
)

// bindingSite is one binding of the input: its reference, its namespace (empty for a ClusterRoleBinding), its
// subjects, and the role it refers to.
type bindingSite struct {
	ref       Ref
	namespace string
	roleRef   RoleRef
	subjects  []Subject
}

// resolvedRole is the role a binding refers to, with the rules that the role has.
type resolvedRole struct {
	ref   Ref
	rules []PolicyRule
}

// bind resolves one binding for the subjects that match and records grants, missing roles, and gaps.
func (e *Evaluator) bind(found *scanResult, req *Request, match func(Subject) bool, site *bindingSite) {
	matched := matchingSubjects(site.subjects, match)
	if len(matched) == 0 {
		return
	}

	role, ok := e.resolveRole(found, site)
	if !ok {
		return
	}

	for i := range role.rules {
		if !ruleGrants(&role.rules[i], req) {
			continue
		}

		for _, sub := range matched {
			found.grants = append(found.grants, Grant{Subject: sub, Binding: site.ref, Role: role.ref, Rule: i})
		}
	}
}

func matchingSubjects(subjects []Subject, match func(Subject) bool) []Subject {
	matched := make([]Subject, 0, len(subjects))

	for _, sub := range subjects {
		if match(sub) {
			matched = append(matched, sub)
		}
	}

	return matched
}

// resolveRole finds the role of a binding. When the binding cannot be resolved, the reason is recorded in found and
// ok is false.
func (e *Evaluator) resolveRole(found *scanResult, site *bindingSite) (role resolvedRole, ok bool) {
	switch {
	case site.roleRef.Kind == KindClusterRole:
		return e.resolveClusterRole(found, site)
	case site.roleRef.Kind == KindRole && site.namespace != "":
		return e.resolveNamespacedRole(found, site)
	}

	found.gaps = append(found.gaps, assurance.Gap{
		Kind: evaluate.GapInvalidRoleRef,
		Detail: fmt.Sprintf("%s has the role reference %s %q, which a binding of this kind cannot have",
			refText(site.ref), site.roleRef.Kind, site.roleRef.Name),
	})

	return resolvedRole{}, false
}

func (e *Evaluator) resolveClusterRole(found *scanResult, site *bindingSite) (resolvedRole, bool) {
	roleRef := Ref{Kind: KindClusterRole, Name: site.roleRef.Name}

	clusterRole, ok := e.clusterRoles[site.roleRef.Name]
	if !ok {
		found.recordMissingRole(roleRef, fmt.Sprintf("%s refers to ClusterRole %s, which is not in the rendered input",
			refText(site.ref), site.roleRef.Name))

		return resolvedRole{}, false
	}

	rules, gaps := e.effectiveRules(clusterRole, map[string]bool{})
	found.gaps = append(found.gaps, gaps...)

	return resolvedRole{ref: roleRef, rules: rules}, true
}

func (e *Evaluator) resolveNamespacedRole(found *scanResult, site *bindingSite) (resolvedRole, bool) {
	roleRef := Ref{Kind: KindRole, Namespace: site.namespace, Name: site.roleRef.Name}

	role, ok := e.roles[site.namespace+"/"+site.roleRef.Name]
	if !ok {
		found.recordMissingRole(roleRef, fmt.Sprintf("%s refers to Role %s/%s, which is not in the rendered input",
			refText(site.ref), site.namespace, site.roleRef.Name))

		return resolvedRole{}, false
	}

	return resolvedRole{ref: roleRef, rules: role.Rules}, true
}

// recordMissingRole notes a role that a binding refers to and the input does not have.
func (s *scanResult) recordMissingRole(role Ref, detail string) {
	s.missing = append(s.missing, role)
	s.gaps = append(s.gaps, assurance.Gap{Kind: evaluate.GapMissingRole, Detail: detail})
}

func refText(r Ref) string {
	if r.Namespace == "" {
		return r.Kind + " " + r.Name
	}

	return r.Kind + " " + r.Namespace + "/" + r.Name
}

// effectiveRules gives the rules of a ClusterRole. A ClusterRole with an aggregation rule has exactly the rules of
// the ClusterRoles that its selectors match: the control plane overwrites any rules written into it, so the rules in
// the document are not used. Aggregation can be nested; a cycle is cut and recorded as a gap.
func (e *Evaluator) effectiveRules(clusterRole *ClusterRole, visiting map[string]bool) ([]PolicyRule, []assurance.Gap) {
	if clusterRole.AggregationRule == nil {
		return clusterRole.Rules, nil
	}

	name := clusterRole.Metadata.Name
	if visiting[name] {
		return nil, []assurance.Gap{{
			Kind: evaluate.GapAggregationCycle, Detail: fmt.Sprintf("ClusterRole %s aggregates itself through its selectors", name),
		}}
	}

	visiting[name] = true
	defer delete(visiting, name)

	var (
		rules []PolicyRule
		gaps  []assurance.Gap
	)

	others := e.otherClusterRoles(name)

	for _, sel := range clusterRole.AggregationRule.ClusterRoleSelectors {
		selected, selGaps := e.aggregateBySelector(name, sel, others, visiting)
		rules = append(rules, selected...)
		gaps = append(gaps, selGaps...)
	}

	return rules, gaps
}

// otherClusterRoles lists the ClusterRoles except the named one, in name order so that rule indexes in grants are
// the same in every run.
func (e *Evaluator) otherClusterRoles(name string) []*ClusterRole {
	others := make([]*ClusterRole, 0, len(e.clusterRoles))

	for _, other := range e.clusterRoles {
		if other.Metadata.Name != name {
			others = append(others, other)
		}
	}

	slices.SortFunc(others, func(a, b *ClusterRole) int { return canonjson.CompareUTF16(a.Metadata.Name, b.Metadata.Name) })

	return others
}

// aggregateBySelector gives the effective rules of the ClusterRoles that one selector of the named ClusterRole
// matches. An invalid selector ends the walk and is recorded as a gap.
func (e *Evaluator) aggregateBySelector(
	name string, sel evaluate.LabelSelector, others []*ClusterRole, visiting map[string]bool,
) ([]PolicyRule, []assurance.Gap) {
	var (
		rules []PolicyRule
		gaps  []assurance.Gap
	)

	for _, other := range others {
		ok, err := sel.Matches(other.Metadata.Labels)
		if err != nil {
			return rules, append(gaps, assurance.Gap{
				Kind: evaluate.GapInvalidSelector, Detail: fmt.Sprintf("aggregation selector of ClusterRole %s: %v", name, err),
			})
		}

		if !ok {
			continue
		}

		sub, subGaps := e.effectiveRules(other, visiting)
		rules = append(rules, sub...)
		gaps = append(gaps, subGaps...)
	}

	return rules, gaps
}

// ruleGrants reports whether one rule grants the request.
func ruleGrants(rule *PolicyRule, req *Request) bool {
	if !listCovers(rule.Verbs, req.Verb) {
		return false
	}

	if req.NonResourceURL != "" {
		return slices.ContainsFunc(rule.NonResourceURLs, func(p string) bool { return urlMatches(p, req.NonResourceURL) })
	}

	return listCovers(rule.APIGroups, req.APIGroup) && resourceRuleGrants(rule, req)
}

// listCovers reports whether a rule list holds the wildcard or the value.
func listCovers(list []string, value string) bool {
	return slices.Contains(list, "*") || slices.Contains(list, value)
}

// resourceRuleGrants reports whether the resources and resource names of a rule cover the requested resource.
func resourceRuleGrants(rule *PolicyRule, req *Request) bool {
	if !slices.ContainsFunc(rule.Resources, func(p string) bool { return resourceMatches(p, req.Resource, req.Subresource) }) {
		return false
	}

	if len(rule.ResourceNames) == 0 {
		return true
	}

	return req.Name != "" && slices.Contains(rule.ResourceNames, req.Name)
}

// resourceMatches matches one `resources` entry: `*` (every resource and subresource), `pods` (the resource only),
// `pods/log`, `pods/*` (every subresource of pods), and `*/scale` (that subresource of every resource).
func resourceMatches(pattern, resource, subresource string) bool {
	if pattern == "*" {
		return true
	}

	if subresource == "" {
		return pattern == resource
	}

	return pattern == resource+"/"+subresource || pattern == resource+"/*" || pattern == "*/"+subresource
}

// urlMatches matches a `nonResourceURLs` entry: `*`, an exact path, or a prefix that ends in `*`.
func urlMatches(pattern, path string) bool {
	if pattern == "*" || pattern == path {
		return true
	}

	prefix, ok := strings.CutSuffix(pattern, "*")

	return ok && strings.HasPrefix(path, prefix)
}

// subjectIdentity is the queried subject as the bindings see it: its user name, the ServiceAccount it stands for (if
// any), and the groups it belongs to.
type subjectIdentity struct {
	subject Subject
	user    string
	account Subject
	groups  []string
}

// subjectMatcher builds the test for the binding subjects that stand for the queried subject.
func subjectMatcher(subject Subject, groups []string) func(Subject) bool {
	ident := &subjectIdentity{
		subject: subject, user: subject.Name, account: serviceAccountOf(subject), groups: slices.Clone(groups),
	}

	if subject.Kind == KindGroup {
		ident.groups = append(ident.groups, subject.Name)
	}

	switch {
	case ident.account.Kind == KindServiceAccount:
		ident.user = serviceAccountUser + ident.account.Namespace + ":" + ident.account.Name
		ident.groups = append(ident.groups, groupAuthenticated, groupServiceAccounts,
			groupServiceAccounts+":"+ident.account.Namespace)
	case subject.Kind == KindUser:
		ident.groups = append(ident.groups, groupAuthenticated)
	}

	return ident.matches
}

// serviceAccountOf gives the ServiceAccount that a subject stands for. The user name of a ServiceAccount is the same
// user as the ServiceAccount; any other subject stands for itself.
func serviceAccountOf(subject Subject) Subject {
	rest, ok := strings.CutPrefix(subject.Name, serviceAccountUser)
	if !ok || subject.Kind != KindUser {
		return subject
	}

	ns, name, ok := strings.Cut(rest, ":")
	if !ok {
		return subject
	}

	return Subject{Kind: KindServiceAccount, Name: name, Namespace: ns}
}

func (id *subjectIdentity) matches(b Subject) bool {
	switch b.Kind {
	case KindServiceAccount:
		return id.account.Kind == KindServiceAccount && b.Name == id.account.Name && b.Namespace == id.account.Namespace
	case KindUser:
		return id.subject.Kind != KindGroup && b.Name == id.user
	case KindGroup:
		return slices.Contains(id.groups, b.Name)
	}

	return false
}
