package adr

import (
	"path"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/intent"
)

// CollectCandidates gives the non-authoritative candidates of the run (design D10, ADR spec "Mentions are never
// governance authority"): the evidence decisions that no register entry matched (`unregistered-decision`, ID the
// label, path the decision path), the `mentions` and `references-file` edges from decision files (`mention`,
// path the decision file, subject the mentioned file or name), and the entries of the CALM `adrs` array that name
// no registered decision (`calm-adrs-entry`, path the entry verbatim). matched is the set of decision paths that
// a register entry matched. The result is sorted by kind, ID, path, subject, has no duplicates, and is never nil.
// Candidates are data for the report only: nothing here reads or changes a presence, applicability, or history
// result, and the caller must not use them as GOVERNS or ENFORCED_BY links.
func CollectCandidates(
	evidence *DecisionEvidence, reg *Register, model *intent.ArchitectureModel, matched map[string]bool,
) []conformance.DecisionCandidate {
	out := []conformance.DecisionCandidate{}
	labels := map[string]string{}

	if evidence != nil {
		for i := range evidence.Decisions {
			d := &evidence.Decisions[i]
			labels[d.Path] = d.Label

			if !matched[d.Path] {
				out = append(out, conformance.DecisionCandidate{Kind: CandidateUnregistered, ID: d.Label, Path: d.Path})
			}
		}

		for _, m := range evidence.Mentions {
			out = append(out, conformance.DecisionCandidate{
				Kind: CandidateMention, ID: labels[m.From], Path: m.From, Subject: m.Subject,
			})
		}
	}

	if model != nil {
		out = append(out, calmCandidates(model.ADRs, evidence, reg, matched)...)
	}

	slices.SortFunc(out, func(x, y conformance.DecisionCandidate) int {
		for _, c := range []int{
			canonjson.CompareUTF16(x.Kind, y.Kind), canonjson.CompareUTF16(x.ID, y.ID),
			canonjson.CompareUTF16(x.Path, y.Path), canonjson.CompareUTF16(x.Subject, y.Subject),
		} {
			if c != 0 {
				return c
			}
		}

		return 0
	})

	return slices.Compact(out)
}

// calmCandidates gives a candidate for every `adrs` entry that names no registered decision.
func calmCandidates(
	adrs []string, evidence *DecisionEvidence, reg *Register, matched map[string]bool,
) []conformance.DecisionCandidate {
	registeredPaths, registeredIDs := registeredTargets(reg, matched)

	var out []conformance.DecisionCandidate

	for _, entry := range adrs {
		target := calmTarget(entry)
		if target == "" || namesRegistered(target, registeredIDs, registeredPaths) {
			continue
		}

		out = append(out, conformance.DecisionCandidate{
			Kind: CandidateCalmADRs,
			Path: entry,
			ID:   evidenceLabelFor(target, evidence),
		})
	}

	return out
}

// registeredTargets gives the decision paths and canonical IDs that the register and the matched set account for.
func registeredTargets(reg *Register, matched map[string]bool) (paths, ids map[string]bool) {
	paths = map[string]bool{}
	ids = map[string]bool{}

	for p := range matched {
		paths[p] = true
	}

	if reg == nil {
		return paths, ids
	}

	for i := range reg.Entries {
		ids[reg.Entries[i].ID] = true

		if reg.Entries[i].Path != "" {
			paths[reg.Entries[i].Path] = true
		}
	}

	return paths, ids
}

// namesRegistered says whether the `adrs` target names a registered decision by its file stem or by its path.
func namesRegistered(target string, ids, paths map[string]bool) bool {
	stem := strings.TrimSuffix(path.Base(target), path.Ext(target))

	return ids[CanonicalID(stem)] || namesPath(target, paths)
}

// evidenceLabelFor gives the label of the first evidence decision the target names, or "" when none.
func evidenceLabelFor(target string, evidence *DecisionEvidence) string {
	if evidence == nil {
		return ""
	}

	for i := range evidence.Decisions {
		if namesOnePath(target, evidence.Decisions[i].Path) {
			return evidence.Decisions[i].Label
		}
	}

	return ""
}

// calmTarget normalizes an `adrs` entry for comparison: no fragment or query, no leading `./`.
func calmTarget(entry string) string {
	t := strings.TrimSpace(entry)
	t, _, _ = strings.Cut(t, "#")
	t, _, _ = strings.Cut(t, "?")

	for strings.HasPrefix(t, "./") {
		t = t[2:]
	}

	return t
}

// namesPath says whether the `adrs` target names one of the paths: equal, a URL or path that ends with the
// repository-relative path, or a path that ends with the target.
func namesPath(target string, paths map[string]bool) bool {
	for p := range paths {
		if namesOnePath(target, p) {
			return true
		}
	}

	return false
}

// namesOnePath says whether the `adrs` target names the path p; see namesPath.
func namesOnePath(target, p string) bool {
	return target == p || strings.HasSuffix(target, "/"+p) || strings.HasSuffix(p, "/"+target)
}
