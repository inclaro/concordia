package rbac_test

import (
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/rbac"
)

const baseYAML = `
evidence: rendered-or-evaluated
clusterCoverage: complete
namespaces:
  - {name: shop, coverage: complete}
  - {name: ops, coverage: complete}
  - {name: broken, coverage: failed}
`

func build(t *testing.T, docs string) rbac.Input {
	t.Helper()

	var in rbac.Input
	if err := yaml.Unmarshal([]byte(baseYAML+docs), &in); err != nil {
		t.Fatalf("input: %v", err)
	}

	return in
}

func sa(ns, name string) rbac.Subject {
	return rbac.Subject{Kind: rbac.KindServiceAccount, Name: name, Namespace: ns}
}

//nolint:gocritic // hugeParam: the helper takes a copy on purpose, so a test can change its input afterwards.
func newEval(in rbac.Input) *rbac.Evaluator { return rbac.New(&in) }

func query(subject rbac.Subject, verb, resource, ns string) *rbac.Query {
	return &rbac.Query{Subject: subject, Request: rbac.Request{Verb: verb, Resource: resource, Namespace: ns}}
}

const shopReader = `
roles:
  - metadata: {name: secret-reader, namespace: shop}
    rules: [{apiGroups: [""], resources: [secrets], verbs: [get, list]}]
roleBindings:
  - metadata: {name: api-reads-secrets, namespace: shop}
    subjects: [{kind: ServiceAccount, name: api, namespace: shop}]
    roleRef: {kind: Role, name: secret-reader}
`

func TestRoleBindingGrants(t *testing.T) {
	t.Parallel()

	ev := newEval(build(t, shopReader))

	tests := []struct {
		name string
		q    *rbac.Query
		want rbac.Verdict
	}{
		{"get secrets is granted", query(sa("shop", "api"), "get", "secrets", "shop"), rbac.Granted},
		{"delete secrets is not granted", query(sa("shop", "api"), "delete", "secrets", "shop"), rbac.NotGranted},
		{"other resource is not granted", query(sa("shop", "api"), "get", "configmaps", "shop"), rbac.NotGranted},
		{"other namespace is not granted", query(sa("shop", "api"), "get", "secrets", "ops"), rbac.NotGranted},
		{"cluster scope is not granted by a RoleBinding", query(sa("shop", "api"), "get", "secrets", ""), rbac.NotGranted},
		{"other ServiceAccount name", query(sa("shop", "web"), "get", "secrets", "shop"), rbac.NotGranted},
		{"same name in another namespace", query(sa("ops", "api"), "get", "secrets", "shop"), rbac.NotGranted},
		{"other API group", &rbac.Query{Subject: sa("shop", "api"), Request: rbac.Request{Verb: "get", APIGroup: "apps", Resource: "secrets", Namespace: "shop"}}, rbac.NotGranted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := ev.Evaluate(tt.q)
			if res.Verdict != tt.want {
				t.Fatalf("verdict = %s (%s), want %s", res.Verdict, res.Reason, tt.want)
			}

			if !hasRBACIdentity(&res) {
				t.Errorf("identity or assumptions: %+v", res)
			}

			if tt.want == rbac.Granted && !hasOneGrant(&res, "api-reads-secrets", "secret-reader") {
				t.Errorf("grants = %+v", res.Grants)
			}
		})
	}
}

// hasRBACIdentity reports whether the result names this evaluator and states its one assumption.
func hasRBACIdentity(res *rbac.Result) bool {
	return res.Evaluator == rbac.Identifier && res.Version == rbac.Version &&
		isOnly(res.Assumptions, "rbac-authorizer-enabled")
}

// hasOneGrant reports whether the result has exactly one grant, through the named binding and role.
func hasOneGrant(res *rbac.Result, binding, role string) bool {
	return len(res.Grants) == 1 && res.Grants[0].Binding.Name == binding && res.Grants[0].Role.Name == role
}

func TestResultStatesLimits(t *testing.T) {
	t.Parallel()

	res := newEval(build(t, shopReader)).Evaluate(query(sa("shop", "api"), "get", "secrets", "shop"))
	joined := strings.Join(res.Limits, "\n")

	for _, want := range []string{"cloud IAM", "admission control", "webhook authorizers"} {
		if !strings.Contains(joined, want) {
			t.Errorf("limits do not state %q: %v", want, res.Limits)
		}
	}
}

func TestBindingScopes(t *testing.T) {
	t.Parallel()

	ev := newEval(build(t, `
clusterRoles:
  - metadata: {name: pod-reader}
    rules: [{apiGroups: [""], resources: [pods, nodes], verbs: [get]}, {nonResourceURLs: ["/healthz"], verbs: [get]}]
roleBindings:
  - metadata: {name: local, namespace: shop}
    subjects: [{kind: ServiceAccount, name: api, namespace: shop}]
    roleRef: {kind: ClusterRole, name: pod-reader}
clusterRoleBindings:
  - metadata: {name: everywhere}
    subjects: [{kind: ServiceAccount, name: ops-bot, namespace: ops}]
    roleRef: {kind: ClusterRole, name: pod-reader}
`))

	tests := []struct {
		name string
		q    *rbac.Query
		want rbac.Verdict
	}{
		{"RoleBinding to a ClusterRole grants in its namespace", query(sa("shop", "api"), "get", "pods", "shop"), rbac.Granted},
		{"RoleBinding to a ClusterRole does not leave its namespace", query(sa("shop", "api"), "get", "pods", "ops"), rbac.NotGranted},
		{"RoleBinding to a ClusterRole does not grant cluster scope", query(sa("shop", "api"), "get", "nodes", ""), rbac.NotGranted},
		{"ClusterRoleBinding grants in every namespace", query(sa("ops", "ops-bot"), "get", "pods", "shop"), rbac.Granted},
		{"ClusterRoleBinding grants cluster scope", query(sa("ops", "ops-bot"), "get", "nodes", ""), rbac.Granted},
		// Non-resource URLs are cluster-wide: only a ClusterRoleBinding grants them, also when the request names a namespace.
		{"RoleBinding grants no non-resource URL", &rbac.Query{Subject: sa("shop", "api"), Request: rbac.Request{Verb: "get", NonResourceURL: "/healthz", Namespace: "shop"}}, rbac.NotGranted},
		{"ClusterRoleBinding grants a non-resource URL", &rbac.Query{Subject: sa("ops", "ops-bot"), Request: rbac.Request{Verb: "get", NonResourceURL: "/healthz", Namespace: "shop"}}, rbac.Granted},
	}

	for _, tt := range tests {
		if got := ev.Evaluate(tt.q).Verdict; got != tt.want {
			t.Errorf("%s: verdict = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestRuleMatching(t *testing.T) {
	t.Parallel()

	in := build(t, `
clusterRoles:
  - {metadata: {name: wild-verbs}, rules: [{apiGroups: [""], resources: [configmaps], verbs: ["*"]}]}
  - {metadata: {name: wild-resources}, rules: [{apiGroups: [""], resources: ["*"], verbs: [get]}]}
  - {metadata: {name: wild-groups}, rules: [{apiGroups: ["*"], resources: [deployments], verbs: [get]}]}
  - {metadata: {name: logs}, rules: [{apiGroups: [""], resources: [pods/log], verbs: [get]}]}
  - {metadata: {name: pods-only}, rules: [{apiGroups: [""], resources: [pods], verbs: [get]}]}
  - {metadata: {name: pod-subs}, rules: [{apiGroups: [""], resources: ["pods/*"], verbs: [get]}]}
  - {metadata: {name: scale}, rules: [{apiGroups: [apps], resources: ["*/scale"], verbs: [update]}]}
  - {metadata: {name: named}, rules: [{apiGroups: [""], resources: [secrets], resourceNames: [db-password], verbs: [get]}]}
  - {metadata: {name: health}, rules: [{nonResourceURLs: ["/healthz", "/metrics/*"], verbs: [get]}]}
  - {metadata: {name: two-rules}, rules: [{apiGroups: [""], resources: [pods], verbs: [list]}, {apiGroups: [""], resources: [secrets], verbs: [delete]}]}
clusterRoleBindings:
  - {metadata: {name: b1}, subjects: [{kind: User, name: wild-verbs}], roleRef: {kind: ClusterRole, name: wild-verbs}}
  - {metadata: {name: b2}, subjects: [{kind: User, name: wild-resources}], roleRef: {kind: ClusterRole, name: wild-resources}}
  - {metadata: {name: b3}, subjects: [{kind: User, name: wild-groups}], roleRef: {kind: ClusterRole, name: wild-groups}}
  - {metadata: {name: b4}, subjects: [{kind: User, name: logs}], roleRef: {kind: ClusterRole, name: logs}}
  - {metadata: {name: b5}, subjects: [{kind: User, name: pods-only}], roleRef: {kind: ClusterRole, name: pods-only}}
  - {metadata: {name: b6}, subjects: [{kind: User, name: pod-subs}], roleRef: {kind: ClusterRole, name: pod-subs}}
  - {metadata: {name: b7}, subjects: [{kind: User, name: scale}], roleRef: {kind: ClusterRole, name: scale}}
  - {metadata: {name: b8}, subjects: [{kind: User, name: named}], roleRef: {kind: ClusterRole, name: named}}
  - {metadata: {name: b9}, subjects: [{kind: User, name: health}], roleRef: {kind: ClusterRole, name: health}}
  - {metadata: {name: b10}, subjects: [{kind: User, name: two-rules}], roleRef: {kind: ClusterRole, name: two-rules}}
`)
	ev := newEval(in)

	user := func(name string) rbac.Subject { return rbac.Subject{Kind: rbac.KindUser, Name: name} }
	req := func(verb, group, resource, name string) rbac.Request {
		return rbac.Request{Verb: verb, APIGroup: group, Resource: resource, Name: name, Namespace: "shop"}
	}

	tests := []struct {
		name string
		user string
		req  rbac.Request
		want rbac.Verdict
	}{
		{"wildcard verb", "wild-verbs", req("delete", "", "configmaps", ""), rbac.Granted},
		{"wildcard verb on another resource", "wild-verbs", req("delete", "", "secrets", ""), rbac.NotGranted},
		{"wildcard resource", "wild-resources", req("get", "", "secrets", ""), rbac.Granted},
		{"wildcard resource includes subresources", "wild-resources", rbac.Request{Verb: "get", Resource: "pods", Subresource: "log", Namespace: "shop"}, rbac.Granted},
		{"wildcard resource keeps the verb", "wild-resources", req("delete", "", "secrets", ""), rbac.NotGranted},
		{"wildcard group", "wild-groups", req("get", "apps", "deployments", ""), rbac.Granted},
		{"wildcard group, other resource", "wild-groups", req("get", "apps", "statefulsets", ""), rbac.NotGranted},
		{"subresource rule", "logs", req("get", "", "pods/log", ""), rbac.Granted},
		{"subresource rule does not grant the resource", "logs", req("get", "", "pods", ""), rbac.NotGranted},
		{"resource rule does not grant subresources", "pods-only", req("get", "", "pods/log", ""), rbac.NotGranted},
		{"resource rule grants the resource", "pods-only", req("get", "", "pods", ""), rbac.Granted},
		{"resource/* grants every subresource", "pod-subs", req("get", "", "pods/exec", ""), rbac.Granted},
		{"resource/* does not grant the resource", "pod-subs", req("get", "", "pods", ""), rbac.NotGranted},
		{"*/subresource", "scale", req("update", "apps", "deployments/scale", ""), rbac.Granted},
		{"*/subresource, other subresource", "scale", req("update", "apps", "deployments/status", ""), rbac.NotGranted},
		{"resourceNames matches the name", "named", req("get", "", "secrets", "db-password"), rbac.Granted},
		{"resourceNames rejects another name", "named", req("get", "", "secrets", "api-key"), rbac.NotGranted},
		{"resourceNames rejects a request without a name", "named", req("get", "", "secrets", ""), rbac.NotGranted},
		{"non-resource URL exact", "health", rbac.Request{Verb: "get", NonResourceURL: "/healthz"}, rbac.Granted},
		{"non-resource URL prefix", "health", rbac.Request{Verb: "get", NonResourceURL: "/metrics/cadvisor"}, rbac.Granted},
		{"non-resource URL other path", "health", rbac.Request{Verb: "get", NonResourceURL: "/version"}, rbac.NotGranted},
		{"non-resource rule grants no resource", "health", req("get", "", "pods", ""), rbac.NotGranted},
		{"second rule of a role", "two-rules", req("delete", "", "secrets", ""), rbac.Granted},
		{"verb of the other rule on the wrong resource", "two-rules", req("delete", "", "pods", ""), rbac.NotGranted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := ev.Evaluate(&rbac.Query{Subject: user(tt.user), Request: tt.req})
			if res.Verdict != tt.want {
				t.Fatalf("verdict = %s (%+v), want %s", res.Verdict, res.Gaps, tt.want)
			}
		})
	}
}

func TestAggregatedClusterRole(t *testing.T) {
	t.Parallel()

	ev := newEval(build(t, `
clusterRoles:
  - metadata: {name: monitoring}
    aggregationRule:
      clusterRoleSelectors:
        - matchLabels: {rbac.example.com/aggregate-to-monitoring: "true"}
    rules: [{apiGroups: [""], resources: [secrets], verbs: [delete]}]
  - metadata: {name: monitoring-endpoints, labels: {rbac.example.com/aggregate-to-monitoring: "true"}}
    rules: [{apiGroups: [""], resources: [services, endpoints, pods], verbs: [get, list, watch]}]
  - metadata: {name: monitoring-extra, labels: {rbac.example.com/aggregate-to-monitoring: "true"}}
    rules: [{apiGroups: [""], resources: [nodes], verbs: [get]}]
  - metadata: {name: unrelated}
    rules: [{apiGroups: [""], resources: [configmaps], verbs: [get]}]
  - metadata: {name: outer}
    aggregationRule:
      clusterRoleSelectors:
        - matchExpressions: [{key: tier, operator: In, values: [inner]}]
  - metadata: {name: inner-role, labels: {tier: inner}}
    aggregationRule:
      clusterRoleSelectors:
        - matchLabels: {rbac.example.com/aggregate-to-monitoring: "true"}
clusterRoleBindings:
  - metadata: {name: prom}
    subjects: [{kind: ServiceAccount, name: prometheus, namespace: ops}]
    roleRef: {kind: ClusterRole, name: monitoring}
  - metadata: {name: outer-bind}
    subjects: [{kind: ServiceAccount, name: nested, namespace: ops}]
    roleRef: {kind: ClusterRole, name: outer}
`))

	prom, nested := sa("ops", "prometheus"), sa("ops", "nested")

	tests := []struct {
		name string
		q    *rbac.Query
		want rbac.Verdict
	}{
		{"permission of a matching ClusterRole", query(prom, "list", "pods", "shop"), rbac.Granted},
		{"second matching ClusterRole", query(prom, "get", "nodes", ""), rbac.Granted},
		{"role without the label is not aggregated", query(prom, "get", "configmaps", "shop"), rbac.NotGranted},
		{"rules written into an aggregated role are overwritten", query(prom, "delete", "secrets", "shop"), rbac.NotGranted},
		{"nested aggregation", query(nested, "watch", "endpoints", "shop"), rbac.Granted},
	}

	for _, tt := range tests {
		res := ev.Evaluate(tt.q)
		if res.Verdict != tt.want {
			t.Errorf("%s: verdict = %s, want %s", tt.name, res.Verdict, tt.want)
		}

		if tt.want == rbac.Granted && res.Grants[0].Role.Name == "" {
			t.Errorf("%s: grant names no role: %+v", tt.name, res.Grants)
		}
	}
}

func TestAggregationCycleAndInvalidSelector(t *testing.T) {
	t.Parallel()

	ev := newEval(build(t, `
clusterRoles:
  - metadata: {name: a, labels: {grp: b}}
    aggregationRule: {clusterRoleSelectors: [{matchLabels: {grp: a}}]}
  - metadata: {name: b, labels: {grp: a}}
    aggregationRule: {clusterRoleSelectors: [{matchLabels: {grp: b}}]}
  - metadata: {name: bad}
    aggregationRule: {clusterRoleSelectors: [{matchExpressions: [{key: x, operator: Bogus}]}]}
clusterRoleBindings:
  - {metadata: {name: ba}, subjects: [{kind: User, name: u1}], roleRef: {kind: ClusterRole, name: a}}
  - {metadata: {name: bb}, subjects: [{kind: User, name: u2}], roleRef: {kind: ClusterRole, name: bad}}
`))

	for _, user := range []string{"u1", "u2"} {
		res := ev.Evaluate(query(rbac.Subject{Kind: rbac.KindUser, Name: user}, "get", "pods", "shop"))
		if res.Verdict != rbac.Unknown || len(res.Gaps) == 0 {
			t.Errorf("%s: %s %+v", user, res.Verdict, res.Gaps)
		}
	}
}

func TestSubjectKinds(t *testing.T) {
	t.Parallel()

	ev := newEval(build(t, `
clusterRoles:
  - {metadata: {name: view-pods}, rules: [{apiGroups: [""], resources: [pods], verbs: [get]}]}
clusterRoleBindings:
  - {metadata: {name: alice}, subjects: [{kind: User, name: alice}], roleRef: {kind: ClusterRole, name: view-pods}}
  - {metadata: {name: devs}, subjects: [{kind: Group, name: devs}], roleRef: {kind: ClusterRole, name: view-pods}}
  - {metadata: {name: shop-sas}, subjects: [{kind: Group, name: "system:serviceaccounts:shop"}], roleRef: {kind: ClusterRole, name: view-pods}}
  - {metadata: {name: sa-user}, subjects: [{kind: User, name: "system:serviceaccount:ops:robot"}], roleRef: {kind: ClusterRole, name: view-pods}}
`))

	tests := []struct {
		name string
		q    *rbac.Query
		want rbac.Verdict
	}{
		{"User", query(rbac.Subject{Kind: rbac.KindUser, Name: "alice"}, "get", "pods", "shop"), rbac.Granted},
		{"other User", query(rbac.Subject{Kind: rbac.KindUser, Name: "bob"}, "get", "pods", "shop"), rbac.NotGranted},
		{"User with a group", &rbac.Query{Subject: rbac.Subject{Kind: rbac.KindUser, Name: "bob"}, Groups: []string{"devs"}, Request: rbac.Request{Verb: "get", Resource: "pods", Namespace: "shop"}}, rbac.Granted},
		{"User without the group", query(rbac.Subject{Kind: rbac.KindUser, Name: "bob"}, "get", "pods", "shop"), rbac.NotGranted},
		{"Group", query(rbac.Subject{Kind: rbac.KindGroup, Name: "devs"}, "get", "pods", "shop"), rbac.Granted},
		{"ServiceAccount through its namespace group", query(sa("shop", "anything"), "get", "pods", "shop"), rbac.Granted},
		{"ServiceAccount of another namespace", query(sa("free", "anything"), "get", "pods", "shop"), rbac.NotGranted},
		{"ServiceAccount through its user name", query(sa("ops", "robot"), "get", "pods", "shop"), rbac.Granted},
		{"user name of a ServiceAccount", query(rbac.Subject{Kind: rbac.KindUser, Name: "system:serviceaccount:shop:x"}, "get", "pods", "shop"), rbac.Granted},
	}

	for _, tt := range tests {
		if got := ev.Evaluate(tt.q).Verdict; got != tt.want {
			t.Errorf("%s: verdict = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestMissingRoleTarget(t *testing.T) {
	t.Parallel()

	docs := `
roleBindings:
  - metadata: {name: api-binding, namespace: shop}
    subjects: [{kind: ServiceAccount, name: api, namespace: shop}]
    roleRef: {kind: Role, name: vanished}
  - metadata: {name: other-binding, namespace: shop}
    subjects: [{kind: ServiceAccount, name: other, namespace: shop}]
    roleRef: {kind: Role, name: also-vanished}
clusterRoleBindings:
  - metadata: {name: admin-binding}
    subjects: [{kind: ServiceAccount, name: api, namespace: shop}]
    roleRef: {kind: ClusterRole, name: cluster-admin}
`

	ev := newEval(build(t, docs))
	res := ev.Evaluate(query(sa("shop", "api"), "get", "secrets", "shop"))

	if res.Verdict != rbac.Unknown || res.Reason != evaluate.ReasonRoleTargetMissing {
		t.Fatalf("verdict = %s (%s)", res.Verdict, res.Reason)
	}

	want := []rbac.Ref{{Kind: "ClusterRole", Name: "cluster-admin"}, {Kind: "Role", Namespace: "shop", Name: "vanished"}}
	if !slices.Equal(res.Missing, want) {
		t.Fatalf("missing = %+v, want %+v", res.Missing, want)
	}

	if len(res.Gaps) != 2 || res.Gaps[0].Kind != evaluate.GapMissingRole {
		t.Fatalf("gaps = %+v", res.Gaps)
	}

	// The binding of another subject is not part of this result, and a binding that grants wins over a missing one.
	if got := ev.Evaluate(query(sa("shop", "stranger"), "get", "secrets", "shop")).Verdict; got != rbac.NotGranted {
		t.Errorf("unrelated subject: %s", got)
	}

	assertGrantBeatsMissingRole(t, docs)
}

// assertGrantBeatsMissingRole checks that a binding that grants wins over a binding whose role is missing: the docs
// get the role "vanished" that the first binding refers to.
func assertGrantBeatsMissingRole(t *testing.T, docs string) {
	t.Helper()

	withGrant := build(t, docs)
	withGrant.Roles = []rbac.Role{{
		Metadata: evaluate.ObjectMeta{Name: "vanished", Namespace: "shop"},
		Rules:    []rbac.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}}},
	}}

	if res := newEval(withGrant).Evaluate(apiGetsSecrets("shop")); res.Verdict != rbac.Granted || len(res.Missing) != 0 {
		t.Fatalf("a grant beats a missing role of another binding: %s %+v", res.Verdict, res.Missing)
	}
}

func TestInvalidRoleReference(t *testing.T) {
	t.Parallel()

	res := newEval(build(t, `
clusterRoleBindings:
  - metadata: {name: bad}
    subjects: [{kind: User, name: u}]
    roleRef: {kind: Role, name: r}
`)).Evaluate(query(rbac.Subject{Kind: rbac.KindUser, Name: "u"}, "get", "pods", "shop"))

	if res.Verdict != rbac.Unknown || res.Gaps[0].Kind != evaluate.GapInvalidRoleRef {
		t.Fatalf("%s %+v", res.Verdict, res.Gaps)
	}
}

func TestCoverageAndEvidenceGates(t *testing.T) {
	t.Parallel()

	in := build(t, shopReader)
	ev := newEval(in)

	if res := ev.Evaluate(apiGetsSecrets("broken")); !isUnknownFor(&res, evaluate.ReasonCoverageIncomplete) {
		t.Fatalf("failed namespace: %s (%s)", res.Verdict, res.Reason)
	}

	if res := ev.Evaluate(apiGetsSecrets("missing")); res.Verdict != rbac.Unknown {
		t.Fatalf("namespace not in the input: %s", res.Verdict)
	}

	if res := ev.Evaluate(apiGetsSecrets("shop")); res.Verdict != rbac.Granted {
		t.Fatalf("other namespace: %s", res.Verdict)
	}

	partial := in
	partial.ClusterCoverage = assurance.CoveragePartial

	if res := newEval(partial).Evaluate(apiGetsSecrets("shop")); !isUnknownWithGap(&res, evaluate.GapClusterCoverage) {
		t.Fatalf("partial cluster coverage: %s %+v", res.Verdict, res.Gaps)
	}

	declared := in
	declared.Evidence = evaluate.EvidenceDeclared

	if res := newEval(declared).Evaluate(apiGetsSecrets("shop")); !isUnknownFor(&res, evaluate.ReasonRenderedEvidenceMissing) {
		t.Fatalf("declared evidence: %s (%s)", res.Verdict, res.Reason)
	}
}

// apiGetsSecrets asks whether the ServiceAccount shop/api may get secrets in the namespace.
func apiGetsSecrets(ns string) *rbac.Query {
	return query(sa("shop", "api"), "get", "secrets", ns)
}

// isUnknownFor reports whether the verdict is unknown for the given reason.
func isUnknownFor(res *rbac.Result, reason string) bool {
	return res.Verdict == rbac.Unknown && res.Reason == reason
}

// isUnknownWithGap reports whether the verdict is unknown and the first gap is of the given kind.
func isUnknownWithGap(res *rbac.Result, kind string) bool {
	return res.Verdict == rbac.Unknown && len(res.Gaps) > 0 && res.Gaps[0].Kind == kind
}

func TestWho(t *testing.T) {
	t.Parallel()

	ev := newEval(build(t, `
roles:
  - metadata: {name: secret-reader, namespace: shop}
    rules: [{apiGroups: [""], resources: [secrets], verbs: [get]}]
clusterRoles:
  - {metadata: {name: all-secrets}, rules: [{apiGroups: [""], resources: [secrets], verbs: ["*"]}]}
roleBindings:
  - metadata: {name: local, namespace: shop}
    subjects: [{kind: ServiceAccount, name: api, namespace: shop}, {kind: User, name: alice}]
    roleRef: {kind: Role, name: secret-reader}
clusterRoleBindings:
  - metadata: {name: ops}
    subjects: [{kind: Group, name: ops-team}]
    roleRef: {kind: ClusterRole, name: all-secrets}
`))

	res := ev.Who(&rbac.Request{Verb: "get", Resource: "secrets", Namespace: "shop"})
	if res.Verdict != rbac.Granted || len(res.Grants) != 3 {
		t.Fatalf("get in shop: %s %+v", res.Verdict, res.Grants)
	}

	if names := grantSubjects(res.Grants); !slices.Equal(names, []string{"Group/ops-team", "ServiceAccount/api", "User/alice"}) {
		t.Fatalf("subjects = %v", names)
	}

	del := ev.Who(&rbac.Request{Verb: "delete", Resource: "secrets", Namespace: "shop"})
	if del.Verdict != rbac.Granted || !grantsOnlyTo(del.Grants, "ops-team") {
		t.Fatalf("delete in shop: %s %+v", del.Verdict, del.Grants)
	}

	if none := ev.Who(&rbac.Request{Verb: "get", Resource: "nodes", Namespace: "shop"}); none.Verdict != rbac.NotGranted {
		t.Fatalf("nodes: %s", none.Verdict)
	}
}

// grantSubjects lists the subjects of grants as "Kind/Name", in grant order.
func grantSubjects(grants []rbac.Grant) []string {
	names := make([]string, 0, len(grants))
	for i := range grants {
		names = append(names, grants[i].Subject.Kind+"/"+grants[i].Subject.Name)
	}

	return names
}

// grantsOnlyTo reports whether there is exactly one grant and it is for the named subject.
func grantsOnlyTo(grants []rbac.Grant, name string) bool {
	return len(grants) == 1 && grants[0].Subject.Name == name
}

func TestDeterminismAndContract(t *testing.T) {
	t.Parallel()

	a := build(t, shopReader+`
clusterRoles:
  - {metadata: {name: x}}
  - {metadata: {name: y}}
`)
	b := a
	b.ClusterRoles = []rbac.ClusterRole{a.ClusterRoles[1], a.ClusterRoles[0]}

	if a.Digest() != b.Digest() || !strings.HasPrefix(a.Digest(), "sha256:") {
		t.Errorf("digest depends on order or is malformed: %s %s", a.Digest(), b.Digest())
	}

	empty := build(t, "")
	if a.Digest() == empty.Digest() {
		t.Error("different inputs have the same digest")
	}

	assertContract(t)
}

// isOnly reports whether the list holds exactly the wanted value.
func isOnly[T comparable](list []T, want T) bool {
	return len(list) == 1 && list[0] == want
}

// isRBACContract reports whether the contract is the one that the RBAC evaluator states.
func isRBACContract(c *evaluate.Contract) bool {
	return c.ID == "rbac" && c.RequiresEvidence == evaluate.EvidenceRendered &&
		isOnly(c.Enables, rbac.ClaimRBACGrants) && isOnly(c.Assumes, "rbac-authorizer-enabled") && len(c.Limits) > 0
}

func assertContract(t *testing.T) {
	t.Helper()

	c := rbac.Contract()
	if !isRBACContract(&c) {
		t.Fatalf("contract = %+v", c)
	}

	again := rbac.Contract()
	if c.Digest() != again.Digest() {
		t.Error("contract digest is not stable")
	}
}
