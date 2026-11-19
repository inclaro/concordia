package binding

import "testing"

func TestTierOrderKeepsTheAsBuiltKindsAndAppendsTheDeployedKinds(t *testing.T) {
	t.Parallel()

	order := []int{
		tierSourceRoot, tierBuildTarget, tierPackage, tierQualifiedName, tierOtelService,
		tierImageRepo, tierComposeService, tierRoute, tierInfraResource, tierWorkload, tierImageDigest,
	}

	for i := 1; i < len(order); i++ {
		if order[i-1] >= order[i] {
			t.Fatalf("tier %d is not above tier %d", order[i], order[i-1])
		}
	}

	if tierSourceRoot != 1 {
		t.Errorf("tierSourceRoot = %d", tierSourceRoot)
	}
}
