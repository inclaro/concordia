package redact_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/redact"
)

const fixtureRoot = "../../test/fixtures/infra"

type seed struct {
	Path  string `json:"path"`
	Class string `json:"class"`
	Value string `json:"value"`
}

func loadSeeds(t *testing.T) []seed {
	t.Helper()

	data, err := os.ReadFile("../../test/fixtures/infra-seeds.json")
	if err != nil {
		t.Fatal(err)
	}

	var seeds []seed
	if err := json.Unmarshal(data, &seeds); err != nil {
		t.Fatal(err)
	}

	if len(seeds) == 0 {
		t.Fatal("no seeds")
	}

	return seeds
}

func readFixture(t *testing.T, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestRedactRemovesEverySeededSecretOfTheInfraFixture(t *testing.T) {
	t.Parallel()

	rules := redact.Default()
	seeds := loadSeeds(t)
	seeded := map[string]bool{}

	for _, s := range seeds {
		seeded[s.Path] = true
	}

	for path := range seeded {
		out, records := rules.Redact(path, readFixture(t, path))

		assertSeedsGone(t, path, out, seeds)
		assertRecordsComplete(t, path, records)
	}
}

// assertSeedsGone checks that the output of the fixture at path has no seed of that fixture.
func assertSeedsGone(t *testing.T, path, out string, seeds []seed) {
	t.Helper()

	for _, s := range seeds {
		if s.Path == path && strings.Contains(out, s.Value) {
			t.Errorf("%s: output still has the %s seed", path, s.Class)
		}
	}
}

// assertRecordsComplete checks that there is a record for the fixture at path and that every
// record has the file, a class, and a line.
func assertRecordsComplete(t *testing.T, path string, records []redact.Record) {
	t.Helper()

	if len(records) == 0 {
		t.Errorf("%s: no redaction record", path)
	}

	for _, r := range records {
		if isIncomplete(r, path) {
			t.Errorf("%s: incomplete record %+v", path, r)
		}
	}
}

// isIncomplete says whether the record lacks the file, the class, or the line.
func isIncomplete(r redact.Record, path string) bool {
	if r.File != path || r.Class == "" {
		return true
	}

	return r.Line <= 0
}

func TestRedactNoOutputOfTheFixtureRepositoryHasASeed(t *testing.T) {
	t.Parallel()

	rules := redact.Default()
	seeds := loadSeeds(t)

	err := filepath.WalkDir(fixtureRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || strings.HasSuffix(p, ".png") {
			return nil
		}

		return checkFixtureFile(t, rules, p, seeds)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// checkFixtureFile redacts the file at p and checks that no seed is in the output or the records.
func checkFixtureFile(t *testing.T, rules redact.Rules, p string, seeds []seed) error {
	t.Helper()

	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	rel, _ := filepath.Rel(fixtureRoot, p)
	out, records := rules.Redact(filepath.ToSlash(rel), string(data))

	assertSeedsNotInOutput(t, rel, out, seeds)

	raw, err := json.Marshal(records)
	if err != nil {
		return err
	}

	assertSeedsNotInRecords(t, rel, string(raw), seeds)

	return nil
}

// assertSeedsNotInOutput checks that no seed is in the output for the file at rel.
func assertSeedsNotInOutput(t *testing.T, rel, out string, seeds []seed) {
	t.Helper()

	for _, s := range seeds {
		if strings.Contains(out, s.Value) {
			t.Errorf("%s: seed %q (%s) is in the output", rel, s.Value, s.Class)
		}
	}
}

// assertSeedsNotInRecords checks that no seed is in the records (as JSON) for the file at rel.
func assertSeedsNotInRecords(t *testing.T, rel, raw string, seeds []seed) {
	t.Helper()

	for _, s := range seeds {
		if strings.Contains(raw, s.Value) {
			t.Errorf("%s: seed %q is in the records", rel, s.Value)
		}
	}
}

func TestRedactKeepsIdentifiersAndStructureOfTheSecretManifest(t *testing.T) {
	t.Parallel()

	checkSecretManifest(t)
	checkDeploymentManifest(t)
}

// propLine is the property and the line that a record is expected to have.
type propLine struct {
	property string
	line     int
}

// assertContainsAll checks that the text of the named output has every wanted part.
func assertContainsAll(t *testing.T, name, text string, wants []string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q:\n%s", name, want, text)
		}
	}
}

// startsWith says whether there are count records and the first ones have the wanted property and line.
func startsWith(records []redact.Record, count int, wants ...propLine) bool {
	if len(records) != count {
		return false
	}

	for i, want := range wants {
		if records[i].Property != want.property || records[i].Line != want.line {
			return false
		}
	}

	return true
}

func checkSecretManifest(t *testing.T) {
	t.Helper()

	out, records := redact.Default().Redact("k8s/prod/secret.yaml", readFixture(t, "k8s/prod/secret.yaml"))

	assertContainsAll(t, "output", out, []string{
		"kind: Secret", "name: api-secrets", "stringData:", "  password: [REDACTED:secret-data]", "data:", "  token: [REDACTED:secret-data]",
	})

	if !startsWith(records, 4, propLine{"password", 8}, propLine{"token", 10}) {
		t.Errorf("records = %+v", records)
	}
}

func checkDeploymentManifest(t *testing.T) {
	t.Helper()

	deployment, records := redact.Default().Redact("k8s/prod/deployment.yaml", readFixture(t, "k8s/prod/deployment.yaml"))

	assertContainsAll(t, "deployment", deployment, []string{
		"name: DB_PASSWORD", "value: [REDACTED:env-value]", "secretKeyRef:", "name: api-secrets", "key: token",
	})

	if !startsWith(records, 1, propLine{"DB_PASSWORD", 24}) || records[0].Class != redact.ClassEnvValue {
		t.Errorf("records = %+v", records)
	}
}

func TestRedactIsDeterministic(t *testing.T) {
	t.Parallel()

	rules := redact.Default()

	for _, s := range loadSeeds(t) {
		content := readFixture(t, s.Path)
		first, firstRecords := rules.Redact(s.Path, content)
		second, secondRecords := rules.Redact(s.Path, content)

		if first != second {
			t.Errorf("%s: outputs differ", s.Path)
		}

		if !reflect.DeepEqual(firstRecords, secondRecords) {
			t.Errorf("%s: records differ: %+v / %+v", s.Path, firstRecords, secondRecords)
		}
	}

	if rules.Version == "" || rules.Version != redact.Version {
		t.Errorf("version = %q", rules.Version)
	}
}

// The output has no hash, prefix, or length of the value: a value of any other length and content
// gives the same output.
func TestRedactOutputHoldsNothingOfTheValue(t *testing.T) {
	t.Parallel()

	rules := redact.Default()
	others := []string{"x", "Zq9-completely-different-and-much-longer-value-0123456789-abcdefghijklmnopqrstuvwxyz"}

	for _, s := range loadSeeds(t) {
		assertOutputIgnoresValue(t, rules, s, others)
	}
}

// assertOutputIgnoresValue checks that replacing the seed in its fixture by any of the others
// leaves the output as it is.
func assertOutputIgnoresValue(t *testing.T, rules redact.Rules, s seed, others []string) {
	t.Helper()

	content := readFixture(t, s.Path)
	if !strings.Contains(content, s.Value) {
		return // the decoded form of a base64 value is not in the file.
	}

	want, _ := rules.Redact(s.Path, content)

	for _, other := range others {
		got, _ := rules.Redact(s.Path, strings.ReplaceAll(content, s.Value, other))
		if got != want {
			t.Errorf("%s: output depends on the value (%q):\n%s\n---\n%s", s.Path, other, want, got)
		}
	}
}

func TestRedactMultiLineValuesLeaveNoTraceOfTheirLength(t *testing.T) {
	t.Parallel()

	short := "-----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY-----\n"
	long := "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB\n", 40) + "-----END RSA PRIVATE KEY-----\n"

	a, ra := redact.Default().Redact("notes.txt", "before\n"+short+"after\n")
	b, rb := redact.Default().Redact("notes.txt", "before\n"+long+"after\n")

	if a != "before\n[REDACTED:private-key]\nafter\n" || a != b {
		t.Errorf("outputs: %q / %q", a, b)
	}

	if !sameSingleRecord(ra, rb, 2) {
		t.Errorf("records: %+v / %+v", ra, rb)
	}
}

// sameSingleRecord says whether both lists hold one record, the same record, that is at the line.
func sameSingleRecord(a, b []redact.Record, line int) bool {
	if len(a) != 1 || len(b) != 1 {
		return false
	}

	return a[0] == b[0] && a[0].Line == line
}

func TestRedactRecordsHoldNoValue(t *testing.T) {
	t.Parallel()

	text := "FROM x\nENV API_TOKEN=abc123xyz\n"
	_, records := redact.Default().Redact("Dockerfile", text)

	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(raw), "abc123") || len(records) != 1 {
		t.Errorf("records = %s", raw)
	}

	if want := `[{"file":"Dockerfile","property":"API_TOKEN","class":"env-value","line":2}]`; string(raw) != want {
		t.Errorf("records = %s, want %s", raw, want)
	}
}

func TestRedactKeepsLineEndings(t *testing.T) {
	t.Parallel()

	out, _ := redact.Default().Redact("a.sh", "export TOKEN=abc\r\necho hi\r\n")
	if out != "export TOKEN=[REDACTED:env-value]\r\necho hi\r\n" {
		t.Errorf("out = %q", out)
	}
}

func TestRedactLinesKeepsOriginalNumbers(t *testing.T) {
	t.Parallel()

	content := "a: 1\nkey: |\n  -----BEGIN PRIVATE KEY-----\n  AAAA\n  -----END PRIVATE KEY-----\nz: 2\n"
	lines, records := redact.Default().RedactLines("x.yaml", content)

	got, numbers := textsAndNumbers(lines)

	if len(records) != 1 || records[0].Line != 3 {
		t.Errorf("records = %+v", records)
	}

	if strings.Join(got, "|") != "a: 1|key: ||  [REDACTED:private-key]|z: 2|" {
		t.Errorf("lines = %q", got)
	}

	if len(numbers) != 5 || !slices.Equal(numbers[:4], []int{1, 2, 3, 6}) {
		t.Errorf("numbers = %v", numbers)
	}
}

// textsAndNumbers splits the lines into their texts and their numbers.
func textsAndNumbers(lines []redact.Line) (texts []string, numbers []int) {
	texts = make([]string, 0, len(lines))
	numbers = make([]int, 0, len(lines))

	for _, l := range lines {
		texts = append(texts, l.Text)
		numbers = append(numbers, l.Number)
	}

	return texts, numbers
}

func TestRedactValue(t *testing.T) {
	t.Parallel()

	rules := redact.Default()

	out, records := rules.RedactValue("db_password", "hunter2")
	if out != "[REDACTED:secret-data]" || !isValueRecord(records, "db_password") {
		t.Errorf("sensitive name: %q %+v", out, records)
	}

	out, records = rules.RedactValue("note", "token is ghp_CONCORDIASEEDshelltoken0000000000000 ok")
	if out != "token is [REDACTED:token] ok" || !isTokenRecord(records, "note") {
		t.Errorf("token shape: %q %+v", out, records)
	}

	out, records = rules.RedactValue("secretName", "db-credentials")
	if out != "db-credentials" || len(records) != 0 {
		t.Errorf("identifier: %q %+v", out, records)
	}
}

// hasSingleProperty says whether there is one record, and it has the property.
func hasSingleProperty(records []redact.Record, property string) bool {
	return len(records) == 1 && records[0].Property == property
}

// isValueRecord says whether there is one record for the property, and it has no line.
func isValueRecord(records []redact.Record, property string) bool {
	if !hasSingleProperty(records, property) {
		return false
	}

	return records[0].Line == 0
}

// isTokenRecord says whether there is one record for the property, and its class is token.
func isTokenRecord(records []redact.Record, property string) bool {
	if !hasSingleProperty(records, property) {
		return false
	}

	return records[0].Class == redact.ClassToken
}

func TestSanitize(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, line, text string }{
		{"plain text", "plain text", "plain text"},
		{"a\x1b[31mred\x1b[0m", `a\x1b[31mred\x1b[0m`, `a\x1b[31mred\x1b[0m`},
		{"two\nlines\tand tab", "two\\nlines\tand tab", "two\nlines\tand tab"},
		{"over\rwrite", `over\rwrite`, `over\rwrite`},
		{"bell\x07 nul\x00 del\x7f", `bell\x07 nul\x00 del\x7f`, `bell\x07 nul\x00 del\x7f`},
		{"c1\u009b31m", `c1\x9b31m`, `c1\x9b31m`},
		{"bidi\u202eevil", `bidi\u202eevil`, `bidi\u202eevil`},
		{"bad\xffbyte", `bad\xffbyte`, `bad\xffbyte`},
		{"ünïcode ✓", "ünïcode ✓", "ünïcode ✓"},
	}

	for _, tc := range tests {
		assertSanitizeLine(t, tc.in, tc.line)
	}

	for _, tc := range tests {
		assertSanitize(t, tc.in, tc.text)
	}
}

// assertSanitizeLine checks that SanitizeLine gives want for in.
func assertSanitizeLine(t *testing.T, in, want string) {
	t.Helper()

	if got := redact.SanitizeLine(in); got != want {
		t.Errorf("SanitizeLine(%q) = %q, want %q", in, got, want)
	}
}

// assertSanitize checks that Sanitize gives want for in.
func assertSanitize(t *testing.T, in, want string) {
	t.Helper()

	if got := redact.Sanitize(in); got != want {
		t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
	}
}
