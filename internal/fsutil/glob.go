package fsutil

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// GlobMatcher compiles globs into one matcher. A pattern matches dot files
// (as picomatch with dot: true does). Pattern and path use "/" as separator;
// a leading "./" is ignored and backslashes in the path are changed to "/".
// An empty list matches nothing. A pattern with bad syntax matches nothing.
func GlobMatcher(patterns []string) func(path string) bool {
	if len(patterns) == 0 {
		return func(string) bool { return false }
	}

	clean := make([]string, len(patterns))
	for i, p := range patterns {
		clean[i] = strings.TrimPrefix(p, "./")
	}

	return func(p string) bool {
		p = strings.TrimPrefix(ToPosix(p), "./")

		for _, pattern := range clean {
			if ok, err := doublestar.Match(pattern, p); err == nil && ok {
				return true
			}
		}

		return false
	}
}
