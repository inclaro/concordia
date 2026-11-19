package cli_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/testutil"
)

// approvedEdge writes the edge assessment, approves it with the given extra flags, and gives the tree (with the
// record) and the dependencies of a run whose trust anchor holds the record.
func approvedEdge(t *testing.T, extra ...string) (dir string, deps cli.Deps) {
	t.Helper()

	dir = t.TempDir()
	testutil.WriteTree(t, dir, reviewTree())

	text, res := approve(t, dir, reviewDeps(nil), extra...)
	if res.Code != 0 {
		t.Fatalf("approve: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	return dir, reviewDeps(map[string]string{"concordia.yaml": reviewWorkspaceYAML, edgeApproval: text})
}

func statusJSON(t *testing.T, dir string, deps cli.Deps, args ...string) map[string]any {
	t.Helper()

	res := reviewRun(t, dir, deps, nil, append([]string{"assessment", "status", "--json"}, args...)...)
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, res.Stdout)
	}

	return out
}

func currencyOf(t *testing.T, report map[string]any) map[string]any {
	t.Helper()

	list, _ := report["assessments"].([]any)
	if len(list) == 0 {
		t.Fatalf("no assessments in %v", report)
	}

	first, _ := list[0].(map[string]any)
	cur, _ := first["currency"].(map[string]any)

	return cur
}

// Scenario: Status at a date.
func TestAssessmentStatusStatesTheEvaluationTimeAndExpiresAtAnotherDate(t *testing.T) {
	t.Parallel()

	dir, deps := approvedEdge(t, "--expires", "2026-06-01")

	res := reviewRun(t, dir, deps, nil, "assessment", "status")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	for _, want := range []string{"Assessment currency at 2026-05-01T10:00:00.000Z", "ASM-EDGE v1: current", "approval ASM-EDGE-v1-1 by alice@example.com", "edge: ASM-EDGE v1 is current"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, res.Stdout)
		}
	}

	later := reviewRun(t, dir, deps, nil, "assessment", "status", "--at", "2027-01-01")
	if later.Code != 0 {
		t.Fatalf("--at: exit code = %d, stderr = %q", later.Code, later.Stderr)
	}

	for _, want := range []string{"Assessment currency at 2027-01-01T00:00:00.000Z", "ASM-EDGE v1: expired", "edge: ASM-EDGE v1 is expired"} {
		if !strings.Contains(later.Stdout, want) {
			t.Errorf("--at stdout lacks %q:\n%s", want, later.Stdout)
		}
	}

	if bad := reviewRun(t, dir, deps, nil, "assessment", "status", "--at", "tomorrow"); bad.Code != 2 {
		t.Errorf("--at tomorrow: exit code = %d, stderr = %q", bad.Code, bad.Stderr)
	}
}

func TestAssessmentStatusJSONIsCanonicalAndStable(t *testing.T) {
	t.Parallel()

	dir, deps := approvedEdge(t)

	first := reviewRun(t, dir, deps, nil, "assessment", "status", "--json")
	second := reviewRun(t, dir, deps, nil, "assessment", "status", "--json")

	if first.Code != 0 || first.Stdout != second.Stdout {
		t.Fatalf("exit code = %d; output differs between runs:\n%s\n%s", first.Code, first.Stdout, second.Stdout)
	}

	canonical, err := canonjson.Canonicalize([]byte(first.Stdout))
	if err != nil || !bytes.Equal(bytes.TrimSpace(canonical), bytes.TrimSpace([]byte(first.Stdout))) {
		t.Errorf("the output is not canonical JSON (err %v):\n%s", err, first.Stdout)
	}

	report := statusJSON(t, dir, deps)
	if report["evaluatedAt"] != "2026-05-01T10:00:00.000Z" || currencyOf(t, report)["state"] != "current" {
		t.Errorf("report = %v", report)
	}
}

// Scenario: A25 manual reviewed flag without authorized approval.
func TestAssessmentStatusWithoutApprovalIsReviewRequired(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := reviewTree()
	files["assessments/ASM-EDGE/v1.yaml"] = strings.Replace(reviewEdgeDoc, "outcome: satisfactory\n", "outcome: satisfactory\nreviewed: true\nnotes: reviewed by security\n", 1)
	testutil.WriteTree(t, dir, files)

	before := dirFiles(t, dir)
	report := statusJSON(t, dir, reviewDeps(nil))
	cur := currencyOf(t, report)

	if cur["state"] != "review-required" || !strings.Contains(strings.Join(stringsOf(cur["reasons"]), ","), "no approval") {
		t.Errorf("currency = %v", cur)
	}

	res := reviewRun(t, dir, reviewDeps(nil), nil, "assessment", "status")
	if !strings.Contains(res.Stdout, "not an approval") {
		t.Errorf("stdout does not name the missing authorized approval:\n%s", res.Stdout)
	}

	if after := dirFiles(t, dir); strings.Join(before, ",") != strings.Join(after, ",") {
		t.Errorf("status wrote files: %v -> %v", before, after)
	}
}

func TestAssessmentStatusFiltersByAssessment(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := reviewTree()
	files["assessments/ASM-LATENCY/v1.yaml"] = assessmentDocQuality
	testutil.WriteTree(t, dir, files)

	report := statusJSON(t, dir, reviewDeps(nil), "--assessment", "ASM-LATENCY")
	list, _ := report["assessments"].([]any)

	var first map[string]any
	if len(list) > 0 {
		first, _ = list[0].(map[string]any)
	}

	if len(list) != 1 || first["id"] != "ASM-LATENCY" {
		t.Errorf("assessments = %v", list)
	}

	if res := reviewRun(t, dir, reviewDeps(nil), nil, "assessment", "status", "--assessment", "ASM-NONE"); res.Code != 2 {
		t.Errorf("unknown id: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func stringsOf(v any) []string {
	var out []string

	list, _ := v.([]any)
	for _, item := range list {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}

	return out
}

// contractV2 is the contract of the documented digest-impact example: review-digest/1 without `exposureClass` on
// public-entry-point and with no equivalence mark.
func contractV2() impact.Contract {
	v2 := impact.V1()
	v2.Version = "review-digest/2"
	v2.Fields = maps.Clone(v2.Fields)

	var kept []string

	for _, f := range v2.Fields[impact.KindPublicEntryPoint] {
		if f != "exposureClass" {
			kept = append(kept, f)
		}
	}

	v2.Fields[impact.KindPublicEntryPoint] = kept

	return v2
}

// statusApproveEach approves the assessments with the given ids, each with the confirmation of its subject digest.
func statusApproveEach(t *testing.T, dir string, ids []string) {
	t.Helper()

	for _, id := range ids {
		args := []string{"assessment", "approve", id, "--reviewer", "alice@example.com", "--authority", "security-review"}
		first := reviewRun(t, dir, reviewDeps(nil), nil, args...)

		confirm := append(slices.Clone(args), "--confirm-subject", subjectDigestOf(t, first.Stdout))

		res := reviewRun(t, dir, reviewDeps(nil), nil, confirm...)
		if res.Code != 0 {
			t.Fatalf("approve %s: exit code = %d, stderr = %q", id, res.Code, res.Stderr)
		}
	}
}

// statusApproveAndAnchor approves the assessments with the given ids and gives the trust anchor that holds the records.
func statusApproveAndAnchor(t *testing.T, dir string, ids, records []string) map[string]string {
	t.Helper()

	statusApproveEach(t, dir, ids)

	anchored := map[string]string{"concordia.yaml": reviewWorkspaceYAML}

	for _, rel := range records {
		text, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}

		anchored[rel] = string(text)
	}

	return anchored
}

// Task 4.5: the digest-impact example of docs/explanation/review-currency.md, run through `assessment status`.
func TestAssessmentStatusDigestImpactMatchesTheDocumentedExample(t *testing.T) {
	t.Parallel()

	block := statusDocJSONBlock(t, filepath.Join("..", "..", "docs", "explanation", "review-currency.md"), "### Digest-impact example")

	want, err := canonjson.Canonicalize([]byte(block))
	if err != nil {
		t.Fatalf("the documented block is not JSON: %v", err)
	}

	dir := t.TempDir()
	files := reviewTree()
	delete(files, "assessments/ASM-EDGE/v1.yaml")
	files["assessments/ASM-EDGE/v2.yaml"] = strings.Replace(reviewEdgeDoc, "version: 1", "version: 2", 1)
	files["assessments/ASM-DATA/v1.yaml"] = strings.Replace(reviewEdgeDoc, "id: ASM-EDGE", "id: ASM-DATA", 1)
	testutil.WriteTree(t, dir, files)

	anchored := statusApproveAndAnchor(t, dir, []string{"ASM-EDGE", "ASM-DATA"},
		[]string{"assessments/approvals/ASM-EDGE-v2-1.yaml", "assessments/approvals/ASM-DATA-v1-1.yaml"})

	deps := reviewDeps(anchored)
	v2 := contractV2()
	registry := impact.NewRegistry(impact.V1(), v2)
	deps.ReviewContracts = &cli.ReviewContracts{Current: v2, Known: registry}

	report := statusJSON(t, dir, deps)

	got, err := canonjson.Marshal(report["digestImpact"])
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("digest impact differs from the documented example:\n got: %s\nwant: %s", got, want)
	}
}

// keyPaths collects the key paths of a JSON value. Array elements share one path, and a path below skip is left out.
func keyPaths(prefix string, v any, skip string, out map[string]bool) {
	switch value := v.(type) {
	case map[string]any:
		for k, child := range value {
			path := prefix + "." + k
			out[path] = true

			if path != skip {
				keyPaths(path, child, skip, out)
			}
		}
	case []any:
		for _, child := range value {
			keyPaths(prefix+"[]", child, skip, out)
		}
	}
}

// statusDocJSONBlock is the first JSON code block after the heading in a documentation file.
func statusDocJSONBlock(t *testing.T, docPath, heading string) string {
	t.Helper()

	doc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}

	_, after, ok := strings.Cut(string(doc), heading)
	if !ok {
		t.Fatalf("section `%s` missing", strings.TrimPrefix(heading, "### "))
	}

	_, block, _ := strings.Cut(after, "```json\n")
	block, _, _ = strings.Cut(block, "```")

	return block
}

// Task 6.7: the `assessment status --json` sample of docs/reference/assessments.md has the keys of the real output.
func TestAssessmentStatusJSONHasTheKeysOfTheDocumentedSample(t *testing.T) {
	t.Parallel()

	block := statusDocJSONBlock(t, filepath.Join("..", "..", "docs", "reference", "assessments.md"), "### Status output")

	var sample map[string]any
	if err := json.Unmarshal([]byte(block), &sample); err != nil {
		t.Fatalf("the documented sample is not JSON: %v", err)
	}

	dir, deps := approvedEdge(t, "--expires", "2026-06-01")
	actual := statusJSON(t, dir, deps)

	// The documented sample says that required[].currency repeats the keys of assessments[].currency.
	const skip = ".required[].currency"

	got, want := map[string]bool{}, map[string]bool{}
	keyPaths("", actual, skip, got)
	keyPaths("", sample, skip, want)

	for path := range statusMissingKeys(want, got) {
		t.Errorf("the sample documents %s and the output has no such key", path)
	}

	for path := range statusMissingKeys(got, want) {
		t.Errorf("the output has the key %s and the sample does not document it", path)
	}

	// The documented sample is the output itself apart from the digest of the document file.
	if sample["evaluatedAt"] != actual["evaluatedAt"] || sample["contractVersion"] != actual["contractVersion"] {
		t.Errorf("sample = %v", sample)
	}
}

// statusMissingKeys is the key paths of have that are not in other.
func statusMissingKeys(have, other map[string]bool) map[string]bool {
	missing := map[string]bool{}

	for path := range have {
		if !other[path] {
			missing[path] = true
		}
	}

	return missing
}
