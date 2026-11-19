package binding

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
)

// specificity: compare the tier first, then the length.
type specificity struct{ tier, length int }

func (s specificity) compare(o specificity) int {
	if s.tier != o.tier {
		return s.tier - o.tier
	}

	return s.length - o.length
}

// Tiers of the match kinds, lowest first. The tiers of the as-built kinds keep
// their order; the deployed-resource tiers are appended after them, so an exact
// deployed-resource kind outranks every as-built kind (and an image repository
// outranks sourceRoot and package).
const (
	tierSourceRoot = 1 + iota
	tierBuildTarget
	tierPackage
	tierQualifiedName
	tierOtelService
	tierImageRepo
	tierComposeService
	tierRoute
	tierInfraResource
	tierWorkload
	tierImageDigest
)

// LiteralPrefixLength gives the number of characters before the first glob character.
func LiteralPrefixLength(glob string) int {
	if i := strings.IndexAny(glob, "*?[{"); i >= 0 {
		return i
	}

	return len(glob)
}

// packageSegments turns `com.acme..` into ["com", "acme"].
func packageSegments(value string) []string {
	return strings.Split(strings.TrimSuffix(value, ".."), ".")
}

func containsSequence(haystack, needle []string) bool {
	if len(needle) == 0 {
		return false
	}

	for i := 0; i+len(needle) <= len(haystack); i++ {
		if slices.Equal(haystack[i:i+len(needle)], needle) {
			return true
		}
	}

	return false
}

type matcher struct {
	test func(n *observation.ObservedNode, segments []string) bool
	spec specificity
}

func isOtel(n *observation.ObservedNode) bool {
	return strings.HasPrefix(n.ObservedID, observation.OtelServicePrefix)
}

// newMatcher builds the matcher of one match. A synthesized entity (`synth:` ID)
// is never matched by an as-built kind: those kinds match provider nodes only.
func newMatcher(sel *Match, pkgs map[string]struct{}, cfg *resolveConfig) matcher {
	if m, ok := newDeployedMatcher(sel, cfg); ok {
		return m
	}

	m := newCodeMatcher(sel, pkgs)
	test := m.test
	m.test = func(n *observation.ObservedNode, segments []string) bool {
		return !strings.HasPrefix(n.ObservedID, observation.SynthPrefix()) && test(n, segments)
	}

	return m
}

func newCodeMatcher(sel *Match, pkgs map[string]struct{}) matcher {
	switch sel.Kind { //nolint:exhaustive // the deployed-resource kinds are matched by newDeployedMatcher.
	case MatchQualifiedName:
		return qualifiedNameMatcher(sel.Value)
	case MatchPackage:
		return packageMatcher(sel.Value)
	case MatchSourceRoot:
		return sourceRootMatcher(sel.Value)
	case MatchBuildTarget:
		if m, ok := buildTargetMatcher(sel.Value, pkgs); ok {
			return m
		}
	case MatchOtelService:
		return otelServiceMatcher(sel.Value)
	}

	return matcher{test: func(*observation.ObservedNode, []string) bool { return false }}
}

func qualifiedNameMatcher(value string) matcher {
	return matcher{
		spec: specificity{tierQualifiedName, len(value)},
		test: func(n *observation.ObservedNode, _ []string) bool {
			return !isOtel(n) && n.QualifiedName == value
		},
	}
}

func packageMatcher(value string) matcher {
	needle := packageSegments(value)

	return matcher{
		spec: specificity{tierPackage, len(needle)},
		test: func(n *observation.ObservedNode, segments []string) bool {
			return !isOtel(n) && containsSequence(segments, needle)
		},
	}
}

func sourceRootMatcher(value string) matcher {
	isMatch := fsutil.GlobMatcher([]string{value})

	return matcher{
		spec: specificity{tierSourceRoot, LiteralPrefixLength(value)},
		test: func(n *observation.ObservedNode, _ []string) bool {
			return !isOtel(n) && n.FilePath != "" && isMatch(n.FilePath)
		},
	}
}

// buildTargetMatcher builds the matcher of a build target label. It gives false for a bad label.
func buildTargetMatcher(value string, pkgs map[string]struct{}) (matcher, bool) {
	pkg, recursive, ok := parseLabel(value)
	if !ok {
		return matcher{}, false
	}

	length := len(pkg) + 1
	if recursive {
		length = len(pkg)
	}

	return matcher{
		spec: specificity{tierBuildTarget, length},
		test: func(n *observation.ObservedNode, _ []string) bool {
			if isOtel(n) || n.FilePath == "" {
				return false
			}

			owner, found := buildOwner(pkgs, n.FilePath)

			return found && inBuildPackage(owner, pkg, recursive)
		},
	}, true
}

func otelServiceMatcher(value string) matcher {
	return matcher{
		spec: specificity{tierOtelService, len(value)},
		test: func(n *observation.ObservedNode, _ []string) bool { return isOtel(n) && n.QualifiedName == value },
	}
}
