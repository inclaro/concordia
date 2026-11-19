package conformance

import (
	"slices"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/canonjson"
)

// The results of the view checks (spec view-currency). They are plain data: `internal/views` makes them, this
// package cannot import it (design D1). `results.json` holds them in the additive `views` array; a run without
// declared views has no `views` key, so its result is the one of before.

// AssessmentCurrencyNotEvaluated is the state of the assessment-currency entry of a view that no evaluated
// assessment covers: a run without assessments, or a view whose depicted subject no assessment names (design D26).
const AssessmentCurrencyNotEvaluated = "not-evaluated"

// ViewPresenceFile is the state of one file that a view needs.
type ViewPresenceFile struct {
	Path string `json:"path"`
	// Role is `source`, `output`, `export` or `manifest`.
	Role string `json:"role"`
	// Reason is empty for a committed file with no problem.
	Reason string `json:"reason,omitempty"`
	// Detail explains the reason.
	Detail string `json:"detail,omitempty"`
	// Committed says that the file is in the tracked revision.
	Committed bool `json:"committed"`
}

// ViewPresence is the presence check of one view.
type ViewPresence struct {
	Status RuleVerdict `json:"status"`
	// Revision is the tracked revision that was listed. Empty when none could be.
	Revision string `json:"revision,omitempty"`
	// Reasons are sorted plain phrases. Never nil.
	Reasons []string `json:"reasons"`
	// Files are the files that the view needs, in declaration order. Never nil.
	Files []ViewPresenceFile `json:"files"`
}

// ViewArtifact is one artifact of a view: the DSL and the exports of a generated view, or the source of an authored one.
type ViewArtifact struct {
	Path   string `json:"path"`
	Format string `json:"format"`
	// Digest is the digest of the committed bytes. Empty when the file is not in the tracked revision.
	Digest string `json:"digest,omitempty"`
	// RegeneratedDigest is the digest of the regenerated canonical bytes. Empty when they could not be made.
	RegeneratedDigest string      `json:"regeneratedDigest,omitempty"`
	Status            RuleVerdict `json:"status"`
	Detail            string      `json:"detail,omitempty"`
	// Reasons are plain phrases. Never nil.
	Reasons []string `json:"reasons"`
}

// ViewFreshness is the freshness check of one view.
type ViewFreshness struct {
	Status RuleVerdict `json:"status"`
	// Verification is `regenerated` when the check recomputed the inputs and regenerated the artifacts. Empty
	// for an authored view and when there was nothing to compare.
	Verification string `json:"verification,omitempty"`
	// Reasons are sorted plain phrases. Never nil.
	Reasons []string `json:"reasons"`
	// DifferingDigests names the input digests that differ from the manifest. Never nil.
	DifferingDigests []string `json:"differingDigests"`
	// ManifestDifferences names the other fields of the manifest that differ. Never nil.
	ManifestDifferences []string `json:"manifestDifferences"`
	// Artifacts are never nil.
	Artifacts []ViewArtifact `json:"artifacts"`
}

// ViewLocation is a place in a repository file. Line is 0 (left out in JSON) when unknown.
type ViewLocation struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

// ViewDifference is one difference between the diagram and a projection.
type ViewDifference struct {
	// Kind is `missing`, `extra`, `incorrect` or `unmapped`.
	Kind string `json:"kind"`
	// Subject is `element` or `relationship`.
	Subject string `json:"subject"`
	// Basis is `declared` or `observed` for `missing` and `incorrect`.
	Basis  string `json:"basis,omitempty"`
	From   string `json:"from"`
	To     string `json:"to,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Locations are the places in the diagram that show the subject. Never nil.
	Locations []ViewLocation `json:"locations"`
}

// ViewUnknownReason is one reason that the correspondence has no definite answer for a subject.
type ViewUnknownReason struct {
	Reason     string   `json:"reason"`
	Subject    string   `json:"subject,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	// Locations are never nil.
	Locations []ViewLocation `json:"locations"`
}

// ViewLostCoverage is a necessary subject that the observed projection cannot show.
type ViewLostCoverage struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
}

// ViewCorrespondence is the correspondence check of one view.
type ViewCorrespondence struct {
	Status RuleVerdict `json:"status"`
	// Method is `generated-source`, `structured-source`, `supplemental-mapping`, `reviewed` or `presence-only`.
	Method string `json:"method"`
	// Basis is `regenerated-projection` or `committed-artifact` for a generated view.
	Basis      string       `json:"basis,omitempty"`
	NextAction string       `json:"nextAction,omitempty"`
	Source     ViewLocation `json:"source"`
	// Section and QualifiedName name the diagram block of a structured source.
	Section       string `json:"section,omitempty"`
	QualifiedName string `json:"qualifiedName,omitempty"`
	// Against, Compared and ByConstruction are the projections of the comparison. Never nil.
	Against        []string `json:"against"`
	Compared       []string `json:"compared"`
	ByConstruction []string `json:"byConstruction"`
	// Differences, Unknown, Lost and Reasons are never nil.
	Differences []ViewDifference    `json:"differences"`
	Unknown     []ViewUnknownReason `json:"unknown"`
	Lost        []ViewLostCoverage  `json:"lost"`
	Reasons     []string            `json:"reasons"`
}

// ViewAssessment is one assessment that covers a subject that a view depicts, from assessment.Evaluation.CurrencyFor.
type ViewAssessment struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// State is the currency state: current, review-required, expired, revoked, or unknown.
	State string `json:"state"`
	// Approval is the id of the approval that the currency rests on, empty when none applies.
	Approval string `json:"approval,omitempty"`
	// Reasons are the review-required details, or the one closed reason of an unknown currency. Never nil.
	Reasons []string `json:"reasons"`
	Version int      `json:"version"`
}

// ViewAssessmentCurrency is the assessment-currency entry of a view (design D26). Without evaluated assessments, or
// when none covers the depicted subject, the state is `not-evaluated` and no assessment is listed. Otherwise the
// state is the most severe state of the listed assessments, in the order revoked, expired, review-required,
// unknown, current.
type ViewAssessmentCurrency struct {
	State string `json:"state"`
	// Assessments lists the matching assessments, sorted by id. Left out when empty.
	Assessments []ViewAssessment `json:"assessments,omitempty"`
}

// viewCurrencySeverity ranks the states for the aggregate state of a view.
var viewCurrencySeverity = []string{
	string(assessment.CurrencyCurrent), string(assessment.CurrencyUnknown), string(assessment.CurrencyReviewRequired),
	string(assessment.CurrencyExpired), string(assessment.CurrencyRevoked),
}

// ViewAssessmentCurrencyOf gives the assessment-currency entry of a view that depicts the subjects: the matching
// assessments from assessment.Evaluation.CurrencyFor, without a second evaluation. A nil evaluation gives
// `not-evaluated`.
func ViewAssessmentCurrencyOf(ev *assessment.Evaluation, subjects []string) ViewAssessmentCurrency {
	out := ViewAssessmentCurrency{State: AssessmentCurrencyNotEvaluated}

	if ev == nil {
		return out
	}

	seen := map[string]bool{}

	for _, subject := range subjects {
		covering := ev.CurrencyFor(subject)

		for i := range covering {
			current := &covering[i]
			if seen[current.ID] {
				continue
			}

			seen[current.ID] = true

			reasons := slices.Clone(current.Reasons)
			if current.UnknownReason != "" {
				reasons = append(reasons, current.UnknownReason)
			}

			out.Assessments = append(out.Assessments, ViewAssessment{
				ID: current.ID, Version: current.Version, Type: string(current.Type), State: string(current.State),
				Reasons: nonNilStrings(reasons), Approval: current.Approval,
			})
		}
	}

	if len(out.Assessments) == 0 {
		return out
	}

	slices.SortFunc(out.Assessments, func(a, b ViewAssessment) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	worst := 0
	for i := range out.Assessments {
		worst = max(worst, slices.Index(viewCurrencySeverity, out.Assessments[i].State))
	}

	out.State = viewCurrencySeverity[worst]

	return out
}

// ViewDepicts is the depicted subject of a view: the sorted CALM element and relationship IDs.
type ViewDepicts struct {
	Elements      []string `json:"elements"`
	Relationships []string `json:"relationships"`
}

// ViewGeneratedFrom is the `GENERATED_FROM` link of a generated artifact (spec "Artifact is linked to its view
// snapshot"): the artifact, its digest, the semantic digest and the manifest that name the view snapshot.
type ViewGeneratedFrom struct {
	ArtifactPath   string `json:"artifactPath"`
	ArtifactDigest string `json:"artifactDigest,omitempty"`
	// SemanticDigest is the semantic digest of the snapshot that the committed manifest names.
	SemanticDigest string `json:"semanticDigest,omitempty"`
	ManifestPath   string `json:"manifestPath"`
}

// ViewResult is the result of the four checks of one declared view. `results.json` sorts them by view ID.
type ViewResult struct {
	// Environment is the declared environment of a deployment view; null for the other views.
	Environment *string `json:"environment"`
	// GeneratedFrom is set for a generated view; null for an authored one.
	GeneratedFrom *ViewGeneratedFrom `json:"generatedFrom"`
	// SemanticDigest is the semantic digest of the view snapshot of this run; null when it could not be computed.
	SemanticDigest *string `json:"semanticDigest"`
	ID             string  `json:"id"`
	Kind           string  `json:"kind"`
	Origin         string  `json:"origin"`
	Owner          string  `json:"owner"`
	Abstraction    string  `json:"abstraction"`
	// Severity is `comparison.severity` of the view: the severity of its findings.
	Severity string `json:"severity"`
	// NextAction is the next-action category of the worst non-pass check. Empty when every check passes.
	NextAction string `json:"nextAction,omitempty"`
	// DigestContract and ProjectionContract are the versions of the digest algorithm and of the projection contract.
	DigestContract     string                 `json:"digestContract"`
	ProjectionContract string                 `json:"projectionContract"`
	Presence           ViewPresence           `json:"presence"`
	Freshness          ViewFreshness          `json:"freshness"`
	Correspondence     ViewCorrespondence     `json:"correspondence"`
	Assessment         ViewAssessmentCurrency `json:"assessmentCurrency"`
	Depicts            ViewDepicts            `json:"depicts"`
	// Scope is never nil.
	Scope []string `json:"scope"`
	// ContractChanged says that the committed manifest has other contract versions than this Concordia.
	ContractChanged bool `json:"contractChanged"`
}

// Verdicts gives the verdicts of the three checks of the view: presence, freshness, correspondence.
func (v *ViewResult) Verdicts() [3]RuleVerdict {
	return [3]RuleVerdict{v.Presence.Status, v.Freshness.Status, v.Correspondence.Status}
}

// SortViews sorts the results by view ID.
func SortViews(views []ViewResult) {
	slices.SortStableFunc(views, func(a, b ViewResult) int { return canonjson.CompareUTF16(a.ID, b.ID) })
}

// AreaOfViews gives the result of the `diagramCurrency` area: the worst verdict of the presence, freshness and
// correspondence checks of all declared views, and the counts of those check results by verdict. `views` is the
// number of views and `findings` the number of view findings. Without views the area is not configured, never `pass`.
func AreaOfViews(views []ViewResult, findings []Finding) AreaResult {
	if len(views) == 0 {
		return AreaResult{Verdict: AreaNotConfigured, Counts: map[string]int{}}
	}

	var (
		counts   RuleCounts
		verdicts []RuleVerdict
	)

	for i := range views {
		for _, verdict := range views[i].Verdicts() {
			verdicts = append(verdicts, verdict)

			switch verdict {
			case VerdictPass:
				counts.Pass++
			case VerdictViolation:
				counts.Violation++
			case VerdictUnknown:
				counts.Unknown++
			case VerdictNotApplicable:
				counts.NotApplicable++
			case VerdictError:
				counts.Error++
			}
		}
	}

	count := 0

	for i := range findings {
		if findings[i].Area == AreaDiagramCurrency {
			count++
		}
	}

	return AreaResult{
		Verdict: AreaVerdict(WorstVerdict(verdicts)),
		Counts: map[string]int{
			"views": len(views), "pass": counts.Pass, "violation": counts.Violation, string(VerdictUnknown): counts.Unknown,
			"notApplicable": counts.NotApplicable, "error": counts.Error, "findings": count,
		},
	}
}
