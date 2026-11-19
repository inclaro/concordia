package intent //nolint:testpackage // the tests use unexported decision helpers.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The parity case test/parity/cases/concordia/decisions compares the decisions
// with the CALM CLI. These tests cover what that comparison cannot show.

func decisionFixture(t *testing.T, name string) any {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "test", "parity", "cases", "concordia", "decisions", name))
	if err != nil {
		t.Fatal(err)
	}

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}

	return v
}

func TestApplyArchitectureOptionsToPatternLeavesThePatternUnchanged(t *testing.T) {
	t.Parallel()

	pattern := decisionFixture(t, "one-of.pattern.json")
	original := decisionClone(pattern)

	for _, arch := range []string{"pass.json", "fail.json"} {
		if _, err := applyArchitectureOptionsToPattern(decisionFixture(t, arch), pattern); err != nil {
			t.Fatalf("%s: %v", arch, err)
		}

		if !reflect.DeepEqual(pattern, original) {
			t.Fatalf("%s: the input pattern was changed", arch)
		}
	}
}

func TestApplyArchitectureOptionsToPatternGivesTheTypeErrorOfTheCalmCLI(t *testing.T) {
	t.Parallel()

	if _, err := applyArchitectureOptionsToPattern(decisionFixture(t, "pass.json"), obj{}); !errors.Is(err, errDecisionTypeError) {
		t.Fatalf("err = %v, want errDecisionTypeError", err)
	}
}
