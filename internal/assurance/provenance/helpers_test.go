package provenance_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/provenance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	goodBuilder   = "https://github.com/acme/shop/.github/workflows/build.yml@refs/heads/main"
	goodSourceURI = "git+https://github.com/acme/shop@refs/heads/main"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// config is the workspace assurance configuration of the shop: one producer for all three statement kinds, one
// trusted builder (declared without a Git reference), and one declared source repository.
func config() *workspace.Assurance {
	return &workspace.Assurance{
		Producers: []workspace.Producer{
			{
				ID: "ci-build", Claims: []string{
					workspace.ProducerClaimBuildProvenance, workspace.ProducerClaimDeploymentRecord, workspace.ProducerClaimWorkloadObservation,
				},
				Authentication: workspace.ProducerAuthentication{Method: workspace.AuthCosignKey, Key: "/keys/ci.pub"},
			},
			{
				ID: "checks", Claims: []string{workspace.ProducerClaimCheckResult},
				Authentication: workspace.ProducerAuthentication{Method: workspace.AuthCosignKey, Key: "/keys/checks.pub"},
			},
		},
		Provenance: &workspace.AssuranceProvenance{
			Builders:           []string{"https://github.com/acme/shop/.github/workflows/build.yml"},
			SourceRepositories: []string{"shop"},
			MaxAgeDays:         30,
		},
		Records: &workspace.AssuranceRecords{MaxAgeDays: 7},
	}
}

func obligation() assurance.Context {
	return assurance.Context{Now: now, Environment: "production"}
}

// verified is the recorded outcome of a successful cosign verification by the producer.
func verified(producer string) statement.Verification {
	return statement.Verification{Signature: statement.SignatureVerified, ProducerID: producer, Subject: statement.SubjectUnchecked}
}

func unsigned() statement.Verification {
	return statement.Verification{Signature: statement.SignatureNone, Subject: statement.SubjectUnchecked}
}

func parse(t *testing.T, path, subject, predicateType string, predicate any) statement.Loaded {
	t.Helper()

	subjects := []statement.Subject{}

	if alg, value, ok := cutDigest(subject); ok {
		subjects = append(subjects, statement.Subject{Name: "registry.example/shop", Digest: map[string]string{alg: value}})
	}

	data, err := json.Marshal(map[string]any{
		"_type": statement.StatementType, "subject": subjects, "predicateType": predicateType, "predicate": predicate,
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := statement.Parse(path, data)
	if err != nil {
		t.Fatalf("Parse(%s): %v", path, err)
	}

	return loaded
}

func cutDigest(text string) (alg, value string, ok bool) {
	d, ok := statement.ParseDigest(text)

	return d.Algorithm, d.Value, ok
}

// slsaPredicate is a SLSA provenance v1 predicate with one source dependency and one base image.
func slsaPredicate(builder, sourceURI string) map[string]any {
	return map[string]any{
		"buildDefinition": map[string]any{
			"buildType": "https://actions.github.io/buildtypes/workflow/v1",
			"resolvedDependencies": []map[string]any{
				{"uri": sourceURI, "digest": map[string]string{"gitCommit": "1111111111111111111111111111111111111111"}},
				{"uri": "pkg:oci/distroless-static?repository_url=gcr.io/distroless/static", "digest": map[string]string{"sha256": "cccc"}},
			},
		},
		"runDetails": map[string]any{
			"builder":  map[string]any{"id": builder},
			"metadata": map[string]any{"finishedOn": "2026-10-30T12:00:00Z"},
		},
	}
}

// slsa admits a provenance statement with the given verification.
func slsa(t *testing.T, path, subject, builder, sourceURI string, verification statement.Verification) provenance.Statement {
	t.Helper()

	loaded := parse(t, path, subject, statement.PredicateSLSAProvenance, slsaPredicate(builder, sourceURI))

	got, err := provenance.NewStatement(&loaded, verification, obligation(), config())
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// occurrence is a rendered container of the production shop workload.
func occurrence(t *testing.T, image string) render.ContainerOccurrence {
	t.Helper()

	ref, err := render.ParseImageRef(image)
	if err != nil {
		t.Fatal(err)
	}

	return render.ContainerOccurrence{
		Workload:  render.ResourceID{Environment: "production", Kind: "Deployment", Namespace: "shop", Name: "api"},
		ImageRef:  ref,
		Container: "api",
		Kind:      render.KindContainer,
		Immutable: ref.Immutable(),
	}
}
