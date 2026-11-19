package conformance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/observation"
)

// verdict is the coverage statement about one selection.
type verdict struct {
	// reason is the unknown reason, "" when coverage is sufficient.
	reason      string
	coverage    string
	lossModes   []string
	details     []string
	unsupported bool
}

func (res *verdict) lossMode(id string) {
	if !slices.Contains(res.lossModes, id) {
		res.lossModes = append(res.lossModes, id)
	}
}

// unknown records an incomplete cause. target-not-declared wins over coverage-incomplete.
func (res *verdict) unknown(reason, detail string) {
	if res.reason != impact.ReasonTargetNotDeclared {
		res.reason = reason
	}

	res.details = append(res.details, detail)

	if res.coverage == "" || res.coverage == string(observation.CoverageComplete) {
		res.coverage = string(observation.CoveragePartial)
	}
}

// normalize sorts the loss modes and the details and drops repeated details, so that the verdict does not depend on
// the order of the records.
func (res *verdict) normalize() {
	slices.Sort(res.lossModes)
	slices.Sort(res.details)

	res.details = slices.Compact(res.details)
}

// verdictOf says whether coverage allows an empty match or a complete match for the selection: every predicate that
// the kind reads is complete in the scope, and no gap names a reference of the selection.
func (f *ReviewFacts) verdictOf(kind impact.Kind, sel *impact.Selector) verdict {
	res := verdict{coverage: string(observation.CoverageComplete)}

	if reason := f.assuranceUnreadable(kind); reason != "" {
		res.unknown(impact.ReasonNoEvidenceSupplied, reason)
		res.coverage = string(observation.CoverageUnknown)
	}

	for _, family := range f.familiesRead(kind, sel) {
		f.familyVerdict(&res, kind, family, sel.Environment)
	}

	f.gapVerdicts(&res, kind, sel)
	res.normalize()

	return res
}

// familiesRead gives the evidence families that the selection reads: the images of the workloads for an
// `implementation` selection that wants them, the families of the kind otherwise.
func (f *ReviewFacts) familiesRead(kind impact.Kind, sel *impact.Selector) []string {
	if kind == impact.KindImplementation && f.wantsImages(sel) {
		return imageFamilies
	}

	return kindFamilies[kind]
}

// gapVerdicts adds every gap of the kind that limits the selection.
func (f *ReviewFacts) gapVerdicts(res *verdict, kind impact.Kind, sel *impact.Selector) {
	if kind == impact.KindImplementation && !f.wantsImages(sel) {
		return
	}

	for i := range f.gaps {
		if f.gaps[i].kind == kind && f.gapApplies(&f.gaps[i].gap, kind, sel) {
			f.gapVerdict(res, &f.gaps[i].gap)
		}
	}
}

// wantsImages says whether an `implementation` selection reads the images of the workloads.
func (f *ReviewFacts) wantsImages(sel *impact.Selector) bool {
	return len(sel.Match[facetPath]) == 0 || len(sel.Identities) > 0 || len(sel.CalmNodes) > 0
}

// familyVerdict adds the cause of an incomplete coverage record of the family in the scope.
func (f *ReviewFacts) familyVerdict(res *verdict, kind impact.Kind, family, env string) {
	records := f.recordsFor(family, env)
	if len(records) == 0 {
		res.unknown(impact.ReasonCoverageIncomplete, fmt.Sprintf("no coverage record of %s%s", family, scopeText(env)))

		return
	}

	for _, rec := range records {
		res.record(rec, kind, family)
	}
}

// record adds the cause that one coverage record of the family gives for an incomplete coverage.
func (res *verdict) record(rec *observation.PredicateCoverage, kind impact.Kind, family string) {
	switch rec.Status {
	case observation.CoverageComplete:
		return
	case observation.CoverageUnsupported:
		res.unsupported = true
		res.coverage = string(observation.CoverageUnsupported)
		res.details = append(res.details, family+" is unsupported by the provider build")
	case observation.CoveragePartial:
		res.partialRecord(rec, kind, family)
	case observation.CoverageUnknown, observation.CoverageFailed:
		res.coverage = string(rec.Status)
		res.details = append(res.details, fmt.Sprintf("coverage of %s%s is %s: %s", family, scopeOf(rec), rec.Status, strings.Join(rec.Reasons, ", ")))

		if res.reason != impact.ReasonTargetNotDeclared {
			res.reason = impact.ReasonCoverageIncomplete
		}
	}
}

// partialRecord adds a partial record. A record whose loss modes are all explained by a gap or by the identity of the
// kind adds nothing.
func (res *verdict) partialRecord(rec *observation.PredicateCoverage, kind impact.Kind, family string) {
	residual := residualLossModes(rec, kind)
	if len(rec.LossModes) > 0 && len(residual) == 0 {
		return
	}

	for _, id := range residual {
		res.lossMode(id)
	}

	res.unknown(impact.ReasonCoverageIncomplete, fmt.Sprintf("coverage of %s%s is partial", family, scopeOf(rec)))
}

// residualLossModes are the loss modes of a partial record that neither a gap (target-not-declared and
// merged-occurrences name their references) nor the identity of the kind explains.
func residualLossModes(rec *observation.PredicateCoverage, kind impact.Kind) []string {
	var out []string

	for _, mode := range rec.LossModes {
		switch {
		case mode.ID == observation.LossTargetNotDeclared || mode.ID == observation.LossMergedOccurrences:
		case slices.Contains(mitigatedLossModes[kind], mode.ID):
		default:
			out = append(out, mode.ID)
		}
	}

	return out
}

func scopeText(env string) string {
	if env == "" {
		return ""
	}

	return " in environment " + env
}

func scopeOf(rec *observation.PredicateCoverage) string {
	switch {
	case rec.Scope.Environment != "":
		return " in environment " + rec.Scope.Environment
	case rec.Scope.Project != "":
		return " in project " + rec.Scope.Project
	default:
		return ""
	}
}

// gapVerdict adds a gap that names a reference or a container occurrence.
func (f *ReviewFacts) gapVerdict(res *verdict, gap *observation.CoverageGap) {
	res.lossMode(gap.Reason)

	switch gap.Reason {
	case observation.LossTargetNotDeclared:
		res.unknown(impact.ReasonTargetNotDeclared, fmt.Sprintf("%s in %s%s references %s %q (%s), which no indexed manifest declares", gap.Workload, gap.Path, scopeText(gap.Environment), gap.TargetKind, gap.Target, gap.ReferenceMode))
	default:
		res.unknown(impact.ReasonCoverageIncomplete, fmt.Sprintf("%s in %s%s: container %q is a merged occurrence (the provider keeps one edge for each image repository)", gap.Workload, gap.Path, scopeText(gap.Environment), gap.Container))
	}
}

// recordsFor gives the coverage records of a family in the scope: the environment records of an infrastructure
// family when an environment is selected (or all of them), the project records of any other family.
func (f *ReviewFacts) recordsFor(family, env string) []*observation.PredicateCoverage {
	environmental := observation.IsInfrastructureKind(observation.CanonicalEdgeKind(family)) || family == observation.FamilyContainerOccurrenceVersions
	inEnv, inProject := f.recordsByScope(family)

	switch {
	case env != "" && environmental:
		return slices.DeleteFunc(inEnv, func(rec *observation.PredicateCoverage) bool { return rec.Scope.Environment != env })
	case environmental && len(inEnv) > 0:
		return inEnv
	default:
		return inProject
	}
}

// recordsByScope splits the coverage records of a family into those of an environment and those of the project.
func (f *ReviewFacts) recordsByScope(family string) (inEnv, inProject []*observation.PredicateCoverage) {
	for i := range f.coverage {
		rec := &f.coverage[i]
		if rec.Predicate != family {
			continue
		}

		if rec.Scope.Environment != "" {
			inEnv = append(inEnv, rec)
		} else {
			inProject = append(inProject, rec)
		}
	}

	return inEnv, inProject
}
