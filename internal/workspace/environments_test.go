package workspace_test

import (
	"reflect"
	"testing"

	"github.com/inclaro/concordia/internal/workspace"
)

const envHead = "observation:\n  projects:\n    - name: a\n"

func TestLoadEnvironmentsValid(t *testing.T) {
	t.Parallel()

	w, err := workspace.Load(writeWorkspace(t, envHead+"environments:\n  - id: prod\n    sources: [k8s/base/**, k8s/prod/**]\n    cluster: c1\n    description: p\n  - id: dev\n    sources: [k8s/base/**]\n"), "")
	if err != nil {
		t.Fatal(err)
	}

	if len(w.Environments) != 2 || w.Environments[0].ID != "prod" || w.Environments[0].Cluster != "c1" || len(w.Environments[0].Sources) != 2 {
		t.Errorf("%+v", w.Environments)
	}
}

func TestLoadEnvironmentsInvalidGivesWRK002(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"duplicate":          "environments:\n  - id: a\n    sources: [x]\n  - id: a\n    sources: [y]\n",
		"empty sources":      "environments:\n  - id: a\n    sources: []\n",
		"bad id":             "environments:\n  - id: Prod\n    sources: [x]\n",
		"unknown key":        "environments:\n  - id: a\n    sources: [x]\n    bogus: 1\n",
		"runtime undeclared": "observation:\n  projects:\n    - name: a\n  runtime:\n    traces: [t.jsonl]\n    environment: prod\n",
		"runtime other":      envHead + "  runtime:\n    traces: [t.jsonl]\n    environment: prod\nenvironments:\n  - id: dev\n    sources: [x]\n",
	}

	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			yaml := envHead + extra
			if name == "runtime undeclared" {
				yaml = extra
			}

			_, err := workspace.Load(writeWorkspace(t, yaml), "")
			if de := failure(t, err); de.Diagnostic.Code != codeWRK002 {
				t.Errorf("%+v", de.Diagnostic)
			}
		})
	}
}

func TestLoadRuntimeEnvironmentDeclaredPasses(t *testing.T) {
	t.Parallel()

	y := envHead + "  runtime:\n    traces: [t.jsonl]\n    environment: prod\nenvironments:\n  - id: prod\n    sources: [x]\n"
	if _, err := workspace.Load(writeWorkspace(t, y), ""); err != nil {
		t.Fatal(err)
	}
}

func TestAttribute(t *testing.T) {
	t.Parallel()

	w, err := workspace.Load(writeWorkspace(t, envHead+"environments:\n  - id: prod\n    sources: [k8s/base/**, k8s/prod/**]\n  - id: dev\n    sources: [k8s/base/**, k8s/dev/**]\n"), "")
	if err != nil {
		t.Fatal(err)
	}

	if got := w.Attribute("k8s/base/app.yaml"); !reflect.DeepEqual(got, []string{"prod", "dev"}) {
		t.Errorf("shared: %v", got)
	}

	if got := w.Attribute("k8s/prod/app.yaml"); !reflect.DeepEqual(got, []string{"prod"}) {
		t.Errorf("prod: %v", got)
	}

	if got := w.Attribute("src/main.go"); len(got) != 0 {
		t.Errorf("unmatched: %v", got)
	}
}
