package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// statusProposed is the status of a deployment candidate. A proposed binding takes
// no part in resolution until a person moves it to a binding file as `active`.
const statusProposed = binding.StatusProposed

const (
	// reviewUnreviewed is the review status of a candidate that no person has read.
	reviewUnreviewed = "unreviewed"
	// whatUniqueID and whatNameSlug say in a reason what a candidate name matched.
	whatUniqueID = "the unique-id"
	whatNameSlug = "a name slug"
)

// workloadKinds are the Kubernetes kinds that a `workload` candidate can name.
var workloadKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod"}

// routeResourceKinds are the Kubernetes kinds that a `route` candidate can name.
var routeResourceKinds = []string{"Ingress", "HTTPRoute", "GRPCRoute"}

// deployedNativeKinds gives the native edge kinds of all infrastructure kinds, sorted.
func deployedNativeKinds() []string {
	seen := map[string]struct{}{}

	for _, kind := range observation.InfrastructureKinds() {
		for _, native := range policy.NativeKindsFor(kind) {
			seen[native] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for native := range seen {
		out = append(out, native)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// deployedNodes gives the deployed resources of a project (`Resource` nodes
// with the declared environment of their source, and image entities) for the
// deployment strategy. Paths get the workspace prefix first, so the environment
// is attributed from the workspace path as `check` does.
func (s *projectSnapshot) deployedNodes(ctx context.Context) ([]observation.ObservedNode, error) {
	res, err := s.provider.FetchEdges(ctx, s.ref, deployedNativeKinds())
	if err != nil {
		return nil, fmt.Errorf("fetch deployment edges: %w", err)
	}

	prefixDeployedPaths(&res, s.prefix)
	observation.AttributeEnvironments(&res, s.wsp.Attribute)

	return deployedResources(&res, s.prefix), nil
}

// prefixDeployedPaths gives the nodes and the edge source spans of a result the workspace path prefix.
func prefixDeployedPaths(res *observation.EvidenceQueryResult, prefix string) {
	for id := range res.Nodes {
		n := res.Nodes[id]
		n.FilePath = prefixWorkspacePath(prefix, n.FilePath)
		res.Nodes[id] = n
	}

	for _, e := range slices.Concat(res.Edges, res.Unsupported) {
		if e.SourceSpan == nil {
			continue
		}

		span := *e.SourceSpan
		span.Path = prefixWorkspacePath(prefix, span.Path)
		e.SourceSpan = &span
	}
}

// deployedResources gives the `Resource` nodes and the image entities of a result.
func deployedResources(res *observation.EvidenceQueryResult, prefix string) []observation.ObservedNode {
	out := make([]observation.ObservedNode, 0, len(res.Nodes)+len(res.Entities))

	for id := range res.Nodes {
		if n := res.Nodes[id]; n.Label == "Resource" {
			out = append(out, n)
		}
	}

	return append(out, imageEntities(res.Entities, prefix)...)
}

// imageEntities gives the image entities, with the workspace path prefix.
func imageEntities(entities []observation.ObservedNode, prefix string) []observation.ObservedNode {
	var images []observation.ObservedNode

	for i := range entities {
		if entities[i].Label != observation.LabelImage {
			continue
		}

		entity := entities[i]
		entity.FilePath = prefixWorkspacePath(prefix, entity.FilePath)
		images = append(images, entity)
	}

	return images
}

// nameScore says how well a deployed name matches a CALM node: the name is the
// unique-id (exact), another slug of the node, or starts with a slug and a dash.
func nameScore(name string, slugs *nodeSlugs) (score float64, what string, matched bool) {
	lower := strings.ToLower(name)

	switch {
	case slugs.ids[0] == lower:
		return exactScore, whatUniqueID, true
	case slices.Contains(slugs.all, lower):
		return slugScore, whatNameSlug, true
	}

	for _, slug := range slugs.all {
		if strings.HasPrefix(lower, slug+"-") {
			return packageScore, "the start of the name", true
		}
	}

	return 0, "", false
}

// candidateSink collects the deployment candidates of one CALM node, each node-and-match once.
type candidateSink struct {
	seen   map[string]struct{}
	nodeID string
	slugs  *nodeSlugs
	out    []candidate
}

func (s *candidateSink) add(match *candidateMatch, score float64, reason string) {
	key := s.nodeID + "\x00" + match.key()
	if _, dup := s.seen[key]; dup {
		return
	}

	s.seen[key] = struct{}{}
	s.out = append(s.out, candidate{
		ArchitectureRef: s.nodeID, Match: *match, Score: score, Reasons: []string{reason},
		ReviewStatus: reviewUnreviewed, Status: statusProposed,
	})
}

// addImage proposes an `image` binding when the node is an image whose name matches. The bool is false for other nodes.
func (s *candidateSink) addImage(node *observation.ObservedNode) bool {
	repo, ok := imageRepository(node)
	if !ok {
		return false
	}

	base := repo[strings.LastIndex(repo, "/")+1:]
	if score, what, matched := nameScore(base, s.slugs); matched {
		s.add(&candidateMatch{Kind: string(binding.MatchImage), Repository: repo}, score,
			fmt.Sprintf("image '%s' name matches %s of '%s'", repo, what, s.nodeID))
	}

	return true
}

// addResource proposes a Compose service, workload, route, or namespace binding when the name of a resource node matches.
func (s *candidateSink) addResource(node *observation.ObservedNode) {
	key, found := observation.ObjKeyOf(node)
	if !found {
		return
	}

	parsed, err := observation.ParseObjKey(key)
	if err != nil {
		return
	}

	score, what, matched := nameScore(parsed.Name, s.slugs)
	if !matched {
		return
	}

	match, usable := deployedResourceMatch(node, &parsed, key)
	if !usable {
		return
	}

	reason := fmt.Sprintf("%s '%s' name matches %s of '%s'", parsed.Kind, parsed.Name, what, s.nodeID)
	if match.Kind == string(binding.MatchInfraResource) {
		reason += " (hosting target)"
	}

	s.add(&match, score, reason)
}

// deployedResourceMatch gives the binding match of a resource node: a Compose service, or, in a
// declared environment, a workload, route, or namespace (an infraResource).
func deployedResourceMatch(node *observation.ObservedNode, parsed *observation.ObjKey, key string) (candidateMatch, bool) {
	declared := node.Environment != "" && node.Environment != observation.EnvironmentUnresolved

	if parsed.Group == "compose" && parsed.Kind == "Service" {
		return composeServiceMatch(node, parsed, declared), true
	}

	// A workload, route, or infraResource binding needs a declared environment.
	if !declared {
		return candidateMatch{}, false
	}

	switch {
	case slices.Contains(workloadKinds, parsed.Kind):
		return namespacedResourceMatch(binding.MatchWorkload, node, parsed), true
	case slices.Contains(routeResourceKinds, parsed.Kind):
		return namespacedResourceMatch(binding.MatchRoute, node, parsed), true
	case parsed.Kind == "Namespace":
		return candidateMatch{Kind: string(binding.MatchInfraResource), Environment: node.Environment, Address: key}, true
	}

	return candidateMatch{}, false
}

// composeServiceMatch gives the Compose service match of a resource node, with its environment when declared.
func composeServiceMatch(node *observation.ObservedNode, parsed *observation.ObjKey, declared bool) candidateMatch {
	match := candidateMatch{Kind: string(binding.MatchComposeService), File: node.FilePath, Service: parsed.Name}
	if declared {
		match.Environment = node.Environment
	}

	return match
}

// namespacedResourceMatch gives the workload or route match of a resource node.
func namespacedResourceMatch(kind binding.MatchKind, node *observation.ObservedNode, parsed *observation.ObjKey) candidateMatch {
	return candidateMatch{
		Kind: string(kind), Environment: node.Environment, APIGroup: parsed.Group, ResourceKind: parsed.Kind,
		Namespace: namespaceOf(parsed), Name: parsed.Name,
	}
}

// deploymentCandidates proposes deployed-resource bindings for one CALM node, each
// with the status `proposed`: a workload, route, or namespace (an infraResource) in
// a declared environment, a Compose service, or an image whose name matches the node.
func deploymentCandidates(nodeID string, slugs *nodeSlugs, nodes []observation.ObservedNode) []candidate {
	sink := candidateSink{seen: map[string]struct{}{}, nodeID: nodeID, slugs: slugs}

	for i := range nodes {
		if !sink.addImage(&nodes[i]) {
			sink.addResource(&nodes[i])
		}
	}

	return sink.out
}

func namespaceOf(key *observation.ObjKey) string {
	if key.ClusterScoped || key.NamespaceUnresolved {
		return ""
	}

	return key.Namespace
}

// imageRepository gives the repository of an `Image` node or entity.
func imageRepository(n *observation.ObservedNode) (string, bool) {
	if n.Label != observation.LabelImage {
		return "", false
	}

	if repo := n.Properties["repository"]; repo != "" {
		return repo, true
	}

	repo := strings.TrimPrefix(n.QualifiedName, "__image__")

	return repo, repo != ""
}

// toMatch gives the binding match of a candidate that names a deployed-resource kind.
func (m *candidateMatch) toMatch() (binding.Match, bool) {
	if m.Kind == "" {
		return binding.Match{}, false
	}

	return binding.Match{
		Kind: binding.MatchKind(m.Kind), Environment: m.Environment, Cluster: m.Cluster, APIGroup: m.APIGroup,
		ResourceKind: m.ResourceKind, Namespace: m.Namespace, Name: m.Name, Repository: m.Repository, Digest: m.Digest,
		Address: m.Address, File: m.File, Service: m.Service,
	}, true
}
