package conformance_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
)

// goldenRuleIDs are the active and the reserved rule IDs of test/golden/rule-ids.json.
type goldenRuleIDs struct {
	Rules    []string `json:"rules"`
	Reserved []string `json:"reserved"`
}

func readGoldenRuleIDs(t *testing.T) goldenRuleIDs {
	t.Helper()

	data, err := os.ReadFile("../../test/golden/rule-ids.json")
	if err != nil {
		t.Fatal(err)
	}

	var golden goldenRuleIDs
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}

	return golden
}

// builtinRuleIDs are the rule IDs of the built-in packs; a duplicate ID is reported.
func builtinRuleIDs(t *testing.T) map[string]struct{} {
	t.Helper()

	current := map[string]struct{}{}

	for _, name := range packs.Names() {
		text, ok := packs.Source(name)
		if !ok {
			t.Fatalf("pack %s has no source", name)
		}

		info, problems := policy.DescribePack(policy.PolicyFile{Path: "builtin:" + name, Text: string(text)})
		if len(problems) > 0 {
			t.Fatalf("pack %s: %+v", name, problems)
		}

		for _, rule := range info.Rules {
			if _, dup := current[rule.ID]; dup {
				t.Errorf("rule ID %s is not unique", rule.ID)
			}

			current[rule.ID] = struct{}{}
		}
	}

	return current
}

// requireReservedRuleIDsUnused reports a reserved ID that a pack uses or that the golden also lists as active.
func requireReservedRuleIDsUnused(t *testing.T, golden *goldenRuleIDs, current map[string]struct{}) {
	t.Helper()

	for _, id := range golden.Reserved {
		if _, ok := current[id]; ok {
			t.Errorf("reserved rule ID %s is used", id)
		}

		if slices.Contains(golden.Rules, id) {
			t.Errorf("rule ID %s is both active and reserved in the golden", id)
		}
	}
}

// TestRuleIdentifiersAreStable: rule IDs of the built-in packs are unique, never
// removed (every ID of test/golden/rule-ids.json is still present), and a reserved
// ID is never used again.
func TestRuleIdentifiersAreStable(t *testing.T) {
	t.Parallel()

	golden := readGoldenRuleIDs(t)
	current := builtinRuleIDs(t)

	for _, id := range golden.Rules {
		if _, ok := current[id]; !ok {
			t.Errorf("rule ID %s of the golden was removed", id)
		}
	}

	requireReservedRuleIDsUnused(t, &golden, current)
}
