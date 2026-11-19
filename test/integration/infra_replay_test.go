package integration_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/observation/cbmprobe"
)

// These tests replay the recordings of the infra fixture (test/fixtures/infra)
// that `mise run record-cbm` made with codebase-memory-mcp 0.12.1. Each subtest
// of TestInfraObservations asserts the fact of one observation O1 to O12 listed in
// test/fixtures/infra/README.md. They need no CBM.

const (
	infraRecordingFile = "../fixtures/cbm-recordings/infra.json"
	infraReincludeFile = "../fixtures/cbm-recordings/infra-reinclude-deploy.json"
	infraSeedsFile     = "../fixtures/infra-seeds.json"
	infraProject       = "concordia-fixture-infra"

	infraKindResource  = "Resource"
	infraLabelImage    = "Image"
	infraLabelModule   = "Module"
	infraLabelFile     = "File"
	infraLabelChart    = "Chart"
	infraLabelEnvVar   = "EnvVar"
	infraEdgeUsage     = "USAGE"
	infraEdgeDependsOn = "DEPENDS_ON"
	infraEdgeConfig    = "CONFIGURES"
	infraEdgeMaps      = "INFRA_MAPS"
	infraEdgeDeploys   = "DEPLOYS"
	infraEdgeSpawns    = "SPAWNS"
	infraEdgeOverride  = "OVERRIDE"
	infraEdgeImports   = "IMPORTS"
	infraEdgeMentions  = "MENTIONS"
	infraEdgeSupersede = "SUPERSEDES"
	infraEdgeRefsFile  = "REFERENCES_FILE"
	infraEdgeCalls     = "CALLS"
	infraAPIImage      = "__image__registry.example.com/payments/api"
	infraProdDeploy    = "k8s/prod/deployment.yaml"
)

// infraSnapshot is a replayed recording: the result of the index call and the whole graph.
type infraSnapshot struct {
	graph *cbmprobe.Graph
	rec   observation.Recording
	index cbmprobe.IndexResult
}

// replayInfra reads a recording with the same code that recorded it. The
// repository path is not part of the key of the index call, so any path works.
func replayInfra(t *testing.T, file string) *infraSnapshot {
	t.Helper()

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the recording: %v", err)
	}

	var rec observation.Recording
	if err = json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("parse the recording: %v", err)
	}

	client := observation.NewReplayClient(rec)

	index, err := cbmprobe.Index(t.Context(), client, "/anywhere/else", infraProject)
	if err != nil {
		t.Fatalf("replay the index call: %v", err)
	}

	graph, err := cbmprobe.Read(t.Context(), client, infraProject)
	if err != nil {
		t.Fatalf("replay the graph: %v", err)
	}

	return &infraSnapshot{graph: graph, index: index, rec: rec}
}

// qn gives the qualified name of a project node.
func qn(rest string) string {
	return infraProject + "." + rest
}

// resource gives the Resource node of a manifest document, by file and obj_key.
func (s *infraSnapshot) resource(t *testing.T, file, objKey string) *cbmprobe.Node {
	t.Helper()

	for _, n := range s.graph.NodesOf(infraKindResource) {
		if n.FilePath == file && n.Prop("obj_key") == objKey {
			return n
		}
	}

	t.Fatalf("no Resource %s in %s", objKey, file)

	return nil
}

// resourcesByKey gives all Resource nodes with an obj_key.
func (s *infraSnapshot) resourcesByKey(objKey string) []*cbmprobe.Node {
	var out []*cbmprobe.Node

	for _, n := range s.graph.NodesOf(infraKindResource) {
		if n.Prop("obj_key") == objKey {
			out = append(out, n)
		}
	}

	return out
}

// edgesFrom gives the edges of one type that start at a node.
func (s *infraSnapshot) edgesFrom(edgeType string, from *cbmprobe.Node) []*cbmprobe.Edge {
	var out []*cbmprobe.Edge

	for _, e := range s.graph.EdgesOf(edgeType) {
		if e.From == from {
			out = append(out, e)
		}
	}

	return out
}

// edgesWhere gives the edges of one type for which keep is true.
func (s *infraSnapshot) edgesWhere(edgeType string, keep func(*cbmprobe.Edge) bool) []*cbmprobe.Edge {
	var out []*cbmprobe.Edge

	for _, e := range s.graph.EdgesOf(edgeType) {
		if keep(e) {
			out = append(out, e)
		}
	}

	return out
}

func (s *infraSnapshot) nodeByQN(t *testing.T, name string) *cbmprobe.Node {
	t.Helper()

	for _, n := range s.graph.Nodes {
		if n.QualifiedName == name {
			return n
		}
	}

	t.Fatalf("no node with the qualified name %q", name)

	return nil
}

func (s *infraSnapshot) fileNode(t *testing.T, file string) *cbmprobe.Node {
	t.Helper()

	for _, n := range s.graph.NodesOf(infraLabelFile) {
		if n.FilePath == file {
			return n
		}
	}

	t.Fatalf("no File node %s", file)

	return nil
}

// propNames gives the sorted property names of an edge set.
func propNames(edges []*cbmprobe.Edge) []string {
	set := map[string]struct{}{}

	for _, e := range edges {
		for name := range e.Props {
			set[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

func requireCount(t *testing.T, what string, got, want int) {
	t.Helper()

	if got != want {
		t.Fatalf("%s: got %d, want %d", what, got, want)
	}
}

func requireProp(t *testing.T, what, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s: got %q, want %q", what, got, want)
	}
}

func TestInfraRecordingHasTheVersionAndTheProject(t *testing.T) {
	for _, file := range []string{infraRecordingFile, infraReincludeFile} {
		snap := replayInfra(t, file)

		requireProp(t, file+" version", snap.rec.Version, "0.12.1")

		if snap.rec.HeadSHA == "" {
			t.Errorf("%s has no head SHA", file)
		}

		requireProp(t, file+" project", snap.index.Project, infraProject)

		if snap.index.Nodes != len(snap.graph.Nodes) {
			t.Errorf("%s: index_repository says %d nodes, the graph has %d", file, snap.index.Nodes, len(snap.graph.Nodes))
		}
	}
}

func mustRead(t *testing.T, file string) []byte {
	t.Helper()

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}

	return data
}

// TestInfraRecordingsHoldNoSeededSecret checks that no seeded secret reached
// the CBM database (O10) or a recording, in clear text or in base64.
func TestInfraRecordingsHoldNoSeededSecret(t *testing.T) {
	var seeds []struct {
		Path  string `json:"path"`
		Class string `json:"class"`
		Value string `json:"value"`
	}

	if err := json.Unmarshal(mustRead(t, infraSeedsFile), &seeds); err != nil {
		t.Fatalf("parse the seeds: %v", err)
	}

	if len(seeds) < 9 {
		t.Fatalf("expected the seeds of the contract, got %d", len(seeds))
	}

	for _, file := range []string{infraRecordingFile, infraReincludeFile} {
		data := mustRead(t, file)

		for _, seed := range seeds {
			for _, needle := range []string{seed.Value, base64.StdEncoding.EncodeToString([]byte(seed.Value))} {
				if bytes.Contains(data, []byte(needle)) {
					t.Errorf("%s contains the seed %s (%s)", file, seed.Class, seed.Path)
				}
			}
		}
	}
}

// infraObservationCase is one observation O1 to O12 and the check of its fact.
type infraObservationCase struct {
	check func(t *testing.T, snap, reinclude *infraSnapshot)
	name  string
}

// infraSnapshotOnly adapts a check that reads only the first snapshot to the signature of an observation check.
func infraSnapshotOnly(check func(t *testing.T, snap *infraSnapshot)) func(t *testing.T, snap, reinclude *infraSnapshot) {
	return func(t *testing.T, snap, _ *infraSnapshot) {
		t.Helper()

		check(t, snap)
	}
}

func infraObservationCases() []infraObservationCase {
	return []infraObservationCase{
		{name: "O1 default exclusions are reported as coverage loss", check: requireInfraO1DefaultExclusions},
		{name: "O2 Kubernetes resource identity", check: infraSnapshotOnly(requireInfraO2ResourceIdentity)},
		{name: "O3 image identity is the repository", check: infraSnapshotOnly(requireInfraO3ImageIdentity)},
		{name: "O4 two containers with one image repository give one USAGE edge", check: infraSnapshotOnly(requireInfraO4MergedImageUsage)},
		{name: "O5 reference USAGE edges and undeclared targets", check: infraSnapshotOnly(requireInfraO5ReferenceUsage)},
		{name: "O6 selector joins are scoped to the directory and silent about gaps", check: infraSnapshotOnly(requireInfraO6SelectorJoins)},
		{name: "O7 routes", check: infraSnapshotOnly(requireInfraO7Routes)},
		{name: "O8 deployment actions", check: infraSnapshotOnly(requireInfraO8DeploymentActions)},
		{name: "O9 build and image lineage", check: infraSnapshotOnly(requireInfraO9Lineage)},
		{name: "O10 configuration", check: infraSnapshotOnly(requireInfraO10Configuration)},
		{name: "O11 other infrastructure families", check: infraSnapshotOnly(requireInfraO11OtherFamilies)},
		{name: "O12 documentation, decisions and diagrams", check: infraSnapshotOnly(requireInfraO12Documentation)},
	}
}

func TestInfraObservations(t *testing.T) {
	snap := replayInfra(t, infraRecordingFile)
	reinclude := replayInfra(t, infraReincludeFile)

	for _, tc := range infraObservationCases() {
		t.Run(tc.name, func(t *testing.T) { tc.check(t, snap, reinclude) })
	}
}
