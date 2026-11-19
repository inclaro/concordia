package evaluate_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
)

func TestLabelSelectorMatches(t *testing.T) {
	t.Parallel()

	labels := map[string]string{"app": "api", "tier": "back"}
	expr := func(key, op string, values ...string) evaluate.LabelSelector {
		return evaluate.LabelSelector{MatchExpressions: []evaluate.Requirement{{Key: key, Operator: op, Values: values}}}
	}

	tests := []struct {
		name    string
		sel     evaluate.LabelSelector
		want    bool
		wantErr bool
	}{
		{"empty selector matches everything", evaluate.LabelSelector{}, true, false},
		{"matchLabels subset", evaluate.LabelSelector{MatchLabels: map[string]string{"app": "api"}}, true, false},
		{"matchLabels wrong value", evaluate.LabelSelector{MatchLabels: map[string]string{"app": "web"}}, false, false},
		{"matchLabels missing key", evaluate.LabelSelector{MatchLabels: map[string]string{"zone": "a"}}, false, false},
		{"In", expr("app", evaluate.OpIn, "api", "web"), true, false},
		{"In missing key", expr("zone", evaluate.OpIn, "a"), false, false},
		{"NotIn", expr("app", evaluate.OpNotIn, "web"), true, false},
		{"NotIn missing key matches", expr("zone", evaluate.OpNotIn, "a"), true, false},
		{"Exists", expr("tier", evaluate.OpExists), true, false},
		{"DoesNotExist", expr("tier", evaluate.OpDoesNotExist), false, false},
		{"labels and expressions both needed", evaluate.LabelSelector{
			MatchLabels:      map[string]string{"app": "api"},
			MatchExpressions: []evaluate.Requirement{{Key: "tier", Operator: evaluate.OpIn, Values: []string{"front"}}},
		}, false, false},
		{"In without values is invalid", expr("app", evaluate.OpIn), false, true},
		{"Exists with values is invalid", expr("app", evaluate.OpExists, "x"), false, true},
		{"unknown operator is invalid", expr("app", "Bogus"), false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.sel.Matches(labels)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("Matches = %v, %v; want %v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestDigestOfIsStable(t *testing.T) {
	t.Parallel()

	a := evaluate.DigestOf(map[string]int{"b": 1, "a": 2})
	b := evaluate.DigestOf(map[string]int{"a": 2, "b": 1})

	if a != b || !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Fatalf("digests = %q, %q", a, b)
	}
}
