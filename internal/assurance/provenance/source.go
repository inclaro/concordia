package provenance

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/statement"
)

// The workspace declares a source repository only by an id (`assurance.provenance.sourceRepositories`), and a SLSA
// provenance statement records its sources as resolved dependencies with a URI such as
// `git+https://github.com/acme/shop@refs/heads/main`. The two are matched like this:
//
//   - A resolved dependency counts as a Git source when its URI has a Git-capable form: `git+<url>`, `https://`,
//     `http://`, `ssh://`, `git://`, or the scp form `git@host:owner/repo`. Package URLs (`pkg:oci/...`), which name
//     base images and tools, are never sources.
//   - The URI is reduced to `host/owner/repo` in lower case: the scheme, the user, the `@<ref>` suffix, a `#fragment`,
//     a trailing slash, and a `.git` suffix are dropped.
//   - An id matches when its slash-separated segments equal the last segments of that path, so `shop`, `acme/shop`,
//     and `github.com/acme/shop` all match `github.com/acme/shop`, and `ex/shop` does not match `github.com/acme/shop`.
//     An id that is itself a URL is reduced the same way first.
//
// A bare repository name matches that name under any owner: the id is all the workspace declares. The trust of the
// result rests on the producer and the builder as well, which the same contract names.

// sourceMatch is the resolved dependency that matched a declared repository.
type sourceMatch struct {
	// ID is the declared source repository id that matched.
	ID string
	// URI is the URI of the resolved dependency, as the statement wrote it.
	URI string
	// Revision is the dependency's `gitCommit` digest, empty when the statement gives none.
	Revision string
}

// findSource gives the first resolved dependency (in statement order) that matches a declared repository, trying the
// declared ids in their sorted order.
func findSource(deps []statement.ResourceDescriptor, declared []string) (sourceMatch, bool) {
	for i := range deps {
		segments, ok := repositorySegments(deps[i].URI)
		if !ok {
			continue
		}

		if id, found := declaredIDFor(declared, segments); found {
			return sourceMatch{ID: id, URI: deps[i].URI, Revision: deps[i].Digest["gitCommit"]}, true
		}
	}

	return sourceMatch{}, false
}

// declaredIDFor gives the first declared id that names the repository whose path segments are given.
func declaredIDFor(declared, segments []string) (string, bool) {
	for _, id := range declared {
		if idMatches(id, segments) {
			return id, true
		}
	}

	return "", false
}

// idMatches says whether the declared id names the repository whose path segments (host first) are given.
func idMatches(id string, segments []string) bool {
	want, ok := repositorySegments(id)
	if !ok {
		want = splitPath(strings.ToLower(strings.Trim(id, "/")))
	}

	if len(want) == 0 || len(want) > len(segments) {
		return false
	}

	return slices.Equal(want, segments[len(segments)-len(want):])
}

// repositorySegments reduces a Git URI to its lower-case segments `host, owner..., repo`. It reports false for a
// value that is not a Git URI or has no path.
func repositorySegments(uri string) ([]string, bool) {
	text := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(uri)), "git+")

	rest, ok := gitLocation(text)
	if !ok {
		return nil, false
	}

	rest = dropAuthorityUser(rest)
	rest, _, _ = strings.Cut(rest, "#")
	rest, _, _ = strings.Cut(rest, "@")
	rest = strings.TrimSuffix(strings.TrimRight(rest, "/"), ".git")

	segments := splitPath(rest)
	if len(segments) < 2 {
		return nil, false
	}

	return segments, true
}

// gitLocation gives the part of a Git URI after its scheme (or, in the scp form, the equivalent `host/owner/repo`).
// It reports false for a scheme that is not Git-capable and for a value that is not in the scp form.
func gitLocation(text string) (string, bool) {
	scheme, after, found := strings.Cut(text, "://")
	if found {
		return after, slices.Contains([]string{"https", "http", "ssh", "git"}, scheme)
	}

	// The scp form: `git@host:owner/repo`.
	user, path, found := strings.Cut(text, ":")
	if !found || !strings.Contains(user, "@") || strings.Contains(user, "/") {
		return "", false
	}

	return user + "/" + path, true
}

// dropAuthorityUser removes the `user@` of the authority, the part before the first slash.
func dropAuthorityUser(rest string) string {
	authority, path, found := strings.Cut(rest, "/")
	if !found {
		return rest
	}

	if _, host, hasUser := strings.Cut(authority, "@"); hasUser {
		authority = host
	}

	return authority + "/" + path
}

func splitPath(text string) []string {
	var out []string

	for part := range strings.SplitSeq(text, "/") {
		if part != "" {
			out = append(out, part)
		}
	}

	return out
}
