// Package deployment builds the declared-level deployment trace: for each CALM service and each declared
// environment, the chain from the service to its workloads, container image references, build definitions, and
// source evidence. It is made of pure functions over normalized evidence, the binding set, the model, the
// coverage records, and the typed rule results. It reads no file and calls no provider.
//
// The trace states what the declared configuration shows. It never says that a workload runs or that a deployment
// happened: the output of this package has no field or text of that kind (DEP-02).
package deployment

import "github.com/inclaro/concordia/internal/observation"

// Status of the whole `deploymentTrace` section.
const (
	// StatusConfigured means the workspace declares at least one environment.
	StatusConfigured = "configured"
	// StatusNotConfigured means the workspace declares no environment, so no trace is built.
	StatusNotConfigured = "not-configured"
)

// The levels of the trace. Only the declared level is evaluated in this release.
const (
	LevelDeclared            = "declared"
	LevelRenderedOrEvaluated = "rendered-or-evaluated"
	LevelDeploymentExecution = "deployment-execution"
	LevelRuntimeObservation  = "runtime-observation"

	levelEvaluated    = "evaluated"
	levelNotAvailable = "not-available"
)

// LevelState is the status of one level.
type LevelState struct {
	Level  string `json:"level"`
	Status string `json:"status"`
}

// Levels gives the four levels with their status: only `declared` is `evaluated`.
func Levels() []LevelState {
	return []LevelState{
		{Level: LevelDeclared, Status: levelEvaluated},
		{Level: LevelRenderedOrEvaluated, Status: levelNotAvailable},
		{Level: LevelDeploymentExecution, Status: levelNotAvailable},
		{Level: LevelRuntimeObservation, Status: levelNotAvailable},
	}
}

// Status is the status of the trace of one subject in one environment.
type Status string

// The trace statuses (deployment-trace spec, "Trace status by evidence").
const (
	// StatusComplete: every step has a resolved entity for every bound workload and no gap is open.
	StatusComplete Status = "complete"
	// StatusPartial: a step is resolved and a gap is open.
	StatusPartial Status = "partial"
	// StatusUnresolved: `deployment-workload-set` is complete and no workload is found.
	StatusUnresolved Status = "unresolved"
	// StatusUnknown: the coverage is not sufficient to decide.
	StatusUnknown Status = "unknown"
)

// Statuses lists the statuses in report order.
func Statuses() []Status {
	return []Status{StatusComplete, StatusPartial, StatusUnresolved, StatusUnknown}
}

// Step names one of the five steps of a trace, in order.
type Step string

// The five steps.
const (
	StepService         Step = "service"
	StepWorkload        Step = "workload"
	StepContainerImage  Step = "container-image"
	StepBuildDefinition Step = "build-definition"
	StepSourceEvidence  Step = "source-evidence"
)

// Steps lists the five steps in order.
func Steps() []Step {
	return []Step{StepService, StepWorkload, StepContainerImage, StepBuildDefinition, StepSourceEvidence}
}

// Reason is the reason of a gap. The set is closed: the closed `unknown` reasons of the foundation and of review
// currency (the identifiers are the same strings) and three reasons of the trace.
type Reason string

// The foundation reasons.
const (
	ReasonCoverageIncomplete      Reason = "coverage-incomplete"
	ReasonUnsupportedFamily       Reason = "unsupported-family"
	ReasonCapabilityRegression    Reason = "capability-regression"
	ReasonEnvironmentUnresolved   Reason = "environment-unresolved"
	ReasonAmbiguousBinding        Reason = "ambiguous-binding"
	ReasonBindingGap              Reason = "binding-gap"
	ReasonEvidenceConflict        Reason = "evidence-conflict"
	ReasonSelectorPropertyUnknown Reason = "selector-property-unknown"
	ReasonLimitExceeded           Reason = "limit-exceeded"
)

// The reasons of review currency (add-review-currency). They are the same strings as the foundation `unknown` reasons.
const (
	ReasonTargetNotDeclared       Reason = "target-not-declared"
	ReasonTrustAnchorUnreadable   Reason = "trust-anchor-unreadable"
	ReasonVerificationUnavailable Reason = "verification-unavailable"
	ReasonReferenceUnresolved     Reason = "reference-unresolved"
)

// The reasons of the assurance adapters (add-assurance-adapters). They are the same strings as the foundation `unknown`
// reasons.
const (
	ReasonClaimNotEnabled          Reason = "claim-not-enabled"
	ReasonNoEvidenceSupplied       Reason = "no-evidence-supplied"
	ReasonNoEvaluatorAvailable     Reason = "no-evaluator-available"
	ReasonRenderedEvidenceMissing  Reason = "rendered-evidence-missing"
	ReasonEnforcementNotDeclared   Reason = "enforcement-not-declared"
	ReasonReviewedModeNotAvailable Reason = "reviewed-mode-not-available"
)

// The reasons that only the trace has.
const (
	// ReasonAbsent is a definite absence: the extraction of the environment is complete and shows no entity.
	ReasonAbsent Reason = "absent"
	// ReasonAmbiguousBuildDefinition: several Dockerfiles are linked to one image repository.
	ReasonAmbiguousBuildDefinition Reason = "ambiguous-build-definition"
	// ReasonMutableTag: the image reference has no immutable digest.
	ReasonMutableTag Reason = "mutable-tag"
)

// Reasons gives the closed set of gap reasons.
func Reasons() []Reason {
	return []Reason{
		ReasonCoverageIncomplete, ReasonUnsupportedFamily, ReasonCapabilityRegression, ReasonEnvironmentUnresolved,
		ReasonAmbiguousBinding, ReasonBindingGap, ReasonEvidenceConflict, ReasonSelectorPropertyUnknown, ReasonLimitExceeded,
		ReasonTargetNotDeclared, ReasonTrustAnchorUnreadable, ReasonVerificationUnavailable, ReasonReferenceUnresolved,
		ReasonClaimNotEnabled, ReasonNoEvidenceSupplied, ReasonNoEvaluatorAvailable, ReasonRenderedEvidenceMissing,
		ReasonEnforcementNotDeclared, ReasonReviewedModeNotAvailable,
		ReasonAbsent, ReasonAmbiguousBuildDefinition, ReasonMutableTag,
	}
}

// Lineage is the lineage of an image: whether a link to a build definition is known. `candidate` is a static link
// (a `built-from` fact), `verified` is an exact lineage claim that passes for the container occurrence, and
// `unknown` is neither.
type Lineage string

// The lineage values.
const (
	LineageCandidate Lineage = "candidate"
	LineageUnknown   Lineage = "unknown"
	LineageVerified  Lineage = "verified"
)

// Location is a place in a file of the workspace. Line 0 means no line.
type Location struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

// Gap is something that supplying evidence or configuration can fix. It counts in the status of the trace.
type Gap struct {
	Reason    Reason   `json:"reason"`
	Step      Step     `json:"step,omitempty"`
	Detail    string   `json:"detail"`
	LossModes []string `json:"lossModes,omitempty"`
}

// Limit is a permanent boundary of the declared level. It never changes the status, and it always shows.
type Limit struct {
	ID     string `json:"id"`
	Detail string `json:"detail"`
}

// The limits of the trace.
const (
	// LimitImageProvenance: no verified link between the image of the cluster and a Dockerfile (DEP-04).
	LimitImageProvenance = "image-provenance-unverified"
	// LimitMergedOccurrences: the provider keeps one reference for each workload and repository (O4, DEP-05).
	LimitMergedOccurrences = "provider-may-merge-occurrences"
	// LimitOtherConsumers: an image reference has consumers that belong to other subjects or environments.
	LimitOtherConsumers = "other-consumers-belong-to-other-subjects"
	// LimitSourceProvenance: no provenance links the image to the source paths of the service (DEP-04).
	LimitSourceProvenance = "source-provenance-unverified"
	// LimitDeclaredLevel: the trace shows the declared configuration only.
	LimitDeclaredLevel = "declared-level-only"
)

// Entity is one thing of a step: a service, workload, image reference, Dockerfile, or source path. Its properties
// are an allow-list (see AllowedProperties); a value of the manifest never reaches it.
type Entity struct {
	Properties map[string]string      `json:"properties"`
	Identity   string                 `json:"identity"`
	Kind       string                 `json:"kind"`
	Resolution observation.Resolution `json:"resolution,omitempty"`
	Lineage    Lineage                `json:"lineage,omitempty"`
	Locations  []Location             `json:"locations"`
	// Bindings are the IDs of the bindings that link the entity to the service.
	Bindings []string `json:"bindings,omitempty"`
	// Consumers is the number of workloads of all declared environments that use the image repository.
	Consumers int `json:"consumers,omitempty"`
}

// The entity kinds.
const (
	KindService          = "service"
	KindWorkload         = "workload"
	KindHostingTarget    = "hosting-target"
	KindContainerImage   = "container-image"
	KindDockerfile       = "dockerfile"
	KindBaseImage        = "base-image"
	KindExternalCopy     = "external-copy-source"
	KindUnlinkedImage    = "unlinked-image"
	KindDeploymentAction = "deployment-action"
	KindSourcePath       = "source-path"
	KindManifest         = "manifest"
	// KindBuildProvenance is the verified link of one container occurrence to its build: a provenance statement of a
	// trusted producer that names the digest (spec artifact-provenance). The Dockerfile entity stays a candidate.
	KindBuildProvenance = "build-provenance"
)

// StepTrace is one step of a trace with its evidence role and resolution. The two are never merged into one score.
type StepTrace struct {
	Step       Step                     `json:"step"`
	Role       observation.EvidenceRole `json:"role"`
	Resolution observation.Resolution   `json:"resolution"`
	Entities   []Entity                 `json:"entities"`
	Gaps       []Gap                    `json:"gaps"`
	Limits     []Limit                  `json:"limits"`
}

// RuleLink is the verdict of an `infrastructure` rule obligation for a subject in an environment. Rules are
// computed by the obligation evaluator, independently of the trace status.
type RuleLink struct {
	RuleID       string `json:"ruleId"`
	ObligationID string `json:"obligationId"`
	Verdict      string `json:"verdict"`
	// Subject and Environment select the link. The trace of a subject lists the links of its own subject and environment.
	Subject     string `json:"-"`
	Environment string `json:"-"`
}

// SubjectTrace is the trace of one subject in one environment.
type SubjectTrace struct {
	ArchitectureID string `json:"architectureId"`
	// Environment is the declared environment of the trace.
	Environment string `json:"environment"`
	// Level is always `declared`.
	Level  string      `json:"level"`
	Status Status      `json:"status"`
	Steps  []StepTrace `json:"steps"`
	// Gaps and Limits are the union of the gaps and limits of the steps.
	Gaps   []Gap      `json:"gaps"`
	Limits []Limit    `json:"limits"`
	Rules  []RuleLink `json:"rules"`
	// Evaluated are the rendered workloads and the planned resources that a binding maps to the subject, at the level
	// `rendered-or-evaluated`. They are not part of the declared chain and never change its status. Left out when none.
	Evaluated []Entity `json:"evaluated,omitempty"`
}

// UnboundWorkload is a workload that no trace subject owns, with its image references and the build definitions
// of their repositories. It stays visible with its location; Concordia never attaches it to a service by name.
type UnboundWorkload struct {
	Images           []Entity `json:"images"`
	BuildDefinitions []Entity `json:"buildDefinitions"`
	Workload         Entity   `json:"workload"`
}

// CoverageState is the `deployment-workload-set` record of an environment.
type CoverageState struct {
	Status    observation.CoverageStatus `json:"status"`
	LossModes []string                   `json:"lossModes"`
	Reasons   []string                   `json:"reasons"`
}

// EnvironmentTrace is the trace of one declared environment.
type EnvironmentTrace struct {
	// Evaluated is the state of the level `rendered-or-evaluated` in this environment: the rendered sources and plans
	// that hold evidence. Left out when the environment has none, so a trace without assurance evidence is unchanged.
	Evaluated        *EvaluatedState   `json:"evaluated,omitempty"`
	Name             string            `json:"name"`
	Cluster          string            `json:"cluster,omitempty"`
	Coverage         CoverageState     `json:"coverage"`
	Subjects         []SubjectTrace    `json:"subjects"`
	UnboundWorkloads []UnboundWorkload `json:"unboundWorkloads"`
	// Actions are the deployment actions whose location belongs to the environment sources: the tool, the location,
	// and the file or chart that the action names. An action is never a placement.
	Actions []Entity `json:"actions"`
}

// UnresolvedWorkload is a workload whose environment is `unresolved`. It takes part in no environment trace.
type UnresolvedWorkload struct {
	Identity  string     `json:"identity"`
	Kind      string     `json:"kind"`
	Locations []Location `json:"locations"`
}

// Summary counts the traces of the section. Every subject counts, `unknown` and `unresolved` ones too.
type Summary struct {
	ByStatus map[Status]int `json:"byStatus"`
	// Subjects is the number of subject traces: one for each subject and environment.
	Subjects     int `json:"subjects"`
	Environments int `json:"environments"`
}

// Trace is the `deploymentTrace` section of `results.json`.
type Trace struct {
	Status                         string               `json:"status"`
	Levels                         []LevelState         `json:"levels"`
	Environments                   []EnvironmentTrace   `json:"environments"`
	UnresolvedEnvironmentWorkloads []UnresolvedWorkload `json:"unresolvedEnvironmentWorkloads"`
	// UnresolvedEnvironmentActions are the deployment actions whose location is in no environment source. They stay
	// visible with their tool, location, and target; no environment trace uses them.
	UnresolvedEnvironmentActions []Entity `json:"unresolvedEnvironmentActions"`
	Summary                      Summary  `json:"summary"`
}

// Configured says whether the workspace declares an environment.
func (t *Trace) Configured() bool { return t.Status == StatusConfigured }

// Subject finds the trace of a subject in an environment.
func (t *Trace) Subject(architectureID, environment string) *SubjectTrace {
	for i := range t.Environments {
		env := &t.Environments[i]
		if env.Name != environment {
			continue
		}

		for j := range env.Subjects {
			if env.Subjects[j].ArchitectureID == architectureID {
				return &env.Subjects[j]
			}
		}
	}

	return nil
}

// The property keys of an entity. They are the allow-list: see AllowedProperties.
const (
	propAPIGroup         = "apiGroup"
	propBindingKind      = "bindingKind"
	propBuilder          = "builder"
	propContainer        = "container"
	propDigest           = "digest"
	propKind             = "kind"
	propName             = "name"
	propNamespace        = "namespace"
	propNodeType         = "nodeType"
	propReference        = "reference"
	propRegistry         = "registry"
	propRepository       = "repository"
	propSourceRepository = "sourceRepository"
	propStage            = "stage"
	propTag              = "tag"
	propTarget           = "target"
	propTargetKind       = "targetKind"
	propTemplated        = "templated"
	propTool             = "tool"
	propTrustContract    = "trustContract"
)
