package deployment

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/observation"
)

// imageStep lists the container image references of the workloads. A reference with no immutable digest has the
// gap `mutable-tag`; a container that the provider lost has a `merged-occurrences` gap that names it.
func (b *traceBuilder) imageStep(env Environment, workloads []*Workload) StepTrace {
	step := StepTrace{
		Step: StepContainerImage, Role: observation.RoleParsedSourceFact, Resolution: observation.ResolutionUnresolved,
		Entities: []Entity{}, Gaps: []Gap{},
		Limits: []Limit{{ID: LimitMergedOccurrences, Detail: "The provider keeps one image reference for each workload and image repository, so containers that share a repository may be merged without a signal."}},
	}

	record := b.recordOf(observation.FamilyContainerOccurrenceVersions, env.ID)
	own := map[string]map[string]struct{}{}

	for _, workload := range workloads {
		step.Gaps = append(step.Gaps, b.imageGaps(workload, record)...)

		for i := range workload.Occurrences {
			occ := &workload.Occurrences[i]
			if occ.Unresolved {
				continue
			}

			step.Entities = append(step.Entities, b.imageEntity(workload, occ))

			repository := occ.Image.Repository
			if own[repository] == nil {
				own[repository] = map[string]struct{}{}
			}

			own[repository][workload.Identity] = struct{}{}

			if occ.Image.Mutable() {
				step.Gaps = append(step.Gaps, mutableGap(occ))
			}
		}
	}

	if len(step.Entities) > 0 {
		step.Resolution = observation.ResolutionExact
	}

	if limit, ok := b.otherConsumers(own); ok {
		step.Limits = append(step.Limits, limit)
	}

	sortEntities(step.Entities)

	return step
}

func (b *traceBuilder) imageEntity(workload *Workload, occ *ContainerOccurrence) Entity {
	identity := workload.Identity
	if occ.Container != "" {
		identity += "#" + occ.Container
	}

	return Entity{
		Identity: identity, Kind: KindContainerImage, Resolution: observation.ResolutionExact,
		Locations: sortedLocations([]Location{occ.Location}), Properties: imageProperties(&occ.Image, occ.Container),
		Consumers: b.view.Consumers(occ.Image.Repository),
	}
}

func imageProperties(ref *observation.ImageRef, container string) map[string]string {
	return props(map[string]string{
		propContainer: container, propRepository: ref.Repository, propRegistry: ref.Registry, propTag: ref.Tag, propDigest: ref.Digest, propReference: ref.Raw,
	})
}

func mutableGap(occ *ContainerOccurrence) Gap {
	name := containerName(occ.Container)

	return Gap{
		Reason: ReasonMutableTag,
		Detail: fmt.Sprintf("%s uses %s: the tag can point to other content later, and no immutable digest is known", name, occ.Image.Raw),
	}
}

// imageGaps gives the gaps of the image references of one workload: a container that the provider lost, a reference
// that is a template, and a workload with no reference at all.
func (b *traceBuilder) imageGaps(workload *Workload, record *observation.PredicateCoverage) []Gap {
	var gaps []Gap

	merged := mergedContainers(workload, record)
	for i := range merged {
		name := containerName(merged[i].Container)

		gaps = append(gaps, Gap{
			Reason: ReasonCoverageIncomplete, LossModes: []string{observation.LossMergedOccurrences},
			Detail: fmt.Sprintf("%s of %s/%s (%s) has no image occurrence: the provider keeps one reference for each workload and repository", name, workload.Kind, workload.Name, locationText(workload.Locations)),
		})
	}

	for i := range workload.Occurrences {
		if occ := &workload.Occurrences[i]; occ.Unresolved {
			gaps = append(gaps, Gap{
				Reason: ReasonCoverageIncomplete, LossModes: []string{observation.LossUnrenderedTemplate},
				Detail: fmt.Sprintf("the image reference of %s is a template: no image is shown", containerName(occ.Container)),
			})
		}
	}

	if len(workload.Occurrences) == 0 && len(gaps) == 0 {
		gaps = append(gaps, noOccurrenceGap(workload, record))
	}

	return gaps
}

func containerName(name string) string {
	if name == "" {
		return "a container"
	}

	return "container " + name
}

func noOccurrenceGap(workload *Workload, record *observation.PredicateCoverage) Gap {
	what := fmt.Sprintf("the image references of %s/%s", workload.Kind, workload.Name)

	if gap := coverageGap(record, what); gap != nil {
		return *gap
	}

	return Gap{Reason: ReasonAbsent, Detail: what + " are complete and show no container image"}
}

// mergedContainers gives the `merged-occurrences` gaps of the coverage record that name a container of the workload.
func mergedContainers(workload *Workload, record *observation.PredicateCoverage) []observation.CoverageGap {
	if record == nil {
		return nil
	}

	var out []observation.CoverageGap

	for _, mode := range record.LossModes {
		if mode.ID == observation.LossMergedOccurrences {
			out = append(out, gapsOfWorkload(workload, mode.Gaps)...)
		}
	}

	return out
}

// gapsOfWorkload gives the coverage gaps that name the workload and one of its manifests.
func gapsOfWorkload(workload *Workload, gaps []observation.CoverageGap) []observation.CoverageGap {
	var out []observation.CoverageGap

	name := workload.Kind + "/" + workload.Name

	for i := range gaps {
		gap := &gaps[i]
		if gap.Workload == name && slices.ContainsFunc(workload.Locations, func(l Location) bool { return l.Path == gap.Path }) {
			out = append(out, *gap)
		}
	}

	return out
}

// otherConsumers gives the limit for image repositories that have consumers outside this trace.
func (b *traceBuilder) otherConsumers(own map[string]map[string]struct{}) (Limit, bool) {
	var repositories []string

	for repository, workloads := range own {
		if b.view.Consumers(repository) > len(workloads) {
			repositories = append(repositories, repository)
		}
	}

	if len(repositories) == 0 {
		return Limit{}, false
	}

	return Limit{
		ID:     LimitOtherConsumers,
		Detail: "Other workloads use the same image repository (" + strings.Join(sortedStrings(repositories), ", ") + "). They belong to other subjects or environments, and this trace does not say that they agree with the service.",
	}, true
}
