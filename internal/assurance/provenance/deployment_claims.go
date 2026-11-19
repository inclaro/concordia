package provenance

import (
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/deployment"
)

// succeeded decides `deployment succeeded` from the newest admissible records.
func succeeded(latest []*Record, rejected int) DeploymentSucceeded {
	out := DeploymentSucceeded{Outcome: Outcome{Claim: ClaimDeploymentSucceeded, Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied}}

	if len(latest) == 0 {
		out.Detail = "no admissible deployment record for this workload container"
		if rejected > 0 {
			out.Detail += " (" + plural(rejected) + " not admissible)"
		}

		return out
	}

	if rec := firstWithOutcome(latest, statement.DeploymentFailed); rec != nil {
		out.decide(VerdictViolation, "the deployment record "+rec.Path+" has outcome failed", rec)

		return out
	}

	if rec := firstWithOutcome(latest, statement.DeploymentSucceeded); rec != nil {
		out.decide(VerdictPass, "the admissible deployment record "+rec.Path+" has outcome succeeded", rec)

		return out
	}

	out.Detail = "the newest deployment record has no outcome that Concordia reads as succeeded or failed"

	return out
}

// decide settles the claim with the record that decided it.
func (d *DeploymentSucceeded) decide(verdict Verdict, detail string, rec *Record) {
	d.Verdict, d.Reason, d.Detail = verdict, "", detail
	d.Statement, d.Time = recordRef(rec), rec.Record.Time.Time
}

func firstWithOutcome(records []*Record, outcome string) *Record {
	for _, rec := range records {
		if rec.Record.Outcome == outcome {
			return rec
		}
	}

	return nil
}

// deployedDigest binds the digest of the newest admissible succeeded record to the occurrence.
func deployedDigest(occ *render.ContainerOccurrence, latest []*Record) DeployedDigest {
	out := DeployedDigest{Outcome: Outcome{Claim: ClaimDeployedDigest, Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied}}

	if len(latest) == 0 {
		out.Detail = "no admissible deployment record binds a digest to this workload container"

		return out
	}

	if slices.ContainsFunc(latest, func(r *Record) bool { return r.Record.Outcome != statement.DeploymentSucceeded }) {
		out.Detail = "the newest deployment record did not succeed, so it binds no deployed digest"

		return out
	}

	digests := distinctDigests(latest, func(r *Record) string { return r.Record.Digest })
	if len(digests) > 1 {
		out.conflict("deployment records of the same time name different digests: " + strings.Join(digests, ", "))

		return out
	}

	bound := latest[0].Record.Digest

	if occ.Digest != "" && !strings.EqualFold(occ.Digest, bound) {
		out.conflict("the rendered image names " + occ.Digest + " and the deployment record " + latest[0].Path + " names " + bound)

		return out
	}

	out.Verdict, out.Reason = VerdictPass, ""
	out.Digest, out.Level, out.Statement = bound, deployment.LevelDeploymentExecution, recordRef(latest[0])
	out.Detail = "the admissible deployment record " + latest[0].Path + " binds " + bound + " to the workload container"

	return out
}

func (d *DeployedDigest) conflict(detail string) {
	d.Reason = ReasonEvidenceConflict
	d.Detail = detail
}

// runtimeDrift compares the newest admissible observations with the newest admissible succeeded records and the
// rendered digest. It reports every difference with both sources and chooses none.
func runtimeDrift(occ *render.ContainerOccurrence, records []*Record, observations []*Observation) RuntimeDrift {
	out := RuntimeDrift{
		Outcome:  Outcome{Claim: ClaimRuntimeDrift, Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied},
		Findings: []Drift{},
	}

	if len(observations) == 0 {
		out.Detail = "no admissible workload observation for this workload container"

		return out
	}

	expected := expectedSources(occ, records)
	if len(expected) == 0 {
		out.Detail = "an admissible workload observation exists, but there is no admissible deployment record or rendered digest to compare it with"

		return out
	}

	out.Level = deployment.LevelRuntimeObservation
	out.Findings = driftFindings(observations, expected)
	out.Verdict, out.Reason = VerdictPass, ""

	if len(out.Findings) == 0 {
		out.Detail = "the admissible observation names the same digest as every source it is compared with"

		return out
	}

	first := &out.Findings[0]
	out.Verdict = VerdictViolation
	out.Detail = "drift: " + first.Observed.describe() + " names " + first.Observed.Digest + " but " + first.Expected.describe() +
		" names " + first.Expected.Digest

	return out
}

// expectedSources are the digests an observation is compared with: the rendered one and those of the succeeded
// records.
func expectedSources(occ *render.ContainerOccurrence, records []*Record) []DigestSource {
	var expected []DigestSource

	if occ.Digest != "" {
		expected = append(expected, DigestSource{Source: SourceRendered, Digest: occ.Digest})
	}

	for _, rec := range records {
		if rec.Record.Outcome == statement.DeploymentSucceeded {
			expected = append(expected, DigestSource{Source: SourceRecord, Digest: rec.Record.Digest, Statement: recordRef(rec)})
		}
	}

	return expected
}

// driftFindings pairs every observation with every expected source that names another digest.
func driftFindings(observations []*Observation, expected []DigestSource) []Drift {
	findings := []Drift{}

	for _, obs := range observations {
		seen := DigestSource{Source: SourceObservation, Digest: obs.Observation.Digest, Statement: observationRef(obs)}

		for i := range expected {
			if !strings.EqualFold(seen.Digest, expected[i].Digest) {
				findings = append(findings, Drift{Observed: seen, Expected: expected[i]})
			}
		}
	}

	return findings
}

// describe names the source and, when there is one, the statement file.
func (d *DigestSource) describe() string {
	if d.Statement == nil {
		return d.Source
	}

	return d.Source + " " + d.Statement.Path
}

func distinctDigests[T any](items []*T, digest func(*T) string) []string {
	out := make([]string, 0, len(items))

	for _, item := range items {
		out = append(out, strings.ToLower(digest(item)))
	}

	slices.Sort(out)

	return slices.Compact(out)
}

func recordRef(rec *Record) *Ref {
	ref := rec.Ref

	return &ref
}

func observationRef(obs *Observation) *Ref {
	ref := obs.Ref

	return &ref
}

func plural(n int) string {
	if n == 1 {
		return "1 statement"
	}

	return strconv.Itoa(n) + " statements"
}
