package policy_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

func placementRef(id, rule string, typ policy.ObligationType, mode policy.TargetMode, target ...string) policy.ObligationRef {
	return policy.ObligationRef{
		ObligationID: id, RuleID: rule, Subject: "svc-s", Environment: "production", Type: typ,
		Relation: policy.RelationSpec{Kind: observation.KindPlacedIn, Direction: policy.DirectionFromWorkload, Target: append([]string{}, target...), TargetMode: mode},
	}
}

func TestDeployedInTargetsConflictOnlyWhenTheyCanRefToOneTarget(t *testing.T) {
	t.Parallel()

	const (
		declared = policy.TargetDeclaredContainers
		other    = policy.TargetOtherHostingTargets
	)

	for name, tc := range map[string]struct {
		first, second policy.ObligationRef
		want          bool
	}{
		"required in the containers and forbidden in the other targets": {
			placementRef("a", "A", policy.ObligationRequired, declared, "prod"), placementRef("b", "B", policy.ObligationForbidden, other, "prod"), false,
		},
		"required in the containers and forbidden in the containers": {
			placementRef("a", "A", policy.ObligationRequired, declared, "prod"), placementRef("b", "B", policy.ObligationForbidden, declared, "prod"), true,
		},
		"allowed in the other targets and forbidden in the other targets": {
			placementRef("a", "A", policy.ObligationAllowed, other, "prod"), placementRef("b", "B", policy.ObligationForbidden, other, "prod"), true,
		},
		"a named target outside the containers and the other targets": {
			placementRef("a", "A", policy.ObligationRequired, policy.TargetExplicit, "dev"), placementRef("b", "B", policy.ObligationForbidden, other, "prod"), true,
		},
		"a named target that is a container and the other targets": {
			placementRef("a", "A", policy.ObligationRequired, policy.TargetExplicit, "prod"), placementRef("b", "B", policy.ObligationForbidden, other, "prod"), false,
		},
		"required with no target and forbidden in the other targets": {
			placementRef("a", "A", policy.ObligationRequired, policy.TargetExplicit), placementRef("b", "B", policy.ObligationForbidden, other, "prod"), false,
		},
		"required with no target and forbidden with no target": {
			placementRef("a", "A", policy.ObligationRequired, policy.TargetExplicit), placementRef("b", "B", policy.ObligationForbidden, policy.TargetExplicit), true,
		},
		"a subject with no declared placement": {
			placementRef("a", "A", policy.ObligationRequired, declared), placementRef("b", "B", policy.ObligationForbidden, other), false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			report := policy.DetectConflicts([]policy.ObligationRef{tc.first, tc.second}, nil)
			if got := len(report.Conflicts) == 1; got != tc.want {
				t.Fatalf("conflict = %v, want %v (%+v)", got, tc.want, report)
			}
		})
	}
}
