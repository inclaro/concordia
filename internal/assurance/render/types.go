package render

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/redact"
)

// Confinement levels of one render, recorded in the result (design D4 layer 7).
const (
	// ConfinementNone is a source that needs no program (plain manifests).
	ConfinementNone = "none"
	// ConfinementProcess is a renderer that ran under layers 1-6: fixed arguments, pinned binary, scrubbed
	// environment, confined copy, pre-scan, limits.
	ConfinementProcess = "process"
	// ConfinementProcessOS is ConfinementProcess plus the OS sandbox (network unshared, read-only binds).
	ConfinementProcessOS = "process+os"
)

// Renderer identity names: the lockfile keys.
const (
	ToolHelm      = "helm"
	ToolKustomize = "kustomize"
)

// Identity is the identity of a renderer executable: the run manifest and the lockfile pin record it.
type Identity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// SHA256 is the digest of the executable in the repository form `sha256:<hex>` (digest.OfFile).
	SHA256 string `json:"sha256"`
}

// Pin is the lockfile pin of one renderer (`resolved.renderers.<tool>`): version and executable digest.
type Pin struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// Pins are the renderer pins of the lockfile by tool name (`helm`, `kustomize`). A missing entry means the tool is
// not pinned.
type Pins map[string]Pin

// ResourceID is the scoped identity of a rendered resource. Two environments keep two identities for one kind,
// namespace, and name. Namespace is empty for cluster-scoped kinds.
type ResourceID struct {
	Environment string `json:"environment"`
	// Cluster is the cluster or context declared for the environment, empty when none is declared.
	Cluster   string `json:"cluster,omitempty"`
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// String is the identity as one stable text, the sort and conflict key.
func (id ResourceID) String() string {
	return strings.Join([]string{id.Environment, id.Cluster, id.Group, id.Kind, id.Namespace, id.Name}, "/")
}

// CompareResourceIDs orders identities by their text in UTF-16 order. It takes values so that it fits slices.SortFunc.
//
//nolint:gocritic // hugeParam: the signature is the one of slices.SortFunc.
func CompareResourceIDs(a, b ResourceID) int { return canonjson.CompareUTF16(a.String(), b.String()) }

// Resource is one rendered document with its identity and the typed facts of its kind. Kinds other than the typed
// ones are inventoried by identity only.
type Resource struct {
	// Document is the redacted document, for readers inside the process. It is never serialized: results and
	// bundles carry digests and typed facts, not rendered values.
	Document    map[string]any    `json:"-"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`

	Workload       *Workload       `json:"workload,omitempty"`
	Service        *Service        `json:"service,omitempty"`
	NetworkPolicy  *NetworkPolicy  `json:"networkPolicy,omitempty"`
	Route          *Route          `json:"route,omitempty"`
	Gateway        *Gateway        `json:"gateway,omitempty"`
	ServiceAccount *ServiceAccount `json:"serviceAccount,omitempty"`
	Role           *Role           `json:"role,omitempty"`
	Binding        *Binding        `json:"binding,omitempty"`

	ID         ResourceID `json:"id"`
	APIVersion string     `json:"apiVersion"`
	// Hook is the value of the `helm.sh/hook` annotation: the hook stays a resource and is never executed.
	Hook string `json:"hook,omitempty"`
	// Digest is the sha256 of the redacted canonical document.
	Digest string `json:"digest"`
	// Location is the repository file the resource came from: the template or manifest file when it is known (a Helm
	// `# Source:` line, the plain file), else the declared source path. It is never a temporary path.
	Location string `json:"location"`
	// DataKeys are the key names of a Secret or ConfigMap `data`, `stringData`, and `binaryData`. The values are
	// never kept.
	DataKeys []string `json:"dataKeys,omitempty"`
	// Conflict is true when another document of the same render has this identity. Claims about the resource are
	// unknown then, and it is not counted as two instances.
	Conflict bool `json:"conflict,omitempty"`
}

// Container kinds.
const (
	KindContainer = "container"
	KindInit      = "init"
	KindEphemeral = "ephemeral"
)

// ContainerOccurrence is one container, init container, or ephemeral container of a rendered workload, with the
// image parts kept separate. Occurrences come from the rendered documents, never from code-graph image edges, which
// merge two containers that use one repository (CBM 0.12.1 observations, O4).
type ContainerOccurrence struct {
	Workload ResourceID `json:"workload"`
	ImageRef
	// Container is the container name.
	Container string `json:"container"`
	// Kind is `container`, `init`, or `ephemeral`.
	Kind string `json:"kind"`
	// Ports are the declared container ports; named ports of NetworkPolicy rules resolve against them.
	Ports []ContainerPort `json:"ports,omitempty"`
	// Immutable is true when the image has a digest. A tag alone is not a stable version.
	Immutable bool `json:"immutable"`
}

// ContainerPort is a declared container port.
type ContainerPort struct {
	Name string `json:"name,omitempty"`
	// Protocol is TCP when the document leaves it out.
	Protocol      string `json:"protocol"`
	ContainerPort int    `json:"containerPort"`
}

// Workload holds the pod facts of a Pod-bearing kind (Pod, Deployment, StatefulSet, DaemonSet, ReplicaSet, Job,
// CronJob).
type Workload struct {
	// PodLabels are the labels of the pod: the metadata of a Pod, or of the pod template.
	PodLabels map[string]string `json:"podLabels,omitempty"`
	// ServiceAccountName is the account the pods run as: `default` when the document names none.
	ServiceAccountName string `json:"serviceAccountName"`
}

// LabelSelector is a Kubernetes label selector as written. An empty selector (no labels, no expressions) selects
// everything in its scope; the typed reader does not turn that into "nothing".
type LabelSelector struct {
	MatchLabels      map[string]string     `json:"matchLabels,omitempty"`
	MatchExpressions []SelectorRequirement `json:"matchExpressions,omitempty"`
}

// SelectorRequirement is one `matchExpressions` entry. Operator is In, NotIn, Exists, or DoesNotExist.
type SelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// IsEmpty says that the selector has no label and no expression.
func (s LabelSelector) IsEmpty() bool { return len(s.MatchLabels) == 0 && len(s.MatchExpressions) == 0 }

// Matches evaluates the selector against a label set.
func (s LabelSelector) Matches(labels map[string]string) bool {
	for k, v := range s.MatchLabels {
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}

	for _, req := range s.MatchExpressions {
		if !req.matches(labels) {
			return false
		}
	}

	return true
}

// matches evaluates one expression. An operator that is not known matches nothing (the reader refuses it first).
func (r SelectorRequirement) matches(labels map[string]string) bool {
	got, has := labels[r.Key]

	switch r.Operator {
	case "In":
		return has && slices.Contains(r.Values, got)
	case "NotIn":
		return !has || !slices.Contains(r.Values, got)
	case "Exists":
		return has
	case "DoesNotExist":
		return !has
	default:
		return false
	}
}

// PortRef is an int-or-string port: a number or a named port.
type PortRef struct {
	Name   string `json:"name,omitempty"`
	Number int    `json:"number,omitempty"`
}

// IsNamed says that the reference is a name.
func (p PortRef) IsNamed() bool { return p.Name != "" }

// Service holds the facts of a Service.
type Service struct {
	// Selector is the `spec.selector` map. A Service with no selector has no endpoints of its own, so it selects
	// nothing and has no selection fact.
	Selector map[string]string `json:"selector,omitempty"`
	Type     string            `json:"type,omitempty"`
	Ports    []ServicePort     `json:"ports,omitempty"`
}

// ServicePort is one Service port.
type ServicePort struct {
	Name       string  `json:"name,omitempty"`
	Protocol   string  `json:"protocol"`
	TargetPort PortRef `json:"targetPort"`
	Port       int     `json:"port"`
}

// NetworkPolicy keeps the full `networking.k8s.io/v1` spec for the evaluators.
type NetworkPolicy struct {
	// PodSelector is the selector of the pods the policy applies to. An empty selector selects every pod of the
	// namespace.
	PodSelector LabelSelector `json:"podSelector"`
	// PolicyTypes are the types as written; nil when the document leaves them out (the default then depends on the
	// rules, see EffectiveTypes).
	PolicyTypes []string            `json:"policyTypes,omitempty"`
	Ingress     []NetworkPolicyRule `json:"ingress,omitempty"`
	Egress      []NetworkPolicyRule `json:"egress,omitempty"`
	// Unsupported lists constructs the reader could not interpret (an unknown selector operator, a malformed rule).
	// Results for the selected workloads are unknown, never "no restriction".
	Unsupported []string `json:"unsupported,omitempty"`
}

// EffectiveTypes gives the policy types: the written ones, else Ingress, plus Egress when the policy has egress
// rules (Kubernetes defaults).
func (p *NetworkPolicy) EffectiveTypes() []string {
	if len(p.PolicyTypes) > 0 {
		return p.PolicyTypes
	}

	types := []string{"Ingress"}
	if len(p.Egress) > 0 {
		types = append(types, "Egress")
	}

	return types
}

// NetworkPolicyRule is one ingress or egress rule. An empty Peers list matches every peer and an empty Ports list
// matches every port (Kubernetes semantics): the lists are kept as written, not interpreted.
type NetworkPolicyRule struct {
	// Peers are the `from` (ingress) or `to` (egress) entries.
	Peers []NetworkPolicyPeer `json:"peers,omitempty"`
	Ports []NetworkPolicyPort `json:"ports,omitempty"`
}

// NetworkPolicyPeer is one peer: pod selector, namespace selector, or IP block. The selectors are pointers because
// "absent" and "empty selector" differ: an absent namespace selector means the policy namespace, an empty one
// means every namespace.
type NetworkPolicyPeer struct {
	PodSelector       *LabelSelector `json:"podSelector,omitempty"`
	NamespaceSelector *LabelSelector `json:"namespaceSelector,omitempty"`
	IPBlock           *IPBlock       `json:"ipBlock,omitempty"`
}

// IPBlock is an `ipBlock` peer.
type IPBlock struct {
	CIDR   string   `json:"cidr"`
	Except []string `json:"except,omitempty"`
}

// NetworkPolicyPort is one rule port.
type NetworkPolicyPort struct {
	// Port is nil when the rule names a protocol only.
	Port *PortRef `json:"port,omitempty"`
	// EndPort is the end of a port range, nil when there is none.
	EndPort *int `json:"endPort,omitempty"`
	// Protocol is TCP when the document leaves it out.
	Protocol string `json:"protocol"`
}

// Route is an Ingress or a Gateway API route (HTTPRoute, GRPCRoute, TCPRoute, TLSRoute).
type Route struct {
	Hosts []string `json:"hosts,omitempty"`
	// ParentRefs are the Gateways a Gateway API route attaches to.
	ParentRefs []ParentRef `json:"parentRefs,omitempty"`
	// Backends are the Services the route sends traffic to.
	Backends []Backend `json:"backends,omitempty"`
}

// ParentRef is a Gateway API parent reference.
type ParentRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Backend is a Service reference of a route.
type Backend struct {
	// Service is the Service name; the namespace is the one of the route unless Namespace is set.
	Service   string  `json:"service"`
	Namespace string  `json:"namespace,omitempty"`
	Port      PortRef `json:"port"`
}

// Gateway is a Gateway API Gateway.
type Gateway struct {
	ClassName string            `json:"className,omitempty"`
	Listeners []GatewayListener `json:"listeners,omitempty"`
}

// GatewayListener is one Gateway listener.
type GatewayListener struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Hostname string `json:"hostname,omitempty"`
	Port     int    `json:"port"`
}

// ServiceAccount holds the facts of a ServiceAccount.
type ServiceAccount struct {
	// AutomountServiceAccountToken is nil when the document leaves it out.
	AutomountServiceAccountToken *bool    `json:"automountServiceAccountToken,omitempty"`
	ImagePullSecrets             []string `json:"imagePullSecrets,omitempty"`
}

// Role is a Role or a ClusterRole.
type Role struct {
	// AggregationRule is nil when the role has none. The rules of the roles it selects are added by the evaluator.
	AggregationRule *AggregationRule `json:"aggregationRule,omitempty"`
	Rules           []PolicyRule     `json:"rules,omitempty"`
}

// PolicyRule is one RBAC rule.
type PolicyRule struct {
	APIGroups       []string `json:"apiGroups,omitempty"`
	Resources       []string `json:"resources,omitempty"`
	ResourceNames   []string `json:"resourceNames,omitempty"`
	Verbs           []string `json:"verbs,omitempty"`
	NonResourceURLs []string `json:"nonResourceURLs,omitempty"` //nolint:tagliatelle // the Kubernetes field name.
}

// AggregationRule is the `aggregationRule` of a ClusterRole.
type AggregationRule struct {
	ClusterRoleSelectors []LabelSelector `json:"clusterRoleSelectors,omitempty"`
}

// Binding is a RoleBinding or a ClusterRoleBinding.
type Binding struct {
	RoleRef  RoleRef   `json:"roleRef"`
	Subjects []Subject `json:"subjects,omitempty"`
}

// RoleRef is the role a binding grants.
type RoleRef struct {
	APIGroup string `json:"apiGroup,omitempty"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

// Subject is one binding subject: ServiceAccount, User, or Group.
type Subject struct {
	Kind      string `json:"kind"`
	APIGroup  string `json:"apiGroup,omitempty"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// Selection kinds.
const (
	SelectionService       = "service-selector"
	SelectionNetworkPolicy = "network-policy-pod-selector"
)

// Selection is a selection fact: a Service selector or a NetworkPolicy pod selector resolved against the rendered
// workloads of its namespace. It is never an isolation claim: whether a policy isolates anything is the business
// of the evaluator, which reads the policy documents.
type Selection struct {
	// Selector is the Service or NetworkPolicy.
	Selector ResourceID `json:"selector"`
	// Kind is `service-selector` or `network-policy-pod-selector`.
	Kind string `json:"kind"`
	// Targets are the selected workloads, sorted. Meaningful only when Unsupported is false.
	Targets []ResourceID `json:"targets,omitempty"`
	// Empty is true for an empty NetworkPolicy pod selector, which selects every pod of the namespace.
	Empty bool `json:"empty,omitempty"`
	// Unsupported is true when the selector uses syntax the contract does not support: the selection is a coverage
	// gap and not an empty result.
	Unsupported bool `json:"unsupported,omitempty"`
}

// Result is the rendered evidence of one source of one environment.
type Result struct {
	Environment string `json:"environment"`
	Cluster     string `json:"cluster,omitempty"`
	// Tool is `helm`, `kustomize`, or `plain`.
	Tool string `json:"tool"`
	// Path is the declared source path.
	Path string `json:"path"`
	// Level is the evidence level: always `rendered-or-evaluated`.
	Level string `json:"level"`
	// Renderer is nil for plain manifests.
	Renderer    *Identity `json:"renderer,omitempty"`
	Confinement string    `json:"confinement"`
	// InputDigest is the digest of the declared inputs that were read and the source settings.
	InputDigest string `json:"inputDigest,omitempty"`
	// OutputDigest is the digest of the redacted canonical resource set. Empty when the coverage is failed.
	OutputDigest string             `json:"outputDigest,omitempty"`
	Coverage     assurance.Coverage `json:"coverage"`
	Gaps         []assurance.Gap    `json:"gaps,omitempty"`

	// Resources, Occurrences, and Selections are sorted. They are empty when the coverage is failed: a partial set
	// is never used as complete evidence.
	Resources   []Resource            `json:"resources,omitempty"`
	Occurrences []ContainerOccurrence `json:"occurrences,omitempty"`
	Selections  []Selection           `json:"selections,omitempty"`
	// Redactions record each value that was removed; they never hold the value.
	Redactions  []redact.Record   `json:"redactions,omitempty"`
	Diagnostics []diag.Diagnostic `json:"diagnostics,omitempty"`
}

// ResourceCount is the number of rendered resources.
func (r *Result) ResourceCount() int { return len(r.Resources) }

// Resource gives the resource with the identity, or nil.
func (r *Result) Resource(id *ResourceID) *Resource {
	for i := range r.Resources {
		if r.Resources[i].ID == *id {
			return &r.Resources[i]
		}
	}

	return nil
}

// OccurrencesOf gives the container occurrences of one workload.
func (r *Result) OccurrencesOf(workload *ResourceID) []ContainerOccurrence {
	var out []ContainerOccurrence

	for i := range r.Occurrences {
		if r.Occurrences[i].Workload == *workload {
			out = append(out, r.Occurrences[i])
		}
	}

	return out
}
