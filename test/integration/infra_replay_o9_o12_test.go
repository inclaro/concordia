package integration_test

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation/cbmprobe"
)

// The observations O9 to O12 of TestInfraObservations: build and image lineage, configuration, the other
// infrastructure families, and documentation, decisions and diagrams.

// requireInfraO9Lineage checks the build and image lineage.
func requireInfraO9Lineage(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	dockerfile := snap.nodeByQN(t, qn("docker.Dockerfile"))

	requireInfraO9FromEdges(t, snap, dockerfile)
	requireInfraO9CopyFrom(t, snap)
	requireInfraO9Builds(t, snap, dockerfile)
	requireInfraO9ImageDependsOn(t, snap)
	requireInfraO9ServiceAndChartDeps(t, snap)
}

// isInfraO9FromEdge is true for a Dockerfile -> Image edge that holds the image and the stage.
func isInfraO9FromEdge(e *cbmprobe.Edge, dockerfile *cbmprobe.Node) bool {
	if e.From != dockerfile || e.To.Label() != infraLabelImage {
		return false
	}

	return e.Prop("image") != "" && e.Prop("stage") != ""
}

func requireInfraO9FromEdges(t *testing.T, snap *infraSnapshot, dockerfile *cbmprobe.Node) {
	t.Helper()

	from := snap.edgesPropEq(infraEdgeDependsOn, "via", "from")
	requireCount(t, "from edges", len(from), 2)

	for _, e := range from {
		if !isInfraO9FromEdge(e, dockerfile) {
			t.Errorf("from edge %s -> %s %v", e.From.QualifiedName, e.To.QualifiedName, e.Props)
		}
	}
}

// requireInfraO9CopyFrom checks that `COPY --from=<stage name>` gave no edge; only the external image has one.
func requireInfraO9CopyFrom(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	copyFrom := snap.edgesPropEq(infraEdgeDependsOn, "via", "copy-from")
	requireCount(t, "copy-from edges", len(copyFrom), 1)
	requireProp(t, "copy-from image", copyFrom[0].Prop("image"), "ghcr.io/example/healthcheck:1.0.0")
	requireProp(t, "copy-from stage", copyFrom[0].Prop("stage"), "1")
	requireProp(t, "copy-from target", copyFrom[0].To.QualifiedName, "__image__ghcr.io/example/healthcheck")
}

// requireInfraO9Builds checks that a build command links an Image to a Dockerfile.
func requireInfraO9Builds(t *testing.T, snap *infraSnapshot, dockerfile *cbmprobe.Node) {
	t.Helper()

	builds := snap.edgesPropEq(infraEdgeDependsOn, "via", "build")
	requireCount(t, "build edges", len(builds), 2)

	var imageBuild, composeBuild *cbmprobe.Edge

	for _, e := range builds {
		switch e.From.Label() {
		case infraLabelImage:
			imageBuild = e
		case infraKindResource:
			composeBuild = e
		}
	}

	if imageBuild == nil || composeBuild == nil {
		t.Fatalf("build edges = %v", builds)
	}

	requireProp(t, "image build tool", imageBuild.Prop("tool"), "docker-build")
	requireProp(t, "image build file", imageBuild.Prop("file"), "scripts/build.sh")
	requireProp(t, "image build image", imageBuild.Prop("image"), "registry.example.com/payments/api:1.2.0")
	requireProp(t, "image build source", imageBuild.From.QualifiedName, infraAPIImage)
	requireProp(t, "image build target", imageBuild.To.QualifiedName, dockerfile.QualifiedName)
	requireProp(t, "compose build tool", composeBuild.Prop("tool"), "compose")
	requireProp(t, "compose build target", composeBuild.To.QualifiedName, dockerfile.QualifiedName)
}

// requireInfraO9ImageDependsOn checks that a Kubernetes image reference alone does not link to a Dockerfile.
func requireInfraO9ImageDependsOn(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	for _, n := range snap.graph.NodesOf(infraLabelImage) {
		if n.QualifiedName == infraAPIImage {
			continue
		}

		for _, e := range snap.edgesFrom(infraEdgeDependsOn, n) {
			t.Errorf("the Image %s has a DEPENDS_ON edge %v", n.QualifiedName, e.Props)
		}
	}
}

func requireInfraO9ServiceAndChartDeps(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	// Compose depends_on is a service to service edge.
	dependsOn := snap.edgesPropEq(infraEdgeDependsOn, "via", "depends_on")
	requireCount(t, "depends_on edges", len(dependsOn), 1)
	requireProp(t, "depends_on from", dependsOn[0].From.Prop("service"), "api")
	requireProp(t, "depends_on to", dependsOn[0].To.Prop("service"), "db")

	// A Chart dependency has no `via`; its property is `repository`.
	chartDeps := snap.edgesWhere(infraEdgeDependsOn, func(e *cbmprobe.Edge) bool { return e.From.Label() == infraLabelChart })
	requireCount(t, "chart dependency edges", len(chartDeps), 1)

	if got := propNames(chartDeps); !slices.Equal(got, []string{"repository"}) {
		t.Errorf("chart dependency properties = %v, want [repository]", got)
	}

	requireProp(t, "chart dependency target", chartDeps[0].To.Label(), infraLabelChart)
}

// requireInfraO10Configuration checks the CONFIGURES edges and the EnvVar nodes.
func requireInfraO10Configuration(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	strategies := requireInfraO10Strategies(t, snap)

	requireInfraO10EnvSetCoverage(t, strategies["env_set"])
	requireInfraO10EnvSetTarget(t, snap)

	// env_read has the property `dockerfile`.
	for _, e := range strategies["env_read"] {
		requireProp(t, "env_read dockerfile", e.Prop("dockerfile"), "docker/Dockerfile")
		requireProp(t, "env_read source", e.From.Label(), infraLabelEnvVar)
	}

	// env_access targets a project-wide EnvVar.
	access := strategies["env_access"]
	requireCount(t, "env_access edges", len(access), 1)
	requireProp(t, "env_access target", access[0].To.QualifiedName, "__env__LISTEN_ADDR")

	// helm_values goes from values.yaml to the Chart.
	helm := strategies["helm_values"]
	requireCount(t, "helm_values edges", len(helm), 1)
	requireProp(t, "helm_values source", helm[0].From.FilePath, "charts/api/values.yaml")
	requireProp(t, "helm_values target", helm[0].To.Label(), infraLabelChart)

	requireInfraO10EnvVarNodes(t, snap)
}

// requireInfraO10Strategies groups the CONFIGURES edges by strategy and checks the strategies.
func requireInfraO10Strategies(t *testing.T, snap *infraSnapshot) map[string][]*cbmprobe.Edge {
	t.Helper()

	strategies := map[string][]*cbmprobe.Edge{}

	for _, e := range snap.graph.EdgesOf(infraEdgeConfig) {
		strategies[e.Prop("strategy")] = append(strategies[e.Prop("strategy")], e)
	}

	names := make([]string, 0, len(strategies))
	for name := range strategies {
		names = append(names, name)
	}

	slices.Sort(names)

	// `key_symbol` did not occur, and `helm_values` is not in PRD section 6.3.
	if !slices.Equal(names, []string{"env_access", "env_read", "env_set", "helm_values"}) {
		t.Fatalf("CONFIGURES strategies = %v", names)
	}

	return strategies
}

// requireInfraO10EnvSetCoverage checks that the env_set edges have a line and cover the scopes and the sources.
func requireInfraO10EnvSetCoverage(t *testing.T, envSet []*cbmprobe.Edge) {
	t.Helper()

	scopes, sources := map[string]bool{}, map[string]bool{}

	for _, e := range envSet {
		scopes[e.Prop("scope")] = true
		sources[e.Prop("source")] = true

		if e.Prop("line") == "" {
			t.Errorf("env_set edge without a line: %s", e.To.QualifiedName)
		}
	}

	for _, scope := range []string{"container", "image"} {
		if !scopes[scope] {
			t.Errorf("no env_set edge with scope %s", scope)
		}
	}

	for _, source := range []string{"value", "secretKeyRef", "dockerfile", "compose"} {
		if !sources[source] {
			t.Errorf("no env_set edge with source %s", source)
		}
	}
}

// requireInfraO10EnvSetTarget checks that an env_set target is scoped to the resource and the container.
func requireInfraO10EnvSetTarget(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	deployment := snap.resource(t, infraProdDeploy, "apps/Deployment/payments/api")

	var dbPassword *cbmprobe.Edge

	for _, e := range snap.edgesFrom(infraEdgeConfig, deployment) {
		if e.To.Prop("env_key") == "DB_PASSWORD" {
			dbPassword = e
		}
	}

	if dbPassword == nil {
		t.Fatal("no env_set edge for DB_PASSWORD")
	}

	requireProp(t, "env_set target", dbPassword.To.QualifiedName, deployment.QualifiedName+"#env/app/DB_PASSWORD")
	requireProp(t, "env_set container", dbPassword.Prop("container"), "app")
	requireProp(t, "env_set source", dbPassword.Prop("source"), "value")
}

// requireInfraO10EnvVarNodes checks that EnvVar nodes carry env_key, scope, container and source, and no value.
func requireInfraO10EnvVarNodes(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	for _, n := range snap.graph.NodesOf(infraLabelEnvVar) {
		for name := range n.Props {
			if !slices.Contains([]string{"env_key", "scope", "container", "source"}, name) {
				t.Errorf("EnvVar %s has the property %s", n.QualifiedName, name)
			}
		}

		if n.Prop("env_key") == "" {
			t.Errorf("EnvVar %s has no env_key", n.QualifiedName)
		}
	}
}

// requireInfraO11OtherFamilies checks the other infrastructure families.
func requireInfraO11OtherFamilies(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	requireInfraO11ConnectsTo(t, snap)
	requireInfraO11Spawns(t, snap)
	requireInfraO11Overrides(t, snap)
	requireInfraO11TerraformImports(t, snap)
	requireInfraO11KustomizeImports(t, snap)

	// Helm: a templated name stays literal; a templated image gave no USAGE edge.
	template := snap.resource(t, "charts/api/templates/deployment.yaml", "apps/Deployment/?/<.Release.Name>-api")
	requireCount(t, "USAGE edges of the templated Deployment", len(snap.edgesFrom(infraEdgeUsage, template)), 0)
}

func requireInfraO11ConnectsTo(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	connects := snap.graph.EdgesOf("CONNECTS_TO")
	requireCount(t, "CONNECTS_TO edges", len(connects), 1)

	for name, want := range map[string]string{"via": "env", "key": "DATABASE_URL", "container": "api"} {
		requireProp(t, "CONNECTS_TO "+name, connects[0].Prop(name), want)
	}

	requireProp(t, "CONNECTS_TO from", connects[0].From.Prop("service"), "api")
	requireProp(t, "CONNECTS_TO to", connects[0].To.Prop("service"), "db")
}

// hasInfraSpawnSources is true when SPAWNS has the shell, dockerfile and GitHub Actions build sources.
func hasInfraSpawnSources(apis, ci map[string]bool) bool {
	return apis["shell"] && apis["dockerfile"] && ci["github-actions/build"]
}

// requireInfraO11Spawns checks SPAWNS from a Dockerfile, a shell script, a Makefile and a CI job.
func requireInfraO11Spawns(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	apis, ci := map[string]bool{}, map[string]bool{}

	var entrypoint, makeTarget *cbmprobe.Edge

	for _, e := range snap.graph.EdgesOf(infraEdgeSpawns) {
		apis[e.Prop("api")] = true

		if e.Prop("ci") != "" {
			ci[e.Prop("ci")+"/"+e.Prop("job")] = true
		}

		if e.Prop("instruction") != "" {
			entrypoint = e
		}

		if e.Prop("target") != "" {
			makeTarget = e
		}
	}

	if !hasInfraSpawnSources(apis, ci) {
		t.Errorf("SPAWNS apis %v, ci %v", apis, ci)
	}

	if entrypoint == nil || makeTarget == nil {
		t.Fatalf("SPAWNS lacks an ENTRYPOINT edge or a Make target edge")
	}

	requireProp(t, "instruction", entrypoint.Prop("instruction"), "ENTRYPOINT")
	requireProp(t, "program", entrypoint.Prop("program"), "/usr/local/bin/api")
	requireProp(t, "make target", makeTarget.Prop("target"), "test")
	requireProp(t, "make path", makeTarget.Prop("path"), "Makefile")
	requireProp(t, "confidence", entrypoint.Prop("confidence"), "1.00")
}

// isInfraTfvarsAndPatchVias is true for two tfvars edges, one patch edge, and no other via.
func isInfraTfvarsAndPatchVias(vias map[string]int) bool {
	return vias["tfvars"] == 2 && vias["patch"] == 1 && len(vias) == 2
}

// requireInfraO11Overrides checks that OVERRIDE has tfvars and Kustomize patches only.
func requireInfraO11Overrides(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	vias := map[string]int{}

	for _, e := range snap.graph.EdgesOf(infraEdgeOverride) {
		vias[e.Prop("via")]++
	}

	if !isInfraTfvarsAndPatchVias(vias) {
		t.Errorf("OVERRIDE vias = %v", vias)
	}

	patches := snap.edgesPropEq(infraEdgeOverride, "via", "patch")
	requireProp(t, "patch kustomization", patches[0].Prop("kustomization"), "kustomize/overlays/prod/kustomization.yaml")
	requireProp(t, "patch file", patches[0].Prop("patch"), "kustomize/overlays/prod/patch-replicas.yaml")
	requireProp(t, "patch target", patches[0].To.FilePath, "kustomize/base/deployment.yaml")

	// A Kustomize `images:` `newTag` override gave no edge: nothing leaves the overlay kustomization
	// except the IMPORTS edges, and no OVERRIDE edge starts at it.
	overlay := snap.nodeByQN(t, qn("kustomize.overlays.prod.kustomization.Kustomization"))
	requireCount(t, "OVERRIDE edges of the overlay", len(snap.edgesFrom(infraEdgeOverride, overlay)), 0)

	for _, e := range snap.graph.Edges {
		if strings.Contains(e.Prop("image"), "gateway:1.1.0") || strings.Contains(e.Prop("kustomization"), "newTag") {
			t.Errorf("the newTag override is visible in %s: %v", e.Type, e.Props)
		}
	}
}

// requireInfraO11TerraformImports checks that infrastructure IMPORTS go from a Terraform module to a Folder with
// via and local_name.
func requireInfraO11TerraformImports(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	terraform := snap.edgesPropEq(infraEdgeImports, "via", "terraform")
	requireCount(t, "Terraform IMPORTS edges", len(terraform), 1)
	requireProp(t, "Terraform IMPORTS local_name", terraform[0].Prop("local_name"), "network")
	requireProp(t, "Terraform IMPORTS target", terraform[0].To.Label(), "Folder")
}

// requireInfraO11KustomizeImports checks that `kustomization.yaml` IMPORTS has a local_name and NO via, like code
// imports.
func requireInfraO11KustomizeImports(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	kustomize := snap.edgesWhere(infraEdgeImports, func(e *cbmprobe.Edge) bool { return path.Base(e.From.FilePath) == "kustomization.yaml" })
	requireCount(t, "kustomization IMPORTS edges", len(kustomize), 3)

	localNames := map[string]bool{}

	for _, e := range kustomize {
		requireProp(t, "kustomization IMPORTS source label", e.From.Label(), infraLabelFile)

		if _, has := e.Props["via"]; has {
			t.Errorf("kustomization IMPORTS has a via: %v", e.Props)
		}

		localNames[e.Prop("local_name")] = true
	}

	if !hasInfraSyntaxes(localNames, 2, "resources", "patches") {
		t.Errorf("kustomization local_name values = %v", localNames)
	}
}

// requireInfraO12Documentation checks the documentation, decisions and diagrams.
func requireInfraO12Documentation(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	requireInfraO12Decisions(t, snap)
	requireInfraO12Mentions(t, snap)
	requireInfraO12ReferencesFile(t, snap)

	// The editable ADR store is separate from ADR nodes: it reported false while two ADR files were indexed.
	if snap.index.ADRPresent {
		t.Error("adr_present is true; the ADR store is separate from the ADR nodes")
	}

	requireInfraO12Diagram(t, snap)
	requireInfraO12TestEdges(t, snap)
}

// hasInfraTwoDecisions is true when the first and the second ADR exist and are the only ADR nodes.
func hasInfraTwoDecisions(adrs map[string]*cbmprobe.Node, first, second *cbmprobe.Node) bool {
	return first != nil && second != nil && len(adrs) == 2
}

func requireInfraO12Decisions(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	adrs := map[string]*cbmprobe.Node{}

	for _, n := range snap.graph.NodesOf("ADR") {
		adrs[n.Prop("adr_id")] = n
	}

	// The ID comes from the number in the title: 0001 gives ADR-1.
	first, second := adrs["ADR-1"], adrs["ADR-2"]
	if !hasInfraTwoDecisions(adrs, first, second) {
		t.Fatalf("ADR nodes = %v", adrs)
	}

	requireProp(t, "first status", first.Prop("status"), "superseded")
	requireProp(t, "first status_text", first.Prop("status_text"), "Superseded by ADR-0002")
	requireProp(t, "first date", first.Prop("date"), "2026-01-10")
	requireProp(t, "first title", first.Prop("title"), "ADR-0001: Use self-hosted Postgres")
	requireProp(t, "second status", second.Prop("status"), "accepted")

	// superseded_by is the link target text, not an ADR ID.
	requireProp(t, "superseded_by", first.Prop("superseded_by"), "0002-use-managed-postgres.md")

	supersedes := snap.graph.EdgesOf(infraEdgeSupersede)
	requireCount(t, "SUPERSEDES edges", len(supersedes), 1)

	if supersedes[0].From != second || supersedes[0].To != first {
		t.Errorf("SUPERSEDES goes %s -> %s, want new -> old", supersedes[0].From.QualifiedName, supersedes[0].To.QualifiedName)
	}

	for name, want := range map[string]string{"via": "markdown", "syntax": "supersedes", "tier": "exact", "line": "7"} {
		requireProp(t, "SUPERSEDES "+name, supersedes[0].Prop(name), want)
	}
}

// hasInfraSyntaxes is true when the set holds the required syntaxes and at most max kinds in all.
func hasInfraSyntaxes(set map[string]bool, maxKinds int, required ...string) bool {
	if len(set) > maxKinds {
		return false
	}

	return !slices.ContainsFunc(required, func(name string) bool { return !set[name] })
}

// requireInfraO12Mentions checks MENTIONS: Section -> File with syntax, tier, line and count (read with properties(r)).
func requireInfraO12Mentions(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	syntaxes := map[string]bool{}

	for _, e := range snap.graph.EdgesOf(infraEdgeMentions) {
		requireProp(t, "MENTIONS source label", e.From.Label(), "Section")

		if label := e.To.Label(); label != infraLabelFile && label != "Folder" {
			t.Errorf("MENTIONS target label = %q, want File or Folder", label)
		}

		requireProp(t, "MENTIONS tier", e.Prop("tier"), "exact")

		if _, isNumber := e.Props["count"].(json.Number); !isNumber {
			t.Errorf("MENTIONS count is not a number: %v", e.Props["count"])
		}

		if e.Prop("line") == "" {
			t.Errorf("MENTIONS without a line: %s", e.From.QualifiedName)
		}

		syntaxes[e.Prop("syntax")] = true
	}

	// The README of the fixture lists paths in plain text, which CBM reads with the syntax `path`.
	if !hasInfraSyntaxes(syntaxes, 3, "link", "code_path") {
		t.Errorf("MENTIONS syntaxes = %v", syntaxes)
	}
}

// requireInfraO12ReferencesFile checks REFERENCES_FILE: File -> File with a strategy and a numeric confidence.
func requireInfraO12ReferencesFile(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	strategies := map[string]bool{}

	for _, e := range snap.graph.EdgesOf(infraEdgeRefsFile) {
		strategies[e.Prop("strategy")] = true

		if _, isNumber := e.Props["confidence"].(json.Number); !isNumber {
			t.Errorf("REFERENCES_FILE confidence is not a number: %v", e.Props["confidence"])
		}
	}

	if !hasInfraSyntaxes(strategies, 3, "md_inline_link", "md_backtick_path") {
		t.Errorf("REFERENCES_FILE strategies = %v", strategies)
	}
}

// requireInfraO12Diagram checks that a diagram has the format mermaid, the kind flowchart, and a qualified name with
// an ordinal, and checks its elements and arrows.
func requireInfraO12Diagram(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	diagrams := snap.graph.NodesOf("Diagram")
	requireCount(t, "Diagram nodes", len(diagrams), 1)
	requireProp(t, "diagram_kind", diagrams[0].Prop("diagram_kind"), "flowchart")
	requireProp(t, "format", diagrams[0].Prop("format"), "mermaid")

	if !strings.HasSuffix(diagrams[0].QualifiedName, "#diagram-1") || !strings.Contains(diagrams[0].QualifiedName, "docs.architecture.") {
		t.Errorf("Diagram qualified name = %s", diagrams[0].QualifiedName)
	}

	ids := map[string]bool{}

	for _, n := range snap.graph.NodesOf("DiagramElement") {
		ids[n.Prop("element_id")] = true

		requireProp(t, "element_kind", n.Prop("element_kind"), "node")
		requireProp(t, "parent_class", n.Prop("parent_class"), diagrams[0].QualifiedName)
	}

	for _, id := range []string{"gateway", "api", "payments", "worker"} {
		if !ids[id] {
			t.Errorf("no DiagramElement with element_id %s", id)
		}
	}

	requireInfraO12Arrows(t, snap)
}

func requireInfraO12Arrows(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	arrows := snap.graph.EdgesOf("DIAGRAM_ARROW")
	requireCount(t, "DIAGRAM_ARROW edges", len(arrows), 3)

	labelled := 0

	for _, e := range arrows {
		requireProp(t, "arrow style", e.Prop("style"), "arrow")

		if e.Prop("label") == "sql" {
			labelled++
		}
	}

	requireCount(t, "labelled arrows", labelled, 1)
}

// requireInfraO12TestEdges checks that TESTS and TESTS_FILE have no properties and that a CALLS edge from a test is
// lsp_direct with 0.95.
func requireInfraO12TestEdges(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	for _, edgeType := range []string{"TESTS", "TESTS_FILE"} {
		edges := snap.graph.EdgesOf(edgeType)
		requireCount(t, edgeType+" edges", len(edges), 1)
		requireCount(t, edgeType+" properties", len(edges[0].Props), 0)
	}

	testCalls := snap.edgesWhere(infraEdgeCalls, func(e *cbmprobe.Edge) bool { return e.From.Name == "TestGreeting" })
	requireCount(t, "CALLS edges of the test", len(testCalls), 1)
	requireProp(t, "CALLS strategy", testCalls[0].Prop("strategy"), "lsp_direct")
	requireProp(t, "CALLS confidence", testCalls[0].Prop("confidence"), "0.95")
}
