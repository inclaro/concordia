package conformance

import (
	"cmp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	checkverdict "github.com/inclaro/concordia/internal/assurance/checkresult/verdict"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/policy"
)

// A check result is trusted evidence that a named check had an outcome for a revision and an environment. The
// evidence loader admits every result (internal/assurance/checkresult/verdict) and groups the admitted results into
// one verdict per check and environment. This file reads that grouping for the rules of the family `check-result`,
// for the checks of the controls, and for the `assurance.checkResults` list of the result. It never reads a test
// association of the code graph: `TESTS` and `TESTS_FILE` are candidates only (assurance_candidates.go).

// CheckResultEntry is one check result of `assurance.checkResults`: what the result says and why it is, or is not,
// admissible. An inadmissible result stays in the list with every reason, and never gives a verdict.
type CheckResultEntry struct {
	Check string `json:"check"`
	// Outcome is `passed`, `failed`, `errored`, or `skipped` as the producer wrote it. An invalid or missing value is
	// kept as written (empty when missing); the reasons say so.
	Outcome     string `json:"outcome"`
	Revision    string `json:"revision"`
	Environment string `json:"environment"`
	// Evaluator and EvaluatorVersion name the program that ran the check.
	Evaluator        string `json:"evaluator"`
	EvaluatorVersion string `json:"evaluatorVersion"`
	// Time is when the check ran (RFC 3339 UTC; the end of the window when the result gives a window).
	Time string `json:"time,omitempty"`
	// Producer is the id of the declared producer whose signature verified; empty when none did.
	Producer  string              `json:"producer,omitempty"`
	Integrity assurance.Integrity `json:"integrity"`
	// Path is the workspace-relative statement file and Digest its `sha256:<hex>` digest.
	Path   string `json:"path"`
	Digest string `json:"statementDigest"`
	// Reasons are every condition that makes the result inadmissible, sorted.
	Reasons []assurance.Detail `json:"reasons"`
	// Covers lists the control, node, and scenario ids that the result covers. CoversNothing is true when the producer
	// stated an empty list, which is different from a missing field.
	Covers []string `json:"covers,omitempty"`
	// Admissible is true when the result may support a verdict. Reasons then is empty.
	Admissible    bool `json:"admissible"`
	CoversNothing bool `json:"coversNothing,omitempty"`
}

// checkResultEntries gives the entries of `assurance.checkResults`, sorted by check, environment, revision, path, and
// digest in UTF-16 order.
func checkResultEntries(ev *evidence.Evidence) []CheckResultEntry {
	producers := map[string]string{}
	for i := range ev.Statements {
		producers[ev.Statements[i].Path+"\x00"+ev.Statements[i].Digest] = ev.Statements[i].Producer
	}

	entries := make([]CheckResultEntry, 0, len(ev.CheckResults))

	for i := range ev.CheckResults {
		admitted := &ev.CheckResults[i]
		result := admitted.Result

		entry := CheckResultEntry{
			Check: result.Check, Outcome: result.Outcome, Revision: result.Revision, Environment: result.Environment,
			Evaluator: result.Evaluator.Name, EvaluatorVersion: result.Evaluator.Version,
			Producer:  producers[admitted.Path+"\x00"+admitted.Digest],
			Integrity: admitted.Admission.Integrity, Admissible: admitted.Admission.Admissible,
			Reasons: slices.Clone(admitted.Admission.Details),
			Covers:  slices.Clone(result.Covers), CoversNothing: result.CoversNothing(),
			Path: admitted.Path, Digest: admitted.Digest,
		}

		if when := result.When(); !when.IsZero() {
			entry.Time = when.UTC().Format("2006-01-02T15:04:05Z")
		}

		if entry.Reasons == nil {
			entry.Reasons = []assurance.Detail{}
		}

		slices.Sort(entry.Covers)

		entries = append(entries, entry)
	}

	slices.SortFunc(entries, func(a, b CheckResultEntry) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.Check, b.Check), canonjson.CompareUTF16(a.Environment, b.Environment),
			canonjson.CompareUTF16(a.Revision, b.Revision), canonjson.CompareUTF16(a.Path, b.Path),
			canonjson.CompareUTF16(a.Digest, b.Digest),
		)
	})

	return entries
}

// checkVerdictOf maps the local verdict and reason strings of internal/assurance/checkresult/verdict onto the verdicts
// and the closed reasons of the results. A reason outside the closed set becomes an `error` in ExternalOutcome.normalize,
// so the evidence packages cannot invent one.
func checkVerdictOf(outcome *checkverdict.Outcome) (RuleVerdict, UnknownReason) {
	switch outcome.Verdict {
	case checkverdict.Pass:
		return VerdictPass, ""
	case checkverdict.Violation:
		return VerdictViolation, ""
	case checkverdict.Unknown:
		return VerdictUnknown, UnknownReason(outcome.Reason)
	}

	return VerdictError, ""
}

// checkDetail is the text of a verdict: the detail lines of the evidence package (the cause of an unknown, then one
// line for each inadmissible result with its reasons) and, for a verdict that rests on results, their digests. The
// admissibility reasons are details here and never reasons of their own: every unknown result has exactly one reason.
func checkDetail(outcome *checkverdict.Outcome) string {
	parts := slices.Clone(outcome.Details)

	if len(outcome.Evidence) > 0 {
		parts = append(parts, "result "+strings.Join(outcome.Evidence, ", "))
	}

	return strings.Join(parts, "; ")
}

// checkOutcomeOf turns the verdict of one check into an outcome of a rule or a control check.
func checkOutcomeOf(outcome *checkverdict.Outcome) ExternalOutcome {
	v, reason := checkVerdictOf(outcome)

	return ExternalOutcome{Verdict: v, Reason: reason, Detail: checkDetail(outcome)}
}

// worstOutcome gives the worst of the outcomes in the order of WorstVerdict (error, violation, unknown, pass), the first
// one of equal rank.
func worstOutcome(outcomes []ExternalOutcome) ExternalOutcome {
	worst := 0

	for i := range outcomes {
		if WorstVerdict([]RuleVerdict{outcomes[worst].Verdict, outcomes[i].Verdict}) != outcomes[worst].Verdict {
			worst = i
		}
	}

	return outcomes[worst]
}

// externalChecks gives the verdict of each check that a result names: the worst verdict over the environments that
// the results of the check cover. A check that no result names has no entry, which ExternalEvaluation reads as no
// admissible result. Results for an environment that `--environment` excludes have no verdict in this run, so they
// never decide a control.
func externalChecks(found *evidence.Evidence, excluded []string) map[string]ExternalCheck {
	byCheck := map[string][]ExternalOutcome{}

	for i := range found.CheckOutcomes {
		environment := found.CheckOutcomes[i].Environment
		if environment != "" && slices.Contains(excluded, environment) {
			continue
		}

		outcome := checkOutcomeOf(&found.CheckOutcomes[i].Outcome)
		if environment != "" {
			outcome.Detail = withEnvironment(environment, outcome.Detail)
		}

		byCheck[found.CheckOutcomes[i].Check] = append(byCheck[found.CheckOutcomes[i].Check], outcome)
	}

	checks := make(map[string]ExternalCheck, len(byCheck))

	for check, outcomes := range byCheck {
		worst := worstOutcome(outcomes)
		checks[check] = ExternalCheck{Verdict: worst.Verdict, Reason: worst.Reason, Detail: worst.Detail}
	}

	return checks
}

// withEnvironment prefixes a detail with the environment that it is about.
func withEnvironment(environment, detail string) string {
	if detail == "" {
		return "environment " + environment
	}

	return "environment " + environment + ": " + detail
}

// checkOutcomes evaluates a rule of the family `check-result` from the admitted results of the run.
//
// The rule names one check and may name an environment and subjects. The verdict follows the admissible results: a
// passed result gives `pass`, a failed or errored one `violation`; a skipped result, only inadmissible results, or no
// result give `unknown` with `no-evidence-supplied`, and results that disagree give `unknown` with `evidence-conflict`.
// Without an environment the rule takes every environment that a result names, one outcome for each, so the
// enforcement mode of each environment applies. With subjects, a result counts for a subject only when its `covers`
// list names it.
func (c *checker) checkOutcomes(plan *policy.RulePlan) ([]ExternalOutcome, bool) {
	run := c.assurance
	if run == nil || run.ev == nil {
		return nil, false
	}

	check := plan.Predicate.CheckResult.Check
	subjects := plan.Subjects

	if len(subjects) == 0 {
		subjects = []string{""}
	}

	var outcomes []ExternalOutcome

	for _, environment := range c.checkEnvironments(run.ev, plan) {
		for _, subject := range subjects {
			outcomes = append(outcomes, run.checkOutcomeAt(checkScope{check: check, environment: environment, subject: subject}))
		}
	}

	return outcomes, true
}

// checkEnvironments lists the environments that a `check-result` rule evaluates. An environment that `--environment`
// excludes has no verdict in this run (as for claim rules); with none left the rule is not applicable.
func (c *checker) checkEnvironments(loaded *evidence.Evidence, plan *policy.RulePlan) []string {
	requirement := plan.Predicate.CheckResult

	environments := []string{requirement.Environment}
	if requirement.Environment == "" {
		environments = environmentsOfCheck(loaded, requirement.Check)
	}

	return slices.DeleteFunc(environments, func(env string) bool {
		return env != "" && slices.Contains(c.environments.Excluded, env)
	})
}

// checkOutcomeAt evaluates one check scope and adds the missing-result details to an `unknown` outcome.
func (run *assuranceRun) checkOutcomeAt(scope checkScope) ExternalOutcome {
	loaded := run.ev
	outcome := checkOutcomeFor(loaded, scope)
	outcome.Subject, outcome.Environment = scope.subject, scope.environment

	if outcome.Verdict != VerdictUnknown || outcome.Reason != ReasonNoEvidenceSupplied {
		return outcome
	}

	if loaded.Empty(evidence.KindCheckResult) {
		outcome.Detail = joinDetails(missingResultsDetail(loaded), outcome.Detail)
	}

	// A test association of the code graph is shown beside the missing result and never used for it.
	outcome.Detail = joinDetails(outcome.Detail, run.candidateDetail(scope.subject))

	return outcome
}

// environmentsOfCheck lists the environments that the results of a check name, sorted; "" (no environment) when no
// result names the check, so that the rule still gets one outcome.
func environmentsOfCheck(ev *evidence.Evidence, check string) []string {
	var environments []string

	for i := range ev.CheckOutcomes {
		if ev.CheckOutcomes[i].Check == check {
			environments = append(environments, ev.CheckOutcomes[i].Environment)
		}
	}

	slices.SortFunc(environments, canonjson.CompareUTF16)
	environments = slices.Compact(environments)

	if len(environments) == 0 {
		return []string{""}
	}

	return environments
}

// checkScope names one check, one environment ("" for every), and one CALM subject ("" for the check as a whole).
type checkScope struct {
	check, environment, subject string
}

// checkOutcomeFor gives the outcome of one check for one environment and, when subject is not empty, one CALM element.
func checkOutcomeFor(loaded *evidence.Evidence, scope checkScope) ExternalOutcome {
	if scope.subject == "" {
		out := loaded.CheckVerdict(scope.check, scope.environment)

		return checkOutcomeOf(&out)
	}

	covering, others := splitCoveringResults(loaded, scope)
	out := checkverdict.Evaluate(scope.check, covering)
	outcome := checkOutcomeOf(&out)

	if len(covering) == 0 && others > 0 {
		outcome.Detail = joinDetails("no result of the check covers "+scope.subject, outcome.Detail)
	}

	return outcome
}

// splitCoveringResults gives the admitted results of the check and environment that cover the subject, and the count
// of those that do not.
func splitCoveringResults(loaded *evidence.Evidence, scope checkScope) (covering []checkverdict.Admitted, others int) {
	for i := range loaded.CheckResults {
		admitted := &loaded.CheckResults[i]
		if !resultMatchesScope(admitted, scope) {
			continue
		}

		if slices.Contains(admitted.Result.Covers, scope.subject) {
			covering = append(covering, *admitted)
		} else {
			others++
		}
	}

	return covering, others
}

func resultMatchesScope(admitted *checkverdict.Admitted, scope checkScope) bool {
	if admitted.Result.Check != scope.check {
		return false
	}

	return scope.environment == "" || admitted.Result.Environment == scope.environment
}

// missingResultsDetail names the declared result globs that matched no file, for the detail of an `unknown` verdict:
// a missing source is visible and is never read as a failed check (spec: missing evidence sources are visible).
func missingResultsDetail(ev *evidence.Evidence) string {
	var patterns []string

	for i := range ev.Globs {
		if ev.Globs[i].Kind == evidence.KindCheckResult && len(ev.Globs[i].Paths) == 0 {
			patterns = append(patterns, ev.Globs[i].Pattern)
		}
	}

	return "no file matches " + strings.Join(patterns, ", ")
}

func joinDetails(parts ...string) string {
	kept := make([]string, 0, len(parts))

	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}

	return strings.Join(kept, "; ")
}
