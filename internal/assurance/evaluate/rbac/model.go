// Package rbac evaluates Kubernetes RBAC over rendered Role, ClusterRole, RoleBinding, and ClusterRoleBinding
// documents (design D8): which permissions a subject is granted, and which subjects are granted a permission. It
// claims RBAC grants only, never effective authorization (no cloud IAM, admission control, or webhook authorizers).
// The input model is plain Go structs that mirror the Kubernetes field names.
package rbac

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/canonjson"
)

// Subject kinds of a binding.
const (
	KindServiceAccount = "ServiceAccount"
	KindUser           = "User"
	KindGroup          = "Group"
)

// Kinds of a role reference.
const (
	KindRole        = "Role"
	KindClusterRole = "ClusterRole"
)

// Input is the rendered evidence of one environment that the evaluator reads.
type Input struct {
	// Evidence is the evidence level (`evaluate.EvidenceRendered`); declared-level input is never evaluated.
	Evidence string `json:"evidence" yaml:"evidence"`
	// ClusterCoverage is the render coverage of the cluster-scoped resources (ClusterRole, ClusterRoleBinding). Every
	// result depends on them. Only `complete` allows results; the empty value counts as incomplete.
	ClusterCoverage assurance.Coverage `json:"clusterCoverage" yaml:"clusterCoverage"`
	// Namespaces lists every namespace of the render with its coverage. A namespaced result depends on its namespace.
	Namespaces          []Namespace          `json:"namespaces" yaml:"namespaces"`
	Roles               []Role               `json:"roles,omitempty" yaml:"roles,omitempty"`
	ClusterRoles        []ClusterRole        `json:"clusterRoles,omitempty" yaml:"clusterRoles,omitempty"`
	RoleBindings        []RoleBinding        `json:"roleBindings,omitempty" yaml:"roleBindings,omitempty"`
	ClusterRoleBindings []ClusterRoleBinding `json:"clusterRoleBindings,omitempty" yaml:"clusterRoleBindings,omitempty"`
}

// Namespace is one namespace of the render.
type Namespace struct {
	Name     string             `json:"name" yaml:"name"`
	Coverage assurance.Coverage `json:"coverage" yaml:"coverage"`
}

// PolicyRule is one `rules[]` entry of a Role or ClusterRole.
type PolicyRule struct {
	Verbs           []string `json:"verbs" yaml:"verbs"`
	APIGroups       []string `json:"apiGroups,omitempty" yaml:"apiGroups,omitempty"`
	Resources       []string `json:"resources,omitempty" yaml:"resources,omitempty"`
	ResourceNames   []string `json:"resourceNames,omitempty" yaml:"resourceNames,omitempty"`
	NonResourceURLs []string `json:"nonResourceURLs,omitempty" yaml:"nonResourceURLs,omitempty"` //nolint:tagliatelle // Kubernetes field name
}

// Role is a namespaced rbac.authorization.k8s.io/v1 Role.
type Role struct {
	Metadata evaluate.ObjectMeta `json:"metadata" yaml:"metadata"`
	Rules    []PolicyRule        `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// ClusterRole is a ClusterRole. With an AggregationRule its rules are those of the matching ClusterRoles.
type ClusterRole struct {
	Metadata        evaluate.ObjectMeta `json:"metadata" yaml:"metadata"`
	AggregationRule *AggregationRule    `json:"aggregationRule,omitempty" yaml:"aggregationRule,omitempty"`
	Rules           []PolicyRule        `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// AggregationRule selects other ClusterRoles by label.
type AggregationRule struct {
	ClusterRoleSelectors []evaluate.LabelSelector `json:"clusterRoleSelectors,omitempty" yaml:"clusterRoleSelectors,omitempty"`
}

// Subject is a binding subject.
type Subject struct {
	// Kind is ServiceAccount, User, or Group.
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
	// Namespace is the namespace of a ServiceAccount.
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

// RoleRef is the role that a binding grants.
type RoleRef struct {
	// Kind is Role (RoleBinding only) or ClusterRole.
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
}

// RoleBinding grants the rules of a Role or ClusterRole inside its own namespace.
type RoleBinding struct {
	Metadata evaluate.ObjectMeta `json:"metadata" yaml:"metadata"`
	RoleRef  RoleRef             `json:"roleRef" yaml:"roleRef"`
	Subjects []Subject           `json:"subjects,omitempty" yaml:"subjects,omitempty"`
}

// ClusterRoleBinding grants the rules of a ClusterRole in every namespace and on cluster-scoped resources.
type ClusterRoleBinding struct {
	Metadata evaluate.ObjectMeta `json:"metadata" yaml:"metadata"`
	RoleRef  RoleRef             `json:"roleRef" yaml:"roleRef"`
	Subjects []Subject           `json:"subjects,omitempty" yaml:"subjects,omitempty"`
}

// Digest is the digest of the input ("sha256:<hex>" of the canonical JSON of the input in a fixed order): the same
// rendered input gives the same digest.
func (in *Input) Digest() string {
	norm := *in
	norm.Namespaces = sortedBy(in.Namespaces, func(n Namespace) string { return n.Name })
	norm.Roles = sortedBy(in.Roles, func(r Role) string { return r.Metadata.Namespace + "/" + r.Metadata.Name })
	norm.ClusterRoles = sortedBy(in.ClusterRoles, func(r ClusterRole) string { return r.Metadata.Name })
	norm.RoleBindings = sortedBy(in.RoleBindings, func(b RoleBinding) string { return b.Metadata.Namespace + "/" + b.Metadata.Name })
	norm.ClusterRoleBindings = sortedBy(in.ClusterRoleBindings, func(b ClusterRoleBinding) string { return b.Metadata.Name })

	return evaluate.DigestOf(norm)
}

// sortedBy returns a copy of items ordered by key with the UTF-16 comparator that all canonical output uses.
func sortedBy[T any](items []T, key func(T) string) []T {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b T) int { return canonjson.CompareUTF16(key(a), key(b)) })

	return out
}
