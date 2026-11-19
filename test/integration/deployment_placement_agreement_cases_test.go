package integration_test

import "testing"

// plcExpectation names the pairs that the rules must show for one node in one environment.
type plcExpectation struct {
	node, env string
	pairs     []plcPair
}

// plcAgreementCase is one scenario of TestPlacementAgreement: the workspace to run and the pairs it must show.
type plcAgreementCase struct {
	name   string
	spec   func(t *testing.T) *deploymentSpec
	expect []plcExpectation
}

func plcAgreementCases() []plcAgreementCase {
	return []plcAgreementCase{
		plcDefaultFixtureCase(),
		plcSynthesizedNamespacesCase(),
		plcWrongHostingTargetCase(),
		plcWrongDirectionCase(),
		plcAmbiguousWorkloadCase(),
		plcUnboundWorkloadCase(),
		plcSplitContainerCase(),
	}
}

// Production api is placed in the declared container; staging has no binding for its namespaces; the production
// worker has no workload at all (complete coverage).
func plcDefaultFixtureCase() plcAgreementCase {
	return plcAgreementCase{
		name: "default_fixture",
		spec: func(*testing.T) *deploymentSpec { return &deploymentSpec{} },
		expect: []plcExpectation{
			{"api-service", "production", []plcPair{pairPass}},
			{"api-service", "staging", []plcPair{pairBindingGap}},
			{"worker-service", "production", []plcPair{pairAbsent}},
			{"worker-service", "staging", []plcPair{pairBindingGap}},
		},
	}
}

// The synthesized namespaces of staging are bound to the declared container.
func plcSynthesizedNamespacesCase() plcAgreementCase {
	return plcAgreementCase{
		name: "synthesized_namespaces_bound",
		spec: func(*testing.T) *deploymentSpec {
			return &deploymentSpec{extraBindings: plcNamespace("bind-ns-synthesized", "prod-cluster", "staging", "payments") +
				plcNamespace("bind-ns-staging", "prod-cluster", "staging", "payments-staging")}
		},
		expect: []plcExpectation{
			{"api-service", "staging", []plcPair{pairPass}},
			{"worker-service", "staging", []plcPair{pairPass}},
		},
	}
}

// Namespace payments of production is bound to dev-cluster, which is not the declared container.
func plcWrongHostingTargetCase() plcAgreementCase {
	return plcAgreementCase{
		name: "wrong_hosting_target",
		spec: func(t *testing.T) *deploymentSpec {
			t.Helper()

			return &deploymentSpec{architecture: plcArchitecture(t, "dev-cluster"), bindings: plcAPIBindings(plcNamespace("bind-ns-prod", "dev-cluster", "production", "payments"))}
		},
		expect: []plcExpectation{{"api-service", "production", []plcPair{pairWrongTarget}}},
	}
}

// The workload payments/reports is bound to prod-cluster and its namespace to api-service.
func plcWrongDirectionCase() plcAgreementCase {
	return plcAgreementCase{
		name: "wrong_direction",
		spec: func(*testing.T) *deploymentSpec {
			return &deploymentSpec{bindings: plcBindings(
				workloadBinding("bind-reports-workload", "prod-cluster", "production", "reports", "payments"),
				plcNamespace("bind-ns-prod", "api-service", "production", "payments"),
			)}
		},
		expect: []plcExpectation{{"api-service", "production", []plcPair{pairWrongDirection}}},
	}
}

// The production api workload matches non-exclusive bindings of two nodes at the same tier.
func plcAmbiguousWorkloadCase() plcAgreementCase {
	return plcAgreementCase{
		name: "ambiguous_workload",
		spec: func(*testing.T) *deploymentSpec {
			return &deploymentSpec{bindings: plcBindings(
				plcNonExclusive(workloadBinding("bind-api-workload", "api-service", "production", "api", "payments")),
				plcNonExclusive(workloadBinding("bind-api-workload-twin", "worker-service", "production", "api", "payments")),
				plcNamespace("bind-ns-prod", "prod-cluster", "production", "payments"),
			)}
		},
		expect: []plcExpectation{
			{"api-service", "production", []plcPair{pairAmbiguous}},
			{"worker-service", "production", []plcPair{pairAmbiguous}},
		},
	}
}

// The production api workload has no binding: it is a gap with no node and the node shows an absence.
func plcUnboundWorkloadCase() plcAgreementCase {
	return plcAgreementCase{
		name: "unbound_workload",
		spec: func(*testing.T) *deploymentSpec {
			return &deploymentSpec{bindings: plcBindings(
				workloadBinding("bind-api-staging", "api-service", "staging", "api", "payments-staging"),
				plcNamespace("bind-ns-prod", "prod-cluster", "production", "payments"),
			)}
		},
		expect: []plcExpectation{{"api-service", "production", []plcPair{pairAbsent}}},
	}
}

// Both workloads of staging are bound to api-service: api is in the container, worker is in dev-cluster.
func plcSplitContainerCase() plcAgreementCase {
	return plcAgreementCase{
		name: "one_workload_in_the_container_one_in_another",
		spec: func(t *testing.T) *deploymentSpec {
			t.Helper()

			return &deploymentSpec{architecture: plcArchitecture(t, "dev-cluster"), bindings: plcBindings(
				workloadBinding("bind-api-staging", "api-service", "staging", "api", "payments-staging"),
				workloadBinding("bind-worker-staging", "api-service", "staging", "worker", "payments"),
				plcNamespace("bind-ns-api", "prod-cluster", "staging", "payments-staging"),
				plcNamespace("bind-ns-worker", "dev-cluster", "staging", "payments"),
			)}
		},
		expect: []plcExpectation{{"api-service", "staging", []plcPair{pairPass, pairWrongTarget}}},
	}
}
