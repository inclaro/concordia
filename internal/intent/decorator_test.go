package intent_test

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const deploymentStandard = "https://calm.finos.org/standards/deployment/1.0/deployment.decorator.standard.json"

// decorator gives the base decorator JSON after mutate changes it.
func decorator(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()

	doc := map[string]any{
		"unique-id":  "d1",
		"type":       "deployment",
		"target":     []any{"../a.calm.json"},
		"applies-to": []any{"web"},
		"data":       map[string]any{"status": "completed"},
	}
	if mutate != nil {
		mutate(doc)
	}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func standardDecorator(start bool) func(map[string]any) {
	return func(m map[string]any) {
		m["$schema"] = deploymentStandard
		m["target-type"] = []any{"architecture"}

		if start {
			m["data"] = map[string]any{"status": "completed", "start-time": "2026-02-23T10:00:00Z"}
		}
	}
}

// decoratorTestFile is one decorator document written to the `d` directory of a test tree.
type decoratorTestFile struct{ name, text string }

// decoratorWant says what a decorator load must report; an empty code means no diagnostics.
type decoratorWant struct {
	code     string
	uri      string
	pointer  string
	detail   string
	notCode  string
	resource string
}

type decoratorCase struct {
	mapping map[string]string // URL -> file in the directory
	extra   map[string]string // more files
	name    string
	calm    string
	want    decoratorWant
	files   []decoratorTestFile
	missing bool // add a decorator path that does not exist
}

// decoratorOne gives a single decorator file built from the base decorator and mutate.
func decoratorOne(t *testing.T, name string, mutate func(map[string]any)) []decoratorTestFile {
	t.Helper()

	return []decoratorTestFile{{name, decorator(t, mutate)}}
}

func decoratorSetSchema(url string) func(map[string]any) {
	return func(m map[string]any) { m["$schema"] = url }
}

func decoratorTargetCases(t *testing.T) []decoratorCase {
	t.Helper()

	return []decoratorCase{
		{name: "valid", files: decoratorOne(t, "base", nil)},
		{
			name: "unknown applies-to",
			files: decoratorOne(t, "ghost", func(m map[string]any) {
				m["applies-to"] = []any{"missing"}
			}),
			want: decoratorWant{code: "CO-INT-009", uri: "d/ghost.json", pointer: "/applies-to/0"},
		},
		{
			name: "other target",
			files: decoratorOne(t, "other", func(m map[string]any) {
				m["target"] = []any{"other.json"}
				m["applies-to"] = []any{"missing"}
			}),
			want: decoratorWant{code: "CO-INT-010", pointer: "/target", notCode: "CO-INT-009"},
		},
		{
			name:  "extra property",
			files: decoratorOne(t, "extra", func(m map[string]any) { m["owner"] = "x" }),
			want:  decoratorWant{code: "CO-INT-008"},
		},
		{
			name:  "duplicate unique-id",
			files: []decoratorTestFile{{"first", decorator(t, nil)}, {"second", decorator(t, nil)}},
			want:  decoratorWant{code: "CO-INT-004", uri: "d/second.json", pointer: "/unique-id"},
		},
		{name: "missing file", missing: true, want: decoratorWant{code: "CO-INT-001"}},
	}
}

func decoratorStandardCases(t *testing.T) []decoratorCase {
	t.Helper()

	const exampleStandard = "https://example.org/std.json"

	return []decoratorCase{
		{
			name:  "standard violation",
			files: decoratorOne(t, "std", standardDecorator(false)),
			want:  decoratorWant{code: "CO-INT-008", detail: "(standard " + deploymentStandard + ")"},
		},
		{name: "standard valid", files: decoratorOne(t, "std", standardDecorator(true))},
		{
			name:  "standard decorator with CALM 1.2",
			calm:  "1.2",
			files: decoratorOne(t, "std", standardDecorator(true)),
			want:  decoratorWant{code: "CO-INT-008"},
		},
		{
			name:    "mapped standard",
			files:   decoratorOne(t, "mapped", decoratorSetSchema(exampleStandard)),
			mapping: map[string]string{exampleStandard: "std.json"},
			extra:   map[string]string{"std.json": `{"type":"object","properties":{"data":{"required":["foo"]}}}`},
			want:    decoratorWant{code: "CO-INT-008", resource: exampleStandard},
		},
		{
			name:  "unresolved standard",
			files: decoratorOne(t, "remote", decoratorSetSchema(exampleStandard)),
			want:  decoratorWant{code: "CO-INT-012", uri: "d/remote.json", pointer: "/$schema"},
		},
	}
}

// decoratorTree gives the files of a test workspace: the architecture at the given CALM
// release, the extra files, and every decorator under `d`.
func decoratorTree(tc *decoratorCase) map[string]string {
	version := tc.calm
	if version == "" {
		version = "1.3"
	}

	tree := map[string]string{
		"a.calm.json": `{"$schema":"https://calm.finos.org/release/` + version + `/meta/calm.json",` +
			`"nodes":[{"unique-id":"web","node-type":"service","name":"Web","description":"d"}],` +
			`"relationships":[{"unique-id":"r","relationship-type":{"interacts":{"actor":"web","nodes":["web"]}}}]}`,
	}

	maps.Copy(tree, tc.extra)

	for _, f := range tc.files {
		tree["d/"+f.name+".json"] = f.text
	}

	return tree
}

func (tc *decoratorCase) load(t *testing.T) intent.LoadResult {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, decoratorTree(tc))

	paths := make([]string, 0, len(tc.files)+1)
	for _, f := range tc.files {
		paths = append(paths, filepath.Join(dir, "d", f.name+".json"))
	}

	if tc.missing {
		paths = append(paths, filepath.Join(dir, "d", "absent.json"))
	}

	mapping := map[string]string{}
	for url, file := range tc.mapping {
		mapping[url] = filepath.Join(dir, file)
	}

	return intent.LoadArchitecture(filepath.Join(dir, "a.calm.json"), intent.LoadContext{
		Root: dir, URLMapping: mapping, Decorators: paths,
	})
}

func decoratorSourceURI(d *diag.Diagnostic) string {
	if d.Source == nil {
		return ""
	}

	return d.Source.URI
}

func decoratorSourcePointer(d *diag.Diagnostic) string {
	if d.Source == nil {
		return ""
	}

	return d.Source.Pointer
}

func (w *decoratorWant) check(t *testing.T, r *intent.LoadResult) {
	t.Helper()

	if w.code == "" {
		if len(r.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %+v", r.Diagnostics)
		}

		return
	}

	d := findCode(r, w.code)
	if d == nil {
		t.Fatalf("diagnostics = %+v, want %s", r.Diagnostics, w.code)
	}

	w.checkSource(t, d)
	w.checkDetail(t, d)
	w.checkAbsent(t, r)
	w.checkResource(t, r)
}

func (w *decoratorWant) checkSource(t *testing.T, d *diag.Diagnostic) {
	t.Helper()

	if w.uri != "" && decoratorSourceURI(d) != w.uri {
		t.Errorf("source = %+v, want URI %s", d.Source, w.uri)
	}

	if w.pointer != "" && decoratorSourcePointer(d) != w.pointer {
		t.Errorf("source = %+v, want pointer %s", d.Source, w.pointer)
	}
}

func (w *decoratorWant) checkDetail(t *testing.T, d *diag.Diagnostic) {
	t.Helper()

	if w.detail != "" && !strings.Contains(d.Message, w.detail) {
		t.Errorf("message = %q, want %q", d.Message, w.detail)
	}
}

func (w *decoratorWant) checkAbsent(t *testing.T, r *intent.LoadResult) {
	t.Helper()

	if w.notCode != "" && findCode(r, w.notCode) != nil {
		t.Errorf("diagnostics = %+v, want no %s", r.Diagnostics, w.notCode)
	}
}

func (w *decoratorWant) checkResource(t *testing.T, r *intent.LoadResult) {
	t.Helper()

	if w.resource == "" {
		return
	}

	found := slices.ContainsFunc(r.Resources, func(res workspace.ResourceRef) bool { return res.URI == w.resource })
	if !found {
		t.Errorf("resources = %+v, want %s", r.Resources, w.resource)
	}
}

func TestLoadValidatesDecorators(t *testing.T) {
	t.Parallel()

	cases := slices.Concat(decoratorTargetCases(t), decoratorStandardCases(t))

	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := tc.load(t)
			tc.want.check(t, &r)
		})
	}
}
