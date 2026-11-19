package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
)

// assuranceValidateVersion is the version of the JSON output of `assurance validate`.
const assuranceValidateVersion = "1.0"

// Input kinds of the output. The statement kinds are the producer claim kinds of the evidence package.
const (
	inputRendering        = "rendering"
	inputPlan             = "plan"
	inputPlanStatement    = "plan-statement"
	inputQualityScenarios = "quality-scenarios"
)

// inputOrder is the order of the kinds in the output.
var inputOrder = []string{
	inputRendering, inputPlan, inputPlanStatement, evidence.KindProvenance, evidence.KindDeployment, evidence.KindObservation,
	evidence.KindCheckResult, evidence.KindMeasurement, inputQualityScenarios,
}

type assuranceValidateOptions struct {
	json bool
}

func newAssuranceCmd(host *Host) *cobra.Command {
	group := &cobra.Command{Use: "assurance", Short: "Assurance evidence commands"}

	var opts assuranceValidateOptions

	validate := &cobra.Command{
		Use:   cmdValidate,
		Short: "Load every declared assurance input and show how far it is admissible, without evaluating policy rules",
		Long: "Loads the rendering sources, plans, statements, check results, measurements, and quality scenarios that the " +
			"`assurance` key declares, as `check` does, and prints for each input its kind, integrity level, admissibility, " +
			"and reasons, and the rendering coverage of each environment. It renders every source twice and warns when the " +
			"output differs. It evaluates no rule and writes no file.",
		Args: cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return assuranceValidate(ctx, app, &opts)
		}),
	}

	validate.Flags().BoolVar(&opts.json, "json", false, "machine-readable output")

	group.AddCommand(validate)

	return group
}

// validateInput is one declared input as `assurance validate` shows it.
type validateInput struct {
	Kind        string              `json:"kind"`
	Path        string              `json:"path"`
	Environment string              `json:"environment,omitempty"`
	Tool        string              `json:"tool,omitempty"`
	Integrity   assurance.Integrity `json:"integrity"`
	Coverage    assurance.Coverage  `json:"coverage,omitempty"`
	Signature   string              `json:"signature,omitempty"`
	Producer    string              `json:"producer,omitempty"`
	Digest      string              `json:"digest,omitempty"`
	// Reasons are the admissibility details: why the input cannot support a claim. A render that failed gives its gap
	// kinds, because the gaps say why.
	Reasons []string `json:"reasons"`
	// Gaps are the kinds of the coverage gaps of a render or a plan, sorted.
	Gaps       []string `json:"gaps"`
	Admissible bool     `json:"admissible"`
}

// validateEnvironment is the rendering of one environment: the worst coverage of its sources and what they hold.
type validateEnvironment struct {
	Environment string             `json:"environment"`
	Coverage    assurance.Coverage `json:"coverage"`
	Gaps        []validateGap      `json:"gaps"`
	Sources     int                `json:"sources"`
	Resources   int                `json:"resources"`
	Occurrences int                `json:"containerOccurrences"`
}

type validateGap struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
}

// validateDocument is the JSON output.
type validateDocument struct {
	SchemaVersion string                `json:"schemaVersion"`
	Inputs        []validateInput       `json:"inputs"`
	Environments  []validateEnvironment `json:"environments"`
	Renderers     []render.Identity     `json:"renderers"`
	// Candidates are the test associations of the last check run: candidates for a check, never evidence.
	Candidates []conformance.TestCandidate `json:"candidates"`
	// CandidatesFrom is the workspace-relative results file the candidates were read from; empty when there are none.
	CandidatesFrom string            `json:"candidatesFrom,omitempty"`
	Diagnostics    []diag.Diagnostic `json:"diagnostics"`
}

// assuranceValidate loads the declared assurance inputs and prints them. It evaluates nothing and writes no file.
// Configuration errors are exit 2, a missing renderer or sandbox is exit 5, and an interrupt is exit 130 (Run maps the
// context error).
func assuranceValidate(ctx context.Context, app *Context, opts *assuranceValidateOptions) (int, error) {
	loaded, err := conformance.LoadAssurance(ctx, &conformance.CheckOptions{Env: checkEnv(app), Deps: checkDeps(app), SkipReports: true}, true)
	if err != nil {
		var config *evidence.ConfigError
		if errors.As(err, &config) {
			for _, failure := range config.Errors {
				app.Err(diag.FormatDiagnostic(failure.Diagnostic) + "\n")
			}

			return diag.InvalidInput, nil
		}

		return diag.Internal, err
	}

	doc := newValidateDocument(loaded.Evidence)
	doc.Candidates = lastCandidates(loaded.Workspace)

	if len(doc.Candidates) > 0 {
		doc.CandidatesFrom = resultsRel(loaded.Workspace)
	}

	text := prettyValidate(&doc)

	if opts.json || app.Opts.Format == modelFormatJSON {
		data, err := canonjson.Marshal(&doc)
		if err != nil {
			return diag.Internal, fmt.Errorf("encode assurance validate: %w", err)
		}

		text = string(data)
	}

	for i := range doc.Diagnostics {
		app.Err(diag.FormatDiagnostic(doc.Diagnostics[i]) + "\n")
	}

	if err := app.WriteOutput(text, ""); err != nil {
		return diag.Internal, err
	}

	if slices.ContainsFunc(doc.Diagnostics, func(d diag.Diagnostic) bool { return d.Severity == diag.SeverityError }) {
		return diag.InvalidInput, nil
	}

	return diag.OK, nil
}

func newValidateDocument(loaded *evidence.Evidence) validateDocument {
	inputs := renderInputs(loaded)
	inputs = append(inputs, planInputs(loaded)...)
	inputs = append(inputs, statementInputs(loaded)...)
	inputs = append(inputs, scenarioInputs(loaded)...)

	slices.SortStableFunc(inputs, func(a, b validateInput) int {
		if c := slices.Index(inputOrder, a.Kind) - slices.Index(inputOrder, b.Kind); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Environment+"\x00"+a.Path, b.Environment+"\x00"+b.Path)
	})

	renderers := slices.CompactFunc(slices.Clone(loaded.Toolchain.Renderers), func(a, b render.Identity) bool { return a == b })
	if renderers == nil {
		renderers = []render.Identity{}
	}

	return validateDocument{
		SchemaVersion: assuranceValidateVersion, Inputs: nonNilSlice(inputs), Environments: environmentRenders(loaded),
		Renderers: renderers, Diagnostics: nonNilDiagnostics(loaded.Diagnostics),
	}
}

func nonNilSlice[T any](in []T) []T {
	if in == nil {
		return []T{}
	}

	return in
}

// renderInputs shows each rendered source. A render is checked by digest (its input and output digests are recorded,
// so a second render can be compared) and usable unless it failed.
func renderInputs(ev *evidence.Evidence) []validateInput {
	out := make([]validateInput, 0, len(ev.Renders))

	for _, res := range ev.Renders {
		integrity := assurance.IntegrityDigest
		if res.Coverage == assurance.CoverageFailed {
			integrity = assurance.IntegrityNotVerified
		}

		gaps := dedupe(gapKinds(res.Gaps))
		admissible := res.Coverage != assurance.CoverageFailed

		reasons := []string{}
		if !admissible {
			reasons = gaps
		}

		out = append(out, validateInput{
			Kind: inputRendering, Path: res.Path, Environment: res.Environment, Tool: res.Tool, Integrity: integrity,
			Admissible: admissible, Reasons: reasons, Gaps: gaps, Coverage: res.Coverage, Digest: res.OutputDigest,
		})
	}

	return out
}

func gapKinds(gaps []assurance.Gap) []string {
	out := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		out = append(out, gap.Kind)
	}

	return out
}

func planInputs(ev *evidence.Evidence) []validateInput {
	out := make([]validateInput, 0, len(ev.Plans))

	for i := range ev.Plans {
		declared := &ev.Plans[i]

		gaps := slices.Clone(declared.Gaps)
		if declared.Plan != nil {
			gaps = append(gaps, declared.Plan.Gaps...)
		}

		input := validateInput{
			Kind: inputPlan, Path: declared.Declared.File, Environment: declared.Declared.Environment, Tool: declared.Declared.Tool,
			Integrity: declared.Binding.Admission.Integrity, Admissible: declared.Binding.Admissible(),
			Reasons: detailTexts(declared.Binding.Admission.Details), Gaps: dedupe(gapKinds(gaps)), Coverage: declared.Coverage,
		}

		if declared.Plan != nil {
			input.Digest = declared.Plan.Digest
		}

		out = append(out, input)
	}

	return out
}

// statementInputs shows every statement that was read: the integrity it reached, whether it is admissible, and every
// reason it is not.
func statementInputs(ev *evidence.Evidence) []validateInput {
	out := make([]validateInput, 0, len(ev.Statements))

	for i := range ev.Statements {
		st := &ev.Statements[i]

		kind := st.Kind
		if kind == evidence.KindPlan {
			kind = inputPlanStatement
		}

		out = append(out, validateInput{
			Kind: kind, Path: st.Path, Integrity: st.Integrity, Admissible: st.Admissible, Reasons: detailTexts(st.Details), Gaps: []string{},
			Signature: string(st.Signature), Producer: st.Producer, Digest: st.Digest,
		})
	}

	return out
}

// scenarioInputs shows the quality scenario files. They are committed files that load or are a configuration error, so
// a loaded file is admissible; its digest is its integrity.
func scenarioInputs(ev *evidence.Evidence) []validateInput {
	if ev.Scenarios == nil {
		return nil
	}

	out := make([]validateInput, 0, len(ev.Scenarios.Files))

	for _, file := range ev.Scenarios.Files {
		out = append(out, validateInput{
			Kind: inputQualityScenarios, Path: file.Path, Integrity: assurance.IntegrityDigest, Admissible: true,
			Reasons: []string{}, Gaps: []string{}, Digest: file.Digest,
		})
	}

	return out
}

func detailTexts(details []assurance.Detail) []string {
	out := make([]string, 0, len(details))
	for _, d := range details {
		out = append(out, string(d))
	}

	return out
}

// dedupe sorts the reasons and removes repeats. The result is never nil.
func dedupe(reasons []string) []string {
	out := slices.Clone(reasons)
	slices.SortFunc(out, canonjson.CompareUTF16)

	return nonNilSlice(slices.Compact(out))
}

// worse orders coverage states: failed is the worst.
func coverageRank(c assurance.Coverage) int {
	switch c {
	case assurance.CoverageFailed:
		return 2
	case assurance.CoveragePartial:
		return 1
	case assurance.CoverageComplete:
		return 0
	}

	return 0
}

// environmentRenders summarizes the sources of each environment: the worst coverage, the sizes, and every gap.
func environmentRenders(ev *evidence.Evidence) []validateEnvironment {
	byEnv := map[string]*validateEnvironment{}

	var order []string

	for _, res := range ev.Renders {
		env := byEnv[res.Environment]
		if env == nil {
			env = &validateEnvironment{Environment: res.Environment, Coverage: assurance.CoverageComplete, Gaps: []validateGap{}}
			byEnv[res.Environment] = env

			order = append(order, res.Environment)
		}

		if coverageRank(res.Coverage) > coverageRank(env.Coverage) {
			env.Coverage = res.Coverage
		}

		env.Sources++
		env.Resources += res.ResourceCount()
		env.Occurrences += len(res.Occurrences)

		for _, gap := range res.Gaps {
			env.Gaps = append(env.Gaps, validateGap{Kind: gap.Kind, Detail: gap.Detail, Source: res.Path, Path: gap.Path})
		}
	}

	slices.SortFunc(order, canonjson.CompareUTF16)

	out := make([]validateEnvironment, 0, len(order))
	for _, id := range order {
		out = append(out, *byEnv[id])
	}

	return out
}

// prettyValidate gives the terminal output: one block per input, then the rendering of each environment.
func prettyValidate(doc *validateDocument) string {
	var out strings.Builder

	if len(doc.Inputs) == 0 {
		out.WriteString("no assurance inputs declared\n")

		return out.String()
	}

	admissible := 0

	for i := range doc.Inputs {
		if doc.Inputs[i].Admissible {
			admissible++
		}

		writeInput(&out, &doc.Inputs[i])
	}

	writeEnvironments(&out, doc.Environments)

	if len(doc.Renderers) > 0 {
		out.WriteString("\nrenderers\n")

		for _, id := range doc.Renderers {
			out.WriteString("  " + id.Name + " " + id.Version + "\n")
		}
	}

	// The command loads no code graph: the candidates are the ones the last `check` recorded, which may be older than
	// the working tree, so the block says where they come from.
	if lines := testCandidateLines(doc.Candidates); len(lines) > 0 {
		out.WriteString("\nfrom the last check run (" + doc.CandidatesFrom + "):\n" + strings.Join(lines, "\n") + "\n")
	}

	out.WriteString("\n" + plural(len(doc.Inputs), "input") + ", " + strconv.Itoa(admissible) + " admissible\n")

	return out.String()
}

// writeInput writes the block of one input: its heading, then one indented line for each fact that it has.
func writeInput(out *strings.Builder, input *validateInput) {
	head := input.Kind + " " + input.Path
	if input.Environment != "" {
		head = input.Kind + " " + input.Environment + " " + input.Path
	}

	out.WriteString(head + "\n")
	out.WriteString("  integrity: " + input.Integrity.Text() + "\n")
	out.WriteString("  admissible: " + yesNo(input.Admissible) + "\n")

	if len(input.Reasons) > 0 {
		out.WriteString("  reasons: " + strings.Join(input.Reasons, ", ") + "\n")
	}

	if len(input.Gaps) > 0 {
		out.WriteString("  gaps: " + strings.Join(input.Gaps, ", ") + "\n")
	}

	if input.Coverage != "" {
		out.WriteString("  coverage: " + string(input.Coverage) + "\n")
	}

	if input.Signature != "" {
		signature := input.Signature
		if input.Producer != "" {
			signature += " (producer " + input.Producer + ")"
		}

		out.WriteString("  signature: " + signature + "\n")
	}
}

// writeEnvironments writes the rendering of each environment with its coverage and gaps.
func writeEnvironments(out *strings.Builder, environments []validateEnvironment) {
	if len(environments) == 0 {
		return
	}

	out.WriteString("\nrendering by environment\n")

	for i := range environments {
		env := &environments[i]
		out.WriteString("  " + env.Environment + ": " + string(env.Coverage) + " (" + plural(env.Sources, "source") + ", " +
			plural(env.Resources, "resource") + ", " + plural(env.Occurrences, "container occurrence") + ")\n")

		for _, gap := range env.Gaps {
			line := "    gap " + gap.Kind + ": " + gap.Detail
			if gap.Path != "" {
				line += " [" + gap.Path + "]"
			}

			out.WriteString(line + "\n")
		}
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}

	return "no"
}
