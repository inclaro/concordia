package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	defaultArchitecture = "architecture/architecture.json"
	policyFile          = "policies/default.policy.yaml"
	bindingsFile        = "bindings/concordia.bindings.yaml"
	waiversFile         = "exceptions/waivers.yaml"
	apiVersion          = "concordia.io/v1alpha1"
	yamlIndent          = 2
	scriptMode          = 0o755
	presetServices      = "services"
	presetMonolith      = "modular-monolith"
	presetCustom        = "custom"
	presetCore          = "core"
	coreName            = "core"
	ciGithub            = "github"
	ciGitlab            = "gitlab"
	ciGeneric           = "generic"
	cbmInstallComment   = "Install the pinned codebase-memory-mcp build (the default directory ~/.local/bin is not on the PATH of a runner)."
	goInstallCommand    = "go install github.com/inclaro/concordia/cmd/concordia@latest"
)

var (
	gitignoreLines = []string{"build/concordia/", ".concordia/"}

	architectureStart = "{\n  \"$schema\": \"" + intent.CalmSchemaID(workspace.LatestCalmSchemaVersion) +
		"\",\n  \"nodes\": [],\n  \"relationships\": []\n}\n"

	unsafeNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	nameEdges       = regexp.MustCompile(`^[-.]+|-+$`)
	lineBreak       = regexp.MustCompile(`\r?\n`)
)

type initOptions struct {
	calm   string
	preset string
	ci     string
	force  bool
}

func newInitCmd(host *Host) *cobra.Command {
	var opts initOptions

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter Concordia workspace",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, initHandler(ctx, app, opts)
		}),
	}

	cmd.Flags().StringVar(&opts.calm, "calm", "", "existing CALM architecture file")
	addChoiceFlag(cmd, false, &opts.preset, "preset", "starter policy", presetMonolith, presetServices, presetCustom, presetCore)
	addChoiceFlag(cmd, false, &opts.ci, "ci", "CI template", ciGithub, ciGitlab, ciGeneric)
	cmd.Flags().BoolVar(&opts.force, "force", false, "overwrite existing files")

	return cmd
}

// sanitizeProjectName makes a CBM project name from a directory name.
func sanitizeProjectName(name string) string {
	s := nameEdges.ReplaceAllString(unsafeNameChars.ReplaceAllString(name, "-"), "")
	if s == "" {
		return "project"
	}

	return s
}

func encodeYAML(value any) (string, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)

	if err := enc.Encode(value); err != nil {
		return "", fmt.Errorf("encode YAML: %w", err)
	}

	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encode YAML: %w", err)
	}

	return buf.String(), nil
}

type metadata struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
}

type subject struct {
	NodeTypes []string `yaml:"nodeTypes"`
}

type edgeSet struct {
	Edges []string `yaml:"edges"`
}

type requirement struct {
	GraphProperty string   `yaml:"graphProperty"`
	CollapseBy    string   `yaml:"collapseBy"`
	Edges         []string `yaml:"edges"`
}

type policyRule struct {
	DeclaredOnly *edgeSet     `yaml:"declaredOnly,omitempty"`
	Require      *requirement `yaml:"require,omitempty"`
	ID           string       `yaml:"id"`
	Name         string       `yaml:"name"`
	Severity     string       `yaml:"severity"`
	Subject      subject      `yaml:"subject"`
}

type policyPack struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   metadata     `yaml:"metadata"`
	Rules      []policyRule `yaml:"rules"`
}

func policyContent(preset string) (string, error) {
	rules := []policyRule{}

	switch preset {
	case presetServices:
		rules = append(rules, policyRule{
			ID:           "CO-DECL-001",
			Name:         "Services only depend on declared architecture relationships",
			Severity:     "error",
			Subject:      subject{NodeTypes: []string{"service", "database", "webclient"}},
			DeclaredOnly: &edgeSet{Edges: []string{edgeKindImports, "CALLS"}},
		})
	case presetMonolith:
		rules = append(rules, policyRule{
			ID:       "CO-CYC-001",
			Name:     "No dependency cycles between modules",
			Severity: "error",
			Subject:  subject{NodeTypes: []string{"service", "system"}},
			Require:  &requirement{GraphProperty: "acyclic", Edges: []string{edgeKindImports, "CALLS"}, CollapseBy: "architectureBinding"},
		})
	}

	return encodeYAML(policyPack{
		APIVersion: apiVersion,
		Kind:       "PolicyPack",
		Metadata:   metadata{ID: "default", Version: "1.0.0"},
		Rules:      rules,
	})
}

type initProject struct {
	Name    string   `yaml:"name"`
	Root    string   `yaml:"root"`
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

type initPackUse struct {
	Use string `yaml:"use"`
}

type initWorkspace struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name   string `yaml:"name"`
		Preset string `yaml:"preset"`
	} `yaml:"metadata"`
	Architecture struct {
		Source string `yaml:"source"`
	} `yaml:"architecture"`
	Observation struct {
		Provider  string        `yaml:"provider"`
		IndexMode string        `yaml:"indexMode"`
		Projects  []initProject `yaml:"projects"`
	} `yaml:"observation"`
	Bindings struct {
		Files []string `yaml:"files"`
	} `yaml:"bindings"`
	Policies struct {
		Files []string      `yaml:"files"`
		Packs []initPackUse `yaml:"packs,omitempty"`
	} `yaml:"policies"`
	Exceptions struct {
		Baseline string `yaml:"baseline"`
		Waivers  string `yaml:"waivers"`
	} `yaml:"exceptions"`
	Check struct {
		Unknown         string  `yaml:"unknown"`
		FailOn          string  `yaml:"failOn"`
		MinimumCoverage float64 `yaml:"minimumCoverage"`
	} `yaml:"check"`
}

func workspaceContent(plan *initPlan) (string, error) {
	source, projectName, preset := plan.source, plan.projectName, plan.preset

	var wsp initWorkspace

	wsp.APIVersion = apiVersion
	wsp.Kind = "Workspace"
	wsp.Metadata.Name = projectName
	wsp.Metadata.Preset = preset
	wsp.Architecture.Source = source
	wsp.Observation.Provider = "codebase-memory-mcp"
	wsp.Observation.IndexMode = string(workspace.IndexModeModerate)
	wsp.Observation.Projects = []initProject{{
		Name:    projectName,
		Root:    ".",
		Include: []string{"**"},
		Exclude: []string{"node_modules/**", "dist/**", "build/**"},
	}}
	wsp.Bindings.Files = []string{bindingsFile}
	wsp.Policies.Files = []string{"policies/**/*.policy.yaml"}

	if preset == presetCore {
		wsp.Policies.Packs = []initPackUse{{Use: packs.Prefix + coreName}}
	}

	wsp.Exceptions.Baseline = workspace.DefaultBaseline
	wsp.Exceptions.Waivers = waiversFile
	wsp.Check.MinimumCoverage = workspace.DefaultMinimumCoverage
	wsp.Check.Unknown = string(workspace.UnknownFail)
	wsp.Check.FailOn = string(diag.SeverityError)

	return encodeYAML(wsp)
}

// ciTemplate gives the path (with "/") and the content of the CI template.
func ciTemplate(ci string) (templatePath, content string) {
	switch ci {
	case ciGitlab:
		return ".gitlab/concordia.gitlab-ci.yml", `concordia:
  image: golang:1
  script:
    - ` + goInstallCommand + `
    # ` + cbmInstallComment + `
    - concordia cbm install --dir "$HOME/.local/bin"
    - export PATH="$HOME/.local/bin:$PATH"
    - concordia index
    - concordia check --format junit --output build/concordia/results.xml
  artifacts:
    when: always
    paths:
      - build/concordia/results.json
    reports:
      junit: build/concordia/results.xml
`
	case ciGeneric:
		return "ci/concordia.sh", `#!/usr/bin/env sh
set -eu
# ` + cbmInstallComment + `
concordia cbm install
export PATH="$HOME/.local/bin:$PATH"
concordia index
concordia check --format json --output build/concordia/results.json
`
	default:
		return ".github/workflows/concordia.yml", `name: concordia
on:
  pull_request:
  push:
    branches: [main]
jobs:
  concordia:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      security-events: write
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: "1.27"
      - run: ` + goInstallCommand + `
      # ` + cbmInstallComment + `
      - run: concordia cbm install --dir "$HOME/.local/bin" && echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - run: concordia index
      - run: concordia check --format sarif --output build/concordia/results.sarif
      - uses: github/codeql-action/upload-sarif@v3
        if: always()
        with:
          sarif_file: build/concordia/results.sarif
      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: concordia-results
          path: build/concordia/results.json
`
	}
}

// initWriter writes the files of `init`: it keeps existing files unless force is set.
type initWriter struct {
	app   *Context
	root  string
	force bool
}

func fileExists(p string) bool {
	_, err := os.Lstat(p)

	return err == nil
}

func (w *initWriter) rel(abs string) string {
	rel, err := fsutil.ToPosixRelative(w.root, abs)
	if err != nil {
		return fsutil.ToPosix(abs)
	}

	return rel
}

// writeWith writes a file with the given writer and reports it. An existing file
// is kept (with a CO-WRK-005 warning) unless force is set.
func (w *initWriter) writeWith(abs string, writer func(p string) error) error {
	if !w.force && fileExists(abs) {
		w.app.Logger.Diagnostics([]diag.Diagnostic{diag.New(diag.WRK005, diag.Params{keyPath: w.rel(abs)})})

		return nil
	}

	if err := writer(abs); err != nil {
		return err
	}

	w.app.Out("wrote " + w.rel(abs) + "\n")

	return nil
}

func (w *initWriter) write(rel, content string) error {
	return w.writeWith(filepath.Join(w.root, filepath.FromSlash(rel)), func(p string) error {
		if strings.HasPrefix(content, "#!") {
			return fsutil.WriteFileAtomicMode(p, []byte(content), scriptMode)
		}

		return fsutil.WriteFileAtomic(p, []byte(content))
	})
}

// updateGitignore adds the Concordia lines that .gitignore does not have.
func (w *initWriter) updateGitignore() error {
	giPath := filepath.Join(w.root, ".gitignore")

	current, err := os.ReadFile(giPath) //nolint:gosec // the path is the .gitignore of the workspace root.
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}

	present := map[string]bool{}
	for _, line := range lineBreak.Split(string(current), -1) {
		present[strings.TrimSpace(line)] = true
	}

	missing := slices.DeleteFunc(slices.Clone(gitignoreLines), func(l string) bool { return present[l] })
	if len(missing) == 0 {
		return nil
	}

	prefix := string(current)
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}

	if err := fsutil.WriteFileAtomic(giPath, []byte(prefix+strings.Join(missing, "\n")+"\n")); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}

	w.app.Out("updated .gitignore\n")

	return nil
}

// defaultCbmBuild gives the version and the file digest of the CBM executable. Deps.Cbm.Find replaces the detection.
func defaultCbmBuild(ctx context.Context, app *Context, executable string) CbmBuild {
	if app.Deps.Cbm != nil && app.Deps.Cbm.Find != nil {
		return app.Deps.Cbm.Find(ctx)
	}

	return detectCbmBuild(ctx, executable)
}

// initPlan is what `init` writes: the options, the architecture source, the project name and the preset.
type initPlan struct {
	source      string
	projectName string
	preset      string
	opts        initOptions
}

func initHandler(ctx context.Context, app *Context, opts initOptions) error {
	root := filepath.Dir(app.WorkspacePath)
	plan := &initPlan{
		opts:        opts,
		source:      orDefault(opts.calm, defaultArchitecture),
		projectName: sanitizeProjectName(filepath.Base(root)),
		preset:      orDefault(opts.preset, presetCustom),
	}
	out := &initWriter{app: app, root: root, force: opts.force}

	if err := out.writeStarterFiles(ctx, plan); err != nil {
		return err
	}

	if err := out.updateGitignore(); err != nil {
		return err
	}

	return validateInit(app, plan.source)
}

func (w *initWriter) writeStarterFiles(ctx context.Context, plan *initPlan) error {
	wsText, err := workspaceContent(plan)
	if err != nil {
		return err
	}

	policyText, err := policyContent(plan.preset)
	if err != nil {
		return err
	}

	if err := w.writeWith(w.app.WorkspacePath, func(p string) error { return fsutil.WriteFileAtomic(p, []byte(wsText)) }); err != nil {
		return err
	}

	if err := w.writeFixedFiles(plan.opts.calm == "", policyText); err != nil {
		return err
	}

	lock := newInitLock(ctx, w.app, w.root, plan)
	if err := w.writeWith(workspace.LockPathFor(w.root), func(p string) error { return workspace.WriteLock(p, lock) }); err != nil {
		return err
	}

	ciPath, ciText := ciTemplate(orDefault(plan.opts.ci, ciGithub))

	return w.write(ciPath, ciText)
}

// writeFixedFiles writes the starter architecture (unless the user brings one), the bindings, the policy and the waivers.
func (w *initWriter) writeFixedFiles(withArchitecture bool, policyText string) error {
	files := []struct{ rel, content string }{
		{bindingsFile, "apiVersion: " + apiVersion + "\nkind: BindingSet\nbindings: []\n"},
		{policyFile, policyText},
		{waiversFile, "waivers: []\n"},
	}

	if withArchitecture {
		files = slices.Insert(files, 0, struct{ rel, content string }{defaultArchitecture, architectureStart})
	}

	for _, f := range files {
		if err := w.write(f.rel, f.content); err != nil {
			return err
		}
	}

	return nil
}

// newInitLock is the lock that records the codebase-memory version, its profile, the CALM schema and the built-in pack.
func newInitLock(ctx context.Context, app *Context, root string, plan *initPlan) *workspace.Lockfile {
	build := defaultCbmBuild(ctx, app, defaultCbmExecutable)
	lock := workspace.NewLock(build.Version, "")
	lock.Resolved.CBM.SHA256 = build.SHA256
	lock.Resolved.CBM.NormalizationVersion = observation.NormalizationVersion

	if profile, ok := observation.ProfileFor(build.Version, observation.AdapterSchema); ok {
		lock.Resolved.CBM.Profile = &workspace.LockProfile{ID: profile.ID, Digest: profile.Digest}
	}

	lock.Resolved.Calm.SchemaVersion = initCalmSchemaVersion(root, plan.opts.calm)

	if plan.preset == presetCore {
		lock.Packs = append(lock.Packs, builtinLockPack(coreName))
	}

	return lock
}

// initCalmSchemaVersion is the CALM schema release of the lock: the latest for the starter architecture, else the
// release of the given document, or the default one when it cannot be read.
func initCalmSchemaVersion(root, calm string) string {
	if calm == "" {
		return workspace.LatestCalmSchemaVersion
	}

	p := filepath.FromSlash(calm)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}

	// On error validateInit reports the same CO-INT-001 or CO-INT-003 later.
	if version, err := intent.ReadSchemaVersion(p, calm); err == nil {
		return version
	}

	return workspace.DefaultCalmSchemaVersion
}

// validateInit checks what is on disk: the workspace, the architecture and the starter policy.
func validateInit(app *Context, source string) error {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return err
	}

	arch, err := loadInitArchitecture(wsp, source)
	if err != nil {
		return err
	}

	if err := policy.ThrowOnErrors(arch.Diagnostics); err != nil {
		return err
	}

	if arch.Model == nil {
		return nil
	}

	if err := compileInitPolicies(wsp, arch.Model); err != nil {
		return err
	}

	app.Out("initialized: workspace, architecture, and policies are valid\n")

	return nil
}

// loadInitArchitecture loads the architecture of the workspace, or the one that init wrote when the workspace names none.
func loadInitArchitecture(wsp *workspace.Workspace, source string) (intent.LoadResult, error) {
	urlMapping, err := intent.LoadURLMapping(wsp)
	if err != nil {
		return intent.LoadResult{}, fmt.Errorf("read the URL mapping: %w", err)
	}

	resolveAll := func(paths []string) []string {
		resolved := make([]string, len(paths))
		for i, p := range paths {
			resolved[i] = wsp.ResolveInRoot(p)
		}

		return resolved
	}

	return intent.LoadArchitecture(wsp.ResolveInRoot(orDefault(wsp.Architecture.Source, source)), intent.LoadContext{
		Root:       wsp.Root,
		URLMapping: urlMapping,
		Patterns:   resolveAll(wsp.Architecture.Patterns),
		Decorators: resolveAll(wsp.Architecture.Decorators),
		Trust:      trust.FromWorkspace(wsp),
	}), nil
}

// compileInitPolicies compiles the starter policy and the policy packs of the workspace against the architecture.
func compileInitPolicies(wsp *workspace.Workspace, model *intent.ArchitectureModel) error {
	files, err := policy.ReadPolicyFiles(wsp.Root, []string{policyFile})
	if err != nil {
		return err
	}

	packFiles, problems := policy.ResolvePacks(wsp.Root, wsp.Policies.Packs)
	if err := policy.ThrowOnErrors(problems); err != nil {
		return err
	}

	files = append(files, packFiles...)

	caps := policy.ProviderCaps{SupportedCanonicalKinds: observation.SupportedCanonicalKinds()}

	compiled, err := policy.CompilePolicies(files, model, caps, nil)
	if err != nil {
		return err
	}

	return policy.ThrowOnErrors(compiled.Diagnostics)
}

// builtinLockPack gives the lock entry of a built-in pack.
func builtinLockPack(name string) workspace.LockPack {
	text, _ := packs.Source(name)
	info, _ := policy.DescribePack(policy.PolicyFile{Path: packs.Prefix + name, Text: string(text)})

	return workspace.LockPack{ID: info.ID, Version: info.Version, Source: packs.Prefix + name, SHA256: digest.OfBytes(text)}
}

// orDefault gives value, or fallback when value is empty.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
