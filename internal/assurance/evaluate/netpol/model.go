// Package netpol evaluates networking.k8s.io/v1 NetworkPolicy semantics over rendered documents (design D8). It
// answers, for a source, a destination, a port, and a protocol, whether traffic is `allowed`, `denied`, or `unknown`,
// and names the policies that decided the answer. The input model is plain Go structs that mirror the Kubernetes
// field names, so the integrator can decode the rendered documents straight into them.
package netpol

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/canonjson"
)

// Policy types of `spec.policyTypes`.
const (
	PolicyTypeIngress = "Ingress"
	PolicyTypeEgress  = "Egress"
)

// Protocols of a rule port and of a query.
const (
	ProtocolTCP  = "TCP"
	ProtocolUDP  = "UDP"
	ProtocolSCTP = "SCTP"
)

// Input is the rendered evidence of one environment that the evaluator reads.
type Input struct {
	// Evidence is the evidence level (`evaluate.EvidenceRendered`); declared-level input is never evaluated.
	Evidence string `json:"evidence" yaml:"evidence"`
	// Namespaces lists every namespace of the render with its labels and coverage. A namespace that is not listed is
	// treated as not rendered.
	Namespaces []Namespace `json:"namespaces" yaml:"namespaces"`
	// Workloads are the pod templates (Pod, Deployment, StatefulSet, ...) with the labels and container ports of the pods.
	Workloads []Workload `json:"workloads" yaml:"workloads"`
	// Policies are the NetworkPolicy documents.
	Policies []NetworkPolicy `json:"policies" yaml:"policies"`
	// Foreign are policy documents of other kinds (CiliumNetworkPolicy, CalicoNetworkPolicy, AdminNetworkPolicy, ...).
	// They are not evaluated; a workload that one of them selects makes its results unknown.
	Foreign []ForeignPolicy `json:"foreign,omitempty" yaml:"foreign,omitempty"`
	// PodRanges are the CIDRs of the cluster's pod and service networks, if the environment declares them. Without
	// them, an `ipBlock` is cluster-dependent for traffic between workloads: it may or may not contain pod addresses.
	PodRanges []string `json:"podRanges,omitempty" yaml:"podRanges,omitempty"`
}

// Namespace is one namespace of the render.
type Namespace struct {
	Name   string            `json:"name" yaml:"name"`
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	// Coverage is the render coverage of the namespace. Only `complete` allows results; the empty value counts as
	// incomplete so that a forgotten field cannot produce a pass.
	Coverage assurance.Coverage `json:"coverage" yaml:"coverage"`
}

// Workload is one pod template as the policies see it: namespace, labels, and container ports.
type Workload struct {
	Kind      string            `json:"kind,omitempty" yaml:"kind,omitempty"`
	Namespace string            `json:"namespace" yaml:"namespace"`
	Name      string            `json:"name" yaml:"name"`
	Labels    map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Ports     []ContainerPort   `json:"ports,omitempty" yaml:"ports,omitempty"`
}

// ContainerPort is one `containers[].ports[]` entry.
type ContainerPort struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// Protocol defaults to TCP.
	Protocol      string `json:"protocol,omitempty" yaml:"protocol,omitempty"`
	ContainerPort int    `json:"containerPort" yaml:"containerPort"`
}

// NetworkPolicy is a networking.k8s.io/v1 NetworkPolicy.
type NetworkPolicy struct {
	Metadata evaluate.ObjectMeta `json:"metadata" yaml:"metadata"`
	Spec     PolicySpec          `json:"spec" yaml:"spec"`
}

// PolicySpec is `spec` of a NetworkPolicy.
type PolicySpec struct {
	PodSelector evaluate.LabelSelector `json:"podSelector" yaml:"podSelector"`
	PolicyTypes []string               `json:"policyTypes,omitempty" yaml:"policyTypes,omitempty"`
	Ingress     []IngressRule          `json:"ingress,omitempty" yaml:"ingress,omitempty"`
	Egress      []EgressRule           `json:"egress,omitempty" yaml:"egress,omitempty"`
}

// IngressRule allows traffic from `From` on `Ports`. An empty list matches everything.
type IngressRule struct {
	From  []Peer `json:"from,omitempty" yaml:"from,omitempty"`
	Ports []Port `json:"ports,omitempty" yaml:"ports,omitempty"`
}

// EgressRule allows traffic to `To` on `Ports`. An empty list matches everything.
type EgressRule struct {
	To    []Peer `json:"to,omitempty" yaml:"to,omitempty"`
	Ports []Port `json:"ports,omitempty" yaml:"ports,omitempty"`
}

// Peer is one `from` or `to` entry: a pod selector, a namespace selector, both, or an IP block.
type Peer struct {
	PodSelector       *evaluate.LabelSelector `json:"podSelector,omitempty" yaml:"podSelector,omitempty"`
	NamespaceSelector *evaluate.LabelSelector `json:"namespaceSelector,omitempty" yaml:"namespaceSelector,omitempty"`
	IPBlock           *IPBlock                `json:"ipBlock,omitempty" yaml:"ipBlock,omitempty"`
}

// IPBlock is a CIDR with excluded sub-ranges.
type IPBlock struct {
	CIDR   string   `json:"cidr" yaml:"cidr"`
	Except []string `json:"except,omitempty" yaml:"except,omitempty"`
}

// Port is one rule port: a protocol (default TCP), an optional port number or name, and an optional end of a range.
type Port struct {
	Port     *IntOrString `json:"port,omitempty" yaml:"port,omitempty"`
	EndPort  *int         `json:"endPort,omitempty" yaml:"endPort,omitempty"`
	Protocol string       `json:"protocol,omitempty" yaml:"protocol,omitempty"`
}

// IntOrString is a port number or a port name, as in the Kubernetes `intstr.IntOrString`.
type IntOrString struct {
	Name   string
	Number int
	// IsName is true when the value is a name.
	IsName bool
}

// PortNumber is an IntOrString that holds a number.
func PortNumber(n int) *IntOrString { return &IntOrString{Number: n} }

// PortName is an IntOrString that holds a name.
func PortName(name string) *IntOrString { return &IntOrString{Name: name, IsName: true} }

// MarshalJSON writes a number or a string.
func (v IntOrString) MarshalJSON() ([]byte, error) {
	if v.IsName {
		return json.Marshal(v.Name)
	}

	return json.Marshal(v.Number)
}

// UnmarshalJSON reads a number or a string.
func (v *IntOrString) UnmarshalJSON(data []byte) error {
	var number int
	if err := json.Unmarshal(data, &number); err == nil {
		*v = IntOrString{Number: number}

		return nil
	}

	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return fmt.Errorf("port must be a number or a name: %w", err)
	}

	*v = IntOrString{Name: name, IsName: true}

	return nil
}

// MarshalYAML writes a number or a string.
func (v IntOrString) MarshalYAML() (any, error) {
	if v.IsName {
		return v.Name, nil
	}

	return v.Number, nil
}

// UnmarshalYAML reads a number or a string; a quoted number stays a name, as in Kubernetes.
func (v *IntOrString) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return errors.New("port must be a number or a name")
	}

	if node.Tag == "!!int" {
		n, err := strconv.Atoi(node.Value)
		if err != nil {
			return fmt.Errorf("port %q: %w", node.Value, err)
		}

		*v = IntOrString{Number: n}

		return nil
	}

	*v = IntOrString{Name: node.Value, IsName: true}

	return nil
}

// ForeignPolicy is a policy document of a kind that the evaluator does not support.
type ForeignPolicy struct {
	// Selector is the endpoint selector of the policy when the reader could read it as a label selector. Nil means
	// that the policy is treated as selecting every workload of its scope, which can only make results unknown.
	Selector *evaluate.LabelSelector `json:"selector,omitempty" yaml:"selector,omitempty"`
	Kind     string                  `json:"kind" yaml:"kind"`
	// Namespace is empty for a cluster-scoped kind, which then applies to every namespace.
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Name      string `json:"name" yaml:"name"`
}

// IsForeignKind reports whether a document kind is a network policy kind of another CNI or API group that the
// evaluator does not support (CiliumNetworkPolicy, GlobalNetworkPolicy, AdminNetworkPolicy, ...). The standard
// `NetworkPolicy` is not foreign.
func IsForeignKind(kind string) bool {
	return kind != "NetworkPolicy" && strings.HasSuffix(kind, "NetworkPolicy")
}

// Digest is the digest of the input ("sha256:<hex>" of the canonical JSON of the input in a fixed order): the same
// rendered input gives the same digest.
func (in *Input) Digest() string {
	norm := *in
	norm.Namespaces = sortedBy(in.Namespaces, func(n Namespace) string { return n.Name })
	norm.Workloads = sortedBy(in.Workloads, func(w Workload) string { return w.Namespace + "/" + w.Name + "/" + w.Kind })
	norm.Policies = sortedBy(in.Policies, func(p NetworkPolicy) string { return p.Metadata.Namespace + "/" + p.Metadata.Name })
	norm.Foreign = sortedBy(in.Foreign, func(p ForeignPolicy) string { return p.Kind + "/" + p.Namespace + "/" + p.Name })
	norm.PodRanges = sortedBy(in.PodRanges, func(s string) string { return s })

	return evaluate.DigestOf(norm)
}

// sortedBy returns a copy of items ordered by key with the UTF-16 comparator that all canonical output uses.
func sortedBy[T any](items []T, key func(T) string) []T {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b T) int { return canonjson.CompareUTF16(key(a), key(b)) })

	return out
}
