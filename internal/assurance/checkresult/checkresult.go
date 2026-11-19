// Package checkresult reads and validates the `check-result` predicate: the statement that a named check had an
// outcome for a revision and an environment (spec verification-results, "Check results have a validated contract").
// It reads fields and reports what is missing. Whether a result is admissible is decided by internal/assurance/admit,
// and the verdict that follows from it by the evaluators of the check-result claim.
package checkresult

import (
	"slices"
	"time"

	"github.com/inclaro/concordia/internal/assurance/statement"
)

// Evidence is one piece of evidence that backs a result, for example a test report, with its digest.
type Evidence struct {
	Digest map[string]string `json:"digest,omitempty"`
	Name   string            `json:"name,omitempty"`
	URI    string            `json:"uri,omitempty"`
}

// Result is a `check-result/v1` predicate.
type Result struct {
	// Check is the check identifier, for example `unit-tests`.
	Check string `json:"check"`
	// Outcome is `passed`, `failed`, `errored`, or `skipped`. InvalidOutcome reports any other value.
	Outcome string `json:"outcome"`
	// Revision is the source revision that the check ran against.
	Revision    string              `json:"revision"`
	Environment string              `json:"environment"`
	Evaluator   statement.Evaluator `json:"evaluator"`
	Evidence    []Evidence          `json:"evidence,omitempty"`
	// Time is when the check ran; a result may give Window instead.
	Time   statement.Time `json:"time"`
	Window struct {
		Start statement.Time `json:"start"`
		End   statement.Time `json:"end"`
	} `json:"window"`
	// Covers lists the control, node, and scenario identifiers that the result covers. nil: the field is missing.
	// A non-nil empty list: the producer states that the result covers nothing. The two stay different.
	Covers []string `json:"covers"`
	// Missing lists the required fields that are absent, in a fixed order.
	Missing []string `json:"-"`
}

// required are the required fields. `time` is satisfied by a `window` with start and end instead.
var required = []string{
	"check", "outcome", "revision", "environment", "evaluator.name", "evaluator.version", "covers",
}

// Parse reads a check-result statement. A predicate of another type or a field of the wrong JSON type is an error
// (the file is malformed). Absent required fields are listed in Result.Missing.
func Parse(st *statement.Statement) (Result, error) {
	var result Result

	fields, err := statement.DecodePredicate(st, statement.PredicateCheckResult, &result)
	if err != nil {
		return Result{}, err //nolint:wrapcheck // the message names the predicate.
	}

	result.Missing = fields.Missing(required)

	if !fields.Has("time") && (!fields.Has("window.start") || !fields.Has("window.end")) {
		result.Missing = append(result.Missing, "time")
	}

	return result, nil
}

// InvalidOutcome says whether the outcome is present and not one of the four values.
func (r *Result) InvalidOutcome() bool {
	return r.Outcome != "" && !slices.Contains(statement.RunOutcomes(), r.Outcome)
}

// CoversDeclared says whether the result states its covered identifiers, an empty list included.
func (r *Result) CoversDeclared() bool { return r.Covers != nil }

// CoversNothing says that the result explicitly covers no identifier.
func (r *Result) CoversNothing() bool { return r.Covers != nil && len(r.Covers) == 0 }

// When is the time the result counts from for its age: the time, else the end of the window.
func (r *Result) When() time.Time {
	if !r.Time.IsZero() {
		return r.Time.Time
	}

	return r.Window.End.Time
}
