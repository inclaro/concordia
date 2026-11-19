package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/testutil"
)

// A20: a decorator with the CALM 1.3 fields `$schema` and `target-type` is valid
// under a 1.3 declaration, CO-INT-008 under 1.2, and CO-INT-003 under 1.4.

const decoratorFixture = "../../test/fixtures/calm/decorator-1.3-fields-under-1.2.json"

func decoratorRelease(t *testing.T, release string) (dir string) {
	t.Helper()

	arch := strings.Replace(chkArch(), "release/1.2/meta/calm.json", "release/"+release+"/meta/calm.json", 1)
	dir = t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{
		"concordia.yaml":                   chkWorkspaceText(""),
		"architecture/a.calm.json":         arch,
		"bindings/concordia.bindings.yaml": chkBindings,
		"policies/demo.policy.yaml":        chkPolicy,
		"decorators/d.json":                readFixture(t),
	})

	return dir
}

func TestDecoratorReleaseA20(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		release string
		code    string // "" = no diagnostic
	}{
		{"1.2", "CO-INT-008"},
		{"1.3", ""},
		{"1.4", "CO-INT-003"},
	} {
		t.Run("model validate "+tc.release, func(t *testing.T) {
			t.Parallel()

			dir := decoratorRelease(t, tc.release)

			res := runIn(t, dir, "model", "validate", "--decorator", "decorators/d.json")
			if tc.code == "" {
				if res.Code != 0 {
					t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
				}

				return
			}

			if res.Code != 2 || !strings.Contains(res.Stdout+res.Stderr, tc.code) {
				t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
			}
		})

		t.Run("check "+tc.release, func(t *testing.T) {
			t.Parallel()

			s := newChkSetup(t, map[string]string{
				"concordia.yaml":           strings.Replace(chkWorkspaceText(""), "architecture: {source: architecture/a.calm.json}", "architecture: {source: architecture/a.calm.json, decorators: [decorators/d.json]}", 1),
				"architecture/a.calm.json": strings.Replace(chkArch(), "release/1.2/meta/calm.json", "release/"+tc.release+"/meta/calm.json", 1),
				"decorators/d.json":        readFixture(t),
			}, nil)

			res := s.run(t, "check", "--no-color")
			if tc.code == "" {
				if res.Code == 2 {
					t.Fatalf("exit code = 2, stdout = %q, stderr = %q", res.Stdout, res.Stderr)
				}

				return
			}

			if res.Code != 2 || !strings.Contains(res.Stdout+res.Stderr, tc.code) {
				t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
			}
		})
	}
}

func readFixture(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(decoratorFixture)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}
