package workspace_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/workspace"
)

func TestLoadLimitsDefaults(t *testing.T) {
	t.Parallel()

	w, err := workspace.Load(writeWorkspace(t, envHead), "")
	if err != nil {
		t.Fatal(err)
	}

	if w.Limits != workspace.DefaultLimits() {
		t.Errorf("limits = %+v, want the defaults %+v", w.Limits, workspace.DefaultLimits())
	}
}

func TestLoadLimitsSetKeysOverrideOnlyThemselves(t *testing.T) {
	t.Parallel()

	w, err := workspace.Load(writeWorkspace(t, envHead+"limits:\n  graph: {maxNodes: 10}\n  traversal: {maxVisited: 7}\n"), "")
	if err != nil {
		t.Fatal(err)
	}

	want := workspace.DefaultLimits()
	want.Graph.MaxNodes, want.Traversal.MaxVisited = 10, 7

	if w.Limits != want {
		t.Errorf("limits = %+v, want %+v", w.Limits, want)
	}
}

func TestLoadLimitsInvalidGivesWRK002(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"zero":          "limits:\n  graph: {maxNodes: 0}\n",
		"negative":      "limits:\n  evidence: {maxFacts: -1}\n",
		"not a number":  "limits:\n  traversal: {maxVisited: many}\n",
		"unknown key":   "limits:\n  graph: {maxFiles: 1}\n",
		"unknown group": "limits:\n  rendering: {maxNodes: 1}\n",
	}

	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := workspace.Load(writeWorkspace(t, envHead+extra), "")
			if de := failure(t, err); de.Diagnostic.Code != codeWRK002 {
				t.Errorf("%+v", de.Diagnostic)
			}
		})
	}
}
