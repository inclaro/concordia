package netpol_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
)

func TestNetworkPolicySemantics(t *testing.T) {
	t.Parallel()

	for _, sc := range allScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			assertScenario(t, &sc)
		})
	}
}

func TestPerPairAndPortResults(t *testing.T) {
	t.Parallel()

	ev := newEval(build(t, `
metadata: {name: web-8080, namespace: shop}
spec:
  podSelector: {matchLabels: {app: api}}
  ingress:
    - from: [{podSelector: {matchLabels: {app: web}}}]
      ports: [{port: 8080}]
`))

	ssh := ev.Evaluate(q(wl("shop", "web"), wl("shop", "api"), 22))
	http := ev.Evaluate(q(wl("shop", "web"), wl("shop", "api"), 8080))

	if ssh.Verdict != netpol.Denied || http.Verdict != netpol.Allowed {
		t.Fatalf("port 22 = %s, port 8080 = %s", ssh.Verdict, http.Verdict)
	}
}

func TestIPBlockWithPodRanges(t *testing.T) {
	t.Parallel()

	policy := `
metadata: {name: web-out, namespace: shop}
spec:
  podSelector: {matchLabels: {app: web}}
  policyTypes: [Egress]
  egress: [{to: [{ipBlock: {cidr: 0.0.0.0/0, except: [10.0.0.0/8]}}]}]
`

	tests := []struct {
		name   string
		want   netpol.Verdict
		ranges []string
	}{
		{"except covers the pod network", netpol.Denied, []string{"10.244.0.0/16"}},
		{"block overlaps the pod network", netpol.Unknown, []string{"172.16.0.0/12"}},
		{"unreadable range proves nothing", netpol.Unknown, []string{"nonsense"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := build(t, policy)
			in.PodRanges = tt.ranges

			res := newEval(in).Evaluate(q(wl("shop", "web"), wl("shop", "db"), 5432))
			if res.Verdict != tt.want {
				t.Fatalf("verdict = %s, want %s (gaps %v)", res.Verdict, tt.want, res.Gaps)
			}
		})
	}
}

func TestUnsupportedKindsAreGapsForAffectedWorkloadsOnly(t *testing.T) {
	t.Parallel()

	in := build(t, allowWebToAPI)
	in.Foreign = []netpol.ForeignPolicy{{
		Kind: "CiliumNetworkPolicy", Namespace: "shop", Name: "api-l7",
		Selector: &evaluate.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
	}}

	ev := newEval(in)

	api := ev.Evaluate(q(wl("shop", "web"), wl("shop", "api"), 8080))
	if api.Verdict != netpol.Unknown || api.Reason != evaluate.ReasonUnsupportedConstruct {
		t.Fatalf("api = %s (%s)", api.Verdict, api.Reason)
	}

	if !isUnsupportedKindGap(&api) {
		t.Fatalf("gaps = %+v", api.Gaps)
	}

	// The policy does not select db, and no other policy is involved.
	if db := ev.Evaluate(q(wl("shop", "web"), wl("shop", "db"), 5432)); db.Verdict != netpol.Allowed {
		t.Fatalf("db = %s", db.Verdict)
	}

	// A native denial of the other end still holds: the source cannot reach anything.
	in.Policies = append(in.Policies, netpol.NetworkPolicy{
		Metadata: evaluate.ObjectMeta{Name: "web-lockdown", Namespace: "shop"},
		Spec: netpol.PolicySpec{
			PodSelector: evaluate.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			PolicyTypes: []string{netpol.PolicyTypeEgress},
		},
	})

	if denied := newEval(in).Evaluate(q(wl("shop", "web"), wl("shop", "api"), 8080)); denied.Verdict != netpol.Denied {
		t.Fatalf("denied at the source = %s", denied.Verdict)
	}
}

func TestClusterScopedForeignPolicyAffectsEveryNamespace(t *testing.T) {
	t.Parallel()

	in := build(t)
	in.Foreign = []netpol.ForeignPolicy{{Kind: "CiliumClusterwideNetworkPolicy", Name: "baseline"}}

	res := newEval(in).Evaluate(q(wl("free", "loner"), wl("ops", "probe"), 80))
	if res.Verdict != netpol.Unknown {
		t.Fatalf("verdict = %s", res.Verdict)
	}

	if !classifiesForeignKinds() {
		t.Error("IsForeignKind classifies kinds wrongly")
	}
}

// opsCoverage is the input of the denyAllAPI policy in which the namespace ops has the coverage c.
func opsCoverage(t *testing.T, c assurance.Coverage) netpol.Input {
	t.Helper()

	in := build(t, denyAllAPI)
	for i := range in.Namespaces {
		if in.Namespaces[i].Name == "ops" {
			in.Namespaces[i].Coverage = c
		}
	}

	return in
}

func TestCoverageGate(t *testing.T) {
	t.Parallel()

	for _, c := range []assurance.Coverage{assurance.CoverageFailed, assurance.CoveragePartial, ""} {
		ev := newEval(opsCoverage(t, c))

		res := ev.Evaluate(q(wl("ops", "probe"), wl("shop", "api"), 80))
		if res.Verdict != netpol.Unknown || res.Reason != evaluate.ReasonCoverageIncomplete {
			t.Fatalf("coverage %q: verdict = %s (%s)", c, res.Verdict, res.Reason)
		}

		if !isOpsCoverageGap(&res) {
			t.Fatalf("gaps = %+v", res.Gaps)
		}

		// Results between the other namespaces are still evaluated.
		if other := ev.Evaluate(q(wl("shop", "web"), wl("shop", "api"), 80)); other.Verdict != netpol.Denied {
			t.Fatalf("shop to shop = %s", other.Verdict)
		}

		if other := ev.Evaluate(q(wl("free", "loner"), wl("shop", "db"), 80)); other.Verdict != netpol.Allowed {
			t.Fatalf("free to shop = %s", other.Verdict)
		}
	}
}

func TestDeclaredEvidenceDoesNotRun(t *testing.T) {
	t.Parallel()

	in := build(t, denyAllAPI)
	in.Evidence = evaluate.EvidenceDeclared

	res := newEval(in).Evaluate(q(wl("shop", "web"), wl("shop", "api"), 80))
	if res.Verdict != netpol.Unknown || res.Reason != evaluate.ReasonRenderedEvidenceMissing {
		t.Fatalf("verdict = %s (%s)", res.Verdict, res.Reason)
	}
}

func TestMissingSubjects(t *testing.T) {
	t.Parallel()

	ev := newEval(build(t))

	tests := []struct {
		query *netpol.Query
		name  string
	}{
		{q(wl("shop", "ghost"), wl("shop", "api"), 80), "unknown workload"},
		{q(addr("10.0.0.1"), addr("10.0.0.2"), 80), "two addresses"},
		{q(addr("not-an-ip"), wl("shop", "api"), 80), "bad address"},
		{q(wl("shop", "web"), wl("shop", "api"), 0), "bad port"},
	}

	for _, tt := range tests {
		if res := ev.Evaluate(tt.query); res.Verdict != netpol.Unknown || len(res.Gaps) == 0 {
			t.Errorf("%s: %+v", tt.name, res)
		}
	}

	in := build(t)
	in.Workloads = append(in.Workloads, netpol.Workload{Namespace: "unrendered", Name: "x"})

	if res := newEval(in).Evaluate(q(wl("unrendered", "x"), wl("shop", "api"), 80)); res.Verdict != netpol.Unknown {
		t.Errorf("workload of a namespace outside the input: %s", res.Verdict)
	}
}

func TestDeterminism(t *testing.T) {
	t.Parallel()

	a := build(t, allowWebToAPI, allowProbeToAPI)
	b := build(t, allowProbeToAPI, allowWebToAPI)
	b.Workloads[0], b.Workloads[1] = b.Workloads[1], b.Workloads[0]

	if a.Digest() != b.Digest() {
		t.Error("the digest depends on input order")
	}

	if !strings.HasPrefix(a.Digest(), "sha256:") {
		t.Errorf("digest = %q", a.Digest())
	}

	c := build(t, allowWebToAPI)
	if a.Digest() == c.Digest() {
		t.Error("different inputs have the same digest")
	}

	query := q(wl("shop", "db"), wl("shop", "api"), 80)

	first, second := newEval(a).Evaluate(query), newEval(b).Evaluate(query)
	if names(first.Policies) != names(second.Policies) || first.Verdict != second.Verdict {
		t.Errorf("results differ: %+v vs %+v", first, second)
	}
}

func TestContract(t *testing.T) {
	t.Parallel()

	c := netpol.Contract()
	if !enablesNetworkIsolation(&c) {
		t.Fatalf("contract = %+v", c)
	}

	if !requiresRenderedAndAssumesEnforcement(&c) {
		t.Fatalf("contract = %+v", c)
	}

	again := netpol.Contract()
	if c.Digest() != again.Digest() {
		t.Error("the contract digest is not stable")
	}
}
