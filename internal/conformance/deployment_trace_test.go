package conformance_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
)

// The gap reasons of the trace are the closed `unknown` reasons of the foundation and three of the trace itself.
func TestTraceGapReasonsIncludeTheFoundationReasons(t *testing.T) {
	t.Parallel()

	gap := make([]string, 0, len(deployment.Reasons()))
	for _, reason := range deployment.Reasons() {
		gap = append(gap, string(reason))
	}

	for _, reason := range conformance.UnknownReasons() {
		if !slices.Contains(gap, string(reason)) {
			t.Errorf("the foundation reason %s is not a gap reason", reason)
		}
	}

	if len(gap) != len(conformance.UnknownReasons())+3 {
		t.Errorf("gap reasons = %v", gap)
	}
}

func TestSettleAreasShowsNoTraceAsNotConfigured(t *testing.T) {
	t.Parallel()

	in := &deployment.Input{}
	trace := deployment.BuildTrace(in)

	if trace.Configured() {
		t.Fatal("a trace with no environment is configured")
	}

	run := &conformance.Run{DeploymentTrace: *trace}
	run.SettleAreas()

	if got := run.Areas.DeploymentTrace.Verdict; got != conformance.AreaNotConfigured {
		t.Errorf("area = %s", got)
	}
}
