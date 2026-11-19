package netpol_test

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
)

// rank orders verdicts by how much they allow.
func rank(v netpol.Verdict) int {
	switch v {
	case netpol.Denied:
		return 0
	case netpol.Unknown:
		return 1
	case netpol.Allowed:
		return 2
	}

	return -1
}

// maxGenerated bounds the number of generated ports and peers.
const maxGenerated = 3

type gen struct{ r *rand.Rand }

func (g gen) pick(items ...string) string { return items[g.r.IntN(len(items))] }

func (g gen) selector() evaluate.LabelSelector {
	switch g.r.IntN(5) {
	case 0:
		return evaluate.LabelSelector{}
	case 1:
		return evaluate.LabelSelector{MatchLabels: map[string]string{"app": g.pick("a", "b", "c")}}
	case 2:
		return evaluate.LabelSelector{MatchExpressions: []evaluate.Requirement{{Key: "tier", Operator: evaluate.OpIn, Values: []string{g.pick("x", "y")}}}}
	case 3:
		return evaluate.LabelSelector{MatchExpressions: []evaluate.Requirement{{Key: "app", Operator: evaluate.OpNotIn, Values: []string{g.pick("a", "b")}}}}
	default:
		// Rejected by Kubernetes: the evaluator must treat it as unknown.
		return evaluate.LabelSelector{MatchExpressions: []evaluate.Requirement{{Key: "app", Operator: "Bogus"}}}
	}
}

func (g gen) peer() netpol.Peer {
	sel := func() *evaluate.LabelSelector { s := g.selector(); return &s }

	switch g.r.IntN(5) {
	case 0:
		return netpol.Peer{PodSelector: sel()}
	case 1:
		return netpol.Peer{NamespaceSelector: sel()}
	case 2:
		return netpol.Peer{NamespaceSelector: sel(), PodSelector: sel()}
	case 3:
		return netpol.Peer{IPBlock: &netpol.IPBlock{CIDR: g.pick("0.0.0.0/0", "10.0.0.0/8", "192.0.2.0/24", "bad")}}
	default:
		return netpol.Peer{}
	}
}

func (g gen) ports() []netpol.Port {
	ports := make([]netpol.Port, 0, maxGenerated)

	for range g.r.IntN(maxGenerated) {
		p := netpol.Port{Protocol: g.pick("", "TCP", "UDP")}

		switch g.r.IntN(4) {
		case 0:
		case 1:
			p.Port = netpol.PortNumber(80 + g.r.IntN(3))
		case 2:
			p.Port = netpol.PortNumber(80)
			end := 82
			p.EndPort = &end
		default:
			p.Port = netpol.PortName(g.pick("http", "ghost"))
		}

		ports = append(ports, p)
	}

	return ports
}

func (g gen) peers() []netpol.Peer {
	peers := make([]netpol.Peer, 0, maxGenerated)
	for range g.r.IntN(maxGenerated) {
		peers = append(peers, g.peer())
	}

	return peers
}

func (g gen) rule() (netpol.IngressRule, netpol.EgressRule) {
	peers, ports := g.peers(), g.ports()

	return netpol.IngressRule{From: peers, Ports: ports}, netpol.EgressRule{To: peers, Ports: ports}
}

func (g gen) policy(i int) netpol.NetworkPolicy {
	p := netpol.NetworkPolicy{
		Metadata: evaluate.ObjectMeta{Name: fmt.Sprintf("p%d", i), Namespace: g.pick("n1", "n2")},
		Spec:     netpol.PolicySpec{PodSelector: g.selector()},
	}

	switch g.r.IntN(3) {
	case 0:
		p.Spec.PolicyTypes = []string{netpol.PolicyTypeIngress}
	case 1:
		p.Spec.PolicyTypes = []string{netpol.PolicyTypeEgress}
	default:
		p.Spec.PolicyTypes = []string{netpol.PolicyTypeIngress, netpol.PolicyTypeEgress}
	}

	for range g.r.IntN(3) {
		in, out := g.rule()
		p.Spec.Ingress = append(p.Spec.Ingress, in)
		p.Spec.Egress = append(p.Spec.Egress, out)
	}

	return p
}

func (g gen) world() netpol.Input {
	in := netpol.Input{
		Evidence: evaluate.EvidenceRendered,
		Namespaces: []netpol.Namespace{
			{Name: "n1", Labels: map[string]string{"zone": "z1"}, Coverage: assurance.CoverageComplete},
			{Name: "n2", Labels: map[string]string{"zone": "z2"}, Coverage: assurance.CoverageComplete},
		},
	}

	for i := range 6 {
		in.Workloads = append(in.Workloads, netpol.Workload{
			Namespace: g.pick("n1", "n2"), Name: fmt.Sprintf("w%d", i),
			Labels: map[string]string{"app": g.pick("a", "b", "c"), "tier": g.pick("x", "y")},
			Ports:  []netpol.ContainerPort{{Name: "http", ContainerPort: 80 + g.r.IntN(3)}},
		})
	}

	for i := range 1 + g.r.IntN(5) {
		in.Policies = append(in.Policies, g.policy(i))
	}

	return in
}

// TestAddingAnAllowRuleNeverRestrictsTraffic checks the monotonicity of the evaluator: when a rule is added to a
// policy, no query gets a more restrictive verdict (allowed -> denied or unknown, unknown -> denied). A policy that
// is added can restrict (it selects pods that were open before); a rule cannot.
func TestAddingAnAllowRuleNeverRestrictsTraffic(t *testing.T) {
	t.Parallel()

	g := gen{r: rand.New(rand.NewPCG(7, 11))}
	// opened counts the queries that a rule made more permissive; without any the property would hold vacuously.
	opened := 0

	for iteration := range 400 {
		before := g.world()
		after := before
		after.Policies = append([]netpol.NetworkPolicy(nil), before.Policies...)

		idx := g.r.IntN(len(after.Policies))
		pol := after.Policies[idx]
		in, out := g.rule()
		pol.Spec.Ingress = append(append([]netpol.IngressRule(nil), pol.Spec.Ingress...), in)
		pol.Spec.Egress = append(append([]netpol.EgressRule(nil), pol.Spec.Egress...), out)
		after.Policies[idx] = pol

		evBefore, evAfter := newEval(before), newEval(after)

		for _, from := range before.Workloads {
			for _, to := range before.Workloads {
				for _, port := range []int{80, 81, 82, 443} {
					query := q(wl(from.Namespace, from.Name), wl(to.Namespace, to.Name), port)
					b, a := evBefore.Evaluate(query), evAfter.Evaluate(query)

					if rank(a.Verdict) < rank(b.Verdict) {
						t.Fatalf("iteration %d: %s/%s -> %s/%s:%d went from %s to %s after adding a rule to %s\npolicy: %+v",
							iteration, from.Namespace, from.Name, to.Namespace, to.Name, port, b.Verdict, a.Verdict,
							pol.Metadata.Name, pol)
					}

					if rank(a.Verdict) > rank(b.Verdict) {
						opened++
					}
				}
			}
		}
	}

	if opened == 0 {
		t.Fatal("no generated rule changed any verdict: the generator does not exercise the property")
	}
}

// TestAllowAllPolicyKeepsAllowedResults checks that adding a policy that allows everything for the pods it selects never
// turns an allowed result into denied.
func TestAllowAllPolicyKeepsAllowedResults(t *testing.T) {
	t.Parallel()

	g := gen{r: rand.New(rand.NewPCG(3, 5))}

	for range 200 {
		before := g.world()
		after := before
		after.Policies = append(append([]netpol.NetworkPolicy(nil), before.Policies...), netpol.NetworkPolicy{
			Metadata: evaluate.ObjectMeta{Name: "allow-all", Namespace: g.pick("n1", "n2")},
			Spec: netpol.PolicySpec{
				PodSelector: g.selector(),
				PolicyTypes: []string{netpol.PolicyTypeIngress, netpol.PolicyTypeEgress},
				Ingress:     []netpol.IngressRule{{}},
				Egress:      []netpol.EgressRule{{}},
			},
		})

		evBefore, evAfter := newEval(before), newEval(after)

		for _, from := range before.Workloads {
			for _, to := range before.Workloads {
				query := q(wl(from.Namespace, from.Name), wl(to.Namespace, to.Name), 80)
				if b, a := evBefore.Evaluate(query), evAfter.Evaluate(query); b.Verdict == netpol.Allowed && a.Verdict == netpol.Denied {
					t.Fatalf("%s -> %s: allowed became denied", from.Name, to.Name)
				}
			}
		}
	}
}
