package reporting_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

// junitSection is the part of a JUnit document from the test case that has a name with the marker to the end of it.
func junitSection(text, marker string) string {
	start := strings.Index(text, `name="`+marker)
	end := strings.Index(text[start:], "</testcase>")

	return text[start : start+end]
}

// renameBoundaryRule gives the boundary rule of the run a new name.
func renameBoundaryRule(run *conformance.Run, name string) {
	for i := range run.Rules {
		if run.Rules[i].RuleID == "CO-BND-001" {
			run.Rules[i].Name = name
		}
	}
}

// activateLayerFindings makes the findings of the layer rule active, with no disposition reference.
func activateLayerFindings(run *conformance.Run) {
	for i := range run.Findings {
		if run.Findings[i].RuleID == "CO-LYR-001" {
			run.Findings[i].Disposition = exceptions.DispositionActive
			run.Findings[i].DispositionRef = nil
		}
	}
}

func TestJUnit(t *testing.T) {
	t.Parallel()

	xml := reporting.RenderJUnit(testutil.SampleRun())

	t.Run("has one testsuite for each pack and one testcase for each rule", func(t *testing.T) {
		t.Parallel()
		contains(t, xml, `<testsuites name="concordia" tests="8" failures="3" errors="1" skipped="1">`)
		contains(t, xml, `<testsuite name="commerce-boundaries@1.0.0" tests="7" failures="3" errors="1" skipped="1">`)
		contains(t, xml, `<testsuite name="other-pack@2.0.0" tests="1" failures="0" errors="0" skipped="0">`)
		equal(t, strings.Count(xml, "<testcase "), 8)
		contains(t, xml, `<testcase classname="commerce-boundaries" name="CO-BND-001 Billing uses Fulfillment contracts only">`)
	})

	t.Run("maps verdicts: violation, unknown(fail), expired waiver, error, skipped", func(t *testing.T) {
		t.Parallel()
		contains(t, xml, `<failure type="violation" message="1 active finding(s)">`)
		contains(t, xml, testutil.FPActive)
		contains(t, xml, `<failure type="unknown" message="CO-CON-021: minimum coverage not met: 0.500 &lt; 0.9">`)
		contains(t, xml, `<error type="error" message="CO-CON-099: rule evaluation error: boom">`)
		contains(t, xml, `<skipped message="CO-CON-022: rule selects no subject"/>`)
	})

	t.Run("a rule whose findings are all baselined or waived is not a test failure", func(t *testing.T) {
		t.Parallel()

		lyr := junitSection(xml, "CO-LYR-001")
		notContains(t, lyr, "<failure")
		contains(t, lyr, "waived:")
	})

	t.Run("an unknown rule with onUnknown=warn is reported in system-out, not as a failure", func(t *testing.T) {
		t.Parallel()

		warn := junitSection(xml, "CO-WARN-001")
		contains(t, warn, "<system-out>")
		notContains(t, warn, "<failure")
	})

	t.Run("is well-formed: escapes XML special characters", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		renameBoundaryRule(run, `a "quoted" <tag> & more`)

		out := reporting.RenderJUnit(run)
		contains(t, out, `name="CO-BND-001 a &quot;quoted&quot; &lt;tag&gt; &amp; more"`)
		notContains(t, out, "<tag>")
	})

	t.Run("counts follow failOn: a warning-level live finding is not a failure at failOn=error", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		activateLayerFindings(run)

		contains(t, reporting.RenderJUnit(run), `<testsuites name="concordia" tests="8" failures="3"`)

		run.Summary.FailOn = diag.SeverityWarning
		contains(t, reporting.RenderJUnit(run), `<testsuites name="concordia" tests="8" failures="4"`)
	})
}
