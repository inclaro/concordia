package statement

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Predicate field names that several predicates and the persisted shapes share.
const (
	fieldContainer   = "container"
	fieldDigest      = "digest"
	fieldEnvironment = "environment"
	fieldName        = "name"
	fieldOutcome     = "outcome"
	fieldRevision    = "revision"
	fieldTime        = "time"
)

// Run and check outcomes. The check-result predicate and the measurement predicate use the same four values.
const (
	OutcomePassed  = "passed"
	OutcomeFailed  = "failed"
	OutcomeErrored = "errored"
	OutcomeSkipped = "skipped"
)

// Deployment outcomes.
const (
	DeploymentSucceeded = "succeeded"
	DeploymentFailed    = "failed"
)

// RunOutcomes are the valid outcomes of a check or a measurement run.
func RunOutcomes() []string {
	return []string{OutcomePassed, OutcomeFailed, OutcomeErrored, OutcomeSkipped}
}

// Time is a time that reads as RFC 3339. An absent field and an empty string give the zero time, which the callers
// list as a missing field; a text that is not a time makes the statement malformed.
type Time struct{ time.Time }

// UnmarshalJSON reads null and "" as the zero time.
func (t *Time) UnmarshalJSON(data []byte) error {
	var text *string
	if err := json.Unmarshal(data, &text); err != nil {
		return err //nolint:wrapcheck // the decoder gives the position.
	}

	if text == nil || strings.TrimSpace(*text) == "" {
		t.Time = time.Time{}

		return nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, *text)
	if err != nil {
		return err //nolint:wrapcheck // the message names the text.
	}

	t.Time = parsed

	return nil
}

// digestText is the shape of an image or plan digest: `<algorithm>:<hex>`.
var digestText = regexp.MustCompile(`^[a-z0-9]+:[0-9a-fA-F]+$`)

// ValidDigest says whether the text is `<algorithm>:<hex>`.
func ValidDigest(text string) bool { return digestText.MatchString(text) }

// Workload names a Kubernetes workload.
type Workload struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

// Evaluator names the program that produced a result.
type Evaluator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ResourceDescriptor is one resolved dependency of a build: a source repository, a base image, a tool.
type ResourceDescriptor struct {
	Digest map[string]string `json:"digest,omitempty"`
	URI    string            `json:"uri,omitempty"`
	Name   string            `json:"name,omitempty"`
}

// SLSA is a SLSA provenance v1 statement as Concordia keeps it: the artifact (subjects), the build definition, the
// resolved dependencies, the builder identity, the producer verification result. External and internal build
// parameters are not kept (they can hold secrets), and no SLSA level is read, derived, or stored: Concordia makes no
// level claim (spec artifact-provenance, "SLSA data is read and no SLSA level is claimed").
type SLSA struct {
	Subjects             []Subject
	BuildType            string
	ResolvedDependencies []ResourceDescriptor
	BuilderID            string
	InvocationID         string
	StartedOn            time.Time
	FinishedOn           time.Time
	// Verification is how far the statement was verified.
	Verification Verification
	// Missing lists the required fields that are absent.
	Missing []string
}

// slsaWire is the part of the SLSA v1 predicate that Concordia reads.
type slsaWire struct {
	RunDetails struct {
		Metadata struct {
			StartedOn    Time   `json:"startedOn"`
			FinishedOn   Time   `json:"finishedOn"`
			InvocationID string `json:"invocationId"`
		} `json:"metadata"`
		Builder struct {
			ID string `json:"id"`
		} `json:"builder"`
	} `json:"runDetails"`
	BuildDefinition struct {
		BuildType            string               `json:"buildType"`
		ResolvedDependencies []ResourceDescriptor `json:"resolvedDependencies"`
	} `json:"buildDefinition"`
}

// ParseSLSA reads a SLSA provenance v1 statement. verification is how far the statement was verified; it is kept
// as one of the recorded facts.
func ParseSLSA(st *Statement, verification Verification) (SLSA, error) {
	var wire slsaWire

	fields, err := DecodePredicate(st, PredicateSLSAProvenance, &wire)
	if err != nil {
		return SLSA{}, err
	}

	return SLSA{
		Subjects:             slices.Clone(st.Subject),
		BuildType:            wire.BuildDefinition.BuildType,
		ResolvedDependencies: wire.BuildDefinition.ResolvedDependencies,
		BuilderID:            wire.RunDetails.Builder.ID,
		InvocationID:         wire.RunDetails.Metadata.InvocationID,
		StartedOn:            wire.RunDetails.Metadata.StartedOn.Time,
		FinishedOn:           wire.RunDetails.Metadata.FinishedOn.Time,
		Verification:         verification,
		Missing:              fields.Missing([]string{"buildDefinition.buildType", "runDetails.builder.id"}),
	}, nil
}

// Time is the time the build finished, else the time it started, else the zero time.
func (s *SLSA) Time() time.Time {
	if !s.FinishedOn.IsZero() {
		return s.FinishedOn
	}

	return s.StartedOn
}

// DeploymentRecord is a `deployment-record/v1` predicate: one deployment of one container of one workload.
type DeploymentRecord struct {
	Environment string `json:"environment"`
	// Cluster or Context names where the deployment ran. One of them is required.
	Cluster   string   `json:"cluster,omitempty"`
	Context   string   `json:"context,omitempty"`
	Workload  Workload `json:"workload"`
	Container string   `json:"container"`
	Image     string   `json:"image,omitempty"`
	Digest    string   `json:"digest"`
	Outcome   string   `json:"outcome"`
	Time      Time     `json:"time"`
	Deployer  string   `json:"deployer"`
	Missing   []string `json:"-"`
}

// ParseDeploymentRecord reads a deployment record. Missing lists the absent required fields; an outcome other than
// `succeeded` or `failed` is reported by InvalidOutcome.
func ParseDeploymentRecord(st *Statement) (DeploymentRecord, error) {
	var record DeploymentRecord

	fields, err := DecodePredicate(st, PredicateDeploymentRecord, &record)
	if err != nil {
		return DeploymentRecord{}, err
	}

	record.Missing = placementMissing(fields, []string{
		fieldEnvironment, "workload.namespace", "workload.kind", "workload.name", fieldContainer, fieldDigest, fieldOutcome, fieldTime, "deployer",
	}, record.Digest)

	return record, nil
}

// InvalidOutcome says whether the outcome is present and not `succeeded` or `failed`.
func (r *DeploymentRecord) InvalidOutcome() bool {
	return r.Outcome != "" && r.Outcome != DeploymentSucceeded && r.Outcome != DeploymentFailed
}

// WorkloadObservation is a `workload-observation/v1` predicate: one container of one workload seen running.
type WorkloadObservation struct {
	Environment string   `json:"environment"`
	Cluster     string   `json:"cluster,omitempty"`
	Context     string   `json:"context,omitempty"`
	Workload    Workload `json:"workload"`
	Container   string   `json:"container"`
	Digest      string   `json:"digest"`
	// Time is when the container was observed.
	Time Time `json:"time"`
	// Observer names the collector, if the producer states it.
	Observer string   `json:"observer,omitempty"`
	Missing  []string `json:"-"`
}

// ParseWorkloadObservation reads a workload observation.
func ParseWorkloadObservation(st *Statement) (WorkloadObservation, error) {
	var observation WorkloadObservation

	fields, err := DecodePredicate(st, PredicateWorkloadObservation, &observation)
	if err != nil {
		return WorkloadObservation{}, err
	}

	observation.Missing = placementMissing(fields, []string{
		fieldEnvironment, "workload.namespace", "workload.kind", "workload.name", fieldContainer, fieldDigest, fieldTime,
	}, observation.Digest)

	return observation, nil
}

// placementMissing lists the absent required fields of a deployment record or an observation: the listed paths, a
// cluster or a context, and a well-formed digest (a digest that is not `<algorithm>:<hex>` counts as missing).
func placementMissing(fields Fields, required []string, digest string) []string {
	out := fields.Missing(required)

	if !fields.Has("cluster") && !fields.Has("context") {
		out = append(out, "cluster")
	}

	if fields.Has(fieldDigest) && !ValidDigest(digest) && !slices.Contains(out, fieldDigest) {
		out = append(out, fieldDigest)
	}

	return out
}

// Measurement is a `measurement/v1` predicate (spec quality-evaluation, "Measurements have a validated contract").
// The producer's threshold and unit are only stated here; the quality evaluator compares them with the scenario and
// compares the value itself. Concordia never uses a verdict from the producer.
type Measurement struct {
	Window struct {
		Start Time `json:"start"`
		End   Time `json:"end"`
	} `json:"window"`
	Evaluator      Evaluator       `json:"evaluator"`
	Unit           string          `json:"unit"`
	Scenario       string          `json:"scenario"`
	Comparison     string          `json:"comparison"`
	Revision       string          `json:"revision"`
	Environment    string          `json:"environment"`
	Outcome        string          `json:"outcome"`
	Metric         string          `json:"metric"`
	TestConditions json.RawMessage `json:"testConditions,omitempty"`
	Missing        []string        `json:"-"`
	Threshold      float64         `json:"threshold"`
	Value          float64         `json:"value"`
}

// ParseMeasurement reads a measurement.
func ParseMeasurement(st *Statement) (Measurement, error) {
	var measurement Measurement

	fields, err := DecodePredicate(st, PredicateMeasurement, &measurement)
	if err != nil {
		return Measurement{}, err
	}

	measurement.Missing = fields.Missing([]string{
		"scenario", "metric", "value", "unit", "threshold", "comparison", "testConditions", fieldRevision, fieldEnvironment,
		"evaluator.name", "evaluator.version", fieldOutcome, "window.start", "window.end",
	})

	return measurement, nil
}

// InvalidOutcome says whether the run outcome is present and not one of RunOutcomes.
func (m *Measurement) InvalidOutcome() bool {
	return m.Outcome != "" && !slices.Contains(RunOutcomes(), m.Outcome)
}

// FailedRun says whether the run did not pass: the value of a failed, errored, or skipped run is not a measurement.
func (m *Measurement) FailedRun() bool {
	return m.Outcome != "" && m.Outcome != OutcomePassed && !m.InvalidOutcome()
}

// Time is the end of the measurement window, which is the time that the maximum age counts from.
func (m *Measurement) Time() time.Time { return m.Window.End.Time }

// Plan is a `plan/v1` predicate: the binding of a Terraform or OpenTofu plan file to a revision and an environment.
// The statement subject is the plan file digest; Concordia compares it with the file.
type Plan struct {
	Environment string `json:"environment"`
	Revision    string `json:"revision"`
	// Tool is `terraform` or `opentofu`, when the producer states it.
	Tool string `json:"tool,omitempty"`
	// Time is when the plan was made.
	Time    Time     `json:"time"`
	Missing []string `json:"-"`
}

// ParsePlan reads a plan binding.
func ParsePlan(st *Statement) (Plan, error) {
	var plan Plan

	fields, err := DecodePredicate(st, PredicatePlan, &plan)
	if err != nil {
		return Plan{}, err
	}

	plan.Missing = fields.Missing([]string{fieldEnvironment, fieldRevision, fieldTime})

	return plan, nil
}
