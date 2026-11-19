package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
)

// TestCandidate is a `TESTS` or `TESTS_FILE` association of the code graph: a test (or test file) that the graph links
// to a tested node (or file). It is a candidate verification artifact and never evidence: it creates no `pass`, does
// not say that a test ran or passed, and does not satisfy a check (spec verification-results, "Test associations are
// candidates only"). CBM 0.12.1 gives these edges no properties (O12), so a candidate holds the two nodes and nothing
// else: no framework, no test name, no confidence.
type TestCandidate struct {
	// Kind is `tests` or `tests-file`.
	Kind string `json:"kind"`
	// Test is the test node (the qualified name, or the file path of a file node) and Tested the node that it tests.
	Test   string `json:"test"`
	Tested string `json:"tested"`
	// Nodes are the CALM nodes that the tested node is bound to, sorted. They are the suggested mapping of a check
	// result to a node: a suggestion for a human to confirm in the `covers` list of a result, never used for a verdict.
	Nodes []string `json:"nodes,omitempty"`
}

// Text is the candidate as one line for `assurance validate` and `explain`. It says that the association is not
// evidence.
func (c *TestCandidate) Text() string {
	text := fmt.Sprintf("%s %s -> %s (candidate, not evidence)", c.Kind, c.Test, c.Tested)
	if len(c.Nodes) > 0 {
		text += "; suggested mapping to " + strings.Join(c.Nodes, ", ")
	}

	return text
}

// candidateName names an endpoint of an edge: a file node by its path, any other node by its qualified name. An
// endpoint that is not among the nodes keeps its observed id.
func candidateName(nodes map[string]observation.ObservedNode, observedID string) string {
	node, ok := nodes[observedID]
	switch {
	case !ok:
		return observedID
	case node.Label == "File" && node.FilePath != "":
		return node.FilePath
	case node.QualifiedName != "":
		return node.QualifiedName
	case node.FilePath != "":
		return node.FilePath
	}

	return observedID
}

// TestCandidatesOf lists the `tests` and `tests-file` facts of the evidence as candidates, sorted by kind, test, and
// tested node, without duplicates. The nodes come from the binding set (an unbound tested node has none).
func TestCandidatesOf(result *observation.EvidenceQueryResult, set *binding.Set) []TestCandidate {
	candidates := []TestCandidate{}

	if result == nil {
		return candidates
	}

	for _, edge := range result.Edges {
		if edge.Kind != observation.KindTests && edge.Kind != observation.KindTestsFile {
			continue
		}

		candidate := TestCandidate{
			Kind: string(edge.Kind), Test: candidateName(result.Nodes, edge.From), Tested: candidateName(result.Nodes, edge.To),
		}

		if set != nil {
			candidate.Nodes = slices.Clone(set.ByObserved[edge.To])
			slices.SortFunc(candidate.Nodes, canonjson.CompareUTF16)
		}

		candidates = append(candidates, candidate)
	}

	slices.SortFunc(candidates, func(a, b TestCandidate) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Kind, b.Kind), canonjson.CompareUTF16(a.Test, b.Test), canonjson.CompareUTF16(a.Tested, b.Tested),
			slices.Compare(a.Nodes, b.Nodes),
		)
	})

	return slices.CompactFunc(candidates, func(a, b TestCandidate) bool {
		return a.Kind == b.Kind && a.Test == b.Test && a.Tested == b.Tested && slices.Equal(a.Nodes, b.Nodes)
	})
}

// recordCandidates keeps the candidates of the evidence for the rules of the run and for the report. It runs after the
// bindings resolved and before the rules are evaluated.
func (r *assuranceRun) recordCandidates(result *observation.EvidenceQueryResult, set *binding.Set) {
	r.candidates = TestCandidatesOf(result, set)

	if r.report != nil {
		r.report.Candidates = slices.Clone(r.candidates)
	}
}

// candidateDetail names the candidates that suggest a mapping to the subject (every candidate when subject is empty),
// for the detail of an `unknown` check: the user sees that the code graph knows a test association, and that it was not
// used.
func (r *assuranceRun) candidateDetail(subject string) string {
	var lines []string

	for i := range r.candidates {
		candidate := &r.candidates[i]
		if subject == "" || slices.Contains(candidate.Nodes, subject) {
			lines = append(lines, candidate.Text())
		}
	}

	const limit = 3

	if len(lines) == 0 {
		return ""
	}

	shown := lines
	if len(shown) > limit {
		shown = shown[:limit]
	}

	text := "candidate test associations that are not evidence: " + strings.Join(shown, "; ")
	if rest := len(lines) - len(shown); rest > 0 {
		text += fmt.Sprintf("; and %d more", rest)
	}

	return text
}
