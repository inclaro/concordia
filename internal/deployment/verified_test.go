package deployment_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
)

// verifiedFor gives a LineageFunc that is `verified` for the container and says what it was asked: a static link
// alone is never verified.
func verifiedFor(container string, asked *[]deployment.OccurrenceKey) deployment.LineageFunc {
	return func(key *deployment.OccurrenceKey, hasStaticLink bool) deployment.BuildLineage {
		*asked = append(*asked, *key)

		if key.Container == container {
			return deployment.BuildLineage{
				Status: deployment.LineageVerified, Digest: "sha256:aaa", Builder: "builder", SourceRepository: "shop",
				TrustContract: "sha256:ccc", Statement: "build/provenance.json",
			}
		}

		if hasStaticLink {
			return deployment.BuildLineage{Status: deployment.LineageCandidate}
		}

		return deployment.BuildLineage{Status: deployment.LineageUnknown}
	}
}

// verifiedWantProof reports a provenance entity that is not the verified answer of verifiedFor.
func verifiedWantProof(t *testing.T, proof *deployment.Entity) {
	t.Helper()

	lineageWant(t, "proof lineage", proof.Lineage, deployment.LineageVerified)
	lineageWant(t, "proof resolution", proof.Resolution, observation.ResolutionExact)
	lineageWant(t, "digest", proof.Properties["digest"], "sha256:aaa")
	lineageWant(t, "builder", proof.Properties["builder"], "builder")
	lineageWant(t, "source repository", proof.Properties["sourceRepository"], "shop")
	lineageWant(t, "trust contract", proof.Properties["trustContract"], "sha256:ccc")
	lineageRequireCount(t, "proof locations", proof.Locations, 1)
	lineageWant(t, "proof path", proof.Locations[0].Path, "build/provenance.json")
}

// verifiedWantCandidateDockerfile reports a build step whose Dockerfile is not the only candidate.
func verifiedWantCandidateDockerfile(t *testing.T, build *deployment.StepTrace) {
	t.Helper()

	dockerfiles := entitiesOf(build, deployment.KindDockerfile)
	lineageRequireCount(t, "dockerfiles", dockerfiles, 1)
	lineageWant(t, "dockerfile lineage", dockerfiles[0].Lineage, deployment.LineageCandidate)
}

// verifiedWantAskedKey reports a lineage question that does not name the environment, the workload, and the
// container: what the rendered evidence also names.
func verifiedWantAskedKey(t *testing.T, key *deployment.OccurrenceKey) {
	t.Helper()

	lineageWant(t, "asked environment", key.Environment, "production")
	lineageWant(t, "asked container", key.Container, "app")

	if key.Kind == "" || key.Name == "" {
		t.Errorf("asked = %+v, want the kind and the name of the workload", key)
	}
}

func TestVerifiedLineageAddsAProvenanceEntityAndKeepsTheDockerfileACandidate(t *testing.T) {
	t.Parallel()

	_, in := a10()

	var asked []deployment.OccurrenceKey

	in.Lineage = verifiedFor("app", &asked)

	build := stepOf(t, deployment.BuildTrace(in).Subject("orders-service", "production"), deployment.StepBuildDefinition)

	proofs := entitiesOf(build, deployment.KindBuildProvenance)
	lineageRequireCount(t, "provenance entities", proofs, 1)
	verifiedWantProof(t, &proofs[0])
	verifiedWantCandidateDockerfile(t, build)

	lineageRequireCount(t, "asked", asked, 1)
	verifiedWantAskedKey(t, &asked[0])
}

func TestNoAssuranceEvidenceLeavesTheStepAsItWas(t *testing.T) {
	t.Parallel()

	_, in := a10()

	build := stepOf(t, deployment.BuildTrace(in).Subject("orders-service", "production"), deployment.StepBuildDefinition)
	lineageWant(t, "provenance entities", len(entitiesOf(build, deployment.KindBuildProvenance)), 0)
}

// An occurrence that is not verified adds no provenance entity: a candidate or unknown answer changes nothing.
func TestAnOccurrenceThatIsNotVerifiedAddsNothing(t *testing.T) {
	t.Parallel()

	_, in := a10()

	var asked []deployment.OccurrenceKey

	in.Lineage = verifiedFor("other-container", &asked)

	sub := deployment.BuildTrace(in).Subject("orders-service", "production")
	build := stepOf(t, sub, deployment.StepBuildDefinition)

	lineageWant(t, "provenance entities", len(entitiesOf(build, deployment.KindBuildProvenance)), 0)

	if len(asked) == 0 {
		t.Errorf("asked = %+v, want a question", asked)
	}

	verifiedWantCandidateDockerfile(t, build)
}

// An image with no build command is `unknown` and a gap; when every occurrence of it has exact lineage, the missing
// static link is no gap.
func TestVerifiedLineageNeedsNoStaticLink(t *testing.T) {
	t.Parallel()

	w, in := a10()
	w.edges = slices.DeleteFunc(w.edges, func(e *observation.EvidenceEdge) bool { return e.Kind == observation.KindBuiltFrom })
	in.Evidence.Edges = w.edges

	unlinked := stepOf(t, deployment.BuildTrace(in).Subject("orders-service", "production"), deployment.StepBuildDefinition)
	lineageRequireCount(t, "unlinked images without lineage", entitiesOf(unlinked, deployment.KindUnlinkedImage), 1)
	lineageRequireCount(t, "gaps without lineage", unlinked.Gaps, 1)

	var asked []deployment.OccurrenceKey

	in.Lineage = verifiedFor("app", &asked)

	sub := deployment.BuildTrace(in).Subject("orders-service", "production")
	build := stepOf(t, sub, deployment.StepBuildDefinition)

	lineageWant(t, "provenance entities", len(entitiesOf(build, deployment.KindBuildProvenance)), 1)
	lineageWant(t, "unlinked images", len(entitiesOf(build, deployment.KindUnlinkedImage)), 0)
	lineageWant(t, "gaps", len(build.Gaps), 0)
	lineageWant(t, "resolution", build.Resolution, observation.ResolutionExact)
}
