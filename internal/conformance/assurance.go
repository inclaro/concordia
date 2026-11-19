package conformance

import (
	"cmp"
	"slices"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// assuranceRun is the assurance state of one check run: the evidence loaded once by internal/assurance/evidence and
// the report that the run carries. It is nil when the workspace has no `assurance` key.
type assuranceRun struct {
	// cfg is the `assurance` configuration that the evidence was loaded from.
	cfg *workspace.Assurance
	// ctx is the evaluation context that the evidence was admitted against: run start time, evaluated revision,
	// offline, dirty tree.
	ctx assurance.Context
	// bindings are the loaded bindings of the workspace; the evaluators map CALM nodes to rendered workloads and
	// planned resources through them. nil in `assurance validate`, which loads no bindings.
	bindings []binding.Binding
	ev       *evidence.Evidence
	report   *AssuranceReport
	// lineage caches the lineage evaluation of the run (assurance_claims_state.go).
	lineage lineageCache
	// candidates are the TESTS and TESTS_FILE edges of the code graph that name a CALM element: candidates for a
	// check, never evidence (assurance_candidates.go).
	candidates []TestCandidate
}

// AssuranceReport is the `assurance` object of results.json (design D16). Every list is sorted.
type AssuranceReport struct {
	Rendered     []RenderedEntry             `json:"rendered"`
	Plans        []PlanEntry                 `json:"plans"`
	Statements   []evidence.StatementSummary `json:"statements"`
	Lineage      []LineageEntry              `json:"lineage"`
	Evaluations  []EvaluationEntry           `json:"evaluations"`
	CheckResults []CheckResultEntry          `json:"checkResults"`
	Quality      []QualityEntry              `json:"quality"`
	// QualityAreas is the coverage of each of the six quality areas with its scenarios and its findings.
	QualityAreas []QualityAreaEntry `json:"qualityAreas"`
	// Candidates are the TESTS and TESTS_FILE edges that name a CALM element. They are candidates for a check and are
	// never evidence that a check ran.
	Candidates []TestCandidate `json:"candidates"`
}

// newAssuranceReport gives a report with no content and no nil list, so every key of the object is an array.
func newAssuranceReport() *AssuranceReport {
	return &AssuranceReport{
		Rendered: []RenderedEntry{}, Plans: []PlanEntry{}, Statements: []evidence.StatementSummary{},
		Lineage: []LineageEntry{}, Evaluations: []EvaluationEntry{}, CheckResults: []CheckResultEntry{},
		Quality: []QualityEntry{}, QualityAreas: []QualityAreaEntry{}, Candidates: []TestCandidate{},
	}
}

// RenderedEntry is one rendered source of `assurance.rendered`: what rendered it, what it read and gave, and how
// complete the result is. It holds digests and counts, never a rendered value.
type RenderedEntry struct {
	Environment string `json:"environment"`
	// Source is the declared source path (the chart directory, the overlay directory, or the plain manifests).
	Source string `json:"source"`
	// Tool is `helm`, `kustomize`, or `plain`.
	Tool string `json:"tool"`
	// ToolVersion and RendererDigest identify the renderer executable that ran; both are empty for `plain`, which
	// starts no program.
	ToolVersion    string `json:"toolVersion,omitempty"`
	RendererDigest string `json:"rendererDigest,omitempty"`
	// Confinement is `none`, `process`, or `process+os`.
	Confinement string `json:"confinement"`
	// InputDigest and OutputDigest are `sha256:<hex>` values. The output digest is empty when the render failed.
	InputDigest  string `json:"inputDigest,omitempty"`
	OutputDigest string `json:"outputDigest,omitempty"`
	// Coverage is `complete`, `partial`, or `failed`.
	Coverage assurance.Coverage `json:"coverage"`
	// Level is always `rendered-or-evaluated`.
	Level         string          `json:"level"`
	Gaps          []assurance.Gap `json:"gaps"`
	ResourceCount int             `json:"resourceCount"`
}

// PlanEntry is one plan of `assurance.plans`. A plan stays visible whatever its binding says; Admissible and Details
// say whether a mandatory claim may rest on it.
type PlanEntry struct {
	Environment string `json:"environment"`
	// Tool is `terraform` or `opentofu`.
	Tool string `json:"tool"`
	// File is the declared plan file.
	File string `json:"file"`
	// Digest is the digest of the plan file; empty when the file could not be read.
	Digest string `json:"digest,omitempty"`
	// FormatVersion is the `format_version` of the plan; empty when the plan was not read.
	FormatVersion string              `json:"formatVersion,omitempty"`
	Integrity     assurance.Integrity `json:"integrity"`
	Coverage      assurance.Coverage  `json:"coverage"`
	// Level is always `rendered-or-evaluated`.
	Level   string             `json:"level"`
	Details []assurance.Detail `json:"details"`
	Gaps    []assurance.Gap    `json:"gaps"`
	// ResourceCount is the number of resources of the plan.
	ResourceCount int  `json:"resourceCount"`
	Admissible    bool `json:"admissible"`
}

// renderedEntries lists the rendered sources of the evidence, sorted by environment, source, and tool.
func renderedEntries(ev *evidence.Evidence) []RenderedEntry {
	out := make([]RenderedEntry, 0, len(ev.Renders))

	for _, res := range ev.Renders {
		entry := RenderedEntry{
			Environment: res.Environment, Source: res.Path, Tool: res.Tool, Confinement: res.Confinement,
			InputDigest: res.InputDigest, OutputDigest: res.OutputDigest, ResourceCount: res.ResourceCount(),
			Coverage: res.Coverage, Level: res.Level, Gaps: gapsOrEmpty(res.Gaps),
		}

		if res.Renderer != nil {
			// render.Identity carries the digest in the repository form `sha256:<hex>` (digest.OfFile).
			entry.ToolVersion, entry.RendererDigest = res.Renderer.Version, res.Renderer.SHA256
		}

		out = append(out, entry)
	}

	slices.SortStableFunc(out, func(a, b RenderedEntry) int {
		return compareKeys3(a.Environment, a.Source, a.Tool, b.Environment, b.Source, b.Tool)
	})

	return out
}

// planEntries lists the declared plans of the evidence, sorted by environment, file, and tool.
func planEntries(ev *evidence.Evidence) []PlanEntry {
	out := make([]PlanEntry, 0, len(ev.Plans))

	for i := range ev.Plans {
		declared := &ev.Plans[i]

		entry := PlanEntry{
			Environment: declared.Declared.Environment, Tool: declared.Declared.Tool, File: declared.Declared.File,
			Integrity: declared.Binding.Admission.Integrity, Admissible: declared.Binding.Admissible(),
			Details: declared.Binding.Admission.Details, Coverage: declared.Coverage, Level: planLevel,
		}

		gaps := slices.Clone(declared.Gaps)

		if p := declared.Plan; p != nil {
			entry.Digest, entry.FormatVersion, entry.ResourceCount = p.Digest, p.FormatVersion, len(p.Resources)
			gaps = append(gaps, p.Gaps...)
		}

		entry.Gaps = gapsOrEmpty(gaps)

		if entry.Details == nil {
			entry.Details = []assurance.Detail{}
		}

		out = append(out, entry)
	}

	slices.SortStableFunc(out, func(a, b PlanEntry) int {
		return compareKeys3(a.Environment, a.File, a.Tool, b.Environment, b.File, b.Tool)
	})

	return out
}

// planLevel is the evidence level of a plan: evaluated, never `deployment-execution` (a plan says what would be
// applied, not that anything was).
const planLevel = "rendered-or-evaluated"

func gapsOrEmpty(gaps []assurance.Gap) []assurance.Gap {
	if gaps == nil {
		return []assurance.Gap{}
	}

	return gaps
}

// compareKeys3 orders two triples of strings in UTF-16 order.
func compareKeys3(a1, a2, a3, b1, b2, b3 string) int {
	for _, pair := range [][2]string{{a1, b1}, {a2, b2}, {a3, b3}} {
		if c := canonjson.CompareUTF16(pair[0], pair[1]); c != 0 {
			return c
		}
	}

	return 0
}

// AssuranceEvidence is the evidence that the assurance report of a run was made from: what the evaluation bundle
// writes (see AssuranceBundleFiles).
type AssuranceEvidence struct {
	Evidence *evidence.Evidence
	// Contracts are the claim contracts that the run loaded.
	Contracts []claims.Contract
}

// applyAssurance sets the assurance report, the toolchain of the assurance evidence, and the bundle evidence on the
// run. Without an `assurance` key the run carries none of them.
func (c *checker) applyAssurance(run *Run) {
	state := c.assurance
	if state == nil {
		return
	}

	state.report.Lineage = state.lineageEntries()

	run.Assurance = state.report
	run.AssuranceEvidence = &AssuranceEvidence{Evidence: state.ev, Contracts: loadedContracts(state.cfg).Contracts()}

	evaluators := evaluatorIdentities(state.cfg)
	if evaluators == nil {
		evaluators = []EvaluatorIdentity{}
	}

	// The loader keeps one identity per rendered source; the toolchain lists each renderer once, sorted by name.
	renderers := slices.Clone(state.ev.Toolchain.Renderers)
	slices.SortFunc(renderers, func(a, b render.Identity) int {
		return cmp.Or(canonjson.CompareUTF16(a.Name, b.Name), canonjson.CompareUTF16(a.Version, b.Version), canonjson.CompareUTF16(a.SHA256, b.SHA256))
	})

	renderers = slices.Compact(renderers)
	if renderers == nil {
		renderers = []render.Identity{}
	}

	run.Toolchain.Assurance = &AssuranceToolchain{
		Renderers: renderers, Cosign: state.ev.Toolchain.Cosign, Evaluators: evaluators,
		ClaimsRegistryDigest: state.ev.ClaimsDigest, TrustContractDigest: trustContractDigestOf(state),
	}
}

// externalRule gives the outcomes of a rule of an external family (see ExternalRuleFunc).
func (c *checker) externalRule(plan *policy.RulePlan) ([]ExternalOutcome, bool) {
	switch plan.Predicate.Family { //nolint:exhaustive // only the three external families have outcomes here.
	case policy.FamilyAssuranceClaim:
		return c.claimOutcomes(plan)
	case policy.FamilyEffectiveAccess:
		return c.accessOutcomes(plan)
	case policy.FamilyCheckResult:
		return c.checkOutcomes(plan)
	}

	return nil, false
}
