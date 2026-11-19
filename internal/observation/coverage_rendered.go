package observation

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// The rendering adapter of the assurance evidence records its own coverage at the level `rendered-or-evaluated`
// (design D20). It does not change the profile of the provider: the provider-level record stays as it is, a
// rendered-level record is added beside it, and a predicate that the rendered occurrences can cure is `complete` when
// either record is. A provider-level gap that the rendered record cures stays listed, marked resolved.

// Level names of the records of one predicate.
const (
	// LevelDeclared is the provider-level record (what the code graph shows).
	LevelDeclared = "declared"
	// LevelRenderedOrEvaluated is the record of the rendering adapter.
	LevelRenderedOrEvaluated = "rendered-or-evaluated"
)

// FamilyRenderedResourceSet is the predicate of the rendering adapter: the rendered sources of an environment gave
// the complete set of resources. It is Concordia's own predicate, not a family of a provider profile, so it is
// listed in no profile and is computed only for an environment with a rendered record. It reuses the loss mode
// `unrendered-template` of `rendered-templates`.
const FamilyRenderedResourceSet = "rendered-resource-set"

// RenderedRecord is the coverage of the rendered sources of one environment, already combined: `complete` only when
// every source rendered completely, `failed` when one failed, else `partial`.
type RenderedRecord struct {
	Environment string
	Status      CoverageStatus
	// Gaps are the gaps of the sources that did not render completely. Each has the loss mode `unrendered-template`.
	Gaps []CoverageGap
}

// LevelRecord is the status of one predicate at one level, listed when a rendered record exists.
type LevelRecord struct {
	Level  string         `json:"level"`
	Status CoverageStatus `json:"status"`
}

func (b *recordBuilder) renderedOf(environment string) *RenderedRecord {
	for i := range b.in.Rendered {
		if b.in.Rendered[i].Environment == environment {
			return &b.in.Rendered[i]
		}
	}

	return nil
}

// renderingCures gives the loss modes that a complete rendered record cures for the predicate: a container that the
// provider merged, or whose manifest is a template, is an occurrence of the rendered documents.
func renderingCures(predicate string) []string {
	switch predicate {
	case FamilyContainerOccurrenceVersions, string(KindUsesImage):
		return []string{LossMergedOccurrences, LossUnrenderedTemplate}
	case FamilyRenderedTemplates:
		return []string{LossUnrenderedTemplate}
	}

	return nil
}

// resolveByRendering moves the loss modes that the complete rendered record of the environment cures out of modes
// (they no longer limit the predicate) into the resolved list. That includes a loss mode that the profile states for a
// partial family as a whole, with no gap: the rendered documents show every container of the environment.
func (b *recordBuilder) resolveByRendering(predicate, environment string, modes map[string][]CoverageGap) (resolved []LossMode, unresolved int) {
	rendered := b.renderedOf(environment)
	if rendered == nil || rendered.Status != CoverageComplete {
		return nil, countGaps(modes)
	}

	for _, id := range renderingCures(predicate) {
		gaps, ok := modes[id]
		if !ok {
			continue
		}

		resolved = append(resolved, LossMode{ID: id, Gaps: gaps, ResolvedBy: LevelRenderedOrEvaluated})

		delete(modes, id)
	}

	slices.SortFunc(resolved, func(a, c LossMode) int { return canonjson.CompareUTF16(a.ID, c.ID) })

	return resolved, countGaps(modes)
}

func countGaps(modes map[string][]CoverageGap) int {
	total := 0
	for _, gaps := range modes {
		total += len(gaps)
	}

	return total
}

// renderedResourceSet gives the records of `rendered-resource-set`: one for each environment with a rendered record.
func (b *recordBuilder) renderedResourceSet() []PredicateCoverage {
	out := make([]PredicateCoverage, 0, len(b.in.Rendered))

	for i := range b.in.Rendered {
		rendered := &b.in.Rendered[i]
		rec := PredicateCoverage{
			Predicate: FamilyRenderedResourceSet, Scope: CoverageScope{Environment: rendered.Environment}, Status: rendered.Status,
			LossModes: []LossMode{}, Reasons: []string{},
		}

		if rendered.Status != CoverageComplete {
			rec.Reasons = append(rec.Reasons, "the rendered sources of the environment are "+string(rendered.Status))
		}

		if len(rendered.Gaps) > 0 {
			gaps := SortGaps(slices.Clone(rendered.Gaps))
			rec.LossModes = append(rec.LossModes, LossMode{ID: LossUnrenderedTemplate, Gaps: gaps})
			rec.Unresolved = len(gaps)
		}

		out = append(out, rec)
	}

	return out
}
