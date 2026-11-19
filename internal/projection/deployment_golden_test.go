package projection_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/projection"
)

// TestDefaultDeploymentIsTheAsBuiltOutput: a workspace that declares no environment renders the one `Default`
// deployment environment, byte for byte as the product did before the environments (VIEW-07 "As-built workspace").
// The golden was recorded with the as-built generator.
func TestDefaultDeploymentIsTheAsBuiltOutput(t *testing.T) {
	t.Parallel()

	got, err := projection.ToStructurizrDSL(deploymentModel(), nil, projection.DSLOptions{})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("testdata", "deployment-default.dsl")

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if got != string(want) {
		t.Errorf("Default deployment DSL differs from %s:\n%s", path, got)
	}
}
