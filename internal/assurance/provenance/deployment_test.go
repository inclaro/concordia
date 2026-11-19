package provenance_test

import (
	"slices"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/provenance"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/deployment"
)

// recordSpec describes one deployment record or workload observation.
type recordSpec struct {
	path         string
	environment  string
	workload     string
	container    string
	digest       string
	subject      string
	outcome      string
	at           time.Time
	verification statement.Verification
}

func (s *recordSpec) fill() {
	if s.environment == "" {
		s.environment = "production"
	}

	if s.workload == "" {
		s.workload = "api"
	}

	if s.container == "" {
		s.container = "api"
	}

	if s.digest == "" {
		s.digest = digestA
	}

	if s.subject == "" {
		s.subject = s.digest
	}

	if s.at.IsZero() {
		s.at = now.Add(-time.Hour)
	}

	if s.verification.Signature == "" {
		s.verification = verified("ci-build")
	}
}

func placement(spec *recordSpec) map[string]any {
	return map[string]any{
		"environment": spec.environment, "cluster": "prod-eu",
		"workload":  map[string]string{"namespace": "shop", "kind": "Deployment", "name": spec.workload},
		"container": spec.container, "digest": spec.digest, "time": spec.at.Format(time.RFC3339),
	}
}

func record(t *testing.T, spec *recordSpec) provenance.Record {
	t.Helper()

	spec.fill()
	predicate := placement(spec)
	predicate["outcome"] = spec.outcome
	predicate["deployer"] = "https://github.com/acme/shop/.github/workflows/deploy.yml"

	if spec.outcome == "" {
		predicate["outcome"] = statement.DeploymentSucceeded
	}

	loaded := parse(t, spec.path, spec.subject, statement.PredicateDeploymentRecord, predicate)

	got, err := provenance.NewRecord(&loaded, spec.verification, obligation(), config())
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func observation(t *testing.T, spec *recordSpec) provenance.Observation {
	t.Helper()

	spec.fill()
	loaded := parse(t, spec.path, spec.subject, statement.PredicateWorkloadObservation, placement(spec))

	got, err := provenance.NewObservation(&loaded, spec.verification, obligation(), config())
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// deploymentCase is one case of TestDeployment.
type deploymentCase struct {
	name  string
	image string

	wantSucceeded provenance.Verdict
	succeededWhy  string
	wantDigest    provenance.Verdict
	digestWhy     string
	boundDigest   string
	wantDrift     provenance.Verdict
	driftWhy      string

	records      []recordSpec
	observations []recordSpec
	wantFindings int
}

// longAgo is a time that is too old for any record or observation.
func longAgo() time.Time {
	return now.Add(-48 * time.Hour).Add(-7 * 24 * time.Hour)
}

// noObservationCases are the cases where records decide the outcome and the digest, and no observation exists.
func noObservationCases() []deploymentCase {
	const tagOnly = "registry.example/shop:1.4"

	return []deploymentCase{
		{
			name: "no record: manifests only", image: tagOnly,
			wantSucceeded: provenance.VerdictUnknown, succeededWhy: provenance.ReasonNoEvidenceSupplied,
			wantDigest: provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "a succeeded record binds a digest to a tag", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json"}},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictPass, boundDigest: digestA,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "a failed record is a violation and binds no digest", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json", outcome: statement.DeploymentFailed}},
			wantSucceeded: provenance.VerdictViolation,
			wantDigest:    provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "the newest record decides: a failed one after a succeeded one", image: tagOnly,
			records: []recordSpec{
				{path: "build/records/deploy-1.json", at: now.Add(-3 * time.Hour)},
				{path: "build/records/deploy-2.json", at: now.Add(-time.Hour), outcome: statement.DeploymentFailed},
			},
			wantSucceeded: provenance.VerdictViolation,
			wantDigest:    provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "the newest record decides: a succeeded one after a failed one", image: tagOnly,
			records: []recordSpec{
				{path: "build/records/deploy-1.json", at: now.Add(-3 * time.Hour), outcome: statement.DeploymentFailed},
				{path: "build/records/deploy-2.json", at: now.Add(-time.Hour), digest: digestB},
			},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictPass, boundDigest: digestB,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "records of the same time that name different digests conflict", image: tagOnly,
			records: []recordSpec{
				{path: "build/records/deploy-1.json", digest: digestA},
				{path: "build/records/deploy-2.json", digest: digestB},
			},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictUnknown, digestWhy: provenance.ReasonEvidenceConflict,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
	}
}

// inadmissibleRecordCases are the cases where a record is not used.
func inadmissibleRecordCases() []deploymentCase {
	const tagOnly = "registry.example/shop:1.4"

	return []deploymentCase{
		{
			name: "a record for another environment is not used", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-staging.json", environment: "staging"}},
			wantSucceeded: provenance.VerdictUnknown, succeededWhy: provenance.ReasonNoEvidenceSupplied,
			wantDigest: provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "an unsigned record is inadmissible", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json", verification: unsigned()}},
			wantSucceeded: provenance.VerdictUnknown, succeededWhy: provenance.ReasonNoEvidenceSupplied,
			wantDigest: provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "a record that names another image than its subject is inadmissible", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json", digest: digestA, subject: digestB}},
			wantSucceeded: provenance.VerdictUnknown, succeededWhy: provenance.ReasonNoEvidenceSupplied,
			wantDigest: provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "a record for another workload is not used", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json", workload: "worker"}},
			wantSucceeded: provenance.VerdictUnknown, succeededWhy: provenance.ReasonNoEvidenceSupplied,
			wantDigest: provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "a record for another container is not used", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json", container: "sidecar"}},
			wantSucceeded: provenance.VerdictUnknown, succeededWhy: provenance.ReasonNoEvidenceSupplied,
			wantDigest: provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "a stale record is inadmissible", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json", at: longAgo()}},
			wantSucceeded: provenance.VerdictUnknown, succeededWhy: provenance.ReasonNoEvidenceSupplied,
			wantDigest: provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
	}
}

// renderedDigestCases are the cases where the rendered image names a digest.
func renderedDigestCases() []deploymentCase {
	const pinned = "registry.example/shop@" + digestA

	return []deploymentCase{
		{
			name: "the rendered digest and the record agree", image: pinned,
			records:       []recordSpec{{path: "build/records/deploy-1.json"}},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictPass, boundDigest: digestA,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "the rendered digest and the record differ: no silent choice", image: pinned,
			records:       []recordSpec{{path: "build/records/deploy-1.json", digest: digestB}},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictUnknown, digestWhy: provenance.ReasonEvidenceConflict,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
	}
}

// observationCases are the cases where an observation is compared with the record and the rendered digest.
func observationCases() []deploymentCase {
	const (
		tagOnly = "registry.example/shop:1.4"
		pinned  = "registry.example/shop@" + digestA
	)

	return []deploymentCase{
		{
			name: "observation agrees with the record", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json"}},
			observations:  []recordSpec{{path: "build/records/observed-1.json"}},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictPass, boundDigest: digestA,
			wantDrift: provenance.VerdictPass,
		},
		{
			name: "observation differs from the record: drift", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json", digest: digestA}},
			observations:  []recordSpec{{path: "build/records/observed-1.json", digest: digestB}},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictPass, boundDigest: digestA,
			wantDrift: provenance.VerdictViolation, wantFindings: 1,
		},
		{
			name: "observation differs from the record and from the rendered digest", image: pinned,
			records:       []recordSpec{{path: "build/records/deploy-1.json", digest: digestA}},
			observations:  []recordSpec{{path: "build/records/observed-1.json", digest: digestB}},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictPass, boundDigest: digestA,
			wantDrift: provenance.VerdictViolation, wantFindings: 2,
		},
		{
			name: "observation without a record is compared with the rendered digest", image: pinned,
			observations:  []recordSpec{{path: "build/records/observed-1.json", digest: digestB}},
			wantSucceeded: provenance.VerdictUnknown, succeededWhy: provenance.ReasonNoEvidenceSupplied,
			wantDigest: provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictViolation, wantFindings: 1,
		},
		{
			name: "observation of a tag without a record has nothing to compare", image: tagOnly,
			observations:  []recordSpec{{path: "build/records/observed-1.json", digest: digestB}},
			wantSucceeded: provenance.VerdictUnknown, succeededWhy: provenance.ReasonNoEvidenceSupplied,
			wantDigest: provenance.VerdictUnknown, digestWhy: provenance.ReasonNoEvidenceSupplied,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
	}
}

// inadmissibleObservationCases are the cases where an observation derives no drift, and the case of two observations.
func inadmissibleObservationCases() []deploymentCase {
	const tagOnly = "registry.example/shop:1.4"

	return []deploymentCase{
		{
			name: "a stale observation derives no drift", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json", digest: digestA}},
			observations:  []recordSpec{{path: "build/records/observed-1.json", digest: digestB, at: longAgo()}},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictPass, boundDigest: digestA,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "an unsigned observation derives no drift", image: tagOnly,
			records:       []recordSpec{{path: "build/records/deploy-1.json", digest: digestA}},
			observations:  []recordSpec{{path: "build/records/observed-1.json", digest: digestB, verification: unsigned()}},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictPass, boundDigest: digestA,
			wantDrift: provenance.VerdictUnknown, driftWhy: provenance.ReasonNoEvidenceSupplied,
		},
		{
			name: "the newest observation is the one compared", image: tagOnly,
			records: []recordSpec{{path: "build/records/deploy-1.json", digest: digestA}},
			observations: []recordSpec{
				{path: "build/records/observed-1.json", digest: digestB, at: now.Add(-5 * time.Hour)},
				{path: "build/records/observed-2.json", digest: digestA, at: now.Add(-time.Hour)},
			},
			wantSucceeded: provenance.VerdictPass,
			wantDigest:    provenance.VerdictPass, boundDigest: digestA,
			wantDrift: provenance.VerdictPass,
		},
	}
}

func TestDeployment(t *testing.T) {
	t.Parallel()

	tests := slices.Concat(
		noObservationCases(), inadmissibleRecordCases(), renderedDigestCases(), observationCases(), inadmissibleObservationCases(),
	)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			evidence := evidenceOf(t, &test)
			occ := occurrence(t, test.image)
			got := provenance.Deployment(&occ, &evidence)

			checkDeployment(t, &got, &test)
		})
	}
}

// evidenceOf admits the records and the observations of the case.
func evidenceOf(t *testing.T, test *deploymentCase) provenance.Evidence {
	t.Helper()

	var evidence provenance.Evidence

	for i := range test.records {
		evidence.Records = append(evidence.Records, record(t, &test.records[i]))
	}

	for i := range test.observations {
		evidence.Observations = append(evidence.Observations, observation(t, &test.observations[i]))
	}

	return evidence
}

// checkDeployment checks the three outcomes, the findings, and the occurrence of a deployment result.
func checkDeployment(t *testing.T, got *provenance.DeploymentResult, test *deploymentCase) {
	t.Helper()

	checkOutcome(t, "deployment succeeded", &got.DeploymentSucceeded.Outcome,
		outcomeWant{claim: provenance.ClaimDeploymentSucceeded, verdict: test.wantSucceeded, reason: test.succeededWhy})
	checkOutcome(t, "deployed digest", &got.DeployedDigest.Outcome,
		outcomeWant{claim: provenance.ClaimDeployedDigest, verdict: test.wantDigest, reason: test.digestWhy})
	checkOutcome(t, "runtime drift", &got.RuntimeDrift.Outcome,
		outcomeWant{claim: provenance.ClaimRuntimeDrift, verdict: test.wantDrift, reason: test.driftWhy})

	checkBinding(t, &got.DeployedDigest, test)

	if len(got.RuntimeDrift.Findings) != test.wantFindings {
		t.Errorf("findings = %+v, want %d", got.RuntimeDrift.Findings, test.wantFindings)
	}

	if got.Occurrence.Container != "api" || got.Rejected == nil {
		t.Errorf("occurrence = %+v, rejected = %v", got.Occurrence, got.Rejected)
	}
}

// checkBinding checks the bound digest, and for a pass its level and its record.
func checkBinding(t *testing.T, got *provenance.DeployedDigest, test *deploymentCase) {
	t.Helper()

	if got.Digest != test.boundDigest {
		t.Errorf("bound digest = %q, want %q", got.Digest, test.boundDigest)
	}

	if test.wantDigest == provenance.VerdictPass && !bindsAtExecutionLevel(got) {
		t.Errorf("the binding must be at the deployment-execution level and name its record: %+v", got)
	}
}

// bindsAtExecutionLevel says whether the binding is at the deployment-execution level and names its record.
func bindsAtExecutionLevel(got *provenance.DeployedDigest) bool {
	if got.Level != deployment.LevelDeploymentExecution {
		return false
	}

	return got.Statement != nil
}

// outcomeWant is the claim, the verdict, and the reason that an outcome must have.
type outcomeWant struct {
	claim   string
	reason  string
	verdict provenance.Verdict
}

func checkOutcome(t *testing.T, what string, got *provenance.Outcome, want outcomeWant) {
	t.Helper()

	gotOutcome := outcomeWant{claim: got.Claim, reason: got.Reason, verdict: got.Verdict}
	if gotOutcome != want {
		t.Errorf("%s = %q %q (%q), want %q %q (%q)", what, got.Claim, got.Verdict, got.Reason, want.claim, want.verdict, want.reason)
	}

	if got.Detail == "" {
		t.Errorf("%s has no detail", what)
	}
}

func TestSucceededNeedsAnAdmissibleSucceededRecord(t *testing.T) {
	t.Parallel()

	occ := occurrence(t, "registry.example/shop:1.4")

	// Every way a record can be inadmissible leaves the claim `unknown`, never `pass`.
	for name, spec := range map[string]*recordSpec{
		"unsigned": {path: "r.json", verification: unsigned()},
		"signature failed": {path: "r.json", verification: statement.Verification{
			Signature: statement.SignatureFailed, Subject: statement.SubjectUnchecked,
		}},
		"producer without the claim kind": {path: "r.json", verification: verified("checks")},
		"offline without verification": {path: "r.json", verification: statement.Verification{
			Signature: statement.SignatureUnavailable, Subject: statement.SubjectUnchecked,
		}},
		"subject differs": {path: "r.json", verification: statement.Verification{
			Signature: statement.SignatureVerified, ProducerID: "ci-build", Subject: statement.SubjectDiffers,
		}},
		"another environment": {path: "r.json", environment: "staging"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := record(t, spec)
			if rec.Admission.Admissible {
				t.Fatalf("the record is admissible: %+v", rec.Admission)
			}

			got := provenance.Deployment(&occ, &provenance.Evidence{Records: []provenance.Record{rec}})
			if got.DeploymentSucceeded.Verdict == provenance.VerdictPass || got.DeployedDigest.Verdict == provenance.VerdictPass {
				t.Errorf("an inadmissible record supports a claim: %+v", got)
			}
		})
	}
}

func TestRecordForAnotherEnvironmentIsInadmissibleAndListed(t *testing.T) {
	t.Parallel()

	rec := record(t, &recordSpec{path: "build/records/deploy-staging.json", environment: "staging"})
	if !slices.Equal(rec.Admission.Details, []assurance.Detail{assurance.DetailDifferentEnvironment}) {
		t.Fatalf("details = %v", rec.Admission.Details)
	}

	occ := occurrence(t, "registry.example/shop:1.4")

	got := provenance.Deployment(&occ, &provenance.Evidence{Records: []provenance.Record{rec}})
	if len(got.Rejected) != 1 || got.Rejected[0].Path != "build/records/deploy-staging.json" {
		t.Errorf("rejected = %+v", got.Rejected)
	}
}

func TestDriftNamesBothSources(t *testing.T) {
	t.Parallel()

	occ := occurrence(t, "registry.example/shop@"+digestA)
	rec := record(t, &recordSpec{path: "build/records/deploy-1.json", digest: digestA})
	obs := observation(t, &recordSpec{path: "build/records/observed-1.json", digest: digestB})

	got := provenance.Deployment(&occ, &provenance.Evidence{Records: []provenance.Record{rec}, Observations: []provenance.Observation{obs}})

	if got.RuntimeDrift.Verdict != provenance.VerdictViolation || got.RuntimeDrift.Level != deployment.LevelRuntimeObservation {
		t.Fatalf("drift = %+v", got.RuntimeDrift)
	}

	want := []provenance.Drift{
		{
			Observed: provenance.DigestSource{Source: provenance.SourceObservation, Digest: digestB, Statement: &obs.Ref},
			Expected: provenance.DigestSource{Source: provenance.SourceRendered, Digest: digestA},
		},
		{
			Observed: provenance.DigestSource{Source: provenance.SourceObservation, Digest: digestB, Statement: &obs.Ref},
			Expected: provenance.DigestSource{Source: provenance.SourceRecord, Digest: digestA, Statement: &rec.Ref},
		},
	}

	checkDriftFindings(t, got.RuntimeDrift.Findings, want)

	if finding := got.RuntimeDrift.Findings[1]; finding.Expected.Statement == nil || finding.Expected.Statement.Path != "build/records/deploy-1.json" {
		t.Errorf("the record finding does not name the record statement: %+v", finding)
	}

	// The record, the observation, and the rendered digest stay separate: the binding is still the record's.
	if got.DeployedDigest.Verdict != provenance.VerdictPass || got.DeployedDigest.Digest != digestA {
		t.Errorf("deployed digest = %+v", got.DeployedDigest)
	}
}

func TestNewRecordKeepsTheStatementIdentity(t *testing.T) {
	t.Parallel()

	rec := record(t, &recordSpec{path: "build/records/deploy-1.json"})

	wantRef := refKey{path: "build/records/deploy-1.json", producer: "ci-build", integrity: assurance.IntegrityAuthenticated}

	if keyOf(&rec.Ref) != wantRef || !rec.Admission.Admissible {
		t.Errorf("record = %+v", rec)
	}

	loaded := parse(t, "x.json", digestA, statement.PredicatePlan, map[string]any{})

	if _, err := provenance.NewRecord(&loaded, verified("ci-build"), obligation(), config()); err == nil {
		t.Error("a predicate of another type must be an error")
	}

	if _, err := provenance.NewObservation(&loaded, verified("ci-build"), obligation(), config()); err == nil {
		t.Error("a predicate of another type must be an error")
	}
}
