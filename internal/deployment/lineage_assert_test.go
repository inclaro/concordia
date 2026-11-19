package deployment_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
)

// lineageWant reports a value of a trace that is not the expected one.
func lineageWant[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// lineageWantList reports a list of a trace that is not the expected one.
func lineageWantList[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// lineageRequireCount stops a test whose list has not the expected length.
func lineageRequireCount[T any](t *testing.T, what string, list []T, want int) {
	t.Helper()

	if len(list) != want {
		t.Fatalf("%s = %+v, want %d items", what, list, want)
	}
}

// lineageHasLimit says whether the limits have the limit.
func lineageHasLimit(limits []deployment.Limit, id string) bool {
	return slices.ContainsFunc(limits, func(l deployment.Limit) bool { return l.ID == id })
}

// lineageDropUsesImage removes the `uses-image` facts of the world: the test adds its own.
func lineageDropUsesImage(w *world) {
	w.edges = slices.DeleteFunc(w.edges, func(e *observation.EvidenceEdge) bool { return e.Kind == observation.KindUsesImage })
}

// lineageTraceStep builds the trace of the input and gives a step of orders-service in production.
func lineageTraceStep(t *testing.T, in *deployment.Input, step deployment.Step) *deployment.StepTrace {
	t.Helper()

	return stepOf(t, deployment.BuildTrace(in).Subject("orders-service", "production"), step)
}

// lineageSharedImageInput is the input of the infra recording with both environments complete.
func lineageSharedImageInput(t *testing.T) *deployment.Input {
	t.Helper()

	in := infraInput(t)
	in.Coverage = slices.Concat(complete("production"), complete("staging"))

	return in
}

// lineageOrdersWorld is a production workload of orders-service that is bound and has complete coverage.
func lineageOrdersWorld() (w *world, workload string) {
	w = newWorld()
	workload = w.workload("w", "production", "k8s/prod/orders.yaml", "apps/Deployment/shop/orders")
	w.bind(workload, "orders-service", "b")

	return w, workload
}

// lineageOrdersInput is the input of the orders world with complete coverage of production.
func lineageOrdersInput(w *world) *deployment.Input {
	in := w.input(envs("production"), "orders-service")
	in.Coverage = complete("production")

	return in
}
