package impact_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
)

// TestDocumentedRuleIDsExist keeps the rule IDs of docs/explanation/review-currency.md equal to
// impact.RuleIDs. The policy pack that carries the same IDs is checked against impact.RuleIDs by
// the pack test, so the document, this package, and the pack name one list.
func TestDocumentedRuleIDsExist(t *testing.T) {
	doc := readDoc(t)

	var named []string

	for _, id := range regexp.MustCompile(`REV-[A-Z]+-\d{3}`).FindAllString(doc, -1) {
		if !slices.Contains(named, id) {
			named = append(named, id)
		}

		if !slices.Contains(impact.RuleIDs(), id) {
			t.Errorf("the document names rule %s, which impact.RuleIDs does not list", id)
		}
	}

	for _, id := range impact.RuleIDs() {
		if !slices.Contains(named, id) {
			t.Errorf("the document does not name rule %s", id)
		}
	}
}

// TestDocumentedDiagnosticCodesExist checks every diagnostic code of the document against the
// diagnostic catalog.
func TestDocumentedDiagnosticCodesExist(t *testing.T) {
	codes := regexp.MustCompile(`CO-[A-Z]+-\d{3}`).FindAllString(readDoc(t), -1)
	if len(codes) == 0 {
		t.Fatal("the document names no diagnostic code")
	}

	for _, code := range codes {
		if _, ok := diag.Lookup(code); !ok {
			t.Errorf("the document names %s, which is not in the diagnostic catalog", code)
		}
	}
}

// TestDocumentedTriggerClassesMatchCode keeps the class table equal to the classification.
func TestDocumentedTriggerClassesMatchCode(t *testing.T) {
	doc := readDoc(t)
	_, section, ok := strings.Cut(doc, "## Review triggers")

	if !ok {
		t.Fatal("section `Review triggers` missing")
	}

	section, _, _ = strings.Cut(section, "\n## ")
	row := regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\| (.+) \\|$")
	table := map[impact.Class][]impact.Kind{}

	for _, m := range row.FindAllStringSubmatch(section, -1) {
		class := impact.Class(m[1])

		for _, kind := range regexp.MustCompile("`([a-z-]+)`").FindAllStringSubmatch(m[2], -1) {
			if impact.Kind(kind[1]).Valid() {
				table[class] = append(table[class], impact.Kind(kind[1]))
			}
		}
	}

	for _, class := range impact.Classes() {
		if !strings.Contains(section, "| `"+string(class)+"` |") {
			t.Errorf("class %s is not in the table", class)
		}
	}

	for _, kind := range impact.Kinds() {
		want, _ := impact.ClassOfKind(kind)
		if !slices.Contains(table[want], kind) {
			t.Errorf("the table does not list %s under %s", kind, want)
		}
	}
}

// TestDocumentedLimitDefaultsMatchCode checks the default limits that the document states.
func TestDocumentedLimitDefaultsMatchCode(t *testing.T) {
	want := "(64, 100000, and 120 seconds)"
	if impact.DefaultMaxDepth != 64 || impact.DefaultMaxVisited != 100000 || impact.DefaultMaxSeconds != 120 {
		t.Fatalf("defaults changed: update the document text %q", want)
	}

	if !strings.Contains(readDoc(t), want) {
		t.Errorf("the document does not state the defaults %q", want)
	}
}
