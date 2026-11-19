package conformance

import (
	"context"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/workspace"
)

// DecisionProject is one selected project as the ADR governance checks read it.
type DecisionProject struct {
	// Git is the Git port of the project root.
	Git workspace.GitPort
	// Name is the workspace project name, also for the primary project (the project whose Prefix is "").
	Name string
	// Prefix is "" for the primary project, else "@<name>/" (the prefix of the paths of the project).
	Prefix string
	// Root is the absolute project root.
	Root string
	// Revision is the evaluated commit of the project; "" when the project has no git revision.
	Revision string
	// ChangedBase is the commit that the changed files are compared with; set with --changed.
	ChangedBase string
}

// DecisionEvalInput is the input of Deps.EvaluateDecisions: the state of the run that the ADR governance checks
// read. It is plain data; the hook reads Git through the ports and never writes a file.
type DecisionEvalInput struct {
	// Workspace is the loaded workspace (the `adr` key, the root).
	Workspace *workspace.Workspace
	// Model is the loaded architecture model.
	Model *intent.ArchitectureModel
	// Evidence is the merged evidence of the run (decision entities, `supersedes`, `mentions`, `references-file`
	// edges, file coverage with the stale files). nil when the run has no evidence.
	Evidence *observation.EvidenceQueryResult
	// Policies is the compile result of the run: the selected plans, the packs, and every declared rule.
	Policies *policy.CompileResult
	// Changed is the changed scope of the run; nil without --changed.
	Changed *RunChanged
	// FailOn is the effective `check.failOn` of the run.
	FailOn diag.Severity
	// OnUnknown is the effective `check.unknown` of the run (after --unknown).
	OnUnknown policy.UnknownDisposition
	// Coverage is the file coverage of each selected project, parallel to Projects: the stale files and the
	// extraction completeness that the evidence of a project depends on. Its paths already carry the project prefix.
	Coverage []observation.FileCoverage
	// Capabilities are the provider capabilities of the run (the families of the capability profile).
	Capabilities observation.Capabilities
	// Projects are the selected projects, primary first.
	Projects []DecisionProject
}

// DecisionEvaluation is the output of Deps.EvaluateDecisions: the rule results and findings of the
// `adr-governance` pack, the decision summary, the inputs to record, and the non-blocking diagnostics. Rules and
// findings are treated as the rest: dispositions, `check.failOn`, `check.unknown` and the exit code apply.
type DecisionEvaluation struct {
	// Summary is the per-decision detail (`decisionSummary`). nil when no register is configured.
	Summary *DecisionSummary
	// Rules are the results of GOV-ADR-001..006 that the run selected.
	Rules []RuleResult
	// Findings are the findings of the rules, with their fingerprints.
	Findings []Finding
	// Inputs are the register files and the lifecycle profile (`inputs.decisions`), sorted by path.
	Inputs []InputRef
	// Redactions records the secret values that were removed from the ADR titles shown in findings and in the
	// summary (Output `finding` or `summary`); never the value. Not part of results.json.
	Redactions []redact.Record
	// Diagnostics are the warning and informational CO-ADR diagnostics of the evaluation.
	Diagnostics []diag.Diagnostic
}

// EvaluateDecisionsFunc runs the ADR governance checks. `internal/adr` implements it (this package cannot import
// it); the CLI injects it as Deps.EvaluateDecisions. An invalid register, reference, link, lifecycle profile or
// trigger gives a *diag.Error with exit code 2.
type EvaluateDecisionsFunc func(ctx context.Context, in *DecisionEvalInput) (*DecisionEvaluation, error)

// DecisionSummary is the top-level `decisionSummary` of a run: the per-decision states, the history, the rule
// justification, the trigger results and the candidates. Every list is sorted and never nil.
type DecisionSummary struct {
	// ProviderSupport is `supported` or `unsupported` (the capability profile has no ADR evidence).
	ProviderSupport string `json:"providerSupport"`
	// Decisions are the registered decisions, sorted by canonical ID.
	Decisions []DecisionEntry `json:"decisions"`
	// History is the normalized supersession history of the components that contain a registered decision.
	History DecisionHistory `json:"history"`
	// RuleJustification are the results of GOV-ADR-003, sorted by rule ref.
	RuleJustification []RuleJustification `json:"ruleJustification"`
	// Triggers are the results of GOV-ADR-006, sorted by trigger ID.
	Triggers []TriggerResult `json:"triggers"`
	// Candidates are non-authoritative candidates (never a verdict), sorted by kind, ID, path.
	Candidates []DecisionCandidate `json:"candidates"`
	// Findings are the fingerprints of the governance findings, sorted.
	Findings []string `json:"findings"`
}

// DecisionEntry is one registered decision with its three separate states.
type DecisionEntry struct {
	Presence      DecisionState         `json:"presence"`
	MappedStatus  string                `json:"mappedStatus,omitempty"`
	Expect        string                `json:"expect"`
	ID            string                `json:"id"`
	Project       string                `json:"project,omitempty"`
	Status        string                `json:"status"`
	StatusText    string                `json:"statusText"`
	Title         string                `json:"title"`
	Path          string                `json:"path"`
	Applicability DecisionApplicability `json:"applicability"`
	Categories    []string              `json:"categories"`
	Governs       []string              `json:"governs"`
	Released      []string              `json:"released"`
	Successors    []string              `json:"successors"`
	Predecessors  []string              `json:"predecessors"`
	Enforcement   DecisionEnforcement   `json:"enforcement"`
	Line          int                   `json:"line,omitempty"`
}

// DecisionState is a state with its evidence reason.
type DecisionState struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// DecisionApplicability is the applicability state of a decision.
type DecisionApplicability struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
	// StatusLags says that the decision is superseded by an active successor while its own status still says active.
	StatusLags bool `json:"statusLags,omitempty"`
}

// DecisionEnforcement is the enforcement coverage of a decision.
type DecisionEnforcement struct {
	State    string   `json:"state"`
	Rules    []string `json:"rules"`
	Required bool     `json:"required"`
}

// DecisionHistory is the normalized supersession history.
type DecisionHistory struct {
	// Links are successor-to-predecessor links, sorted by from, then to.
	Links []HistoryLink `json:"links"`
}

// HistoryLink is one supersession link.
type HistoryLink struct {
	// From is the label of the successor (canonical ID), To the label of the predecessor.
	From string `json:"from"`
	To   string `json:"to"`
	// FromPath and ToPath are the repository-relative ADR paths (the evidence identity).
	FromPath string `json:"fromPath"`
	ToPath   string `json:"toPath"`
	// Provenance is `edge`, `metadata`, or `both`.
	Provenance string `json:"provenance"`
	// Effect is `effective` or `pending`.
	Effect string `json:"effect"`
}

// RuleJustification is the result of GOV-ADR-003 for one linked rule.
type RuleJustification struct {
	Rule      string   `json:"rule"`
	Result    string   `json:"result"`
	LinkedBy  []string `json:"linkedBy"`
	Mandatory bool     `json:"mandatory"`
}

// TriggerResult is the result of GOV-ADR-006 for one configured trigger.
type TriggerResult struct {
	ID          string   `json:"id"`
	Category    string   `json:"category"`
	Result      string   `json:"result"`
	Reason      string   `json:"reason,omitempty"`
	RaisedBy    []string `json:"raisedBy"`
	SatisfiedBy []string `json:"satisfiedBy"`
	Raised      bool     `json:"raised"`
}

// DecisionCandidate is a non-authoritative candidate: an unregistered decision, a mention, or a CALM `adrs` entry.
type DecisionCandidate struct {
	// Kind is `unregistered-decision`, `mention`, or `calm-adrs-entry`.
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
	// Subject is the mentioned file or node, for a mention.
	Subject string `json:"subject,omitempty"`
}

// FindingDecision is the `decision` block of a governance finding (design D7).
type FindingDecision struct {
	ID                  string               `json:"id"`
	Title               string               `json:"title"`
	Status              string               `json:"status"`
	StatusText          string               `json:"statusText"`
	Path                string               `json:"path"`
	Successors          []string             `json:"successors"`
	Predecessors        []string             `json:"predecessors"`
	Governs             []string             `json:"governs"`
	EnforcedBy          []string             `json:"enforcedBy"`
	CandidateSuccessors []CandidateSuccessor `json:"candidateSuccessors"`
	Line                int                  `json:"line,omitempty"`
}

// CandidateSuccessor is a successor named by the history. Applied is always false: governance never moves to a
// successor without a register edit.
type CandidateSuccessor struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Applied bool   `json:"applied"`
}
