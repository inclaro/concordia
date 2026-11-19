package intent

import (
	"math"
	"sort"
	"strconv"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/trust"
)

// trustOf reads a `concordia.trust` object that the extension schema has accepted
// and validates it against the workspace vocabulary (CO-SEC-001): a dimension name
// that is neither built in nor declared, a classification outside the list. A
// declaration with a problem keeps its valid parts only, so a later check never
// uses an undeclared dimension. It gives nil when the element declares no trust.
func (l *loader) trustOf(raw any, pointer string) *trust.Declaration {
	tr, ok := asObj(raw)
	if !ok {
		return nil
	}

	decl := trust.Declaration{Classification: asStr(tr["classification"])}

	if dims, isObj := asObj(tr["dimensions"]); isObj {
		decl.Dimensions = make(map[string]string, len(dims))

		for name, value := range dims {
			decl.Dimensions[name] = asStr(value)
		}
	}

	valid, problems := l.ctx.Trust.Valid(decl)
	for _, p := range problems {
		l.add(diag.SEC001, diag.Detail(p.Detail()), p.Pointer(pointer))
	}

	if valid.IsZero() {
		return nil
	}

	return &valid
}

// collectFlow records the transitions of a CALM flow.
func (l *loader) collectFlow(raw obj, pointer string) {
	id, ok := raw["unique-id"].(string)
	if !ok {
		return
	}

	flow := FlowDeclaration{ID: id, Pointer: pointer, Transitions: []FlowTransition{}}

	for i, item := range asArr(raw["transitions"]) {
		step, isObj := asObj(item)
		if !isObj {
			continue
		}

		relID, hasRel := step["relationship-unique-id"].(string)
		if !hasRel {
			continue
		}

		direction := asStr(step["direction"])
		if direction == "" {
			direction = "source-to-destination"
		}

		sequence := 0
		if n, isNum := step["sequence-number"].(float64); isNum && n == math.Trunc(n) {
			sequence = int(n)
		}

		flow.Transitions = append(flow.Transitions, FlowTransition{
			RelationshipID: relID, Direction: direction, Sequence: sequence,
			Pointer: pointer + "/transitions/" + strconv.Itoa(i),
		})
	}

	sort.SliceStable(flow.Transitions, func(a, b int) bool { return flow.Transitions[a].Sequence < flow.Transitions[b].Sequence })

	l.flows = append(l.flows, flow)
}
