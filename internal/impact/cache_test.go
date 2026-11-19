package impact_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/impact"
)

func testKey() impact.CacheKey {
	return impact.CacheKey{
		ContractVersion: impact.ContractVersion, NormalizationVersion: "2.1", CapabilityProfile: "cbm-0.12.1",
		PolicyDigest: "sha256:" + strings.Repeat("a", 64), AuthorityDigest: "sha256:" + strings.Repeat("b", 64),
		EvaluatorVersions: map[string]string{impact.EvaluatorNormalization: "2.1", "cbm": "0.12.1"}, AnchorSHA: "abc123",
	}
}

func raw(text string) json.RawMessage { return json.RawMessage(text) }

// The key members of design D15: every one of them gives another file.
func TestTheCacheKeyCoversEveryMemberOfD15(t *testing.T) {
	t.Parallel()

	base := testKey()
	seen := map[string]string{base.Digest(): "base"}

	members := map[string]func(k *impact.CacheKey){
		"contract version":      func(k *impact.CacheKey) { k.ContractVersion = "review-digest/2" },
		"normalization version": func(k *impact.CacheKey) { k.NormalizationVersion = "2.2" },
		"capability profile":    func(k *impact.CacheKey) { k.CapabilityProfile = "cbm-0.13.0" },
		"policy digest":         func(k *impact.CacheKey) { k.PolicyDigest = "sha256:" + strings.Repeat("c", 64) },
		"authority digest":      func(k *impact.CacheKey) { k.AuthorityDigest = "sha256:" + strings.Repeat("d", 64) },
		"evaluator versions":    func(k *impact.CacheKey) { k.EvaluatorVersions = map[string]string{"cbm": "0.13.0"} },
		"anchor SHA":            func(k *impact.CacheKey) { k.AnchorSHA = "def456" },
	}

	for name, change := range members {
		key := testKey()
		change(&key)

		got := key.Digest()
		if other, dup := seen[got]; dup {
			t.Errorf("%s gives the key of %s", name, other)
		}

		seen[got] = name
	}

	// The order of the evaluator map does not matter, and a nil map is the empty one.
	again := testKey()
	if again.Digest() != base.Digest() {
		t.Error("the key digest is not stable")
	}

	empty, none := base, base
	empty.EvaluatorVersions, none.EvaluatorVersions = map[string]string{}, nil

	if empty.Digest() != none.Digest() {
		t.Error("a nil evaluator map is another key than an empty one")
	}
}

// Scenario: Item digest unchanged gives a hit.
func TestACacheAnswersAnEqualFingerprintFromTheNextRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	key := testKey()

	cacheTestSaveFirstRun(t, dir, &key)

	second := impact.OpenCache(dir, &key)
	if second.Status() != impact.CacheLoaded || second.Detail() != "" {
		t.Fatalf("second run: %s %q", second.Status(), second.Detail())
	}

	cacheTestAssertLookups(t, second)

	if stats := second.Stats(); stats.Hits != 1 || stats.Misses != 2 {
		t.Errorf("stats = %+v, want 1 hit and 2 misses", stats)
	}

	second.NoteItems([]impact.Entry{{Kind: impact.KindPublicEntryPoint, Identity: "a", Digest: "sha256:2"}, {Kind: impact.KindRoleBinding, Identity: "b", Digest: "sha256:3"}})

	if stats := second.Stats(); stats.ChangedItems != 2 {
		t.Errorf("changed items = %d, want the modified and the new one", stats.ChangedItems)
	}
}

// cacheTestSaveFirstRun stores two results and one item digest from an empty cache.
func cacheTestSaveFirstRun(t *testing.T, dir string, key *impact.CacheKey) {
	t.Helper()

	first := impact.OpenCache(dir, key)
	if first.Status() != impact.CacheMissing || first.Detail() == "" {
		t.Fatalf("first run: %s %q", first.Status(), first.Detail())
	}

	if _, ok := first.Lookup("ASM-A@1", "fp1"); ok {
		t.Fatal("an empty cache answered")
	}

	first.Put("ASM-A@1", "fp1", raw(`{"n":1}`))
	first.Put("ASM-B@1", "fp2", raw(`{"n":2}`))
	first.NoteItems([]impact.Entry{{Kind: impact.KindPublicEntryPoint, Identity: "a", Digest: "sha256:1"}})

	if err := first.Save(); err != nil {
		t.Fatal(err)
	}
}

// cacheTestAssertLookups checks one hit and two misses against the cache of cacheTestSaveFirstRun.
func cacheTestAssertLookups(t *testing.T, cache *impact.Cache) {
	t.Helper()

	if got, ok := cache.Lookup("ASM-A@1", "fp1"); !ok || string(got) != `{"n":1}` {
		t.Errorf("equal fingerprint: %s, %v", got, ok)
	}

	if _, ok := cache.Lookup("ASM-B@1", "other"); ok {
		t.Error("a changed fingerprint was answered")
	}

	if _, ok := cache.Lookup("ASM-C@1", "fp3"); ok {
		t.Error("an assessment that was never stored was answered")
	}
}

// Scenario: Stale cache (policy digest change misses).
func TestAnotherKeyIsAnotherFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	key := testKey()

	run := impact.OpenCache(dir, &key)
	run.Put("ASM-A@1", "fp1", raw(`{}`))

	if err := run.Save(); err != nil {
		t.Fatal(err)
	}

	changed := testKey()
	changed.PolicyDigest = "sha256:" + strings.Repeat("9", 64)

	stale := impact.OpenCache(dir, &changed)
	if stale.Status() != impact.CacheMissing {
		t.Fatalf("status = %s", stale.Status())
	}

	if _, ok := stale.Lookup("ASM-A@1", "fp1"); ok {
		t.Error("a result of another policy digest was answered")
	}

	// The file of the first key stays valid for a run of that key.
	if back := impact.OpenCache(dir, &key); back.Status() != impact.CacheLoaded {
		t.Errorf("status of the first key = %s", back.Status())
	}
}

// Scenario: Corrupt cache falls back to full.
func TestADamagedFileIsCorruptAndGivesNoResult(t *testing.T) {
	t.Parallel()

	damage := map[string]func(text string) string{
		"not json":     func(string) string { return "{" },
		"empty":        func(string) string { return "" },
		"truncated":    func(text string) string { return text[:len(text)/2] },
		"changed byte": func(text string) string { return strings.Replace(text, `"n":1`, `"n":9`, 1) },
		"other format": func(text string) string { return strings.Replace(text, `"format":1`, `"format":2`, 1) },
		"unknown key":  func(text string) string { return strings.Replace(text, `"format":1`, `"format":1,"extra":true`, 1) },
		"other key":    func(text string) string { return strings.Replace(text, `"anchorSha":"abc123"`, `"anchorSha":"zzz"`, 1) },
	}

	for name, hurt := range damage {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			key := testKey()

			cacheTestSaveDamaged(t, dir, &key, hurt)

			next := impact.OpenCache(dir, &key)
			if next.Status() != impact.CacheCorrupt || next.Detail() == "" {
				t.Fatalf("status = %s, detail %q", next.Status(), next.Detail())
			}

			if _, ok := next.Lookup("ASM-A@1", "fp1"); ok {
				t.Error("a corrupt cache answered")
			}

			// The next save replaces the damaged file.
			if err := next.Save(); err != nil {
				t.Fatal(err)
			}

			if again := impact.OpenCache(dir, &key); again.Status() != impact.CacheLoaded {
				t.Errorf("status after the repair = %s (%s)", again.Status(), again.Detail())
			}
		})
	}
}

// cacheTestSaveDamaged saves one result and then rewrites the cache file through hurt.
func cacheTestSaveDamaged(t *testing.T, dir string, key *impact.CacheKey, hurt func(string) string) {
	t.Helper()

	run := impact.OpenCache(dir, key)
	run.Put("ASM-A@1", "fp1", raw(`{"n":1}`))

	if err := run.Save(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(run.Path())
	if err != nil {
		t.Fatal(err)
	}

	changed := hurt(string(data))
	if changed == string(data) {
		t.Fatal("the damage changed nothing")
	}

	if err := os.WriteFile(run.Path(), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAnUnreadableCacheFileIsCorrupt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	key := testKey()

	// A directory where the file should be.
	if err := os.MkdirAll(impact.OpenCache(dir, &key).Path(), 0o750); err != nil {
		t.Fatal(err)
	}

	if cache := impact.OpenCache(dir, &key); cache.Status() != impact.CacheCorrupt {
		t.Errorf("status = %s", cache.Status())
	}
}

func TestOldFilesOfOtherKeysAreRemoved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	start := time.Now().Add(-time.Hour)

	for i := range 12 {
		key := testKey()
		key.AnchorSHA = strings.Repeat("a", i+1)

		cache := impact.OpenCache(dir, &key)
		if err := cache.Save(); err != nil {
			t.Fatal(err)
		}

		if err := os.Chtimes(cache.Path(), start.Add(time.Duration(i)*time.Minute), start.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) > 9 {
		t.Errorf("%d files stay in the cache directory", len(entries))
	}

	// The file of the key that just saved is never the one that goes.
	key := testKey()
	key.AnchorSHA = "newest"
	cache := impact.OpenCache(dir, &key)

	if err := cache.Save(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, filepath.Base(cache.Path()))); err != nil {
		t.Errorf("the file of the saving key was removed: %v", err)
	}
}

func TestRememberedDigestsSurviveTheFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	key := testKey()
	contract := impact.V1()
	item := contract.NewItem(impact.KindPublicEntryPoint, "ing/api", impact.Facets{"host": {"a.example.com"}, "path": {"/"}}, nil, nil)

	first := impact.OpenCache(dir, &key)
	want := first.Digester(contract)(item, nil)

	if want != contract.Digest(item, nil) {
		t.Fatalf("the remembered digest %s differs from the contract digest %s", want, contract.Digest(item, nil))
	}

	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	calls := 0
	second := impact.OpenCache(dir, &key)
	got := second.Remember("never-used", func() string { calls++; return "x" })

	if got != "x" || calls != 1 {
		t.Fatalf("a new key is computed: %q after %d calls", got, calls)
	}

	// A change of a contract field is another key; a change of a facet that no field covers is not.
	moved := contract.NewItem(impact.KindPublicEntryPoint, "ing/api", impact.Facets{"host": {"b.example.com"}, "path": {"/"}}, nil, nil)
	if second.Digester(contract)(moved, nil) == want {
		t.Error("an item with another host has the digest of the first")
	}

	if second.Digester(contract)(item, nil) != want {
		t.Error("the digest of the first item changed")
	}

	// A file digest is part of the key.
	withFile := contract.NewItem(impact.KindPublicEntryPoint, "ing/api", impact.Facets{"host": {"a.example.com"}, "path": {"/"}}, []string{"src/a.ts"}, nil)
	one := second.Digester(contract)(withFile, []impact.RefDigest{{Role: impact.RefImplementation, Path: "src/a.ts", Digest: "sha256:1"}})
	two := second.Digester(contract)(withFile, []impact.RefDigest{{Role: impact.RefImplementation, Path: "src/a.ts", Digest: "sha256:2"}})

	if one == two {
		t.Error("a changed file digest kept the digest of the item")
	}
}
