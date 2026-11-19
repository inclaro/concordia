package conformance

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// Observability holds the counters of one run (PRD §15). They are plain integers with fixed names: they contain
// no text, path, name, or value from the repository. The run manifest has the same object.
type Observability struct {
	// IngestedFacts counts the facts that the provider returned: the evidence edges and the unsupported facts of all projects.
	IngestedFacts int `json:"ingestedFacts"`
	// NormalizedFacts counts the evidence edges with a canonical kind (not `ambiguous`, not `unsupported`).
	NormalizedFacts int `json:"normalizedFacts"`
	// UnresolvedFacts counts the facts that could not be fully identified: edges with an unbound endpoint or an
	// endpoint that the graph query did not return, and edges whose resolution is `ambiguous` or `unresolved`.
	UnresolvedFacts int `json:"unresolvedFacts"`
	// UnsupportedFacts counts the native edges of types that Concordia does not map (kept as evidence of kind `unsupported`).
	UnsupportedFacts int `json:"unsupportedFacts"`
	// CoverageGaps counts the predicate coverage records of the run whose status is not `complete`.
	CoverageGaps int `json:"coverageGaps"`
	// Conflicts counts the competing declarations of one resource identity, the observed entities that several
	// non-exclusive bindings map to different CALM elements, and the evidence conflicts of obligation results.
	Conflicts int `json:"conflicts"`
	// CacheHits counts the remote resources that the run took from the resource cache.
	CacheHits int `json:"cacheHits"`
	// StageFailures counts the projects whose query failed and whose coverage is therefore `failed` (CO-OBS-018).
	StageFailures int `json:"stageFailures"`
}

// observabilityOf gives the counters from the merged evidence and the coverage section of the run. conflicts and
// cacheHits come from the stages that know them.
func observabilityOf(evidence *observation.EvidenceQueryResult, coverage *RunCoverage, conflicts, cacheHits int) Observability {
	obs := Observability{Conflicts: conflicts, CacheHits: cacheHits, StageFailures: len(coverage.ScopeFailures)}

	for _, count := range evidence.UnmappedNativeEdges {
		obs.UnsupportedFacts += count
	}

	// The facts of an unmapped native kind are kept in Unsupported; the counts say how many native edges there were.
	obs.IngestedFacts = len(evidence.Edges) + max(len(evidence.Unsupported), obs.UnsupportedFacts)

	for _, edge := range evidence.Edges {
		if edge.Kind != observation.KindAmbiguous && edge.Kind != observation.KindUnsupported {
			obs.NormalizedFacts++
		}

		if edge.Resolution == observation.ResolutionAmbiguous || edge.Resolution == observation.ResolutionUnresolved {
			obs.UnresolvedFacts++
		}
	}

	obs.UnresolvedFacts += coverage.Skipped.Unbound + coverage.Skipped.EndpointMissing

	for i := range coverage.Predicates {
		if coverage.Predicates[i].Status != observation.CoverageComplete {
			obs.CoverageGaps++
		}
	}

	return obs
}

// identityConflicts gives the groups of competing declarations of one resource identity (observation.Conflicts).
// A node with a malformed identity is not a declaration and is not in a group.
func identityConflicts(wspace *workspace.Workspace, evidence *fetched) []observation.Conflict {
	decls := observation.DeclarationsOfValid(evidence.result.Nodes, func(env string) string {
		for i := range wspace.Environments {
			if wspace.Environments[i].ID == env {
				return wspace.Environments[i].Cluster
			}
		}

		return ""
	})

	return observation.Conflicts(observation.GroupDeclarations(decls))
}

// conflictDiagnostics gives one CO-OBS-021 warning for each group of competing declarations. The message names the
// scoped identity and every location. A group whose environment or namespace is unresolved may be different
// resources: the warning says so, and it does not block any obligation (see TypedContext.Conflicts).
func conflictDiagnostics(conflicts []observation.Conflict) []diag.Diagnostic {
	out := make([]diag.Diagnostic, 0, len(conflicts))

	for i := range conflicts {
		conflict := &conflicts[i]
		places := make([]string, len(conflict.Locations))

		for j, loc := range conflict.Locations {
			places[j] = loc.Path
			if loc.Line > 0 {
				places[j] += ":" + strconv.Itoa(loc.Line)
			}
		}

		detail := "same environment and namespace: they are not two instances, no declaration wins, and the obligations that depend on this resource are unknown"
		if !conflict.Certain {
			detail = "the environment or the namespace is unresolved, so they may be different resources"
		}

		out = append(out, diag.New(diag.OBS021, diag.Params{"id": conflict.ID.String(), "locations": strings.Join(places, " and "), paramDetail: detail}))
	}

	return out
}

// occurrenceConflicts counts the obligation results that disagree about one property (class evidence-conflict).
func occurrenceConflicts(rules []RuleResult) int {
	conflicts := 0

	for i := range rules {
		for j := range rules[i].Obligations {
			for k := range rules[i].Obligations[j].Occurrences {
				if rules[i].Obligations[j].Occurrences[k].Class == OccurrenceConflict {
					conflicts++
				}
			}
		}
	}

	return conflicts
}

// ResultDigest gives `sha256:<hex>` of the canonical JSON of the run with the volatile fields zeroed: `run.id`,
// `run.startedAt`, `run.finishedAt`, `run.host`, the digest itself, `observability.cacheHits` (whether a
// remote resource was cached is a state of the machine, not of the inputs), and `assessmentCurrency.evaluatedAt`
// with the mode and cache counters of `analysis`. Findings, areas, coverage, and
// every other field stay: two runs with the same inputs have the same digest (PRD OUT-06).
func ResultDigest(run *Run) (string, error) {
	canonical := *run
	canonical.Run.ID, canonical.Run.StartedAt, canonical.Run.FinishedAt, canonical.Run.Host = "", "", "", RunHost{}
	canonical.Summary.ResultDigest = ""
	canonical.Observability.CacheHits = 0

	// The evaluation time and the state of the impact cache are volatile too: the currency itself stays.
	if canonical.AssessmentCurrency != nil {
		report := *canonical.AssessmentCurrency
		report.EvaluatedAt = ""
		canonical.AssessmentCurrency = &report
	}

	if canonical.Analysis != nil {
		analysis := *canonical.Analysis
		analysis.Mode, analysis.CacheHits, analysis.CacheMisses = "", 0, 0
		canonical.Analysis = &analysis
	}

	text, err := canonjson.Marshal(&canonical)
	if err != nil {
		return "", fmt.Errorf("result digest: %w", err)
	}

	return digest.OfBytes(text), nil
}
