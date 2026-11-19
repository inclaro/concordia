package exceptions

import (
	"slices"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
)

// Disposition says how the exception registers treat a finding.
type Disposition string

// Disposition values. The text is the value in results.json.
const (
	DispositionActive        Disposition = "active"
	DispositionBaselined     Disposition = "baselined"
	DispositionWaived        Disposition = "waived"
	DispositionExpiredWaiver Disposition = "expired-waiver"
	baselineRef                          = "baseline"
)

// Key is the part of a finding that a disposition reads. A finding waiver reads Fingerprint and RuleID. A scoped
// waiver reads RuleID and the scope keys that it lists, and never reads Fingerprint. An empty Subject,
// Environment, or ObligationID means the finding has none, so a scope key that names one does not match it.
type Key struct {
	Fingerprint  string
	RuleID       string
	Subject      string
	Environment  string
	ObligationID string
}

// Assignment is the disposition of one finding. Ref is the waiver ID, or
// "baseline"; it is empty (JSON null) for DispositionActive.
type Assignment struct {
	Disposition Disposition
	Ref         string
}

// Result is the result of ApplyDispositions.
type Result struct {
	// Assignments has one value for each finding, in the order of the input.
	Assignments []Assignment
	// ResolvedBaselineEntries are the fingerprints of baseline entries that are
	// no longer current, for the rules that ran. Sorted.
	ResolvedBaselineEntries []string
	// OrphanedWaivers are the IDs of waivers that match no current finding. Sorted.
	OrphanedWaivers []string
	// Diagnostics are CO-EXC-006 for each expired waiver in use (finding
	// order), then CO-EXC-003 for each orphaned waiver (ID order).
	Diagnostics []diag.Diagnostic
}

// ApplyDispositions gives the disposition of each finding: a waiver that matches and is active, else such a waiver that expired, else the baseline (fingerprint), else active. A finding waiver matches by fingerprint and rule. A scoped waiver matches by rule and each scope key that it lists, never by fingerprint. The caller passes only waivers whose approver it has verified. When two or more waivers match, an active waiver wins, then the later expiry, then the smaller ID. It never removes a finding.
//
// rulesRun limits the resolved-baseline detection to the rules that ran. A nil
// rulesRun means the rules of the findings; an empty one means no rule.
// A nil baseline or nil waivers means no register.
func ApplyDispositions(findings []Key, baseline *Baseline, waivers []Waiver, now time.Time, rulesRun []string) Result {
	run := dispositionRun{
		waivers:     waivers,
		baselineSet: baselineFingerprints(baseline),
		now:         now,
		used:        make(map[string]struct{}),
		diagnostics: make([]diag.Diagnostic, 0),
	}
	assignments := make([]Assignment, 0, len(findings))

	for i := range findings {
		assignments = append(assignments, run.assign(&findings[i]))
	}

	orphaned := orphanedWaivers(waivers, run.used)

	return Result{
		Assignments:             assignments,
		ResolvedBaselineEntries: resolvedEntries(findings, baseline, rulesRun),
		OrphanedWaivers:         orphaned,
		Diagnostics:             append(run.diagnostics, orphanDiagnostics(waivers, orphaned)...),
	}
}

// dispositionRun holds the registers and the state that ApplyDispositions builds while it walks the findings.
type dispositionRun struct {
	waivers     []Waiver
	baselineSet map[string]struct{}
	now         time.Time
	// used has the ID of each waiver that matched a finding.
	used map[string]struct{}
	// diagnostics has CO-EXC-006 for each expired waiver in use, in finding order.
	diagnostics []diag.Diagnostic
}

// assign gives the disposition of one finding and records the waivers that it used.
func (r *dispositionRun) assign(found *Key) Assignment {
	matched, matchedActive := selectWaiver(r.waivers, found, r.now, r.used)

	switch {
	case matched != nil && matchedActive:
		return Assignment{Disposition: DispositionWaived, Ref: matched.ID}
	case matched != nil:
		r.diagnostics = append(r.diagnostics, diag.New(diag.EXC006, diag.Params{idKey: matched.ID, expiresKey: matched.Expires}))

		return Assignment{Disposition: DispositionExpiredWaiver, Ref: matched.ID}
	}

	if _, ok := r.baselineSet[found.Fingerprint]; ok {
		return Assignment{Disposition: DispositionBaselined, Ref: baselineRef}
	}

	return Assignment{Disposition: DispositionActive}
}

// baselineFingerprints gives the set of fingerprints in the baseline. A nil baseline gives an empty set.
func baselineFingerprints(baseline *Baseline) map[string]struct{} {
	set := make(map[string]struct{})

	if baseline == nil {
		return set
	}

	for _, entry := range baseline.Findings {
		set[entry.Fingerprint] = struct{}{}
	}

	return set
}

// orphanDiagnostics gives the OrphanDiagnostic of each waiver whose ID is in orphaned, in the order of orphaned.
func orphanDiagnostics(waivers []Waiver, orphaned []string) []diag.Diagnostic {
	byID := make(map[string]*Waiver, len(waivers))

	for i := range waivers {
		byID[waivers[i].ID] = &waivers[i]
	}

	out := make([]diag.Diagnostic, 0, len(orphaned))

	for _, id := range orphaned {
		out = append(out, OrphanDiagnostic(byID[id]))
	}

	return out
}

// OrphanDiagnostic gives the warning for a waiver that matches no current finding: CO-EXC-010 for a scoped waiver,
// CO-EXC-003 for a finding waiver. The waiver stays in the register.
func OrphanDiagnostic(w *Waiver) diag.Diagnostic {
	if w != nil && w.IsScoped() {
		return diag.New(diag.EXC010, diag.Detail(w.ID+" (rule "+w.Rule+")"))
	}

	id := ""
	if w != nil {
		id = w.ID
	}

	return diag.New(diag.EXC003, diag.Params{idKey: id})
}

// Covers says whether the waiver covers the finding. A finding waiver needs the fingerprint and the rule. A
// scoped waiver needs the rule and each scope key that it lists; it never reads the fingerprint.
func (w *Waiver) Covers(found *Key) bool {
	if w.Rule != found.RuleID {
		return false
	}

	s := w.Scope
	if s == nil {
		return w.Finding == found.Fingerprint
	}

	return (s.Subject == "" || s.Subject == found.Subject) &&
		(s.Environment == "" || s.Environment == found.Environment) &&
		(s.Obligation == "" || s.Obligation == found.ObligationID)
}

// betterWaiver says whether candidate beats best: active first, then the later
// end (an invalid date is the earliest), then the smaller ID.
func betterWaiver(candidate *Waiver, candidateActive bool, best *Waiver, bestActive bool) bool {
	if candidateActive != bestActive {
		return candidateActive
	}

	candidateEnd, candidateOK := waiverEnd(candidate)
	bestEnd, bestOK := waiverEnd(best)

	if candidateOK != bestOK {
		return candidateOK
	}

	if candidateOK && !candidateEnd.Equal(bestEnd) {
		return candidateEnd.After(bestEnd)
	}

	return canonjson.CompareUTF16(candidate.ID, best.ID) < 0
}

// resolvedEntries gives the sorted fingerprints of baseline entries that no
// current finding has, for the rules that ran.
func resolvedEntries(findings []Key, baseline *Baseline, rulesRun []string) []string {
	out := make([]string, 0)

	if baseline == nil {
		return out
	}

	current := currentFingerprints(findings)
	ran := rulesThatRan(findings, rulesRun)

	for _, b := range baseline.Findings {
		_, ruleRan := ran[b.RuleID]
		_, isCurrent := current[b.Fingerprint]

		if ruleRan && !isCurrent {
			out = append(out, b.Fingerprint)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// currentFingerprints gives the set of fingerprints of the findings.
func currentFingerprints(findings []Key) map[string]struct{} {
	current := make(map[string]struct{}, len(findings))

	for i := range findings {
		current[findings[i].Fingerprint] = struct{}{}
	}

	return current
}

// rulesThatRan gives the set of rules that ran. A nil rulesRun means the rules of the findings.
func rulesThatRan(findings []Key, rulesRun []string) map[string]struct{} {
	ran := make(map[string]struct{})

	if rulesRun != nil {
		for _, rule := range rulesRun {
			ran[rule] = struct{}{}
		}

		return ran
	}

	for i := range findings {
		ran[findings[i].RuleID] = struct{}{}
	}

	return ran
}

// orphanedWaivers gives the sorted IDs of the waivers that are not in used.
func orphanedWaivers(waivers []Waiver, used map[string]struct{}) []string {
	out := make([]string, 0)

	for i := range waivers {
		if _, ok := used[waivers[i].ID]; !ok {
			out = append(out, waivers[i].ID)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// selectWaiver gives the best waiver for a finding (see betterWaiver) and whether it is
// active. It adds every matching waiver to used. It gives nil when none matches.
func selectWaiver(waivers []Waiver, found *Key, now time.Time, used map[string]struct{}) (*Waiver, bool) {
	var (
		matched       *Waiver
		matchedActive bool
	)

	for i := range waivers {
		w := &waivers[i]
		if !w.Covers(found) {
			continue
		}

		used[w.ID] = struct{}{}
		active := IsWaiverActive(w, now)

		if matched == nil || betterWaiver(w, active, matched, matchedActive) {
			matched, matchedActive = w, active
		}
	}

	return matched, matchedActive
}
