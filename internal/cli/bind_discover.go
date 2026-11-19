package cli

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
)

const (
	maxDiscoverDepth     = 3
	maxPackageCandidates = 3
	maxSampleIDs         = 5
	exactScore           = 1.0
	slugScore            = 0.8
	packageScore         = 0.6
)

var (
	packageSegmentPattern = regexp.MustCompile(`^[A-Za-z_][\w-]*$`)
	labelSegmentPattern   = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)
	serviceSuffixPattern  = regexp.MustCompile(`-(service|svc)$`)
	spacesPattern         = regexp.MustCompile(`\s+`)
)

// candidateMatch is the match of a candidate: one of the as-built keys, or a
// deployed-resource kind (`kind` with its identity fields, as in a binding file).
type candidateMatch struct {
	SourceRoot   string `yaml:"sourceRoot,omitempty"`
	Package      string `yaml:"package,omitempty"`
	BuildTarget  string `yaml:"buildTarget,omitempty"`
	Kind         string `yaml:"kind,omitempty"`
	Environment  string `yaml:"environment,omitempty"`
	Cluster      string `yaml:"cluster,omitempty"`
	APIGroup     string `yaml:"apiGroup,omitempty"`
	ResourceKind string `yaml:"resourceKind,omitempty"`
	Namespace    string `yaml:"namespace,omitempty"`
	Name         string `yaml:"name,omitempty"`
	Repository   string `yaml:"repository,omitempty"`
	Digest       string `yaml:"digest,omitempty"`
	Address      string `yaml:"address,omitempty"`
	File         string `yaml:"file,omitempty"`
	Service      string `yaml:"service,omitempty"`
}

func (m *candidateMatch) key() string {
	switch {
	case m.SourceRoot != "":
		return m.SourceRoot
	case m.BuildTarget != "":
		return m.BuildTarget
	case m.Kind != "":
		return strings.Join([]string{
			m.Kind, m.Environment, m.Cluster, m.APIGroup, m.ResourceKind, m.Namespace, m.Name,
			m.Repository, m.Digest, m.Address, m.File, m.Service,
		}, "\x00")
	}

	return m.Package
}

type candidate struct {
	Match           candidateMatch `yaml:"match"`
	ArchitectureRef string         `yaml:"architectureRef"`
	ReviewStatus    string         `yaml:"reviewStatus"`
	// Status is `proposed` for a deployment candidate (never `active`): a person
	// moves the match to a binding file and sets the status there. Empty for the other strategies.
	Status               string   `yaml:"status,omitempty"`
	Reasons              []string `yaml:"reasons"`
	SampleObservedIDs    []string `yaml:"sampleObservedIds"`
	ConflictsWith        []string `yaml:"conflictsWith"`
	Score                float64  `yaml:"score"`
	MatchedObservedCount int      `yaml:"matchedObservedCount"`
}

// unreviewedCandidate gives a candidate for a CALM node that no person has read.
func unreviewedCandidate(nodeID string, match *candidateMatch, score float64, reason string) candidate {
	return candidate{
		ArchitectureRef: nodeID, Match: *match, Score: score,
		Reasons:      []string{reason},
		ReviewStatus: reviewUnreviewed,
	}
}

// nodeSlugs are the slugs of a CALM node: ids are the ID slugs (the ID, and the ID
// without -service/-svc), all are the ID slugs plus the name with dashes.
type nodeSlugs struct {
	ids []string
	all []string
}

// slugsOf gives the slugs of a CALM node.
func slugsOf(id, name string) nodeSlugs {
	base := strings.ToLower(id)
	idSlugs := []string{base}

	if stripped := serviceSuffixPattern.ReplaceAllString(base, ""); stripped != base {
		idSlugs = append(idSlugs, stripped)
	}

	nameSlug := spacesPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")

	var all []string

	for _, s := range append(slices.Clone(idSlugs), nameSlug) {
		if s != "" && !slices.Contains(all, s) {
			all = append(all, s)
		}
	}

	return nodeSlugs{ids: idSlugs, all: all}
}

// bindSortedKeys gives the keys of a map, ordered by UTF-16 code units.
func bindSortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	return keys
}

func includeRoots(include []string) []string {
	if len(include) == 0 {
		return []string{""}
	}

	var roots []string

	for _, g := range include {
		prefix := g[:binding.LiteralPrefixLength(g)]
		prefix = prefix[:strings.LastIndex(prefix, "/")+1]
		prefix = strings.TrimSuffix(prefix, "/")

		if !slices.Contains(roots, prefix) {
			roots = append(roots, prefix)
		}
	}

	return roots
}

func candidateDirs(files, include []string) []string {
	roots := includeRoots(include)
	seen := map[string]struct{}{}

	for _, f := range files {
		parts := strings.Split(f, "/")
		parts = parts[:len(parts)-1]

		for _, root := range roots {
			for _, dir := range dirsBelowRoot(parts, root) {
				seen[dir] = struct{}{}
			}
		}
	}

	return bindSortedKeys(seen)
}

// dirsBelowRoot gives the directories of a file, at most maxDiscoverDepth levels below the include root.
func dirsBelowRoot(parts []string, root string) []string {
	var rootParts []string
	if root != "" {
		rootParts = strings.Split(root, "/")
	}

	if len(rootParts) > len(parts) || !slices.Equal(rootParts, parts[:len(rootParts)]) {
		return nil
	}

	var dirs []string

	for depth := 1; depth <= maxDiscoverDepth && len(rootParts)+depth <= len(parts); depth++ {
		dirs = append(dirs, strings.Join(parts[:len(rootParts)+depth], "/"))
	}

	return dirs
}

func packageCandidates(nodeID string, all []string, nodes []observation.ObservedNode) []candidate {
	keys := topPackages(packageFrequencies(all, nodes))
	out := make([]candidate, 0, len(keys))

	for _, pkg := range keys {
		out = append(out, unreviewedCandidate(nodeID, &candidateMatch{Package: pkg}, packageScore,
			fmt.Sprintf("qualified-name segment equals a slug of '%s'", nodeID)))
	}

	return out
}

// packageFrequencies counts the observed nodes under each package prefix that ends at a slug.
func packageFrequencies(all []string, nodes []observation.ObservedNode) map[string]int {
	freq := map[string]int{}

	for i := range nodes {
		if pkg, ok := slugPackage(nodes[i].QualifiedName, all); ok {
			freq[pkg]++
		}
	}

	return freq
}

// slugPackage gives the package pattern `<segments up to a slug>..` of a qualified name.
func slugPackage(qualifiedName string, all []string) (string, bool) {
	if qualifiedName == "" {
		return "", false
	}

	segs := strings.Split(qualifiedName, ".")

	at := slices.IndexFunc(segs, func(s string) bool { return slices.Contains(all, strings.ToLower(s)) })
	if at < 0 {
		return "", false
	}

	prefix := segs[:at+1]
	if slices.ContainsFunc(prefix, func(s string) bool { return !packageSegmentPattern.MatchString(s) }) {
		return "", false
	}

	return strings.Join(prefix, ".") + "..", true
}

// topPackages gives the most frequent packages, the most frequent first.
func topPackages(freq map[string]int) []string {
	keys := bindSortedKeys(freq)

	slices.SortStableFunc(keys, func(a, b string) int { return freq[b] - freq[a] })

	return keys[:min(len(keys), maxPackageCandidates)]
}

// candidateSources are the directories and prefixes that the path, build and cluster strategies match against.
type candidateSources struct {
	dirs      []string
	buildDirs []string
	clusters  []string
}

// nodeCandidates proposes the bindings of one CALM node from the strategies.
func nodeCandidates(node *intent.NodeDeclaration, nodes []observation.ObservedNode, sources *candidateSources, strategies map[string]bool) []candidate {
	slugs := slugsOf(node.ID, node.Name)

	out := dirCandidates(node.ID, &slugs, sources.dirs, pathMatch)
	out = append(out, dirCandidates(node.ID, &slugs, sources.buildDirs, buildMatch)...)

	if strategies[strategyPackage] {
		out = append(out, packageCandidates(node.ID, slugs.all, nodes)...)
	}

	if strategies[strategyRoute] {
		out = append(out, routeCandidates(node.ID, slugs.all, nodes)...)
	}

	if strategies[strategyDeployment] {
		out = append(out, deploymentCandidates(node.ID, &slugs, nodes)...)
	}

	return append(out, clusterCandidates(node.ID, slugs.all, sources.clusters)...)
}

// clusterCandidates proposes a sourceRoot for each import cluster whose prefix has a slug of the CALM node.
func clusterCandidates(nodeID string, all, clusters []string) []candidate {
	var out []candidate

	for _, prefix := range clusters {
		if slug := slugInPath(prefix, all); slug != "" {
			out = append(out, unreviewedCandidate(nodeID, &candidateMatch{SourceRoot: prefix + "/**"}, packageScore,
				fmt.Sprintf("import cluster under '%s' matches the slug '%s'", prefix, slug)))
		}
	}

	return out
}

// dirMatcher builds the match and the reason of a candidate for a directory whose leaf equals a slug.
type dirMatcher func(dir, leaf, what, nodeID string) (candidateMatch, string)

func pathMatch(dir, leaf, what, nodeID string) (match candidateMatch, reason string) {
	return candidateMatch{SourceRoot: dir + "/**"}, fmt.Sprintf("directory name '%s' equals %s of '%s'", leaf, what, nodeID)
}

func buildMatch(dir, _, what, nodeID string) (match candidateMatch, reason string) {
	return candidateMatch{BuildTarget: "//" + dir + "/..."}, fmt.Sprintf("build package '%s' name equals %s of '%s'", dir, what, nodeID)
}

// dirCandidates proposes a candidate for each directory whose leaf name is a slug of the node.
func dirCandidates(nodeID string, slugs *nodeSlugs, dirs []string, matcher dirMatcher) []candidate {
	var out []candidate

	for _, dir := range dirs {
		leaf := strings.ToLower(dir[strings.LastIndex(dir, "/")+1:])
		if !slices.Contains(slugs.all, leaf) {
			continue
		}

		score, what := slugScore, whatNameSlug
		if slugs.ids[0] == leaf {
			score, what = exactScore, whatUniqueID
		}

		match, reason := matcher(dir, leaf, what, nodeID)
		out = append(out, unreviewedCandidate(nodeID, &match, score, reason))
	}

	return out
}

// buildPackageDirs gives the sorted directories that hold a build file, without the root and
// without directories that no buildTarget label can name.
func buildPackageDirs(files []string) []string {
	var dirs []string

	for _, f := range binding.BuildFiles(files) {
		if d := path.Dir(f); isNewBuildDir(d, dirs) {
			dirs = append(dirs, d)
		}
	}

	slices.SortFunc(dirs, canonjson.CompareUTF16)

	return dirs
}

// isNewBuildDir says whether dir is not the root, is not listed yet, and can be in a buildTarget label.
func isNewBuildDir(dir string, listed []string) bool {
	if dir == "." || slices.Contains(listed, dir) {
		return false
	}

	return isBuildLabelDir(dir)
}

// isBuildLabelDir says whether every segment of dir can be in a buildTarget label.
func isBuildLabelDir(dir string) bool {
	return !slices.ContainsFunc(strings.Split(dir, "/"), func(s string) bool { return !labelSegmentPattern.MatchString(s) })
}

// slugInPath gives the first segment of dir that is one of the slugs, else "".
func slugInPath(dir string, slugs []string) string {
	for _, seg := range strings.Split(dir, "/") {
		if l := strings.ToLower(seg); slices.Contains(slugs, l) {
			return l
		}
	}

	return ""
}

// routeCandidates proposes a sourceRoot for each Route node whose name has a slug of the CALM node.
func routeCandidates(nodeID string, all []string, nodes []observation.ObservedNode) []candidate {
	seen := map[string]struct{}{}

	var out []candidate

	for i := range nodes {
		root, reason, ok := routeSourceRoot(&nodes[i], all)
		if !ok {
			continue
		}

		if _, dup := seen[root]; dup {
			continue
		}

		seen[root] = struct{}{}

		out = append(out, unreviewedCandidate(nodeID, &candidateMatch{SourceRoot: root}, packageScore, reason))
	}

	return out
}

// routeSourceRoot gives the sourceRoot and the reason for a Route node in a directory whose name has a slug.
func routeSourceRoot(node *observation.ObservedNode, all []string) (root, reason string, ok bool) {
	if node.Label != nodeLabelRoute || !strings.Contains(node.FilePath, "/") {
		return "", "", false
	}

	parts := strings.FieldsFunc(strings.ToLower(node.QualifiedName), func(r rune) bool { return r == '/' || r == ':' || r == '.' })

	matchAt := slices.IndexFunc(parts, func(s string) bool { return slices.Contains(all, s) })
	if matchAt < 0 {
		return "", "", false
	}

	return path.Dir(node.FilePath) + "/**", fmt.Sprintf("route '%s' has the segment '%s'", node.QualifiedName, parts[matchAt]), true
}

// candidateBindingMatch gives the binding match that resolves the candidate.
func candidateBindingMatch(cand *candidate) binding.Match {
	if deployed, ok := cand.Match.toMatch(); ok {
		return deployed
	}

	switch {
	case cand.Match.Package != "":
		return binding.Match{Kind: binding.MatchPackage, Value: cand.Match.Package}
	case cand.Match.BuildTarget != "":
		return binding.Match{Kind: binding.MatchBuildTarget, Value: cand.Match.BuildTarget}
	}

	return binding.Match{Kind: binding.MatchSourceRoot, Value: cand.Match.SourceRoot}
}

// matchedObserved gives the sorted observed IDs that each candidate binds.
func matchedObserved(raw []candidate, nodes []observation.ObservedNode, files []string) [][]string {
	matched := make([][]string, len(raw))
	buildFiles := binding.BuildFiles(files)

	for i := range raw {
		set := binding.Resolve([]binding.Binding{{
			ID: fmt.Sprintf("cand-%d", i), ArchitectureRef: raw[i].ArchitectureRef,
			Match: []binding.Match{candidateBindingMatch(&raw[i])},
		}}, nodes, buildFiles)

		matched[i] = bindSortedKeys(set.ByObserved)
	}

	return matched
}

// conflictsOf gives the CALM refs of the other nodes whose candidates bind one of the same observed nodes.
func conflictsOf(index int, raw []candidate, matched [][]string) []string {
	mine := make(map[string]struct{}, len(matched[index]))
	for _, id := range matched[index] {
		mine[id] = struct{}{}
	}

	bindsMine := func(id string) bool { _, ok := mine[id]; return ok }
	refs := map[string]struct{}{}

	for j := range raw {
		if raw[j].ArchitectureRef != raw[index].ArchitectureRef && slices.ContainsFunc(matched[j], bindsMine) {
			refs[raw[j].ArchitectureRef] = struct{}{}
		}
	}

	conflicts := bindSortedKeys(refs)
	for k := range conflicts {
		conflicts[k] = calmNodePrefix + conflicts[k]
	}

	return conflicts
}

// finishCandidates adds the match counts, the samples and the conflicts, and sorts.
func finishCandidates(raw []candidate, nodes []observation.ObservedNode, files []string) []candidate {
	matched := matchedObserved(raw, nodes, files)
	out := make([]candidate, len(raw))

	for i := range raw {
		out[i] = raw[i]
		out[i].MatchedObservedCount = len(matched[i])
		out[i].SampleObservedIDs = slices.Clone(matched[i][:min(len(matched[i]), maxSampleIDs)])
		out[i].ConflictsWith = conflictsOf(i, raw, matched)
		out[i].ArchitectureRef = calmNodePrefix + raw[i].ArchitectureRef
	}

	sort.SliceStable(out, func(a, b int) bool { return compareCandidates(&out[a], &out[b]) < 0 })

	return out
}

// compareCandidates orders by CALM ref, then by the highest score, then by match key.
func compareCandidates(left, right *candidate) int {
	if c := canonjson.CompareUTF16(left.ArchitectureRef, right.ArchitectureRef); c != 0 {
		return c
	}

	switch {
	case left.Score > right.Score:
		return -1
	case left.Score < right.Score:
		return 1
	}

	return canonjson.CompareUTF16(left.Match.key(), right.Match.key())
}
