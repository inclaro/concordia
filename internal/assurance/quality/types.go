// Package quality loads quality scenario files (kind QualityScenarioSet) and holds the pure decision logic of the
// three evaluation modes (structural, measured, reviewed), area coverage, findings, and ADR links (design D11, D21,
// D26). The package reads results that the check pipeline has already made (rule verdicts, the currency of the
// review evaluation, the decision states) and never imports internal/conformance, internal/adr, or internal/cli:
// the integrator passes those results in and maps its own types onto the small types declared here.
package quality

// Kind and APIVersion of a scenario file.
const (
	Kind       = "QualityScenarioSet"
	APIVersion = "concordia.io/v1alpha1"
)

// Mode is the evaluation mode of a scenario. A result of one mode never satisfies a scenario of another mode.
type Mode string

// The three modes.
const (
	ModeStructural Mode = "structural"
	ModeMeasured   Mode = "measured"
	ModeReviewed   Mode = "reviewed"
)

// Enforcement says whether a scenario blocks (`mandatory`) or only reports (`advisory`, the default).
type Enforcement string

// The two enforcement values.
const (
	EnforcementMandatory Enforcement = "mandatory"
	EnforcementAdvisory  Enforcement = "advisory"
)

// Comparison is how a measured value is compared with the threshold.
type Comparison string

// The five comparisons.
const (
	CompareLT  Comparison = "lt"
	CompareLTE Comparison = "lte"
	CompareGT  Comparison = "gt"
	CompareGTE Comparison = "gte"
	CompareEQ  Comparison = "eq"
)

// The areas of the report, in report order. A scenario names exactly one; `requiredAreas` of the workspace names
// the ones that need a scenario.
const (
	AreaAvailability     = "availability"
	AreaModifiability    = "modifiability"
	AreaSecurity         = "security"
	AreaPerformance      = "performance"
	AreaDeploymentSafety = "deployment-safety"
	AreaOperability      = "operability"
)

// Areas is the closed list of areas, in report order.
var Areas = []string{
	AreaAvailability, AreaModifiability, AreaSecurity, AreaPerformance, AreaDeploymentSafety, AreaOperability,
}

// FindingType is the type of a finding.
type FindingType string

// The finding types.
const (
	FindingSensitivityPoint FindingType = "sensitivity-point"
	FindingRisk             FindingType = "risk"
	FindingTradeoff         FindingType = "tradeoff"
)

// FindingStatus is the status of a finding. Only `open` counts as an open finding.
type FindingStatus string

// The finding statuses.
const (
	FindingOpen     FindingStatus = "open"
	FindingAccepted FindingStatus = "accepted"
	FindingResolved FindingStatus = "resolved"
)

// Location is a place in a scenario file. Line is 1-based, 0 when unknown.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
}

// ADRRef is one `addressedBy` entry as written, with its place.
type ADRRef struct {
	ID       string   `json:"id"`
	Location Location `json:"location"`
}

// Criterion is the mode-specific criterion of a scenario. Only the fields of the scenario mode are set: Metric,
// Unit, Comparison, Threshold, and MaxAgeDays for a measured scenario, Rules for a structural one, Subject and
// Authority for a reviewed one.
type Criterion struct {
	// Metric, Unit, Comparison, and Threshold are the measured criterion.
	Metric     string     `json:"metric,omitempty"`
	Unit       string     `json:"unit,omitempty"`
	Comparison Comparison `json:"comparison,omitempty"`
	// Subject and Authority are the assessment subject and the reviewer authority of a reviewed scenario.
	Subject   string `json:"subject,omitempty"`
	Authority string `json:"authority,omitempty"`
	// Rules are the policy rules of a structural scenario.
	Rules     []string `json:"rules,omitempty"`
	Threshold float64  `json:"threshold,omitempty"`
	// MaxAgeDays is the optional maximum age of a measurement window end; 0 means the scenario sets none.
	MaxAgeDays int `json:"maxAgeDays,omitempty"`
}

// Scenario is one quality scenario.
type Scenario struct {
	Mode             Mode        `json:"mode"`
	Enforcement      Enforcement `json:"enforcement"`
	QualityAttribute string      `json:"qualityAttribute"`
	StimulusSource   string      `json:"stimulusSource"`
	Stimulus         string      `json:"stimulus"`
	Environment      string      `json:"environment"`
	Area             string      `json:"area"`
	ExpectedResponse string      `json:"expectedResponse"`
	ID               string      `json:"id"`
	Owner            string      `json:"owner"`
	// Scope holds CALM node and relationship ids.
	Scope       []string `json:"scope"`
	AddressedBy []ADRRef `json:"addressedBy"`
	Location    Location `json:"location"`
	// scopeLines and ruleLines are the lines of the scope and rule entries, for the location of an unresolved id.
	scopeLines []int
	ruleLines  []int
	Criterion  Criterion `json:"criterion"`
}

// Finding is a sensitivity point, a risk, or a tradeoff.
type Finding struct {
	ID        string        `json:"id"`
	Type      FindingType   `json:"type"`
	Statement string        `json:"statement"`
	Owner     string        `json:"owner"`
	Status    FindingStatus `json:"status"`
	// Scenarios are the ids of the scenarios that the finding concerns. There is at least one.
	Scenarios []string `json:"scenarios"`
	// Areas are the areas that the finding concerns when they differ from the areas of its scenarios (a tradeoff
	// that lowers `modifiability` while the scenario tests `performance`). Empty means the areas of the scenarios.
	Areas       []string `json:"areas,omitempty"`
	AddressedBy []ADRRef `json:"addressedBy"`
	// Assumptions are assumption ids that the finding depends on.
	Assumptions []string `json:"assumptions"`
	Location    Location `json:"location"`

	scenarioLines []int
}

// File is one loaded scenario file.
type File struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// Set is the loaded scenarios and findings of all files, sorted by id. It holds what loaded cleanly even when the
// load reported errors; a caller that got an error exits with code 2 and does not use it.
type Set struct {
	Scenarios []*Scenario `json:"scenarios"`
	Findings  []*Finding  `json:"findings"`
	Files     []File      `json:"files"`
}

// Scenario gives the scenario with the id, or nil.
func (s *Set) Scenario(id string) *Scenario {
	for _, sc := range s.Scenarios {
		if sc.ID == id {
			return sc
		}
	}

	return nil
}

// Verdict is the verdict of a rule or a scenario. The integrator maps the verdicts of internal/conformance onto it.
type Verdict string

// The verdicts.
const (
	VerdictPass          Verdict = "pass"
	VerdictViolation     Verdict = "violation"
	VerdictUnknown       Verdict = "unknown"
	VerdictNotApplicable Verdict = "not-applicable"
	VerdictError         Verdict = "error"
)

// The ranks of the verdicts for worst-of: error, violation, unknown, pass.
const (
	rankPass = iota
	rankUnknown
	rankViolation
	rankError
)

// severity ranks a verdict for worst-of. not-applicable and a value that this package does not know rank as
// unknown, because they are not evidence for a pass.
func (v Verdict) severity() int {
	switch v {
	case VerdictError:
		return rankError
	case VerdictViolation:
		return rankViolation
	case VerdictPass:
		return rankPass
	case VerdictUnknown, VerdictNotApplicable:
	}

	return rankUnknown
}

// Unknown reasons that this package sets. They are the literal values of the closed set of
// internal/conformance/reasons.go; the integrator converts them.
const (
	ReasonNoEvidenceSupplied       = "no-evidence-supplied"
	ReasonReviewedModeNotAvailable = "reviewed-mode-not-available"
	ReasonCoverageIncomplete       = "coverage-incomplete"
)

// Details that this package sets.
const (
	DetailNoAssessmentsKey          = "the workspace has no assessments key"
	DetailADRGovernanceNotAvailable = "adr-governance-not-available"
)

// Result is the result of one scenario in its own mode. The Mode is the label of the result: a `structural` pass
// is structural evidence and never a behavioral result.
type Result struct {
	Scenario string  `json:"scenario"`
	Mode     Mode    `json:"mode"`
	Verdict  Verdict `json:"verdict"`
	// Reason is the closed unknown reason, set only when the verdict is `unknown`.
	Reason string `json:"reason,omitempty"`
	// Details say why, one sentence or token each, in a fixed order.
	Details []string `json:"details"`
	// Evidence names what the verdict rests on: the assessment ids of a reviewed result. It is empty for a result
	// without evidence.
	Evidence []string `json:"evidence,omitempty"`
}

// Satisfies says whether the result is a pass for the scenario: a result of another mode never satisfies it, so a
// structural pass cannot stand in for a measured or reviewed scenario.
func (r *Result) Satisfies(scenario *Scenario) bool {
	return scenario != nil && r.Scenario == scenario.ID && r.Mode == scenario.Mode && r.Verdict == VerdictPass
}
