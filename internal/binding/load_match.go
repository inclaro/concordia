package binding

import (
	"regexp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

var (
	packagePattern = regexp.MustCompile(`^[A-Za-z_][\w-]*(\.[A-Za-z_][\w-]*)*\.\.$`)
	driveLetter    = regexp.MustCompile(`^[A-Za-z]:`)
)

// deployKinds gives, for each deployed-resource kind, its required and optional fields.
var deployKinds = map[MatchKind]struct{ required, optional []string }{
	MatchWorkload:       {[]string{fieldEnvironment, fieldResourceKind, fieldName}, []string{fieldCluster, fieldAPIGroup, fieldNamespace}},
	MatchImage:          {[]string{fieldRepository}, []string{fieldDigest, fieldEnvironment}},
	MatchRoute:          {[]string{fieldEnvironment, fieldName}, []string{fieldCluster, fieldAPIGroup, fieldResourceKind, fieldNamespace}},
	MatchInfraResource:  {[]string{fieldEnvironment, fieldAddress}, []string{fieldCluster}},
	MatchComposeService: {[]string{fieldFile, fieldService}, []string{fieldEnvironment}},
}

// matchFieldGetters read the identity fields of a Match by their field name.
var matchFieldGetters = map[string]func(*Match) string{
	fieldEnvironment:  func(m *Match) string { return m.Environment },
	fieldCluster:      func(m *Match) string { return m.Cluster },
	fieldAPIGroup:     func(m *Match) string { return m.APIGroup },
	fieldResourceKind: func(m *Match) string { return m.ResourceKind },
	fieldNamespace:    func(m *Match) string { return m.Namespace },
	fieldName:         func(m *Match) string { return m.Name },
	fieldRepository:   func(m *Match) string { return m.Repository },
	fieldDigest:       func(m *Match) string { return m.Digest },
	fieldAddress:      func(m *Match) string { return m.Address },
	fieldFile:         func(m *Match) string { return m.File },
	fieldService:      func(m *Match) string { return m.Service },
}

// field gives the named identity field of m.
func (m *Match) field(name string) string {
	if get, ok := matchFieldGetters[name]; ok {
		return get(m)
	}

	return ""
}

// matchSite is where a match is declared and what it is validated against. A
// bad match adds its diagnostic to diagnostics.
type matchSite struct {
	scp         *scope
	diagnostics *[]diag.Diagnostic
	source      string
	pointer     string
}

// withSource gives the diagnostic option that names the site.
func (s matchSite) withSource() diag.Option {
	return diag.WithSource(diag.Source{URI: s.source, Pointer: s.pointer})
}

// at gives the site of the part of the declaration that the JSON pointer suffix names.
func (s matchSite) at(suffix string) matchSite {
	s.pointer += suffix

	return s
}

// report adds the diagnostic that diag.New makes from the code, the params and the options.
func (s matchSite) report(code string, params diag.Params, opts ...diag.Option) {
	*s.diagnostics = append(*s.diagnostics, diag.New(code, params, opts...))
}

// reject adds the CO-BND-002 diagnostic of a bad match.
func (s matchSite) reject(detail string) {
	s.report(diag.BND002, diag.Detail(detail), s.withSource())
}

// isRootedPath says whether value starts at the file system root: a leading `/`, a drive letter, or a backslash.
func isRootedPath(value string) bool {
	return strings.HasPrefix(value, "/") || driveLetter.MatchString(value) || strings.Contains(value, `\`)
}

// validSourceRoot says whether value is a relative POSIX glob: no leading `/`,
// no drive letter, no backslash, no `..` segment.
//
// A leading `@<name>/` selects the project <name> of the workspace. The second
// result is the unknown project name, when there is one.
func validSourceRoot(value string, projects map[string]struct{}) (ok bool, unknown string) {
	if isRootedPath(value) || slices.Contains(strings.Split(value, "/"), "..") {
		return false, ""
	}

	if name, _ := workspace.SplitProjectPath(value); name != "" {
		if _, known := projects[name]; !known {
			return false, name
		}
	}

	return true, ""
}

// deployMatchProblem says what is wrong with a deployed-resource match; it is empty for a valid one.
func deployMatchProblem(match *Match, scp *scope) string {
	for _, name := range deployKinds[match.Kind].required {
		if match.field(name) == "" {
			return string(match.Kind) + " match needs '" + name + "'"
		}
	}

	if match.Value != "" {
		return string(match.Kind) + " match does not take 'value'"
	}

	if match.Environment == "" {
		return ""
	}

	if _, ok := scp.environments[match.Environment]; !ok {
		return "environment '" + match.Environment + "' is not declared in the workspace"
	}

	return ""
}

// deployMatchOf validates a deployed-resource match. A bad one adds a diagnostic and gives false.
func deployMatchOf(match *Match, site matchSite) (Match, bool) {
	if detail := deployMatchProblem(match, site.scp); detail != "" {
		site.reject(detail)

		return Match{}, false
	}

	return *match, true
}

// sourceRootProblem says what is wrong with a sourceRoot value; it is empty for a valid one.
func sourceRootProblem(value string, projects map[string]struct{}) string {
	ok, unknown := validSourceRoot(value, projects)
	if ok {
		return ""
	}

	if unknown != "" {
		return "unknown project '" + unknown + "' in sourceRoot"
	}

	return "sourceRoot '" + value + "' must be a relative POSIX glob"
}

// matchProblem says what is wrong with the value of an as-built match; it is empty for a valid one.
func matchProblem(kind MatchKind, value string, projects map[string]struct{}) string {
	switch kind {
	case MatchBuildTarget:
		if _, _, ok := parseLabel(value); !ok {
			return "buildTarget '" + value + "' is not a label //path, //path:name or //path/..."
		}
	case MatchOtelService:
		if value == "" {
			return "otelService must not be empty"
		}
	case MatchPackage:
		if !packagePattern.MatchString(value) {
			return "package '" + value + "' must end with '..' (for example com.acme.billing..)"
		}
	case MatchSourceRoot:
		return sourceRootProblem(value, projects)
	case MatchQualifiedName, MatchWorkload, MatchImage, MatchRoute, MatchInfraResource, MatchComposeService:
	}

	return ""
}

// matchOf turns one `match.any` item into a Match. A bad item adds a
// diagnostic and gives false.
func matchOf(kind, value string, site matchSite) (Match, bool) {
	if detail := matchProblem(MatchKind(kind), value, site.scp.projects); detail != "" {
		site.reject(detail)

		return Match{}, false
	}

	return Match{Kind: MatchKind(kind), Value: value}, true
}

// itemMatch turns one raw `match.any` item into a Match. The item is either
// `{<legacyKind>: value}`, or `{kind: ..., ...}` (the schema has checked the shape).
func itemMatch(item map[string]string, site matchSite) (Match, bool) {
	kind, structured := item["kind"]
	if !structured {
		for k, v := range item { // the schema allows exactly one key
			return matchOf(k, v, site)
		}

		return Match{}, false
	}

	if _, deployed := deployKinds[MatchKind(kind)]; !deployed {
		return matchOf(kind, item["value"], site)
	}

	return deployMatchOf(structuredMatch(MatchKind(kind), item), site)
}

// structuredMatch reads the identity fields of a `{kind: ..., ...}` item.
func structuredMatch(kind MatchKind, item map[string]string) *Match {
	return &Match{
		Kind: kind, Environment: item[fieldEnvironment], Cluster: item[fieldCluster],
		APIGroup: item[fieldAPIGroup], ResourceKind: item[fieldResourceKind], Namespace: item[fieldNamespace],
		Name: item[fieldName], Repository: item[fieldRepository], Digest: item[fieldDigest],
		Address: item[fieldAddress], File: item[fieldFile], Service: item[fieldService], Value: item["value"],
	}
}
