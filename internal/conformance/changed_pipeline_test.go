package conformance_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

func changedSetup(t *testing.T, files []string) *setup {
	t.Helper()

	s := newSetup(t, nil, &setupOpts{graph: buildGraph(graphOpts{refund: true})})
	s.deps.CreateGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{
			Rev:     "abc123",
			Files:   []string{billingFile, storeFile, legacyFile, refundFile},
			Changed: workspace.ChangeSet{Ref: "HEAD", Base: "abc123abc123abc123", Files: files},
		}
	}

	return s
}

func hasCode(run *conformance.Run, code string) bool {
	return slices.ContainsFunc(run.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == code })
}

func TestRunCheckChangedReducesEdgeFindings(t *testing.T) {
	t.Parallel()

	run := changedSetup(t, []string{refundFile}).mustCheck(t, func(o *conformance.CheckOptions) {
		o.Rules = []string{"CO-BND-001"}
		o.Changed = "HEAD"
	})

	if len(run.Findings) != 1 || run.Findings[0].Location.Path != refundFile {
		t.Fatalf("findings %+v", run.Findings)
	}

	r := run.Rules[0]
	if r.ChangedScope != "reduced" || r.FindingCount != 1 || r.Verdict != conformance.VerdictViolation {
		t.Errorf("rule %+v", r)
	}

	if run.Run.Changed == nil || run.Run.Changed.Ref != "HEAD" || !slices.Equal(run.Run.Changed.Files, []string{refundFile}) {
		t.Errorf("changed %+v", run.Run.Changed)
	}
}

func TestRunCheckChangedEmptyScopePasses(t *testing.T) {
	t.Parallel()

	run := changedSetup(t, nil).mustCheck(t, func(o *conformance.CheckOptions) {
		o.Rules = []string{"CO-BND-001"}
		o.Changed = "HEAD"
	})

	if len(run.Findings) != 0 || run.Rules[0].Verdict != conformance.VerdictPass {
		t.Errorf("findings %d verdict %s", len(run.Findings), run.Rules[0].Verdict)
	}

	if !hasCode(run, "CO-CON-029") {
		t.Errorf("diagnostics %+v", run.Diagnostics)
	}
}

func TestRunCheckChangedErrorFromGit(t *testing.T) {
	t.Parallel()

	s := changedSetup(t, nil)
	s.deps.CreateGit = func(string) workspace.GitPort {
		return &workspace.FakeGit{Rev: "abc123", ChangedErr: diag.Fail(diag.WRK015, diag.Detail("unknown git reference 'nope'"))}
	}

	e := s.failureOf(t, func(o *conformance.CheckOptions) { o.Changed = "nope" })
	if e.Diagnostic.Code != "CO-WRK-015" {
		t.Fatalf("code %s", e.Diagnostic.Code)
	}
}
