package intent

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

var (
	httpURL = regexp.MustCompile(`(?i)^https?://`)

	remoteKeys = []string{keyRef, "requirement-url", "config-url"}
)

// LoadURLMapping reads the architecture URL mapping file of the workspace
// (`{url: relativePath}`) and gives the URLs with absolute OS paths.
func LoadURLMapping(ws *workspace.Workspace) (map[string]string, error) {
	if ws.Architecture.URLMapping == "" {
		return map[string]string{}, nil
	}

	abs := ws.ResolveInRoot(ws.Architecture.URLMapping)

	data, err := os.ReadFile(abs) //nolint:gosec // the path comes from the workspace file.
	if err != nil {
		return nil, fmt.Errorf("read url mapping: %w", err)
	}

	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse url mapping %s: %w", abs, err)
	}

	out := make(map[string]string, len(raw))

	for url, p := range raw {
		if filepath.IsAbs(p) {
			out[url] = p
		} else {
			out[url] = filepath.Join(filepath.Dir(abs), filepath.FromSlash(p))
		}
	}

	return out, nil
}

// localFile gives the local path of a reference in the document: an http(s) URL
// resolves through the URL mapping or ctx.Remote (and is recorded in resources
// when seen is not nil), another value is a path relative to the document.
// ok is false when a remote URL does not resolve (the diagnostic is added).
func (l *loader) localFile(ref, pointer string, seen map[string]struct{}, resources *[]workspace.ResourceRef) (string, bool) {
	if httpURL.MatchString(ref) {
		if seen != nil {
			return l.noteRemote(ref, l.src(pointer), seen, resources)
		}

		return l.resolveRemote(ref, l.src(pointer))
	}

	if filepath.IsAbs(ref) {
		return ref, true
	}

	return filepath.Join(filepath.Dir(l.abs), filepath.FromSlash(path.Clean(ref))), true
}

// mapped gives the local path of a remote URL.
func (l *loader) mapped(url string) (string, bool) {
	if p, ok := l.ctx.URLMapping[url]; ok {
		return p, true
	}

	base, _, _ := strings.Cut(url, "#")
	p, ok := l.ctx.URLMapping[base]

	return p, ok
}

// resolveRemote gives the local path of a remote URL. It uses the URL mapping first, then ctx.Remote.
// Each URL gives at most one diagnostic.
func (l *loader) resolveRemote(url string, src diag.Option) (string, bool) {
	if p, ok := l.mapped(url); ok {
		return p, true
	}

	base, _, _ := strings.Cut(url, "#")

	if res, done := l.remote[base]; done {
		return res.path, res.ok
	}

	if l.remote == nil {
		l.remote = map[string]remoteResult{}
	}

	if l.ctx.Remote == nil {
		l.remote[base] = remoteResult{}
		l.diagnostics = append(l.diagnostics, diag.New(diag.INT012, diag.Detail(url+" has no entry in architecture.urlMapping"), src))

		return "", false
	}

	p, code, detail := l.ctx.Remote(base)
	if p == "" {
		l.remote[base] = remoteResult{}
		l.diagnostics = append(l.diagnostics, diag.New(code, diag.Detail(detail), src))

		return "", false
	}

	l.remote[base] = remoteResult{path: p, ok: true}

	return p, true
}

// scanRemote finds remote references. They resolve through the URL mapping or ctx.Remote.
// seen holds the base URLs already recorded.
func (l *loader) scanRemote(seen map[string]struct{}) []workspace.ResourceRef {
	out := []workspace.ResourceRef{}

	l.scanValue(l.doc, "", seen, &out)

	return out
}

func (l *loader) scanValue(value any, pointer string, seen map[string]struct{}, out *[]workspace.ResourceRef) {
	switch val := value.(type) {
	case []any:
		for i, item := range val {
			l.scanValue(item, pointer+"/"+strconv.Itoa(i), seen, out)
		}
	case obj:
		for _, key := range sortedKeys(val) {
			l.scanEntry(key, val[key], pointer+"/"+esc(key), seen, out)
		}
	}
}

// scanEntry records a remote reference held by key, or scans the value further.
func (l *loader) scanEntry(key string, child any, childPointer string, seen map[string]struct{}, out *[]workspace.ResourceRef) {
	text, isStr := child.(string)
	if isStr && slices.Contains(remoteKeys, key) && httpURL.MatchString(text) {
		l.noteRemote(text, l.src(childPointer), seen, out)

		return
	}

	l.scanValue(child, childPointer, seen, out)
}

// noteRemote checks one remote URL against the URL mapping and records it. The CALM
// release schemas are built in. It gives the local path and true when the URL resolves.
func (l *loader) noteRemote(url string, src diag.Option, seen map[string]struct{}, out *[]workspace.ResourceRef) (string, bool) {
	if strings.HasPrefix(url, calmReleasePrefix) {
		return "", false
	}

	mapped, ok := l.resolveRemote(url, src)
	if !ok {
		return "", false
	}

	base, _, _ := strings.Cut(url, "#")
	if _, dup := seen[base]; dup {
		return mapped, true
	}

	seen[base] = struct{}{}
	*out = append(*out, workspace.ResourceRef{URI: base, AbsPath: mapped})

	return mapped, true
}
