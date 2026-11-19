package evaluate

import (
	"fmt"
	"slices"
)

// ObjectMeta is the part of Kubernetes object metadata that the evaluators read.
type ObjectMeta struct {
	Labels    map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Name      string            `json:"name" yaml:"name"`
	Namespace string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

// Selector expression operators (`matchExpressions[].operator`).
const (
	OpIn           = "In"
	OpNotIn        = "NotIn"
	OpExists       = "Exists"
	OpDoesNotExist = "DoesNotExist"
)

// Requirement is one `matchExpressions` entry.
type Requirement struct {
	Key      string   `json:"key" yaml:"key"`
	Operator string   `json:"operator" yaml:"operator"`
	Values   []string `json:"values,omitempty" yaml:"values,omitempty"`
}

// LabelSelector mirrors the Kubernetes `metav1.LabelSelector`. The empty selector matches every label set.
type LabelSelector struct {
	MatchLabels      map[string]string `json:"matchLabels,omitempty" yaml:"matchLabels,omitempty"`
	MatchExpressions []Requirement     `json:"matchExpressions,omitempty" yaml:"matchExpressions,omitempty"`
}

// Matches reports whether the label set satisfies the selector: every `matchLabels` pair and every expression. A
// selector that Kubernetes would reject (an unknown operator, `In` without values, `Exists` with values) gives an
// error, and the caller must not read it as match or as no match.
func (s LabelSelector) Matches(labels map[string]string) (bool, error) {
	matched := true

	for key, want := range s.MatchLabels {
		if got, ok := labels[key]; !ok || got != want {
			matched = false
		}
	}

	for _, req := range s.MatchExpressions {
		ok, err := req.matches(labels)
		if err != nil {
			return false, err
		}

		if !ok {
			matched = false
		}
	}

	return matched, nil
}

func (r Requirement) matches(labels map[string]string) (bool, error) {
	got, present := labels[r.Key]

	switch r.Operator {
	case OpIn, OpNotIn:
		if len(r.Values) == 0 {
			return false, fmt.Errorf("selector expression %q: operator %s needs values", r.Key, r.Operator)
		}

		in := present && slices.Contains(r.Values, got)
		if r.Operator == OpIn {
			return in, nil
		}

		return !in, nil
	case OpExists, OpDoesNotExist:
		if len(r.Values) != 0 {
			return false, fmt.Errorf("selector expression %q: operator %s takes no values", r.Key, r.Operator)
		}

		if r.Operator == OpExists {
			return present, nil
		}

		return !present, nil
	default:
		return false, fmt.Errorf("selector expression %q: unknown operator %q", r.Key, r.Operator)
	}
}
