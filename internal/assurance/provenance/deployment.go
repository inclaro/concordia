package provenance

import (
	"slices"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/admit"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/workspace"
)

// Record is a deployment record statement together with the outcome of verifying and admitting it.
type Record struct {
	Ref
	Record    statement.DeploymentRecord `json:"-"`
	Admission assurance.Admission        `json:"admission"`
}

// Observation is a workload observation statement together with the outcome of verifying and admitting it.
type Observation struct {
	Ref
	Observation statement.WorkloadObservation `json:"-"`
	Admission   assurance.Admission           `json:"admission"`
}

// NewRecord reads a loaded deployment record and admits it for the claim kind `deployment-record`. The environment
// of ctx is the environment of the obligation: a record for another one is inadmissible with `different-environment`.
// The subject of the statement must name the digest of the record; if it does not, the statement is inadmissible
// with `integrity-failure` (a statement that names one image and states another is not trustworthy). The maximum age
// is `assurance.records.maxAgeDays`.
func NewRecord(loaded *statement.Loaded, verification statement.Verification, ctx assurance.Context, cfg *workspace.Assurance) (Record, error) {
	parsed, err := statement.ParseDeploymentRecord(&loaded.Statement)
	if err != nil {
		return Record{}, err //nolint:wrapcheck // the reader names the predicate type that is wrong.
	}

	req := admissionRequest{loaded: loaded, verification: verification, ctx: ctx, cfg: cfg}
	facts := admit.DeploymentRecordFacts(&parsed)
	ref, admission := req.admit(workspace.ProducerClaimDeploymentRecord, &facts, parsed.Digest)

	return Record{Ref: ref, Record: parsed, Admission: admission}, nil
}

// NewObservation reads a loaded workload observation and admits it for the claim kind `workload-observation`, with
// the same subject check as NewRecord. An observation older than `assurance.records.maxAgeDays` is inadmissible with
// `stale`, so no drift is derived from it.
func NewObservation(loaded *statement.Loaded, verification statement.Verification, ctx assurance.Context, cfg *workspace.Assurance) (Observation, error) {
	parsed, err := statement.ParseWorkloadObservation(&loaded.Statement)
	if err != nil {
		return Observation{}, err //nolint:wrapcheck // the reader names the predicate type that is wrong.
	}

	req := admissionRequest{loaded: loaded, verification: verification, ctx: ctx, cfg: cfg}
	facts := admit.WorkloadObservationFacts(&parsed)
	ref, admission := req.admit(workspace.ProducerClaimWorkloadObservation, &facts, parsed.Digest)

	return Observation{Ref: ref, Observation: parsed, Admission: admission}, nil
}

// admissionRequest is a loaded statement with what admitting it needs besides its own facts.
type admissionRequest struct {
	loaded       *statement.Loaded
	cfg          *workspace.Assurance
	verification statement.Verification
	ctx          assurance.Context
}

// admit admits the statement for the claim kind with the facts of its predicate. stated is the digest the predicate
// states; the subject of the statement must name it.
func (q *admissionRequest) admit(kind string, facts *admit.Facts, stated string) (Ref, assurance.Admission) {
	facts.Extra = append(facts.Extra, subjectDetails(&q.loaded.Statement, stated)...)

	admission := admit.Admit(&admit.Input{
		Verification: q.verification, Kind: kind, Context: q.ctx,
		Producers: producersOf(q.cfg), Facts: *facts, MaxAge: recordsAge(q.cfg),
	})

	ref := Ref{Path: q.loaded.Path, Digest: q.loaded.Digest, Producer: q.verification.ProducerID, Integrity: admission.Integrity}

	return ref, admission
}

func recordsAge(cfg *workspace.Assurance) time.Duration {
	if cfg == nil || cfg.Records == nil {
		return 0
	}

	return days(cfg.Records.MaxAgeDays)
}

// subjectDetails is `integrity-failure` when the statement names no subject with the digest the predicate states
// (statement.Contradicts); the evidence loader applies the same check.
func subjectDetails(st *statement.Statement, stated string) []assurance.Detail {
	if !st.Contradicts(stated) {
		return nil
	}

	return []assurance.Detail{assurance.DetailIntegrityFailure}
}

// Evidence is the admitted deployment records and workload observations of a run.
type Evidence struct {
	Records      []Record
	Observations []Observation
}

// Outcome is the verdict of one claim.
type Outcome struct {
	Claim   string  `json:"claim"`
	Verdict Verdict `json:"verdict"`
	// Reason is the unknown reason when the verdict is `unknown`.
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail"`
}

// DeployedDigest is the `deployed-digest` claim: the digest that an admissible deployment record binds to the
// workload container.
type DeployedDigest struct {
	// Statement is the record that binds the digest.
	Statement *Ref `json:"statement,omitempty"`
	// Digest is the bound digest, empty unless the verdict is `pass`.
	Digest string `json:"digest,omitempty"`
	// Level is the evidence level of the binding when it holds: `deployment-execution`.
	Level string `json:"level,omitempty"`
	Outcome
}

// DeploymentSucceeded is the `deployment-succeeded` claim. It is `pass` only from an admissible record whose outcome
// is `succeeded`, and a `violation` from an admissible record whose outcome is `failed`.
type DeploymentSucceeded struct {
	// Statement is the record that decided the claim.
	Statement *Ref `json:"statement,omitempty"`
	// Time is the time of that record.
	Time time.Time `json:"time,omitzero"`
	Outcome
}

// Digest source kinds, named in a drift finding.
const (
	SourceRendered    = "rendered"
	SourceRecord      = "deployment-record"
	SourceObservation = "workload-observation"
)

// DigestSource is where one digest of a comparison comes from.
type DigestSource struct {
	// Statement is the statement the digest comes from; nil for the rendered digest.
	Statement *Ref `json:"statement,omitempty"`
	// Source is SourceRendered, SourceRecord, or SourceObservation.
	Source string `json:"source"`
	Digest string `json:"digest"`
}

// Drift is one finding: two sources that name different digests for one workload container. Neither is chosen.
type Drift struct {
	// Observed is the admissible workload observation.
	Observed DigestSource `json:"observed"`
	// Expected is what the observation differs from: the deployment record, or the rendered digest.
	Expected DigestSource `json:"expected"`
}

// RuntimeDrift is the `runtime-drift` claim: `pass` when an admissible observation names the same digest as every
// admissible source it is compared with, `violation` when it differs from one, `unknown` without an observation or
// without anything to compare it with.
type RuntimeDrift struct {
	Outcome
	// Level is the evidence level of the observation: `runtime-observation`.
	Level    string  `json:"level,omitempty"`
	Findings []Drift `json:"findings"`
}

// DeploymentResult is the deployment evidence of one container occurrence. Intended placement (the rendered
// digest), deployment records, and observations stay separate facts.
type DeploymentResult struct {
	Occurrence          OccurrenceRef       `json:"occurrence"`
	DeployedDigest      DeployedDigest      `json:"deployedDigest"`
	DeploymentSucceeded DeploymentSucceeded `json:"deploymentSucceeded"`
	RuntimeDrift        RuntimeDrift        `json:"runtimeDrift"`
	// Rejected are the records and observations of this workload container that cannot support a claim, with
	// their reasons (stale, different environment, not authenticated, integrity failure).
	Rejected []Rejected `json:"rejected"`
}

// Deployment evaluates the deployed digest, the deployment outcome, and the drift of one occurrence.
//
// A record or an observation is about the occurrence when it names the same namespace, workload kind, workload name,
// and container, and, when the rendered workload has a cluster and the statement states a cluster or
// context, the same cluster. Only admissible statements of the environment of the occurrence count; the inadmissible
// ones (a different environment among them) are listed in Rejected.
//
// Of the admissible records, the newest decides (those that share the newest time all count, and a failed one among
// them wins); an older record never overrides it. A newest record that failed binds no digest. The same rule
// selects the observations. A digest that records or the rendered image state differently is a conflict, not a
// choice.
func Deployment(occ *render.ContainerOccurrence, evidence *Evidence) DeploymentResult {
	records, rejectedRecords := partitionAbout(evidence.Records, occ, recordParts)
	observations, rejectedObservations := partitionAbout(evidence.Observations, occ, observationParts)

	rejected := append(append([]Rejected{}, rejectedRecords...), rejectedObservations...)
	slices.SortFunc(rejected, func(a, b Rejected) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	latestRecords := newest(records, func(r *Record) (time.Time, string) { return r.Record.Time.Time, r.Path })
	latestObservations := newest(observations, func(o *Observation) (time.Time, string) { return o.Observation.Time.Time, o.Path })

	return DeploymentResult{
		Occurrence:          RefOf(occ),
		DeploymentSucceeded: succeeded(latestRecords, len(rejected)),
		DeployedDigest:      deployedDigest(occ, latestRecords),
		RuntimeDrift:        runtimeDrift(occ, latestRecords, latestObservations),
		Rejected:            rejected,
	}
}
