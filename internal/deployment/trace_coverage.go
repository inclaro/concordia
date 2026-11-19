package deployment

import (
	"strings"

	"github.com/inclaro/concordia/internal/observation"
)

// recordOf gives the coverage record of a predicate in an environment, nil when the run has none.
func (b *traceBuilder) recordOf(predicate, environment string) *observation.PredicateCoverage {
	return b.coverage[coverageKey{predicate: predicate, environment: environment}]
}

// worstProjectRecord gives the project record of a predicate with the status that limits a claim the most.
func (b *traceBuilder) worstProjectRecord(predicate string) *observation.PredicateCoverage {
	var worst *observation.PredicateCoverage

	for key, rec := range b.coverage {
		if key.predicate != predicate || key.project == "" {
			continue
		}

		if worst == nil || limitsMore(rec, worst) {
			worst = rec
		}
	}

	return worst
}

// limitsMore says whether a project record limits a claim more than the other one: a worse status, and the
// project name that sorts first on a tie.
func limitsMore(rec, other *observation.PredicateCoverage) bool {
	if rec.Status.Rank() != other.Status.Rank() {
		return rec.Status.Rank() > other.Status.Rank()
	}

	return rec.Scope.Project < other.Scope.Project
}

func coverageStateOf(rec *observation.PredicateCoverage) CoverageState {
	if rec == nil {
		return CoverageState{Status: observation.CoverageUnknown, LossModes: []string{}, Reasons: []string{"no coverage record for the environment"}}
	}

	return CoverageState{Status: rec.Status, LossModes: lossModeIDs(rec), Reasons: sortedStrings(rec.Reasons)}
}

func lossModeIDs(rec *observation.PredicateCoverage) []string {
	ids := make([]string, 0, len(rec.LossModes))

	for _, mode := range rec.LossModes {
		ids = append(ids, mode.ID)
	}

	return sortedStrings(ids)
}

// coverageGap gives the gap that an incomplete record shows, nil for a complete one. what names the predicate in the text.
func coverageGap(rec *observation.PredicateCoverage, what string) *Gap {
	switch {
	case rec == nil:
		return &Gap{Reason: ReasonCoverageIncomplete, Detail: "the run has no coverage record for " + what}
	case rec.Status == observation.CoverageComplete:
		return nil
	case rec.Status == observation.CoverageUnsupported:
		return &Gap{Reason: ReasonUnsupportedFamily, Detail: "the provider build does not supply " + what}
	}

	gap := &Gap{Reason: ReasonCoverageIncomplete, LossModes: lossModeIDs(rec)}

	switch {
	case len(rec.Reasons) > 0:
		gap.Detail = what + " is " + string(rec.Status) + ": " + strings.Join(sortedStrings(rec.Reasons), "; ")
	case len(gap.LossModes) > 0:
		gap.Detail = what + " is " + string(rec.Status) + ": " + strings.Join(gap.LossModes, ", ")
	default:
		gap.Detail = what + " is " + string(rec.Status)
	}

	return gap
}
