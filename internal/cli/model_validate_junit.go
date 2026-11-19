package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// checkCategory is one JUnit test case of `model validate`.
type checkCategory struct {
	name  string
	codes []string
}

// checkCategories has one JUnit test case for each check category.
var checkCategories = []checkCategory{
	{"document-readable", []string{diag.INT001}},
	{"schema-version", []string{diag.INT003}},
	{"calm-schema", []string{diag.INT002}},
	{"unique-ids", []string{diag.INT004}},
	{"relationship-endpoints", []string{diag.INT005}},
	{"patterns", []string{diag.INT006}},
	{"decorators", []string{diag.INT008, diag.INT009, diag.INT010}},
	{"calm-rules", []string{diag.INT014, diag.INT015}},
	{"concordia-extensions", []string{diag.INT007}},
	{"remote-references", []string{diag.INT012}},
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

// categoryErrors gives the error diagnostics of one check category.
func categoryErrors(category *checkCategory, diagnostics []diag.Diagnostic) []diag.Diagnostic {
	var errs []diag.Diagnostic

	for i := range diagnostics {
		if diagnostics[i].Severity == diag.SeverityError && slices.Contains(category.codes, diagnostics[i].Code) {
			errs = append(errs, diagnostics[i])
		}
	}

	return errs
}

func renderModelValidateJUnit(diagnostics []diag.Diagnostic) string {
	errsByCategory := make([][]diag.Diagnostic, len(checkCategories))
	failures := 0

	for i := range checkCategories {
		errsByCategory[i] = categoryErrors(&checkCategories[i], diagnostics)
		if len(errsByCategory[i]) > 0 {
			failures++
		}
	}

	var out strings.Builder

	out.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	out.WriteString("<testsuites name=\"concordia\">\n")
	fmt.Fprintf(&out, "  <testsuite name=\"model-validate\" tests=\"%d\" failures=\"%d\">\n", len(checkCategories), failures)

	for i := range checkCategories {
		writeJUnitTestCase(&out, checkCategories[i].name, errsByCategory[i])
	}

	out.WriteString("  </testsuite>\n</testsuites>\n")

	return out.String()
}

// writeJUnitTestCase writes the test case of one check category, with a failure when it has errors.
func writeJUnitTestCase(out *strings.Builder, name string, errs []diag.Diagnostic) {
	fmt.Fprintf(out, "    <testcase classname=\"model-validate\" name=\"%s\">\n", xmlEscaper.Replace(name))

	if len(errs) > 0 {
		texts := make([]string, len(errs))
		for i := range errs {
			texts[i] = diag.FormatDiagnostic(errs[i])
		}

		fmt.Fprintf(out, "      <failure type=\"%s\" message=\"%d error(s)\">%s</failure>\n",
			xmlEscaper.Replace(errs[0].Code), len(errs), xmlEscaper.Replace(strings.Join(texts, "\n")))
	}

	out.WriteString("    </testcase>\n")
}
