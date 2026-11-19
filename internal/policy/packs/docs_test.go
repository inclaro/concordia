package packs_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/policy/packs"
)

// reviewRuleIDs are the rule IDs of the review-currency pack, sorted.
var reviewRuleIDs = []string{"REV-APR-001", "REV-CAP-001", "REV-DSP-001", "REV-GOV-001"}

// Every REV-* rule of the pack appears in docs/reference/policies.md, and the pack has no rule that this list lacks.
func TestEveryReviewRuleIsDocumentedInThePoliciesReference(t *testing.T) {
	t.Parallel()

	docs, err := os.ReadFile("../../../docs/reference/policies.md")
	if err != nil {
		t.Fatal(err)
	}

	source, ok := packs.Source("review-currency")
	if !ok {
		t.Fatal("builtin pack review-currency is missing")
	}

	var inPack []string

	for line := range strings.SplitSeq(string(source), "\n") {
		if id, found := strings.CutPrefix(strings.TrimSpace(line), "- id: "); found && strings.HasPrefix(id, "REV-") {
			inPack = append(inPack, id)
		}
	}

	slices.Sort(inPack)

	if !slices.Equal(inPack, reviewRuleIDs) {
		t.Fatalf("pack rules = %v, want %v", inPack, reviewRuleIDs)
	}

	for _, id := range inPack {
		if !strings.Contains(string(docs), "`"+id+"`") {
			t.Errorf("docs/reference/policies.md does not describe %s", id)
		}
	}
}
