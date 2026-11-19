package deployment_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/observation"
)

// extractWant reports a value of an extraction that is not the expected one.
func extractWant[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// extractWantList reports a list of an extraction that is not the expected one; a nil list and an empty one are the same.
func extractWantList[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// extractRequireLen stops a test whose list has not the expected length.
func extractRequireLen[T any](t *testing.T, what string, list []T, want int) {
	t.Helper()

	if len(list) != want {
		t.Fatalf("%s = %+v, want %d items", what, list, want)
	}
}

// extractWantImage reports an image reference that has not the expected repository and tag.
func extractWantImage(t *testing.T, image *observation.ImageRef, repository, tag string) {
	t.Helper()

	extractWant(t, "image repository", image.Repository, repository)
	extractWant(t, "image tag", image.Tag, tag)
}

// extractWantBase reports a base image that is not the expected stage and repository.
func extractWantBase(t *testing.T, base *deployment.BaseImage, stage, repository string) {
	t.Helper()

	extractWant(t, "base stage", base.Stage, stage)
	extractWant(t, "base repository of stage "+stage, base.Image.Repository, repository)
}

// extractHelmTemplate gives the workload of the Helm template of the recording, or nil.
func extractHelmTemplate(view *deployment.View) *deployment.Workload {
	for i := range view.Workloads {
		if view.Workloads[i].Locations[0].Path == "charts/api/templates/deployment.yaml" {
			return &view.Workloads[i]
		}
	}

	return nil
}

// extractActionsByTool indexes the actions of a view by tool.
func extractActionsByTool(view *deployment.View) map[string]deployment.Action {
	byTool := map[string]deployment.Action{}
	for _, action := range view.Actions {
		byTool[action.Tool] = action
	}

	return byTool
}

// extractWantDockerfileBases reports the bases of the multi-stage Dockerfile that are not its two stages.
func extractWantDockerfileBases(t *testing.T, def *deployment.BuildDefinition) {
	t.Helper()

	extractRequireLen(t, "bases", def.Bases, 2)
	extractWantBase(t, &def.Bases[0], "0", "golang")
	extractWantBase(t, &def.Bases[1], "1", "gcr.io/distroless/static")
}

// extractWantDockerfileCopies reports the copy sources of the multi-stage Dockerfile.
// `COPY --from=builder` names a stage: only the external image is a copy source.
func extractWantDockerfileCopies(t *testing.T, def *deployment.BuildDefinition) {
	t.Helper()

	extractRequireLen(t, "copies", def.Copies, 1)
	extractWantImage(t, &def.Copies[0].Image, "ghcr.io/example/healthcheck", "1.0.0")
}

// extractWantDockerfileCommands reports the build commands of the multi-stage Dockerfile.
func extractWantDockerfileCommands(t *testing.T, def *deployment.BuildDefinition) {
	t.Helper()

	extractRequireLen(t, "commands", def.Commands, 1)
	extractWant(t, "command location", def.Commands[0].Location, deployment.Location{Path: "scripts/build.sh", Line: 4})
	extractWant(t, "command tool", def.Commands[0].Tool, "docker-build")
}
