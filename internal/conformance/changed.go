package conformance

import (
	"context"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// Changed scopes of a rule result.
const (
	changedScopeReduced = "reduced"
	changedScopeFull    = "full"
)

// freezeChanged reads the changed files of each selected project and sets state.changed.
func freezeChanged(ctx context.Context, state *frozen, ref string) error {
	seen := map[string]struct{}{}
	files := []string{}
	lead := state.lead()
	base := ""

	for i := range state.projects {
		proj := &state.projects[i]

		set, err := proj.git.ChangedFiles(ctx, ref)
		if err != nil {
			return err //nolint:wrapcheck // a *diag.Error that the caller reports.
		}

		proj.changedBase = set.Base
		if proj == lead {
			base = set.Base
		}

		for _, f := range proj.prefixed(set.Files) {
			if _, ok := seen[f]; !ok {
				seen[f] = struct{}{}
				files = append(files, f)
			}
		}
	}

	slices.SortFunc(files, canonjson.CompareUTF16)

	state.changed = &RunChanged{Ref: ref, Base: base, Files: files}

	return nil
}

// reduceToChanged limits the rules with edge findings to the findings that touch the changed files.
func reduceToChanged(rules []RuleResult, findings []Finding, changed map[string]struct{}) ([]RuleResult, []Finding) {
	out := slices.Clone(rules)
	reduced := map[string]bool{}

	for i := range out {
		if !out[i].Family.EdgeFindings() {
			out[i].ChangedScope = changedScopeFull

			continue
		}

		out[i].ChangedScope = changedScopeReduced
		reduced[out[i].RuleID] = true
	}

	kept := make([]Finding, 0, len(findings))
	counts := map[string]int{}

	for i := range findings {
		f := &findings[i]
		if reduced[f.RuleID] && !touchesChanged(f, changed) {
			continue
		}

		kept = append(kept, *f)
		counts[f.RuleID]++
	}

	for i := range out {
		if !reduced[out[i].RuleID] {
			continue
		}

		out[i].FindingCount = counts[out[i].RuleID]

		if out[i].Verdict == VerdictViolation && out[i].FindingCount == 0 {
			if len(out[i].Reasons) > 0 {
				out[i].Verdict = VerdictUnknown
			} else {
				out[i].Verdict = VerdictPass
			}
		}
	}

	return out, kept
}

// fullScopeRules gives the sorted IDs of the full-scope rules that gave an answer.
func fullScopeRules(rules []RuleResult) []string {
	var ids []string

	for i := range rules {
		r := &rules[i]
		if r.ChangedScope != changedScopeFull {
			continue
		}

		switch r.Verdict {
		case VerdictPass, VerdictViolation, VerdictUnknown:
			ids = append(ids, r.RuleID)
		case VerdictNotApplicable, VerdictError:
		}
	}

	slices.SortFunc(ids, canonjson.CompareUTF16)

	return ids
}

func touchesChanged(finding *Finding, changed map[string]struct{}) bool {
	isChanged := func(p string) bool {
		_, ok := changed[p]

		return ok
	}

	if finding.Location != nil && isChanged(finding.Location.Path) {
		return true
	}

	for _, l := range finding.RelatedLocations {
		if isChanged(l.Path) {
			return true
		}
	}

	for _, s := range finding.EvidencePath {
		if s.Node != nil && isChanged(s.Node.FilePath) {
			return true
		}

		if s.Edge != nil && s.Edge.Location != nil && isChanged(s.Edge.Location.Path) {
			return true
		}
	}

	return false
}
