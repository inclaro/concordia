package impact

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
)

// CacheDir is the workspace-relative directory of the incremental cache (design D15). It is a derived, ignored
// directory and not a workspace output (design D24): `check --incremental` is the only command that writes it,
// and no run trusts a file in it without its key.
const CacheDir = ".concordia/cache/impact"

// CacheFormat is the version of the cache file layout. A file of another format is not usable.
const CacheFormat = 1

// maxCacheFiles is the number of cache files that stay in the directory: each key has a file, and the files of
// keys that no run uses any more are removed, oldest first.
const maxCacheFiles = 8

// decimal is the base of the lengths that Hasher writes.
const decimal = 10

// CacheKey is everything that makes a stored result valid for the current run (design D15). The key names the
// cache file, so a change of any member is a miss for everything that depends on it.
type CacheKey struct {
	// EvaluatorVersions are the versions of the evaluators that produced the evidence, with the normalization.
	EvaluatorVersions map[string]string `json:"evaluatorVersions"`
	// ContractVersion is the digest contract of the run.
	ContractVersion string `json:"contractVersion"`
	// NormalizationVersion is the version of the evidence normalization (EvaluatorNormalization).
	NormalizationVersion string `json:"normalizationVersion"`
	// CapabilityProfile is the id of the capability profile that supplied the evidence.
	CapabilityProfile string `json:"capabilityProfile"`
	// PolicyDigest is the digest of the policy set that selects the review rules.
	PolicyDigest string `json:"policyDigest"`
	// AuthorityDigest is the digest of the authority configuration that verification used.
	AuthorityDigest string `json:"authorityDigest"`
	// AnchorSHA is the revision of the trust anchor.
	AnchorSHA string `json:"anchorSha"`
}

// Digest gives the `sha256:` digest of the canonical JSON of the key. It is the name of the cache file.
func (k *CacheKey) Digest() string {
	key := *k
	if key.EvaluatorVersions == nil {
		key.EvaluatorVersions = map[string]string{}
	}

	return digest.OfBytes([]byte(canonjson.MustString(key)))
}

// CacheItem is the digest that a run saw for one item.
type CacheItem struct {
	Kind     Kind   `json:"kind"`
	Identity string `json:"identity"`
	Digest   string `json:"digest"`
}

// CacheDigest is a remembered item digest: Key names the inputs of the digest (the contract, the kind, the contract
// fields, and the digests of the files), Digest is the result.
type CacheDigest struct {
	Key    string `json:"key"`
	Digest string `json:"digest"`
}

// CacheEntry is a stored result of one assessment version. Fingerprint covers every input of the result that is not
// in the key, so a result is reused only when the fingerprint is equal. Result is opaque to this package.
type CacheEntry struct {
	Assessment  string          `json:"assessment"`
	Fingerprint string          `json:"fingerprint"`
	Result      json.RawMessage `json:"result"`
}

// cacheFile is the content of `<key digest hex>.json`. Digest covers every other member, so a damaged file is found
// even when it still parses.
type cacheFile struct {
	Key         CacheKey      `json:"key"`
	KeyDigest   string        `json:"keyDigest"`
	Digest      string        `json:"digest"`
	Items       []CacheItem   `json:"items"`
	Digests     []CacheDigest `json:"digests"`
	Assessments []CacheEntry  `json:"assessments"`
	Format      int           `json:"format"`
}

// CacheStatus says what opening the cache found.
type CacheStatus string

// Cache statuses.
const (
	// CacheLoaded: a file for the key was read and is usable.
	CacheLoaded CacheStatus = "loaded"
	// CacheMissing: no file exists for the key (the first run, or a run of another policy, authority, contract,
	// capability profile, evaluator, normalization, or anchor).
	CacheMissing CacheStatus = "missing"
	// CacheCorrupt: the file exists and cannot be used: unreadable, not JSON, another format, a key or digest that
	// does not match.
	CacheCorrupt CacheStatus = "corrupt"
)

// CacheStats are the counters of one run.
type CacheStats struct {
	// Hits are the results that were reused.
	Hits int `json:"hits"`
	// Misses are the results that were evaluated.
	Misses int `json:"misses"`
	// ChangedItems are the items whose digest differs from the stored one, or that are new or gone. Zero without a
	// stored file.
	ChangedItems int `json:"changedItems"`
}

// Cache is the incremental cache of one run: it reads the file of the key once, answers lookups, collects the
// results of the run, and writes the file. Methods are not safe for concurrent use.
type Cache struct {
	stored      map[string]CacheEntry
	storedItems map[string]string
	memo        map[string]string
	next        map[string]CacheEntry
	nextItems   map[string]string
	nextMemo    map[string]string
	key         CacheKey
	dir         string
	digest      string
	status      CacheStatus
	detail      string
	stats       CacheStats
}

// OpenCache reads the cache file of the key from dir (an OS path). It never fails: a file that cannot be used gives
// the status CacheCorrupt or CacheMissing and an empty cache, and the run is a full evaluation.
func OpenCache(dir string, key *CacheKey) *Cache {
	cache := &Cache{
		dir: dir, key: *key, digest: key.Digest(), stored: map[string]CacheEntry{}, storedItems: map[string]string{},
		memo: map[string]string{}, next: map[string]CacheEntry{}, nextItems: map[string]string{}, nextMemo: map[string]string{},
	}
	if cache.key.EvaluatorVersions == nil {
		cache.key.EvaluatorVersions = map[string]string{}
	}

	data, err := os.ReadFile(cache.path())

	switch {
	case errors.Is(err, fs.ErrNotExist):
		cache.status = CacheMissing
		cache.detail = fmt.Sprintf("no cache file exists for key %s: this is the first run, or the contract, normalization, "+
			"capability profile, policy, authority, evaluator versions, or trust anchor differ from the earlier runs", cache.digest)
	case err != nil:
		cache.corruptf("the cache file cannot be read: %v", err)
	default:
		cache.load(data)
	}

	return cache
}

// path is the file of the key: the hex of the digest names it.
func (c *Cache) path() string {
	return filepath.Join(c.dir, strings.TrimPrefix(c.digest, digest.Prefix)+".json")
}

func (c *Cache) load(data []byte) {
	var file cacheFile

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&file); err != nil {
		c.corruptf("the cache file is not valid: %v", err)

		return
	}

	switch {
	case file.Format != CacheFormat:
		c.corruptf("the cache file has format %d, this build writes format %d", file.Format, CacheFormat)

		return
	case file.KeyDigest != c.digest:
		c.corruptf("the cache file belongs to key %s, not to %s", file.KeyDigest, c.digest)

		return
	case file.Digest != file.sum():
		c.corruptf("the digest of the cache file does not match its content")

		return
	}

	for _, entry := range file.Assessments {
		c.stored[entry.Assessment] = entry
	}

	for _, item := range file.Items {
		c.storedItems[itemKeyOf(item.Kind, item.Identity)] = item.Digest
	}

	for _, d := range file.Digests {
		c.memo[d.Key] = d.Digest
	}

	c.status = CacheLoaded
}

func (c *Cache) corruptf(format string, args ...any) {
	c.status = CacheCorrupt
	c.detail = fmt.Sprintf(format, args...)
}

// sum is the digest of the file without its own Digest member. The encoding is the compact `encoding/json` one:
// the members are structs and sorted maps, so it is the same for the same content.
func (f cacheFile) sum() string { //nolint:gocritic // a value receiver copies the struct once to blank its digest.
	f.Digest = ""

	data, err := json.Marshal(f)
	if err != nil {
		// The members are strings, slices, and raw JSON that decoded: encoding cannot fail.
		return ""
	}

	return digest.OfBytes(data)
}

// Status says what opening the cache found.
func (c *Cache) Status() CacheStatus { return c.status }

// Detail says why the cache is not usable. Empty when it is loaded.
func (c *Cache) Detail() string {
	if c.status == CacheLoaded {
		return ""
	}

	return c.detail
}

// KeyDigest gives the digest of the key: the name of the file.
func (c *Cache) KeyDigest() string { return c.digest }

// Path gives the OS path of the cache file.
func (c *Cache) Path() string { return c.path() }

// Stats gives the counters of the run so far.
func (c *Cache) Stats() CacheStats { return c.stats }

func itemKeyOf(kind Kind, identity string) string { return string(kind) + "\x00" + identity }

// NoteItems records the digests of the items that the run saw, and counts those that differ from the stored file.
// Call it once per run with the items of every assessment.
func (c *Cache) NoteItems(entries []Entry) {
	for i := range entries {
		e := &entries[i]
		c.nextItems[itemKeyOf(e.Kind, e.Identity)] = e.Digest
	}

	if c.status != CacheLoaded {
		return
	}

	changed := 0

	for key, current := range c.nextItems {
		if old, ok := c.storedItems[key]; !ok || old != current {
			changed++
		}
	}

	for key := range c.storedItems {
		if _, ok := c.nextItems[key]; !ok {
			changed++
		}
	}

	c.stats.ChangedItems = changed
}

// Digester gives a digester for the contract that remembers digests: an item whose contract fields and file digests
// are those of an earlier run (or of an earlier item of this run) keeps its digest without the canonical encoding
// that Contract.Digest does. The memory is part of the cache file, so the key covers the contract and the
// normalization that the items came from.
func (c *Cache) Digester(contract Contract) Digester {
	return func(item Item, refs []RefDigest) string {
		return c.Remember(digestKey(contract, &item, refs), func() string { return contract.Digest(item, refs) })
	}
}

// Remember gives the value that an earlier run or an earlier call stored for the key, else computes and stores it. The
// key must name every input of compute: the remembered values are part of the cache file.
func (c *Cache) Remember(key string, compute func() string) string {
	if d, ok := c.memo[key]; ok {
		c.nextMemo[key] = d

		return d
	}

	d := compute()
	c.memo[key] = d
	c.nextMemo[key] = d

	return d
}

// digestKey names the inputs of Contract.Digest: the contract version, the kind, the values of the contract fields,
// and the digests of the files. It is a hash of a flat layout, which is far cheaper than the digest itself.
func digestKey(contract Contract, item *Item, refs []RefDigest) string {
	key := NewHasher().Add(contract.Version, string(item.Kind))

	for _, name := range contract.Fields[item.Kind] {
		key.Add("f", name).Add(sortedSet(item.Facets[name])...)
	}

	sorted := slices.Clone(refs)
	slices.SortFunc(sorted, func(a, b RefDigest) int {
		return cmp.Or(canonjson.CompareUTF16(string(a.Role), string(b.Role)), canonjson.CompareUTF16(a.Path, b.Path))
	})

	for _, r := range slices.CompactFunc(sorted, func(a, b RefDigest) bool { return a == b }) {
		key.Add("r", string(r.Role), r.Path, r.Digest)
	}

	return key.Sum()
}

// Lookup gives the stored result of an assessment version when the fingerprint is equal, and counts a hit. Any other
// outcome counts a miss.
func (c *Cache) Lookup(assessment, fingerprint string) (json.RawMessage, bool) {
	if entry, ok := c.stored[assessment]; ok && entry.Fingerprint == fingerprint {
		c.stats.Hits++
		c.next[assessment] = entry

		return entry.Result, true
	}

	c.stats.Misses++

	return nil, false
}

// Reject turns the hit of an assessment into a miss: the stored result could not be used.
func (c *Cache) Reject(assessment string) {
	c.stats.Hits--
	c.stats.Misses++

	delete(c.next, assessment)
}

// Put records the result that a miss evaluated.
func (c *Cache) Put(assessment, fingerprint string, result json.RawMessage) {
	c.next[assessment] = CacheEntry{Assessment: assessment, Fingerprint: fingerprint, Result: result}
}

// Save writes the cache file of the key atomically and removes the files of old keys beyond maxCacheFiles. The
// file holds what this run used: results of assessments that no longer exist are dropped.
func (c *Cache) Save() error {
	file := cacheFile{
		Format: CacheFormat, KeyDigest: c.digest, Key: c.key,
		Items: make([]CacheItem, 0, len(c.nextItems)), Digests: make([]CacheDigest, 0, len(c.nextMemo)),
		Assessments: make([]CacheEntry, 0, len(c.next)),
	}

	for key, d := range c.nextItems {
		kind, identity, _ := strings.Cut(key, "\x00")
		file.Items = append(file.Items, CacheItem{Kind: Kind(kind), Identity: identity, Digest: d})
	}

	slices.SortFunc(file.Items, func(a, b CacheItem) int {
		return cmp.Or(strings.Compare(string(a.Kind), string(b.Kind)), strings.Compare(a.Identity, b.Identity))
	})

	for key, d := range c.nextMemo {
		file.Digests = append(file.Digests, CacheDigest{Key: key, Digest: d})
	}

	slices.SortFunc(file.Digests, func(a, b CacheDigest) int { return strings.Compare(a.Key, b.Key) })

	for _, entry := range c.next {
		file.Assessments = append(file.Assessments, entry)
	}

	slices.SortFunc(file.Assessments, func(a, b CacheEntry) int { return strings.Compare(a.Assessment, b.Assessment) })

	file.Digest = file.sum()

	text, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("encode the cache: %w", err)
	}

	if err := fsutil.WriteFileAtomic(c.path(), append(text, '\n')); err != nil {
		return fmt.Errorf("write the cache: %w", err)
	}

	c.prune()

	return nil
}

// prune removes the oldest cache files beyond maxCacheFiles. A file that cannot be removed stays: it is only a
// derived file that no run trusts without its key.
func (c *Cache) prune() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}

	type aged struct {
		path string
		time int64
	}

	var files []aged

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, aged{path: filepath.Join(c.dir, entry.Name()), time: info.ModTime().UnixNano()})
	}

	if len(files) <= maxCacheFiles {
		return
	}

	// The file of this key stays first, then the newest.
	slices.SortFunc(files, func(a, b aged) int {
		return cmp.Or(
			cmp.Compare(boolRank(b.path == c.path()), boolRank(a.path == c.path())),
			cmp.Compare(b.time, a.time),
			strings.Compare(a.path, b.path),
		)
	})

	for _, f := range files[maxCacheFiles:] {
		_ = os.Remove(f.path)
	}
}

func boolRank(b bool) int {
	if b {
		return 1
	}

	return 0
}

// Hasher builds the digest of a flat layout of strings: each value is written with its length, so two different lists
// of values never give one byte sequence. It is far cheaper than the canonical JSON encoding, and it is for keys that
// only this program reads: the cache key members, the fingerprints, and the keys of remembered digests.
type Hasher struct {
	h       hash.Hash
	scratch []byte
}

// NewHasher gives an empty Hasher.
func NewHasher() *Hasher { return &Hasher{h: sha256.New(), scratch: make([]byte, 0, 256)} }

// Add writes values.
func (x *Hasher) Add(values ...string) *Hasher {
	for _, v := range values {
		x.scratch = strconv.AppendInt(x.scratch[:0], int64(len(v)), decimal)
		x.scratch = append(x.scratch, ':')
		x.scratch = append(x.scratch, v...)

		_, _ = x.h.Write(x.scratch)
	}

	return x
}

// AddInts writes numbers.
func (x *Hasher) AddInts(values ...int) *Hasher {
	for _, v := range values {
		x.Add(strconv.Itoa(v))
	}

	return x
}

// AddEntries writes comparison entries.
func (x *Hasher) AddEntries(entries []Entry) *Hasher {
	x.AddInts(len(entries))

	for i := range entries {
		e := &entries[i]
		x.Add(string(e.Kind), e.Identity, e.Digest, e.Reason)
	}

	return x
}

// AddDispositions writes dispositions.
func (x *Hasher) AddDispositions(list []Disposition) *Hasher {
	x.AddInts(len(list))

	for i := range list {
		d := &list[i]
		x.Add(string(d.Kind), d.Identity, string(d.Type), d.MovedTo, strconv.FormatBool(d.Verified))
	}

	return x
}

// Sum gives `sha256:<hex>` of what was written.
func (x *Hasher) Sum() string { return digest.Prefix + hex.EncodeToString(x.h.Sum(nil)) }
