package conformance

import (
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/redact"
)

// ResultSchemaVersion is the `schemaVersion` of the check result.
const ResultSchemaVersion = "1.1"

// InputRef is a material input of the run: workspace-relative "/" path and `sha256:<hex>` digest.
type InputRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// RunInputs are the material inputs of the run.
type RunInputs struct {
	Lock            *InputRef  `json:"lock,omitempty"`
	Baseline        *InputRef  `json:"baseline,omitempty"`
	Waivers         *InputRef  `json:"waivers,omitempty"`
	Workspace       InputRef   `json:"workspace"`
	Architecture    InputRef   `json:"architecture"`
	QueryPlanDigest string     `json:"queryPlanDigest"`
	Patterns        []InputRef `json:"patterns"`
	Decorators      []InputRef `json:"decorators"`
	Policies        []InputRef `json:"policies"`
	Bindings        []InputRef `json:"bindings"`
	Traces          []InputRef `json:"traces,omitempty"`
	// Decisions are the decision register files and the lifecycle profile that the ADR governance checks read.
	// Left out when no register is configured.
	Decisions []InputRef `json:"decisions,omitempty"`
	// Assessments are the assessment and assumption files that the review evaluation read, and Approvals the approval
	// and revocation files. Left out when the run does not evaluate assessments.
	Assessments []InputRef `json:"assessments,omitempty"`
	Approvals   []InputRef `json:"approvals,omitempty"`
	// AuthorityDigest is the digest of the authority configuration that verification used (the trust anchor's when
	// it is readable), and TrustAnchor is the revision of the trust anchor. Left out with Assessments.
	AuthorityDigest string `json:"authorityDigest,omitempty"`
	TrustAnchor     string `json:"trustAnchor,omitempty"`
	// Environments records the declared environments the run covers. Excluded environments have no verdict.
	Environments RunEnvironments `json:"environments"`
	// IndexResults are the index result records (`concordia index`) that the run read, one for each project that had one.
	IndexResults []InputRef `json:"indexResults"`
}

// RunEnvironments is the environment selection of a run (`check --environment`).
type RunEnvironments struct {
	// Digest is the SHA-256 of the canonical JSON of the declared environments (id, sources, cluster).
	Digest string `json:"digest"`
	// Selected are the environments that obligations evaluate, in declaration order. All declared ones without `--environment`.
	Selected []string `json:"selected"`
	// Excluded are the declared environments that `--environment` left out. They have no verdict.
	Excluded []string `json:"excluded"`
}

// RunHost describes the machine of the run.
type RunHost struct {
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	Go       string `json:"go"`
}

// RunInfo identifies the run.
type RunInfo struct {
	// SourceRevision is the git HEAD, or nil when the workspace is not a git repository.
	SourceRevision *string `json:"sourceRevision"`
	// Changed is set when the run is limited with --changed.
	Changed *RunChanged `json:"changed,omitempty"`
	Host    RunHost     `json:"host"`
	ID      string      `json:"id"`
	// StartedAt and FinishedAt are ISO 8601 UTC.
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	// DirtyTreeDigest is the `sha256:<hex>` of the dirty-tree content.
	DirtyTreeDigest string `json:"dirtyTreeDigest"`
	// GraphGeneration is `cbm:<project>:<16 hex>`.
	GraphGeneration string `json:"graphGeneration"`
	Project         string `json:"project"`
	// Projects lists the selected projects. It is set only when two or more projects are selected.
	Projects []RunProject `json:"projects,omitempty"`
}

// RunChanged is the changed scope of a run.
type RunChanged struct {
	Ref   string   `json:"ref"`
	Base  string   `json:"base"`
	Files []string `json:"files"`
}

// RunProject is the frozen state of one project of a run.
type RunProject struct {
	Name            string  `json:"name"`
	Root            string  `json:"root"`
	SourceRevision  *string `json:"sourceRevision"`
	DirtyTreeDigest string  `json:"dirtyTreeDigest"`
	GraphGeneration string  `json:"graphGeneration"`
	// ChangedBase is the commit that the changed files of the project are compared with.
	ChangedBase string `json:"changedBase,omitempty"`
}

// CbmProfile is the capability profile of a run. When no profile applies to
// the build, Applies is false and the identifier and digest are empty: the
// run records that no profile applies and never omits the field.
type CbmProfile struct {
	ID      string `json:"id,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Applies bool   `json:"applies"`
}

// NewCbmProfile records the profile of a run (nil: no profile applies).
func NewCbmProfile(ref *observation.ProfileRef) CbmProfile {
	if ref == nil {
		return CbmProfile{}
	}

	return CbmProfile{Applies: true, ID: ref.ID, Digest: ref.Digest}
}

// CbmToolchain describes the CBM executable, adapter and capability profile.
type CbmToolchain struct {
	ExecutableVersion string `json:"executableVersion"`
	// SHA256 is `sha256:<hex>` of the CBM executable file that served the run. It is left out when the client has no
	// executable (a test fake), so a run without it has the toolchain of before.
	SHA256        string     `json:"sha256,omitempty"`
	AdapterSchema string     `json:"adapterSchema"`
	Profile       CbmProfile `json:"profile"`
}

// RunToolchain holds the versions of the tools of the run.
type RunToolchain struct {
	// Assurance is what the assurance evidence used: renderers, cosign, evaluators, and the digests of the claim
	// registry and the trust contract. Left out without an `assurance` key (additive, schemaVersion stays 1.1).
	Assurance  *AssuranceToolchain `json:"assurance,omitempty"`
	Concordia  string              `json:"concordia"`
	CalmSchema string              `json:"calmSchema"`
	// Structurizr is the Structurizr CLI version that regenerated a view export in this run (the pinned version).
	// It is left out when no export was verified, so a run without view exports has the toolchain of before.
	Structurizr string       `json:"structurizr,omitempty"`
	Cbm         CbmToolchain `json:"cbm"`
}

// AssuranceToolchain records the tools and the contracts that the assurance evidence of a run depended on, so that a
// run after an upgrade shows what changed (design D23).
type AssuranceToolchain struct {
	// Cosign is the version of the cosign that verified a statement; left out when none ran.
	Cosign string `json:"cosign,omitempty"`
	// ClaimsRegistryDigest is the digest of the claim registry of this build.
	ClaimsRegistryDigest string `json:"claimsRegistryDigest"`
	// TrustContractDigest is the digest of the producer trust contract of the workspace; left out when the workspace
	// declares no provenance.
	TrustContractDigest string `json:"trustContractDigest,omitempty"`
	// Renderers are the renderers that rendered a source, sorted by name.
	Renderers []render.Identity `json:"renderers"`
	// Evaluators are the effective-policy evaluators that an environment enables, with their contract digests.
	Evaluators []EvaluatorIdentity `json:"evaluators"`
}

// RuleCounts counts the rules by verdict.
type RuleCounts struct {
	Pass          int `json:"pass"`
	Violation     int `json:"violation"`
	Unknown       int `json:"unknown"`
	NotApplicable int `json:"notApplicable"`
	Error         int `json:"error"`
}

// RunSummary is the summary of the run.
type RunSummary struct {
	// Rollback records the active `check.rollback`: the typed-obligation, decision-governance, and review
	// rules ran as `shadow`, so the output is not a successful typed, governance, or review check. nil when no rollback was active.
	Rollback *RunRollback `json:"rollback,omitempty"`
	// Verdict is the worst rule verdict; `not-applicable` when there are no rules.
	Verdict RuleVerdict   `json:"verdict"`
	FailOn  diag.Severity `json:"failOn"`
	// UnknownPolicy is the unknown policy of the run. Each rule has its own effective `onUnknown`.
	UnknownPolicy policy.UnknownDisposition `json:"unknownPolicy"`
	// ResultDigest is `sha256:<hex>` of the canonical JSON of the run without the volatile fields (see ResultDigest).
	ResultDigest string     `json:"resultDigest"`
	Rules        RuleCounts `json:"rules"`
	ExitCode     int        `json:"exitCode"`
	// Active counts findings with the disposition `active`.
	Active        int `json:"active"`
	Baselined     int `json:"baselined"`
	Waived        int `json:"waived"`
	ExpiredWaiver int `json:"expiredWaiver"`
	// UnknownRules counts the rules with the verdict `unknown`.
	UnknownRules int `json:"unknownRules"`
}

// ControlResult is the result of one control: the worst verdict of its rules and its checks.
type ControlResult struct {
	Scope     intent.ControlScope `json:"scope"`
	ControlID string              `json:"controlId"`
	Verdict   RuleVerdict         `json:"verdict"`
	RuleIDs   []string            `json:"ruleIds"`
	// CheckIDs are the checks that the control lists in `check-ids`, sorted. Left out when it lists none.
	CheckIDs []string `json:"checkIds,omitempty"`
	// Reasons say why a check of the control is `unknown` (or `error`): one for each such check, naming it. Left out
	// when every check has a definite result.
	Reasons []Reason `json:"reasons,omitempty"`
}

// AppliedWaiver is a waiver that a finding of this run refers to.
type AppliedWaiver struct {
	ID   string `json:"id"`
	Rule string `json:"rule"`
	// Finding is the fingerprint of a finding waiver. Empty (left out) for a scoped waiver.
	Finding string `json:"finding,omitempty"`
	Owner   string `json:"owner"`
	Reason  string `json:"reason"`
	Issue   string `json:"issue,omitempty"`
	// Expires is YYYY-MM-DD.
	Expires string `json:"expires"`
	// State is `active` or `expired`.
	State string `json:"state"`
}

// RunExceptions describes how the exception registers were used.
type RunExceptions struct {
	// ResolvedBaselineEntries are baseline fingerprints with no current finding (only rules that ran). Sorted.
	ResolvedBaselineEntries []string `json:"resolvedBaselineEntries"`
	// OrphanedWaivers are waiver IDs with no current finding (only rules that ran). Sorted.
	OrphanedWaivers []string `json:"orphanedWaivers"`
	// Waivers are the waivers that findings refer to, sorted by ID.
	Waivers []AppliedWaiver `json:"waivers"`
	// ScopedWaivers records each scoped waiver of the register with its approver verification and the findings it
	// covered, sorted by ID. Left out when the register has no scoped waiver.
	ScopedWaivers []ScopedWaiver `json:"scopedWaivers,omitempty"`
}

// Run is the canonical result of one `concordia check` run (`schemaVersion` "1.0").
//
// The JSON reporter writes it with canonjson. All collections are sorted: Rules by rule
// ID, Findings by (ruleId, fingerprint), Reconciliation by (from, to), Controls by
// (controlId, scope, pointer), Diagnostics by (code, source, message).
type Run struct {
	// Evidence is the merged evidence of the run. It is set only with CheckOptions.KeepEvidence and is not part of the JSON.
	Evidence *observation.EvidenceQueryResult `json:"-"`
	// ViewInput is the state that the view checks read. It is set only with CheckOptions.KeepViewInput and is not part of the JSON.
	ViewInput *ViewInput `json:"-"`
	// Areas are the six report areas, each with its own verdict and counts (OUT-03). An area that the workspace
	// does not configure shows `not-configured`.
	Areas         Areas     `json:"areas"`
	SchemaVersion string    `json:"schemaVersion"`
	Inputs        RunInputs `json:"inputs"`
	Run           RunInfo   `json:"run"`
	// DeploymentTrace is the declared-level deployment trace: one trace for each trace subject and selected
	// environment, the unbound workloads, the workloads with no environment, and the summary. Always present.
	DeploymentTrace deployment.Trace      `json:"deploymentTrace"`
	Toolchain       RunToolchain          `json:"toolchain"`
	Exceptions      RunExceptions         `json:"exceptions"`
	Controls        []ControlResult       `json:"controls"`
	Diagnostics     []diag.Diagnostic     `json:"diagnostics"`
	Reconciliation  []ReconciliationEntry `json:"reconciliation"`
	Rules           []RuleResult          `json:"rules"`
	Findings        []Finding             `json:"findings"`
	// Views are the results of the view checks, one for each declared view, sorted by view ID. The key is left out
	// when the workspace declares no view, so the result of such a run is the one of before (design D3).
	Views []ViewResult `json:"views,omitempty"`
	// DecisionSummary is the per-decision detail of the ADR governance area. Left out when no register is
	// configured, so the result of such a run is the one of before (schemaVersion stays 1.1).
	DecisionSummary *DecisionSummary `json:"decisionSummary,omitempty"`
	// Trust holds the boundary crossings of the relationships, flow steps, and observed flows. Left out when no
	// node, relationship, or binding declares a trust value, so the result of such a run is the one of before
	// (schemaVersion stays 1.1).
	Trust *TrustResult `json:"trust,omitempty"`
	// AssessmentCurrency holds the currency of every assessment version and required assessment. Left out when the
	// run does not evaluate assessments, so the result of such a run is the one of before (schemaVersion stays 1.1).
	AssessmentCurrency *AssessmentCurrencyReport `json:"assessmentCurrency,omitempty"`
	// Governance holds the relaxations of the review configuration and the dispositions of removed subjects. Left
	// out with AssessmentCurrency.
	Governance *GovernanceReport `json:"governance,omitempty"`
	// Analysis says how the evaluation ran and how complete it was. Left out with AssessmentCurrency.
	Analysis *AnalysisReport `json:"analysis,omitempty"`
	// Assurance holds the rendered sources, plans, statements, lineage, effective-policy evaluations, check results,
	// and quality scenarios of the run. Left out without an `assurance` key (schemaVersion stays 1.1).
	Assurance *AssuranceReport `json:"assurance,omitempty"`
	// AssuranceEvidence is the evidence that the assurance report was made from, with its redaction records, for the
	// evaluation bundle. It is not part of the JSON; nil without an `assurance` key.
	AssuranceEvidence *AssuranceEvidence `json:"-"`
	// ReviewEvidence is what the evaluation bundle records about the review evaluation. It is not part of the JSON.
	ReviewEvidence *ReviewEvidence `json:"-"`
	// DecisionRedactions records the secret values that were removed from the ADR titles in the findings and the
	// decision summary. It is not part of the JSON; the CLI reports it (CO-REP-005) and the bundle manifest lists it.
	DecisionRedactions []redact.Record `json:"-"`
	// Coverage is the coverage section of the run: predicate records, failed scopes, index results, skipped facts, limits.
	Coverage RunCoverage `json:"coverage"`
	Summary  RunSummary  `json:"summary"`
	// Observability holds the counters of the run: plain integers with fixed names, no text of the repository.
	Observability Observability `json:"observability"`
}
