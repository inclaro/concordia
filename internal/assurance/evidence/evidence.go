// Package evidence loads every input that the `assurance` key of the workspace declares, once: the rendered sources
// of each environment, the plans and their binding statements, and the statements of the provenance, deployment
// record, workload observation, check result, and measurement globs (verified and run through the admissibility
// engine), and the quality scenario files. The check pipeline and `concordia assurance validate` both call Load, so
// the two cannot disagree on what the inputs say.
//
// Load only loads. It evaluates no claim: lineage, drift, effective policy, and quality verdicts belong to the
// evaluators, which read what Load returns. The one exception is the verdict of a check, which is a pure grouping of
// admitted results and is part of the loaded evidence (design D6, spec verification-results).
//
// Failure semantics follow design D13. A problem with the content of an evidence source (a failed render, a
// malformed or unverifiable statement, an unreadable plan, a glob that matches nothing) is a diagnostic on the result,
// and the other inputs go on. A configuration error (invalid scenario file, a declared path outside the root) is
// returned as a *ConfigError (exit 2), and a toolchain error (renderer missing or different from its pin, OS sandbox
// required and unavailable, cosign missing when a verification needs it) is returned as a *diag.Error (exit 5). A
// canceled context returns the context error, which the CLI turns into exit 130. No process starts before the
// configuration errors are known.
//
// The package never imports internal/conformance or internal/cli.
package evidence

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/checkresult/verdict"
	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/cosign"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/procexec"
	"github.com/inclaro/concordia/internal/workspace"
)

// Evidence kinds. They are the producer claim kinds, so a statement kind is also the claim kind that a producer needs
// to attest it.
const (
	KindProvenance  = workspace.ProducerClaimBuildProvenance
	KindDeployment  = workspace.ProducerClaimDeploymentRecord
	KindObservation = workspace.ProducerClaimWorkloadObservation
	KindCheckResult = workspace.ProducerClaimCheckResult
	KindMeasurement = workspace.ProducerClaimMeasurement
	KindPlan        = workspace.ProducerClaimPlan
)

// cosignProbeLimit bounds the `cosign version` call.
const cosignProbeLimit = 30 * time.Second

// unknownVersion is the cosign version when the probe does not answer.
const unknownVersion = "unknown"

// Gap kinds of a plan that cannot be used.
const (
	// GapPlanMissing: the declared plan file does not exist or cannot be read.
	GapPlanMissing = "plan-file-missing"
	// GapPlanUnusable: the plan file is not a plan that the reader supports.
	GapPlanUnusable = "plan-unusable"
)

// Options are the inputs of Load.
type Options struct {
	// Assurance is the `assurance` configuration. Nil: Load returns an empty Evidence.
	Assurance *workspace.Assurance
	// Environments maps each declared environment id to its cluster or context, "" when none is declared. A render
	// takes the cluster of its environment from here.
	Environments map[string]string
	// Pins are the renderer pins of the lockfile (`resolved.renderers`), nil when the lock has none. See PinsFromLock.
	Pins render.Pins
	// Run starts programs for the renderers and the cosign version probe; the default is procexec.Run. cosign
	// verification itself starts through internal/cosign.
	Run render.Runner
	// Locate finds the renderers and bubblewrap; the default is the lookup of `lock update` and `doctor`.
	Locate render.Locator
	// Getenv reads COSIGN, the override of the cosign executable; the default is os.Getenv.
	Getenv func(string) string
	// Root is the absolute workspace root (an OS path). Every declared path is read below it.
	Root string
	// Known are the CALM element ids and the rule ids that scenario files may name.
	Known quality.Known
	// Context is the evaluation context of the run: time, revision, offline, dirty tree. The environment of the
	// context is not used: Load admits every statement against the environment it declares, and a consumer that has
	// an obligation for another environment asks for it (CheckVerdict, or a new admission).
	Context assurance.Context
	// RenderTwice renders every source a second time and adds a CO-PRV-014 warning when the two renders differ. Only
	// `assurance validate` sets it: a run never renders twice (design D18).
	RenderTwice bool
	// DeferScenarios leaves the scenario files unread. The check pipeline needs the check ids of the evidence before
	// it compiles the policies, and the scenario files need the compiled rule ids (Known.Rules), so it loads
	// everything else first and calls Evidence.LoadScenarios after the compile. Without the option Load reads the
	// scenarios itself, as `assurance validate` does when it has no policies.
	DeferScenarios bool
}

// PinsFromLock gives the renderer pins of a lockfile for Options.Pins. A nil lock, or a lock without pins, gives nil.
func PinsFromLock(lock *workspace.Lockfile) render.Pins {
	var pins render.Pins

	for _, tool := range []string{workspace.RenderToolHelm, workspace.RenderToolKustomize} {
		if pin := lock.Renderer(tool); pin != nil {
			if pins == nil {
				pins = render.Pins{}
			}

			pins[tool] = render.Pin{Version: pin.Version, SHA256: pin.SHA256}
		}
	}

	return pins
}

// ConfigError is every configuration error of the declared inputs, in a fixed order. The run stops with exit 2.
// errors.As finds the first *diag.Error in it.
type ConfigError struct {
	Errors []*diag.Error
}

// Error names the first error and the number of the others.
func (e *ConfigError) Error() string {
	switch len(e.Errors) {
	case 0:
		return "assurance configuration error"
	case 1:
		return e.Errors[0].Error()
	}

	return fmt.Sprintf("%s (and %d more)", e.Errors[0].Error(), len(e.Errors)-1)
}

// Unwrap gives the errors for errors.Is and errors.As.
func (e *ConfigError) Unwrap() []error {
	out := make([]error, len(e.Errors))
	for i, err := range e.Errors {
		out[i] = err
	}

	return out
}

// ExitCode is the exit code of a configuration error.
func (e *ConfigError) ExitCode() int { return diag.InvalidInput }

// Statement is one statement file that was read and verified: the parsed file, how far it is verified, the decision of
// the admissibility engine, and the redacted form that is persisted.
type Statement struct {
	Verification statement.Verification
	// Kind is the producer claim kind: `build-provenance`, `deployment-record`, `workload-observation`, `check-result`,
	// `measurement`, or `plan`.
	Kind   string
	Loaded statement.Loaded
	// Persisted is the redacted form with its redaction records.
	Persisted statement.Persisted
	Admission assurance.Admission
}

// StatementSummary is the line of a statement in the results (`assurance.statements[]`).
type StatementSummary struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
	// Digest is the digest of the statement file, `sha256:<hex>`.
	Digest string `json:"digest"`
	// SubjectDigests are the subject digests of the statement as `<algorithm>:<value>`, sorted.
	SubjectDigests []string `json:"subjectDigests"`
	// Producer is the id of the declared producer whose signature verified; empty when none did.
	Producer  string              `json:"producer,omitempty"`
	Integrity assurance.Integrity `json:"integrity"`
	Signature statement.Signature `json:"signature"`
	// Details are every reason the statement is not admissible, sorted. Empty when it is admissible.
	Details    []assurance.Detail `json:"details"`
	Admissible bool               `json:"admissible"`
}

// Plan is one declared plan: the plan that was read and its binding to the revision.
type Plan struct {
	// Plan is the plan that was read. Nil when the file is missing or not a supported plan: no partial plan is ever
	// returned.
	Plan *plan.Plan
	// Statement is the binding statement that was read, nil when the plan declares none or it could not be read.
	Statement *Statement
	Coverage  assurance.Coverage
	// Declared is the entry of `assurance.plans`.
	Declared workspace.AssurancePlan
	// Gaps are the gaps that Load adds when the plan cannot be used; the gaps of a plan that was read are in Plan.
	Gaps []assurance.Gap
	// Binding is the admissibility of the plan for its revision and environment. The plan may still be shown whatever
	// the binding says.
	Binding plan.Binding
}

// CheckOutcome is the verdict of one check in one environment (`environment` is "" for a result that names none).
type CheckOutcome struct {
	Environment string
	verdict.Outcome
}

// Glob is one declared evidence glob and the files it matched. Paths is empty for a glob that matched nothing.
type Glob struct {
	// Kind is the evidence kind: one of the Kind constants (build-provenance, deployment-record, workload-observation,
	// check-result, measurement).
	Kind    string
	Pattern string
	Paths   []string
}

// Toolchain is what the run used besides Concordia: the identity of every renderer that ran and the cosign version.
type Toolchain struct {
	// Cosign is the version of the cosign that verified at least one statement, "" when cosign never started.
	Cosign string
	// Renderers are the identities of the renderers that rendered a source, sorted by name.
	Renderers []render.Identity
}

// Evidence is everything the declared inputs say, loaded once. Every list is sorted with canonjson.CompareUTF16, so
// two loads of the same inputs are equal.
type Evidence struct {
	// Scenarios are the loaded quality scenarios and findings; nil when the workspace has no `assurance.quality`.
	Scenarios *quality.Set
	// Renders hold one result per declared source, sorted by environment and path.
	Renders []*render.Result
	// Plans are the declared plans, sorted by environment and file.
	Plans []Plan
	// Provenance, Deployments, and Observations are the SLSA provenance statements, deployment records, and workload
	// observations that were read and verified, sorted by path. The lineage evaluators read them.
	Provenance   []Statement
	Deployments  []Statement
	Observations []Statement
	// CheckResults are the check results with their admissibility, sorted by path.
	CheckResults []verdict.Admitted
	// CheckOutcomes are the verdicts of the checks, one per check and environment, sorted by check and environment.
	CheckOutcomes []CheckOutcome
	// CheckIDs are the check ids that the result files name (admissible or not), sorted and without duplicates.
	CheckIDs []string
	// Measurements are the measurement statements that name a scenario, sorted by path.
	Measurements []quality.Measurement
	// Statements lists every statement that was read, for the results.
	Statements []StatementSummary
	// Persisted are the redacted forms of the statements for the bundle, sorted by digest. They carry the redaction
	// records.
	Persisted []statement.Persisted
	// Globs list the declared evidence globs with the files they matched, sorted by kind and pattern.
	Globs []Glob
	// Toolchain records the renderers and the cosign that were used.
	Toolchain Toolchain
	// ClaimsDigest is the digest of the claim registry of this build.
	ClaimsDigest string
	// Diagnostics are the warnings and infos of the whole load, sorted. They include the diagnostics that single
	// results (a render, a plan) also carry.
	Diagnostics []diag.Diagnostic
}

// Empty says whether the workspace declares globs of the kind and none of them matched a file. Obligations that need
// such evidence are `unknown` with `no-evidence-supplied`.
func (e *Evidence) Empty(kind string) bool {
	declared := false

	for i := range e.Globs {
		if e.Globs[i].Kind != kind {
			continue
		}

		declared = true

		if len(e.Globs[i].Paths) > 0 {
			return false
		}
	}

	return declared
}

// CheckVerdict gives the verdict of a check for an environment, from the admitted results that name that
// environment. An empty environment takes the results of every environment. A check that has no result gives
// `unknown` with `no-evidence-supplied`. It adds no diagnostic: the CO-PRV-012 warnings of conflicts are in
// Evidence.Diagnostics already.
func (e *Evidence) CheckVerdict(check, environment string) verdict.Outcome {
	var results []verdict.Admitted

	for i := range e.CheckResults {
		if environment == "" || e.CheckResults[i].Result.Environment == environment {
			results = append(results, e.CheckResults[i])
		}
	}

	outcome := verdict.Evaluate(check, results)
	outcome.Diagnostics = nil

	return outcome
}

// LoadScenarios reads the quality scenario files that Load left unread (Options.DeferScenarios). known are the CALM
// element ids and the compiled rule ids that the files may name. A configuration problem is a *ConfigError (exit 2)
// with every scenario error. A workspace without `assurance.quality` has no scenarios and nothing to read.
func (e *Evidence) LoadScenarios(cfg *workspace.Assurance, root string, known quality.Known) error {
	if cfg == nil || cfg.Quality == nil {
		return nil
	}

	set, errs := quality.Load(root, cfg.Quality.Scenarios, known)
	if len(errs) > 0 {
		return &ConfigError{Errors: errs}
	}

	e.Scenarios = set

	return nil
}

// Load loads every input of opts.Assurance once. See the package documentation for the failure semantics.
func Load(ctx context.Context, opts *Options) (*Evidence, error) {
	result := &Evidence{
		Renders: []*render.Result{}, Plans: []Plan{}, Provenance: []Statement{}, Deployments: []Statement{},
		Observations: []Statement{}, CheckResults: []verdict.Admitted{}, CheckOutcomes: []CheckOutcome{},
		CheckIDs: []string{}, Measurements: []quality.Measurement{}, Statements: []StatementSummary{},
		Persisted: []statement.Persisted{}, Globs: []Glob{}, Diagnostics: []diag.Diagnostic{},
		Toolchain:    Toolchain{Renderers: []render.Identity{}},
		ClaimsDigest: claims.Default().Digest(),
	}

	cfg := opts.Assurance
	if cfg == nil {
		return result, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, err //nolint:wrapcheck // the CLI maps the context error to exit 130.
	}

	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	run := opts.Run
	if run == nil {
		run = procexec.Run
	}

	ldr := &loader{ev: result, opts: opts, cfg: cfg, getenv: getenv, run: run, verifier: &statement.Verifier{Getenv: getenv}}
	if err := ldr.configure(); err != nil {
		return nil, err
	}

	steps := []func(context.Context) error{ldr.loadRenders, ldr.loadPlans, ldr.loadStatements}

	for _, step := range steps {
		if err := step(ctx); err != nil {
			return nil, err
		}
	}

	ldr.finish(ctx)

	return result, nil
}

// cosignVersion reads `GitVersion: v3.0.2` from the output of `cosign version`.
var cosignVersion = regexp.MustCompile(`GitVersion:\s+v?(\S+)`)

// finish sorts everything and records the toolchain.
func (l *loader) finish(ctx context.Context) {
	out := l.ev

	slices.SortStableFunc(out.Statements, func(a, b StatementSummary) int {
		return compareKeys([]string{a.Kind, a.Path, a.Digest}, []string{b.Kind, b.Path, b.Digest})
	})
	slices.SortStableFunc(out.Persisted, func(a, b statement.Persisted) int { return canonjson.CompareUTF16(a.Digest, b.Digest) })
	out.Persisted = slices.CompactFunc(out.Persisted, func(a, b statement.Persisted) bool { return a.Digest == b.Digest })

	slices.SortStableFunc(out.Globs, func(a, b Glob) int {
		return compareKeys([]string{a.Kind, a.Pattern}, []string{b.Kind, b.Pattern})
	})

	slices.SortFunc(out.Toolchain.Renderers, func(a, b render.Identity) int {
		return compareKeys([]string{a.Name, a.Version, a.SHA256}, []string{b.Name, b.Version, b.SHA256})
	})
	out.Toolchain.Renderers = slices.Compact(out.Toolchain.Renderers)

	if l.cosignUsed {
		out.Toolchain.Cosign = l.probeCosign(ctx)
	}

	out.Diagnostics = diag.Sort(l.diags)
	if out.Diagnostics == nil {
		out.Diagnostics = []diag.Diagnostic{}
	}
}

// probeCosign asks the cosign that verified for its version. A cosign that does not answer gives "unknown": the
// verification already happened, so the probe may not turn it into a failure.
func (l *loader) probeCosign(ctx context.Context) string {
	exe, err := cosign.Path(l.getenv)
	if err != nil {
		return unknownVersion
	}

	res, err := l.run(ctx, exe, []string{"version"}, procexec.Options{Timeout: cosignProbeLimit})
	if err != nil || res.Code != 0 {
		return unknownVersion
	}

	if m := cosignVersion.FindStringSubmatch(res.Stdout + "\n" + res.Stderr); m != nil {
		return m[1]
	}

	return unknownVersion
}

// compareKeys orders two key lists element by element in UTF-16 order.
func compareKeys(a, b []string) int {
	for i := range a {
		if c := canonjson.CompareUTF16(a[i], b[i]); c != 0 {
			return c
		}
	}

	return 0
}

// trimDot removes the leading "./" of a declared path or glob.
func trimDot(p string) string { return strings.TrimPrefix(p, "./") }
