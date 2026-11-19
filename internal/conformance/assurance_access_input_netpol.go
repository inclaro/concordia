package conformance

import (
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/render"
)

// selectorOf converts a rendered selector. The conversion keeps an unknown operator as written, so that the evaluator
// reports it as an invalid selector and the result is `unknown`.
func selectorOf(sel *render.LabelSelector) *evaluate.LabelSelector {
	if sel == nil {
		return nil
	}

	out := &evaluate.LabelSelector{MatchLabels: sel.MatchLabels}
	for _, req := range sel.MatchExpressions {
		out.MatchExpressions = append(out.MatchExpressions, evaluate.Requirement{Key: req.Key, Operator: req.Operator, Values: req.Values})
	}

	return out
}

func portOf(ref *render.PortRef) *netpol.IntOrString {
	if ref == nil {
		return nil
	}

	if ref.IsNamed() {
		return netpol.PortName(ref.Name)
	}

	return netpol.PortNumber(ref.Number)
}

func netpolPorts(ports []render.NetworkPolicyPort) []netpol.Port {
	out := make([]netpol.Port, 0, len(ports))

	for i := range ports {
		out = append(out, netpol.Port{Port: portOf(ports[i].Port), EndPort: ports[i].EndPort, Protocol: ports[i].Protocol})
	}

	return out
}

func netpolPeers(peers []render.NetworkPolicyPeer) []netpol.Peer {
	out := make([]netpol.Peer, 0, len(peers))

	for i := range peers {
		peer := netpol.Peer{PodSelector: selectorOf(peers[i].PodSelector), NamespaceSelector: selectorOf(peers[i].NamespaceSelector)}
		if block := peers[i].IPBlock; block != nil {
			peer.IPBlock = &netpol.IPBlock{CIDR: block.CIDR, Except: block.Except}
		}

		out = append(out, peer)
	}

	return out
}

// netpolPolicy converts one rendered NetworkPolicy.
func netpolPolicy(resource *render.Resource) netpol.NetworkPolicy {
	spec := resource.NetworkPolicy
	policy := netpol.NetworkPolicy{
		Metadata: evaluate.ObjectMeta{Name: resource.ID.Name, Namespace: resource.ID.Namespace, Labels: resource.Labels},
		Spec: netpol.PolicySpec{
			PodSelector: *selectorOf(&spec.PodSelector),
			PolicyTypes: spec.PolicyTypes,
		},
	}

	for i := range spec.Ingress {
		policy.Spec.Ingress = append(policy.Spec.Ingress, netpol.IngressRule{From: netpolPeers(spec.Ingress[i].Peers), Ports: netpolPorts(spec.Ingress[i].Ports)})
	}

	for i := range spec.Egress {
		policy.Spec.Egress = append(policy.Spec.Egress, netpol.EgressRule{To: netpolPeers(spec.Egress[i].Peers), Ports: netpolPorts(spec.Egress[i].Ports)})
	}

	return policy
}

// netpolForeignOf names a resource that the network evaluator cannot read as a policy.
func netpolForeignOf(ident *render.ResourceID) netpol.ForeignPolicy {
	return netpol.ForeignPolicy{Kind: ident.Kind, Namespace: ident.Namespace, Name: ident.Name}
}

// netpolContainerPorts gives the declared ports of the containers of each workload of the environment.
func (m *envModel) netpolContainerPorts() map[render.ResourceID][]netpol.ContainerPort {
	ports := map[render.ResourceID][]netpol.ContainerPort{}

	for _, result := range m.renders {
		for i := range result.Occurrences {
			occ := &result.Occurrences[i]
			if occ.Kind != render.KindContainer {
				continue
			}

			for _, port := range occ.Ports {
				ports[occ.Workload] = append(ports[occ.Workload], netpol.ContainerPort{Name: port.Name, Protocol: port.Protocol, ContainerPort: port.ContainerPort})
			}
		}
	}

	return ports
}

// netpolCollector gathers the resources of one environment into the input of the network evaluator.
type netpolCollector struct {
	input  *netpol.Input
	labels map[string]map[string]string
	ports  map[render.ResourceID][]netpol.ContainerPort
}

// add files one rendered resource under the input collection it belongs to.
func (c *netpolCollector) add(resource *render.Resource) {
	ident := &resource.ID

	switch {
	case ident.Kind == "Namespace" && ident.Group == "":
		c.labels[ident.Name] = resource.Labels
	case resource.Workload != nil:
		c.input.Workloads = append(c.input.Workloads, netpol.Workload{
			Kind: ident.Kind, Namespace: ident.Namespace, Name: ident.Name,
			Labels: resource.Workload.PodLabels, Ports: c.ports[*ident],
		})
	case resource.NetworkPolicy != nil:
		c.addPolicy(resource)
	case netpol.IsForeignKind(ident.Kind):
		c.input.Foreign = append(c.input.Foreign, netpolForeignOf(ident))
	}
}

// addPolicy files a rendered NetworkPolicy. A policy that the reader could not interpret is an unsupported construct.
// With no selector it is treated as selecting every workload of its namespace, which can only make results unknown.
func (c *netpolCollector) addPolicy(resource *render.Resource) {
	if len(resource.NetworkPolicy.Unsupported) > 0 {
		c.input.Foreign = append(c.input.Foreign, netpolForeignOf(&resource.ID))

		return
	}

	c.input.Policies = append(c.input.Policies, netpolPolicy(resource))
}

// netpolInput builds the input of the network evaluator for one environment. The coverage of a namespace is the one of
// its renders, and only `complete` allows results (the evaluator treats everything else, and a namespace that is not
// listed, as not rendered).
func (m *envModel) netpolInput() *netpol.Input {
	collector := &netpolCollector{
		input:  &netpol.Input{Evidence: evaluate.EvidenceRendered},
		labels: map[string]map[string]string{},
		ports:  m.netpolContainerPorts(),
	}

	m.resources(func(_ *render.Result, resource *render.Resource) { collector.add(resource) })

	for _, name := range m.namespaceList() {
		collector.input.Namespaces = append(collector.input.Namespaces, netpol.Namespace{Name: name, Labels: collector.labels[name], Coverage: m.namespaces[name]})
	}

	return collector.input
}
