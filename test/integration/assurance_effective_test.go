package integration_test

import (
	"bytes"
	"cmp"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
)

// The effective-access world is the infra workspace with one environment (`production`), three workloads in the
// namespace `shop` rendered from `assurance-render/production` (`web`, `api`, `db`), the CALM services that bind to
// them (`web-service`, `api-service`, `db-service`), and a rule file with `require.effectiveAccess` rules. The
// recording knows nothing about NetworkPolicy selection: whatever the rules decide, they decide from the manifests
// (O6, A35).

const (
	effNamespace = `apiVersion: v1
kind: Namespace
metadata:
  name: shop
`

	effWorkloads = `---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  template:
    metadata:
      labels: {app: web, tier: web}
    spec:
      serviceAccountName: web-sa
      containers:
        - name: app
          image: registry.example.com/shop/web:1.0.0
          ports: [{containerPort: 8080}]
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: shop
spec:
  template:
    metadata:
      labels: {app: api, tier: backend}
    spec:
      serviceAccountName: api-sa
      containers:
        - name: app
          image: registry.example.com/shop/api:1.0.0
          ports: [{containerPort: 8080}]
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: db
  namespace: shop
spec:
  template:
    metadata:
      labels: {app: db, tier: data}
    spec:
      containers:
        - name: app
          image: registry.example.com/shop/db:1.0.0
          ports: [{containerPort: 22}, {containerPort: 5432}, {containerPort: 8080}]
`

	// effDenyAll selects every pod of the namespace with an empty `podSelector`: CBM 0.12.1 records no edge for it.
	effDenyAll = `---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny
  namespace: shop
spec:
  podSelector: {}
  policyTypes: [Ingress]
`

	// effAllowWebToDB allows ingress to the db from pods that match a `matchExpressions` selector: no CBM edge either.
	effAllowWebToDB = `---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: db-allow-web
  namespace: shop
spec:
  podSelector:
    matchLabels: {app: db}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchExpressions:
              - {key: tier, operator: In, values: [web, edge]}
      ports:
        - {protocol: TCP, port: 8080}
`

	effArchitecture = `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {"unique-id": "web-service", "node-type": "service", "name": "Web", "description": "The web tier."},
    {"unique-id": "api-service", "node-type": "service", "name": "API", "description": "The API."},
    {"unique-id": "db-service", "node-type": "service", "name": "Database", "description": "The database."},
    {"unique-id": "db-credentials", "node-type": "data-asset", "name": "Credentials", "description": "The database credentials."}
  ],
  "relationships": []
}
`

	effBindings = `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-web
    architectureRef: calm:node:web-service
    required: false
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: web, namespace: shop}
  - id: bind-api
    architectureRef: calm:node:api-service
    required: false
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: api, namespace: shop}
  - id: bind-db
    architectureRef: calm:node:db-service
    required: false
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: db, namespace: shop}
  - id: bind-credentials
    architectureRef: calm:node:db-credentials
    required: false
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Secret, name: db-credentials, namespace: shop}
`

	effEnvironment = `environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/prod/**"]
`

	effPolicyHead = `apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata:
  id: access
  version: 1.0.0
rules:
`
)

// effEdge is the pair of CALM nodes that a `require.effectiveAccess` rule relates.
type effEdge struct {
	from, to string
}

var (
	effWebToDB          = effEdge{from: "web-service", to: "db-service"}
	effAPIToDB          = effEdge{from: "api-service", to: "db-service"}
	effAPIToCredentials = effEdge{from: "api-service", to: "db-credentials"}
)

// effRuleSpec describes a `require.effectiveAccess` rule.
type effRuleSpec struct {
	id, dimension, expect string
	edge                  effEdge
	ports                 []int
}

// effNetRule is a rule of the network dimension between two CALM nodes, for the ports (any port when none).
func effNetRule(id string, edge effEdge, expect string, ports ...int) string {
	return effRule(&effRuleSpec{id: id, dimension: "network", edge: edge, expect: expect, ports: ports})
}

// effAuthRule is a rule of the authorization dimension between two CALM nodes.
func effAuthRule(id string, edge effEdge, expect string) string {
	return effRule(&effRuleSpec{id: id, dimension: "authorization", edge: edge, expect: expect})
}

func effRule(spec *effRuleSpec) string {
	text := "  - id: " + spec.id + "\n    name: " + spec.id + "\n    severity: error\n    require:\n      effectiveAccess:\n        dimension: " + spec.dimension +
		"\n        from: {calmNodes: [" + spec.edge.from + "]}\n        to: {calmNodes: [" + spec.edge.to + "]}\n        expect: " + spec.expect + "\n"

	if len(spec.ports) > 0 {
		list := make([]string, len(spec.ports))
		for i, port := range spec.ports {
			list[i] = strconv.Itoa(port)
		}

		text += "        ports: [" + strings.Join(list, ", ") + "]\n"
	}

	return text
}

// effWorld is one test: the manifests of the environment, the rules, and the `assurance` block.
type effWorld struct {
	// evaluators is the `evaluators` list of the `assurance` block, written as YAML under `assurance:`. Empty: none.
	evaluators string
	// bindings replaces the binding file.
	bindings string
	// files are extra files of the rendered directory (file name to text).
	files map[string]string
	// manifests are the documents of the rendered directory, after the namespace and the workloads.
	manifests []string
	rules     []string
}

func (w *effWorld) spec() *deploymentSpec {
	head := strings.Replace(deploymentWorkspaceHead, "  files: []\n  packs:\n    - use: builtin:infrastructure\n", "  files: [policies/**/*.policy.yaml]\n", 1)
	block := "assurance:\n  rendering:\n    sources:\n      - {environment: production, tool: plain, path: assurance-render/production}\n" + w.evaluators

	files := map[string]string{
		"concordia.yaml":                          head + deploymentCheck + effEnvironment + block,
		"policies/access.policy.yaml":             effPolicyHead + strings.Join(w.rules, ""),
		"assurance-render/production/render.yaml": effNamespace + effWorkloads + strings.Join(w.manifests, ""),
	}

	for name, text := range w.files {
		files["assurance-render/production/"+name] = text
	}

	return &deploymentSpec{
		files: files, architecture: effArchitecture, bindings: cmp.Or(w.bindings, effBindings), environments: effEnvironment,
		withoutPack: true, noIndex: true,
	}
}

func (w *effWorld) run(t *testing.T) *conformance.Run {
	t.Helper()

	return deploymentRun(t, w.spec()).run
}

const (
	effNetworkEnforced = "  evaluators:\n    - {environment: production, kinds: [network-policy], assumes: [network-policy-enforced]}\n"
	effNetworkOnly     = "  evaluators:\n    - {environment: production, kinds: [network-policy]}\n"
	effRBACEnforced    = "  evaluators:\n    - {environment: production, kinds: [rbac], assumes: [rbac-authorizer-enabled]}\n"
)

// effOne gives the one obligation of a rule.
func effOne(t *testing.T, run *conformance.Run, rule string) *conformance.ObligationResult {
	t.Helper()

	obligations := ruleOf(t, run, rule).Obligations
	if len(obligations) != 1 {
		t.Fatalf("rule %s has %d obligations, want 1: %+v", rule, len(obligations), obligations)
	}

	return &obligations[0]
}

// A35: a NetworkPolicy selects `db` in the rendered manifests, but no evaluator is enabled. The isolation claim is
// `unknown` with `no-evaluator-available`, never a pass, and nothing says that the db is isolated.
func TestEffectiveAccessA35SelectorEdgeWithoutEvaluator(t *testing.T) {
	t.Parallel()

	for name, evaluators := range map[string]string{
		"no evaluators":                  "",
		"an evaluator of the other kind": effRBACEnforced,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run := (&effWorld{
				manifests: []string{effDenyAll}, evaluators: evaluators,
				rules: []string{effNetRule("ACC-001", effWebToDB, "denied")},
			}).run(t)

			effRequireNoEvaluatorUnknown(t, run)
		})
	}
}

// effRequireNoEvaluatorUnknown checks that the rule ACC-001 is unknown for lack of an evaluator, claims no isolation, and
// that no evaluator ran.
func effRequireNoEvaluatorUnknown(t *testing.T, run *conformance.Run) {
	t.Helper()

	if rule := ruleOf(t, run, "ACC-001"); rule.Verdict != conformance.VerdictUnknown {
		t.Fatalf("verdict = %s, want unknown", rule.Verdict)
	}

	got := effOne(t, run, "ACC-001")
	if got.Verdict != conformance.VerdictUnknown || got.Reason != conformance.ReasonNoEvaluatorAvailable {
		t.Errorf("obligation = %s %q, want unknown no-evaluator-available", got.Verdict, got.Reason)
	}

	acc := findingsOf(run, "ACC-001")
	for i := range acc {
		if strings.Contains(strings.ToLower(acc[i].Message), "isolated") {
			t.Errorf("a finding says that the workload is isolated: %q", acc[i].Message)
		}
	}

	if len(run.Assurance.Evaluations) != 0 {
		t.Errorf("evaluations = %+v, want none: no evaluator ran", run.Assurance.Evaluations)
	}
}

// effRequireDeclaredAssumption checks the run of a declared `network-policy-enforced`: ACC-001 passes, lists the
// assumption, and every evaluation entry names the evaluator, the input digest, and the declared assumption.
func effRequireDeclaredAssumption(t *testing.T, run *conformance.Run) {
	t.Helper()

	got := effOne(t, run, "ACC-001")
	if got.Verdict != conformance.VerdictPass {
		t.Fatalf("obligation = %s %q %q, want pass", got.Verdict, got.Reason, got.Detail)
	}

	if !strings.Contains(got.Detail, "network-policy-enforced") {
		t.Errorf("detail %q does not list the assumption", got.Detail)
	}

	if len(run.Assurance.Evaluations) == 0 {
		t.Fatal("no evaluation entry")
	}

	for i := range run.Assurance.Evaluations {
		effRequireDeclaredEntry(t, &run.Assurance.Evaluations[i])
	}
}

func effRequireDeclaredEntry(t *testing.T, entry *conformance.EvaluationEntry) {
	t.Helper()

	if !slices.Contains(entry.Assumptions, "network-policy-enforced") || !slices.Contains(entry.DeclaredAssumptions, "network-policy-enforced") {
		t.Errorf("entry %+v does not list the declared assumption", *entry)
	}

	if entry.Evaluator != "network-policy" || entry.EvaluatorVersion == "" || entry.InputDigest == "" {
		t.Errorf("entry %+v does not name the evaluator and the input digest", *entry)
	}
}

// effRequireUndeclaredAssumption checks the run without the declaration: ACC-001 is unknown with
// `enforcement-not-declared`, and no entry lists an assumption.
func effRequireUndeclaredAssumption(t *testing.T, run *conformance.Run) {
	t.Helper()

	got := effOne(t, run, "ACC-001")
	if got.Verdict != conformance.VerdictUnknown || got.Reason != conformance.ReasonEnforcementNotDeclared {
		t.Fatalf("obligation = %s %q %q, want unknown enforcement-not-declared", got.Verdict, got.Reason, got.Detail)
	}

	for i := range run.Assurance.Evaluations {
		if entry := &run.Assurance.Evaluations[i]; len(entry.DeclaredAssumptions) != 0 {
			t.Errorf("entry %+v lists an assumption that the workspace does not declare", *entry)
		}
	}
}

// The enforcement assumption gate (7.4): `denied` with a declared `network-policy-enforced` is `pass` and lists the
// assumption; without the declaration it is `unknown` with `enforcement-not-declared`.
func TestEffectiveAccessEnforcementAssumption(t *testing.T) {
	t.Parallel()

	rules := []string{effNetRule("ACC-001", effAPIToDB, "denied")}

	t.Run("declared", func(t *testing.T) {
		t.Parallel()

		effRequireDeclaredAssumption(t, (&effWorld{manifests: []string{effDenyAll, effAllowWebToDB}, rules: rules, evaluators: effNetworkEnforced}).run(t))
	})

	t.Run("not declared", func(t *testing.T) {
		t.Parallel()

		effRequireUndeclaredAssumption(t, (&effWorld{manifests: []string{effDenyAll, effAllowWebToDB}, rules: rules, evaluators: effNetworkOnly}).run(t))
	})
}

// O6: an empty `podSelector` and a `matchExpressions` rule have no CBM edge. The evaluator applies both from the
// rendered documents: `web` (tier web) reaches the db through the expression, `api` (tier backend) is denied by the
// empty selector.
func TestEffectiveAccessEvaluatesEmptyAndExpressionSelectorsFromManifests(t *testing.T) {
	t.Parallel()

	run := (&effWorld{
		manifests: []string{effDenyAll, effAllowWebToDB}, evaluators: effNetworkEnforced,
		rules: []string{
			effNetRule("ACC-001", effWebToDB, "denied", 8080),
			effNetRule("ACC-002", effAPIToDB, "denied", 8080),
			effNetRule("ACC-003", effWebToDB, "allowed", 8080),
		},
	}).run(t)

	if got := effOne(t, run, "ACC-001"); got.Verdict != conformance.VerdictViolation || !strings.Contains(got.Detail, "db-allow-web") {
		t.Errorf("web -> db denied: %s %q, want a violation that names the allowing policy", got.Verdict, got.Detail)
	}

	if got := effOne(t, run, "ACC-002"); got.Verdict != conformance.VerdictPass || !strings.Contains(got.Detail, "default-deny") {
		t.Errorf("api -> db denied: %s %q, want pass through default-deny", got.Verdict, got.Detail)
	}

	if got := effOne(t, run, "ACC-003"); got.Verdict != conformance.VerdictPass {
		t.Errorf("web -> db allowed: %s %q, want pass", got.Verdict, got.Detail)
	}
}

// Results are per pair and port: a denied port 22 never hides the allowed port 8080 of the same workloads, and a rule
// that forbids port 8080 gets a violation.
func TestEffectiveAccessPerPortResults(t *testing.T) {
	t.Parallel()

	run := (&effWorld{
		manifests: []string{effDenyAll, effAllowWebToDB}, evaluators: effNetworkEnforced,
		rules: []string{effNetRule("ACC-001", effWebToDB, "denied", 22, 8080)},
	}).run(t)

	rule := ruleOf(t, run, "ACC-001")
	if rule.Verdict != conformance.VerdictViolation {
		t.Fatalf("verdict = %s, want violation", rule.Verdict)
	}

	if len(rule.Obligations) != 2 {
		t.Fatalf("obligations = %d, want 2 (one for each port): %+v", len(rule.Obligations), rule.Obligations)
	}

	verdicts := map[conformance.RuleVerdict]int{}
	for i := range rule.Obligations {
		verdicts[rule.Obligations[i].Verdict]++
	}

	if verdicts[conformance.VerdictPass] != 1 || verdicts[conformance.VerdictViolation] != 1 {
		t.Errorf("verdicts = %v, want one pass (22) and one violation (8080)", verdicts)
	}

	effRequirePortQueries(t, run)
}

// effRequirePortQueries checks that the evaluations deny port 22 and allow port 8080 of web to db.
func effRequirePortQueries(t *testing.T, run *conformance.Run) {
	t.Helper()

	queries := map[string]string{}

	for i := range run.Assurance.Evaluations {
		entry := &run.Assurance.Evaluations[i]
		queries[entry.Query] = entry.Verdict
	}

	if queries["tcp/22 from shop/web to shop/db"] != "denied" || queries["tcp/8080 from shop/web to shop/db"] != "allowed" {
		t.Errorf("evaluations = %v, want 22 denied and 8080 allowed", queries)
	}
}

// A namespace whose render is not complete gives `coverage-incomplete`, not a result from what could be read.
func TestEffectiveAccessIncompleteRenderIsUnknown(t *testing.T) {
	t.Parallel()

	broken, err := os.ReadFile(filepath.Join("..", "fixtures", "assurance", "plain-broken", "bad.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	run := (&effWorld{
		manifests: []string{effDenyAll}, evaluators: effNetworkEnforced, files: map[string]string{"bad.yaml": string(broken)},
		rules: []string{effNetRule("ACC-001", effAPIToDB, "denied")},
	}).run(t)

	got := effOne(t, run, "ACC-001")
	if got.Verdict != conformance.VerdictUnknown || got.Reason != conformance.ReasonCoverageIncomplete {
		t.Errorf("obligation = %s %q %q, want unknown coverage-incomplete", got.Verdict, got.Reason, got.Detail)
	}
}

// A CALM element that no binding reaches is a binding gap, never an isolated or unreachable element.
func TestEffectiveAccessUnboundElementIsABindingGap(t *testing.T) {
	t.Parallel()

	bindings := strings.Replace(effBindings, "name: db, namespace: shop", "name: other, namespace: shop", 1)

	run := (&effWorld{
		manifests: []string{effDenyAll}, evaluators: effNetworkEnforced, bindings: bindings,
		rules: []string{effNetRule("ACC-001", effAPIToDB, "denied")},
	}).run(t)

	got := effOne(t, run, "ACC-001")
	if got.Verdict != conformance.VerdictUnknown || got.Reason != conformance.ReasonBindingGap {
		t.Errorf("obligation = %s %q %q, want unknown binding-gap", got.Verdict, got.Reason, got.Detail)
	}
}

const effRBACBase = `---
apiVersion: v1
kind: ServiceAccount
metadata: {name: api-sa, namespace: shop}
---
apiVersion: v1
kind: Secret
metadata: {name: db-credentials, namespace: shop}
stringData: {password: not-a-real-value}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: read-secrets, namespace: shop}
rules:
  - {apiGroups: [""], resources: [secrets], verbs: [get]}
`

const effRBACBinding = `---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: api-read-secrets, namespace: shop}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: read-secrets}
subjects:
  - {kind: ServiceAccount, name: api-sa, namespace: shop}
`

// The authorization dimension asks what RBAC grants to the service account of the `from` workload on the resource that
// the `to` element is bound to: the RoleBinding grants `get` on secrets, so a rule that expects `not-granted` is
// violated and one that expects `granted` holds. Without the binding the result is `not-granted`, which counts only
// with the declared authorizer.
func TestEffectiveAccessAuthorization(t *testing.T) {
	t.Parallel()

	rules := []string{
		effAuthRule("ACC-001", effAPIToCredentials, "not-granted"),
		effAuthRule("ACC-002", effAPIToCredentials, "granted"),
	}

	t.Run("granted by a role binding", func(t *testing.T) {
		t.Parallel()

		run := (&effWorld{manifests: []string{effRBACBase, effRBACBinding}, evaluators: effRBACEnforced, rules: rules}).run(t)

		if got := effOne(t, run, "ACC-001"); got.Verdict != conformance.VerdictViolation || !strings.Contains(got.Detail, "api-read-secrets") {
			t.Errorf("not-granted: %s %q, want a violation that names the binding", got.Verdict, got.Detail)
		}

		if got := effOne(t, run, "ACC-002"); got.Verdict != conformance.VerdictPass {
			t.Errorf("granted: %s %q, want pass", got.Verdict, got.Detail)
		}
	})

	t.Run("no binding", func(t *testing.T) {
		t.Parallel()

		run := (&effWorld{manifests: []string{effRBACBase}, evaluators: effRBACEnforced, rules: rules}).run(t)

		if got := effOne(t, run, "ACC-001"); got.Verdict != conformance.VerdictPass {
			t.Errorf("not-granted: %s %q, want pass", got.Verdict, got.Detail)
		}

		if got := effOne(t, run, "ACC-002"); got.Verdict != conformance.VerdictViolation {
			t.Errorf("granted: %s %q, want violation", got.Verdict, got.Detail)
		}
	})

	t.Run("authorizer not declared", func(t *testing.T) {
		t.Parallel()

		run := (&effWorld{
			manifests: []string{effRBACBase}, rules: rules,
			evaluators: "  evaluators:\n    - {environment: production, kinds: [rbac]}\n",
		}).run(t)

		if got := effOne(t, run, "ACC-001"); got.Verdict != conformance.VerdictUnknown || got.Reason != conformance.ReasonEnforcementNotDeclared {
			t.Errorf("not-granted: %s %q %q, want unknown enforcement-not-declared", got.Verdict, got.Reason, got.Detail)
		}
	})
}

// Two runs over the same input give equal evaluations with equal digests (7.6), and the entries are sorted.
func TestEffectiveAccessEvaluationsAreDeterministic(t *testing.T) {
	t.Parallel()

	world := &effWorld{
		manifests: []string{effDenyAll, effAllowWebToDB}, evaluators: effNetworkEnforced,
		rules: []string{
			effNetRule("ACC-002", effAPIToDB, "denied", 8080),
			effNetRule("ACC-001", effWebToDB, "denied", 22, 8080),
		},
	}

	first, second := world.run(t), world.run(t)

	a, err := canonjson.Marshal(first.Assurance.Evaluations)
	if err != nil {
		t.Fatal(err)
	}

	b, err := canonjson.Marshal(second.Assurance.Evaluations)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(a, b) {
		t.Errorf("evaluations differ between two runs:\n%s\n%s", a, b)
	}

	if len(first.Assurance.Evaluations) == 0 {
		t.Fatal("no evaluations")
	}

	digests := map[string]struct{}{}
	for _, entry := range first.Assurance.Evaluations {
		digests[entry.InputDigest] = struct{}{}
	}

	if len(digests) != 1 {
		t.Errorf("input digests = %v, want one for one environment", slices.Sorted(maps.Keys(digests)))
	}

	if !slices.IsSortedFunc(first.Assurance.Evaluations, func(x, y conformance.EvaluationEntry) int {
		return cmp.Or(canonjson.CompareUTF16(x.Environment, y.Environment), canonjson.CompareUTF16(x.Kind, y.Kind), canonjson.CompareUTF16(x.Query, y.Query))
	}) {
		t.Error("evaluations are not sorted")
	}

	provGolden(t, "effective-evaluations.json", first.Assurance.Evaluations)
}

const effCilium = `---
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: db-l7
  namespace: shop
spec:
  endpointSelector:
    matchLabels: {app: db}
  ingress:
    - fromEndpoints:
        - matchLabels: {app: web}
`

// A policy kind that the evaluator does not support makes the results for the workloads it applies to `unknown`, and
// the run warns once with CO-PRV-013 for the environment, the evaluator, and the kind of gap, however many rules and
// queries meet it.
func TestEffectiveAccessUnsupportedPolicyKindWarnsOnce(t *testing.T) {
	t.Parallel()

	run := (&effWorld{
		manifests: []string{effDenyAll, effCilium}, evaluators: effNetworkEnforced,
		rules: []string{
			effNetRule("ACC-001", effAPIToDB, "denied"),
			effNetRule("ACC-002", effWebToDB, "denied", 8080),
		},
	}).run(t)

	for _, rule := range []string{"ACC-001", "ACC-002"} {
		got := effOne(t, run, rule)
		if got.Verdict != conformance.VerdictUnknown || got.Reason != conformance.ReasonCoverageIncomplete {
			t.Errorf("%s = %s %q %q, want unknown coverage-incomplete", rule, got.Verdict, got.Reason, got.Detail)
		}
	}

	warnings := effPRV013Warnings(run)
	if len(warnings) != 1 {
		t.Fatalf("CO-PRV-013 warnings = %q, want exactly one", warnings)
	}

	for _, want := range []string{"production", "network-policy", "unsupported-policy-kind", "CiliumNetworkPolicy"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning %q does not name %q", warnings[0], want)
		}
	}
}

// effPRV013Warnings are the messages of the CO-PRV-013 diagnostics of the run.
func effPRV013Warnings(run *conformance.Run) []string {
	var warnings []string

	for i := range run.Diagnostics {
		if run.Diagnostics[i].Code == diag.PRV013 {
			warnings = append(warnings, run.Diagnostics[i].Message)
		}
	}

	return warnings
}
