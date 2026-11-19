package netpol_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
)

// world is the fixture: namespaces shop (web, api, db), ops (probe), and a policy-free namespace free.
const worldYAML = `
namespaces:
  - {name: shop, labels: {team: shop}, coverage: complete}
  - {name: ops, labels: {team: ops, env: prod}, coverage: complete}
  - {name: free, coverage: complete}
workloads:
  - {namespace: shop, name: web, labels: {app: web, tier: front}}
  - namespace: shop
    name: api
    labels: {app: api, tier: back}
    ports:
      - {name: http, containerPort: 8080}
      - {name: metrics, containerPort: 9090}
  - {namespace: shop, name: db, labels: {app: db, tier: back}, ports: [{name: pg, containerPort: 5432}]}
  - {namespace: ops, name: probe, labels: {app: probe}}
  - {namespace: free, name: loner, labels: {app: loner}}
`

func build(t *testing.T, policies ...string) netpol.Input {
	t.Helper()

	var in netpol.Input
	if err := yaml.Unmarshal([]byte(worldYAML), &in); err != nil {
		t.Fatalf("world: %v", err)
	}

	in.Evidence = evaluate.EvidenceRendered

	for _, doc := range policies {
		var p netpol.NetworkPolicy
		if err := yaml.Unmarshal([]byte(doc), &p); err != nil {
			t.Fatalf("policy: %v\n%s", err, doc)
		}

		in.Policies = append(in.Policies, p)
	}

	return in
}

//nolint:gocritic // hugeParam: the helper takes a copy on purpose, so a test can change its input afterwards.
func newEval(in netpol.Input) *netpol.Evaluator { return netpol.New(&in) }

func wl(ns, name string) netpol.Endpoint { return netpol.Endpoint{Namespace: ns, Workload: name} }

func addr(a string) netpol.Endpoint { return netpol.Endpoint{Address: a} }

func names(refs []netpol.PolicyRef) string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name+":"+r.Direction)
	}

	return strings.Join(out, ",")
}

type scenario struct {
	name string
	// deciders is the expected `name:direction` list of the deciding policies, "-" when not checked.
	deciders string
	want     netpol.Verdict
	query    *netpol.Query
	policies []string
}

func q(from, to netpol.Endpoint, port int) *netpol.Query {
	return &netpol.Query{From: from, To: to, Port: port}
}

// hasReasonAndGap is whether an unknown result explains itself.
func hasReasonAndGap(res *netpol.Result) bool {
	return len(res.Gaps) != 0 && res.Reason != ""
}

// hasOnlyEnforcementAssumption is whether a decided result has no gaps and rests on one assumption: that the cluster
// enforces network policies.
func hasOnlyEnforcementAssumption(res *netpol.Result) bool {
	if len(res.Gaps) != 0 || len(res.Assumptions) != 1 {
		return false
	}

	return res.Assumptions[0] == "network-policy-enforced"
}

// assertDeciders checks the deciding policies of a result, unless the scenario does not name them.
func assertDeciders(t *testing.T, res *netpol.Result, want string) {
	t.Helper()

	if want == "-" {
		return
	}

	if got := names(res.Policies); got != want {
		t.Errorf("deciding policies = %q, want %q", got, want)
	}
}

// assertExplained checks that a result gives the reason, gaps and assumptions that fit its verdict.
func assertExplained(t *testing.T, res *netpol.Result) {
	t.Helper()

	if res.Verdict == netpol.Unknown {
		if !hasReasonAndGap(res) {
			t.Errorf("unknown result without reason or gap: %+v", res)
		}

		return
	}

	if !hasOnlyEnforcementAssumption(res) {
		t.Errorf("assumptions = %v, gaps = %v", res.Assumptions, res.Gaps)
	}
}

// assertScenario evaluates the query of the scenario against its policies and checks the whole result.
func assertScenario(t *testing.T, sc *scenario) {
	t.Helper()

	res := newEval(build(t, sc.policies...)).Evaluate(sc.query)
	if res.Verdict != sc.want {
		t.Fatalf("verdict = %s (reason %q, gaps %v), want %s", res.Verdict, res.Reason, res.Gaps, sc.want)
	}

	assertDeciders(t, &res, sc.deciders)

	if res.Evaluator != netpol.Identifier || res.Version != netpol.Version {
		t.Errorf("evaluator identity = %s %s", res.Evaluator, res.Version)
	}

	assertExplained(t, &res)

	if len(res.Limits) == 0 {
		t.Error("result states no limits")
	}
}

// isUnsupportedKindGap is whether the result has exactly one gap, for the unsupported policy of the foreign kind.
func isUnsupportedKindGap(res *netpol.Result) bool {
	if len(res.Gaps) != 1 || res.Gaps[0].Kind != evaluate.GapUnsupportedKind {
		return false
	}

	return strings.Contains(res.Gaps[0].Detail, "CiliumNetworkPolicy shop/api-l7")
}

// isOpsCoverageGap is whether the result has exactly one gap, for the coverage of the ops namespace.
func isOpsCoverageGap(res *netpol.Result) bool {
	if len(res.Gaps) != 1 || res.Gaps[0].Kind != evaluate.GapNamespaceCoverage {
		return false
	}

	return strings.Contains(res.Gaps[0].Detail, "ops")
}

// classifiesForeignKinds is whether IsForeignKind accepts a Cilium kind and rejects the native and a core kind.
func classifiesForeignKinds() bool {
	if !netpol.IsForeignKind("CiliumNetworkPolicy") {
		return false
	}

	return !netpol.IsForeignKind("NetworkPolicy") && !netpol.IsForeignKind("Deployment")
}

// enablesNetworkIsolation is whether the contract has the ID, a version, and the one claim of the evaluator.
func enablesNetworkIsolation(c *evaluate.Contract) bool {
	if c.ID != "network-policy" || c.Version == "" {
		return false
	}

	return len(c.Enables) == 1 && c.Enables[0] == netpol.ClaimNetworkIsolation
}

// requiresRenderedAndAssumesEnforcement is whether the contract needs rendered evidence, assumes that the cluster
// enforces policies, and states limits.
func requiresRenderedAndAssumesEnforcement(c *evaluate.Contract) bool {
	if c.RequiresEvidence != evaluate.EvidenceRendered || len(c.Limits) == 0 {
		return false
	}

	return len(c.Assumes) == 1 && c.Assumes[0] == "network-policy-enforced"
}
