package workspace_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

var (
	docKeyRow  = regexp.MustCompile("^\\| `([A-Za-z][A-Za-z0-9]*)` \\|")
	docHeading = regexp.MustCompile("^## `?([A-Za-z][A-Za-z0-9]*)`?\\s*$")
)

// schemaProps gives the `properties` of an object schema node.
func schemaProps(node map[string]any) map[string]any {
	props, _ := node["properties"].(map[string]any)

	return props
}

// TestDocumentedWorkspaceKeysExistInTheSchema checks the keys of docs/reference/workspace.md against the embedded
// schema: each key of the top-level table is a root property, and each key of a table directly under the
// `## <key>` heading of an object key is a property of it, and every root property is documented.
func TestDocumentedWorkspaceKeysExistInTheSchema(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("schemas/workspace.schema.json")
	if err != nil {
		t.Fatal(err)
	}

	var schema map[string]any
	if err = json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}

	root := schemaProps(schema)

	doc, err := os.ReadFile("../../docs/reference/workspace.md")
	if err != nil {
		t.Fatal(err)
	}

	section := ""
	documented := map[string]bool{}

	for _, line := range strings.Split(string(doc), "\n") {
		if strings.HasPrefix(line, "#") {
			section = ""
			if m := docHeading.FindStringSubmatch(line); m != nil {
				section = m[1]
			}

			if line == "## Top-level keys" {
				section = "<top>"
			}

			continue
		}

		m := docKeyRow.FindStringSubmatch(line)
		if m == nil || section == "" {
			continue
		}

		key := m[1]

		if section == "<top>" {
			documented[key] = true

			if _, ok := root[key]; !ok {
				t.Errorf("workspace.md documents top-level key %q, which the schema lacks", key)
			}

			continue
		}

		parent, ok := root[section].(map[string]any)
		if !ok {
			continue
		}

		if props := schemaProps(parent); props != nil {
			if _, found := props[key]; !found {
				t.Errorf("workspace.md documents %s.%s, which the schema lacks", section, key)
			}
		}
	}

	for key := range root {
		if !documented[key] {
			t.Errorf("schema key %q is not in the workspace.md top-level table", key)
		}
	}
}
