package integration_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation/cbmprobe"
)

// The observations O1 to O4 of TestInfraObservations: default exclusions, Kubernetes resource identity, image
// identity, and the merge of two containers with one image repository.

// requireInfraO1DefaultExclusions checks that the default exclusions are reported as coverage loss.
func requireInfraO1DefaultExclusions(t *testing.T, snap, reinclude *infraSnapshot) {
	t.Helper()

	requireInfraO1ExcludedDirs(t, snap)
	requireInfraO1NotIndexed(t, snap)
	requireInfraO1Reincluded(t, snap, reinclude)
}

func requireInfraO1ExcludedDirs(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	dirs := snap.index.Excluded.Dirs
	if !slices.Contains(dirs, "deploy") || !slices.Contains(dirs, ".git") {
		t.Fatalf("excluded.dirs = %v, want deploy and .git", dirs)
	}

	for _, n := range snap.graph.Nodes {
		if strings.HasPrefix(n.FilePath, "deploy/") {
			t.Errorf("the default-excluded deploy/ has a node: %s", n.QualifiedName)
		}
	}
}

func requireInfraO1NotIndexed(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	if !slices.Contains(snap.index.NotIndexedFiles.Files, cbmprobe.PathReason{Path: "docs/context.png", Reason: "ignored-suffix"}) {
		t.Errorf("not_indexed_files = %v, want docs/context.png with ignored-suffix", snap.index.NotIndexedFiles.Files)
	}

	var partial *cbmprobe.PartialFile

	for i := range snap.index.ParsePartial.Files {
		if snap.index.ParsePartial.Files[i].Path == "charts/api/templates/deployment.yaml" {
			partial = &snap.index.ParsePartial.Files[i]
		}
	}

	if partial == nil || partial.ErrorRanges == "" {
		t.Errorf("parse_partial = %+v, want the Helm template with line ranges", snap.index.ParsePartial.Files)
	}
}

// requireInfraO1Reincluded checks that `.cbmignore` with `!deploy/` includes the directory again: only .git stays
// excluded.
func requireInfraO1Reincluded(t *testing.T, snap, reinclude *infraSnapshot) {
	t.Helper()

	if got := reinclude.index.Excluded.Dirs; !slices.Equal(got, []string{".git"}) {
		t.Errorf("excluded.dirs with !deploy/ = %v, want [.git]", got)
	}

	edge := reinclude.resource(t, "deploy/edge.yaml", "apps/Deployment/payments/edge")
	if edge.Prop("kind") != "Deployment" {
		t.Errorf("the re-included Deployment has kind %q", edge.Prop("kind"))
	}

	if len(reinclude.graph.Nodes) <= len(snap.graph.Nodes) {
		t.Errorf("re-including deploy/ must add nodes: %d <= %d", len(reinclude.graph.Nodes), len(snap.graph.Nodes))
	}
}

// requireInfraO2ResourceIdentity checks the identity of a Kubernetes resource.
func requireInfraO2ResourceIdentity(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	requireInfraO2ObjKeys(t, snap)
	requireInfraO2Competing(t, snap)
	requireInfraO2NoClusterProperty(t, snap)
	requireInfraO2Kustomization(t, snap)
}

func requireInfraO2ObjKeys(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	deployment := snap.resource(t, infraProdDeploy, "apps/Deployment/payments/api")
	requireProp(t, "qualified name", deployment.QualifiedName, qn("k8s.prod.deployment.yaml::apps/Deployment/payments/api"))

	for name, want := range map[string]string{"kind": "Deployment", "group": "apps", "namespace": "payments", "version": "v1", "source": "k8s"} {
		requireProp(t, "Deployment "+name, deployment.Prop(name), want)
	}

	requireProp(t, "core group", snap.resource(t, "k8s/prod/secret.yaml", "core/Secret/payments/api-secrets").Prop("group"), "core")

	// A cluster-scoped kind uses `_` in the obj_key.
	requireProp(t, "Namespace kind", snap.resource(t, "k8s/prod/namespace.yaml", "core/Namespace/_/payments").Prop("kind"), "Namespace")
	snap.resource(t, "k8s/prod/namespace.yaml", "rbac.authorization.k8s.io/ClusterRole/_/payments-reader")

	// A namespaced kind without a namespace uses `?` (Helm template, Kustomize base).
	snap.resource(t, "charts/api/templates/deployment.yaml", "apps/Deployment/?/<.Release.Name>-api")
	snap.resource(t, "kustomize/base/deployment.yaml", "apps/Deployment/?/gateway")
}

func requireInfraO2Competing(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	// The same obj_key in two files gives two nodes.
	competing := snap.resourcesByKey("apps/Deployment/payments/reports")
	requireCount(t, "nodes of the competing obj_key", len(competing), 2)

	if competing[0].QualifiedName == competing[1].QualifiedName || competing[0].FilePath == competing[1].FilePath {
		t.Errorf("competing declarations must be two nodes: %s, %s", competing[0].QualifiedName, competing[1].QualifiedName)
	}

	// A Compose service is a Resource with the ecosystem `compose`.
	compose := snap.nodeByQN(t, qn("compose::compose/Service/-/api"))
	requireProp(t, "compose ecosystem", compose.Prop("ecosystem"), "compose")
	requireProp(t, "compose service", compose.Prop("service"), "api")
}

// requireInfraO2NoClusterProperty checks that no property carries a cluster, a context or an environment.
func requireInfraO2NoClusterProperty(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	for _, n := range snap.graph.NodesOf(infraKindResource) {
		for name := range n.Props {
			if slices.Contains([]string{"cluster", "context", "environment", "env"}, name) {
				t.Errorf("%s has the property %s", n.QualifiedName, name)
			}
		}
	}
}

// requireInfraO2Kustomization checks that a Kustomization document is a Resource with no kind, group or obj_key.
func requireInfraO2Kustomization(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	kustomization := snap.nodeByQN(t, qn("kustomize.overlays.prod.kustomization.Kustomization"))
	requireProp(t, "Kustomization label", kustomization.Label(), infraKindResource)

	for _, name := range []string{"kind", "group", "obj_key"} {
		if _, has := kustomization.Props[name]; has {
			t.Errorf("Kustomization has the property %s", name)
		}
	}
}

// requireInfraO3ImageIdentity checks that the identity of an image is its repository.
func requireInfraO3ImageIdentity(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	node := requireInfraO3APINode(t, snap)

	// Prod and staging use the same repository: one node, two edges with the tag in the edge.
	into := snap.edgesWhere(infraEdgeUsage, func(e *cbmprobe.Edge) bool { return e.To == node })
	requireCount(t, "USAGE edges into the api Image node", len(into), 2)

	// A short Docker Hub name gets `registry: unknown`.
	requireProp(t, "short name registry", snap.nodeByQN(t, "__image__postgres").Prop("registry"), "unknown")

	// A digest reference keeps the form repo@sha256:...
	worker := snap.resource(t, "k8s/staging/worker.yaml", "apps/Deployment/payments/worker")

	usage := snap.edgesFrom(infraEdgeUsage, worker)
	requireCount(t, "worker USAGE edges", len(usage), 1)

	if !strings.HasPrefix(usage[0].Prop("image"), "registry.example.com/payments/worker@sha256:") {
		t.Errorf("digest reference = %q", usage[0].Prop("image"))
	}
}

// requireInfraO3APINode checks the one Image node of the api repository and gives it.
func requireInfraO3APINode(t *testing.T, snap *infraSnapshot) *cbmprobe.Node {
	t.Helper()

	var apiNodes []*cbmprobe.Node

	for _, n := range snap.graph.NodesOf(infraLabelImage) {
		if n.QualifiedName == infraAPIImage {
			apiNodes = append(apiNodes, n)
		}
	}

	requireCount(t, "Image nodes of one repository", len(apiNodes), 1)

	node := apiNodes[0]
	requireProp(t, "registry", node.Prop("registry"), "registry.example.com")
	requireProp(t, "repository", node.Prop("repository"), "registry.example.com/payments/api")

	for name := range node.Props {
		if slices.Contains([]string{"tag", "digest", "image", "version"}, name) {
			t.Errorf("an Image node must have no %s", name)
		}
	}

	return node
}

// requireInfraO4MergedImageUsage checks that two containers with one image repository give one USAGE edge.
func requireInfraO4MergedImageUsage(t *testing.T, snap *infraSnapshot) {
	t.Helper()

	deployment := snap.resource(t, infraProdDeploy, "apps/Deployment/payments/api")

	var images []*cbmprobe.Edge

	for _, e := range snap.edgesFrom(infraEdgeUsage, deployment) {
		if e.Prop("ref") == "image" {
			images = append(images, e)
		}
	}

	requireCount(t, "image USAGE edges of the Deployment", len(images), 1)
	requireProp(t, "container", images[0].Prop("container"), "sidecar")
	requireProp(t, "image", images[0].Prop("image"), "registry.example.com/payments/api:1.3.0")

	// The `app` occurrence (:1.2.0) is lost, and nothing in the graph says so.
	for _, e := range snap.graph.Edges {
		if strings.Contains(e.Prop("image"), "payments/api:1.2.0") && e.From == deployment {
			t.Errorf("the lost occurrence is visible: %v", e.Props)
		}
	}

	if got := propNames(images); !slices.Equal(got, []string{"container", "image", "ref"}) {
		t.Errorf("the merged edge has the properties %v, want container, image, ref only", got)
	}
}
