package render

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/yamlparse"
)

// Gap kinds of the pre-scan (design D4 layer 5).
const (
	GapRemoteBase           = "remote-base"
	GapRemoteChartRepo      = "remote-chart-repository"
	GapUnvendoredDependency = "unvendored-dependency"
	GapExecGenerator        = "exec-generator"
	GapKRMFunction          = "krm-function"
	GapHelmInflation        = "helm-chart-inflation"
	GapPostRenderer         = "post-renderer"
)

// finding is a gap of the pre-scan. A blocking finding means that the source must not be rendered: rendering would
// fetch something or start a program, or cannot succeed. A non-blocking finding is a request that Concordia ignores
// (the render proceeds without it) and the coverage is partial.
type finding struct {
	assurance.Gap
	Blocking bool
}

var remoteScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// isRemoteRef says that a kustomize resource, base, or component reference names something to download: a URL, a
// git address, or a `host.tld/org/repo` form that is not a local path.
func isRemoteRef(ref string, existsLocally bool) bool {
	if hasRemoteMarker(ref) {
		return true
	}

	if existsLocally || isLocalPathForm(ref) {
		return false
	}

	host, _, hasPath := strings.Cut(ref, "/")

	return hasPath && strings.Contains(host, ".")
}

// isLocalPathForm says that a reference is written as a relative or absolute path.
func isLocalPathForm(ref string) bool {
	return strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "/")
}

// hasRemoteMarker says that a reference carries a URL scheme, a git address, or a `?ref=` query.
func hasRemoteMarker(ref string) bool {
	if remoteScheme.MatchString(ref) || strings.Contains(ref, "?ref=") {
		return true
	}

	return strings.HasPrefix(ref, "git@") || strings.HasPrefix(ref, "git::")
}

// isRemoteRepository says that a chart dependency repository is not a local `file://` path.
func isRemoteRepository(repo string) bool {
	return repo != "" && !strings.HasPrefix(repo, "file://")
}

func readYAMLFile(file string) (any, bool) {
	data, err := os.ReadFile(file) //nolint:gosec // G304: file is in the confined copy of the declared paths.
	if err != nil {
		return nil, false
	}

	v, problems := yamlparse.Parse(data)

	return v, len(problems) == 0
}

// scanHelm checks a chart in the confined copy for dependencies that are not vendored (Concordia never downloads
// them) and for post-renderer requests in the values files. confined is the confined directory, chart the repository
// path of the chart, values the repository paths of the values files.
func scanHelm(confined, chart string, values []string) []finding {
	chartDir := filepath.Join(confined, filepath.FromSlash(chart))

	meta := asMap(firstYAML(filepath.Join(chartDir, "Chart.yaml")))
	if meta == nil {
		return []finding{{Gap: assurance.Gap{Kind: GapMissingSource, Detail: "the chart has no readable Chart.yaml", Path: chart}, Blocking: true}}
	}

	out := dependencyFindings(chart, chartDir, chartDependencies(chartDir, meta))

	return append(out, postRendererFindings(confined, chart, values)...)
}

// chartDependencies lists the dependencies of Chart.yaml and of the legacy requirements.yaml.
func chartDependencies(chartDir string, meta map[string]any) []any {
	deps := asList(meta["dependencies"])
	if req := asMap(firstYAML(filepath.Join(chartDir, "requirements.yaml"))); req != nil {
		deps = append(deps, asList(req["dependencies"])...)
	}

	return deps
}

// dependencyFindings gives a blocking finding for each dependency that is not vendored.
func dependencyFindings(chart, chartDir string, deps []any) []finding {
	var out []finding

	for _, d := range deps {
		dm := asMap(d)
		name, repo := str(dm, "name"), str(dm, "repository")

		if name == "" || vendored(chartDir, name) {
			continue
		}

		kind := GapUnvendoredDependency
		if isRemoteRepository(repo) {
			kind = GapRemoteChartRepo
		}

		detail := "the chart dependency " + name + " is not in charts/ and is not downloaded"
		if repo != "" {
			detail += " (repository " + repo + ")"
		}

		out = append(out, finding{Gap: assurance.Gap{Kind: kind, Detail: detail, Path: chart + "/Chart.yaml"}, Blocking: true})
	}

	return out
}

// postRendererFindings gives a non-blocking finding for each values file of the chart that asks for a post-renderer.
func postRendererFindings(confined, chart string, values []string) []finding {
	var out []finding

	candidates := append([]string{path.Join(chart, "values.yaml")}, values...)
	for _, v := range candidates {
		if key, ok := findPostRenderer(firstYAML(filepath.Join(confined, filepath.FromSlash(v)))); ok {
			out = append(out, finding{Gap: assurance.Gap{
				Kind: GapPostRenderer, Path: v,
				Detail: "the values ask for a post-renderer (" + key + "); Concordia never starts one, so the output is without it",
			}})
		}
	}

	return out
}

func firstYAML(file string) any {
	v, _ := readYAMLFile(file)

	return v
}

// vendored says that the dependency is present in the charts directory, as a directory or as an archive.
func vendored(chartDir, name string) bool {
	if info, err := os.Stat(filepath.Join(chartDir, "charts", name)); err == nil && info.IsDir() {
		return true
	}

	matches, _ := filepath.Glob(filepath.Join(chartDir, "charts", name+"-*.tgz"))

	return len(matches) > 0
}

// postRendererKey strips dashes and underscores so that `post-renderer` and `post_renderers` match.
var postRendererKey = strings.NewReplacer("-", "", "_", "")

func isPostRendererKey(k string) bool {
	lower := strings.ToLower(postRendererKey.Replace(k))

	return lower == "postrenderer" || lower == "postrenderers"
}

// findPostRenderer looks for a post-renderer key at any depth of a values document.
func findPostRenderer(v any) (string, bool) {
	switch val := v.(type) {
	case map[string]any:
		return findPostRendererInMap(val)
	case []any:
		return findPostRendererInList(val)
	}

	return "", false
}

func findPostRendererInMap(m map[string]any) (string, bool) {
	for k, item := range m {
		if isPostRendererKey(k) {
			return k, true
		}

		if key, ok := findPostRenderer(item); ok {
			return key, true
		}
	}

	return "", false
}

func findPostRendererInList(list []any) (string, bool) {
	for _, item := range list {
		if key, ok := findPostRenderer(item); ok {
			return key, true
		}
	}

	return "", false
}

// kustomizeScanner walks the kustomization of an overlay and the local directories it refers to. It copies each
// local directory into the confined copy (the base of an overlay lives beside it, not inside it) and records the
// remote bases and the constructs that run programs.
type kustomizeScanner struct {
	copier   *copier
	seen     map[string]bool
	findings []finding
}

var kustomizationNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

// scanKustomize copies the overlay and every local base or component that it reaches, and scans their
// kustomization files. The directories stay inside the workspace root: a reference that leaves it is a gap.
func scanKustomize(c *copier, overlay string) []finding {
	s := &kustomizeScanner{copier: c, seen: map[string]bool{}}

	if gap := c.copy(overlay); gap != nil {
		return []finding{{Gap: *gap, Blocking: true}}
	}

	s.scanDir(overlay)

	return s.findings
}

func (s *kustomizeScanner) add(kind, detail, where string) {
	s.findings = append(s.findings, finding{Gap: assurance.Gap{Kind: kind, Detail: detail, Path: where}, Blocking: true})
}

func (s *kustomizeScanner) scanDir(dir string) {
	if s.seen[dir] {
		return
	}

	s.seen[dir] = true

	file, doc := s.readKustomization(dir)
	if doc == nil {
		s.add(GapMissingSource, "the directory "+dir+" has no readable kustomization file", dir)

		return
	}

	if len(asList(doc["helmCharts"])) > 0 {
		s.add(GapHelmInflation, "helmCharts needs a helm run during the build, which Concordia never starts", file)
	}

	s.followAll(dir, file, doc)
	s.scanPlugins(dir, file, doc)
}

func (s *kustomizeScanner) followAll(dir, file string, doc map[string]any) {
	for _, field := range []string{"resources", "bases", "components"} {
		for _, item := range asList(doc[field]) {
			if ref, ok := item.(string); ok {
				s.follow(dir, file, ref)
			}
		}
	}
}

func (s *kustomizeScanner) scanPlugins(dir, file string, doc map[string]any) {
	for _, field := range []string{"generators", "transformers", "validators"} {
		for _, item := range asList(doc[field]) {
			s.scanPlugin(dir, file, field, item)
		}
	}
}

// readKustomization gives the repository path and the document of the first readable kustomization file of dir.
func (s *kustomizeScanner) readKustomization(dir string) (repoPath string, doc map[string]any) {
	for _, name := range kustomizationNames {
		candidate := filepath.Join(s.copier.in, filepath.FromSlash(dir), name)
		if v, ok := readYAMLFile(candidate); ok && asMap(v) != nil {
			return path.Join(dir, name), asMap(v)
		}
	}

	return "", nil
}

// checkRef resolves a reference against the directory of the file that holds it. A remote reference and one that
// leaves the workspace root are findings, and ok is then false. label starts the finding detail.
func (s *kustomizeScanner) checkRef(label, dir, ref, file string) (string, bool) {
	local := path.Clean(path.Join(dir, ref))
	// A reference that leaves the workspace root is never looked at, so no host file is read.
	escapes := leavesRoot(local)
	exists := !escapes && s.existsInRoot(local)

	switch {
	case isRemoteRef(ref, exists):
		s.add(GapRemoteBase, label+" "+ref+" is remote; it is not fetched", file)

		return local, false
	case escapes:
		s.add(GapPathEscape, label+" "+ref+" leaves the workspace root", file)

		return local, false
	}

	return local, true
}

func (s *kustomizeScanner) existsInRoot(local string) bool {
	_, err := os.Lstat(filepath.Join(s.copier.root, filepath.FromSlash(local)))

	return err == nil
}

// copyLocal copies a local path into the confined copy. A copy that fails is a blocking finding.
func (s *kustomizeScanner) copyLocal(local string) bool {
	gap := s.copier.copy(local)
	if gap == nil {
		return true
	}

	s.findings = append(s.findings, finding{Gap: *gap, Blocking: true})

	return false
}

// follow handles a resource, base, or component reference: remote is a gap, a local directory is copied and
// scanned, a local file is left to the copy (a file in the overlay directory is there already).
func (s *kustomizeScanner) follow(dir, file, ref string) {
	local, ok := s.checkRef("the reference", dir, ref, file)
	if !ok {
		return
	}

	info, err := os.Lstat(filepath.Join(s.copier.root, filepath.FromSlash(local)))
	if err != nil {
		s.add(GapMissingSource, "the reference "+ref+" does not exist", file)

		return
	}

	if !info.IsDir() || !s.copyLocal(local) {
		return
	}

	s.scanDir(local)
}

// pluginEntry is one generators, transformers, or validators entry of a kustomization file.
type pluginEntry struct {
	dir, file, field, ref string
}

// scanPlugin checks one entry of generators, transformers, or validators. Builtin plugin configurations are
// kustomize's own code; a KRM function runs a container or program, and any other plugin is an exec plugin.
func (s *kustomizeScanner) scanPlugin(dir, file, field string, item any) {
	ref, ok := item.(string)
	if !ok {
		return
	}

	pe := pluginEntry{dir: dir, file: file, field: field, ref: ref}

	text, ok := s.pluginText(pe)
	if !ok {
		return
	}

	for _, chunk := range splitDocuments(text) {
		v, _ := yamlparse.Parse([]byte(chunk.text))
		if doc := asMap(v); doc != nil {
			s.checkPluginDocument(pe, doc)
		}
	}
}

// pluginText gives the text of the plugin configuration: an inline entry is the text itself, any other entry is a
// file that is read from the confined copy only, after the copier's symlink and root checks.
func (s *kustomizeScanner) pluginText(entry pluginEntry) (string, bool) {
	if strings.Contains(entry.ref, "\n") {
		return entry.ref, true
	}

	local, ok := s.checkRef("the "+entry.field+" reference", entry.dir, entry.ref, entry.file)
	if !ok || !s.copyLocal(local) {
		return "", false
	}

	data, err := os.ReadFile(filepath.Join(s.copier.in, filepath.FromSlash(local)))
	if err != nil {
		s.add(GapMissingSource, "the "+entry.field+" file "+entry.ref+" cannot be read", entry.file)

		return "", false
	}

	return string(data), true
}

func (s *kustomizeScanner) checkPluginDocument(e pluginEntry, doc map[string]any) {
	annotations := strMap(asMap(doc["metadata"])["annotations"])
	_, krm := annotations["config.kubernetes.io/function"]

	switch {
	case krm:
		s.add(GapKRMFunction, "the "+e.field+" entry "+e.ref+" is a KRM function, which would start a program or container", e.file)
	case !strings.HasPrefix(str(doc, "apiVersion"), "builtin"):
		s.add(GapExecGenerator, "the "+e.field+" entry "+e.ref+" is an exec plugin ("+str(doc, "kind")+"), which Concordia never starts", e.file)
	}
}
