package cli

import (
	"encoding/json"
	"os"
	"slices"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// explainCandidates gives the TESTS and TESTS_FILE associations that suggest a mapping to a subject of the rule or of
// a selected finding, for a rule that needs the result of a check (`require.checkResult`). They are candidates that
// suggest which node a check covers; they are never evidence and the verdict of the rule did not use them. Without
// such a rule, or without a matching association, there is nothing to show.
func explainCandidates(run *conformance.Run, rule *conformance.RuleResult, selected []*conformance.Finding) []conformance.TestCandidate {
	if rule.Family != policy.FamilyCheckResult || run.Assurance == nil {
		return nil
	}

	subjects := slices.Clone(rule.Subjects)
	for _, finding := range selected {
		subjects = append(subjects, finding.Subject)
	}

	var out []conformance.TestCandidate

	for i := range run.Assurance.Candidates {
		candidate := run.Assurance.Candidates[i]
		if slices.ContainsFunc(candidate.Nodes, func(node string) bool { return slices.Contains(subjects, node) }) {
			out = append(out, candidate)
		}
	}

	return out
}

// testCandidateLines shows candidates as the block of `explain` and `assurance validate`.
func testCandidateLines(candidates []conformance.TestCandidate) []string {
	if len(candidates) == 0 {
		return nil
	}

	lines := []string{"Candidate test associations (not evidence, never a verdict):"}
	for i := range candidates {
		lines = append(lines, "  "+candidates[i].Text())
	}

	return lines
}

// lastCandidates reads the test associations that the last check run recorded in the results file. `assurance validate`
// loads no code graph, so it shows what that run found; there is none (the empty list) without a results file or when
// the file has no associations. The command never writes the file.
func lastCandidates(wsp *workspace.Workspace) []conformance.TestCandidate {
	data, err := os.ReadFile(resultsPath(wsp))
	if err != nil {
		return []conformance.TestCandidate{}
	}

	var doc struct {
		Assurance *struct {
			Candidates []conformance.TestCandidate `json:"candidates"`
		} `json:"assurance"`
	}

	if json.Unmarshal(data, &doc) != nil || doc.Assurance == nil || doc.Assurance.Candidates == nil {
		return []conformance.TestCandidate{}
	}

	return doc.Assurance.Candidates
}
