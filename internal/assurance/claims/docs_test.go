package claims_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/claims"
)

func quoteAll(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = "`" + item + "`"
	}

	return strings.Join(quoted, ", ")
}

// The tables of docs/reference/assurance.md are the registry: a contract or claim that changes changes the page.
func TestAssuranceReferenceListsTheRegistry(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../docs/reference/assurance.md")
	if err != nil {
		t.Fatal(err)
	}

	page := string(raw)
	registry := claims.Default()

	for _, contract := range registry.Contracts() {
		enabled := make([]string, len(contract.Enabled))
		for i, e := range contract.Enabled {
			enabled[i] = string(e.Claim)
		}

		experimental := "no"
		if contract.Experimental {
			experimental = "yes"
		}

		row := fmt.Sprintf("| %#q (%s) | %s | %s | %s | %s |", contract.ID, contract.Version, contract.Kind, quoteAll(enabled), quoteAll(contract.NotEnabled), experimental)
		if !strings.Contains(page, row+"\n") {
			t.Errorf("contract row missing from the page:\n%s", row)
		}
	}

	for _, id := range claims.IDs() {
		out := registry.Lookup(string(id))
		row := fmt.Sprintf("| %#q | %#q | %#q | %#q | %s |", id, out.Contract, out.Requires.Level, out.Requires.Completeness, out.Requires.Integrity.Text())

		if !strings.Contains(page, row+"\n") {
			t.Errorf("claim row missing from the page:\n%s", row)
		}
	}

	for _, reason := range []string{
		"claim-not-enabled", "no-evidence-supplied", "no-evaluator-available", "rendered-evidence-missing",
		"enforcement-not-declared", "reviewed-mode-not-available",
	} {
		if !strings.Contains(page, "| `"+reason+"` |") {
			t.Errorf("reason %s missing from the page", reason)
		}
	}
}
