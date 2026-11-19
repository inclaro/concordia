package deployment

import "github.com/inclaro/concordia/internal/observation"

// OccurrenceKey names one container of a workload in a declared environment, the key that the trace and the rendered
// evidence share: the rendered evidence and the manifest facts both name the environment, the workload kind,
// namespace, and name, and the container.
type OccurrenceKey struct {
	Environment string
	Kind        string
	Namespace   string
	Name        string
	Container   string
}

// BuildLineage is the state of the image-to-build step for one container occurrence. Status is `verified` only when
// the exact lineage claim is `pass` for the occurrence (spec artifact-provenance, "Static build links stay lineage
// candidates"); then the other fields name what the claim rests on.
type BuildLineage struct {
	Status Lineage
	// Digest is the immutable digest that the provenance names.
	Digest string
	// Builder is the builder of the provenance statement.
	Builder string
	// SourceRepository is the id of the declared source repository that the statement matched.
	SourceRepository string
	// TrustContract is the digest of the trust contract that the claim was reached under.
	TrustContract string
	// Statement is the workspace-relative path of the provenance statement.
	Statement string
}

// LineageFunc gives the lineage of a container occurrence. hasStaticLink says whether a build command links the image
// repository of the occurrence to a Dockerfile: that link alone is `candidate`, never `verified`. An occurrence the
// assurance evidence does not know has the status `candidate` with a static link and `unknown` without.
type LineageFunc func(key *OccurrenceKey, hasStaticLink bool) BuildLineage

// lineageOf gives the lineage of an occurrence of the trace in an environment.
func (b *traceBuilder) lineageOf(env Environment, occ *occurrence, hasStaticLink bool) BuildLineage {
	if b.input.Lineage == nil {
		return BuildLineage{Status: staticLineage(hasStaticLink)}
	}

	key := OccurrenceKey{
		Environment: env.ID, Kind: occ.workload.Kind, Namespace: occ.workload.Namespace, Name: occ.workload.Name, Container: occ.Container,
	}

	return b.input.Lineage(&key, hasStaticLink)
}

func staticLineage(hasStaticLink bool) Lineage {
	if hasStaticLink {
		return LineageCandidate
	}

	return LineageUnknown
}

// provenanceEntities gives one `build-provenance` entity for each occurrence of the repository whose exact lineage
// is verified. all is true when every resolved occurrence of the repository is verified: the repository then needs no
// static link to be traced to a build.
func (b *traceBuilder) provenanceEntities(env Environment, repository string, occurrences []occurrence, hasStaticLink bool) (entities []Entity, all bool) {
	count := 0

	for i := range occurrences {
		occ := &occurrences[i]
		if occ.Unresolved || occ.Image.Repository != repository {
			continue
		}

		count++

		lineage := b.lineageOf(env, occ, hasStaticLink)
		if lineage.Status != LineageVerified {
			continue
		}

		identity := occ.workload.Identity
		if occ.Container != "" {
			identity += "#" + occ.Container
		}

		entities = append(entities, Entity{
			Identity: identity, Kind: KindBuildProvenance, Resolution: observation.ResolutionExact, Lineage: LineageVerified,
			Locations: []Location{{Path: lineage.Statement}},
			Properties: props(map[string]string{
				propContainer: occ.Container, propRepository: repository, propDigest: lineage.Digest, propBuilder: lineage.Builder,
				propSourceRepository: lineage.SourceRepository, propTrustContract: lineage.TrustContract,
			}),
		})
	}

	return entities, count > 0 && len(entities) == count
}
