package cli

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	decisionRegisterPath = "governance/decisions.yaml"
	decisionLifecycle    = "concordia.yaml#adr.lifecycle"
	decisionRegisterText = "apiVersion: concordia.io/v1alpha1\nkind: DecisionRegister\ndecisions: []\n"
)

// decisionBundleRun is the A12 run whose inputs name the register file at its real digest.
func decisionBundleRun(t *testing.T, dir string) (*conformance.Run, *workspace.Workspace) {
	t.Helper()

	testutil.WriteTree(t, dir, map[string]string{
		"concordia.yaml":                  "apiVersion: concordia.io/v1alpha1\nkind: Workspace\n",
		decisionRegisterPath:              decisionRegisterText,
		"architecture/commerce.calm.json": "{}",
	})

	run := testutil.ADRRun(true)
	run.Inputs.Workspace = conformance.InputRef{Path: "concordia.yaml", Digest: digest.OfBytes([]byte("workspace"))}
	run.Inputs.Policies = []conformance.InputRef{}
	run.Inputs.Decisions = []conformance.InputRef{
		{Path: decisionRegisterPath, Digest: digest.OfBytes([]byte(decisionRegisterText))},
		{Path: decisionLifecycle, Digest: digest.OfBytes([]byte(`{"active":["accepted"],"statusMap":{}}`))},
	}

	return run, &workspace.Workspace{Root: dir}
}

func TestBundleHoldsTheRegisterUnderInputs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	run, wsp := decisionBundleRun(t, dir)
	app := &Context{}
	byPath := map[string][]byte{}

	if err := addInputs(app, wsp, run, byPath); err != nil {
		t.Fatal(err)
	}

	register, ok := byPath["inputs/"+decisionRegisterPath]
	if !ok {
		t.Fatalf("the bundle has no register: %v", slices.Sorted(maps.Keys(byPath)))
	}

	for p := range byPath {
		if strings.Contains(p, "#") {
			t.Errorf("the bundle holds a file for the pseudo-path %s", p)
		}
	}

	// The manifest digest of the register is the digest of the file that the bundle holds.
	if got, want := digest.OfBytes(register), run.Inputs.Decisions[0].Digest; got != want {
		t.Errorf("file digest = %s, manifest digest = %s", got, want)
	}

	provenance := bundleProvenance(app, run)

	want := []bundle.DigestRef{
		{Path: decisionRegisterPath, Digest: run.Inputs.Decisions[0].Digest},
		{Path: decisionLifecycle, Digest: run.Inputs.Decisions[1].Digest},
	}
	if !slices.Equal(provenance.Decisions, want) {
		t.Errorf("provenance decisions = %+v, want %+v", provenance.Decisions, want)
	}
}

func TestBundleReferencesTheRegisterAndTheLifecycleProfile(t *testing.T) {
	t.Parallel()

	run, _ := decisionBundleRun(t, t.TempDir())
	opts := &exportBundleOptions{}

	kinds := func(entries []bundle.Entry) map[string]bundle.Reference {
		out := map[string]bundle.Reference{}

		for _, r := range bundleReferences(run, opts, entries) {
			out[r.Kind] = r
		}

		return out
	}

	full := kinds([]bundle.Entry{{Path: "inputs/" + decisionRegisterPath}, {Path: "inputs/concordia.yaml"}})
	if r := full["decision"]; r.Availability != bundle.Included || r.Digest != run.Inputs.Decisions[0].Digest {
		t.Errorf("decision reference = %+v", r)
	}

	if r := full["decision-lifecycle"]; r.Availability != bundle.Included || r.Digest != run.Inputs.Decisions[1].Digest {
		t.Errorf("lifecycle reference = %+v", r)
	}

	partial := kinds(nil)
	if r := partial["decision"]; r.Availability != bundle.Unavailable {
		t.Errorf("decision reference without the file = %+v", r)
	}

	if r := partial["decision-lifecycle"]; r.Availability != bundle.Unavailable {
		t.Errorf("lifecycle reference without the workspace file = %+v", r)
	}
}

func TestBundleWithARegisterVerifies(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	run, wsp := decisionBundleRun(t, dir)
	app := &Context{}
	byPath := map[string][]byte{"results/results.json": []byte("{}")}

	if err := addInputs(app, wsp, run, byPath); err != nil {
		t.Fatal(err)
	}

	entries := make([]bundle.Entry, 0, len(byPath))
	for p, data := range byPath {
		entries = append(entries, bundle.Entry{Path: p, Data: data})
	}

	manifest, err := bundleManifest(app, run, &exportBundleOptions{}, entries, nil)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := bundle.Write(&buf, &manifest, entries); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "bundle.tar.gz")
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	verified, err := bundle.Verify(out)
	if err != nil {
		t.Fatal(err)
	}

	got := verified.Integrity.Files["inputs/"+decisionRegisterPath]
	if want := run.Inputs.Decisions[0].Digest; got != want {
		t.Errorf("integrity digest of the register = %s, manifest input digest = %s", got, want)
	}
}

func TestBundleWithoutARegisterHasNoDecisionEntries(t *testing.T) {
	t.Parallel()

	run := testutil.SampleRun()
	app := &Context{}

	if got := bundleProvenance(app, run).Decisions; len(got) != 0 {
		t.Errorf("provenance decisions = %+v", got)
	}

	for _, r := range bundleReferences(run, &exportBundleOptions{}, nil) {
		if strings.HasPrefix(r.Kind, "decision") {
			t.Errorf("reference %+v", r)
		}
	}
}
