package conformance_test

import (
	"encoding/json"
	"testing"
)

// requireReviewKeysAbsent checks that none of the keys is in the section of results.json.
func requireReviewKeysAbsent(t *testing.T, section map[string]any, keys []string, where string) {
	t.Helper()

	for _, key := range keys {
		if _, present := section[key]; present {
			t.Errorf("%s has the key %s without assessments", where, key)
		}
	}
}

func TestARunWithoutAssessmentsHasNoReviewKeys(t *testing.T) {
	t.Parallel()

	run := newSetup(t, nil, nil).mustCheck(t, nil)

	var tree map[string]any
	if err := json.Unmarshal(canonicalOf(t, run), &tree); err != nil {
		t.Fatal(err)
	}

	requireReviewKeysAbsent(t, tree, []string{"assessmentCurrency", "governance", "analysis"}, "results.json")

	inputs, ok := tree["inputs"].(map[string]any)
	if !ok {
		t.Fatalf("results.json has no inputs object: %v", tree["inputs"])
	}

	requireReviewKeysAbsent(t, inputs, []string{"assessments", "approvals", "authorityDigest", "trustAnchor"}, "inputs")
}
