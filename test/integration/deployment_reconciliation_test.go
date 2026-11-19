package integration_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
)

// Deployment facts stay out of the pair scan (INFRA-PLC rules; specs/deployment-placement, "Placement is independent of
// pair reconciliation"). The pair `api-service` and `prod-cluster` has a `placed-in` fact and a `deployed-in`
// relationship in the model: a pair scan over placement would give it an entry.

func reconciliationOf(run *conformance.Run) []conformance.ReconciliationEntry {
	return run.Reconciliation
}

func TestDeploymentFactsCreateNoReconciliationPair(t *testing.T) {
	t.Parallel()

	with := deploymentRun(t, &deploymentSpec{})
	without := deploymentRun(t, &deploymentSpec{withoutPack: true})

	if len(placementFacts(with.run)) == 0 {
		t.Fatal("the run has no placed-in fact, so the test proves nothing")
	}

	if len(placementFacts(without.run)) == 0 {
		t.Fatal("the run without the pack has no placed-in fact: the facts must not depend on the rules")
	}

	if got, want := reconciliationOf(with.run), reconciliationOf(without.run); !reflect.DeepEqual(got, want) {
		t.Fatalf("reconciliation with the pack = %+v, want the entries of the run without it: %+v", got, want)
	}

	for i := range with.run.Reconciliation {
		entry := &with.run.Reconciliation[i]

		if placementKinds(entry) {
			t.Errorf("entry %+v is derived from a deployment fact", entry)
		}

		if entry.From == "prod-cluster" || entry.To == "prod-cluster" {
			t.Errorf("entry %+v pairs the container, which only deployment facts reach", entry)
		}
	}
}

// placementKinds says whether an entry is derived from a workload, image, Dockerfile, or placement fact.
func placementKinds(entry *conformance.ReconciliationEntry) bool {
	for _, kind := range entry.ObservedKinds {
		if observation.IsInfrastructureKind(kind) || strings.Contains(string(kind), "placed") {
			return true
		}
	}

	return false
}
