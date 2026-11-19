package conformance

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// Diagnostic parameter names of the coverage diagnostics.
const (
	paramDetail  = "detail"
	paramProject = "project"
	paramCount   = "count"
	paramPath    = "path"
)

// SkipCounts counts the candidate edges that an evaluation leaves out, by reason.
// The edges stay in the evidence: a skip changes what a rule looks at, never what
// the run knows.
type SkipCounts struct {
	// Unbound counts edges with an endpoint that no binding matches.
	Unbound int `json:"unbound"`
	// EndpointMissing counts edges that refer to a node that the graph query did not return.
	EndpointMissing int `json:"endpointMissing"`
	// OutOfScope counts edges with an endpoint in a file outside the coverage scope (excluded).
	OutOfScope int `json:"outOfScope"`
	// Unsupported counts facts of a kind that the provider build or the adapter does not map.
	Unsupported int `json:"unsupported"`
}

// Skip reasons: the stable names and texts of the four counts.
const (
	SkipUnbound         = "unbound"
	SkipEndpointMissing = "endpointMissing"
	SkipOutOfScope      = "outOfScope"
	SkipUnsupported     = "unsupported"

	skipReasonUnbound         = "an endpoint has no binding to a CALM element (resolve the binding)"
	skipReasonEndpointMissing = "endpoint missing: the edge refers to a node that the graph query did not return"
	skipReasonOutOfScope      = "an endpoint is in a file outside the coverage scope (excluded by the project settings)"
	skipReasonUnsupported     = "the fact kind is not mapped by the provider profile (kept as evidence, not evaluated)"
)

// Total is the number of skipped edges.
func (s SkipCounts) Total() int {
	return s.Unbound + s.EndpointMissing + s.OutOfScope + s.Unsupported
}

// unsupportedKind gives 1 for a fact kind that no rule can read (`unsupported`, `ambiguous`), else 0.
func unsupportedKind(kind observation.CanonicalEdgeKind) int {
	if kind == observation.KindUnsupported || kind == observation.KindAmbiguous {
		return 1
	}

	return 0
}

// RunSkipCounts counts, over the facts of every kind (structure aside), the edges that no evaluation can read:
// an endpoint with no binding, an endpoint missing from the graph, an endpoint outside the coverage scope,
// and facts of unsupported kinds. unsupportedFacts is the number of provider facts that the adapter kept as
// unsupported evidence. Nothing is removed from the evidence.
func RunSkipCounts(input *ReconcileInput, unsupportedFacts int) SkipCounts {
	excluded := excludedFile(input)
	counts := SkipCounts{Unsupported: unsupportedFacts}

	for _, edge := range input.Evidence.Edges {
		if unsupportedKind(edge.Kind) == 1 {
			counts.Unsupported++

			continue
		}

		if IsStructuralKind(edge.Kind) {
			continue
		}

		from, okFrom := input.Evidence.Nodes[edge.From]
		dest, okTo := input.Evidence.Nodes[edge.To]

		switch {
		case !okFrom || !okTo:
			counts.EndpointMissing++
		case (from.FilePath != "" && excluded(from.FilePath)) || (dest.FilePath != "" && excluded(dest.FilePath)):
			counts.OutOfScope++
		case len(input.BindingSet.ByObserved[edge.From]) == 0 || len(input.BindingSet.ByObserved[edge.To]) == 0:
			counts.Unbound++
		}
	}

	return counts
}

// Add gives the sum of two counts.
func (s SkipCounts) Add(other SkipCounts) SkipCounts {
	return SkipCounts{
		Unbound: s.Unbound + other.Unbound, EndpointMissing: s.EndpointMissing + other.EndpointMissing,
		OutOfScope: s.OutOfScope + other.OutOfScope, Unsupported: s.Unsupported + other.Unsupported,
	}
}

// Reasons gives the reason of each count that is not zero, keyed by the name of the count.
// It is never nil.
func (s SkipCounts) Reasons() map[string]string {
	out := map[string]string{}

	for name, entry := range map[string]struct {
		reason string
		count  int
	}{
		SkipUnbound:         {skipReasonUnbound, s.Unbound},
		SkipEndpointMissing: {skipReasonEndpointMissing, s.EndpointMissing},
		SkipOutOfScope:      {skipReasonOutOfScope, s.OutOfScope},
		SkipUnsupported:     {skipReasonUnsupported, s.Unsupported},
	} {
		if entry.count > 0 {
			out[name] = entry.reason
		}
	}

	return out
}

// LimitBreach is one limit that a run went above: the limit key, the configured
// value, and the observed value. A traversal reports the number of nodes visited
// when it stopped, so its observed value is a lower bound.
type LimitBreach struct {
	// Limit is the workspace key, for example `limits.graph.maxNodes`.
	Limit      string `json:"limit"`
	Configured int    `json:"configured"`
	Observed   int    `json:"observed"`
}

// Limit keys of the workspace `limits` block.
const (
	LimitGraphMaxNodes     = "limits.graph.maxNodes"
	LimitGraphMaxEdges     = "limits.graph.maxEdges"
	LimitEvidenceMaxFacts  = "limits.evidence.maxFacts"
	LimitTraversalMaxVisit = "limits.traversal.maxVisited"
)

// Diagnostic gives the CO-CON-030 warning of a breach.
func (b LimitBreach) Diagnostic() diag.Diagnostic {
	return diag.New(diag.CON030, diag.Params{"limit": b.Limit, "configured": b.Configured, "observed": b.Observed})
}

// Reason gives the rule reason of a breach (CO-CON-030).
func (b LimitBreach) Reason() Reason {
	return Reason{Code: diag.CON030, Message: b.Diagnostic().Message}
}

// CheckGraphLimits compares the size of the evidence of a run with the graph and
// evidence limits. It gives one breach for each limit that is exceeded, sorted by
// limit. The evidence is read, not changed: no node, edge, or fact is cut.
func CheckGraphLimits(limits workspace.Limits, res *observation.EvidenceQueryResult) []LimitBreach {
	var out []LimitBreach

	nodes := len(res.Nodes)
	edges := len(res.Edges) + len(res.Unsupported)
	facts := len(res.Edges) + len(res.Entities)

	if nodes > limits.Graph.MaxNodes {
		out = append(out, LimitBreach{Limit: LimitGraphMaxNodes, Configured: limits.Graph.MaxNodes, Observed: nodes})
	}

	if edges > limits.Graph.MaxEdges {
		out = append(out, LimitBreach{Limit: LimitGraphMaxEdges, Configured: limits.Graph.MaxEdges, Observed: edges})
	}

	if facts > limits.Evidence.MaxFacts {
		out = append(out, LimitBreach{Limit: LimitEvidenceMaxFacts, Configured: limits.Evidence.MaxFacts, Observed: facts})
	}

	slices.SortFunc(out, func(a, b LimitBreach) int { return canonjson.CompareUTF16(a.Limit, b.Limit) })

	return out
}

// traversal counts the nodes that one graph traversal visits against the traversal limit.
// A nil traversal has no limit. Once a traversal stops, it is incomplete: it cannot support a `pass`.
type traversal struct {
	limit      int
	visited    int
	incomplete bool
}

// newTraversal makes a traversal with a limit of visited nodes. A limit of 0 or less means no limit.
func newTraversal(limit int) *traversal { return &traversal{limit: limit} }

// visit counts one visited node. It gives false once the limit is reached: the caller stops the traversal.
func (t *traversal) visit() bool {
	if t == nil || t.limit <= 0 {
		return true
	}

	if t.visited >= t.limit {
		t.incomplete = true

		return false
	}

	t.visited++

	return true
}

// stopped says whether the traversal stopped at its limit.
func (t *traversal) stopped() bool { return t != nil && t.incomplete }

// breach gives the limit breach of a traversal that stopped. The observed value is the
// number of nodes visited when it stopped plus the node that it refused: a lower bound.
func (t *traversal) breach() LimitBreach {
	return LimitBreach{Limit: LimitTraversalMaxVisit, Configured: t.limit, Observed: t.visited + 1}
}

// ScopeContext is what Evaluate knows about the scopes of a run: the predicate
// coverage records, the projects that failed, which projects each CALM node
// depends on, the limits, and the facts that no rule can read.
type ScopeContext struct {
	// NodeProjects gives, for a CALM node ID, the projects that the observed evidence of the node can come from.
	// A node that is not listed depends on every project.
	NodeProjects map[string][]string
	// Predicates are the coverage records of the run.
	Predicates []observation.PredicateCoverage
	// Failures are the projects whose query failed (coverage `failed`).
	Failures []observation.ScopeFailure
	// Projects are all selected projects.
	Projects []string
	// Breaches are the graph and evidence limits that the run went above. Every rule depends on them.
	Breaches []LimitBreach
	// Limits are the resolved workspace limits; a zero value means none.
	Limits workspace.Limits
	// UnsupportedFacts counts the provider facts that the adapter kept as unsupported evidence.
	UnsupportedFacts int
}

// projectsOfNodes gives the projects that the evidence of the CALM nodes comes from, sorted.
// A node without an entry depends on every project.
func (c *ScopeContext) projectsOfNodes(ids []string) []string {
	set := map[string]struct{}{}

	for _, id := range ids {
		projects, ok := c.NodeProjects[id]
		if !ok || len(projects) == 0 {
			projects = c.Projects
		}

		for _, p := range projects {
			set[p] = struct{}{}
		}
	}

	if len(ids) == 0 {
		for _, p := range c.Projects {
			set[p] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// FailedScopesOf gives the scope failures of the projects that a claim depends on, sorted by project.
// Obligations use it: a claim that depends on a failed scope is `unknown` with the reason `coverage-incomplete`;
// a claim that depends on other scopes only keeps its result.
func (c *ScopeContext) FailedScopesOf(projects []string) []observation.ScopeFailure {
	var out []observation.ScopeFailure

	for _, f := range c.Failures {
		if slices.Contains(projects, f.Project) {
			out = append(out, f)
		}
	}

	slices.SortFunc(out, func(a, b observation.ScopeFailure) int { return canonjson.CompareUTF16(a.Project, b.Project) })

	return out
}

// FailedScopesOfNodes gives the scope failures of the projects that the CALM nodes depend on.
func (c *ScopeContext) FailedScopesOfNodes(ids []string) []observation.ScopeFailure {
	if len(c.Failures) == 0 {
		return nil
	}

	return c.FailedScopesOf(c.projectsOfNodes(ids))
}

// scopeFailureReason gives the rule reason of a failed scope (CO-OBS-018).
func scopeFailureReason(f observation.ScopeFailure) Reason {
	return Reason{Code: diag.OBS018, Message: diag.New(diag.OBS018, diag.Params{paramProject: f.Project, paramDetail: f.Reason}).Message}
}

// PredicatesOf gives the project-scope coverage records of the families of edge kinds
// in the projects, sorted as the records are. The result is never nil.
func PredicatesOf(records []observation.PredicateCoverage, kinds []observation.CanonicalEdgeKind, projects []string) []observation.PredicateCoverage {
	families := map[string]struct{}{}

	for _, k := range kinds {
		if name, ok := observation.FamilyForKind(k); ok {
			families[name] = struct{}{}
		}
	}

	out := []observation.PredicateCoverage{}

	for i := range records {
		rec := &records[i]
		if _, ok := families[rec.Predicate]; ok && rec.Scope.Environment == "" && slices.Contains(projects, rec.Scope.Project) {
			out = append(out, *rec)
		}
	}

	return out
}

// predicateReasons gives the rule reasons of the records that are not complete (CO-CON-032), and
// CO-CON-031 for a family that the provider profile does not support. A kind that the rule already reports as unsupported (CO-POL-009)
// gives no second reason.
func predicateReasons(records []observation.PredicateCoverage, reported map[string]struct{}) []Reason {
	var out []Reason

	for i := range records {
		rec := &records[i]
		if rec.Complete() {
			continue
		}

		if _, done := reported[rec.Predicate]; done && rec.Status == observation.CoverageUnsupported {
			continue
		}

		scope := rec.Scope.Project
		if scope == "" {
			scope = rec.Scope.Environment
		}

		if rec.Status == observation.CoverageUnsupported {
			out = append(out, Reason{Code: diag.CON031, Message: diag.New(diag.CON031, diag.Params{"family": rec.Predicate}).Message})

			continue
		}

		detail := fmt.Sprintf("%d unresolved", rec.Unresolved)
		if len(rec.Reasons) > 0 {
			detail = rec.Reasons[0]
		}

		out = append(out, Reason{Code: diag.CON032, Message: diag.New(diag.CON032, diag.Params{
			"predicate": rec.Predicate, "status": string(rec.Status), "scope": scope, paramDetail: detail,
		}).Message})
	}

	return out
}

// planScope is what the scopes of the run say about one rule: the predicate
// records it depends on, the reasons that a failed scope or an exceeded limit
// give (they block every claim), and the reasons of predicates that are not
// complete (they block a claim of absence).
type planScope struct {
	records []observation.PredicateCoverage
	// blocking are the failed scopes and exceeded limits that the rule depends on.
	blocking []Reason
	// incomplete are the predicates of the rule that are not `complete`.
	incomplete []Reason
}

// complete says that nothing in the scopes blocks a definite absence for the rule.
func (p *planScope) complete() bool { return len(p.blocking) == 0 && len(p.incomplete) == 0 }

// reasonsFor gives the reasons that the scopes add to a rule of a family. A rule that claims an absence
// (`requireEdge`, `observedRequired`, `requireEntity`) is also blocked by predicates that are not complete:
// a missing item is a violation only with complete qualified coverage. A rule that forbids something keeps
// its result with a partial predicate, which the coverage records show.
func (p *planScope) reasonsFor(family policy.PredicateFamily) []Reason {
	if isAbsenceFamily(family) {
		return slices.Concat(p.blocking, p.incomplete)
	}

	return slices.Clone(p.blocking)
}

// isAbsenceFamily says whether the family claims that a necessary item is absent.
func isAbsenceFamily(family policy.PredicateFamily) bool {
	return slices.Contains([]policy.PredicateFamily{policy.FamilyRequireEdge, policy.FamilyObservedRequired, policy.FamilyRequireEntity}, family)
}

// forPlan gives the scope of a rule. A run without scope information (no records,
// no failures, no breaches) gives an empty scope that blocks nothing.
func (c *ScopeContext) forPlan(plan *policy.RulePlan) planScope {
	nodes := slices.Concat(plan.Subjects, plan.Targets)
	projects := c.projectsOfNodes(nodes)
	scope := planScope{records: PredicatesOf(c.Predicates, plan.Traversal.EdgeKinds, projects)}

	reported := map[string]struct{}{}

	for _, k := range plan.UnsupportedEdgeKinds {
		if name, ok := observation.FamilyForKind(k); ok {
			reported[name] = struct{}{}
		}
	}

	scope.incomplete = predicateReasons(scope.records, reported)

	for _, f := range c.FailedScopesOf(projects) {
		scope.blocking = append(scope.blocking, scopeFailureReason(f))
	}

	for _, b := range c.Breaches {
		scope.blocking = append(scope.blocking, b.Reason())
	}

	return scope
}

// newTraversal makes the traversal counter of one cycle or path search of the rule.
func (r *planRun) newTraversal() *traversal {
	return newTraversal(r.input.Scope.Limits.Traversal.MaxVisited)
}

// noteTraversal records that a traversal stopped at its limit. The rule is then incomplete and cannot pass.
func (r *planRun) noteTraversal(t *traversal) {
	if t.stopped() && r.breach == nil {
		breach := t.breach()
		r.breach = &breach
	}
}
