package plan

import (
	"strconv"

	"github.com/inclaro/concordia/internal/redact"
)

// walker walks the `after` value of one resource beside its `after_unknown` and `after_sensitive` mirrors.
type walker struct {
	address    string
	file       string
	unknown    []string
	redactions []redact.Record
}

// mark gives the mirror entry for a key of an object: false when the mirror has none.
func mark(mirror any, key string) any {
	if m, ok := mirror.(map[string]any); ok {
		return m[key]
	}

	return nil
}

// markAt gives the mirror entry for an index of an array.
func markAt(mirror any, index int) any {
	if m, ok := mirror.([]any); ok && index < len(m) {
		return m[index]
	}

	return nil
}

func isTrue(v any) bool { b, ok := v.(bool); return ok && b }

// node is one value of `after` with its entries in the `after_unknown` and `after_sensitive` mirrors. name is the
// attribute name that the value belongs to; an array element has the name of its array.
type node struct {
	item      any
	unknown   any
	sensitive any
	path      string
	name      string
}

// child is the node for a key of an object.
func (n *node) child(item any, key string) *node {
	return &node{item: item, unknown: mark(n.unknown, key), sensitive: mark(n.sensitive, key), path: join(n.path, key), name: key}
}

// element is the node for an index of an array. An index beyond value has no item: only the mirror names it.
func (n *node) element(value []any, index int) *node {
	var item any
	if index < len(value) {
		item = value[index]
	}

	return &node{
		item: item, unknown: markAt(n.unknown, index), sensitive: markAt(n.sensitive, index),
		path: n.path + "[" + strconv.Itoa(index) + "]", name: n.name,
	}
}

// object cleans the attributes of an object. Its node holds the mirrors of the object.
func (w *walker) object(value map[string]any, n *node) map[string]any {
	out := make(map[string]any, len(value))

	for key, item := range value {
		if cleaned, keep := w.value(n.child(item, key)); keep {
			out[key] = cleaned
		}
	}

	w.unknownKeys(value, n)

	return out
}

// unknownKeys records the unknown attributes that are not in `after`; the mirror is the only place that names them.
func (w *walker) unknownKeys(value map[string]any, parent *node) {
	mirror, ok := parent.unknown.(map[string]any)
	if !ok {
		return
	}

	for key, u := range mirror {
		if _, present := value[key]; present {
			continue
		}

		w.value(&node{unknown: u, path: join(parent.path, key), name: key})
	}
}

// array cleans the elements of an array. Its node holds the mirrors of the array. An element that is unknown until
// apply keeps its position as null, so that the indexes of the other elements still match their paths; an index
// that only the mirror names adds no element.
func (w *walker) array(value []any, n *node) []any {
	count := len(value)
	if m, ok := n.unknown.([]any); ok {
		count = max(count, len(m))
	}

	out := make([]any, 0, len(value))

	for i := range count {
		cleaned, _ := w.value(n.element(value, i))
		if i < len(value) {
			out = append(out, cleaned)
		}
	}

	return out
}

// value cleans one value. keep is false when the value is unknown and so has no value to keep.
func (w *walker) value(current *node) (cleaned any, keep bool) {
	if isTrue(current.unknown) {
		w.unknown = append(w.unknown, current.path)

		return nil, false
	}

	if isTrue(current.sensitive) && current.item != nil {
		w.redact(current.path, redact.ClassSecretData)

		return redact.Marker(redact.ClassSecretData), true
	}

	switch typed := current.item.(type) {
	case map[string]any:
		return w.object(typed, current), true
	case []any:
		return w.array(typed, current), true
	case string:
		return w.text(typed, current), true
	case nil:
		w.partlyUnknown(current)

		return nil, true
	default:
		return current.item, true
	}
}

// text removes a credential from a string. The plan marks what Terraform knows to be sensitive; a credential in a
// plain attribute is still removed.
func (w *walker) text(value string, n *node) string {
	text, records := redact.Default().RedactValue(n.name, value)
	for _, r := range records {
		w.redact(n.path, r.Class)
	}

	return text
}

// partlyUnknown handles a null value: `after` omits an object or a list that is unknown in part, and the mirror still
// names the unknown parts.
func (w *walker) partlyUnknown(n *node) {
	rest := &node{unknown: n.unknown, path: n.path, name: n.name}

	switch n.unknown.(type) {
	case map[string]any:
		w.object(nil, rest)
	case []any:
		w.array(nil, rest)
	}
}

func (w *walker) redact(path, class string) {
	w.redactions = append(w.redactions, redact.Record{File: w.file, Property: w.address + "." + path, Class: class})
}

func join(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}
