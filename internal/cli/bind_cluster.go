package cli

import (
	"path"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// fileLink is an IMPORTS edge between two files.
type fileLink struct{ From, To string }

// crossDirs gives the directories of the two files of a link, when both files are named and in different directories.
func (l fileLink) crossDirs() (from, to string, ok bool) {
	from, to = path.Dir(l.From), path.Dir(l.To)

	return from, to, l.From != "" && l.To != "" && from != to
}

// dirGraph is an undirected graph of directories.
type dirGraph map[string]map[string]struct{}

// connect adds the undirected edge between two directories.
func (g dirGraph) connect(a, b string) {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		if g[pair[0]] == nil {
			g[pair[0]] = map[string]struct{}{}
		}

		g[pair[0]][pair[1]] = struct{}{}
	}
}

// sortedDirs gives the directories of the graph in order.
func (g dirGraph) sortedDirs() []string {
	dirs := make([]string, 0, len(g))
	for d := range g {
		dirs = append(dirs, d)
	}

	slices.SortFunc(dirs, canonjson.CompareUTF16)

	return dirs
}

// clusterPrefixes gives the longest common directory prefix of each connected component of the
// directory import graph, ordered by the smallest directory of the component.
func clusterPrefixes(links []fileLink) []string {
	adj := dirAdjacency(links)
	visited := map[string]bool{}

	var out []string

	for _, start := range adj.sortedDirs() {
		if visited[start] {
			continue
		}

		prefix := commonDirPrefix(dirComponent(start, adj, visited))
		if isNewClusterPrefix(prefix, out) {
			out = append(out, prefix)
		}
	}

	return out
}

// isNewClusterPrefix says whether a prefix names a directory that no earlier cluster has.
func isNewClusterPrefix(prefix string, found []string) bool {
	if prefix == "" || prefix == "." {
		return false
	}

	return !slices.Contains(found, prefix)
}

// dirAdjacency gives the undirected directory graph of the links between files of different directories.
func dirAdjacency(links []fileLink) dirGraph {
	adj := dirGraph{}

	for _, l := range links {
		if a, b, ok := l.crossDirs(); ok {
			adj.connect(a, b)
		}
	}

	return adj
}

// dirComponent gives the directories connected to start in adj and marks them visited.
func dirComponent(start string, adj dirGraph, visited map[string]bool) []string {
	comp, stack := []string{}, []string{start}
	visited[start] = true

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		comp = append(comp, cur)

		for next := range adj[cur] {
			if !visited[next] {
				visited[next] = true

				stack = append(stack, next)
			}
		}
	}

	return comp
}

func commonDirPrefix(dirs []string) string {
	common := strings.Split(dirs[0], "/")

	for _, d := range dirs[1:] {
		common = common[:commonSegments(common, strings.Split(d, "/"))]
	}

	return strings.Join(common, "/")
}

// commonSegments gives the length of the common leading segments of two paths.
func commonSegments(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}

	return n
}
