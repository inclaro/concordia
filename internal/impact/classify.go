package impact

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

// ActionGetReview is the next-action category of every trigger. A trigger asks for a review; it
// does not say that anything is wrong.
const ActionGetReview = "get review"

// Class is one of the eight classes of a mandatory review trigger (security-assessments, "Mandatory
// review trigger classes").
type Class string

// The eight trigger classes.
const (
	// ClassExposure: ingress, route, backend.
	ClassExposure Class = "exposure"
	// ClassBoundary: a boundary-crossing flow or destination.
	ClassBoundary Class = "boundary-crossing"
	// ClassIdentity: principal, role, binding of a role, identity provider.
	ClassIdentity Class = "identity"
	// ClassSensitiveReference: secret, configuration, storage.
	ClassSensitiveReference Class = "sensitive-reference"
	// ClassProtection: protocol, certificate, control configuration.
	ClassProtection Class = "protection"
	// ClassEnvironment: environment or hosting.
	ClassEnvironment Class = "environment-hosting"
	// ClassImplementation: implementation or verification evidence.
	ClassImplementation Class = "implementation-verification"
	// ClassRemoval: removal or capability loss.
	ClassRemoval Class = "removal-capability-loss"
)

// Classes gives the eight classes in the order of the spec.
func Classes() []Class {
	return []Class{
		ClassExposure, ClassBoundary, ClassIdentity, ClassSensitiveReference,
		ClassProtection, ClassEnvironment, ClassImplementation, ClassRemoval,
	}
}

// Label gives the words that reports use for the class.
func (c Class) Label() string {
	switch c {
	case ClassExposure:
		return "exposure (ingress, route, backend)"
	case ClassBoundary:
		return "boundary-crossing flow or destination"
	case ClassIdentity:
		return "identity (principal, role, binding, identity provider)"
	case ClassSensitiveReference:
		return "sensitive reference (secret, configuration, storage)"
	case ClassProtection:
		return "protection (protocol, certificate, control configuration)"
	case ClassEnvironment:
		return "environment or hosting"
	case ClassImplementation:
		return "implementation or verification"
	case ClassRemoval:
		return "removal or capability loss"
	default:
		return string(c)
	}
}

// ClassOfKind gives the class of an addition or modification of a dependency kind. It reports
// false for a kind outside the closed list.
//
// A `binding` is the flow between a source and a target, so it belongs to the boundary class. A
// `quality-scenario` is the verification of a quality claim, so it belongs to the implementation
// and verification class. A removal never uses this table: ClassifyChange gives every removal the
// removal class.
func ClassOfKind(kind Kind) (Class, bool) {
	switch kind {
	case KindPublicEntryPoint:
		return ClassExposure, true
	case KindExternalDestination, KindTrustContext, KindDataClassification, KindBinding:
		return ClassBoundary, true
	case KindRuntimeIdentity, KindRoleBinding:
		return ClassIdentity, true
	case KindSecretReference, KindConfigurationReference, KindStorageReference:
		return ClassSensitiveReference, true
	case KindProtocol, KindCertificateReference, KindControl:
		return ClassProtection, true
	case KindPlacement:
		return ClassEnvironment, true
	case KindImplementation, KindVerification, KindQualityScenario:
		return ClassImplementation, true
	default:
		return "", false
	}
}

// ClassifyChange gives the class of one change. A removal is always ClassRemoval: the regression
// matters more than the kind of the item that went away. An unknown kind is classified as a
// removal-or-capability-loss review too, so a change is never left without a class.
func ClassifyChange(c Change) Class { //nolint:gocritic // Change is a value type that Diff returns in slices.
	return classify(c.Op, c.Kind)
}

func classify(op ChangeKind, kind Kind) Class {
	if op == ChangeRemoved {
		return ClassRemoval
	}

	if class, ok := ClassOfKind(kind); ok {
		return class
	}

	return ClassRemoval
}

// Related is a further changed dependency that reaches the same assessment.
type Related struct {
	Class      Class      `json:"class"`
	Change     ChangeKind `json:"change"`
	Dependency string     `json:"dependency"`
}

// ReviewTrigger is one trigger as a report shows it: the changed dependency, its class, the kind of
// change, the assumptions and assessment it reaches, the shortest path, and the next action.
type ReviewTrigger struct {
	// Class is the class of the primary change.
	Class Class `json:"class"`
	// Change is `added`, `removed`, or `modified`.
	Change ChangeKind `json:"change"`
	// Dependency is the changed dependency as `kind:identity`.
	Dependency string `json:"dependency"`
	// Assessment is the reached assessment.
	Assessment string `json:"assessment"`
	// Action is ActionGetReview.
	Action string `json:"action"`
	// Assumption lists the assumptions on the paths, sorted. Never nil.
	Assumption []string `json:"assumption"`
	// Path is the shortest path from the primary change to the assessment. Never nil.
	Path []string `json:"path"`
	// Evidence lists every node that contributes, sorted. Never nil.
	Evidence []string `json:"evidence"`
	// Related lists the other changed dependencies that reach the assessment. Never nil.
	Related []Related `json:"related"`
}

// Text gives the sentence that terminal output, SARIF, and JSON messages use. It names the changed
// dependency, the class, the assessment, and the action. It asks for a review and never says that
// the system is defective or acceptable.
func (t ReviewTrigger) Text() string { //nolint:gocritic // a report value; a pointer receiver would not suit the slices of values.
	return fmt.Sprintf("%s of %s is a reason to review %s; class: %s; next action: %s",
		t.Change.noun(), t.Dependency, t.Assessment, t.Class.Label(), t.Action)
}

// ChangeText gives the change and its class for terminal output, where the assessment and the next action are already
// shown: `modification of implementation:file:a.ts, class implementation or verification`.
func (t ReviewTrigger) ChangeText() string { //nolint:gocritic // a report value, like Text.
	return fmt.Sprintf("%s of %s, class %s", t.Change.noun(), t.Dependency, t.Class.Label())
}

// noun gives the word that Text uses for the kind of change.
func (k ChangeKind) noun() string {
	switch k {
	case ChangeAdded:
		return "addition"
	case ChangeRemoved:
		return "removal"
	case ChangeModified:
		return "modification"
	default:
		return string(k)
	}
}

// DependencyName gives `kind:identity` of a change.
func DependencyName(kind Kind, identity string) string { return string(kind) + ":" + identity }

// ChangeOfSeed reads the kind and identity back from the seed of an item node (see ItemNodeID).
// It reports false for a node that is not an item node.
func ChangeOfSeed(s Seed) (Change, bool) {
	kindText, identity, ok := strings.Cut(s.Node.Identity, ":")
	if !ok || !Kind(kindText).Valid() {
		return Change{}, false
	}

	return Change{Op: s.Op, Kind: Kind(kindText), Identity: identity}, true
}

// NewReviewTrigger turns a traversal trigger for an assessment into the report form. The primary
// change is the changed item that the shortest path starts at; the other changed items are listed
// as related. The graph, when not nil, tells which evidence nodes are assumptions. A seed that is
// not an item node is classified as ClassRemoval, never dropped.
func NewReviewTrigger(trig Trigger, graph *Graph) ReviewTrigger { //nolint:gocritic // Trigger is a value type that Traverse returns in slices.
	out := ReviewTrigger{
		Assumption: []string{}, Path: []string{}, Evidence: []string{}, Related: []Related{},
		Assessment: trig.Target.String(), Action: ActionGetReview,
	}

	for _, n := range trig.Path {
		out.Path = append(out.Path, n.String())
	}

	for _, n := range trig.Evidence {
		out.Evidence = append(out.Evidence, n.String())

		if graph != nil && graph.Type(n) == NodeAssumption {
			out.Assumption = append(out.Assumption, n.String())
		}
	}

	slices.SortFunc(out.Assumption, canonjson.CompareUTF16)
	slices.SortFunc(out.Evidence, canonjson.CompareUTF16)

	var primary Seed

	if len(trig.Path) > 0 {
		for _, s := range trig.Changed {
			if s.Node == trig.Path[0] {
				primary = s
			}
		}
	}

	if primary.Node == (NodeID{}) && len(trig.Changed) > 0 {
		primary = trig.Changed[0]
	}

	described := func(s Seed) Related {
		if c, ok := ChangeOfSeed(s); ok {
			return Related{Class: ClassifyChange(c), Change: s.Op, Dependency: DependencyName(c.Kind, c.Identity)}
		}

		return Related{Class: ClassRemoval, Change: s.Op, Dependency: s.Node.String()}
	}

	main := described(primary)
	out.Class, out.Change, out.Dependency = main.Class, main.Change, main.Dependency

	for _, s := range trig.Changed {
		if s != primary {
			out.Related = append(out.Related, described(s))
		}
	}

	return out
}
