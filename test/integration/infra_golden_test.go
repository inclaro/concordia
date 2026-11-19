package integration_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation/cbmprobe"
)

// The golden test/golden/infra/native-discriminators.json lists, for each
// native CBM edge type of the infra recording, the property names and the
// values of the discriminators `via`, `ref`, `kind`, `strategy`, `syntax` and
// `tier`. The rule table of the evidence normalization (task 4.2) is built
// from it. To write it again after a new recording:
//
//	CONCORDIA_UPDATE_GOLDEN=1 go test ./test/integration -run TestInfraNativeDiscriminatorsGolden

const (
	infraGoldenFile   = "../golden/infra/native-discriminators.json"
	updateGoldenEnv   = "CONCORDIA_UPDATE_GOLDEN"
	goldenFileMode    = 0o644
	goldenDirMode     = 0o755
	infraGoldenSchema = "concordia-native-discriminators/v1"
)

// discriminatorNames are the edge properties that tell the meaning of an edge.
var discriminatorNames = []string{"via", "ref", "kind", "strategy", "syntax", "tier"}

// nodeLabelGolden describes the nodes of one label.
type nodeLabelGolden struct {
	Properties []string `json:"properties"`
	Count      int      `json:"count"`
}

// combinationGolden is one distinct shape of an edge: where it starts and ends,
// and which discriminators it has. SourceFileType is the file extension of the
// source node, or the file name for a file without extension and for kustomization files.
type combinationGolden struct {
	Discriminators map[string]string `json:"discriminators"`
	SourceLabel    string            `json:"sourceLabel"`
	SourceFileType string            `json:"sourceFileType"`
	TargetLabel    string            `json:"targetLabel"`
	TargetKind     string            `json:"targetKind"`
	Count          int               `json:"count"`
}

// edgeTypeGolden describes the edges of one native type.
type edgeTypeGolden struct {
	// Discriminators maps a discriminator name to its values and their number of edges.
	Discriminators map[string]map[string]int `json:"discriminators"`
	Properties     []string                  `json:"properties"`
	Combinations   []combinationGolden       `json:"combinations"`
	Count          int                       `json:"count"`
}

type nativeDiscriminatorsGolden struct {
	NodeLabels map[string]nodeLabelGolden `json:"nodeLabels"`
	EdgeTypes  map[string]edgeTypeGolden  `json:"edgeTypes"`
	Schema     string                     `json:"schema"`
	Recording  string                     `json:"recording"`
	CbmVersion string                     `json:"cbmVersion"`
}

// fileTypeOf gives the file type of a path: the extension, or the base name
// for a file without extension (`Dockerfile`, `Makefile`) and for kustomization files.
func fileTypeOf(filePath string) string {
	base := path.Base(filePath)
	if filePath == "" {
		return ""
	}

	if strings.HasPrefix(base, "kustomization.") {
		return base
	}

	if ext := path.Ext(base); ext != "" {
		return ext
	}

	return base
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

// goldenSummary collects the nodes and edges of a graph into the golden: the counts and the sets of property names
// that become sorted lists once the whole graph has been read.
type goldenSummary struct {
	nodeProps map[string]map[string]struct{}
	edgeProps map[string]map[string]struct{}
	combos    map[string]map[string]*combinationGolden
	golden    nativeDiscriminatorsGolden
}

func newGoldenSummary(version string) *goldenSummary {
	return &goldenSummary{
		golden: nativeDiscriminatorsGolden{
			Schema:     infraGoldenSchema,
			Recording:  "test/fixtures/cbm-recordings/infra.json",
			CbmVersion: version,
			NodeLabels: map[string]nodeLabelGolden{},
			EdgeTypes:  map[string]edgeTypeGolden{},
		},
		nodeProps: map[string]map[string]struct{}{},
		edgeProps: map[string]map[string]struct{}{},
		combos:    map[string]map[string]*combinationGolden{},
	}
}

func (s *goldenSummary) addNode(n *cbmprobe.Node) {
	label := n.Label()
	entry := s.golden.NodeLabels[label]
	entry.Count++
	s.golden.NodeLabels[label] = entry

	if s.nodeProps[label] == nil {
		s.nodeProps[label] = map[string]struct{}{}
	}

	for name := range n.Props {
		s.nodeProps[label][name] = struct{}{}
	}
}

// edgeTypeEntry gives the entry of an edge type, and makes it with its property and combination sets when it is new.
func (s *goldenSummary) edgeTypeEntry(edgeType string) edgeTypeGolden {
	entry, ok := s.golden.EdgeTypes[edgeType]
	if !ok {
		entry = edgeTypeGolden{Discriminators: map[string]map[string]int{}}
		s.edgeProps[edgeType] = map[string]struct{}{}
		s.combos[edgeType] = map[string]*combinationGolden{}
	}

	return entry
}

// countDiscriminators adds the discriminators of the edge to the entry and gives the ones that the edge has.
func countDiscriminators(entry *edgeTypeGolden, e *cbmprobe.Edge) map[string]string {
	values := map[string]string{}

	for _, name := range discriminatorNames {
		value := e.Prop(name)
		if value == "" {
			continue
		}

		values[name] = value

		if entry.Discriminators[name] == nil {
			entry.Discriminators[name] = map[string]int{}
		}

		entry.Discriminators[name][value]++
	}

	return values
}

func (s *goldenSummary) addEdge(e *cbmprobe.Edge) {
	entry := s.edgeTypeEntry(e.Type)
	entry.Count++

	for name := range e.Props {
		s.edgeProps[e.Type][name] = struct{}{}
	}

	values := countDiscriminators(&entry, e)
	s.golden.EdgeTypes[e.Type] = entry
	s.addCombination(e, values)
}

// addCombination counts the shape of the edge: the labels at its ends, the type of its source file, and its discriminators.
func (s *goldenSummary) addCombination(e *cbmprobe.Edge, values map[string]string) {
	combo := combinationGolden{
		SourceLabel:    e.From.Label(),
		SourceFileType: fileTypeOf(e.From.FilePath),
		TargetLabel:    e.To.Label(),
		TargetKind:     e.To.Prop("kind"),
		Discriminators: values,
	}

	key := fmt.Sprintf("%s|%s|%s|%s|%v", combo.SourceLabel, combo.SourceFileType, combo.TargetLabel, combo.TargetKind, values)
	if existing, found := s.combos[e.Type][key]; found {
		existing.Count++

		return
	}

	combo.Count = 1
	s.combos[e.Type][key] = &combo
}

// finish turns the property sets and the combinations into the sorted lists of the golden.
func (s *goldenSummary) finish() nativeDiscriminatorsGolden {
	for label, entry := range s.golden.NodeLabels {
		entry.Properties = sortedKeys(s.nodeProps[label])
		s.golden.NodeLabels[label] = entry
	}

	for edgeType, entry := range s.golden.EdgeTypes {
		entry.Properties = sortedKeys(s.edgeProps[edgeType])

		for _, key := range sortedKeys(s.combos[edgeType]) {
			entry.Combinations = append(entry.Combinations, *s.combos[edgeType][key])
		}

		s.golden.EdgeTypes[edgeType] = entry
	}

	return s.golden
}

// summarize reads the discriminators of all native edge types of a graph.
func summarize(graph *cbmprobe.Graph, version string) nativeDiscriminatorsGolden {
	summary := newGoldenSummary(version)

	for _, n := range graph.Nodes {
		summary.addNode(n)
	}

	for _, e := range graph.Edges {
		summary.addEdge(e)
	}

	return summary.finish()
}

// writeInfraGolden writes the golden again from the recording.
func writeInfraGolden(t *testing.T, got *nativeDiscriminatorsGolden) {
	t.Helper()

	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("encode the golden: %v", err)
	}

	if err = os.MkdirAll(filepath.Dir(infraGoldenFile), goldenDirMode); err != nil {
		t.Fatalf("create the golden directory: %v", err)
	}

	if err = os.WriteFile(infraGoldenFile, append(data, '\n'), goldenFileMode); err != nil {
		t.Fatalf("write the golden: %v", err)
	}
}

// failGoldenDifference stops the test with both documents.
func failGoldenDifference(t *testing.T, got, want *nativeDiscriminatorsGolden) {
	t.Helper()

	gotText, gotErr := json.MarshalIndent(got, "", "  ")
	wantText, wantErr := json.MarshalIndent(want, "", "  ")

	if err := errors.Join(gotErr, wantErr); err != nil {
		t.Fatalf("encode the difference: %v", err)
	}

	t.Fatalf("the discriminators of the recording differ from %s (set %s=1 to write it again)\n--- golden\n%s\n--- recording\n%s",
		infraGoldenFile, updateGoldenEnv, wantText, gotText)
}

// observedNativeDiscriminators are the discriminator values that each native edge type of the recording must list.
var observedNativeDiscriminators = map[string]map[string][]string{
	"USAGE":      {"ref": {"image", "env", "envFrom", "volume", "tls", "parentRef", "roleRef", "subject", "podSelector", "selector", "scaleTarget", "serviceAccountName", "var", "module", "output", "resource"}, "via": {"terraform"}},
	"DEPENDS_ON": {"via": {"from", "copy-from", "build", "depends_on"}},
	"CONFIGURES": {"strategy": {"env_set", "env_access", "env_read", "helm_values"}},
	"INFRA_MAPS": {"kind": {"selector", "ingress", "route", "httproute"}},
	"DEPLOYS":    {"via": {"kubectl", "helm", "chart"}},
	"OVERRIDE":   {"via": {"tfvars", "patch"}},
	"IMPORTS":    {"via": {"terraform"}},
	"MENTIONS":   {"syntax": {"link", "code_path"}, "tier": {"exact"}},
	"SUPERSEDES": {"via": {"markdown"}, "syntax": {"supersedes"}, "tier": {"exact"}},
	"CALLS":      {"strategy": {"lsp_direct"}},
}

// checkEdgeTypeDiscriminators reports each value of the edge type that the golden has no edge for.
func checkEdgeTypeDiscriminators(t *testing.T, edgeType string, entry *edgeTypeGolden, want map[string][]string) {
	t.Helper()

	for name, values := range want {
		for _, value := range values {
			if entry.Discriminators[name][value] == 0 {
				t.Errorf("%s: no %s value %q", edgeType, name, value)
			}
		}
	}
}

func TestInfraNativeDiscriminatorsGolden(t *testing.T) {
	snap := replayInfra(t, infraRecordingFile)
	got := summarize(snap.graph, snap.rec.Version)

	if os.Getenv(updateGoldenEnv) != "" {
		writeInfraGolden(t, &got)
	}

	var want nativeDiscriminatorsGolden
	if err := json.Unmarshal(mustRead(t, infraGoldenFile), &want); err != nil {
		t.Fatalf("parse the golden: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		failGoldenDifference(t, &got, &want)
	}

	t.Run("lists the discriminators of every observed native edge type", func(t *testing.T) {
		for edgeType, want := range observedNativeDiscriminators {
			entry, ok := got.EdgeTypes[edgeType]
			if !ok {
				t.Errorf("the golden has no edge type %s", edgeType)

				continue
			}

			checkEdgeTypeDiscriminators(t, edgeType, &entry, want)
		}
	})
}
