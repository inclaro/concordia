// Package admit is the admissibility engine (design D6): it decides whether one externally produced statement can
// support one claim, and says why not. Plans, provenance, deployment records, observations, check results, and
// measurements all go through the same function, so the rules cannot drift apart.
//
// Every reason is collected, not only the first one. The evaluation time comes from the Context, which the run takes
// once at its start and records in the manifest; the engine never reads the clock, so a second evaluation from the
// bundle gives the same answer.
package admit

import (
	"slices"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/checkresult"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/workspace"
)

// Facts are the things the engine needs from a statement besides its verification. An empty field is not checked:
// a statement kind that has no revision does not fail for a missing revision; the required fields of a kind are
// listed in Missing by its reader.
type Facts struct {
	Time           time.Time
	Environment    string
	Revision       string
	Missing        []string
	Extra          []assurance.Detail
	FailedRun      bool
	InvalidOutcome bool
}

// Input is one admissibility question.
type Input struct {
	Verification statement.Verification
	Kind         string
	Context      assurance.Context
	Producers    []workspace.Producer
	Facts        Facts
	MaxAge       time.Duration
}

// Admit decides whether the statement is admissible and collects every reason it is not.
func Admit(input *Input) assurance.Admission {
	var details []assurance.Detail

	details = append(details, signatureDetails(input)...)

	if input.Verification.Subject == statement.SubjectDiffers {
		details = append(details, assurance.DetailIntegrityFailure)
	}

	for _, name := range input.Facts.Missing {
		details = append(details, assurance.MissingField(name))
	}

	details = append(details, contextDetails(input)...)

	if input.Facts.FailedRun {
		details = append(details, assurance.DetailFailedRun)
	}

	if input.Facts.InvalidOutcome {
		details = append(details, assurance.DetailInvalidOutcome)
	}

	details = append(details, input.Facts.Extra...)

	return assurance.NewAdmission(integrity(input.Verification), details)
}

// integrity is how far the statement is verified: `producer authenticated` needs a verified signature and a subject
// that was not found different; `content verified by digest` needs a subject that matched and no verified signature.
func integrity(v statement.Verification) assurance.Integrity {
	switch {
	case v.Signature == statement.SignatureVerified && v.Subject != statement.SubjectDiffers:
		return assurance.IntegrityAuthenticated
	case v.Subject == statement.SubjectMatches:
		return assurance.IntegrityDigest
	}

	return assurance.IntegrityNotVerified
}

// signatureDetails are the reasons that come from the signature and the trust contract.
func signatureDetails(input *Input) []assurance.Detail {
	switch input.Verification.Signature {
	case statement.SignatureNone:
		return []assurance.Detail{assurance.DetailNotAuthenticated}
	case statement.SignatureVerified:
		index := slices.IndexFunc(input.Producers, func(p workspace.Producer) bool { return p.ID == input.Verification.ProducerID })

		switch {
		case index < 0:
			return []assurance.Detail{assurance.DetailProducerNotTrusted}
		case !slices.Contains(input.Producers[index].Claims, input.Kind):
			return []assurance.Detail{assurance.DetailProducerNotTrustedForClaim}
		}

		return nil
	case statement.SignatureNoProducer:
		return []assurance.Detail{assurance.DetailProducerNotTrusted}
	case statement.SignatureUnavailable:
		return []assurance.Detail{assurance.DetailOfflineVerificationUnavailable}
	case statement.SignatureFailed:
		return []assurance.Detail{assurance.DetailIntegrityFailure}
	}

	// An unknown state is treated as no signature.
	return []assurance.Detail{assurance.DetailNotAuthenticated}
}

// contextDetails are the reasons that come from comparing the statement with the obligation.
func contextDetails(input *Input) []assurance.Detail {
	var details []assurance.Detail

	facts, ctx := input.Facts, input.Context

	if facts.Environment != "" && ctx.Environment != "" && facts.Environment != ctx.Environment {
		details = append(details, assurance.DetailDifferentEnvironment)
	}

	if facts.Revision != "" {
		if ctx.Revision != "" && facts.Revision != ctx.Revision {
			details = append(details, assurance.DetailDifferentRevision)
		}

		if ctx.DirtyTree {
			details = append(details, assurance.DetailSubjectNotEvaluatedState)
		}
	}

	if input.MaxAge > 0 {
		switch {
		case facts.Time.IsZero():
			details = append(details, assurance.MissingField("time"))
		case !ctx.Now.IsZero() && ctx.Now.Sub(facts.Time) > input.MaxAge:
			details = append(details, assurance.DetailStale)
		}
	}

	return details
}

// SLSAFacts are the facts of a SLSA provenance statement. It has no environment and no revision: the lineage
// evaluator links it to an occurrence by the subject digest.
func SLSAFacts(s *statement.SLSA) Facts {
	return Facts{Missing: s.Missing, Time: s.Time()}
}

// DeploymentRecordFacts are the facts of a deployment record.
func DeploymentRecordFacts(r *statement.DeploymentRecord) Facts {
	return Facts{Missing: r.Missing, Environment: r.Environment, Time: r.Time.Time, InvalidOutcome: r.InvalidOutcome()}
}

// WorkloadObservationFacts are the facts of a workload observation.
func WorkloadObservationFacts(o *statement.WorkloadObservation) Facts {
	return Facts{Missing: o.Missing, Environment: o.Environment, Time: o.Time.Time}
}

// MeasurementFacts are the facts of a measurement. Threshold and unit are compared with the scenario by the caller,
// who adds the details through Facts.Extra.
func MeasurementFacts(m *statement.Measurement) Facts {
	return Facts{
		Missing: m.Missing, Environment: m.Environment, Revision: m.Revision, Time: m.Time(),
		FailedRun: m.FailedRun(), InvalidOutcome: m.InvalidOutcome(),
	}
}

// PlanFacts are the facts of a plan binding.
func PlanFacts(p *statement.Plan) Facts {
	return Facts{Missing: p.Missing, Environment: p.Environment, Revision: p.Revision, Time: p.Time.Time}
}

// CheckResultFacts are the facts of a check result. A failed or errored result is admissible: the verdict maps it
// to a violation, so it is not a failed run in the sense of a measurement.
func CheckResultFacts(r *checkresult.Result) Facts {
	return Facts{
		Missing: r.Missing, Environment: r.Environment, Revision: r.Revision, Time: r.When(),
		InvalidOutcome: r.InvalidOutcome(),
	}
}
