package intent

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

type obj = map[string]any

func asObj(v any) (obj, bool) {
	m, ok := v.(obj)

	return m, ok
}

func asArr(v any) []any {
	list, _ := v.([]any)

	return list
}

func asStr(v any) string {
	s, _ := v.(string)

	return s
}

func strList(v any) []string {
	out := []string{}

	for _, item := range asArr(v) {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

func isObjKey(m obj, key string) bool {
	_, ok := asObj(m[key])

	return ok
}

// eachObject calls visit for each object of the JSON array raw, with the
// pointer base/index of the item. Items that are not objects are skipped.
func eachObject(raw any, base string, visit func(item obj, pointer string)) {
	for i, item := range asArr(raw) {
		if o, ok := asObj(item); ok {
			visit(o, base+"/"+strconv.Itoa(i))
		}
	}
}

func esc(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func sortedKeys(m obj) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	return keys
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}

// readJSONFile reads and parses a JSON file.
func readJSONFile(file string) (any, error) {
	data, err := os.ReadFile(file) //nolint:gosec // the path comes from the architecture document or the URL mapping.
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller adds the reference.
	}

	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err //nolint:wrapcheck // the caller adds the reference.
	}

	return value, nil
}

// metadataOf gives the CALM `metadata`, an object or an array of objects, as one object.
func metadataOf(raw any) map[string]any {
	out := map[string]any{}

	if m, ok := asObj(raw); ok {
		maps.Copy(out, m)

		return out
	}

	for _, item := range asArr(raw) {
		m, _ := asObj(item)
		maps.Copy(out, m)
	}

	return out
}

// adrsOf gives the string entries of the CALM `adrs` array, in document order; nil when there are none.
func adrsOf(raw any) []string {
	out := strList(raw)
	if len(out) == 0 {
		return nil
	}

	return out
}
