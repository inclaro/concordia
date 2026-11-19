package observation_test

import (
	"os"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

// realCbmEnv names the CBM executable for TestProcessClientRealCBM. The test reads only (`list_projects` and
// `index_status` of a project that does not exist); it never indexes.
const realCbmEnv = "CONCORDIA_REAL_CBM"

func TestProcessClientRealCBM(t *testing.T) {
	exe := os.Getenv(realCbmEnv)
	if exe == "" {
		t.Skipf("set %s to a codebase-memory-mcp executable to run this test", realCbmEnv)
	}

	c := observation.NewProcessClient(observation.ProcessOptions{Executable: exe, Timeout: testTimeout, IndexTimeout: testTimeout})

	t.Cleanup(func() { _ = c.Close() })

	if _, err := observation.CallAs[struct {
		Total int `json:"total"`
	}](t.Context(), c, "list_projects", map[string]any{observation.ArgFormat: observation.FormatJSON, "limit": 1}); err != nil {
		t.Fatalf("list_projects: %v", err)
	}

	_, err := c.Call(t.Context(), "index_status", map[string]any{"project": "concordia-no-such-project", observation.ArgFormat: observation.FormatJSON})

	de := diagOf(t, err, "CO-OBS-002", diag.AdapterFailure)
	requireMessageContains(t, de, "concordia-no-such-project")
}
