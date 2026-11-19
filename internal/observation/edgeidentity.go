package observation

import (
	"slices"
	"strings"
)

// LossMergedOccurrences is the profile loss mode for a provider that merges
// occurrences into one native edge before Concordia receives it (CBM 0.12.1
// observations, O4).
const LossMergedOccurrences = "merged-occurrences"

// EdgeKey is the identity of an evidence fact: the semantic kind, the
// direction (From to To), the scope (environment), the protocol or operation,
// the reference role, and the occurrence discriminator. Two edges that differ
// in one of these are two facts.
type EdgeKey struct {
	Kind        CanonicalEdgeKind
	From        string
	To          string
	Environment string
	// Operation is the protocol or operation of the fact (facet `operation`
	// or `protocol`, else the same native property), empty when there is none.
	Operation string
	// Reference is the resource reference role (facet `referenceMode`, else the native `ref`).
	Reference string
	// Occurrence is the occurrence discriminator, for example a container name.
	Occurrence string
}

// EdgeKeyOf gives the identity of an edge.
func EdgeKeyOf(e *EvidenceEdge) EdgeKey {
	return EdgeKey{
		Kind:        e.Kind,
		From:        e.From,
		To:          e.To,
		Environment: e.Environment,
		Operation:   firstOf(e.Facets["operation"], e.Facets["protocol"], e.Properties["operation"], e.Properties["protocol"]),
		Reference:   firstOf(e.Facets["referenceMode"], e.Properties["ref"]),
		Occurrence:  e.Occurrence,
	}
}

// String gives the key as one line, for reports and as a map key where a
// string is needed.
func (k EdgeKey) String() string {
	return strings.Join([]string{string(k.Kind), k.From, k.To, k.Environment, k.Operation, k.Reference, k.Occurrence}, "\x1f")
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

// DedupeEdges keeps the first edge of each EdgeKey, in order, and counts the
// edges it drops. The same fact that arrives twice is one fact and its count is
// not doubled; edges that differ in any key part stay separate.
func DedupeEdges(edges []*EvidenceEdge) (kept []*EvidenceEdge, dropped int) {
	seen := make(map[EdgeKey]struct{}, len(edges))
	kept = make([]*EvidenceEdge, 0, len(edges))

	for _, e := range edges {
		key := EdgeKeyOf(e)
		if _, dup := seen[key]; dup {
			continue
		}

		seen[key] = struct{}{}

		kept = append(kept, e)
	}

	return kept, len(edges) - len(kept)
}

// LossModes gives the loss modes that the profile states for the family of a
// canonical kind. A nil profile or a family that the profile does not list has none.
func LossModes(p *CapabilityProfile, kind CanonicalEdgeKind) []string {
	if p == nil {
		return nil
	}

	f, ok := p.Family(string(kind))
	if !ok {
		return nil
	}

	return slices.Clone(f.LossModes)
}

// MarkPossiblyLossy sets PossiblyLossy on each edge whose kind family has the
// loss mode `merged-occurrences` in the profile: the provider can merge
// occurrences before Concordia receives them, and Concordia does not claim to
// recover what the provider removed. It returns the number of marked edges.
// A nil profile marks nothing.
func MarkPossiblyLossy(edges []*EvidenceEdge, p *CapabilityProfile) int {
	if p == nil {
		return 0
	}

	marked := 0

	for _, e := range edges {
		if slices.Contains(LossModes(p, e.Kind), LossMergedOccurrences) {
			e.PossiblyLossy = true
			e.LossModes = LossModes(p, e.Kind)
			marked++
		}
	}

	return marked
}

// EdgeIdentity is what identifies one evidence fact, for reports: the edge key,
// the environment, the occurrence, the image reference, and whether the
// provider may have merged the fact with others (with the profile loss modes).
type EdgeIdentity struct {
	Key           string   `json:"key"`
	Environment   string   `json:"environment,omitempty"`
	Occurrence    string   `json:"occurrence,omitempty"`
	Image         string   `json:"image,omitempty"`
	LossModes     []string `json:"lossModes,omitempty"`
	PossiblyLossy bool     `json:"possiblyLossy,omitempty"`
}

// IdentityOf gives the identity of an edge. The key joins the EdgeKey parts with "|".
func IdentityOf(e *EvidenceEdge) EdgeIdentity {
	return EdgeIdentity{
		Key:           strings.ReplaceAll(EdgeKeyOf(e).String(), "\x1f", "|"),
		Environment:   e.Environment,
		Occurrence:    e.Occurrence,
		Image:         e.Properties["image"],
		LossModes:     e.LossModes,
		PossiblyLossy: e.PossiblyLossy,
	}
}

// HasIdentityDetail is true when the identity says more than the key: an
// environment, an occurrence, an image, or a lossy mark.
func (i *EdgeIdentity) HasIdentityDetail() bool {
	return i.Environment != "" || i.Occurrence != "" || i.Image != "" || i.PossiblyLossy
}
