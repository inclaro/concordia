// Package yamlparse reads one YAML document into plain Go values (maps with
// string keys, slices, scalars) and says where a problem is.
package yamlparse

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Problem is one syntax error or duplicate key.
type Problem struct {
	Message string
	// Line is 1-based. 0 means unknown.
	Line int
}

var lineText = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)

// Parse decodes the first YAML document of text. A syntax error or a
// duplicate mapping key gives problems and no value. Empty text gives nil.
func Parse(text []byte) (any, []Problem) {
	var raw any
	if err := yaml.Unmarshal(text, &raw); err != nil {
		return nil, problems(err)
	}

	return normalize(raw), nil
}

// ParseNode decodes the first YAML document of text into its node tree, which keeps the
// 1-based line and column of every node. It gives the root node of the document. Problems are
// the same as for Parse. Empty text gives a nil node.
func ParseNode(text []byte) (*yaml.Node, []Problem) {
	var doc yaml.Node
	if err := yaml.Unmarshal(text, &doc); err != nil {
		return nil, problems(err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil
	}

	return doc.Content[0], nil
}

func problems(err error) []Problem {
	var typeErr *yaml.TypeError

	texts := []string{err.Error()}
	if errors.As(err, &typeErr) {
		texts = typeErr.Errors
	}

	out := make([]Problem, 0, len(texts))

	for _, text := range texts {
		first, _, _ := strings.Cut(text, "\n")

		if m := lineText.FindStringSubmatch(first); m != nil {
			line, _ := strconv.Atoi(m[1])
			out = append(out, Problem{Line: line, Message: m[2]})

			continue
		}

		out = append(out, Problem{Message: strings.TrimPrefix(first, "yaml: ")})
	}

	return out
}

// normalize changes the maps that the decoder makes with non-string keys into
// maps with string keys, as JSON needs them.
func normalize(node any) any {
	switch val := node.(type) {
	case map[string]any:
		for key, item := range val {
			val[key] = normalize(item)
		}

		return val
	case map[any]any:
		out := make(map[string]any, len(val))
		for key, item := range val {
			out[fmt.Sprint(key)] = normalize(item)
		}

		return out
	case []any:
		for i, item := range val {
			val[i] = normalize(item)
		}

		return val
	default:
		return node
	}
}
