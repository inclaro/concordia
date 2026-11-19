// Package verdict decides whether a check result is admissible and what verdict the results of one check give
// (spec verification-results: "Admissibility is explicit and complete", "Verdicts follow admissibility",
// "Conflicting admissible results are not resolved silently").
//
// It is a subpackage of checkresult because internal/assurance/admit imports checkresult for its facts, so checkresult
// itself cannot call the engine. The verdicts are the strings `pass`, `violation`, and `unknown`, and the unknown
// reasons are the literals of the closed set in internal/conformance/reasons.go: this package does not import
// internal/conformance, and the integrator converts them.
package verdict

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/admit"
	"github.com/inclaro/concordia/internal/assurance/checkresult"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// Verdicts of a check. They have the same spelling as the conformance verdicts.
const (
	Pass      = "pass"
	Violation = "violation"
	Unknown   = "unknown"
)

// Unknown reasons of a check, as spelled in the closed set of internal/conformance/reasons.go.
const (
	ReasonNoEvidenceSupplied = "no-evidence-supplied"
	ReasonEvidenceConflict   = "evidence-conflict"
)

// Details of an unknown verdict.
const (
	// DetailNoAdmissibleResult is the detail the spec gives when no result can support a verdict.
	DetailNoAdmissibleResult = "no admissible result"
	// DetailSkipped says that the admissible results only report that the check was skipped.
	DetailSkipped = "the check was skipped"
)

// MaxAge turns the configured maximum age in days into a duration. 0 means no limit, as in the workspace schema.
func MaxAge(days int) time.Duration {
	return time.Duration(days) * 24 * time.Hour
}

// Input is one check result with what the engine needs to decide on it.
type Input struct {
	Result       *checkresult.Result
	Verification statement.Verification
	// Path is the workspace-relative path of the statement file.
	Path string
	// Digest is the digest of the statement file (`sha256:<hex>`), the evidence of a verdict.
	Digest    string
	Context   assurance.Context
	Producers []workspace.Producer
	// MaxAge is assurance.verification.maxAgeDays as a duration; 0 checks no age.
	MaxAge time.Duration
}

// Admitted is a check result with the decision of the admissibility engine.
type Admitted struct {
	// Result is a copy of the result that was read.
	Result    *checkresult.Result
	Path      string
	Digest    string
	Admission assurance.Admission
}

// Admit decides whether the result is admissible: its producer must be trusted for `check-result` and the signature
// verified, the revision must be the evaluated one, the environment the obligation's, the result not older than the
// maximum age, and the tree must be the evaluated state. Every failed condition is a separate detail.
//
// A result with outcome `failed` or `errored` is admissible; it gives a violation. Only a result that is not
// admissible gives no verdict.
func Admit(input *Input) Admitted {
	admission := admit.Admit(&admit.Input{
		Verification: input.Verification,
		Kind:         workspace.ProducerClaimCheckResult,
		Context:      input.Context,
		Producers:    input.Producers,
		Facts:        admit.CheckResultFacts(input.Result),
		MaxAge:       input.MaxAge,
	})

	copied := *input.Result

	return Admitted{Result: &copied, Admission: admission, Path: input.Path, Digest: input.Digest}
}

// Outcome is the verdict of one check.
type Outcome struct {
	Check   string
	Verdict string
	// Reason is the unknown reason; it is empty unless Verdict is `unknown`.
	Reason string
	// Details say why, in a fixed order: one sentence for the cause of an unknown, then one line for each
	// inadmissible result with its reasons.
	Details []string
	// Evidence is the digests of the statements that the verdict rests on, sorted. It is empty for an unknown that
	// has no admissible result.
	Evidence []string
	// Diagnostics holds the CO-PRV-012 warning when admissible results disagree.
	Diagnostics []diag.Diagnostic
}

// Evaluate gives the verdict of check from all the results that were read. Results of other checks are ignored.
//
//   - An admissible result with outcome `passed` gives `pass`; `failed` or `errored` gives `violation`.
//   - Admissible results that disagree (a passed one and a failed or errored one) give `unknown` with
//     `evidence-conflict` and a CO-PRV-012 warning that names both. No result wins by order or by time.
//   - A skipped result says nothing about the check. It gives a verdict only when it is the only admissible kind,
//     and then the verdict is `unknown` with `no-evidence-supplied`; next to a passed or failed result it is ignored.
//   - Only inadmissible results, or none, give `unknown` with `no-evidence-supplied`. An inadmissible result never
//     gives `pass` or `violation`.
func Evaluate(check string, results []Admitted) Outcome {
	out := Outcome{Check: check}

	var passed, failed, skipped, inadmissible []Admitted

	for _, result := range results {
		if result.Result.Check != check {
			continue
		}

		switch {
		case !result.Admission.Admissible:
			inadmissible = append(inadmissible, result)
		case result.Result.Outcome == statement.OutcomePassed:
			passed = append(passed, result)
		case result.Result.Outcome == statement.OutcomeFailed || result.Result.Outcome == statement.OutcomeErrored:
			failed = append(failed, result)
		default:
			skipped = append(skipped, result)
		}
	}

	switch {
	case len(passed) > 0 && len(failed) > 0:
		out.Verdict, out.Reason = Unknown, ReasonEvidenceConflict
		out.Evidence = evidence(passed, failed)
		out.Details = []string{conflictDetail(check, passed, failed)}
		out.Diagnostics = []diag.Diagnostic{
			diag.New(diag.PRV012, diag.Detail(out.Details[0]), diag.WithSource(diag.Source{URI: sorted(passed)[0].Path})),
		}
	case len(failed) > 0:
		out.Verdict = Violation
		out.Evidence = evidence(failed)
	case len(passed) > 0:
		out.Verdict = Pass
		out.Evidence = evidence(passed)
	case len(skipped) > 0:
		out.Verdict, out.Reason = Unknown, ReasonNoEvidenceSupplied
		out.Evidence = evidence(skipped)
		out.Details = []string{DetailSkipped}
	default:
		out.Verdict, out.Reason = Unknown, ReasonNoEvidenceSupplied
		out.Details = []string{DetailNoAdmissibleResult}
	}

	for _, result := range sorted(inadmissible) {
		out.Details = append(out.Details, inadmissibleDetail(&result))
	}

	return out
}

// sorted orders results by path and then digest, so that the output does not depend on the order of loading.
func sorted(results []Admitted) []Admitted {
	out := slices.Clone(results)
	slices.SortFunc(out, func(a, b Admitted) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}

		return strings.Compare(a.Digest, b.Digest)
	})

	return out
}

// evidence lists the statement digests of the groups, sorted and without duplicates.
func evidence(groups ...[]Admitted) []string {
	var out []string

	for _, group := range groups {
		for _, result := range group {
			out = append(out, result.Digest)
		}
	}

	slices.Sort(out)

	return slices.Compact(out)
}

func conflictDetail(check string, passed, failed []Admitted) string {
	return fmt.Sprintf("admissible results of check %s disagree: passed in %s, not passed in %s",
		check, names(passed), names(failed))
}

// names lists the results as `path (digest)`, sorted.
func names(results []Admitted) string {
	sortedResults := sorted(results)
	parts := make([]string, 0, len(sortedResults))

	for _, result := range sortedResults {
		parts = append(parts, result.Path+" ("+result.Digest+")")
	}

	return strings.Join(parts, ", ")
}

func inadmissibleDetail(result *Admitted) string {
	reasons := make([]string, 0, len(result.Admission.Details))
	for _, detail := range result.Admission.Details {
		reasons = append(reasons, string(detail))
	}

	return result.Path + " is inadmissible: " + strings.Join(reasons, ", ")
}
