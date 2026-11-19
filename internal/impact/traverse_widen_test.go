package impact_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/impact"
)

// scopeOf gives a declared scope that is already known.
func scopeOf(nodes []impact.NodeID, ok bool) func() ([]impact.NodeID, bool) {
	return func() ([]impact.NodeID, bool) { return nodes, ok }
}

// scopeNeverRead gives a declared scope that fails the test when it is read.
func scopeNeverRead(t *testing.T) func() ([]impact.NodeID, bool) {
	t.Helper()

	return func() ([]impact.NodeID, bool) {
		t.Fatal("declared scope read")

		return nil, false
	}
}

// Scenario: SEC-07 selector not resolvable.
func TestSec07UnresolvedSelectorWidensToDeclaredScope(t *testing.T) {
	a := asmt("ASM-EDGE")
	resolved := []impact.NodeID{impact.ItemNodeID(impact.KindPublicEntryPoint, "r1")}
	scope := []impact.NodeID{impact.ItemNodeID(impact.KindPublicEntryPoint, "r1"), impact.ItemNodeID(impact.KindPublicEntryPoint, "r2"), impact.ItemNodeID(impact.KindRuntimeIdentity, "svc")}
	unresolved := []impact.Unresolved{{Selector: "workloads of api", Reason: "binding-gap"}}

	// Everything resolved: no widening, the declared scope is not asked.
	closure := impact.WidenClosure(resolved, nil, scopeNeverRead(t))
	checkTrue(t, !closure.Widened, "a resolved closure is widened")
	checkTrue(t, !closure.Unknown, "a resolved closure is unknown")
	checkEq(t, "resolved closure size", len(closure.Nodes), 1)

	// An unresolved selector widens to the declared scope.
	closure = impact.WidenClosure(resolved, unresolved, scopeOf(scope, true))
	checkTrue(t, closure.Widened, "an unresolved selector did not widen the closure")
	checkTrue(t, !closure.Unknown, "a widened closure is unknown")
	checkEq(t, "widened closure size", len(closure.Nodes), 3)
	checkEq(t, "unresolved selectors", len(closure.Unresolved), 1)

	// The widened closure reaches the assessment in a traversal.
	g := impact.NewGraph()
	g.AddNode(a, impact.NodeAssessment)
	g.AddDependency(a, resolved[0], impact.EdgeDependsOn)
	g.Widen(a, closure)

	res := g.Traverse(seedOf(impact.ItemNodeID(impact.KindRuntimeIdentity, "svc")), impact.Options{})
	checkEq(t, "reach of a change inside the widened scope", res.Reach(a), impact.ReachAffected)

	// A scope that cannot be computed gives unknown with the reason of the unresolved selector.
	closure = impact.WidenClosure(resolved, unresolved, scopeOf(nil, false))
	checkTrue(t, closure.Unknown, "an unknown scope is not unknown")
	checkEq(t, "unknown reason", closure.Reason, "binding-gap")
	checkTrue(t, !closure.Widened, "an unknown scope is widened")

	closure = impact.WidenClosure(nil, []impact.Unresolved{{Selector: "x"}}, scopeOf(nil, false))
	checkTrue(t, closure.Unknown, "an unknown scope without a reason is not unknown")
	checkEq(t, "default reason", closure.Reason, impact.ReasonCoverageIncomplete)
}
