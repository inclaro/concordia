package trust

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// State is the result of comparing one dimension across a flow.
type State string

// Crossing states.
const (
	// StateCrossing means both ends have known, different values.
	StateCrossing State = "crossing"
	// StateSame means both ends have known, equal values.
	StateSame State = "same"
	// StateUnknown means at least one end has no value.
	StateUnknown State = "unknown"
)

// FlowKind says where a flow comes from.
type FlowKind string

// Flow kinds.
const (
	// FlowRelationship is a CALM relationship (`connects` or `interacts`).
	FlowRelationship FlowKind = "relationship"
	// FlowStep is one transition of a CALM flow.
	FlowStep FlowKind = "flow-step"
	// FlowObserved is an observed call.
	FlowObserved FlowKind = "observed"
)

// DirectionSeparator joins the source and destination values of a direction.
const DirectionSeparator = " → "

// Endpoint is one end of a flow: a CALM node, a binding entry, or an observed
// destination that nothing binds.
type Endpoint struct {
	// ID identifies the end (a CALM node ID, or an observed entity ID).
	ID string
	// Trust is what the end declares. The zero value declares nothing.
	Trust Declaration
	// External says that the end is an observed destination that no binding maps
	// to a node or to a binding entry with trust values.
	External bool
}

// Flow is a directed flow between two endpoints.
type Flow struct {
	// ID is the identity of the flow in the output: the relationship ID, the flow
	// step (`<flow>/<sequence>`), or the observed edge.
	ID string
	// Kind says where the flow comes from; empty means FlowRelationship.
	Kind FlowKind
	// Relationship is the CALM relationship that a flow step uses.
	Relationship string
	// Classification is the data classification that the flow declares (the
	// relationship's own declaration). Empty means "not declared".
	Classification string
	Source         Endpoint
	Destination    Endpoint
}

// Crossing is the result for one dimension of one flow.
type Crossing struct {
	Flow         string   `json:"flow"`
	Kind         FlowKind `json:"kind"`
	Relationship string   `json:"relationship,omitempty"`
	// Source and Destination are the IDs of the two ends (CALM node IDs, or the observed identity of an external destination).
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Dimension   string `json:"dimension"`
	// From and To are the dimension values of the source and the destination, or
	// "unknown" when an end declares none.
	From string `json:"from"`
	To   string `json:"to"`
	// Direction is `From → To`, kept from source to destination.
	Direction string `json:"direction"`
	State     State  `json:"state"`
	// Classification is the data classification of the flow, "unknown" when not declared.
	Classification string `json:"classification"`
	// ExternalDestination is set when the destination is an observed destination
	// with no trust declaration; its trust is unknown.
	ExternalDestination bool `json:"externalDestination,omitempty"`
}

// Classification gives the data classification text of a declared value: the
// value itself, or "unknown" when none was declared.
func Classification(declared string) string {
	if declared == "" {
		return Unknown
	}

	return declared
}

// Crossings derives one crossing per dimension of the vocabulary for the flow, in
// dimension order of CompareUTF16. An end that has no value for a dimension makes
// the result `unknown`; nothing but a declared value gives `same` or `crossing`.
func Crossings(v Vocabulary, flow *Flow) []Crossing {
	dimensions := v.Dimensions()
	slices.SortFunc(dimensions, canonjson.CompareUTF16)

	kind := flow.Kind
	if kind == "" {
		kind = FlowRelationship
	}

	class := Classification(flow.Classification)
	out := make([]Crossing, 0, len(dimensions))

	for _, dimension := range dimensions {
		from, dest := flow.Source.Trust.Value(dimension), flow.Destination.Trust.Value(dimension)

		state := StateUnknown

		switch {
		case from == "" || dest == "":
			from, dest = orUnknown(from), orUnknown(dest)
		case from == dest:
			state = StateSame
		default:
			state = StateCrossing
		}

		out = append(out, Crossing{
			Flow: flow.ID, Kind: kind, Relationship: flow.Relationship, Dimension: dimension,
			Source: flow.Source.ID, Destination: flow.Destination.ID,
			From: from, To: dest, Direction: from + DirectionSeparator + dest,
			State: state, Classification: class,
			ExternalDestination: flow.Destination.External,
		})
	}

	return out
}

func orUnknown(value string) string {
	if value == "" {
		return Unknown
	}

	return value
}

// Sort orders crossings by flow identity, then by dimension, with
// canonjson.CompareUTF16.
func Sort(crossings []Crossing) {
	slices.SortStableFunc(crossings, func(a, b Crossing) int {
		if c := canonjson.CompareUTF16(a.Flow, b.Flow); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Dimension, b.Dimension)
	})
}
