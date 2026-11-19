package quality

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/admit"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// ReasonEvidenceConflict is the unknown reason of a scenario whose admissible measurements of the same window
// disagree. It is a literal value of the closed set of internal/conformance/reasons.go.
const ReasonEvidenceConflict = "evidence-conflict"

// Measurement is one measurement statement that the run read: the file, how far it is verified, and the typed
// predicate. The integrator verifies the statement (statement.Verifier) and passes the outcome in, so this package
// never starts cosign.
type Measurement struct {
	// Path is the workspace-relative path of the statement file and Digest the digest of its bytes.
	Path         string
	Digest       string
	Verification statement.Verification
	Predicate    statement.Measurement
}

// ReadMeasurement reads the measurement predicate of a loaded statement. A predicate that is malformed, of another
// type, or that names no scenario cannot be assigned to a scenario and cannot decide anything: it gives the
// CO-QUA-003 warning and no measurement.
func ReadMeasurement(loaded *statement.Loaded, verification statement.Verification) (Measurement, []diag.Diagnostic) {
	source := diag.WithSource(diag.Source{URI: loaded.Path})

	predicate, err := statement.ParseMeasurement(&loaded.Statement)
	if err != nil {
		return Measurement{}, []diag.Diagnostic{diag.New(diag.QUA003, diag.Detail(loaded.Path+": "+err.Error()), source)}
	}

	if predicate.Scenario == "" {
		return Measurement{}, []diag.Diagnostic{diag.New(diag.QUA003,
			diag.Detail(loaded.Path+": the measurement names no scenario"), source)}
	}

	return Measurement{Path: loaded.Path, Digest: loaded.Digest, Verification: verification, Predicate: predicate}, nil
}

// MeasuredInput is what the measured evaluation of a scenario needs besides the scenario.
type MeasuredInput struct {
	Context assurance.Context
	// Producers are the producers that the workspace declares, for the trust check of the admissibility engine.
	Producers []workspace.Producer
	// Measurements are all measurements of the run, for all scenarios. The evaluation picks the ones that name the
	// scenario.
	Measurements []Measurement
}

// Window is the measurement window of a measurement.
type Window struct {
	Start, End time.Time
}

// text states the window.
func (w Window) text() string {
	return w.Start.UTC().Format(time.RFC3339) + " to " + w.End.UTC().Format(time.RFC3339)
}

// same says whether both windows have the same start and end.
func (w Window) same(other Window) bool { return w.Start.Equal(other.Start) && w.End.Equal(other.End) }

// after says whether the window ends later than the other, or at the same time and starts later.
func (w Window) after(other Window) bool {
	if w.End.Equal(other.End) {
		return w.Start.After(other.Start)
	}

	return w.End.After(other.End)
}

// Assessed is what the evaluation found about one measurement that names the scenario.
type Assessed struct {
	// Window is the measurement window that the statement states.
	Window Window
	// Notes are reasons that the closed detail list has no code for: the measurement is of another metric.
	Notes  []string
	Path   string
	Digest string
	// Verdict is the verdict of the value against the criterion of the scenario, set only for an admissible
	// measurement. Concordia compares the value itself and never reads a verdict of the producer.
	Verdict Verdict
	// Summary states the value, the criterion, and the window, so that a finding can show them.
	Summary string
	// Admission is the answer of the admissibility engine.
	Admission assurance.Admission
}

// Admissible says whether the measurement can decide the scenario.
func (a *Assessed) Admissible() bool { return a.Admission.Admissible && len(a.Notes) == 0 }

// reasons lists everything that makes the measurement inadmissible, details first.
func (a *Assessed) reasons() []string {
	out := make([]string, 0, len(a.Admission.Details)+len(a.Notes))

	for _, detail := range a.Admission.Details {
		out = append(out, string(detail))
	}

	return append(out, a.Notes...)
}

// Measured is the outcome of the measured evaluation of one scenario.
type Measured struct {
	Result Result
	// Assessed lists the measurements that name the scenario, sorted by path.
	Assessed []Assessed
	// Diagnostics are the CO-QUA-003 warnings of the inadmissible measurements and the CO-PRV-012 warning of a
	// conflict.
	Diagnostics []diag.Diagnostic
}

// EvaluateMeasured gives the measured result of a scenario (spec "Measurements have a validated contract",
// "Inadmissible measurements never decide a mandatory obligation", "Admissible measurements give pass or
// violation"; design D6, D11).
//
// Each measurement that names the scenario goes through the admissibility engine against an obligation context with
// the environment of the scenario, the evaluated revision, and the evaluation time of the run. The threshold, the
// comparison, and the unit that the producer states must be the ones of the scenario: a measurement against another
// threshold is a different question, and no unit is converted. The scenario's `maxAgeDays` is the maximum age of
// the window end.
//
//   - No measurement names the scenario, or none is admissible: `unknown` with `no-evidence-supplied` and the
//     reasons of every inadmissible measurement as details. A measurement from the wrong environment, a failed run,
//     an untrusted producer, or another revision never becomes a pass or a violation.
//   - Admissible measurements: the newest window decides, because an older window is the state before the newest
//     one. The value is compared with the threshold and the comparison of the scenario: `pass` if it satisfies it,
//     `violation` if not. Measurements of that window that disagree make the scenario `unknown` with
//     `evidence-conflict` and a CO-PRV-012 warning, because neither can be preferred.
//
// The result is labelled `measured`; a structural or reviewed result never satisfies the scenario (Result.Satisfies).
func EvaluateMeasured(scenario *Scenario, input *MeasuredInput) Measured {
	out := Measured{
		Result: Result{
			Scenario: scenario.ID, Mode: ModeMeasured, Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied, Details: []string{},
		},
		Assessed: []Assessed{},
	}

	obligation := input.Context
	obligation.Environment = scenario.Environment

	relevant := make([]*Measurement, 0, len(input.Measurements))

	for i := range input.Measurements {
		if input.Measurements[i].Predicate.Scenario == scenario.ID {
			relevant = append(relevant, &input.Measurements[i])
		}
	}

	slices.SortFunc(relevant, func(first, second *Measurement) int {
		return cmp.Or(cmp.Compare(first.Path, second.Path), cmp.Compare(first.Digest, second.Digest))
	})

	for _, measurement := range relevant {
		assessed := assess(scenario, measurement, obligation, input.Producers)

		if !assessed.Admissible() {
			reasons := strings.Join(assessed.reasons(), ", ")
			out.Result.Details = append(out.Result.Details, measurement.Path+": inadmissible ("+reasons+")")
			out.Diagnostics = append(out.Diagnostics, diag.New(diag.QUA003,
				diag.Detail(measurement.Path+": "+reasons), diag.WithSource(diag.Source{URI: measurement.Path})))
		}

		out.Assessed = append(out.Assessed, assessed)
	}

	if !slices.ContainsFunc(out.Assessed, func(item Assessed) bool { return item.Admissible() }) {
		if len(relevant) == 0 {
			out.Result.Details = append(out.Result.Details, "no measurement names the scenario")
		}

		return out
	}

	decide(scenario, &out)

	return out
}

// assess runs one measurement through the admissibility engine and compares its value with the criterion.
func assess(scenario *Scenario, measurement *Measurement, obligation assurance.Context, producers []workspace.Producer) Assessed {
	predicate := &measurement.Predicate

	facts := admit.MeasurementFacts(predicate)
	facts.Extra = criterionMismatches(scenario, predicate)

	assessed := Assessed{
		Path: measurement.Path, Digest: measurement.Digest,
		Window: Window{Start: predicate.Window.Start.Time, End: predicate.Window.End.Time},
		Admission: admit.Admit(&admit.Input{
			Verification: measurement.Verification, Kind: workspace.ProducerClaimMeasurement, Context: obligation,
			Producers: producers, Facts: facts, MaxAge: time.Duration(scenario.Criterion.MaxAgeDays) * 24 * time.Hour,
		}),
	}

	if !slices.Contains(predicate.Missing, "metric") && predicate.Metric != scenario.Criterion.Metric {
		assessed.Notes = append(assessed.Notes,
			"the measurement is of metric "+strconv.Quote(predicate.Metric)+", not "+strconv.Quote(scenario.Criterion.Metric))
	}

	if !assessed.Admissible() {
		return assessed
	}

	assessed.Verdict = compare(predicate.Value, scenario.Criterion.Comparison, scenario.Criterion.Threshold)
	assessed.Summary = summary(scenario, predicate, assessed.Window)

	return assessed
}

// criterionMismatches compares what the producer states with the criterion of the scenario. A field that the
// predicate does not carry is already listed as missing, so it is not also a mismatch. A different comparison
// changes the question that the threshold answers, so it counts as a threshold mismatch.
func criterionMismatches(scenario *Scenario, predicate *statement.Measurement) []assurance.Detail {
	var details []assurance.Detail

	present := func(field string) bool { return !slices.Contains(predicate.Missing, field) }

	if (present("threshold") && predicate.Threshold != scenario.Criterion.Threshold) ||
		(present("comparison") && predicate.Comparison != string(scenario.Criterion.Comparison)) {
		details = append(details, assurance.DetailThresholdMismatch)
	}

	if present("unit") && predicate.Unit != scenario.Criterion.Unit {
		details = append(details, assurance.DetailUnitMismatch)
	}

	return details
}

// compare gives pass when the value satisfies the comparison with the threshold and violation otherwise. A
// comparison that is none of the five is an error: the loader rejects it, so it can only come from a scenario that
// was not loaded.
func compare(value float64, comparison Comparison, threshold float64) Verdict {
	var satisfied bool

	switch comparison {
	case CompareLT:
		satisfied = value < threshold
	case CompareLTE:
		satisfied = value <= threshold
	case CompareGT:
		satisfied = value > threshold
	case CompareGTE:
		satisfied = value >= threshold
	case CompareEQ:
		satisfied = value == threshold
	default:
		return VerdictError
	}

	if satisfied {
		return VerdictPass
	}

	return VerdictViolation
}

// decide sets the verdict of the result from the admissible measurements.
func decide(scenario *Scenario, out *Measured) {
	var admissible []*Assessed

	for i := range out.Assessed {
		if candidate := &out.Assessed[i]; candidate.Admissible() {
			admissible = append(admissible, candidate)
		}
	}

	newest := admissible[0]

	for _, candidate := range admissible[1:] {
		if candidate.Window.after(newest.Window) {
			newest = candidate
		}
	}

	conflict := false

	for _, candidate := range admissible {
		line := candidate.Path + ": " + candidate.Summary

		if !candidate.Window.same(newest.Window) {
			out.Result.Details = append(out.Result.Details, line+" is older than the newest window and does not decide")

			continue
		}

		out.Result.Details = append(out.Result.Details, line+" is "+string(candidate.Verdict))
		out.Result.Evidence = append(out.Result.Evidence, candidate.Path)

		if candidate.Verdict != newest.Verdict {
			conflict = true
		}
	}

	if conflict {
		out.Result.Reason = ReasonEvidenceConflict
		out.Diagnostics = append(out.Diagnostics, diag.New(diag.PRV012, diag.Detail(
			"scenario "+scenario.ID+": the measurements "+strings.Join(out.Result.Evidence, ", ")+" of the window "+
				newest.Window.text()+" disagree"),
			diag.WithSource(diag.Source{URI: newest.Path})))

		return
	}

	out.Result.Verdict, out.Result.Reason = newest.Verdict, ""
}

// summary states the value, the criterion, and the window of an admissible measurement.
func summary(scenario *Scenario, predicate *statement.Measurement, window Window) string {
	criterion := &scenario.Criterion

	return "value " + number(predicate.Value) + " " + criterion.Unit + " against " + string(criterion.Comparison) + " " +
		number(criterion.Threshold) + " " + criterion.Unit + " in the window " + window.text()
}

func number(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
