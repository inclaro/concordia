// Package binding loads the bindings of CALM nodes to observed entities and
// resolves them against the observed nodes of a graph snapshot.
package binding

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/trust"
)

// paramID is the "id" parameter of the binding diagnostics.
const paramID = "id"

// MatchKind is the kind of a binding match.
type MatchKind string

// Match kinds.
const (
	MatchSourceRoot    MatchKind = "sourceRoot"
	MatchPackage       MatchKind = "package"
	MatchQualifiedName MatchKind = "qualifiedName"
	MatchBuildTarget   MatchKind = "buildTarget"
	MatchOtelService   MatchKind = "otelService"

	MatchWorkload       MatchKind = "workload"
	MatchImage          MatchKind = "image"
	MatchRoute          MatchKind = "route"
	MatchInfraResource  MatchKind = "infraResource"
	MatchComposeService MatchKind = "composeService"
)

// Binding cardinality, agreement and status values. The zero value of each
// field of Binding means the default (`many`, `all`, `active`).
const (
	CardinalityOne  = "one"
	CardinalityMany = "many"
	AgreementAll    = "all"
	AgreementAny    = "any"
	StatusActive    = "active"
	StatusProposed  = "proposed"
)

// Match is one selector of a binding. The five as-built kinds use Value; the
// deployed-resource kinds use the identity fields.
type Match struct {
	Kind         MatchKind `json:"kind"`
	Value        string    `json:"value,omitempty"`
	Environment  string    `json:"environment,omitempty"`
	Cluster      string    `json:"cluster,omitempty"`
	APIGroup     string    `json:"apiGroup,omitempty"`
	ResourceKind string    `json:"resourceKind,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	Name         string    `json:"name,omitempty"`
	Repository   string    `json:"repository,omitempty"`
	Digest       string    `json:"digest,omitempty"`
	Address      string    `json:"address,omitempty"`
	File         string    `json:"file,omitempty"`
	Service      string    `json:"service,omitempty"`
}

// Projection holds the projection hints of a binding.
type Projection struct {
	C4Level string `json:"c4Level,omitempty"`
}

// Binding is one binding rule after loading (from a BindingSet file or from a
// CALM `concordia.bindings` item). Load fills Cardinality, Agreement and Status
// with their defaults; an empty value in a hand-built Binding means the default.
type Binding struct {
	Projection *Projection `json:"projection,omitempty"`
	// Trust is the trust context (authority dimensions and data classification) that the
	// entry declares for the entities it binds; nil when none.
	Trust           *trust.Declaration `json:"trust,omitempty"`
	ID              string             `json:"id"`
	ArchitectureRef string             `json:"architectureRef"`
	Source          string             `json:"source"`
	Cardinality     string             `json:"cardinality,omitempty"`
	Agreement       string             `json:"agreement,omitempty"`
	Status          string             `json:"status,omitempty"`
	Authority       string             `json:"authority,omitempty"`
	Match           []Match            `json:"match"`
	Exclusive       bool               `json:"exclusive"`
	Required        bool               `json:"required"`
}

// Conflict is an observed node that two or more CALM nodes claim with an exclusive binding.
type Conflict struct {
	ObservedID string
	CalmIDs    []string
	BindingIDs []string
}

// Set is the result of Resolve.
type Set struct {
	// ByObserved maps an observed ID to its CALM IDs (sorted). Conflicted nodes are absent.
	ByObserved map[string][]string
	// MatchCounts maps a binding ID to the number of observed nodes that the
	// binding matched (before the specificity filter).
	MatchCounts map[string]int
	// Ambiguous maps an observed ID to the IDs (sorted) of the non-exclusive
	// bindings at its top tier that name different CALM elements. The entity keeps
	// all CALM IDs in ByObserved; no winner is chosen by order of declaration, and
	// the obligations that depend on it are `unknown` (reason `ambiguous-binding`).
	Ambiguous map[string][]string
	// UsedBy maps an observed ID to the IDs (sorted) of the bindings that bound it
	// at the top tier. Use it to record the binding, its status, and its authority in evidence.
	UsedBy map[string][]string
	// Agreements maps a CALM ID to the agreement of the bindings that bound a
	// deployed resource to it: `any` if every such binding says `any`, else
	// `all`. A CALM ID with no deployed-resource binding has no entry (see AgreementOf).
	Agreements map[string]string
	Conflicts  []Conflict
	// CardinalityBreaches are the bindings with cardinality `one` that matched more
	// than one entity in one environment (CO-BND-008).
	CardinalityBreaches []CardinalityBreach
	// UnboundObserved counts the observed nodes without a CALM ID (conflicted nodes are not counted).
	UnboundObserved int
}

type hit struct {
	binding *Binding
	spec    specificity
}

// ResolveOption configures Resolve.
type ResolveOption func(*resolveConfig)

type resolveConfig struct {
	clusterOf func(environment string) string
}

// WithClusterOf gives the cluster or context that the workspace declares for an
// environment (empty when it declares none). A workload, route, or
// infraResource match that names a `cluster` only matches an entity whose
// environment has this cluster.
func WithClusterOf(clusterOf func(environment string) string) ResolveOption {
	return func(c *resolveConfig) { c.clusterOf = clusterOf }
}

// isResolvedEnvironment says whether an environment id names an environment: it is neither empty nor unresolved.
func isResolvedEnvironment(environment string) bool {
	return environment != "" && environment != observation.EnvironmentUnresolved
}

// clusterFor gives the cluster of an environment id, empty for none or for an unresolved environment.
func (c *resolveConfig) clusterFor(environment string) string {
	if c.clusterOf == nil || !isResolvedEnvironment(environment) {
		return ""
	}

	return c.clusterOf(environment)
}

// Resolve resolves bindings against observed nodes. It is deterministic: the
// order of bindings has no effect. For each node the most specific matches
// win; two or more CALM IDs at the top specificity with one exclusive binding
// make a conflict (CO-BND-003). Without an exclusive binding they are
// ambiguous (Set.Ambiguous): the node keeps all CALM IDs and no winner is chosen.
// A binding with status `proposed` takes no part in resolution.
func Resolve(bindings []Binding, nodes []observation.ObservedNode, buildFiles []string, opts ...ResolveOption) Set {
	cfg := &resolveConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	matchers := newMatchers(bindings, buildPackages(buildFiles), cfg)
	matchCounts := zeroCounts(bindings)
	set := Set{
		ByObserved: map[string][]string{}, MatchCounts: matchCounts, Conflicts: []Conflict{},
		Ambiguous: map[string][]string{}, UsedBy: map[string][]string{}, CardinalityBreaches: []CardinalityBreach{},
	}
	agree := agreementTracker{any: map[string]bool{}}
	card := newCardinalityTracker(bindings, cfg)

	sorted := sortedNodes(nodes)
	for i := range sorted {
		node := &sorted[i]

		hits := hitsOf(bindings, matchers, node, nodeSegments(node), matchCounts, card)
		if len(hits) == 0 {
			set.UnboundObserved++

			continue
		}

		resolveNode(&set, node.ObservedID, hits, &agree)
	}

	slices.SortStableFunc(set.Conflicts, func(a, b Conflict) int { return canonjson.CompareUTF16(a.ObservedID, b.ObservedID) })
	set.CardinalityBreaches = card.breaches()
	set.Agreements = agree.result()

	return set
}

// buildPackages gives the directories of the build files, with "" for the root.
func buildPackages(buildFiles []string) map[string]struct{} {
	pkgs := make(map[string]struct{}, len(buildFiles))

	for _, f := range buildFiles {
		d := path.Dir(f)
		if d == "." {
			d = ""
		}

		pkgs[d] = struct{}{}
	}

	return pkgs
}

// newMatchers builds the matchers of each binding, in the order of the bindings. A `proposed` binding has none.
func newMatchers(bindings []Binding, pkgs map[string]struct{}, cfg *resolveConfig) [][]matcher {
	matchers := make([][]matcher, len(bindings))

	for i := range bindings {
		if bindings[i].Status == StatusProposed {
			continue
		}

		for mi := range bindings[i].Match {
			matchers[i] = append(matchers[i], newMatcher(&bindings[i].Match[mi], pkgs, cfg))
		}
	}

	return matchers
}

// zeroCounts gives the match count 0 for the ID of each binding.
func zeroCounts(bindings []Binding) map[string]int {
	counts := make(map[string]int, len(bindings))
	for i := range bindings {
		counts[bindings[i].ID] = 0
	}

	return counts
}

// sortedNodes gives a copy of nodes in the order of their observed IDs.
func sortedNodes(nodes []observation.ObservedNode) []observation.ObservedNode {
	sorted := slices.Clone(nodes)
	slices.SortStableFunc(sorted, func(a, b observation.ObservedNode) int { return canonjson.CompareUTF16(a.ObservedID, b.ObservedID) })

	return sorted
}

// nodeSegments splits the qualified name of a node at the dots; a node without one has no segments.
func nodeSegments(node *observation.ObservedNode) []string {
	if node.QualifiedName == "" {
		return nil
	}

	return strings.Split(node.QualifiedName, ".")
}

func hitsOf(bindings []Binding, matchers [][]matcher, node *observation.ObservedNode, segments []string, counts map[string]int, card *cardinalityTracker) []hit {
	var hits []hit

	for i := range bindings {
		best, found := bestMatch(matchers[i], node, segments)
		if found {
			hits = append(hits, hit{binding: &bindings[i], spec: best})
			counts[bindings[i].ID]++
			card.record(i, node)
		}
	}

	return hits
}

// bestMatch gives the highest specificity of the matchers that match the node, and false when none does.
func bestMatch(matchers []matcher, node *observation.ObservedNode, segments []string) (best specificity, found bool) {
	for _, m := range matchers {
		if !m.test(node, segments) {
			continue
		}

		if !found || m.spec.compare(best) > 0 {
			best, found = m.spec, true
		}
	}

	return best, found
}

// topClaim is what the hits at the top specificity of a node claim: their
// CALM IDs (sorted, with no duplicate), their binding IDs (sorted), and
// whether one of the bindings is exclusive.
type topClaim struct {
	calmIDs    []string
	bindingIDs []string
	exclusive  bool
}

// topSpecificity gives the highest specificity of the hits; hits is not empty.
func topSpecificity(hits []hit) specificity {
	top := hits[0].spec
	for _, h := range hits {
		if h.spec.compare(top) > 0 {
			top = h.spec
		}
	}

	return top
}

// claimOf gives the claim of the hits whose specificity is top.
func claimOf(hits []hit, top specificity) topClaim {
	var claim topClaim

	for _, h := range hits {
		if h.spec.compare(top) != 0 {
			continue
		}

		claim.calmIDs = append(claim.calmIDs, h.binding.ArchitectureRef)
		claim.bindingIDs = append(claim.bindingIDs, h.binding.ID)
		claim.exclusive = claim.exclusive || h.binding.Exclusive
	}

	slices.SortFunc(claim.calmIDs, canonjson.CompareUTF16)
	claim.calmIDs = slices.Compact(claim.calmIDs)

	slices.SortFunc(claim.bindingIDs, canonjson.CompareUTF16)

	return claim
}

func resolveNode(set *Set, observedID string, hits []hit, agree *agreementTracker) {
	top := topSpecificity(hits)
	claim := claimOf(hits, top)

	if len(claim.calmIDs) > 1 && claim.exclusive {
		set.Conflicts = append(set.Conflicts, Conflict{ObservedID: observedID, CalmIDs: claim.calmIDs, BindingIDs: claim.bindingIDs})

		return
	}

	set.ByObserved[observedID] = claim.calmIDs
	set.UsedBy[observedID] = claim.bindingIDs

	if len(claim.calmIDs) > 1 {
		set.Ambiguous[observedID] = claim.bindingIDs
	}

	if top.tier >= tierImageRepo {
		addAgreements(agree, hits, top)
	}
}

// addAgreements records the agreement of each hit whose specificity is top.
func addAgreements(agree *agreementTracker, hits []hit, top specificity) {
	for _, h := range hits {
		if h.spec.compare(top) == 0 {
			agree.add(h.binding)
		}
	}
}

// Problems gives the errors of a resolved binding set: CO-BND-003 for each conflict,
// CO-BND-008 for each breach of cardinality `one`, then CO-BND-004 for each
// required binding that matched no observed entity. A `proposed` binding is never required to match.
func Problems(loaded *Loaded, set *Set) []diag.Diagnostic {
	var problems []diag.Diagnostic

	for i := range set.Conflicts {
		problems = append(problems, conflictProblem(&set.Conflicts[i]))
	}

	for i := range set.CardinalityBreaches {
		problems = append(problems, breachProblem(loaded, &set.CardinalityBreaches[i]))
	}

	for i := range loaded.Bindings {
		if b := &loaded.Bindings[i]; isUnmatchedRequired(b, set) {
			problems = append(problems, diag.New(diag.BND004, diag.Params{paramID: b.ID}, diag.WithSource(diag.Source{URI: b.Source})))
		}
	}

	return problems
}

// conflictProblem makes the CO-BND-003 diagnostic of a conflict.
func conflictProblem(c *Conflict) diag.Diagnostic {
	return diag.New(diag.BND003, diag.Detail(fmt.Sprintf("%s binds to %s (bindings %s)",
		c.ObservedID, strings.Join(c.CalmIDs, ", "), strings.Join(c.BindingIDs, ", "))))
}

// breachProblem makes the CO-BND-008 diagnostic of a breach of cardinality `one`.
func breachProblem(loaded *Loaded, breach *CardinalityBreach) diag.Diagnostic {
	var opts []diag.Option

	if b := findBinding(loaded, breach.BindingID); b != nil {
		opts = append(opts, diag.WithSource(diag.Source{URI: b.Source}))
	}

	return diag.New(diag.BND008, breach.detail(), opts...)
}

// isUnmatchedRequired says whether b is a required binding that matched no observed entity.
func isUnmatchedRequired(b *Binding, set *Set) bool {
	if !b.Required || b.Status == StatusProposed {
		return false
	}

	return set.MatchCounts[b.ID] == 0
}

// Warnings gives CO-BND-009 for each group of observed entities that several
// non-exclusive bindings map to different CALM elements. The group is the
// set of binding IDs; the diagnostic lists the CALM IDs, the bindings, and the entities.
func Warnings(set *Set) []diag.Diagnostic {
	groups := map[string][]string{}

	for observedID, ids := range set.Ambiguous {
		key := strings.Join(ids, ", ")
		groups[key] = append(groups[key], observedID)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	out := make([]diag.Diagnostic, 0, len(keys))

	for _, key := range keys {
		entities := groups[key]
		slices.SortFunc(entities, canonjson.CompareUTF16)

		out = append(out, diag.New(diag.BND009, diag.Detail(fmt.Sprintf(
			"bindings %s map %d entities to different CALM elements (%s); first: %s; the obligations that depend on them are unknown",
			key, len(entities), strings.Join(set.ByObserved[entities[0]], ", "), entities[0]))))
	}

	return out
}
