package deployment

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
)

// buildDefinitionStep matches the image repository of each container occurrence (tag ignored) to the `built-from`
// facts and lists the Dockerfiles as lineage candidates. It gives the candidates too, for the source step.
//
//   - One Dockerfile: a `candidate`, resolution `inferred`.
//   - Several Dockerfiles: all of them, resolution `ambiguous`, and the gap `ambiguous-build-definition`.
//   - None: the gap `coverage-incomplete` with the loss mode `build-link-needs-build-command`, and the repository
//     with lineage `unknown`. The trace never says that no Dockerfile builds the image.
//
// A repository whose every occurrence has verified lineage (`build-provenance` entities, lineage `verified`) needs no
// static link, so it adds neither the `unknown` entity nor the gap. The Dockerfile of a static link stays a candidate
// beside the verified entity: only the exact lineage claim makes a step `verified`.
//
// The step always carries the provenance limit: no verified link between the image and the Dockerfile.
func (b *traceBuilder) buildDefinitionStep(env Environment, occurrences []occurrence) ([]BuildDefinition, StepTrace) {
	step := StepTrace{
		Step: StepBuildDefinition, Role: observation.RoleInferredAssociation, Resolution: observation.ResolutionUnresolved,
		Entities: []Entity{}, Gaps: []Gap{},
		Limits: []Limit{{ID: LimitImageProvenance, Detail: "No verified link ties the image that the manifest names to the Dockerfile. The link comes from a build command that names the image repository, and the tag is ignored."}},
	}

	var candidates []BuildDefinition

	resolutions := []observation.Resolution{}

	for _, repository := range repositoriesOf(occurrences) {
		defs := b.view.BuildDefinitions(repository)
		candidates = append(candidates, defs...)

		proofs, verified := b.provenanceEntities(env, repository, occurrences, len(defs) > 0)
		step.Entities = append(step.Entities, proofs...)

		switch {
		case len(defs) == 0 && verified:
			// Every occurrence of the repository has exact lineage, so the missing static link is not a gap.
			resolutions = append(resolutions, observation.ResolutionExact)
		case len(defs) == 0:
			step.Entities = append(step.Entities, unlinkedEntity(repository, occurrences))
			step.Gaps = append(step.Gaps, b.noBuildDefinitionGap(env, repository))
			resolutions = append(resolutions, observation.ResolutionUnresolved)
		case len(defs) == 1:
			step.Entities = append(step.Entities, buildEntities(&defs[0], observation.ResolutionInferred)...)
			resolutions = append(resolutions, observation.ResolutionInferred)
		default:
			for i := range defs {
				step.Entities = append(step.Entities, buildEntities(&defs[i], observation.ResolutionAmbiguous)...)
			}

			step.Gaps = append(step.Gaps, ambiguousGap(repository, defs))
			resolutions = append(resolutions, observation.ResolutionAmbiguous)
		}

		step.Gaps = append(step.Gaps, baseImageGaps(defs)...)
	}

	step.Resolution = weakest(resolutions)

	return candidates, step
}

// weakest gives the resolution that limits a claim the most; no entity gives `unresolved`.
func weakest(resolutions []observation.Resolution) observation.Resolution {
	if len(resolutions) == 0 {
		return observation.ResolutionUnresolved
	}

	worst := resolutions[0]

	for _, r := range resolutions[1:] {
		if r.Rank() < worst.Rank() {
			worst = r
		}
	}

	return worst
}

func unlinkedEntity(repository string, occurrences []occurrence) Entity {
	var locations []Location

	for i := range occurrences {
		if !occurrences[i].Unresolved && occurrences[i].Image.Repository == repository {
			locations = append(locations, occurrences[i].Location)
		}
	}

	return Entity{
		Identity: repository, Kind: KindUnlinkedImage, Resolution: observation.ResolutionUnresolved, Lineage: LineageUnknown,
		Locations: sortedLocations(locations), Properties: props(map[string]string{propRepository: repository}),
	}
}

// noBuildDefinitionGap says why an image repository has no candidate. The Dockerfile set that is not complete adds
// its loss modes: a Dockerfile that the provider did not read could still build the image.
func (b *traceBuilder) noBuildDefinitionGap(env Environment, repository string) Gap {
	if rec := b.recordOf(string(observation.KindBuiltFrom), env.ID); rec != nil && rec.Status == observation.CoverageUnsupported {
		return Gap{Reason: ReasonUnsupportedFamily, Detail: "the provider build does not supply build links"}
	}

	modes := []string{observation.LossBuildLinkNeedsBuildCommand}
	detail := "no build command names the image repository " + repository

	if rec := b.worstProjectRecord(observation.FamilyDockerfileSet); rec != nil && rec.Status != observation.CoverageComplete {
		modes = append(modes, lossModeIDs(rec)...)
		detail += fmt.Sprintf("; the Dockerfile set is %s", rec.Status)

		if len(rec.Reasons) > 0 {
			detail += " (" + strings.Join(sortedStrings(rec.Reasons), "; ") + ")"
		}
	}

	return Gap{Reason: ReasonCoverageIncomplete, LossModes: sortedStrings(modes), Detail: detail}
}

func ambiguousGap(repository string, defs []BuildDefinition) Gap {
	paths := make([]string, 0, len(defs))
	for i := range defs {
		paths = append(paths, defs[i].Path)
	}

	return Gap{
		Reason: ReasonAmbiguousBuildDefinition,
		Detail: fmt.Sprintf("%d Dockerfiles are linked to %s (%s); all of them are candidates", len(defs), repository, strings.Join(sortedStrings(paths), ", ")),
	}
}

// baseImageGaps gives a gap for each base image that is a build argument with no default: the image of that stage
// is not known.
func baseImageGaps(defs []BuildDefinition) []Gap {
	var gaps []Gap

	for i := range defs {
		def := &defs[i]

		for j := range def.Bases {
			if base := &def.Bases[j]; base.Unresolved {
				gaps = append(gaps, unresolvedStageGap(def, "base image", base.Stage, base.Location))
			}
		}

		for j := range def.Copies {
			if source := &def.Copies[j]; source.Unresolved {
				gaps = append(gaps, unresolvedStageGap(def, "external copy source", source.Stage, source.Location))
			}
		}
	}

	return gaps
}

func unresolvedStageGap(def *BuildDefinition, what, stage string, at Location) Gap {
	return Gap{
		Reason: ReasonCoverageIncomplete, LossModes: []string{observation.LossUnrenderedTemplate},
		Detail: fmt.Sprintf("the %s of stage %s in %s (%s) comes from a build argument: no image is shown", what, stage, def.Path, locationText([]Location{at})),
	}
}

// buildEntities gives the entities of one build definition: the Dockerfile, its base images with their stage
// indexes, and its external copy sources. A stage that `COPY --from` names is not an external source.
func buildEntities(def *BuildDefinition, resolution observation.Resolution) []Entity {
	locations := []Location{{Path: def.Path, Line: def.Line}}
	for _, command := range def.Commands {
		if command.Location.Path != "" {
			locations = append(locations, command.Location)
		}
	}

	tool := ""
	if len(def.Commands) > 0 {
		tool = def.Commands[0].Tool
	}

	out := make([]Entity, 1, 1+len(def.Bases)+len(def.Copies))
	out[0] = Entity{
		Identity: def.Path, Kind: KindDockerfile, Resolution: resolution, Lineage: LineageCandidate,
		Locations: slices.Compact(locations), Properties: props(map[string]string{propRepository: def.Repository, propTool: tool}),
	}

	for i := range def.Bases {
		base := &def.Bases[i]
		out = append(out, stageEntity(def, KindBaseImage, base.Stage, &base.Image, base.Location, base.Unresolved))
	}

	for i := range def.Copies {
		source := &def.Copies[i]
		out = append(out, stageEntity(def, KindExternalCopy, source.Stage, &source.Image, source.Location, source.Unresolved))
	}

	return out
}

func stageEntity(def *BuildDefinition, kind, stage string, image *observation.ImageRef, at Location, unresolved bool) Entity {
	entity := Entity{
		Identity: fmt.Sprintf("%s#stage:%s", def.Path, stage), Kind: kind, Resolution: observation.ResolutionExact,
		Locations: sortedLocations([]Location{at}), Properties: props(map[string]string{propStage: stage}),
	}

	if unresolved {
		entity.Resolution = observation.ResolutionUnresolved

		return entity
	}

	entity.Identity = fmt.Sprintf("%s#stage:%s:%s", def.Path, stage, image.Repository)
	entity.Properties = props(map[string]string{
		propStage: stage, propRepository: image.Repository, propRegistry: image.Registry, propTag: image.Tag, propDigest: image.Digest, propReference: image.Raw,
	})

	return entity
}

// sourceStep lists the repository paths bound to the subject (`sourceRoot` and `package` bindings), the manifests of
// the workloads, and the Dockerfile candidates. It reads no `COPY` source: it shows the binding, not a proof of the
// build context. It always carries the provenance limit (DEP-04).
func (b *traceBuilder) sourceStep(subject string, workloads []*Workload, builds []BuildDefinition) StepTrace {
	step := StepTrace{
		Step: StepSourceEvidence, Role: observation.RoleInferredAssociation, Resolution: observation.ResolutionUnresolved,
		Entities: []Entity{}, Gaps: []Gap{},
		Limits: []Limit{{ID: LimitSourceProvenance, Detail: "No provenance links the image that the manifest names to these source paths. The paths are the ones bound to the service."}},
	}

	paths := b.sourcePathsOf(subject)
	step.Entities = append(step.Entities, paths...)

	if len(paths) > 0 {
		step.Resolution = observation.ResolutionExplicitlyBound
	} else {
		step.Gaps = append(step.Gaps, Gap{Reason: ReasonBindingGap, Detail: "no sourceRoot or package binding names source paths for " + subject})
	}

	for _, workload := range workloads {
		step.Entities = append(step.Entities, Entity{
			Identity: workload.Identity, Kind: KindManifest, Locations: slices.Clone(workload.Locations),
			Properties: props(map[string]string{propKind: workload.Kind, propName: workload.Name}),
		})
	}

	for i := range builds {
		step.Entities = append(step.Entities, Entity{
			Identity: builds[i].Path, Kind: KindDockerfile, Locations: sortedLocations([]Location{{Path: builds[i].Path, Line: builds[i].Line}}),
			Properties: props(map[string]string{propRepository: builds[i].Repository}),
		})
	}

	step.Entities = uniqueEntities(step.Entities)

	return step
}

// sourcePathsOf gives one entity for each active `sourceRoot` and `package` match of the bindings of the subject.
func (b *traceBuilder) sourcePathsOf(subject string) []Entity {
	var out []Entity

	for i := range b.input.Bindings {
		bound := &b.input.Bindings[i]
		if bound.ArchitectureRef != subject || !isActive(bound) {
			continue
		}

		for j := range bound.Match {
			match := &bound.Match[j]
			if match.Kind != binding.MatchSourceRoot && match.Kind != binding.MatchPackage {
				continue
			}

			out = append(out, Entity{
				Identity: match.Value, Kind: KindSourcePath, Resolution: observation.ResolutionExplicitlyBound,
				Locations: sourceLocation(match), Properties: props(map[string]string{propBindingKind: string(match.Kind)}), Bindings: []string{bound.ID},
			})
		}
	}

	slices.SortFunc(out, func(a, c Entity) int {
		return canonjson.CompareUTF16(a.Identity, c.Identity)
	})

	return out
}

// sourceLocation gives the place of a `sourceRoot` path: the path pattern itself. A package label is no path.
func sourceLocation(match *binding.Match) []Location {
	if match.Kind == binding.MatchSourceRoot && match.Value != "" {
		return []Location{{Path: match.Value}}
	}

	return []Location{}
}

// unbound gives a workload that no trace subject owns, with its image references and the build definitions of
// their repositories. Nothing attaches it to a service.
func (b *traceBuilder) unbound(_ Environment, workload *Workload) UnboundWorkload {
	out := UnboundWorkload{Workload: workloadEntity(workload), Images: []Entity{}, BuildDefinitions: []Entity{}}
	out.Workload.Resolution = observation.ResolutionUnbound
	out.Workload.Bindings = nil

	repositories := map[string]struct{}{}

	for i := range workload.Occurrences {
		occ := &workload.Occurrences[i]
		if occ.Unresolved {
			continue
		}

		repositories[occ.Image.Repository] = struct{}{}
		out.Images = append(out.Images, b.imageEntity(workload, occ))
	}

	for repository := range repositories {
		defs := b.view.BuildDefinitions(repository)
		for i := range defs {
			out.BuildDefinitions = append(out.BuildDefinitions, buildEntities(&defs[i], observation.ResolutionInferred)[0])
		}
	}

	sortEntities(out.Images)
	sortEntities(out.BuildDefinitions)

	return out
}
