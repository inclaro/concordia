// Package remote downloads remote CALM resources and pins them in the lockfile.
package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fetch"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/workspace"
)

// CacheDir is the workspace-relative directory of downloaded resources.
const CacheDir = ".concordia/cache/resources"

const httpsPrefix = "https://"

// cacheDirMode is the permission of the cache directories.
const cacheDirMode = 0o750

// Resolver downloads remote resources and pins them in the lockfile.
type Resolver struct {
	lock     *workspace.Lockfile // read once, on first use
	Client   *http.Client        // fetch.NewClient() when nil
	Root     string              // absolute workspace root
	LockPath string              // absolute lockfile path
	// CacheHits counts the resources that Resolve took from the cache because the lockfile pins them.
	CacheHits int
	Offline   bool
	lockRead  bool
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// CachePath gives the absolute path of the cache file for a digest ("sha256:" and 64 lower-case
// hex digits, or the hex digits alone). The bool is false when the digest is not valid: the lock
// is not trusted to name a path.
func CachePath(root, sum string) (string, bool) {
	hexSum := strings.TrimPrefix(sum, "sha256:")
	if !digestPattern.MatchString(hexSum) {
		return "", false
	}

	return filepath.Join(root, filepath.FromSlash(CacheDir), hexSum), true
}

// ValidDigest tells whether sum is "sha256:" and 64 lower-case hex digits.
func ValidDigest(sum string) bool {
	hexSum, ok := strings.CutPrefix(sum, "sha256:")

	return ok && digestPattern.MatchString(hexSum)
}

// WriteCache writes data into the cache and gives the absolute path and the hex digest.
func WriteCache(root string, data []byte) (path, hexSum string, err error) {
	sum := sha256.Sum256(data)
	hexSum = hex.EncodeToString(sum[:])
	path, _ = CachePath(root, hexSum)

	if err := os.MkdirAll(filepath.Dir(path), cacheDirMode); err != nil {
		return "", "", err //nolint:wrapcheck // the caller adds the context.
	}

	if err := fsutil.WriteFileAtomic(path, data); err != nil {
		return "", "", err //nolint:wrapcheck // the caller adds the context.
	}

	return path, hexSum, nil
}

// Resolve gives the absolute path of the local copy of uri. On failure it gives "" and a
// diagnostic code (INT012 or INT013) and a detail.
func (r *Resolver) Resolve(ctx context.Context, uri string) (path, code, detail string) {
	if badCode, badDetail := checkURI(uri); badCode != "" {
		return "", badCode, badDetail
	}

	r.readLock()

	key := workspace.SanitizeURI(uri)

	cached, found := r.cachedCopy(key)
	if cached != "" {
		r.CacheHits++

		return cached, "", ""
	}

	if r.Offline {
		if !found {
			return "", diag.INT012, key + ": not in the lockfile, and --offline is set"
		}

		return "", diag.INT012, key + ": not in the cache, and --offline is set"
	}

	data, err := fetch.Get(ctx, r.Client, uri)
	if err != nil {
		return "", diag.INT013, key + ": " + err.Error()
	}

	cached, _, err = WriteCache(r.Root, data)
	if err != nil {
		return "", diag.INT013, key + ": cannot write the cache: " + err.Error()
	}

	return cached, "", ""
}

// checkURI gives a diagnostic code and detail when uri is not fetched at all.
func checkURI(uri string) (code, detail string) {
	if !strings.HasPrefix(uri, httpsPrefix) {
		return diag.INT012, uri + ": only https URLs are fetched; add an entry to architecture.urlMapping"
	}

	if parsed, err := url.Parse(uri); err == nil && parsed.User != nil {
		return diag.INT012, workspace.SanitizeURI(uri) + ": userinfo in URLs is not supported"
	}

	return "", ""
}

// readLock reads the lockfile on the first call; a missing or bad lockfile counts as empty.
func (r *Resolver) readLock() {
	if r.lockRead {
		return
	}

	r.lockRead = true

	if lock, _, err := workspace.ReadLock(r.LockPath); err == nil {
		r.lock = lock
	}
}

// cachedCopy gives the cache file that the lockfile pins for key ("" when it
// is not there) and whether the lockfile has an entry for key.
func (r *Resolver) cachedCopy(key string) (path string, found bool) {
	if r.lock == nil {
		return "", false
	}

	for _, entry := range r.lock.Resources {
		if entry.URI != key {
			continue
		}

		if cached, ok := CachePath(r.Root, entry.SHA256); ok {
			if _, err := os.Stat(cached); err == nil {
				return cached, true
			}
		}

		return "", true
	}

	return "", false
}

// ResolvePath gives the local path for an architecture.source or architecture.patterns value.
// A value without "https://" is returned unchanged with a nil ref.
func ResolvePath(ctx context.Context, r *Resolver, value string) (string, *workspace.ResourceRef, error) {
	if !strings.HasPrefix(value, httpsPrefix) {
		return value, nil, nil
	}

	p, code, detail := r.Resolve(ctx, value)
	if p == "" {
		return "", nil, diag.Fail(code, diag.Detail(detail))
	}

	return p, &workspace.ResourceRef{URI: value, AbsPath: p}, nil
}
