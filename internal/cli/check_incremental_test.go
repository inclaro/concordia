package cli_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/impact"
)

// No golden file holds the help text of the commands (test/golden has the recorded results only), so the help of
// `check --incremental` is asserted here.
func TestCheckHelpDocumentsTheIncrementalFlagAndTheCacheDirectory(t *testing.T) {
	t.Parallel()

	res := runCLI(t, "check", "--help")
	if res.Code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	for _, want := range []string{"--incremental", impact.CacheDir} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("check --help does not show %q:\n%s", want, res.Stdout)
		}
	}

	if strings.Contains(res.Stdout, "--incremental=") {
		t.Error("--incremental takes no value")
	}
}
