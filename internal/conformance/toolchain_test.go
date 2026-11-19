package conformance_test

import (
	"encoding/json"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
)

// The Structurizr version is in the toolchain only when a view export was verified: a run without one has the
// toolchain of before (the commerce goldens do not change), and a run with one validates against the schema.
func TestToolchainRecordsStructurizrOnlyForVerifiedExports(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)

	toolchainOf := func(r *conformance.Run) map[string]any {
		t.Helper()

		var tree map[string]any
		if err := json.Unmarshal(canonicalOf(t, r), &tree); err != nil {
			t.Fatal(err)
		}

		return child(t, tree, "toolchain")
	}

	if _, present := toolchainOf(run)["structurizr"]; present {
		t.Fatal("a run without a verified view export has toolchain.structurizr")
	}

	run.Toolchain.Structurizr = "2025.11.09"

	if got := toolchainOf(run)["structurizr"]; got != "2025.11.09" {
		t.Errorf("toolchain.structurizr = %v", got)
	}

	if errs := conformance.ValidateResultJSON(canonicalOf(t, run)); len(errs) > 0 {
		t.Errorf("a result with toolchain.structurizr does not validate: %v", errs)
	}
}
