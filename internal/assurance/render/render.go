package render

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/procexec"
	"github.com/inclaro/concordia/internal/workspace"
)

// Options are the inputs of one render: one source of one environment.
type Options struct {
	// Pins are the renderer pins of the lockfile.
	Pins Pins
	// Run starts programs; the default is procexec.Run.
	Run Runner
	// Locate finds programs; the default is exec.LookPath.
	Locate Locator
	// Root is the workspace root (an OS path). Every declared path is read below it.
	Root string
	// Cluster is the cluster or context declared for the environment, empty when none is declared.
	Cluster string
	Source  workspace.RenderSource
	Limits  Limits
	// RequireOsSandbox is `assurance.rendering.requireOsSandbox`.
	RequireOsSandbox bool
}

// stderrTail is the number of trailing characters of renderer error output that a diagnostic keeps.
const stderrTail = 1000

var dnsName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

// Render renders one source for its environment.
//
// The error is only for what stops the run: a missing, unpinned, or different renderer (CO-PRV-003, exit 5), an OS
// sandbox that is required and not available (CO-PRV-004, exit 5), or an interrupted context (the context error,
// which the caller turns into exit 130). Every problem with the content of the source is in the Result: a coverage
// gap, a failed coverage, and a diagnostic. The Result is returned with the error when there is one, with failed
// coverage, so no caller can take an interrupted render as complete.
func Render(ctx context.Context, given *Options) (*Result, error) {
	// The defaults are set on a copy: the caller's options stay as they were.
	local := *given
	opts := &local

	res := newResult(opts.Source.Environment, opts.Cluster, opts.Source.Tool, opts.Source.Path)

	if opts.Run == nil {
		opts.Run = procexec.Run
	}

	if opts.Locate == nil {
		opts.Locate = defaultLocator
	}

	if opts.Limits == (Limits{}) {
		opts.Limits = LimitsFrom(workspace.RenderingLimits{})
	}

	bld := &builder{ctx: docContext{environment: res.Environment, cluster: res.Cluster, namespace: defaultNamespace, location: res.Path}}

	if err := ctx.Err(); err != nil {
		bld.fail(res, "interrupted", "the run was interrupted before the source was rendered")

		return res, err
	}

	switch opts.Source.Tool {
	case workspace.RenderToolPlain:
		return res, renderPlain(ctx, opts, res, bld)
	case workspace.RenderToolHelm, workspace.RenderToolKustomize:
		return res, renderTool(ctx, opts, res, bld)
	default:
		bld.fail(res, "unsupported-tool", fmt.Sprintf("the rendering tool %q is not supported", opts.Source.Tool))

		return res, nil
	}
}

// isManifest says that a file name is a literal manifest of a plain source.
func isManifest(rel string) bool {
	base := strings.ToLower(path.Base(rel))

	switch base {
	case "kustomization.yaml", "kustomization.yml", "chart.yaml":
		return false
	}

	ext := path.Ext(base)

	return ext == ".yaml" || ext == ".yml" || ext == ".json"
}

// renderPlain reads the literal manifests. No program starts.
func renderPlain(ctx context.Context, opts *Options, res *Result, bld *builder) error {
	work, err := newWorkdir()
	if err != nil {
		bld.fail(res, "workdir", err.Error())

		return nil
	}

	defer work.close()

	tree := newCopier(opts.Root, work)
	if gap := tree.copy(opts.Source.Path); gap != nil {
		bld.sourceFailed(res, []finding{{Gap: *gap, Blocking: true}})

		return nil
	}

	res.InputDigest = tree.inputDigest(isManifest, "tool="+workspace.RenderToolPlain)

	files := tree.manifestFiles()

	var total int64

	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			bld.fail(res, "interrupted", "the run was interrupted while the source was read")

			return err
		}

		data, err := os.ReadFile(filepath.Join(work.in, filepath.FromSlash(rel)))
		if err != nil {
			bld.gap(GapUnparseableDocument, "the file cannot be read: "+err.Error(), rel)

			continue
		}

		total += int64(len(data))
		if total > opts.Limits.MaxOutputBytes {
			bld.fail(res, "output-limit", fmt.Sprintf("the manifests are larger than the limit of %d bytes", opts.Limits.MaxOutputBytes))

			return nil
		}

		bld.add(input{file: rel, text: string(data), fallback: rel})
	}

	bld.finish(res, opts.Limits)

	return nil
}

// manifestFiles gives the copied manifest files, sorted.
func (c *copier) manifestFiles() []string {
	out := make([]string, 0, len(c.files))

	for p := range c.files {
		if isManifest(p) {
			out = append(out, p)
		}
	}

	sortStrings(out)

	return out
}

// renderTool runs helm or kustomize under the confinement layers of design D4.
func renderTool(ctx context.Context, opts *Options, res *Result, bld *builder) error {
	src := &opts.Source

	if problem := validateSource(src); problem != "" {
		bld.sourceFailed(res, []finding{{Gap: assurance.Gap{Kind: GapInvalidSource, Detail: problem, Path: src.Path}, Blocking: true}})

		return nil
	}

	work, err := newWorkdir()
	if err != nil {
		bld.fail(res, "workdir", err.Error())

		return nil
	}

	defer work.close()

	tree := newCopier(opts.Root, work)

	var findings []finding

	// Layers 4 and 5: the confined copy, then the pre-scan of the copy. Nothing runs before the pre-scan is done.
	switch src.Tool {
	case workspace.RenderToolHelm:
		findings = copyHelm(tree, src)
		if len(findings) == 0 {
			findings = scanHelm(work.in, cleanPath(src.Path), cleanPaths(src.ValuesFiles))
		}

		bld.ctx.namespace = defaultNamespace
		if src.Namespace != "" {
			bld.ctx.namespace = src.Namespace
		}

		res.InputDigest = tree.inputDigest(nil, "tool=helm", "release="+helmRelease(src), "namespace="+src.Namespace, "values="+strings.Join(cleanPaths(src.ValuesFiles), ","))
	default:
		findings = scanKustomize(tree, cleanPath(src.Path))
		res.InputDigest = tree.inputDigest(nil, "tool=kustomize")
	}

	if blocked := bld.recordFindings(res, findings); blocked {
		return nil
	}

	// The toolchain checks come after the pre-scan: a source that is refused starts no program at all.
	bwrap, haveSandbox := findSandbox(ctx, opts.Run, opts.Locate)
	if opts.RequireOsSandbox {
		if err := requireSandbox(haveSandbox); err != nil {
			bld.fail(res, "", "")

			return err
		}
	}

	exe, id, err := ResolveRenderer(ctx, opts.Run, opts.Locate, src.Tool, opts.Pins)
	if err != nil {
		bld.fail(res, "", "")

		return err
	}

	res.Renderer = &id
	res.Confinement = ConfinementProcess

	runName, runArgs := exe, toolArguments(src)

	if haveSandbox {
		res.Confinement = ConfinementProcessOS
		runName = bwrap
		runArgs = bwrapArgs(sandboxSpec{Bwrap: bwrap, Executable: exe, In: work.in, Home: work.home}, append([]string{exe}, runArgs...))
	}

	maxBuffer := int(min(opts.Limits.MaxOutputBytes, int64(^uint(0)>>1)))

	out, runErr := opts.Run(ctx, runName, runArgs, procexec.Options{
		Dir: work.in, Env: childEnv(work.home), Timeout: opts.Limits.Timeout, MaxBuffer: maxBuffer,
	})

	switch {
	case ctx.Err() != nil:
		bld.fail(res, "interrupted", "the run was interrupted while the source was rendering")

		return ctx.Err()
	case errors.Is(runErr, procexec.ErrOutputTooLarge):
		bld.fail(res, "output-limit", fmt.Sprintf("the rendered output is larger than the limit of %d bytes", opts.Limits.MaxOutputBytes))

		return nil
	case runErr != nil:
		var spawn *procexec.SpawnError
		if errors.As(runErr, &spawn) {
			bld.fail(res, "", "")

			return diag.Fail(diag.PRV003, diag.Detail(src.Tool+" cannot be started: "+work.scrub(spawn.Error())))
		}

		bld.fail(res, "render-error", work.scrub(runErr.Error()))

		return nil
	case out.TimedOut:
		bld.fail(res, "time-limit", fmt.Sprintf("the render did not finish within %s", opts.Limits.Timeout))

		return nil
	case out.Code != 0:
		bld.fail(res, "render-error", fmt.Sprintf("%s exited with code %d: %s", src.Tool, out.Code, lastChars(work.scrub(out.Stderr), stderrTail)))

		return nil
	}

	in := input{file: path.Join(cleanPath(src.Path), "rendered.yaml"), text: out.Stdout, fallback: cleanPath(src.Path)}
	if src.Tool == workspace.RenderToolHelm {
		in.location = helmLocation(cleanPath(src.Path))
	}

	bld.add(in)
	bld.finish(res, opts.Limits)

	return nil
}

func lastChars(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}

	return "..." + s[len(s)-n:]
}

// helmRelease is the release name: the declared one, else the name of the chart directory.
func helmRelease(src *workspace.RenderSource) string {
	if src.Release != "" {
		return src.Release
	}

	return path.Base(cleanPath(src.Path))
}

// validateSource checks the values that become arguments of the renderer. They are not flags, but a name that
// starts with `-` must never be taken for one, so only plain names pass.
func validateSource(src *workspace.RenderSource) string {
	if src.Tool != workspace.RenderToolHelm {
		return ""
	}

	if rel := helmRelease(src); !dnsName.MatchString(rel) {
		return fmt.Sprintf("the Helm release name %q is not a valid name", rel)
	}

	if src.Namespace != "" && !dnsName.MatchString(src.Namespace) {
		return fmt.Sprintf("the namespace %q is not a valid name", src.Namespace)
	}

	return ""
}

func cleanPath(p string) string {
	if cleaned, gap := clean(p); gap == nil {
		return cleaned
	}

	return p
}

func cleanPaths(list []string) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, cleanPath(p))
	}

	return out
}

// copyHelm copies the chart and the values files.
func copyHelm(tree *copier, src *workspace.RenderSource) []finding {
	for _, p := range append([]string{src.Path}, src.ValuesFiles...) {
		if gap := tree.copy(p); gap != nil {
			return []finding{{Gap: *gap, Blocking: true}}
		}
	}

	return nil
}
