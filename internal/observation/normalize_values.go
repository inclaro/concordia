package observation

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// StripProjectPrefix removes the prefix `<project>.` from a qualified name.
func StripProjectPrefix(project, qualifiedName string) string {
	return strings.TrimPrefix(qualifiedName, project+".")
}

func observedID(project, nativeID string) string {
	return "cbm:" + project + ":" + nativeID
}

// parseLabels reads `labels(x)`, which CBM gives as a JSON string, for example `["Module"]`.
func parseLabels(raw string) []string {
	if raw == "" {
		return nil
	}

	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return []string{raw}
	}

	list, ok := v.([]any)
	if !ok {
		return []string{raw}
	}

	return stringElements(list)
}

// stringElements gives the elements of a decoded JSON array that are strings.
func stringElements(list []any) []string {
	out := make([]string, 0, len(list))

	for _, x := range list {
		if s, isString := x.(string); isString {
			out = append(out, s)
		}
	}

	return out
}

// stringOf converts a value of a CBM result cell to text: null is empty,
// scalars are written as text, other values as JSON.
func stringOf(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case json.Number:
		return val.String()
	case bool:
		return strconv.FormatBool(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	default:
		data, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}

		return string(data)
	}
}

// parseIntPrefix reads the leading integer of text as JavaScript parseInt(text, 10) does.
func parseIntPrefix(text string) (int, bool) {
	text = strings.TrimLeft(text, " \t\n\r\v\f")

	signLen := leadingSignLength(text)
	digitLen := leadingDigitLength(text[signLen:])

	if digitLen == 0 {
		return 0, false
	}

	num, err := strconv.Atoi(text[:signLen+digitLen])
	if err != nil {
		return 0, false
	}

	return num, true
}

// leadingSignLength gives 1 when text starts with a `-` or `+`, else 0.
func leadingSignLength(text string) int {
	if strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		return 1
	}

	return 0
}

// leadingDigitLength gives the number of ASCII digits that text starts with.
func leadingDigitLength(text string) int {
	n := 0
	for n < len(text) && text[n] >= '0' && text[n] <= '9' {
		n++
	}

	return n
}

// NodeColumns are the result columns of one node in a CBM query. Each value
// is a JSON value as decoded from the result (usually a string or null).
type NodeColumns struct {
	ID            any
	Labels        any
	QualifiedName any
	FilePath      any
	StartLine     any
}

// ToObservedNode makes an observed node from the columns of a query. The label
// is the first label of `labels(x)`, else fallbackLabel.
func ToObservedNode(project string, cols *NodeColumns, fallbackLabel string) ObservedNode {
	label := fallbackLabel

	if labels := parseLabels(labelText(cols.Labels)); len(labels) > 0 {
		label = labels[0]
	}

	node := ObservedNode{
		ObservedID:    observedID(project, stringOf(cols.ID)),
		Label:         label,
		QualifiedName: StripProjectPrefix(project, stringOf(cols.QualifiedName)),
		FilePath:      stringOf(cols.FilePath),
	}

	if line, ok := parseIntPrefix(stringOf(cols.StartLine)); ok && line > 0 {
		node.StartLine = line
	}

	return node
}

func labelText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	return ""
}
