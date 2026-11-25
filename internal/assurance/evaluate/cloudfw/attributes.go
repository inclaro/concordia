package cloudfw

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// pathSteps are the keys and indexes of an attribute path, in order.
type pathSteps []string

// pathTokens splits an attribute path into its keys and indexes. Both `ingress.0.cidr_blocks` and
// `ingress[0].cidr_blocks` give `ingress`, `0`, `cidr_blocks`.
func pathTokens(path string) pathSteps {
	path = strings.ReplaceAll(strings.ReplaceAll(path, "[", "."), "]", "")

	return strings.FieldsFunc(path, func(r rune) bool { return r == '.' })
}

// planPath is the path in the form of plan.Resource.Unknown and plan.Resource.References: `ingress[0].cidr_blocks`.
func planPath(path string) string {
	var out strings.Builder

	for _, tok := range pathTokens(path) {
		if _, err := strconv.Atoi(tok); err == nil {
			out.WriteString("[" + tok + "]")

			continue
		}

		if out.Len() > 0 {
			out.WriteByte('.')
		}

		out.WriteString(tok)
	}

	return out.String()
}

// attr is the known value at the path of the planned values, or nil when the value is absent, null, or unknown.
func attr(r *plan.Resource, path string) any {
	return pathTokens(path).lookup(r.Values)
}

// lookup walks the steps from the root value. It gives nil when a step is absent.
func (steps pathSteps) lookup(root map[string]any) any {
	var cur any = root

	for i := range steps {
		next, ok := steps.child(cur, i)
		if !ok {
			return nil
		}

		cur = next
	}

	return cur
}

// child is the value that step i selects in cur: a key of an object or an index of a list.
func (steps pathSteps) child(cur any, i int) (any, bool) {
	switch typed := cur.(type) {
	case map[string]any:
		v, ok := typed[steps[i]]

		return v, ok
	case []any:
		return steps.element(typed, i)
	}

	return nil, false
}

// element is the item of the list at the index that step i holds.
func (steps pathSteps) element(list []any, i int) (any, bool) {
	index, err := strconv.Atoi(steps[i])
	if err != nil {
		return nil, false
	}

	if !inRange(0, len(list)-1, index) {
		return nil, false
	}

	return list[index], true
}

// related reports whether one path is the other or a part of it.
func related(a, b pathSteps) bool {
	n := min(len(a), len(b))

	return slices.Equal(a[:n], b[:n])
}

// attrString is the known string at the path.
func attrString(r *plan.Resource, path string) (string, bool) {
	return scalarText(attr(r, path))
}

// attrStrings is the known string, or the known strings of the list, at the path. Numbers become their text; empty
// strings and other values are left out.
func attrStrings(r *plan.Resource, path string) []string {
	value := attr(r, path)

	list, isList := value.([]any)
	if !isList {
		list = []any{value}
	}

	var out []string

	for _, item := range list {
		if s, ok := scalarText(item); ok && s != "" {
			out = append(out, s)
		}
	}

	return out
}

func scalarText(v any) (string, bool) {
	switch typed := v.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	}

	return "", false
}

// attrInt is the known integer at the path (a number, or a string that holds one).
func attrInt(r *plan.Resource, path string) (int, bool) {
	s, ok := attrString(r, path)
	if !ok {
		return 0, false
	}

	n, err := strconv.Atoi(s)

	return n, err == nil
}

// attrBool is true when the known value at the path is true.
func attrBool(r *plan.Resource, path string) bool {
	value, ok := attr(r, path).(bool)

	return ok && value
}

// blockCount is the number of known elements of the list or nested block at the path.
func blockCount(r *plan.Resource, path string) int {
	list, _ := attr(r, path).([]any)

	return len(list)
}

// unknownAt reports whether the attribute at the path, a part of it, or a value that holds it is unknown until apply.
func unknownAt(r *plan.Resource, path string) bool {
	steps := pathTokens(path)

	return slices.ContainsFunc(r.Unknown, func(u string) bool { return related(pathTokens(u), steps) })
}

// listHasUnknownElements reports whether Unknown marks a whole element of the list at the path, for example
// `ingress[1]`. The plan reader keeps such an element as null in Values; its rule or interface is not known, so a
// provider treats the list as unknown.
func listHasUnknownElements(r *plan.Resource, path string) bool {
	list := pathTokens(path)

	return slices.ContainsFunc(r.Unknown, func(u string) bool { return isElementOf(pathTokens(u), list) })
}

// listUnknown reports whether the list at the path is unknown until apply as a whole or in whole elements.
func listUnknown(r *plan.Resource, path string) bool {
	return slices.Contains(r.Unknown, planPath(path)) || listHasUnknownElements(r, path)
}

// isElementOf reports whether the steps name one element of the list: the list steps and one index.
func isElementOf(steps, list pathSteps) bool {
	if len(steps) != len(list)+1 || !related(steps, list) {
		return false
	}

	_, err := strconv.Atoi(steps[len(list)])

	return err == nil
}
