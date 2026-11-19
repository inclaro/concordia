package conformance

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// claimBlocked gives what stops a claim that needs the completeness `need`, or nil: `r.spec.PassNeed()` for a pass,
// `r.spec.Completeness` for an absence. The order of the checks is: a limit that the run went above, a capability
// regression, a failed scope, and the coverage of the predicate (the absence rule). Only the last depends on `need`,
// so a claim that is blocked for another reason stays blocked for every value.
func (r *obligationRun) claimBlocked(need policy.Completeness) *claimBlock {
	if block, done := r.blocks[need]; done {
		return block
	}

	if r.blocks == nil {
		r.blocks = map[policy.Completeness]*claimBlock{}
	}

	block := r.computeBlock(need)
	r.blocks[need] = block

	return block
}

func (r *obligationRun) computeBlock(need policy.Completeness) *claimBlock {
	scope := &r.e.input.Scope

	if len(scope.Breaches) > 0 {
		b := scope.Breaches[0]

		return &claimBlock{ReasonLimitExceeded, fmt.Sprintf("%s is configured as %d, observed %d", b.Limit, b.Configured, b.Observed)}
	}

	family, hasFamily := observation.FamilyForKind(r.ob.Relation.Kind)

	if reg, ok := r.e.regressions[family]; hasFamily && ok {
		return &claimBlock{ReasonCapabilityRegression, fmt.Sprintf("the family %s was %s in the lock profile (%s) and is %s in this run (%s)",
			family, reg.Was, reg.LockDigest, reg.Now, reg.CurrentDigest)}
	}

	projects := scope.projectsOfNodes(slices.Concat([]string{r.ob.Subject}, r.ob.Relation.Target))

	if failed := scope.FailedScopesOf(projects); len(failed) > 0 {
		return &claimBlock{ReasonCoverageIncomplete, fmt.Sprintf("the scope %s failed: %s", failed[0].Project, failed[0].Reason)}
	}

	return r.coverageBlock(family, hasFamily, projects, need)
}

// coverageBlock applies the completeness that the claim needs to the coverage records of the predicate in the scope.
func (r *obligationRun) coverageBlock(family string, hasFamily bool, projects []string, need policy.Completeness) *claimBlock {
	supported := hasFamily && !r.familyUnsupported(family)
	records := r.predicateRecords(family, hasFamily, projects)
	claim := &AbsenceInput{Coverage: records, Breaches: nil, Supported: supported, InScope: true, AnalysisOK: true}

	switch need {
	case policy.CompletenessAny:
		// Nothing is needed of the coverage, but an unsupported family stays unsupported: it blocks every claim.
		if !supported || slices.ContainsFunc(records, func(p observation.PredicateCoverage) bool { return p.Status == observation.CoverageUnsupported }) {
			return unsupportedObservationBlock()
		}

		return nil
	case policy.CompletenessPartial:
		claim.Coverage = slices.DeleteFunc(slices.Clone(records), func(p observation.PredicateCoverage) bool { return p.Status == observation.CoveragePartial })

		if len(records) > 0 && len(claim.Coverage) == 0 {
			return partialOnlyBlock(supported)
		}
	case policy.CompletenessComplete:
	}

	if res := EvaluateAbsence(claim); res.Verdict == VerdictUnknown {
		return &claimBlock{res.Reason, res.Detail}
	}

	return nil
}

// familyUnsupported says whether the run reports the family as unsupported.
func (r *obligationRun) familyUnsupported(family string) bool {
	state, ok := r.e.input.Typed.Families[family]

	return ok && state.Level == observation.SupportUnsupported
}

// predicateRecords are the coverage records that judge the obligation: none when its relation has no family.
// An obligation of an environment is judged by the record of its predicate for that environment. The project
// records apply only to an obligation with no environment, and to a predicate with no record for the environment.
func (r *obligationRun) predicateRecords(family string, hasFamily bool, projects []string) []observation.PredicateCoverage {
	if !hasFamily {
		return nil
	}

	predicates := r.e.input.Scope.Predicates

	if r.ob.Environment != "" {
		scope := observation.CoverageScope{Environment: r.ob.Environment}
		if records := CoverageOf(predicates, []string{family}, scope); len(records) > 0 {
			return records
		}
	}

	return PredicatesOf(predicates, []observation.CanonicalEdgeKind{r.ob.Relation.Kind}, projects)
}

func unsupportedObservationBlock() *claimBlock {
	return &claimBlock{ReasonUnsupportedFamily, "the provider does not support the observation"}
}

// partialOnlyBlock is the block of a claim that tolerates partial coverage when every record is partial: only an
// unsupported family blocks it.
func partialOnlyBlock(supported bool) *claimBlock {
	if !supported {
		return unsupportedObservationBlock()
	}

	return nil
}
