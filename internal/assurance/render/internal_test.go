package render

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/workspace"
)

// Tests of the unexported parts: the confined copy, the pre-scan, the sandbox arguments, and the identity rule.
// The behavior of Render is tested through the exported API in the other files.

var forbiddenFlags = []string{
	"--dependency-update", "--enable-helm", "--enable-exec", "--enable-alpha-plugins", "--post-renderer", "--no-hooks",
	"--repo", "--version", "--kube-context", "--kubeconfig", "--validate", "--plugin",
}

// fixturesDir is test/fixtures/assurance.
func fixturesDir(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "test", "fixtures", "assurance")
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}

		dir = parent
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSettleIdentities(t *testing.T) {
	t.Parallel()

	id := func(name string) ResourceID {
		return ResourceID{Environment: "e", Kind: "ConfigMap", Namespace: "n", Name: name}
	}
	mk := func(name, loc string) entry {
		return entry{resource: Resource{ID: id(name), Location: loc}, canonical: []byte(loc)}
	}

	kept, conflicts := settleIdentities([]entry{mk("a", "x"), mk("b", "y"), mk("a", "z")})

	if len(kept) != 2 {
		t.Fatalf("kept = %+v", kept)
	}

	if !kept[0].resource.Conflict || kept[1].resource.Conflict {
		t.Fatalf("kept = %+v", kept)
	}

	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %+v", conflicts)
	}

	if conflicts[0].ID != id("a") {
		t.Errorf("conflicts = %+v", conflicts)
	}

	if !reflect.DeepEqual(conflicts[0].Locations, []string{"x", "z"}) {
		t.Errorf("conflicts = %+v", conflicts)
	}
}

func newTestCopier(t *testing.T, root string) (*copier, *workdir) {
	t.Helper()

	w, err := newWorkdir()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(w.close)

	return newCopier(root, w), w
}

func TestCopyRejectsSymlinkAndNeverReadsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "chart", "Chart.yaml"), "name: x\n")

	// A values file that is a link to a file outside the declared paths.
	if err := os.Symlink("/etc/hostname", filepath.Join(root, "chart", "values.yaml")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}

	c, w := newTestCopier(t, root)

	gap := c.copy("chart")
	if gap == nil || gap.Kind != GapSymlink {
		t.Fatalf("gap = %+v, want a symlink gap", gap)
	}

	if _, err := os.Lstat(filepath.Join(w.in, "chart", "values.yaml")); err == nil {
		t.Error("the link or its target was copied")
	}

	for p := range c.files {
		if strings.Contains(p, "values.yaml") {
			t.Errorf("the linked file %s was read and digested", p)
		}
	}
}

func TestCopyRejectsLinkInAComponent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "Chart.yaml"), "name: x\n")

	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}

	c, _ := newTestCopier(t, root)

	if gap := c.copy("linked/Chart.yaml"); gap == nil || gap.Kind != GapSymlink {
		t.Errorf("gap = %+v, want a symlink gap", gap)
	}

	if len(c.files) != 0 {
		t.Errorf("read through a link: %v", c.files)
	}
}

func TestCopyRejectsEscapes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	c, _ := newTestCopier(t, root)

	for _, p := range []string{"../x", "a/../../x", "..", "/etc/hostname"} {
		if gap := c.copy(p); gap == nil || gap.Kind != GapPathEscape {
			t.Errorf("copy(%q) = %+v, want a path-escape gap", p, gap)
		}
	}

	if gap := c.copy("missing"); gap == nil || gap.Kind != GapMissingSource {
		t.Errorf("copy(missing) = %+v", gap)
	}
}

func TestCopyKeepsLayoutAndDigestsContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "deploy", "chart", "Chart.yaml"), "name: x\n")
	mustWrite(t, filepath.Join(root, "deploy", "values", "prod.yaml"), "a: 1\n")
	mustWrite(t, filepath.Join(root, "deploy", "chart", ".git", "config"), "skipped\n")

	c, w := newTestCopier(t, root)

	for _, p := range []string{"deploy/chart", "deploy/values/prod.yaml", "deploy/chart/Chart.yaml"} {
		if gap := c.copy(p); gap != nil {
			t.Fatalf("copy(%s): %+v", p, gap)
		}
	}

	if _, err := os.Stat(filepath.Join(w.in, "deploy", "values", "prod.yaml")); err != nil {
		t.Error(err)
	}

	files := make([]string, 0, len(c.files))
	for p := range c.files {
		files = append(files, p)
	}

	slices.Sort(files)

	if want := []string{"deploy/chart/Chart.yaml", "deploy/values/prod.yaml"}; !reflect.DeepEqual(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}

	before := c.inputDigest(nil, "tool=helm")

	mustWrite(t, filepath.Join(root, "deploy", "values", "prod.yaml"), "a: 2\n")

	c2, _ := newTestCopier(t, root)
	_ = c2.copy("deploy/chart")
	_ = c2.copy("deploy/values/prod.yaml")

	if after := c2.inputDigest(nil, "tool=helm"); after == before {
		t.Error("a changed input must change the input digest")
	}

	if c.inputDigest(nil, "tool=helm") == c.inputDigest(nil, "tool=kustomize") {
		t.Error("the settings are part of the input digest")
	}
}

func TestScrubRemovesTheTemporaryPath(t *testing.T) {
	t.Parallel()

	w, err := newWorkdir()
	if err != nil {
		t.Fatal(err)
	}

	defer w.close()

	got := w.scrub("error in " + w.in + "/chart/x.yaml and " + w.home)
	if strings.Contains(got, w.dir) {
		t.Errorf("scrub left the temporary path: %s", got)
	}
}

func TestChildEnvHasNoAmbientVariables(t *testing.T) {
	// Not parallel: the test sets process environment variables.
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "KUBECONFIG", "HELM_REGISTRY_CONFIG", "AWS_ACCESS_KEY_ID", "GITHUB_TOKEN", "XDG_CONFIG_HOME"} {
		t.Setenv(name, "ambient-"+name)
	}

	for _, entry := range childEnv("/tmp/home") {
		if strings.Contains(entry, "ambient-") {
			t.Errorf("the ambient environment reached the child: %s", entry)
		}

		name, _, _ := strings.Cut(entry, "=")
		for _, banned := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "KUBECONFIG", "AWS_ACCESS_KEY_ID", "GITHUB_TOKEN"} {
			if name == banned {
				t.Errorf("%s is in the child environment", banned)
			}
		}
	}
}

func TestKustomizeBaseOutsideOverlayIsCopied(t *testing.T) {
	t.Parallel()

	root := fixturesDir(t)

	// The overlay refers to ../../base. The base is inside the workspace, so it is part of the declared inputs.
	w, err := newWorkdir()
	if err != nil {
		t.Fatal(err)
	}

	defer w.close()

	c := newCopier(filepath.Join(root, "kustomize-shop"), w)

	if findings := scanKustomize(c, "overlays/staging"); len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}

	for _, p := range []string{"overlays/staging/kustomization.yaml", "base/deployment.yaml", "base/kustomization.yaml"} {
		if _, ok := c.files[p]; !ok {
			t.Errorf("%s is not in the confined copy: %v", p, c.files)
		}
	}
}

func TestIsRemoteRef(t *testing.T) {
	t.Parallel()

	for ref, want := range map[string]bool{
		"https://github.com/x/y//base?ref=v1": true,
		"git@github.com:x/y.git":              true,
		"git::https://x/y":                    true,
		"github.com/x/y//deploy":              true,
		"../base":                             false,
		"./base":                              false,
		"base":                                false,
		"deployment.yaml":                     false,
		"/abs/base":                           false,
	} {
		if got := isRemoteRef(ref, false); got != want {
			t.Errorf("isRemoteRef(%q) = %v, want %v", ref, got, want)
		}
	}

	if isRemoteRef("my.dir/base", true) {
		t.Error("an existing local directory is not remote")
	}
}

func TestBwrapArgs(t *testing.T) {
	t.Parallel()

	spec := sandboxSpec{Bwrap: "/usr/bin/bwrap", Executable: "/opt/helm/helm", In: "/tmp/r/in", Home: "/tmp/r/home"}
	args := bwrapArgs(spec, []string{"/opt/helm/helm", "template", "./chart"})

	for _, flag := range []string{"--unshare-net", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--die-with-parent", "--new-session"} {
		if !slices.Contains(args, flag) {
			t.Errorf("%s is missing: %v", flag, args)
		}
	}

	checkBwrapBinds(t, args)

	if i := slices.Index(args, "--chdir"); i < 0 || args[i+1] != "/tmp/r/in" {
		t.Errorf("working directory: %v", args)
	}

	// The command follows `--` and is the last part.
	i := slices.Index(args, "--")
	if i < 0 || !slices.Equal(args[i+1:], []string{"/opt/helm/helm", "template", "./chart"}) {
		t.Errorf("command = %v", args[i+1:])
	}
}

// checkBwrapBinds checks the binds, in order and exactly: only the home directory is writable, and the root is
// never bound whole.
func checkBwrapBinds(t *testing.T, args []string) {
	t.Helper()

	binds, ro := bwrapBinds(args)

	if want := []string{"/tmp/r/home->/tmp/r/home"}; !slices.Equal(binds, want) {
		t.Errorf("writable binds = %v, want %v", binds, want)
	}

	for _, p := range ro {
		if isBroadHostPath(p) {
			t.Errorf("%s is bound read-only", p)
		}
	}

	for _, p := range []string{"/opt/helm/helm", "/tmp/r/in"} {
		if !slices.Contains(ro, p) {
			t.Errorf("%s is not bound read-only: %v", p, ro)
		}
	}
}

// bwrapBinds gives the writable binds as source->target and the sources of the read-only binds, in order.
func bwrapBinds(args []string) (writable, readOnly []string) {
	for i, a := range args {
		switch a {
		case "--bind":
			writable = append(writable, args[i+1]+"->"+args[i+2])
		case "--ro-bind", "--ro-bind-try":
			readOnly = append(readOnly, args[i+1])
		}
	}

	return writable, readOnly
}

// isBroadHostPath tells whether p is a host directory that a sandbox must never bind whole.
func isBroadHostPath(p string) bool {
	return slices.Contains([]string{"/", "/etc", "/home", "/root", "/tmp"}, p)
}

func TestToolArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  workspace.RenderSource
		want []string
	}{
		{
			name: "helm with values and namespace",
			src:  workspace.RenderSource{Tool: "helm", Path: "deploy/chart", ValuesFiles: []string{"deploy/values/a.yaml", "deploy/values/b.yaml"}, Release: "shop", Namespace: "shop-prod"},
			want: []string{"template", "--include-crds", "--namespace", "shop-prod", "--values", "./deploy/values/a.yaml", "--values", "./deploy/values/b.yaml", "shop", "./deploy/chart"},
		},
		{
			name: "helm release defaults to the chart directory",
			src:  workspace.RenderSource{Tool: "helm", Path: "deploy/./web"},
			want: []string{"template", "--include-crds", "web", "./deploy/web"},
		},
		{
			name: "kustomize",
			src:  workspace.RenderSource{Tool: "kustomize", Path: "deploy/overlays/prod/"},
			want: []string{"build", "./deploy/overlays/prod"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkToolArguments(t, &tc.src, tc.want)
		})
	}
}

func checkToolArguments(t *testing.T, src *workspace.RenderSource, want []string) {
	t.Helper()

	got := toolArguments(src)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("arguments = %v, want %v", got, want)
	}

	for _, f := range forbiddenFlags {
		if slices.Contains(got, f) {
			t.Errorf("%s is in the arguments", f)
		}
	}
}

func TestValidateSourceRefusesNamesThatLookLikeFlags(t *testing.T) {
	t.Parallel()

	for _, src := range []workspace.RenderSource{
		{Tool: "helm", Path: "chart", Release: "--post-renderer=evil"},
		{Tool: "helm", Path: "chart", Release: "ok", Namespace: "--kubeconfig=x"},
		{Tool: "helm", Path: "chart", Release: "UPPER"},
	} {
		if validateSource(&src) == "" {
			t.Errorf("%+v is accepted", src)
		}
	}

	if got := validateSource(&workspace.RenderSource{Tool: "helm", Path: "deploy/chart", Namespace: "shop"}); got != "" {
		t.Errorf("a plain release and namespace are refused: %s", got)
	}
}
