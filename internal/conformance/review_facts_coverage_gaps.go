package conformance

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/observation"
)

// collectGaps keeps the gaps of the coverage records that name a reference whose target is not declared, or a
// merged container occurrence, with the dependency kind they limit.
func (f *ReviewFacts) collectGaps() {
	envScoped, projectScoped := f.scopedGaps()

	gaps := envScoped
	if len(gaps) == 0 {
		gaps = projectScoped
	}

	sorted := observation.SortGaps(gaps)
	for i := range sorted {
		if kind, ok := gapKind(&sorted[i]); ok {
			f.gaps = append(f.gaps, reviewGap{kind: kind, gap: sorted[i]})
		}
	}
}

// scopedGaps gives the gaps that the coverage records name, apart by whether the record is scoped to an environment.
func (f *ReviewFacts) scopedGaps() (envScoped, projectScoped []observation.CoverageGap) {
	for i := range f.coverage {
		rec := &f.coverage[i]

		for _, mode := range rec.LossModes {
			if !isReferenceLossMode(mode.ID) {
				continue
			}

			if rec.Scope.Environment != "" {
				envScoped = append(envScoped, mode.Gaps...)
			} else {
				projectScoped = append(projectScoped, mode.Gaps...)
			}
		}
	}

	return envScoped, projectScoped
}

// isReferenceLossMode says whether a loss mode names references or occurrences in its gaps.
func isReferenceLossMode(id string) bool {
	return id == observation.LossTargetNotDeclared || id == observation.LossMergedOccurrences
}

// gapKind gives the dependency kind that a gap limits (the reference mode and the target kind decide, as for the
// item of an existing edge).
func gapKind(gap *observation.CoverageGap) (impact.Kind, bool) {
	if gap.Reason == observation.LossMergedOccurrences {
		return impact.KindImplementation, true
	}

	if gap.Reason != observation.LossTargetNotDeclared {
		return "", false
	}

	switch gap.ReferenceMode {
	case refTLS:
		return impact.KindCertificateReference, true
	case refParentRef:
		return impact.KindPublicEntryPoint, true
	case refRoleRef:
		return impact.KindRoleBinding, true
	case refServiceAccount, refSubject:
		return impact.KindRuntimeIdentity, true
	case refEnv, refEnvFrom, refVolume:
		return gapReferenceKind(gap.TargetKind)
	default:
		return "", false
	}
}

func gapReferenceKind(targetKind string) (impact.Kind, bool) {
	switch targetKind {
	case targetSecret:
		return impact.KindSecretReference, true
	case targetConfigMap:
		return impact.KindConfigurationReference, true
	case targetClaim:
		return impact.KindStorageReference, true
	default:
		return "", false
	}
}

// indexConsumers makes the resources findable by the `<Kind>/<name>` that a gap names, per environment.
func (f *ReviewFacts) indexConsumers() {
	for id := range f.nodes {
		node := f.nodes[id]

		res, ok := resourceOf(&node)
		if !ok {
			continue
		}

		key := node.Environment + "|" + res.kind + "/" + res.name
		f.consumers[key] = append(f.consumers[key], reviewConsumer{scoped: res.scoped, nodeID: id})
	}
}

// gapApplies says whether a gap limits the selection: the environment agrees and the resource that holds the
// reference is one that the selector can select. A gap whose resource has no node limits every selection.
func (f *ReviewFacts) gapApplies(gap *observation.CoverageGap, kind impact.Kind, sel *impact.Selector) bool {
	if !gapInEnvironment(gap, sel.Environment) {
		return false
	}

	if kind != impact.KindImplementation && !gapMatchesReference(gap, sel.Match["reference"]) {
		return false
	}

	consumers := f.consumers[gap.Environment+"|"+gap.Workload]
	if len(consumers) == 0 {
		return true
	}

	return slices.ContainsFunc(consumers, func(c reviewConsumer) bool { return f.consumerSelected(c, sel) })
}

// gapInEnvironment says whether the environment of a gap agrees with the selected one; either may be unstated.
func gapInEnvironment(gap *observation.CoverageGap, environment string) bool {
	return environment == "" || gap.Environment == "" || gap.Environment == environment
}

// consumerSelected says whether the selector can select the resource that holds a reference: by its identity and by
// the CALM nodes that the bindings map its observed node to.
func (f *ReviewFacts) consumerSelected(c reviewConsumer, sel *impact.Selector) bool {
	if !c.named(sel.Identities) {
		return false
	}

	return len(sel.CalmNodes) == 0 || intersects(f.bindingSet.ByObserved[c.nodeID], sel.CalmNodes)
}

// named says whether no identity is selected, or one names the resource or an item that belongs to it
// (`<resource>#...`).
func (c reviewConsumer) named(identities []string) bool {
	if len(identities) == 0 {
		return true
	}

	return slices.ContainsFunc(identities, func(id string) bool { return id == c.scoped || strings.HasPrefix(id, c.scoped+"#") })
}

// gapMatchesReference says whether a `reference` facet selection can name the target of the gap: the gap knows the
// kind and the name of the target but not its namespace.
func gapMatchesReference(gap *observation.CoverageGap, references []string) bool {
	if len(references) == 0 {
		return true
	}

	return slices.ContainsFunc(references, func(ref string) bool {
		return strings.Contains(ref, "/"+gap.TargetKind+"/") && strings.HasSuffix(ref, "/"+gap.Target)
	})
}
