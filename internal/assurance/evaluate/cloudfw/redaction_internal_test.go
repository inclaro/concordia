package cloudfw

import (
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/redact"
)

// TestRedactedRuleFieldsAreNotRead: the plan reader writes a marker in place of a sensitive value. A rule whose
// ports or whose whole rule list the plan redacts was not read, so it may allow the traffic.
func TestRedactedRuleFieldsAreNotRead(t *testing.T) {
	t.Parallel()

	marker := redact.Marker(redact.ClassSecretData)
	world := func(ingress any) []plan.Resource {
		db := resourceOf(awsDBGroup, typeSecurityGroup, map[string]any{"id": idDB, "ingress": ingress, "egress": []any{}})

		return []plan.Resource{group(awsAppGroup, idApp, []any{}, []any{allTraffic()}), db}
	}

	runQueryCases(t, []queryCase{
		{
			name: "redacted ports of an allow rule", from: awsAppGroup, to: awsDBGroup, want: VerdictUnknown,
			resources: world([]any{map[string]any{
				"protocol": "tcp", "from_port": marker, "to_port": marker, "cidr_blocks": []any{"0.0.0.0/0"},
			}}),
			gaps: []string{evaluate.GapUnknownAfterApply + ": " + awsDBGroup + ".ingress[0].from_port"},
		},
		{
			name: "redacted rule list", from: awsAppGroup, to: awsDBGroup, want: VerdictUnknown, resources: world(marker),
		},
		{
			name: "redacted element of the rule list", from: awsAppGroup, to: awsDBGroup, want: VerdictUnknown,
			resources: world([]any{marker}),
		},
		{
			name: "redacted description of a rule that does not allow", from: awsAppGroup, to: awsDBGroup,
			want: VerdictDenied, resources: world([]any{map[string]any{
				"protocol": "tcp", "from_port": "22", "to_port": "22", "cidr_blocks": []any{"0.0.0.0/0"},
				"description": marker,
			}}),
		},
	})
}

// TestRedactedTextIsNotKnown: a redacted text is neither a known value nor absent.
func TestRedactedTextIsNotKnown(t *testing.T) {
	t.Parallel()

	marker := redact.Marker(redact.ClassSecretData)
	res := resourceOf("aws_security_group.db", typeSecurityGroup, map[string]any{
		"name": marker, "ids": []any{"sg-1", marker}, "block": marker,
	})

	if _, ok := attrString(&res, "name"); ok {
		t.Error("attrString(name) is known, want not known")
	}

	for path, want := range map[string]bool{"name": true, "ids": true, "block.0.port": true, "other": false} {
		if got := unknownAt(&res, path); got != want {
			t.Errorf("unknownAt(%s) = %v, want %v", path, got, want)
		}
	}

	if !listHasUnknownElements(&res, "ids") || !listHasUnknownElements(&res, "block") {
		t.Error("listHasUnknownElements = false for a redacted list or element, want true")
	}
}
