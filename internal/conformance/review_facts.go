package conformance

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// reviewProfileNone is the identity of the capability profile of a run that no profile applies to.
const reviewProfileNone = "none"

// Provider names in ReviewFacts.EvaluatorVersions besides impact.EvaluatorNormalization.
const (
	// ReviewEvaluatorProvider is the key of the observation provider version (the CBM executable).
	ReviewEvaluatorProvider = "provider"
	// ReviewEvaluatorAdapter is the key of the adapter schema.
	ReviewEvaluatorAdapter = "adapterSchema"
)

// Facets that a selector can use besides the contract fields of the kind.
const (
	facetPath       = "path"
	facetRepository = "repository"
	facetTag        = "tag"
	facetDigest     = "digest"
	facetSubject    = "subject"
	facetFamily     = "family"
)

// ReviewFactsInput is the run state that NewReviewFacts reads. Every field is the value that the check
// pipeline already holds after the evidence, the bindings, and the coverage are computed; nothing is read again.
type ReviewFactsInput struct {
	// Evidence is the normalized, environment-attributed evidence with scoped IDs and derived placement.
	Evidence *observation.EvidenceQueryResult
	// BindingSet resolves the observed nodes to CALM IDs. nil: nothing is bound.
	BindingSet *binding.Set
	// Profile is the capability profile of the build. nil: no profile applies, so no observed fact is supported.
	Profile *observation.CapabilityProfile
	// Model is the CALM model. nil: no model-based item exists.
	Model *intent.ArchitectureModel
	// Assurance is the assurance evidence of the run: the admitted check results and the loaded quality scenarios.
	// nil means that the workspace has no `assurance` key: the kinds `verification` and `quality-scenario` are then
	// not in the capability profile and a selector of either is `unsupported-family` (design D27).
	Assurance *ReviewAssurance
	// Git reads files at a revision. nil: only the working tree can be read.
	Git workspace.GitPort
	// Root is the workspace root: an empty revision reads files below it.
	Root string
	// ProviderVersion and AdapterSchema are the versions of the observation evaluator (`state.caps`).
	ProviderVersion string
	AdapterSchema   string
	// Contract is the digest contract of the items. The zero value is impact.V1().
	Contract impact.Contract
	// Coverage are the predicate coverage records of the run (`RunCoverage.Predicates`).
	Coverage []observation.PredicateCoverage
	// Bindings are the loaded binding entries.
	Bindings []binding.Binding
	// Environments are the declared environments (for the cluster of a workload).
	Environments []deployment.Environment
}

// ReviewFacts is the impact.FactSource over the observation snapshot, the bindings, the CALM model, and the
// Kubernetes facts of a run. It never reads the graph again: every item is built once, with the digest contract,
// from the frozen evidence.
//
// Mapping of facts to dependency kinds (design D5):
//   - runtime-identity: `uses-runtime-identity` (workload to ServiceAccount) and `binding-applies-to-principal`
//     (`subject`); the role references come from the RoleBindings of the ServiceAccount.
//   - role-binding: `binding-refers-to-role` (`roleRef`).
//   - secret-reference, configuration-reference, storage-reference, certificate-reference: the `references-*`
//     facts; the target kind decides Secret versus ConfigMap, the reference mode is the role.
//   - public-entry-point: the Ingress or HTTPRoute `Resource` is the identity; host and path come from the
//     `Route` nodes that route to the same Service, and are facets. A `parentRef` is part of the exposure class.
//   - placement: the workload with its `placed-in` namespace, environment, cluster, and the `region` trust value.
//   - implementation: the files that a selector names (`path`), and the image of each container occurrence of a
//     workload (the `image` property of the `uses-image` fact; the `Image` node has no tag).
//   - protocol, trust-context, data-classification, binding, control: the CALM model and the bindings.
//   - external-destination: observed remote calls from a bound node to a destination that no binding maps.
//   - verification: one item for each check and environment that has an admissible check result of the `assurance`
//     evidence (identity `check:<check>@<environment>`; the covered identifiers and the outcome with the digest of
//     the result statement are the fields). quality-scenario: one item for each scenario of `assurance.quality`
//     (identity `scenario:<id>`; the digest of the definition and the response measure are the fields). Both kinds
//     are supported only when the workspace has an `assurance` key; an evidence source that is not read completely
//     gives an `unknown` match with `no-evidence-supplied`, never an empty one.
type ReviewFacts struct {
	assurance  *ReviewAssurance
	contract   impact.Contract
	profile    impact.CapabilityProfile
	versions   map[string]string
	nodes      map[string]observation.ObservedNode
	edges      map[observation.CanonicalEdgeKind][]*observation.EvidenceEdge
	candidates map[impact.Kind][]reviewCandidate
	consumers  map[string][]reviewConsumer
	bindingSet *binding.Set
	model      *intent.ArchitectureModel
	git        workspace.GitPort
	root       string
	coverage   []observation.PredicateCoverage
	gaps       []reviewGap
	evidence   *observation.EvidenceQueryResult
	bindings   []binding.Binding
	envs       []deployment.Environment
}

var _ impact.FactSource = (*ReviewFacts)(nil)

// reviewCandidate is an item with what a selector filters on.
type reviewCandidate struct {
	// consumer is the scoped identity of the consuming resource, empty for a model-based item.
	consumer string
	// env is the declared environment of the item, empty when the item has none (a model-based item).
	env  string
	calm []string
	item impact.Item
}

// reviewConsumer is a resource that a coverage gap names by kind and name.
type reviewConsumer struct {
	scoped string
	nodeID string
}

// reviewGap is a coverage gap that limits one dependency kind.
type reviewGap struct {
	kind impact.Kind
	gap  observation.CoverageGap
}

// NewReviewFacts builds the fact source of a run. It builds all items at once and never changes the input.
func NewReviewFacts(input *ReviewFactsInput) *ReviewFacts {
	facts := &ReviewFacts{
		contract: input.Contract, bindingSet: input.BindingSet, model: input.Model, git: input.Git, root: input.Root,
		coverage: input.Coverage, bindings: input.Bindings, envs: input.Environments, evidence: input.Evidence,
		nodes:      map[string]observation.ObservedNode{},
		edges:      map[observation.CanonicalEdgeKind][]*observation.EvidenceEdge{},
		candidates: map[impact.Kind][]reviewCandidate{},
		consumers:  map[string][]reviewConsumer{},
		versions: map[string]string{
			impact.EvaluatorNormalization: observation.NormalizationVersion,
			ReviewEvaluatorProvider:       input.ProviderVersion,
			ReviewEvaluatorAdapter:        input.AdapterSchema,
		},
	}

	if facts.contract.Version == "" {
		facts.contract = impact.V1()
	}

	if facts.bindingSet == nil {
		facts.bindingSet = &binding.Set{}
	}

	if input.Evidence != nil {
		facts.nodes = input.Evidence.Nodes

		for _, edge := range input.Evidence.Edges {
			facts.edges[edge.Kind] = append(facts.edges[edge.Kind], edge)
		}
	}

	facts.assurance = input.Assurance
	facts.profile = reviewProfile(input.Profile, &facts.contract, input.Assurance != nil)
	facts.indexConsumers()
	facts.collectGaps()
	facts.build()

	return facts
}

// Profile implements impact.FactSource.
func (f *ReviewFacts) Profile() impact.CapabilityProfile {
	return impact.CapabilityProfile{
		ID: f.profile.ID, Kinds: slices.Clone(f.profile.Kinds), Selectors: cloneSelectors(f.profile.Selectors),
	}
}

func cloneSelectors(in map[impact.Kind][]string) map[impact.Kind][]string {
	out := make(map[impact.Kind][]string, len(in))
	for kind, names := range in {
		out[kind] = slices.Clone(names)
	}

	return out
}

// EvaluatorVersions implements impact.FactSource: the normalization version and the versions of the
// observation provider and its adapter schema.
func (f *ReviewFacts) EvaluatorVersions() map[string]string {
	out := make(map[string]string, len(f.versions))
	for name, version := range f.versions {
		if version != "" {
			out[name] = version
		}
	}

	return out
}

// FileDigest implements impact.FactSource. An empty revision reads the working tree below the workspace root
// (the candidate being checked); any other revision reads git.
func (f *ReviewFacts) FileDigest(ctx context.Context, revision, path string) (sum string, found bool, err error) {
	if revision == "" {
		return f.workingTreeDigest(path)
	}

	return f.revisionDigest(ctx, revision, path)
}

// workingTreeDigest digests a file below the workspace root. A file that does not exist is not found, not an error.
func (f *ReviewFacts) workingTreeDigest(path string) (sum string, found bool, err error) {
	if f.root == "" {
		return "", false, errors.New("review facts: no workspace root to read the working tree")
	}

	abs, err := fsutil.Confine(f.root, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}

		return "", false, fmt.Errorf("review facts: %w", err)
	}

	data, err := os.ReadFile(abs) //nolint:gosec // abs is confined below the workspace root by fsutil.Confine.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}

		return "", false, fmt.Errorf("review facts: read %s: %w", path, err)
	}

	return digest.OfBytes(data), true, nil
}

// revisionDigest digests a file as git holds it at a revision.
func (f *ReviewFacts) revisionDigest(ctx context.Context, revision, path string) (sum string, found bool, err error) {
	if f.git == nil {
		return "", false, errors.New("review facts: no git port to read a revision")
	}

	data, found, err := f.git.ShowFile(ctx, revision, path)
	if err != nil {
		return "", false, fmt.Errorf("review facts: read %s at %s: %w", path, revision, err)
	}

	if !found {
		return "", false, nil
	}

	return digest.OfBytes(data), true, nil
}

// Items implements impact.FactSource.
func (f *ReviewFacts) Items(_ context.Context, sel impact.Selector) (impact.Match, error) { //nolint:gocritic // hugeParam: the signature is impact.FactSource.
	kind := sel.Kind
	if !kind.Valid() {
		return impact.Match{}, fmt.Errorf("%w: %q", impact.ErrUnknownKind, kind)
	}

	if !f.profile.Supports(kind) {
		return f.unsupported("the capability profile does not map the kind"), nil
	}

	if facet := f.unselectable(kind, &sel); facet != "" {
		return f.unsupported(unselectableDetail(kind, facet, f.profile.ID)), nil
	}

	found := f.selectItems(kind, &sel)
	res := f.verdictOf(kind, &sel)

	return f.matchOf(found, &res), nil
}

// matchOf gives the match of the selected items under the coverage verdict of the selection.
func (f *ReviewFacts) matchOf(found []impact.Item, res *verdict) impact.Match {
	match := impact.Match{Items: found, Coverage: res.coverage, LossModes: res.lossModes}

	switch {
	case res.unsupported:
		return f.unsupported(strings.Join(res.details, "; "))
	case res.reason != "":
		match.State, match.Reason, match.Detail = impact.MatchUnknown, res.reason, strings.Join(res.details, "; ")
	case len(found) > 0:
		match.State = impact.MatchFound
	default:
		match.State = impact.MatchEmpty
		match.Detail = "coverage is complete and the selector matches nothing"
	}

	return match
}

// unsupported is the match for a kind or a selector that the profile does not map.
func (f *ReviewFacts) unsupported(detail string) impact.Match {
	return impact.Match{
		State: impact.MatchUnsupported, Reason: impact.ReasonUnsupportedFamily, Profile: f.profile.ID,
		Detail: detail, Items: []impact.Item{},
	}
}

// unselectable gives the first facet name of the selector that the profile cannot select on, "" when there is none.
func (f *ReviewFacts) unselectable(kind impact.Kind, sel *impact.Selector) string {
	names := make([]string, 0, len(sel.Match))
	for name := range sel.Match {
		names = append(names, name)
	}

	slices.SortFunc(names, canonjson.CompareUTF16)

	for _, name := range names {
		if !f.profile.SupportsSelector(kind, name) {
			return name
		}
	}

	return ""
}

// terraformFacets are the selector names that ask for a Terraform source of a reference.
var terraformFacets = []string{"via", facetFamily, "terraformRef"}

func unselectableDetail(kind impact.Kind, facet, profile string) string {
	if slices.Contains(terraformFacets, facet) {
		return fmt.Sprintf("Terraform references (via: terraform) are not mapped to %s by the capability profile %s", kind, profile)
	}

	return fmt.Sprintf("the capability profile %s cannot select %s on the facet %q", profile, kind, facet)
}

// selectItems gives the items of the kind that the selector matches, sorted by identity.
func (f *ReviewFacts) selectItems(kind impact.Kind, sel *impact.Selector) []impact.Item {
	rest := *sel
	paths := sel.Match[facetPath]
	pathSelector := kind == impact.KindImplementation && len(paths) > 0

	if pathSelector {
		rest.Match = reviewMatchWithoutPath(sel.Match)
	}

	if kind == impact.KindQualityScenario {
		rest.Identities = scenarioIdentities(sel.Identities)
	}

	items := []impact.Item{}

	// A path-only selector names files, not the images of the workloads.
	if !pathSelector || len(sel.Identities) > 0 || len(sel.CalmNodes) > 0 {
		items = append(items, f.matchingCandidates(kind, &rest)...)
	}

	if kind == impact.KindImplementation {
		items = append(items, f.fileItems(kind, paths)...)
	}

	slices.SortFunc(items, func(a, b impact.Item) int { return canonjson.CompareUTF16(a.Identity, b.Identity) })

	return slices.CompactFunc(items, func(a, b impact.Item) bool { return a.Identity == b.Identity })
}

// reviewMatchWithoutPath copies the facet filter of a selector without the `path` facet, which names files.
func reviewMatchWithoutPath(match map[string][]string) map[string][]string {
	rest := make(map[string][]string, len(match))

	for name, values := range match {
		if name != facetPath {
			rest[name] = values
		}
	}

	return rest
}

// matchingCandidates gives the items of the kind whose candidate the selector selects.
func (f *ReviewFacts) matchingCandidates(kind impact.Kind, sel *impact.Selector) []impact.Item {
	var items []impact.Item

	for i := range f.candidates[kind] {
		if f.candidates[kind][i].matches(sel) {
			items = append(items, f.candidates[kind][i].item)
		}
	}

	return items
}

// fileItems gives the item of each file that a selector names by path.
func (f *ReviewFacts) fileItems(kind impact.Kind, paths []string) []impact.Item {
	items := make([]impact.Item, 0, len(paths))

	for _, path := range paths {
		items = append(items, f.contract.NewItem(
			kind, "file:"+path, impact.Facets{facetSubject: {path}}, []string{path}, nil,
		))
	}

	return items
}

// matches says whether the selector selects the candidate. An item that has no environment (the CALM model) matches
// every environment.
func (c *reviewCandidate) matches(sel *impact.Selector) bool {
	return c.inEnvironment(sel.Environment) && c.matchesCalmNodes(sel.CalmNodes) &&
		c.matchesIdentities(sel.Identities) && c.matchesFacets(sel.Match)
}

func (c *reviewCandidate) inEnvironment(environment string) bool {
	return environment == "" || c.env == "" || c.env == environment
}

func (c *reviewCandidate) matchesCalmNodes(nodes []string) bool {
	return len(nodes) == 0 || intersects(c.calm, nodes)
}

func (c *reviewCandidate) matchesIdentities(identities []string) bool {
	return len(identities) == 0 || slices.ContainsFunc(identities, c.hasIdentity)
}

func (c *reviewCandidate) matchesFacets(match map[string][]string) bool {
	for name, want := range match {
		if !intersects(c.item.Facets[name], want) {
			return false
		}
	}

	return true
}

// hasIdentity says whether id names the item, or the resource that the item belongs to (`<resource>#...`).
func (c *reviewCandidate) hasIdentity(id string) bool {
	return c.item.Identity == id || strings.HasPrefix(c.item.Identity, id+"#")
}

func intersects(a, b []string) bool {
	return slices.ContainsFunc(a, func(v string) bool { return slices.Contains(b, v) })
}
