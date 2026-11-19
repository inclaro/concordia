package impact_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/impact"
)

const reviewCurrencyDoc = "../../docs/explanation/review-currency.md"

func readDoc(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(filepath.FromSlash(reviewCurrencyDoc))
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// TestDocumentedDigestImpactExample runs the digest-impact example of
// docs/explanation/review-currency.md and compares the report with the block in the document.
//
// Task 4.5 asks for the run to live in internal/cli/assessment_test.go once the `assessment` command
// exists. That command belongs to another slice and is not there yet, so the example runs against
// the package API here. The CLI test should repeat the same inputs against the command output.
func TestDocumentedDigestImpactExample(t *testing.T) {
	doc := readDoc(t)

	_, after, ok := strings.Cut(doc, "### Digest-impact example")
	if !ok {
		t.Fatal("section `Digest-impact example` missing")
	}

	_, block, ok := strings.Cut(after, "```json\n")
	if !ok {
		t.Fatal("json block missing in the example")
	}

	block, _, ok = strings.Cut(block, "```")
	if !ok {
		t.Fatal("json block not closed")
	}

	v1 := impact.V1()
	v2 := contractWithout(v1, "review-digest/2", impact.KindPublicEntryPoint, "exposureClass")
	report := impact.NewRegistry(v1, v2).DigestImpact("review-digest/2", []impact.Bound{
		{Assessment: "ASM-EDGE@2", Approval: "ASM-EDGE-v2-1", Contract: impact.ContractVersion},
		{Assessment: "ASM-DATA@1", Approval: "ASM-DATA-v1-1", Contract: impact.ContractVersion},
	})

	got, err := canonjson.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}

	want, err := canonjson.Canonicalize([]byte(block))
	if err != nil {
		t.Fatalf("documented block is not JSON: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("documented report differs from the computed one:\n got: %s\nwant: %s", got, want)
	}

	var decoded impact.DigestImpactReport
	if err := json.Unmarshal([]byte(block), &decoded); err != nil || len(decoded.Affected) != 2 {
		t.Errorf("decoded = %+v, err = %v", decoded, err)
	}
}

// TestDocumentedContractTableMatchesV1 keeps the field table of the document equal to the contract.
func TestDocumentedContractTableMatchesV1(t *testing.T) {
	doc := readDoc(t)
	row := regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\| (`[A-Za-z]+`(?:, `[A-Za-z]+`)*) \\|$")
	table := map[impact.Kind][]string{}

	for _, m := range row.FindAllStringSubmatch(doc, -1) {
		parts := strings.Split(m[2], ", ")

		fields := make([]string, 0, len(parts))
		for _, f := range parts {
			fields = append(fields, strings.Trim(f, "`"))
		}

		slices.Sort(fields)

		table[impact.Kind(m[1])] = fields
	}

	c := impact.V1()

	if len(table) != len(impact.Kinds()) {
		t.Fatalf("table has %d kinds, want %d", len(table), len(impact.Kinds()))
	}

	for _, k := range impact.Kinds() {
		if got, want := table[k], c.FieldsOf(k); !slices.Equal(got, want) {
			t.Errorf("%s: document %v, contract %v", k, got, want)
		}
	}

	if !strings.Contains(doc, "`"+impact.ContractVersion+"`") {
		t.Error("document does not name the contract version")
	}
}
