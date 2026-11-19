package reporting_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

var plain = reporting.PrettyOptions{}

func contains(t *testing.T, text, want string) {
	t.Helper()

	if !strings.Contains(text, want) {
		t.Errorf("missing %q in:\n%s", want, text)
	}
}

func notContains(t *testing.T, text, unwanted string) {
	t.Helper()

	if strings.Contains(text, unwanted) {
		t.Errorf("unexpected %q in:\n%s", unwanted, text)
	}
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")

	return lines[len(lines)-1]
}

func equal[T comparable](t *testing.T, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// inOrder says whether every position is found and each one is after the one before it.
func inOrder(positions ...int) bool {
	previous := -1

	for _, position := range positions {
		if position <= previous {
			return false
		}

		previous = position
	}

	return true
}

// renderJSON renders the run as JSON, and fails the test when it cannot.
func renderJSON(t *testing.T, run *conformance.Run) []byte {
	t.Helper()

	out, err := reporting.Render(reporting.FormatJSON, run, plain)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// decodeJSON decodes JSON into a value, and fails the test when it is not valid.
func decodeJSON(t *testing.T, data []byte, target any) {
	t.Helper()

	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func ruleWith(verdict conformance.RuleVerdict, onUnknown policy.UnknownDisposition) conformance.RuleResult {
	p := testutil.SampleRun().Rules[0].Plan
	p.EvidenceRequirements.OnUnknown = onUnknown

	return conformance.RuleResult{
		RuleID: "R", Name: "r", Severity: policy.SeverityError, Policy: policy.PackRef{ID: "p", Version: "1"}, Family: policy.FamilyBoundary,
		Verdict: verdict, Reasons: []conformance.Reason{}, Subjects: []string{}, Targets: []string{}, Plan: p,
	}
}

func findingWith(d exceptions.Disposition, s policy.RuleSeverity) []conformance.Finding {
	f := testutil.SampleRun().Findings[0]
	f.Disposition = d
	f.Severity = s

	return []conformance.Finding{f}
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	r := func(v conformance.RuleVerdict) conformance.RuleResult { return ruleWith(v, policy.UnknownFail) }
	active, errSev := exceptions.DispositionActive, policy.SeverityError

	t.Run("an error verdict wins over everything (7)", func(t *testing.T) {
		t.Parallel()
		equal(t, conformance.ComputeExitCode([]conformance.RuleResult{r(conformance.VerdictError), r(conformance.VerdictViolation), r(conformance.VerdictUnknown)}, findingWith(active, errSev), diag.SeverityError), 7)
	})

	t.Run("an active or expired-waiver finding gives 1, before unknown (4)", func(t *testing.T) {
		t.Parallel()

		unknown := []conformance.RuleResult{r(conformance.VerdictUnknown)}
		equal(t, conformance.ComputeExitCode(unknown, findingWith(active, errSev), diag.SeverityError), 1)
		equal(t, conformance.ComputeExitCode(unknown, findingWith(exceptions.DispositionExpiredWaiver, errSev), diag.SeverityError), 1)
	})

	t.Run("baselined and waived findings do not fail", func(t *testing.T) {
		t.Parallel()

		both := append(findingWith(exceptions.DispositionBaselined, errSev), findingWith(exceptions.DispositionWaived, errSev)...)
		equal(t, conformance.ComputeExitCode([]conformance.RuleResult{r(conformance.VerdictViolation)}, both, diag.SeverityError), 0)
	})

	t.Run("a finding below --fail-on does not fail; at the threshold it does", func(t *testing.T) {
		t.Parallel()

		v := []conformance.RuleResult{r(conformance.VerdictViolation)}
		equal(t, conformance.ComputeExitCode(v, findingWith(active, policy.SeverityWarning), diag.SeverityError), 0)
		equal(t, conformance.ComputeExitCode(v, findingWith(active, policy.SeverityWarning), diag.SeverityWarning), 1)
		equal(t, conformance.ComputeExitCode(v, findingWith(active, policy.SeverityInfo), diag.SeverityInfo), 1)
	})

	t.Run("unknown fails (4) only when the effective policy is fail", func(t *testing.T) {
		t.Parallel()
		equal(t, conformance.ComputeExitCode([]conformance.RuleResult{ruleWith(conformance.VerdictUnknown, policy.UnknownFail)}, nil, diag.SeverityError), 4)
		equal(t, conformance.ComputeExitCode([]conformance.RuleResult{ruleWith(conformance.VerdictUnknown, policy.UnknownWarn)}, nil, diag.SeverityError), 0)
		equal(t, conformance.ComputeExitCode([]conformance.RuleResult{ruleWith(conformance.VerdictUnknown, policy.UnknownIgnore)}, nil, diag.SeverityError), 0)
	})

	t.Run("not-applicable and pass give 0", func(t *testing.T) {
		t.Parallel()
		equal(t, conformance.ComputeExitCode([]conformance.RuleResult{r(conformance.VerdictPass), r(conformance.VerdictNotApplicable)}, nil, diag.SeverityError), 0)
	})

	t.Run("summary verdict is the worst rule verdict; no rules is not-applicable", func(t *testing.T) {
		t.Parallel()

		vs := func(list ...conformance.RuleVerdict) conformance.RuleVerdict { return conformance.WorstVerdict(list) }
		equal(t, vs(conformance.VerdictPass, conformance.VerdictUnknown, conformance.VerdictNotApplicable), conformance.VerdictUnknown)
		equal(t, vs(conformance.VerdictPass, conformance.VerdictViolation, conformance.VerdictUnknown), conformance.VerdictViolation)
		equal(t, vs(conformance.VerdictPass, conformance.VerdictError), conformance.VerdictError)
		equal(t, vs(), conformance.VerdictNotApplicable)
		equal(t, conformance.Summarize(nil, nil, diag.SeverityError, policy.UnknownFail).Verdict, conformance.VerdictNotApplicable)
	})

	t.Run("summary counts findings by disposition and rules by verdict", func(t *testing.T) {
		t.Parallel()

		s := testutil.SampleRun().Summary
		equal(t, s.Verdict, conformance.VerdictError)
		equal(t, s.ExitCode, 7)
		equal(t, s.Active, 1)
		equal(t, s.Baselined, 1)
		equal(t, s.Waived, 1)
		equal(t, s.ExpiredWaiver, 1)
		equal(t, s.UnknownRules, 2)
		equal(t, s.Rules, conformance.RuleCounts{Pass: 1, Violation: 3, Unknown: 2, NotApplicable: 1, Error: 1})
	})
}

func TestJSONAndDispatch(t *testing.T) {
	t.Parallel()

	t.Run("json is the canonical JSON of the run", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		got := renderJSON(t, run)

		want, err := canonjson.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}

		equal(t, string(got), string(want))
	})

	t.Run("an unknown format gives CO-WRK-010", func(t *testing.T) {
		t.Parallel()

		_, err := reporting.Render("yaml", testutil.SampleRun(), plain)
		if err == nil || !strings.Contains(err.Error(), "CO-WRK-010") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("every format renders", func(t *testing.T) {
		t.Parallel()

		for _, f := range reporting.Formats {
			if _, err := reporting.Render(f, testutil.SampleRun(), plain); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
	})
}

// evilRun is the sample run with terminal control sequences in a node name, a message, a rule
// name, and a reason: repository content and provider output can hold them.
func evilRun() *conformance.Run {
	run := testutil.SampleRun()

	for i := range run.Findings {
		f := &run.Findings[i]
		f.Message = "bad\x1b[2Jmessage"

		for j := range f.EvidencePath {
			if node := f.EvidencePath[j].Node; node != nil {
				node.QualifiedName = "evil\x1b[31mname\x1b]0;title\x07"
			}
		}
	}

	run.Rules[0].Name += " \x1b[1;31mfake"
	run.Rules[1].Reasons[0].Message = "reason\x1b[0m\nFAIL CO-FAKE-001 forged line"

	return run
}

func TestControlSequencesFromContentAreNeutralized(t *testing.T) {
	t.Parallel()

	const (
		escName = `evil\x1b[31mname\x1b]0;title\x07`
		raw     = "\x1b[31mname"
	)

	t.Run("pretty output shows a visible escaped form", func(t *testing.T) {
		t.Parallel()

		for _, opts := range []reporting.PrettyOptions{plain, {Color: true, Verbose: true}} {
			text := reporting.RenderPretty(evilRun(), opts)
			contains(t, text, escName)
			contains(t, text, `fake`)
			contains(t, text, `reason\x1b[0m\nFAIL CO-FAKE-001 forged line`)
			notContains(t, text, raw)
			notContains(t, text, "\x07")
			notContains(t, text, "\nFAIL CO-FAKE-001")

			// The only escape characters are our own color codes.
			stripped := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(text, "")
			notContains(t, stripped, "\x1b")
		}
	})

	t.Run("SARIF keeps valid JSON with the escaped text", func(t *testing.T) {
		t.Parallel()

		out, doc := renderSarif(t, evilRun())
		assertValid(t, sarifValidator(t), out)
		notContains(t, string(out), `\u001b`)
		notContains(t, string(out), `\u0007`)
		contains(t, string(out), `evil\\x1b[31mname`)

		contains(t, string(out), `bad\\x1b[2Jmessage`)
		equal(t, len(doc.Runs[0].Results), 4)
	})

	t.Run("JUnit shows the escaped text and no control character", func(t *testing.T) {
		t.Parallel()

		xml := reporting.RenderJUnit(evilRun())
		contains(t, xml, `bad\x1b[2Jmessage`)
		contains(t, xml, `fake`)

		for _, r := range xml {
			if r < ' ' && !strings.ContainsRune("\n\t", r) {
				t.Fatalf("control character %q in the XML", r)
			}
		}
	})

	t.Run("JSON keeps the text as an escaped string", func(t *testing.T) {
		t.Parallel()

		out := renderJSON(t, evilRun())
		if !json.Valid(out) {
			t.Fatal("invalid JSON")
		}

		contains(t, string(out), `\u001b`)
	})
}
