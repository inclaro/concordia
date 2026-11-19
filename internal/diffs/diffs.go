// Package diffs compares two states of the architecture, the bindings or a check result.
// All functions are pure. Every list in the results is non-nil and sorted.
package diffs

import (
	"bytes"
	"slices"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
)

// Change is one added, removed or changed declaration.
type Change struct {
	Type string `json:"type"` // node|relationship|control|binding
	ID   string `json:"id"`
}

// IntentDiff is the difference of two declaration sets.
type IntentDiff struct {
	Added   []Change `json:"added"`
	Removed []Change `json:"removed"`
	Changed []Change `json:"changed"`
}

// ObservedPair is one directed CALM pair with its classification.
type ObservedPair struct {
	From           string `json:"from"`
	To             string `json:"to"`
	RelationshipID string `json:"relationshipId,omitempty"`
	Classification string `json:"classification"`
}

// ObservedChange is a pair whose classification differs.
type ObservedChange struct {
	From               string `json:"from"`
	To                 string `json:"to"`
	RelationshipID     string `json:"relationshipId,omitempty"`
	FromClassification string `json:"fromClassification"`
	ToClassification   string `json:"toClassification"`
}

// ObservedDiff is the difference of two reconciliations.
type ObservedDiff struct {
	Added   []ObservedPair   `json:"added"`
	Removed []ObservedPair   `json:"removed"`
	Changed []ObservedChange `json:"changed"`
}

// FindingRef names one finding.
type FindingRef struct {
	Location    *conformance.Location `json:"location,omitempty"`
	Fingerprint string                `json:"fingerprint"`
	RuleID      string                `json:"ruleId"`
	Message     string                `json:"message"`
}

// FindingChange is a finding whose disposition differs.
type FindingChange struct {
	Fingerprint string `json:"fingerprint"`
	RuleID      string `json:"ruleId"`
	From        string `json:"from"`
	To          string `json:"to"`
}

// FindingDiff is the difference of two finding lists.
type FindingDiff struct {
	New          []FindingRef    `json:"new"`
	Resolved     []FindingRef    `json:"resolved"`
	Reintroduced []FindingRef    `json:"reintroduced"`
	Changed      []FindingChange `json:"changed"`
}

// CoverageChange is a rule whose file coverage ratio differs.
type CoverageChange struct {
	RuleID string  `json:"ruleId"`
	From   float64 `json:"from"`
	To     float64 `json:"to"`
}

// CoverageDiff is the difference of the rule coverage.
type CoverageDiff struct {
	Regressions  []CoverageChange `json:"regressions"`
	Improvements []CoverageChange `json:"improvements"`
}

// WaiverDiff is the difference of the waivers.
type WaiverDiff struct {
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Expired []string `json:"expired"`
}

func jsonEqual(a, b any) bool {
	x, errA := canonjson.Marshal(a)
	y, errB := canonjson.Marshal(b)

	return errA == nil && errB == nil && bytes.Equal(x, y)
}

func sortChanges(c []Change) {
	slices.SortFunc(c, func(a, b Change) int {
		if r := canonjson.CompareUTF16(a.Type, b.Type); r != 0 {
			return r
		}

		return canonjson.CompareUTF16(a.ID, b.ID)
	})
}

func diffKeyed(typ string, from, target map[string]any, out *IntentDiff) {
	for k, v := range target {
		old, ok := from[k]

		switch {
		case !ok:
			out.Added = append(out.Added, Change{Type: typ, ID: k})
		case !jsonEqual(old, v):
			out.Changed = append(out.Changed, Change{Type: typ, ID: k})
		}
	}

	for k := range from {
		if _, ok := target[k]; !ok {
			out.Removed = append(out.Removed, Change{Type: typ, ID: k})
		}
	}
}

func declarations(model *intent.ArchitectureModel) (nodes, rels, controls map[string]any) {
	nodes, rels, controls = map[string]any{}, map[string]any{}, map[string]any{}
	if model == nil {
		return nodes, rels, controls
	}

	for i := range model.Nodes {
		n := model.Nodes[i]
		n.Pointer = ""
		nodes[n.ID] = n
	}

	for i := range model.Relationships {
		r := model.Relationships[i]
		r.Pointer = ""
		rels[r.ID] = r
	}

	for i := range model.Controls {
		c := model.Controls[i]
		c.Pointer = ""
		controls[string(c.Scope.Level)+":"+c.Scope.ID+":"+c.ControlID] = c
	}

	return nodes, rels, controls
}

// Intent compares two architecture models. A nil model is an empty model.
func Intent(from, to *intent.ArchitectureModel) IntentDiff {
	out := IntentDiff{Added: []Change{}, Removed: []Change{}, Changed: []Change{}}
	fn, fr, fc := declarations(from)
	tn, tr, tc := declarations(to)

	diffKeyed("node", fn, tn, &out)
	diffKeyed("relationship", fr, tr, &out)
	diffKeyed("control", fc, tc, &out)

	sortChanges(out.Added)
	sortChanges(out.Removed)
	sortChanges(out.Changed)

	return out
}

// Bindings compares two binding lists by ID.
func Bindings(from, target []binding.Binding) IntentDiff {
	out := IntentDiff{Added: []Change{}, Removed: []Change{}, Changed: []Change{}}
	index := func(list []binding.Binding) map[string]any {
		m := make(map[string]any, len(list))

		for i := range list {
			b := list[i]
			b.Source = ""
			m[b.ID] = b
		}

		return m
	}

	diffKeyed("binding", index(from), index(target), &out)
	sortChanges(out.Added)
	sortChanges(out.Removed)
	sortChanges(out.Changed)

	return out
}

// pairKey identifies a reconciliation entry: pairs that no relationship declares have no
// relationship ID, so one pair can have several entries.
func pairKey(e *conformance.ReconciliationEntry) string {
	return e.From + "\x00" + e.To + "\x00" + e.RelationshipID
}

func reconciliation(r *conformance.Run) map[string]conformance.ReconciliationEntry {
	m := map[string]conformance.ReconciliationEntry{}
	if r == nil {
		return m
	}

	for i := range r.Reconciliation {
		m[pairKey(&r.Reconciliation[i])] = r.Reconciliation[i]
	}

	return m
}

// Observed compares the reconciliation of two runs.
func Observed(from, to *conformance.Run) ObservedDiff {
	out := ObservedDiff{Added: []ObservedPair{}, Removed: []ObservedPair{}, Changed: []ObservedChange{}}
	before, after := reconciliation(from), reconciliation(to)

	for k, e := range after {
		old, ok := before[k]

		switch {
		case !ok:
			out.Added = append(out.Added, ObservedPair{From: e.From, To: e.To, RelationshipID: e.RelationshipID, Classification: string(e.Classification)})
		case old.Classification != e.Classification:
			out.Changed = append(out.Changed, ObservedChange{
				From: e.From, To: e.To, RelationshipID: e.RelationshipID,
				FromClassification: string(old.Classification), ToClassification: string(e.Classification),
			})
		}
	}

	for k, e := range before {
		if _, ok := after[k]; !ok {
			out.Removed = append(out.Removed, ObservedPair{From: e.From, To: e.To, RelationshipID: e.RelationshipID, Classification: string(e.Classification)})
		}
	}

	pairCmp := func(a, b ObservedPair) int {
		if r := canonjson.CompareUTF16(a.From, b.From); r != 0 {
			return r
		}

		if r := canonjson.CompareUTF16(a.To, b.To); r != 0 {
			return r
		}

		return canonjson.CompareUTF16(a.RelationshipID, b.RelationshipID)
	}
	slices.SortFunc(out.Added, pairCmp)
	slices.SortFunc(out.Removed, pairCmp)
	slices.SortFunc(out.Changed, func(a, b ObservedChange) int {
		if r := canonjson.CompareUTF16(a.From, b.From); r != 0 {
			return r
		}

		if r := canonjson.CompareUTF16(a.To, b.To); r != 0 {
			return r
		}

		return canonjson.CompareUTF16(a.RelationshipID, b.RelationshipID)
	})

	return out
}

func findings(r *conformance.Run) map[string]*conformance.Finding {
	m := map[string]*conformance.Finding{}
	if r == nil {
		return m
	}

	for i := range r.Findings {
		m[r.Findings[i].Fingerprint] = &r.Findings[i]
	}

	return m
}

func refOf(f *conformance.Finding) FindingRef {
	return FindingRef{Fingerprint: f.Fingerprint, RuleID: f.RuleID, Message: f.Message, Location: f.Location}
}

func cmpRef(a, b FindingRef) int {
	if r := canonjson.CompareUTF16(a.RuleID, b.RuleID); r != 0 {
		return r
	}

	return canonjson.CompareUTF16(a.Fingerprint, b.Fingerprint)
}

// Findings compares the findings of two runs by fingerprint.
func Findings(from, to *conformance.Run) FindingDiff {
	out := FindingDiff{New: []FindingRef{}, Resolved: []FindingRef{}, Reintroduced: []FindingRef{}, Changed: []FindingChange{}}
	fromByFP, toByFP := findings(from), findings(to)

	resolvedBefore := map[string]struct{}{}

	if from != nil {
		for _, fprint := range from.Exceptions.ResolvedBaselineEntries {
			resolvedBefore[fprint] = struct{}{}
		}
	}

	for fprint, cur := range toByFP {
		old, ok := fromByFP[fprint]

		switch {
		case !ok:
			if _, was := resolvedBefore[fprint]; was {
				out.Reintroduced = append(out.Reintroduced, refOf(cur))
			} else {
				out.New = append(out.New, refOf(cur))
			}
		case old.Disposition != cur.Disposition:
			out.Changed = append(out.Changed, FindingChange{
				Fingerprint: fprint, RuleID: cur.RuleID, From: string(old.Disposition), To: string(cur.Disposition),
			})
		}
	}

	for fprint, prev := range fromByFP {
		if _, ok := toByFP[fprint]; !ok {
			out.Resolved = append(out.Resolved, refOf(prev))
		}
	}

	slices.SortFunc(out.New, cmpRef)
	slices.SortFunc(out.Resolved, cmpRef)
	slices.SortFunc(out.Reintroduced, cmpRef)
	slices.SortFunc(out.Changed, func(a, b FindingChange) int {
		if r := canonjson.CompareUTF16(a.RuleID, b.RuleID); r != 0 {
			return r
		}

		return canonjson.CompareUTF16(a.Fingerprint, b.Fingerprint)
	})

	return out
}

func ratios(r *conformance.Run) map[string]float64 {
	out := map[string]float64{}
	if r == nil {
		return out
	}

	for i := range r.Rules {
		if c := r.Rules[i].Coverage; c != nil {
			out[r.Rules[i].RuleID] = c.Files.Ratio
		}
	}

	return out
}

// Coverage compares the file coverage ratio of the rules that have coverage in both runs.
func Coverage(from, to *conformance.Run) CoverageDiff {
	out := CoverageDiff{Regressions: []CoverageChange{}, Improvements: []CoverageChange{}}
	f, t := ratios(from), ratios(to)

	for id, tv := range t {
		fv, ok := f[id]
		switch {
		case !ok:
		case tv < fv:
			out.Regressions = append(out.Regressions, CoverageChange{RuleID: id, From: fv, To: tv})
		case tv > fv:
			out.Improvements = append(out.Improvements, CoverageChange{RuleID: id, From: fv, To: tv})
		}
	}

	cmp := func(a, b CoverageChange) int { return canonjson.CompareUTF16(a.RuleID, b.RuleID) }
	slices.SortFunc(out.Regressions, cmp)
	slices.SortFunc(out.Improvements, cmp)

	return out
}

func waivers(r *conformance.Run) map[string]string {
	m := map[string]string{}
	if r == nil {
		return m
	}

	for i := range r.Exceptions.Waivers {
		w := &r.Exceptions.Waivers[i]
		m[w.ID] = w.State
	}

	return m
}

// Waivers compares the waivers of two runs by ID.
func Waivers(from, to *conformance.Run) WaiverDiff {
	out := WaiverDiff{Added: []string{}, Removed: []string{}, Expired: []string{}}
	before, tgt := waivers(from), waivers(to)

	for id, state := range tgt {
		old, ok := before[id]

		switch {
		case !ok:
			out.Added = append(out.Added, id)
		case old == "active" && state == "expired":
			out.Expired = append(out.Expired, id)
		}
	}

	for id := range before {
		if _, ok := tgt[id]; !ok {
			out.Removed = append(out.Removed, id)
		}
	}

	for _, l := range [][]string{out.Added, out.Removed, out.Expired} {
		slices.SortFunc(l, canonjson.CompareUTF16)
	}

	return out
}
