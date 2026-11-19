package observation_test

import (
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

const ccInfraFixtures = "../../test/fixtures/infra"

// ccFixtureFiles lists the YAML files of the infra fixture, as "/" paths relative to its root.
func ccFixtureFiles(t *testing.T) []string {
	t.Helper()

	var files []string

	err := filepath.WalkDir(ccInfraFixtures, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, err := filepath.Rel(ccInfraFixtures, p)
		if err == nil {
			files = append(files, filepath.ToSlash(rel))
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return files
}

func ccFixtureAttribute(p string) []string {
	switch {
	case strings.HasPrefix(p, "k8s/prod/"), strings.HasPrefix(p, "charts/"):
		return []string{"production"}
	case strings.HasPrefix(p, "k8s/staging/"):
		return []string{"staging"}
	default:
		return nil
	}
}

func ccReadFixture(p string) ([]byte, error) {
	return os.ReadFile(filepath.Join(ccInfraFixtures, filepath.FromSlash(p)))
}

// resourceNode builds a provider `Resource` node.
func ccResource(id, file, objKey, env string) observation.ObservedNode {
	return observation.ObservedNode{
		ObservedID: id, Label: "Resource", FilePath: file, Environment: env,
		Properties: map[string]string{"obj_key": objKey},
	}
}

func ccUsesImage(from, container string) *observation.EvidenceEdge {
	return &observation.EvidenceEdge{
		From: from, Kind: observation.KindUsesImage, Occurrence: container,
		Properties: map[string]string{"container": container},
	}
}

// ccProviderO4 is the evidence of O4: the provider merged the containers of Deployment/api into `sidecar`.
func ccProviderO4() *observation.EvidenceQueryResult {
	return &observation.EvidenceQueryResult{
		Nodes: map[string]observation.ObservedNode{
			"n1": ccResource("n1", "k8s/prod/deployment.yaml", "apps/Deployment/payments/api", "production"),
			"n2": ccResource("n2", "k8s/prod/networkpolicy.yaml", "networking.k8s.io/NetworkPolicy/payments/api-allow-web", "production"),
		},
		Edges: []*observation.EvidenceEdge{ccUsesImage("n1", "sidecar")},
	}
}

func ccFixtureCheck(t *testing.T, ev *observation.EvidenceQueryResult) observation.CrossCheckResult {
	t.Helper()

	return observation.CrossCheck(&observation.CrossCheckInput{
		Files: ccFixtureFiles(t), Attribute: ccFixtureAttribute, Read: ccReadFixture, Evidence: ev,
	})
}

func ccGapsOf(res *observation.CrossCheckResult, reason string) []observation.CoverageGap {
	var out []observation.CoverageGap

	for i := range res.Gaps {
		if res.Gaps[i].Reason == reason {
			out = append(out, res.Gaps[i])
		}
	}

	return out
}

// ccGapWant is the expected form of a gap: an empty field or a zero line is not checked.
type ccGapWant struct {
	reason, workload, path, environment, container string
	line                                           int
}

func ccGapMatches(g *observation.CoverageGap, w *ccGapWant) bool {
	pairs := [][2]string{
		{g.Reason, w.reason},
		{g.Workload, w.workload},
		{g.Path, w.path},
		{g.Environment, w.environment},
		{g.Container, w.container},
	}

	for _, p := range pairs {
		if p[1] != "" && p[0] != p[1] {
			return false
		}
	}

	return w.line == 0 || g.Line == w.line
}

// ccExpectGaps reports gaps that are not the wanted gaps, in order.
func ccExpectGaps(t *testing.T, label string, gaps []observation.CoverageGap, want ...ccGapWant) {
	t.Helper()

	if len(gaps) != len(want) {
		t.Errorf("%s = %+v, want %d", label, gaps, len(want))

		return
	}

	for i := range want {
		if !ccGapMatches(&gaps[i], &want[i]) {
			t.Errorf("%s[%d] = %+v, want %+v", label, i, gaps[i], want[i])
		}
	}
}

func ccExpectNoGaps(t *testing.T, res *observation.CrossCheckResult, reason string) {
	t.Helper()

	if gaps := ccGapsOf(res, reason); len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none", gaps)
	}
}

func ccExpectString(t *testing.T, what, got, want string) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func ccExpectStrings(t *testing.T, what string, got, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestCrossCheckMergedOccurrence(t *testing.T) {
	t.Parallel()

	res := ccFixtureCheck(t, ccProviderO4())

	ccExpectGaps(t, "merged-occurrences gaps", ccGapsOf(&res, observation.LossMergedOccurrences), ccGapWant{
		path: "k8s/prod/deployment.yaml", workload: "Deployment/api", container: "app", environment: "production", line: 20,
	})
}

func TestCrossCheckEveryContainerHasOccurrence(t *testing.T) {
	t.Parallel()

	ev := ccProviderO4()
	ev.Edges = append(ev.Edges, ccUsesImage("n1", "app"))

	res := ccFixtureCheck(t, ev)

	ccExpectNoGaps(t, &res, observation.LossMergedOccurrences)
}

func TestCrossCheckNoProviderNodeNoOccurrenceGap(t *testing.T) {
	t.Parallel()

	res := ccFixtureCheck(t, &observation.EvidenceQueryResult{})

	ccExpectNoGaps(t, &res, observation.LossMergedOccurrences)
}

func TestCrossCheckOccurrenceEnvironmentIsolated(t *testing.T) {
	t.Parallel()

	// The node of another environment says nothing about the production file.
	ev := ccProviderO4()
	n := ev.Nodes["n1"]
	n.Environment = "staging"
	ev.Nodes["n1"] = n

	res := ccFixtureCheck(t, ev)

	ccExpectNoGaps(t, &res, observation.LossMergedOccurrences)
}

type ccTuple struct{ mode, target, kind string }

// ccDeclaredTargets are the targets that the infra fixture declares: none is a gap.
var ccDeclaredTargets = map[string]bool{
	"api-secrets": true, "api-config": true, "api-data": true, "api-sa": true,
	"api-role": true, "public-gateway": true, "api-tls": true,
}

// ccProductionTargetGaps groups the target-not-declared gaps of the production environment by workload.
func ccProductionTargetGaps(res *observation.CrossCheckResult) map[string]map[ccTuple]bool {
	got := map[string]map[ccTuple]bool{}

	gaps := ccGapsOf(res, observation.LossTargetNotDeclared)
	for i := range gaps {
		g := &gaps[i]
		if g.Environment != "production" {
			continue
		}

		if got[g.Workload] == nil {
			got[g.Workload] = map[ccTuple]bool{}
		}

		got[g.Workload][ccTuple{g.ReferenceMode, g.Target, g.TargetKind}] = true
	}

	return got
}

func TestCrossCheckTargetsNotDeclared(t *testing.T) {
	t.Parallel()

	res := ccFixtureCheck(t, ccProviderO4())
	got := ccProductionTargetGaps(&res)

	want := map[string][]ccTuple{
		"Deployment/api": {
			{"env", "missing-secret", "Secret"},
			{"envFrom", "missing-config", "ConfigMap"},
			{"volume", "missing-pvc", "PersistentVolumeClaim"},
		},
		"HTTPRoute/api":           {{"parentRef", "missing-gateway", "Gateway"}},
		"RoleBinding/api-binding": {{"subject", "ghost-sa", "ServiceAccount"}},
		"Ingress/api":             {{"tls", "missing-tls", "Secret"}},
	}

	ccExpectTargetsReported(t, got, want)
	ccExpectNoDeclaredTargetReported(t, got)
}

func ccExpectTargetsReported(t *testing.T, got map[string]map[ccTuple]bool, want map[string][]ccTuple) {
	t.Helper()

	for wl, tuples := range want {
		for _, tp := range tuples {
			if !got[wl][tp] {
				t.Errorf("%s: missing target-not-declared %+v; got %+v", wl, tp, got[wl])
			}
		}
	}
}

// ccExpectNoDeclaredTargetReported checks that what the fixture declares is no gap.
func ccExpectNoDeclaredTargetReported(t *testing.T, got map[string]map[ccTuple]bool) {
	t.Helper()

	for wl, set := range got {
		for tp := range set {
			if ccDeclaredTargets[tp.target] {
				t.Errorf("%s: declared target reported: %+v", wl, tp)
			}
		}
	}
}

const ccTargetKindsYAML = `apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: ns}
spec:
  template:
    spec:
      serviceAccountName: ghost
      containers:
        - name: c
          image: x
      volumes:
        - name: v
          secret: {secretName: vs}
        - name: w
          configMap: {name: vc}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: b}
roleRef: {kind: ClusterRole, name: missing-cr}
subjects: [{kind: User, name: alice}]
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: h, namespace: ns}
spec:
  scaleTargetRef: {kind: StatefulSet, name: gone}
`

func TestCrossCheckTargetKinds(t *testing.T) {
	t.Parallel()

	files := map[string]string{"a.yaml": ccTargetKindsYAML}

	res := observation.CrossCheck(&observation.CrossCheckInput{
		Files: []string{"a.yaml"}, Attribute: func(string) []string { return []string{"production"} },
		Read: func(p string) ([]byte, error) { return []byte(files[p]), nil },
	})

	got := map[ccTuple]bool{}
	for _, g := range ccGapsOf(&res, observation.LossTargetNotDeclared) {
		got[ccTuple{g.ReferenceMode, g.Target, g.TargetKind}] = true
	}

	for _, want := range []ccTuple{
		{"serviceAccountName", "ghost", "ServiceAccount"},
		{"volume", "vs", "Secret"},
		{"volume", "vc", "ConfigMap"},
		{"roleRef", "missing-cr", "ClusterRole"},
		{"scaleTarget", "gone", "StatefulSet"},
	} {
		if !got[want] {
			t.Errorf("missing %+v; got %+v", want, got)
		}
	}

	if len(got) != 5 {
		t.Errorf("got %d gaps, want 5: %+v", len(got), got)
	}
}

func TestCrossCheckProviderDeclaresTarget(t *testing.T) {
	t.Parallel()

	ev := ccProviderO4()
	ev.Nodes["s"] = ccResource("s", "elsewhere/secret.yaml", "core/Secret/payments/missing-secret", "")

	res := ccFixtureCheck(t, ev)
	for _, g := range ccGapsOf(&res, observation.LossTargetNotDeclared) {
		if g.Target == "missing-secret" {
			t.Errorf("target declared by a provider node is reported: %+v", g)
		}
	}
}

func TestCrossCheckSelectors(t *testing.T) {
	t.Parallel()

	res := ccFixtureCheck(t, ccProviderO4())

	ccExpectGaps(t, "empty-selector gaps", ccGapsOf(&res, observation.LossEmptySelector),
		ccGapWant{workload: "NetworkPolicy/default-deny", path: "k8s/prod/networkpolicy.yaml", line: 25})
	ccExpectGaps(t, "unsupported-selector-expression gaps", ccGapsOf(&res, observation.LossUnsupportedSelectorExpression),
		ccGapWant{workload: "NetworkPolicy/api-allow-web", path: "k8s/prod/networkpolicy.yaml", line: 13})

	// The NetworkPolicy has a provider node and no selection edge: its matchLabels selector is not joined.
	ccExpectGaps(t, "join gaps", ccGapsOf(&res, observation.LossSelectorJoinDirectoryScoped),
		ccGapWant{workload: "NetworkPolicy/api-allow-web", line: 7})
}

func TestCrossCheckSelectorJoinedByProvider(t *testing.T) {
	t.Parallel()

	ev := ccProviderO4()
	ev.Edges = append(ev.Edges, &observation.EvidenceEdge{From: "n2", Properties: map[string]string{"ref": "podSelector"}})

	res := ccFixtureCheck(t, ev)

	ccExpectNoGaps(t, &res, observation.LossSelectorJoinDirectoryScoped)
}

func TestCrossCheckTemplateSkipped(t *testing.T) {
	t.Parallel()

	res := ccFixtureCheck(t, ccProviderO4())

	const tpl = "charts/api/templates/deployment.yaml"

	if !slices.Equal(res.FilesSkipped, []string{tpl}) {
		t.Fatalf("FilesSkipped = %v", res.FilesSkipped)
	}

	var gaps []observation.CoverageGap

	for _, g := range res.Gaps {
		if g.Path == tpl {
			gaps = append(gaps, g)
		}
	}

	ccExpectGaps(t, "gaps for the template", gaps, ccGapWant{reason: observation.LossUnrenderedTemplate, line: 1, environment: "production"})
}

func TestCrossCheckTemplateByChartDirectory(t *testing.T) {
	t.Parallel()

	res := observation.CrossCheck(&observation.CrossCheckInput{
		Files:     []string{"c/Chart.yaml", "c/templates/a.yaml", "plain/templates/b.yaml"},
		Attribute: func(string) []string { return []string{"production"} },
		Read: func(string) ([]byte, error) {
			return []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"), nil
		},
	})

	if len(res.FilesSkipped) != 1 || res.FilesSkipped[0] != "c/templates/a.yaml" {
		t.Errorf("FilesSkipped = %v", res.FilesSkipped)
	}

	if res.FilesChecked != 2 {
		t.Errorf("FilesChecked = %d", res.FilesChecked)
	}
}

func TestCrossCheckTwoEnvironmentsOneGapEach(t *testing.T) {
	t.Parallel()

	res := observation.CrossCheck(&observation.CrossCheckInput{
		Files:     []string{"k8s/prod/networkpolicy.yaml"},
		Attribute: func(string) []string { return []string{"staging", "production"} },
		Read:      ccReadFixture,
	})

	emptySelectors := ccGapsOf(&res, observation.LossEmptySelector)
	envs := make([]string, 0, len(emptySelectors))

	for i := range emptySelectors {
		envs = append(envs, emptySelectors[i].Environment)
	}

	want := []string{"production", "staging"}

	ccExpectStrings(t, "empty-selector environments", envs, want)
	ccExpectStrings(t, "Environments", res.Environments, want)
}

func TestCrossCheckSkipsUnattributedAndBadInput(t *testing.T) {
	t.Parallel()

	res := observation.CrossCheck(&observation.CrossCheckInput{
		Files: []string{"none.yaml", "bad.yaml", "missing.yaml", "notes.txt"},
		Attribute: func(p string) []string {
			return map[string][]string{"bad.yaml": {"production"}, "missing.yaml": {"production"}}[p]
		},
		Read: func(p string) ([]byte, error) {
			if p == "missing.yaml" {
				return nil, os.ErrNotExist
			}

			return []byte("apiVersion: v1\nkind: [unclosed\n"), nil
		},
	})

	if len(res.Gaps) != 0 || res.FilesChecked != 1 {
		t.Errorf("res = %+v", res)
	}
}

// ccSeedValues reads the seeded secret values of the infra fixture, with the decoded form of each base64 value.
func ccSeedValues(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile("../../test/fixtures/infra-seeds.json")
	if err != nil {
		t.Fatal(err)
	}

	var seeds []struct {
		Path  string `json:"path"`
		Class string `json:"class"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &seeds); err != nil {
		t.Fatal(err)
	}

	values := make([]string, 0, len(seeds)*2)

	for _, s := range seeds {
		values = append(values, s.Value)

		if dec, err := base64.StdEncoding.DecodeString(s.Value); err == nil && len(dec) > 0 {
			values = append(values, string(dec))
		}
	}

	return values
}

// TestCrossCheckNeverEmitsSeedValues is the allow-list test: no seeded secret value of the fixture is in the result.
func TestCrossCheckNeverEmitsSeedValues(t *testing.T) {
	t.Parallel()

	values := ccSeedValues(t)
	res := ccFixtureCheck(t, ccProviderO4())

	out, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range values {
		if v != "" && strings.Contains(string(out), v) {
			t.Errorf("result contains a seeded value (%d bytes)", len(v))
		}
	}

	if len(values) == 0 || len(res.Gaps) == 0 {
		t.Fatal("the test checked nothing")
	}
}

func ccWorkload(res *observation.CrossCheckResult, env, file, name string) (observation.WorkloadDocument, bool) {
	for _, w := range res.Workloads {
		if [3]string{w.Environment, w.Path, w.Name} == [3]string{env, file, name} {
			return w, true
		}
	}

	return observation.WorkloadDocument{}, false
}

var ccWorkloadKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod"}

func ccExpectOnlyWorkloadKinds(t *testing.T, res *observation.CrossCheckResult) {
	t.Helper()

	for _, w := range res.Workloads {
		if !slices.Contains(ccWorkloadKinds, w.Kind) {
			t.Errorf("%+v is no workload", w)
		}
	}
}

func ccExpectAPIWorkload(t *testing.T, res *observation.CrossCheckResult) {
	t.Helper()

	api, ok := ccWorkload(res, "production", "k8s/prod/deployment.yaml", "api")
	if !ok {
		t.Fatalf("api = %+v (%v)", api, ok)
	}

	ccExpectString(t, "api kind", api.Kind, "Deployment")
	ccExpectString(t, "api namespace", api.Namespace, "payments")

	if api.Line == 0 {
		t.Errorf("api = %+v has no line", api)
	}

	if !api.ProviderNode {
		t.Errorf("api = %+v has no provider node", api)
	}
}

func TestCrossCheckListsTheWorkloadDocumentsItRead(t *testing.T) {
	t.Parallel()

	res := ccFixtureCheck(t, ccProviderO4())

	ccExpectAPIWorkload(t, &res)

	// The staging workloads are listed for staging only, and a document that is no workload is not listed.
	if _, ok := ccWorkload(&res, "production", "k8s/staging/worker.yaml", "worker"); ok {
		t.Error("a staging document is listed for production")
	}

	ccExpectOnlyWorkloadKinds(t, &res)

	// Only one workload of the O4 evidence has a node; each other document says so.
	worker, ok := ccWorkload(&res, "staging", "k8s/staging/worker.yaml", "worker")
	if !ok {
		t.Fatalf("worker = %+v (%v)", worker, ok)
	}

	if worker.ProviderNode {
		t.Errorf("worker = %+v (%v)", worker, ok)
	}

	ccExpectString(t, "reason", worker.NoProviderNodeReason(), "workload declared in k8s/staging/worker.yaml has no provider node")
}

// ccEvidenceWithNodesFor gives provider evidence with a node for each listed workload.
func ccEvidenceWithNodesFor(res *observation.CrossCheckResult) *observation.EvidenceQueryResult {
	ev := &observation.EvidenceQueryResult{Nodes: map[string]observation.ObservedNode{}}

	for i, w := range res.Workloads {
		id := "n" + string(rune('a'+i))
		ev.Nodes[id] = ccResource(id, w.Path, "apps/"+w.Kind+"/"+w.Namespace+"/"+w.Name, w.Environment)
	}

	return ev
}

func TestCrossCheckWorkloadsAllHaveNodesAddNoReason(t *testing.T) {
	t.Parallel()

	res := ccFixtureCheck(t, ccProviderO4())

	// Give every listed workload a node: the list then adds no reason.
	again := ccFixtureCheck(t, ccEvidenceWithNodesFor(&res))
	for _, w := range again.Workloads {
		if !w.ProviderNode {
			t.Errorf("%+v has no provider node", w)
		}
	}

	in := &observation.PredicateInput{
		Families: profileFamilies(t), Projects: []observation.ProjectScope{indexed("app")},
		Environments: []observation.EnvironmentScope{{ID: "production", Projects: []string{"app"}, CrossChecked: true, Workloads: again.Workloads}},
	}

	rec := recordOf(t, observation.ComputePredicateCoverage(in), workloadSet, observation.CoverageScope{Environment: "production"})
	if slices.ContainsFunc(rec.Reasons, func(r string) bool { return strings.Contains(r, "provider node") }) {
		t.Errorf("reasons = %v", rec.Reasons)
	}
}

func TestCrossCheckSourcesByFileName(t *testing.T) {
	t.Parallel()

	files := []string{
		"kustomize/base/kustomization.yaml", "kustomize/overlays/prod/kustomization.yml", "charts/api/Chart.yaml",
		"terraform/main.tf", "terraform/prod.tfvars", "compose.yaml", "docker-compose.override.yml", "k8s/prod/plain.yaml", "docs/readme.md",
	}

	res := observation.CrossCheck(&observation.CrossCheckInput{
		Files:     files,
		Attribute: func(string) []string { return []string{"production"} },
		Read:      func(string) ([]byte, error) { return []byte("kind: Nothing\n"), nil },
		Evidence:  &observation.EvidenceQueryResult{},
	})

	unsupported := make([]string, 0, len(res.UnsupportedSources))

	for _, u := range res.UnsupportedSources {
		ccExpectString(t, "source environment", u.Environment, "production")

		unsupported = append(unsupported, u.Path)
	}

	ccExpectStrings(t, "unsupported sources", unsupported, []string{"compose.yaml", "docker-compose.override.yml", "terraform/main.tf", "terraform/prod.tfvars"})

	ccExpectGaps(t, "override-unsignalled gaps", ccGapsOf(&res, observation.LossOverrideUnsignalled),
		ccGapWant{path: "kustomize/base/kustomization.yaml", environment: "production"},
		ccGapWant{path: "kustomize/overlays/prod/kustomization.yml"})
	ccExpectGaps(t, "unrendered-template gaps", ccGapsOf(&res, observation.LossUnrenderedTemplate),
		ccGapWant{path: "charts/api/Chart.yaml"})
}

func TestCrossCheckUnattributedSourcesAreIgnored(t *testing.T) {
	t.Parallel()

	res := observation.CrossCheck(&observation.CrossCheckInput{
		Files:     []string{"terraform/main.tf", "kustomize/kustomization.yaml", "charts/api/Chart.yaml"},
		Attribute: func(string) []string { return nil },
		Read:      func(string) ([]byte, error) { return nil, nil },
		Evidence:  &observation.EvidenceQueryResult{},
	})

	if len(res.UnsupportedSources)+len(res.Gaps)+len(res.Workloads) != 0 {
		t.Errorf("result = %+v", res)
	}
}
