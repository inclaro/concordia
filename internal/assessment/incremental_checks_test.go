package assessment_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
)

// incrementalWantCounts stops the test when the cache report does not have the wanted hits and misses.
func incrementalWantCounts(t *testing.T, rep *assessment.IncrementalReport, hits, misses int) {
	t.Helper()

	if rep == nil {
		t.Fatal("the evaluation has no cache report")
	}

	if rep.Hits != hits || rep.Misses != misses {
		t.Fatalf("run = %+v, want %d hits and %d misses", rep, hits, misses)
	}
}

// incrementalRun is the cache report that a run is expected to give.
type incrementalRun struct {
	status       impact.CacheStatus
	hits, misses int
}

// incrementalWantRun stops the test when the cache report does not have the wanted status, hits, and misses.
func incrementalWantRun(t *testing.T, rep *assessment.IncrementalReport, want incrementalRun) {
	t.Helper()

	incrementalWantCounts(t, rep, want.hits, want.misses)

	if rep.Status != want.status {
		t.Fatalf("run = %+v, want the cache status %s", rep, want.status)
	}
}

// incrementalDropCacheNote removes from the tree of an evaluation the diagnostics that name the cache, and the
// list itself when nothing else is left.
func incrementalDropCacheNote(tree map[string]any) {
	list, ok := tree["diagnostics"].([]any)
	if !ok {
		delete(tree, "diagnostics")

		return
	}

	list = slices.DeleteFunc(list, incrementalIsCacheNote)
	if len(list) == 0 {
		delete(tree, "diagnostics")

		return
	}

	tree["diagnostics"] = list
}

// incrementalIsCacheNote reports whether a decoded diagnostic is the CO-ASM-010 note.
func incrementalIsCacheNote(d any) bool {
	m, isMap := d.(map[string]any)

	return isMap && m["code"] == diag.ASM010
}

// incrementalDamages are the ways to damage a cache file. Each one gets the path of the file.
func incrementalDamages() map[string]func(t *testing.T, path string) {
	return map[string]func(t *testing.T, path string){
		"not json": func(t *testing.T, path string) {
			t.Helper()
			writeFile(t, path, "{ not json")
		},
		"truncated": func(t *testing.T, path string) {
			t.Helper()
			truncate(t, path)
		},
		"changed value": incrementalChangeValue,
		"empty": func(t *testing.T, path string) {
			t.Helper()
			writeFile(t, path, "")
		},
	}
}

// incrementalChangeValue changes the value of one item in a cache file and keeps the file well formed.
func incrementalChangeValue(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, path, strings.Replace(string(data), "k8s:rolebinding/a", "k8s:rolebinding/b", 1))
}

// incrementalAssertCorruptFallback damages the cache file that a first run wrote and checks that the next run is a
// full evaluation with CO-ASM-010, equal to the full result, and that it repairs the file.
func incrementalAssertCorruptFallback(t *testing.T, hurt func(t *testing.T, path string)) {
	t.Helper()

	dir := t.TempDir()
	in := incrementalInput(t, dir)
	first := evaluate(t, in)

	hurt(t, filepath.Join(dir, strings.TrimPrefix(first.Incremental.Key, "sha256:")+".json"))

	ev := evaluate(t, in)
	incrementalWantRun(t, ev.Incremental, incrementalRun{impact.CacheCorrupt, 0, 2})

	if !hasCacheNote(ev) || ev.Incremental.Fallback == "" {
		t.Errorf("CO-ASM-010 missing: %+v", ev.Diagnostics)
	}

	if d := firstDifference(semantic(t, ev), full(t, in)); d != "" {
		t.Errorf("the result after a corrupt cache differs from the full result: %s", d)
	}

	// The run repaired the file: the next one hits.
	if again := evaluate(t, in); again.Incremental.Status != impact.CacheLoaded || again.Incremental.Hits != 2 {
		t.Errorf("run after repair = %+v", again.Incremental)
	}
}
