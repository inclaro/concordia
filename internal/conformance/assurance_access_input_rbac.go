package conformance

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/rbac"
	"github.com/inclaro/concordia/internal/assurance/render"
)

func rbacRules(rules []render.PolicyRule) []rbac.PolicyRule {
	out := make([]rbac.PolicyRule, 0, len(rules))
	for i := range rules {
		out = append(out, rbac.PolicyRule{
			Verbs: rules[i].Verbs, APIGroups: rules[i].APIGroups, Resources: rules[i].Resources,
			ResourceNames: rules[i].ResourceNames, NonResourceURLs: rules[i].NonResourceURLs,
		})
	}

	return out
}

func rbacSubjects(subjects []render.Subject) []rbac.Subject {
	out := make([]rbac.Subject, 0, len(subjects))
	for i := range subjects {
		out = append(out, rbac.Subject{Kind: subjects[i].Kind, Name: subjects[i].Name, Namespace: subjects[i].Namespace})
	}

	return out
}

// rbacAggregationOf converts the aggregation rule of a rendered ClusterRole, nil when it has none.
func rbacAggregationOf(agg *render.AggregationRule) *rbac.AggregationRule {
	if agg == nil {
		return nil
	}

	out := &rbac.AggregationRule{}
	for i := range agg.ClusterRoleSelectors {
		out.ClusterRoleSelectors = append(out.ClusterRoleSelectors, *selectorOf(&agg.ClusterRoleSelectors[i]))
	}

	return out
}

// rbacCollector gathers the resources of one environment into the input of the authorization evaluator.
type rbacCollector struct {
	input *rbac.Input
}

// add files one rendered resource under the input collection it belongs to.
func (c *rbacCollector) add(resource *render.Resource) {
	meta := evaluate.ObjectMeta{Name: resource.ID.Name, Namespace: resource.ID.Namespace, Labels: resource.Labels}

	if resource.Role != nil {
		c.addRole(resource, meta)
	}

	if resource.Binding != nil {
		c.addBinding(resource, meta)
	}
}

func (c *rbacCollector) addRole(resource *render.Resource, meta evaluate.ObjectMeta) {
	rules := rbacRules(resource.Role.Rules)

	switch resource.ID.Kind {
	case "Role":
		c.input.Roles = append(c.input.Roles, rbac.Role{Metadata: meta, Rules: rules})
	case "ClusterRole":
		c.input.ClusterRoles = append(c.input.ClusterRoles, rbac.ClusterRole{
			Metadata: meta, Rules: rules, AggregationRule: rbacAggregationOf(resource.Role.AggregationRule),
		})
	}
}

func (c *rbacCollector) addBinding(resource *render.Resource, meta evaluate.ObjectMeta) {
	roleRef := rbac.RoleRef{Kind: resource.Binding.RoleRef.Kind, Name: resource.Binding.RoleRef.Name}
	subjects := rbacSubjects(resource.Binding.Subjects)

	switch resource.ID.Kind {
	case "RoleBinding":
		c.input.RoleBindings = append(c.input.RoleBindings, rbac.RoleBinding{Metadata: meta, RoleRef: roleRef, Subjects: subjects})
	case "ClusterRoleBinding":
		c.input.ClusterRoleBindings = append(c.input.ClusterRoleBindings, rbac.ClusterRoleBinding{Metadata: meta, RoleRef: roleRef, Subjects: subjects})
	}
}

// rbacInput builds the input of the authorization evaluator for one environment. Every result depends on the
// cluster-scoped resources, so the cluster coverage is the worst coverage of the environment.
func (m *envModel) rbacInput() *rbac.Input {
	collector := &rbacCollector{input: &rbac.Input{Evidence: evaluate.EvidenceRendered, ClusterCoverage: m.overall}}

	m.resources(func(_ *render.Result, resource *render.Resource) { collector.add(resource) })

	for _, name := range m.namespaceList() {
		collector.input.Namespaces = append(collector.input.Namespaces, rbac.Namespace{Name: name, Coverage: m.namespaces[name]})
	}

	return collector.input
}
