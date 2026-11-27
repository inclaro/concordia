package cloudfw

import (
	"maps"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/redact"
)

// redactionPrefix opens every marker that the plan reader writes in place of a sensitive value or of a credential
// in a string. A redacted value was not read: the evaluator treats it as a value that is unknown until apply.
var redactionPrefix = strings.TrimSuffix(redact.Marker(""), "]")

// isRedacted reports whether the text is, or holds, a redaction marker.
func isRedacted(text string) bool { return strings.Contains(text, redactionPrefix) }

// redactedAt reports whether the value at the path, a value that holds it, or a part of it is redacted.
func redactedAt(r *plan.Resource, path string) bool {
	cur, inside := redactedWalk(r, path)
	if inside {
		return true
	}

	return holdsRedaction(cur)
}

// listRedacted reports whether the list at the path, a value that holds it, or a whole element of it is redacted:
// its rules or interfaces are then not known.
func listRedacted(r *plan.Resource, path string) bool {
	cur, inside := redactedWalk(r, path)
	if inside {
		return true
	}

	list, isList := cur.([]any)
	if !isList {
		list = []any{cur}
	}

	return slices.ContainsFunc(list, func(item any) bool {
		text, isText := item.(string)

		return isText && isRedacted(text)
	})
}

// redactedWalk walks the path. inside is true when a redacted text stands where the path needs an object or a list;
// otherwise cur is the value at the path, or nil when the path is absent.
func redactedWalk(r *plan.Resource, path string) (cur any, inside bool) {
	steps := pathTokens(path)
	cur = r.Values

	for i := range steps {
		if text, isText := cur.(string); isText {
			return nil, isRedacted(text)
		}

		next, ok := steps.child(cur, i)
		if !ok {
			return nil, false
		}

		cur = next
	}

	return cur, false
}

// holdsRedaction reports whether the value is, or holds, a redacted text.
func holdsRedaction(v any) bool {
	switch typed := v.(type) {
	case string:
		return isRedacted(typed)
	case []any:
		return slices.ContainsFunc(typed, holdsRedaction)
	case map[string]any:
		return slices.ContainsFunc(slices.Collect(maps.Values(typed)), holdsRedaction)
	}

	return false
}
