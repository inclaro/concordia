package assessment_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
)

const roleAssessment = `apiVersion: concordia.io/v1alpha1
kind: Assessment
id: ASM-ROLE
version: 1
type: security
title: Role bindings
scope:
  calmNodes: [api-gateway]
environment: production
subject:
  summary: Role bindings of the gateway
  dependencies:
    - kind: role-binding
      selector:
        calmNodes: [api-gateway]
assumptions: []
evidence: []
outcome: satisfactory
state: ready-for-review
`

// twoAssessmentFiles is the edge tree with a second assessment that depends on another kind.
func twoAssessmentFiles() map[string]string {
	files := edgeFiles()
	files["assessments/ASM-ROLE/v1.yaml"] = roleAssessment

	return files
}

func roleBinding(identity string) impact.Item {
	return impact.V1().NewItem(impact.KindRoleBinding, identity, impact.Facets{"roleReference": {"admin"}}, nil, nil)
}

// incrementalInput is the input of the two-assessment tree with the cache at dir. The approval binds the edge subject.
func incrementalInput(t *testing.T, dir string) *assessment.EvalInput {
	t.Helper()

	facts := newFacts(entryPoint("k8s:ingress/api", "api.example.com"))
	facts.matches[impact.KindRoleBinding] = found(roleBinding("k8s:rolebinding/a"))

	in := edgeInputOf(t, facts, twoAssessmentFiles())
	approve(t, in, "ASM-EDGE-v1-0001", atDecision)

	in.PolicyDigest = digestA
	in.AuthorityDigest = digestB
	in.Incremental = &assessment.Incremental{Dir: dir, AnchorRevision: "abc123"}

	return in
}

// semantic is the canonical JSON of an evaluation without what differs between the modes by design: the cache
// report and the note that a cache was not usable.
func semantic(t *testing.T, ev *assessment.Evaluation) string {
	t.Helper()

	data, err := canonjson.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}

	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}

	delete(tree, "incremental")

	incrementalDropCacheNote(tree)

	text, err := canonjson.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

func full(t *testing.T, in *assessment.EvalInput) string {
	t.Helper()

	plain := *in
	plain.Incremental = nil

	return semantic(t, evaluate(t, &plain))
}

// firstDifference names the first line at which two canonical texts differ, "" when they are equal.
func firstDifference(got, want string) string {
	if got == want {
		return ""
	}

	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range min(len(gl), len(wl)) {
		if gl[i] != wl[i] {
			return fmt.Sprintf("line %d: incremental %q, full %q", i+1, gl[i], wl[i])
		}
	}

	return fmt.Sprintf("lengths differ: %d and %d lines", len(gl), len(wl))
}

func hasCacheNote(ev *assessment.Evaluation) bool {
	return slices.ContainsFunc(ev.Diagnostics, func(d diag.Diagnostic) bool { return d.Code == diag.ASM010 })
}

// Scenario: A32 two modes equal.
func TestIncrementalEqualsFullInEveryState(t *testing.T) {
	t.Parallel()

	changed := func(in *assessment.EvalInput) {
		factsOf(t, in).matches[impact.KindPublicEntryPoint] = found(
			entryPoint("k8s:ingress/api", "api.example.com"), entryPoint("k8s:ingress/new", "new.example.com"))
	}

	removed := func(in *assessment.EvalInput) {
		factsOf(t, in).matches[impact.KindPublicEntryPoint] = found()
	}

	cases := map[string]func(in *assessment.EvalInput){
		"unchanged":    func(*assessment.EvalInput) {},
		"added item":   changed,
		"removed item": removed,
		"modified item": func(in *assessment.EvalInput) {
			factsOf(t, in).matches[impact.KindPublicEntryPoint] = found(entryPoint("k8s:ingress/api", "other.example.com"))
		},
		"expired": func(in *assessment.EvalInput) {
			in.Set.Approvals[0].Expiry = &assessment.Expiry{ExpiresAt: atEval.Add(-time.Hour)}
		},
		"revoked": func(in *assessment.EvalInput) {
			ap := in.Set.Approvals[0]
			in.Set.Revocations = append(in.Set.Revocations, &assessment.Revocation{
				ID: "REV-1", Approval: assessment.Ref{ID: ap.ID, Digest: ap.Digest}, DecidedAt: atDecision.Add(time.Hour),
				Reviewer: assessment.Reviewer{Identity: "alice@example.com", Authority: "security-review"},
			})
		},
		"limit stop": func(in *assessment.EvalInput) {
			changed(in)

			in.Limits = impact.Limits{MaxVisited: 1}
		},
		"policy changed": func(in *assessment.EvalInput) { in.PolicyDigest = digestB },
		"no fact source": func(in *assessment.EvalInput) { in.Facts = nil },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			// The cache of the first run is the one that the changed run reads.
			warm := incrementalInput(t, dir)
			if _, err := assessment.Evaluate(t.Context(), warm); err != nil {
				t.Fatal(err)
			}

			in := incrementalInput(t, dir)
			mutate(in)

			want := full(t, in)

			// Twice: the second run reads the file that the first wrote (cold, then warm).
			for _, run := range []string{"first", "second"} {
				if d := firstDifference(semantic(t, evaluate(t, in)), want); d != "" {
					t.Fatalf("%s incremental run differs from the full run: %s", run, d)
				}
			}
		})
	}
}

// Scenario: Item digest unchanged gives a hit.
func TestAnUnchangedItemGivesAHit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	in := incrementalInput(t, dir)
	cold := evaluate(t, in)

	incrementalWantRun(t, cold.Incremental, incrementalRun{impact.CacheMissing, 0, 2})

	if !hasCacheNote(cold) {
		t.Errorf("a first run without a cache file has no CO-ASM-010: %+v", cold.Diagnostics)
	}

	warm := evaluate(t, in)
	incrementalWantRun(t, warm.Incremental, incrementalRun{impact.CacheLoaded, 2, 0})

	if warm.Incremental.ChangedItems != 0 {
		t.Fatalf("warm run = %+v", warm.Incremental)
	}

	if warm.Incremental.Fallback != "" || hasCacheNote(warm) {
		t.Errorf("a usable cache reports a fallback: %+v", warm.Incremental)
	}

	if d := firstDifference(semantic(t, warm), semantic(t, cold)); d != "" {
		t.Errorf("the warm run differs from the cold run: %s", d)
	}
}

// Scenario: Item digest changed misses only the assessments that depend on the item.
func TestAChangedItemMissesOnlyItsAssessments(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	evaluate(t, incrementalInput(t, dir))

	in := incrementalInput(t, dir)
	factsOf(t, in).matches[impact.KindPublicEntryPoint] = found(entryPoint("k8s:ingress/api", "other.example.com"))

	ev := evaluate(t, in)
	incrementalWantCounts(t, ev.Incremental, 1, 1)

	if ev.Incremental.ChangedItems != 1 {
		t.Fatalf("run = %+v, want one hit (ASM-ROLE), one miss (ASM-EDGE), one changed item", ev.Incremental)
	}

	if edgeResult(t, ev).Currency.State != assessment.CurrencyReviewRequired {
		t.Errorf("ASM-EDGE = %+v", edgeResult(t, ev).Currency)
	}
}

// Scenario: Stale cache.
func TestAChangedKeyMemberIsAMissForEverything(t *testing.T) {
	t.Parallel()

	members := map[string]func(in *assessment.EvalInput){
		"policy digest":    func(in *assessment.EvalInput) { in.PolicyDigest = digestB },
		"authority digest": func(in *assessment.EvalInput) { in.AuthorityDigest = digestA },
		"anchor":           func(in *assessment.EvalInput) { in.Incremental.AnchorRevision = "def456" },
		"evaluator version": func(in *assessment.EvalInput) {
			factsOf(t, in).evaluators = map[string]string{impact.EvaluatorNormalization: "1", "cbm": "0.13.0"}
		},
		"normalization version": func(in *assessment.EvalInput) {
			factsOf(t, in).evaluators = map[string]string{impact.EvaluatorNormalization: "2", "cbm": "0.12.1"}
		},
		"capability profile": func(in *assessment.EvalInput) { factsOf(t, in).profile = "cbm-0.13.0" },
	}

	for name, change := range members {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			evaluate(t, incrementalInput(t, dir))

			in := incrementalInput(t, dir)
			change(in)

			ev := evaluate(t, in)
			incrementalWantRun(t, ev.Incremental, incrementalRun{impact.CacheMissing, 0, 2})

			if !hasCacheNote(ev) {
				t.Errorf("the full evaluation is not reported: %+v", ev.Diagnostics)
			}

			if d := firstDifference(semantic(t, ev), full(t, in)); d != "" {
				t.Errorf("the result differs from the full result: %s", d)
			}
		})
	}
}

// Scenario: Corrupt cache falls back to full.
func TestACorruptCacheFallsBackToFullWithASM010(t *testing.T) {
	t.Parallel()

	for name, hurt := range incrementalDamages() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			incrementalAssertCorruptFallback(t, hurt)
		})
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func truncate(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, path, string(data[:len(data)/2]))
}

// Scenario: Expiry, revocation, and verification always run.
func TestACacheHitStillRunsExpiryRevocationAndVerification(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	in := incrementalInput(t, dir)
	in.Set.Approvals[0].Expiry = &assessment.Expiry{ExpiresAt: atEval.Add(time.Hour)}

	wantState(t, &edgeResult(t, evaluate(t, in)).Currency, assessment.CurrencyCurrent, "")

	// Time passes and nothing else changes: the comparison is a hit, the expiry is judged again.
	in.Now = func() time.Time { return atEval.Add(2 * time.Hour) }

	ev := evaluate(t, in)
	if ev.Incremental.Hits != 2 {
		t.Fatalf("run = %+v, want hits", ev.Incremental)
	}

	wantState(t, &edgeResult(t, ev).Currency, assessment.CurrencyExpired, assessment.DetailExpired)

	// A revocation appears.
	in.Now = func() time.Time { return atEval }
	ap := in.Set.Approvals[0]
	in.Set.Revocations = append(in.Set.Revocations, &assessment.Revocation{
		ID: "REV-1", Approval: assessment.Ref{ID: ap.ID, Digest: ap.Digest}, DecidedAt: atDecision.Add(time.Hour),
		Reviewer: assessment.Reviewer{Identity: "alice@example.com", Authority: "security-review"},
	})

	ev = evaluate(t, in)
	if ev.Incremental.Hits != 2 {
		t.Fatalf("run = %+v, want hits", ev.Incremental)
	}

	wantState(t, &edgeResult(t, ev).Currency, assessment.CurrencyRevoked, assessment.DetailRevoked)

	// The verifier stops applying the approval: the cached comparison cannot make it current.
	in.Set.Revocations = nil
	in.Verifier = verifiedAll{rejects: map[string]bool{ap.ID: true}}

	ev = evaluate(t, in)
	if res := edgeResult(t, ev); res.Currency.State == assessment.CurrencyCurrent || len(res.Currency.NotApplied) != 1 {
		t.Errorf("an approval that does not verify is current: %+v", res.Currency)
	}
}

func TestAnIncompleteTraversalIsNotStored(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	in := incrementalInput(t, dir)
	factsOf(t, in).matches[impact.KindPublicEntryPoint] = found(
		entryPoint("k8s:ingress/api", "api.example.com"), entryPoint("k8s:ingress/new", "new.example.com"))
	in.Limits = impact.Limits{MaxVisited: 1}

	first := evaluate(t, in)
	stopped, complete := 0, 0

	for i := range first.Assessments {
		if first.Assessments[i].Currency.Analysis.Incomplete != nil {
			stopped++
		} else {
			complete++
		}
	}

	if stopped == 0 {
		t.Fatal("no traversal was stopped by the limit")
	}

	// A traversal that a limit stopped depends on the time that the limit measured: it is evaluated again.
	second := evaluate(t, in)
	if second.Incremental.Hits != complete || second.Incremental.Misses != stopped {
		t.Fatalf("run = %+v, want %d hits and %d misses", second.Incremental, complete, stopped)
	}
}

func TestAFullEvaluationWritesNoFile(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "cache")
	in := incrementalInput(t, dir)
	in.Incremental = nil

	ev := evaluate(t, in)
	if ev.Incremental != nil || hasCacheNote(ev) {
		t.Errorf("a full evaluation reports a cache: %+v", ev.Incremental)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a full evaluation made %s: %v", dir, err)
	}
}

func TestAnUnwritableCacheIsANoteNotAFailure(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "file")
	writeFile(t, blocker, "x")

	in := incrementalInput(t, filepath.Join(blocker, "impact"))

	ev := evaluate(t, in)
	if !hasCacheNote(ev) {
		t.Errorf("an unwritable cache is not reported: %+v", ev.Diagnostics)
	}

	if d := firstDifference(semantic(t, ev), full(t, in)); d != "" {
		t.Errorf("the result depends on the cache: %s", d)
	}
}

func TestCountersOfAnEvaluation(t *testing.T) {
	t.Parallel()

	in := incrementalInput(t, t.TempDir())
	ev := evaluate(t, in)

	if ev.Invalidations != 0 || ev.IncompleteAnalyses != 0 {
		t.Fatalf("counters = %d invalidations, %d incomplete", ev.Invalidations, ev.IncompleteAnalyses)
	}

	// A policy that differs from the one the approval recorded invalidates it.
	in.Set.Approvals[0].ProducedUnder.PolicyDigest = digestA
	in.PolicyDigest = digestB

	if ev = evaluate(t, in); ev.Invalidations != 1 {
		t.Errorf("invalidations = %d, want 1", ev.Invalidations)
	}

	in.Facts = nil

	if ev = evaluate(t, in); ev.IncompleteAnalyses == 0 {
		t.Error("an analysis without a fact source is complete")
	}
}

// A selector that becomes unresolved widens the scope, and the widened scope here resolves to the same item. The
// incremental run must report the widened closure exactly as the full run does, never the cached comparison of the
// resolved run (the widened flag is part of the comparison fingerprint).
func TestIncrementalReportsAWidenedScopeLikeTheFullRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	api := entryPoint("k8s:ingress/api", "api.example.com")
	files := edit(t, edgeFiles(), "assessments/ASM-EDGE/v1.yaml", "      selector:\n        calmNodes: [api-gateway]\nassumptions",
		"      selector:\n        calmNodes: [api-gateway]\n        host: [api.example.com]\nassumptions")

	input := func(gap bool) *assessment.EvalInput {
		facts := newFacts(api)
		if gap {
			facts.gap = &struct {
				kind  impact.Kind
				match impact.Match
			}{impact.KindPublicEntryPoint, impact.Match{State: impact.MatchUnknown, Reason: "binding-gap"}}
		}

		in := edgeInputOf(t, facts, files)
		approve(t, in, "ASM-EDGE-v1-0001", atDecision)

		in.PolicyDigest, in.AuthorityDigest = digestA, digestB
		in.Incremental = &assessment.Incremental{Dir: dir, AnchorRevision: "abc123"}

		return in
	}

	// The resolved run fills the cache.
	if res := edgeResult(t, evaluate(t, input(false))); res.Currency.Analysis.Widened {
		t.Fatalf("resolved run = %+v, want no widened closure", res.Currency.Analysis)
	}

	widened := input(true)
	want := full(t, widened)

	if d := firstDifference(semantic(t, evaluate(t, widened)), want); d != "" {
		t.Fatalf("incremental run differs from the full run: %s", d)
	}

	if res := edgeResult(t, evaluate(t, widened)); !res.Currency.Analysis.Widened {
		t.Errorf("incremental run = %+v, want a widened closure", res.Currency.Analysis)
	}
}
