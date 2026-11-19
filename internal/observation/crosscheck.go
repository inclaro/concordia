package observation

import (
	"cmp"
	"path"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/yamlparse"
)

// The manifest cross-check reads the environment manifests a second time, by structure only, to find
// losses that the provider does not signal (CBM 0.12.1 observations O4, O5, O6): a workload container that
// the provider merged into another, a reference whose target no manifest declares, and a selector that
// gives no edge.
//
// ALLOW-LIST. The check reads only these fields of a manifest, and never keeps a value from one except
// a name or a kind that goes into a gap:
//   - `apiVersion`, `kind`, `metadata.name`, `metadata.namespace`;
//   - workload pod specs (`spec`, `spec.template.spec`, `spec.jobTemplate.spec.template.spec`):
//     `containers[]` and `initContainers[]` `name`, whether `image` is present (its value is never read),
//     `serviceAccountName`, `env[].valueFrom.secretKeyRef.name` and `configMapKeyRef.name`,
//     `envFrom[].secretRef.name` and `configMapRef.name`, `volumes[].persistentVolumeClaim.claimName`,
//     `volumes[].secret.secretName` and `volumes[].configMap.name`;
//   - Ingress `spec.tls[].secretName`; HTTPRoute `spec.parentRefs[].name`, `kind`, `namespace`;
//   - RoleBinding and ClusterRoleBinding `roleRef.kind`, `roleRef.name`, `subjects[].kind`, `name`, `namespace`;
//   - HorizontalPodAutoscaler `spec.scaleTargetRef.kind` and `name`;
//   - the shape of selectors (`spec.podSelector`, `spec.selector`, `ingress[].from[]` and `egress[].to[]`
//     `podSelector` and `namespaceSelector`): present, an empty mapping, has `matchExpressions`, has `matchLabels`.
//
// Never read: Secret `data` and `stringData`, `env[].value`, `env[].name`, `args`, `command`, labels, annotations.
// Every read of a manifest goes through the helpers field, items, and name, and the fields above are the only
// keys that the code passes to them.

// CrossCheckInput is the input of CrossCheck.
type CrossCheckInput struct {
	// Attribute gives the declared environments of a file (workspace.Attribute). A file with none is outside the environment sources and is skipped.
	Attribute func(path string) []string
	// Read gives the content of a file by its workspace-relative path.
	Read func(path string) ([]byte, error)
	// Evidence is the provider evidence (nodes carry FilePath and obj_key, edges are the facts).
	Evidence *EvidenceQueryResult
	// Files are the workspace-relative "/" paths to consider (git files, with the `@<project>/` prefix for non-primary projects).
	Files []string
}

// CrossCheckResult is the result of CrossCheck.
type CrossCheckResult struct {
	// Gaps are the losses found, sorted (SortGaps), one per environment of the file.
	Gaps []CoverageGap `json:"gaps"`
	// Environments are the environments whose sources were cross-checked, sorted.
	Environments []string `json:"environments"`
	// FilesSkipped are the templated files, sorted. They are never parsed.
	FilesSkipped []string `json:"filesSkipped"`
	// Workloads are the workload documents that were read, one for each environment of the file, sorted. A
	// document keeps its kind, name, namespace, environment, and location only, never a value.
	Workloads []WorkloadDocument `json:"workloads"`
	// UnsupportedSources are the Compose and Terraform files in the environment sources, by file name, sorted.
	// They are not read: the deployment workload set of their environment is unknown.
	UnsupportedSources []UnsupportedSource `json:"unsupportedSources"`
	// FilesChecked counts the files that were read and parsed.
	FilesChecked int `json:"filesChecked"`
}

// WorkloadDocument is one workload manifest document that the cross-check read in one environment.
type WorkloadDocument struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Namespace   string `json:"namespace,omitempty"`
	Environment string `json:"environment"`
	Path        string `json:"path"`
	// Line is the 1-based line of the document.
	Line int `json:"line"`
	// ProviderNode says whether the provider evidence has a `Resource` node for the document. The provider
	// returns a workload node only as the endpoint of a fetched edge.
	ProviderNode bool `json:"providerNode"`
}

// NoProviderNodeReason gives the coverage reason of a workload document that has no provider node.
func (d *WorkloadDocument) NoProviderNodeReason() string {
	return "workload declared in " + d.Path + " has no provider node"
}

// UnsupportedSource is a Compose or Terraform file in the sources of an environment.
type UnsupportedSource struct {
	Environment string `json:"environment"`
	Path        string `json:"path"`
}

const (
	kindDeployment  = "Deployment"
	kindServiceAcct = "ServiceAccount"
	kindSecret      = "Secret"
	kindConfigMap   = "ConfigMap"
	kindPVC         = "PersistentVolumeClaim"
	kindGateway     = "Gateway"
	kindNetPolicy   = "NetworkPolicy"
	kindPDB         = "PodDisruptionBudget"
	kindService     = "Service"
	kindClusterRole = "ClusterRole"
)

var (
	crossDocSeparator = regexp.MustCompile(`^(---|\.{3})(\s|$)`)

	// workloadKinds are the kinds that have a pod spec.
	workloadKinds = []string{kindDeployment, "StatefulSet", "DaemonSet", "Job", "ReplicaSet", "Pod", "CronJob"}
)

// IsWorkloadKind says whether a Kubernetes kind has a pod spec: Deployment, StatefulSet, DaemonSet, ReplicaSet,
// Job, CronJob, or Pod. It is the set of kinds that placement and the deployment trace read.
func IsWorkloadKind(kind string) bool { return slices.Contains(workloadKinds, kind) }

// manifestDoc is one parsed YAML document that is a resource.
type manifestDoc struct {
	root      *yaml.Node
	path      string
	kind      string
	name      string
	namespace string
	envs      []string
	// offset is the number of lines before the document in its file.
	offset int
}

func (d *manifestDoc) workload() string { return d.kind + "/" + d.name }

func (d *manifestDoc) line(n *yaml.Node) int { return n.Line + d.offset }

type loadOutcome int

const (
	loadFailed loadOutcome = iota
	loadSkipped
	loadChecked
)

type crossChecker struct {
	in       *CrossCheckInput
	provider *providerIndex
	// decls gives, by environment ("" for provider nodes without one), the namespaces ("" for none) that declare a `<Kind>/<name>`.
	decls map[string]map[string][]string
	docs  []*manifestDoc
	gaps  []CoverageGap
}

// CrossCheck reads the manifests of the declared environments and finds the losses that the provider does not signal.
func CrossCheck(in *CrossCheckInput) CrossCheckResult {
	crosser := &crossChecker{in: in, provider: newProviderIndex(in.Evidence), decls: map[string]map[string][]string{}}
	res := CrossCheckResult{
		Gaps: []CoverageGap{}, Environments: []string{}, FilesSkipped: []string{},
		Workloads: []WorkloadDocument{}, UnsupportedSources: []UnsupportedSource{},
	}
	envs := map[string]struct{}{}

	files := crossFiles(in)
	for _, f := range files {
		for _, e := range crosser.readFile(f, files, &res) {
			envs[e] = struct{}{}
		}
	}

	crosser.declareProvider()
	crosser.analyzeAll()

	res.Gaps = append(res.Gaps, crosser.gaps...)
	res.Gaps = SortGaps(res.Gaps)
	res.Workloads = crosser.workloadDocuments()

	for e := range envs {
		res.Environments = append(res.Environments, e)
	}

	slices.Sort(res.Environments)
	slices.Sort(res.FilesSkipped)

	return res
}

// readFile reads one file and gives the environments that its sources add to the cross-checked ones, none for a file that is not read.
func (c *crossChecker) readFile(f string, files []string, res *CrossCheckResult) []string {
	fenvs := c.in.Attribute(f)
	c.sourceByName(f, fenvs, res)

	if !isYAMLFile(f) || len(fenvs) == 0 {
		return nil
	}

	switch c.load(f, fenvs, files, res) {
	case loadFailed:
		return nil
	case loadChecked:
		res.FilesChecked++
	case loadSkipped:
	}

	return fenvs
}

// analyzeAll finds the gaps of every document in each of its environments.
func (c *crossChecker) analyzeAll() {
	for _, d := range c.docs {
		for _, env := range d.envs {
			c.analyze(d, env)
		}
	}
}

// crossFiles gives the files of in.Files, sorted and without duplicates.
func crossFiles(in *CrossCheckInput) []string {
	files := slices.Clone(in.Files)
	slices.Sort(files)

	return slices.Compact(files)
}

// sourceByName finds the deployment sources that no cross-check reads, by file name: a Kustomize source
// (`override-unsignalled`: the provider gives no edge for an image override), a Helm chart (`unrendered-template`),
// and a Compose or Terraform file (unsupported). Each is kept apart from the plain manifest graph.
func (c *crossChecker) sourceByName(file string, envs []string, res *CrossCheckResult) {
	base := path.Base(file)

	for _, env := range envs {
		switch {
		case isKustomizationFile(base):
			c.gaps = append(c.gaps, CoverageGap{Reason: LossOverrideUnsignalled, Path: file, Environment: env, Detail: "Kustomize source"})
		case base == "Chart.yaml":
			c.gaps = append(c.gaps, CoverageGap{Reason: LossUnrenderedTemplate, Path: file, Environment: env, Detail: "Helm chart"})
		case IsComposeFile(file) || IsTerraformFile(file):
			res.UnsupportedSources = append(res.UnsupportedSources, UnsupportedSource{Environment: env, Path: file})
		}
	}
}

func isKustomizationFile(base string) bool {
	return base == "kustomization.yaml" || base == "kustomization.yml"
}

// IsComposeFile says whether a path is a Compose file by its name.
func IsComposeFile(file string) bool {
	base := path.Base(file)
	if ext := path.Ext(base); ext != extYAML && ext != extYML {
		return false
	}

	stem := strings.TrimSuffix(base, path.Ext(base))

	return stem == "compose" || stem == "docker-compose" || strings.HasPrefix(stem, "compose.") || strings.HasPrefix(stem, "docker-compose.")
}

// IsTerraformFile says whether a path is a Terraform file by its name.
func IsTerraformFile(file string) bool {
	ext := path.Ext(file)

	return ext == ".tf" || ext == ".tfvars"
}

// workloadDocuments lists the workload documents that were read, one for each environment, sorted.
func (c *crossChecker) workloadDocuments() []WorkloadDocument {
	out := []WorkloadDocument{}

	for _, d := range c.docs {
		if !slices.Contains(workloadKinds, d.kind) {
			continue
		}

		for _, env := range d.envs {
			out = append(out, WorkloadDocument{
				Kind: d.kind, Name: d.name, Namespace: d.namespace, Environment: env, Path: d.path, Line: d.line(d.root),
				ProviderNode: len(c.provider.resources(d.path, d.kind, d.name, env)) > 0,
			})
		}
	}

	slices.SortFunc(out, func(a, b WorkloadDocument) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Environment, b.Environment), canonjson.CompareUTF16(a.Path, b.Path), cmp.Compare(a.Line, b.Line),
			canonjson.CompareUTF16(a.Kind, b.Kind), canonjson.CompareUTF16(a.Namespace, b.Namespace), canonjson.CompareUTF16(a.Name, b.Name),
		)
	})

	return out
}

func isYAMLFile(f string) bool {
	switch path.Ext(f) {
	case extYAML, extYML, extTpl:
		return true
	default:
		return false
	}
}

// load reads one file: a templated file gives a gap and is skipped, any other adds its documents.
func (c *crossChecker) load(file string, envs, files []string, res *CrossCheckResult) loadOutcome {
	content, err := c.in.Read(file)
	templated := isTemplatedPath(file, files) || strings.Contains(string(content), "{{")

	if templated {
		line := templateLine(content)
		for _, e := range envs {
			c.gaps = append(c.gaps, CoverageGap{
				Reason: LossUnrenderedTemplate, Path: file, Line: line, Environment: e,
				Detail: "templated file is not cross-checked",
			})
		}

		res.FilesSkipped = append(res.FilesSkipped, file)

		return loadSkipped
	}

	if err != nil {
		return loadFailed
	}

	c.parse(file, envs, content)

	return loadChecked
}

// isTemplatedPath says whether the file is a template by its place: a `.tpl` file, or a file under `templates/` of a Helm chart.
func isTemplatedPath(f string, files []string) bool {
	if path.Ext(f) == extTpl {
		return true
	}

	dir := path.Dir(f)
	for dir != "." && dir != "/" && dir != "" {
		if path.Base(dir) == "templates" {
			return slices.Contains(files, path.Join(path.Dir(dir), "Chart.yaml"))
		}

		dir = path.Dir(dir)
	}

	return false
}

// templateLine gives the line of the first `{{` in content, 1 when there is none.
func templateLine(content []byte) int {
	for i, l := range strings.Split(string(content), "\n") {
		if strings.Contains(l, "{{") {
			return i + 1
		}
	}

	return 1
}

// parse splits a file into YAML documents and keeps the ones that are resources. A document that does not parse is skipped.
func (c *crossChecker) parse(f string, envs []string, content []byte) {
	lines := strings.Split(string(content), "\n")
	start := 0

	for i := 0; i <= len(lines); i++ {
		if i < len(lines) && !crossDocSeparator.MatchString(lines[i]) {
			continue
		}

		if i > start {
			c.document(f, envs, strings.Join(lines[start:i], "\n"), start)
		}

		start = i + 1
	}
}

func (c *crossChecker) document(f string, envs []string, text string, offset int) {
	root, problems := yamlparse.ParseNode([]byte(text))
	if len(problems) > 0 || !isMappingNode(root) {
		return
	}

	doc := &manifestDoc{root: root, path: f, envs: envs, offset: offset}
	if !doc.readIdentity() {
		return
	}

	c.docs = append(c.docs, doc)

	for _, e := range envs {
		c.declare(e, doc.kind, doc.name, doc.namespace)
	}
}

func isMappingNode(n *yaml.Node) bool { return n != nil && n.Kind == yaml.MappingNode }

// readIdentity reads the kind, name, and namespace of the document. It gives false for a document that is not a
// resource: one with no string `apiVersion` or `kind`, or a Kustomization.
func (d *manifestDoc) readIdentity() bool {
	if _, v := field(d.root, "apiVersion"); !isString(v) {
		return false
	}

	_, kv := field(d.root, "kind")
	if !isString(kv) {
		return false
	}

	d.kind = name(kv)
	if d.kind == "Kustomization" {
		return false
	}

	_, meta := field(d.root, "metadata")
	_, nv := field(meta, "name")
	_, nsv := field(meta, "namespace")
	d.name, d.namespace = name(nv), name(nsv)

	return true
}

func (c *crossChecker) declare(env, kind, nm, ns string) {
	if c.decls[env] == nil {
		c.decls[env] = map[string][]string{}
	}

	key := kind + "/" + nm
	c.decls[env][key] = append(c.decls[env][key], ns)
}

// declared says whether a manifest or a provider node declares the target. Namespaces are compared only when both sides have one.
func (c *crossChecker) declared(env string, r *reference) bool {
	key := r.kind + "/" + r.name

	for _, e := range []string{env, ""} {
		for _, ns := range c.decls[e][key] {
			if namespacesCompatible(ns, r.namespace) {
				return true
			}
		}
	}

	return false
}

// namespacesCompatible says whether two namespaces can name the same target: they are compared only when both are set.
func namespacesCompatible(a, b string) bool {
	return a == "" || b == "" || a == b
}

// analyze finds the gaps of one document in one environment.
func (c *crossChecker) analyze(d *manifestDoc, env string) {
	base := CoverageGap{Path: d.path, Environment: env, Workload: d.workload()}

	c.targetGaps(d, env, &base)

	if pod := podSpec(d); pod != nil {
		c.occurrenceGaps(d, env, pod, &base)
	}

	c.selectorGaps(d, env, &base)
}

// declareProvider adds the provider nodes to the declarations: a node with no environment declares for every environment.
func (c *crossChecker) declareProvider() {
	for _, r := range c.provider.all {
		c.declare(r.env, r.kind, r.name, r.namespace)
	}
}

// ----- merged occurrences -----

func (c *crossChecker) occurrenceGaps(doc *manifestDoc, env string, pod *yaml.Node, base *CoverageGap) {
	nodes := c.provider.resources(doc.path, doc.kind, doc.name, env)
	if len(nodes) == 0 {
		return
	}

	for _, ct := range containers(pod) {
		keyNode, nv := field(ct, "name")
		if nv == nil || !hasField(ct, "image") {
			continue
		}

		cn := name(nv)
		if cn == "" || c.provider.hasOccurrence(nodes, cn) {
			continue
		}

		g := *base
		g.Reason, g.Container, g.Line = LossMergedOccurrences, cn, doc.line(keyNode)
		g.Detail = "container has no uses-image fact"
		c.gaps = append(c.gaps, g)
	}
}

// ----- references -----

func (c *crossChecker) targetGaps(d *manifestDoc, env string, base *CoverageGap) {
	seen := map[reference]bool{}

	for _, use := range references(d) {
		key := use
		key.line = 0
		key.namespace = ""

		if seen[key] || c.declared(env, &use) {
			continue
		}

		seen[key] = true

		g := *base
		g.Reason, g.Line = LossTargetNotDeclared, use.line
		g.ReferenceMode, g.Target, g.TargetKind = use.mode, use.name, use.kind
		c.gaps = append(c.gaps, g)
	}
}
