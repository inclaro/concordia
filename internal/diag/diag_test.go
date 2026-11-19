package diag_test

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
)

var codePattern = regexp.MustCompile(`^CO-[A-Z]{3}-\d{3}$`)

func pathParams(path string) diag.Params {
	return diag.Params{"path": path}
}

func TestCatalogHasMessageForEveryCodeAndUsesCOFormat(t *testing.T) {
	t.Parallel()

	all := diag.All()
	seen := make(map[string]bool, len(all))

	for _, e := range all {
		if !codePattern.MatchString(e.Code) {
			t.Errorf("code %q does not match the CO- format", e.Code)
		}

		if e.Message == "" {
			t.Errorf("code %s has no message", e.Code)
		}

		if seen[e.Code] {
			t.Errorf("code %s is twice in the catalog", e.Code)
		}

		seen[e.Code] = true
	}

	if seen["CO-BND-007"] {
		t.Error("CO-BND-007 is reserved and must not be in the catalog")
	}
}

func TestEveryCatalogCodeIsDocumented(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../docs/reference/diagnostics.md")
	if err != nil {
		t.Fatal(err)
	}

	doc := string(raw)
	all := diag.All()

	for _, e := range all {
		if !strings.Contains(doc, e.Code) {
			t.Errorf("code %s is not in docs/reference/diagnostics.md", e.Code)
		}
	}

	if want := fmt.Sprintf("lists all %d codes", len(all)); !strings.Contains(doc, want) {
		t.Errorf("docs/reference/diagnostics.md does not say %q", want)
	}
}

func TestFillsTemplatesAndTakesExitCodeFromCatalog(t *testing.T) {
	t.Parallel()

	e := diag.Fail(diag.WRK001, pathParams("x.yaml"))
	if e.ExitCode != diag.InvalidInput {
		t.Errorf("exit code %d, want %d", e.ExitCode, diag.InvalidInput)
	}

	if e.Diagnostic.Message != "workspace file not found: x.yaml" {
		t.Errorf("message %q", e.Diagnostic.Message)
	}

	if e.Diagnostic.Suggestion != "run `concordia init` or pass --workspace <path>" {
		t.Errorf("suggestion %q", e.Diagnostic.Suggestion)
	}

	if got := diag.Fail(diag.INT012, diag.Detail("d")).ExitCode; got != diag.Network {
		t.Errorf("exit code %d, want %d", got, diag.Network)
	}

	if got := e.Error(); got != "CO-WRK-001: workspace file not found: x.yaml" {
		t.Errorf("Error() = %q", got)
	}
}

func TestOverridesForDualUseCodes(t *testing.T) {
	t.Parallel()

	if got := diag.New(diag.WRK005, pathParams("p")).Severity; got != diag.SeverityWarning {
		t.Errorf("severity %q", got)
	}

	e := diag.Fail(diag.WRK005, pathParams("p"), diag.WithSeverity(diag.SeverityError), diag.WithExitCode(diag.InvalidInput))
	if e.ExitCode != 2 {
		t.Errorf("exit code %d, want 2", e.ExitCode)
	}

	d := diag.New(diag.EXC006, diag.Params{"id": "WV-1", "expires": "2026-01-01"}, diag.WithSeverity(diag.SeverityError))
	if d.Severity != diag.SeverityError {
		t.Errorf("severity %q", d.Severity)
	}
}

func TestPlaceholderHandling(t *testing.T) {
	t.Parallel()

	d := diag.New(diag.CON021, diag.Params{"ratio": 0.5, "required": 0.95})
	if d.Message != "minimum coverage not met: 0.5 < 0.95" {
		t.Errorf("message %q", d.Message)
	}

	d = diag.New(diag.WRK001, nil)
	if d.Message != "workspace file not found: {path}" {
		t.Errorf("a missing parameter must keep its placeholder: %q", d.Message)
	}

	d = diag.New(diag.POL009, diag.Params{"kind": "k", "ruleId": 7})
	if !strings.Contains(d.Message, "(rule 7)") {
		t.Errorf("message %q", d.Message)
	}
}

func TestFailWithNoCatalogExitCodeGivesInternal(t *testing.T) {
	t.Parallel()

	if got := diag.Fail(diag.WRK003, diag.Detail("d")).ExitCode; got != diag.Internal {
		t.Errorf("exit code %d, want %d", got, diag.Internal)
	}

	if _, ok := diag.ExitCodeOf(diag.WRK003); ok {
		t.Error("CO-WRK-003 must have no exit code")
	}

	if code, ok := diag.ExitCodeOf(diag.OBS003); !ok || code != diag.Toolchain {
		t.Errorf("CO-OBS-003: %d, %v", code, ok)
	}
}

func TestUnknownCodePanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("expected a panic")
		}
	}()

	diag.New("CO-XXX-000", nil)
}

func TestErrorsAs(t *testing.T) {
	t.Parallel()

	var err error = diag.Fail(diag.POL001, diag.Detail("d"))

	var target *diag.Error
	if !errors.As(err, &target) || target.Diagnostic.Code != "CO-POL-001" {
		t.Fatalf("errors.As failed: %v", err)
	}
}

func TestSortOrder(t *testing.T) {
	t.Parallel()

	in := []diag.Diagnostic{
		diag.New(diag.WRK002, diag.Detail("b"), diag.WithSource(diag.Source{URI: "b", Line: 2})),
		diag.New(diag.WRK002, diag.Detail("a"), diag.WithSource(diag.Source{URI: "b", Line: 10})),
		diag.New(diag.WRK001, pathParams("p")),
		diag.New(diag.WRK002, diag.Detail("z"), diag.WithSource(diag.Source{URI: "a"})),
	}
	out := diag.Sort(in)

	got := make([]string, 0, len(out))
	for _, d := range out {
		got = append(got, d.Message)
	}

	want := "workspace file not found: p|workspace not valid: z|workspace not valid: b|workspace not valid: a"
	if strings.Join(got, "|") != want {
		t.Errorf("order %q, want %q", strings.Join(got, "|"), want)
	}

	if in[0].Message != "workspace not valid: b" || in[2].Code != diag.WRK001 {
		t.Error("input was changed")
	}
}

func TestFormatDiagnostic(t *testing.T) {
	t.Parallel()

	d := diag.New(diag.WRK001, pathParams("x.yaml"), diag.WithSource(diag.Source{URI: "f.yaml", Line: 3, Pointer: "/a"}),
		diag.WithCause([]diag.Diagnostic{diag.New(diag.WRK010, diag.Detail("c"))}))
	want := "error CO-WRK-001: workspace file not found: x.yaml [f.yaml:3#/a]\n" +
		"  hint: run `concordia init` or pass --workspace <path>\n" +
		"  error CO-WRK-010: usage error: c"

	if got := diag.FormatDiagnostic(d); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// catalogRow is the expected severity and exit code of one catalog code.
type catalogRow struct {
	code     string
	severity diag.Severity
	exit     int
}

// hasShape reports whether the entry has the severity and the exit code.
func hasShape(entry diag.Entry, severity diag.Severity, exit int) bool {
	return entry.Severity == severity && entry.ExitCode == exit
}

// checkCatalogRow checks that the catalog has the code of the row with its severity and exit code,
// and that ExitCodeOf agrees with the exit code.
func checkCatalogRow(t *testing.T, row catalogRow) {
	t.Helper()

	entry, ok := diag.Lookup(row.code)
	if !ok {
		t.Errorf("%s is not in the catalog", row.code)

		return
	}

	if !hasShape(entry, row.severity, row.exit) {
		t.Errorf("%s = severity %s exit %d, want %s exit %d", row.code, entry.Severity, entry.ExitCode, row.severity, row.exit)
	}

	if _, has := diag.ExitCodeOf(row.code); has != (row.exit != diag.NoExit) {
		t.Errorf("%s: ExitCodeOf presence = %v, want %v", row.code, has, row.exit != diag.NoExit)
	}
}

// Scenario: Invalid declaration.
// Scenario: Non-blocking diagnostics.
func TestADRCatalogSeveritiesAndExitCodes(t *testing.T) {
	t.Parallel()

	invalid := []string{diag.ADR001, diag.ADR002, diag.ADR003, diag.ADR004, diag.ADR005}
	for _, code := range invalid {
		checkInvalidADR(t, code)
	}

	if entry, _ := diag.Lookup(diag.ADR006); !hasShape(entry, diag.SeverityWarning, diag.NoExit) {
		t.Errorf("CO-ADR-006 = %+v, want a warning with no exit code", entry)
	}

	for _, code := range []string{diag.ADR007, diag.ADR008, diag.ADR009} {
		checkInformationalADR(t, code)
	}
}

// checkInvalidADR checks that the code is an ADR error that exits with the invalid-input code.
func checkInvalidADR(t *testing.T, code string) {
	t.Helper()

	entry, ok := diag.Lookup(code)
	if !ok || !strings.HasPrefix(code, "CO-ADR-") || !hasShape(entry, diag.SeverityError, diag.InvalidInput) {
		t.Errorf("%s = %+v, want an error with exit code %d", code, entry, diag.InvalidInput)
	}
}

// checkInformationalADR checks that the code is informational and has no exit code.
func checkInformationalADR(t *testing.T, code string) {
	t.Helper()

	entry, ok := diag.Lookup(code)
	if !ok || !hasShape(entry, diag.SeverityInfo, diag.NoExit) {
		t.Errorf("%s = %+v, want informational with no exit code", code, entry)
	}

	if _, has := diag.ExitCodeOf(code); has {
		t.Errorf("%s has an exit code", code)
	}
}

// Scenario: Review-currency diagnostics have fixed numbers, severities, and exit codes (design D17).
func TestReviewCurrencyCatalogMatchesDesignTable(t *testing.T) {
	t.Parallel()

	table := []catalogRow{
		{diag.ASM001, diag.SeverityError, diag.InvalidInput},
		{diag.ASM002, diag.SeverityError, diag.InvalidInput},
		{diag.ASM003, diag.SeverityError, diag.InvalidInput},
		{diag.ASM004, diag.SeverityError, diag.InvalidInput},
		{diag.ASM005, diag.SeverityError, diag.InvalidInput},
		{diag.ASM006, diag.SeverityWarning, diag.NoExit},
		{diag.ASM007, diag.SeverityWarning, diag.NoExit},
		{diag.ASM008, diag.SeverityWarning, diag.NoExit},
		{diag.ASM009, diag.SeverityInfo, diag.NoExit},
		{diag.ASM010, diag.SeverityInfo, diag.NoExit},
		{diag.ASM011, diag.SeverityWarning, diag.NoExit},
		{diag.SEC001, diag.SeverityError, diag.InvalidInput},
		{diag.SEC002, diag.SeverityWarning, diag.NoExit},
		{diag.EXC009, diag.SeverityWarning, diag.NoExit},
		{diag.EXC010, diag.SeverityWarning, diag.NoExit},
		{diag.EXC011, diag.SeverityWarning, diag.NoExit},
	}

	wantNumbers := map[string]string{
		diag.ASM001: "CO-ASM-001", diag.ASM011: "CO-ASM-011", diag.SEC001: "CO-SEC-001", diag.SEC002: "CO-SEC-002",
		diag.EXC009: "CO-EXC-009", diag.EXC010: "CO-EXC-010", diag.EXC011: "CO-EXC-011",
	}

	for code, want := range wantNumbers {
		if code != want {
			t.Errorf("constant is %q, want %q", code, want)
		}
	}

	for _, row := range table {
		checkCatalogRow(t, row)
	}
}

// Scenario: CO-EXC-005 stays reserved (design D21).
func TestExc005IsReservedAndDocumented(t *testing.T) {
	t.Parallel()

	if _, ok := diag.Lookup("CO-EXC-005"); ok {
		t.Error("CO-EXC-005 is reserved and must not be in the catalog")
	}

	raw, err := os.ReadFile("../../docs/reference/diagnostics.md")
	if err != nil {
		t.Fatal(err)
	}

	if doc := string(raw); !strings.Contains(doc, "`CO-EXC-005`") || !strings.Contains(doc, "reserved") {
		t.Error("docs/reference/diagnostics.md does not document CO-EXC-005 as reserved")
	}
}

// Scenario: Assurance diagnostics have fixed numbers, severities, and exit codes (design D14, INT-01, INT-02).
func TestAssuranceCatalogMatchesDesignTable(t *testing.T) {
	t.Parallel()

	table := []catalogRow{
		{diag.PRV001, diag.SeverityError, diag.InvalidInput},
		{diag.PRV002, diag.SeverityError, diag.InvalidInput},
		{diag.PRV003, diag.SeverityError, diag.Toolchain},
		{diag.PRV004, diag.SeverityError, diag.Toolchain},
		{diag.PRV005, diag.SeverityWarning, diag.NoExit},
		{diag.PRV006, diag.SeverityWarning, diag.NoExit},
		{diag.PRV007, diag.SeverityWarning, diag.NoExit},
		{diag.PRV008, diag.SeverityWarning, diag.NoExit},
		{diag.PRV009, diag.SeverityWarning, diag.NoExit},
		{diag.PRV010, diag.SeverityInfo, diag.NoExit},
		{diag.PRV011, diag.SeverityWarning, diag.NoExit},
		{diag.PRV012, diag.SeverityWarning, diag.NoExit},
		{diag.PRV013, diag.SeverityWarning, diag.NoExit},
		{diag.PRV014, diag.SeverityWarning, diag.NoExit},
		{diag.QUA001, diag.SeverityError, diag.InvalidInput},
		{diag.QUA002, diag.SeverityError, diag.InvalidInput},
		{diag.QUA003, diag.SeverityWarning, diag.NoExit},
	}

	for i, row := range table {
		const prvCodes = 14

		prefix, number := "CO-PRV-", i+1
		if i >= prvCodes {
			prefix, number = "CO-QUA-", i-prvCodes+1
		}

		if want := fmt.Sprintf("%s%03d", prefix, number); row.code != want {
			t.Errorf("constant is %q, want %q: numbers have no gaps and are never reused", row.code, want)
		}

		checkCatalogRow(t, row)
	}

	// No other code of either prefix exists.
	if count := countCodesWithPrefix("CO-PRV-", "CO-QUA-"); count != len(table) {
		t.Errorf("the catalog has %d CO-PRV and CO-QUA codes, want %d", count, len(table))
	}
}

// countCodesWithPrefix counts the catalog codes that start with one of the prefixes.
func countCodesWithPrefix(prefixes ...string) int {
	count := 0

	for _, entry := range diag.All() {
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry.Code, prefix) {
				count++

				break
			}
		}
	}

	return count
}

// Scenario: Every exit code of the catalog is one of the contract set (0, 1, 2, 3, 4, 5, 6, 7, 130), a code that never
// stops a run has none, and an error that has no exit code does not exist: a catalog error always names how it exits.
func TestEveryCatalogExitCodeIsInTheContractSet(t *testing.T) {
	t.Parallel()

	allowed := map[int]bool{
		diag.OK: true, diag.Violation: true, diag.InvalidInput: true, diag.AdapterFailure: true, diag.Unknown: true,
		diag.Toolchain: true, diag.Network: true, diag.Internal: true, diag.Interrupted: true, diag.NoExit: true,
	}

	for _, entry := range diag.All() {
		if !allowed[entry.ExitCode] {
			t.Errorf("%s has the exit code %d, which is not in 0-7, 130 or none", entry.Code, entry.ExitCode)
		}

		if entry.Severity == diag.SeverityInfo && entry.ExitCode != diag.NoExit {
			t.Errorf("%s is informational and has the exit code %d", entry.Code, entry.ExitCode)
		}
	}
}
