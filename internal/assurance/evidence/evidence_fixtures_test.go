package evidence_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/digest"
)

// copyAs copies the fixture rel to dest below root.
func copyAs(t *testing.T, root, rel, dest string) {
	t.Helper()

	src := filepath.Join(fixtures(t), filepath.FromSlash(rel))

	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		sub, _ := filepath.Rel(src, path)
		target := filepath.Join(root, filepath.FromSlash(dest), sub)

		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// bare is an unsigned workload-observation statement.
func bare(t *testing.T, predicate map[string]any) []byte {
	t.Helper()

	return bareStatement(t, statement.PredicateWorkloadObservation, predicate, "aa")
}

// digestOf is the digest of a file.
func digestOf(t *testing.T, file string) string {
	t.Helper()

	sum, err := digest.OfFile(file)
	if err != nil {
		t.Fatal(err)
	}

	return sum
}

// known is the architecture that the scenarios may refer to.
func known() quality.Known {
	set := func(ids ...string) map[string]struct{} {
		out := map[string]struct{}{}
		for _, id := range ids {
			out[id] = struct{}{}
		}

		return out
	}

	return quality.Known{Elements: set("orders-db", "orders-service", "orders-calls-db"), Rules: set("boundary-rule", "layering-rule")}
}
