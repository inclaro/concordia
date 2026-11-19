package conformance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// RolloutSchemaVersion is the `schemaVersion` of the rollout report.
const RolloutSchemaVersion = "1.0"

// DefaultRolloutReportPath is where `check --rollout-report` writes the report without an explicit path.
const DefaultRolloutReportPath = "build/concordia/rollout-report.json"

// mixedMode is the enforcement of a row whose rules do not all have one mode.
const mixedMode = "mixed"

// What a missing guarantee is about (RolloutMissing.Kind).
const (
	// MissingBinding is an observed entity that no binding maps to a CALM element, or that several bindings map.
	MissingBinding = "binding"
	// MissingCoverage is a coverage predicate that is not complete, a family the provider does not support, or a setting
	// of the workspace that the result needs (a claim contract, an evaluator, an enforcement assumption, `assessments`).
	MissingCoverage = "coverage"
	// MissingEnvironment is an environment attribution that is not resolved.
	MissingEnvironment = "environment"
	// MissingEvidence is evidence that conflicts or lacks a property that a selector needs.
	MissingEvidence = "evidence"
)

// RolloutReport is the rollout report of one evaluation (MIG-03). It supports the review before a family becomes
// `mandatory`: it changes no mode and no exit code.
type RolloutReport struct {
	// Rollback is the active `check.rollback`. nil when none is active.
	Rollback      *RunRollback  `json:"rollback,omitempty"`
	SchemaVersion string        `json:"schemaVersion"`
	RunID         string        `json:"runId"`
	FailOn        diag.Severity `json:"failOn"`
	// Families has one row for each rule family and environment, sorted by family and environment.
	Families []RolloutFamily `json:"families"`
}

// RolloutFamily is the row of one rule family in one environment.
type RolloutFamily struct {
	// Family is the family that the rules declare (`rule.family`, the selector of `check.enforcement`). Empty for rules that declare none.
	Family string `json:"family"`
	// Environment is the declared environment of the results. Empty for rules with no environment.
	Environment string `json:"environment"`
	// Enforcement is the mode of the rules of the row, or `mixed` when they differ (see Rules).
	Enforcement string `json:"enforcement"`
	// Rules are the rules of the row with their modes, sorted by ID.
	Rules []RolloutRule `json:"rules"`
	// Unknown lists each unknown result with its reason and what is missing.
	Unknown []RolloutUnknown `json:"unknown"`
	// WouldBlock lists the live violations that block if the row were `mandatory`. Empty when the row already is.
	WouldBlock []RolloutBlock `json:"wouldBlock"`
	// Verdicts counts the results of the row: obligation results for a typed rule, rule results for the others.
	Verdicts RuleCounts `json:"verdicts"`
	// Superseded counts the obligations that a precedence entry placed below another rule. They have no verdict in the row.
	Superseded int `json:"superseded"`
}

// RolloutRule is a rule of a row and its mode there.
type RolloutRule struct {
	RuleID      string             `json:"ruleId"`
	Enforcement policy.Enforcement `json:"enforcement"`
}

// RolloutUnknown is an unknown result of a rollout row.
type RolloutUnknown struct {
	RuleID string `json:"ruleId"`
	// ObligationID is the obligation of a typed rule. Empty for another rule.
	ObligationID string `json:"obligationId,omitempty"`
	// Subject is the CALM ID of the obligation.
	Subject string `json:"subject,omitempty"`
	// Reason is the closed unknown reason of an obligation, or the diagnostic code of the reason of another rule.
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
	// Missing says which binding or coverage is missing.
	Missing []RolloutMissing `json:"missing"`
	// WouldBlock is true when the unknown fails the run if the row were `mandatory` (the rule says `onUnknown: fail`).
	WouldBlock bool `json:"wouldBlock"`
}

// RolloutMissing names a binding, coverage predicate, environment attribution, or evidence that is missing.
type RolloutMissing struct {
	Kind string `json:"kind"`
	// Subject is the evidence family or predicate that is missing. Empty for a binding.
	Subject    string `json:"subject,omitempty"`
	Detail     string `json:"detail"`
	NextAction string `json:"nextAction,omitempty"`
}

// RolloutBlock is a finding that would block if its family were `mandatory`.
type RolloutBlock struct {
	RuleID       string              `json:"ruleId"`
	Fingerprint  string              `json:"fingerprint"`
	Severity     policy.RuleSeverity `json:"severity"`
	Kind         FindingKind         `json:"kind"`
	Message      string              `json:"message"`
	ObligationID string              `json:"obligationId,omitempty"`
	Subject      string              `json:"subject,omitempty"`
	Location     *Location           `json:"location,omitempty"`
	// Enforcement is the mode of the finding now (`advisory` or `shadow`).
	Enforcement policy.Enforcement `json:"enforcement"`
}

type rolloutKey struct{ family, environment string }

// modeOf gives the mode of a result: a result that declares none gates as `mandatory`.
func modeOf(mode policy.Enforcement) policy.Enforcement {
	if mode == "" {
		return policy.EnforcementMandatory
	}

	return mode
}

// BuildRolloutReport builds the rollout report from a run: no new evaluation and no new evidence.
func BuildRolloutReport(run *Run) *RolloutReport {
	report := &RolloutReport{
		SchemaVersion: RolloutSchemaVersion, RunID: run.Run.ID, FailOn: run.Summary.FailOn,
		Rollback: run.Summary.Rollback, Families: []RolloutFamily{},
	}

	rows := map[rolloutKey]*RolloutFamily{}
	byRule := make(map[string]*RuleResult, len(run.Rules))
	superseded := map[string]bool{}

	row := func(family, environment string) *RolloutFamily {
		key := rolloutKey{family, environment}
		if rows[key] == nil {
			rows[key] = &RolloutFamily{Family: family, Environment: environment, Rules: []RolloutRule{}, Unknown: []RolloutUnknown{}, WouldBlock: []RolloutBlock{}}
		}

		return rows[key]
	}

	for i := range run.Rules {
		rule := &run.Rules[i]
		byRule[rule.RuleID] = rule

		if len(rule.Obligations) == 0 {
			addRuleResult(row(rule.Plan.Family, ""), rule)

			continue
		}

		for j := range rule.Obligations {
			obl := &rule.Obligations[j]
			target := row(rule.Plan.Family, obl.Environment)
			addMode(target, rule.RuleID, obl.Enforcement)

			if obl.Superseded() {
				superseded[obl.ID] = true
				target.Superseded++

				continue
			}

			target.Verdicts.add(obl.Verdict)

			if obl.Verdict == VerdictUnknown {
				target.Unknown = append(target.Unknown, obligationUnknown(rule, obl))
			}
		}
	}

	for i := range run.Findings {
		f := &run.Findings[i]

		rule := byRule[f.RuleID]
		if rule == nil || superseded[f.ObligationID] || !wouldBlock(f, run.Summary.FailOn) {
			continue
		}

		target := row(rule.Plan.Family, f.Environment)
		target.WouldBlock = append(target.WouldBlock, RolloutBlock{
			RuleID: f.RuleID, Fingerprint: f.Fingerprint, Severity: f.Severity, Kind: f.Kind, Message: f.Message,
			ObligationID: f.ObligationID, Subject: f.Subject, Location: f.Location, Enforcement: f.Enforcement,
		})
	}

	for _, r := range rows {
		finishRow(r)
		report.Families = append(report.Families, *r)
	}

	slices.SortFunc(report.Families, func(a, b RolloutFamily) int {
		if c := canonjson.CompareUTF16(a.Family, b.Family); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Environment, b.Environment)
	})

	return report
}

// wouldBlock says whether a finding blocks as soon as its mode gates: it is a live violation at or above the failure
// threshold and its mode does not gate now.
func wouldBlock(f *Finding, failOn diag.Severity) bool {
	if gates(f.Enforcement) {
		return false
	}

	asMandatory := *f
	asMandatory.Enforcement = policy.EnforcementMandatory

	return IsFailing(&asMandatory, failOn)
}

func (c *RuleCounts) add(verdict RuleVerdict) {
	switch verdict {
	case VerdictPass:
		c.Pass++
	case VerdictViolation:
		c.Violation++
	case VerdictUnknown:
		c.Unknown++
	case VerdictNotApplicable:
		c.NotApplicable++
	case VerdictError:
		c.Error++
	}
}

func addMode(fam *RolloutFamily, ruleID string, mode policy.Enforcement) {
	mode = modeOf(mode)

	if slices.ContainsFunc(fam.Rules, func(x RolloutRule) bool { return x.RuleID == ruleID && x.Enforcement == mode }) {
		return
	}

	fam.Rules = append(fam.Rules, RolloutRule{RuleID: ruleID, Enforcement: mode})
}

// addRuleResult counts a rule with no obligation results (a rule of the as-built families, or a typed rule that has
// no obligation).
func addRuleResult(r *RolloutFamily, rule *RuleResult) {
	addMode(r, rule.RuleID, rule.Enforcement)
	r.Verdicts.add(rule.Verdict)

	if rule.Verdict == VerdictUnknown {
		r.Unknown = append(r.Unknown, ruleUnknown(rule))
	}
}

func finishRow(fam *RolloutFamily) {
	slices.SortFunc(fam.Rules, func(a, b RolloutRule) int {
		if c := canonjson.CompareUTF16(a.RuleID, b.RuleID); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(string(a.Enforcement), string(b.Enforcement))
	})

	fam.Enforcement = mixedMode
	if modes := distinctModes(fam.Rules); len(modes) == 1 {
		fam.Enforcement = string(modes[0])
	}

	slices.SortFunc(fam.Unknown, func(a, b RolloutUnknown) int {
		return cmpStrings([]string{a.RuleID, a.ObligationID, a.Reason}, []string{b.RuleID, b.ObligationID, b.Reason})
	})
	slices.SortFunc(fam.WouldBlock, func(a, b RolloutBlock) int {
		return cmpStrings([]string{a.RuleID, a.Fingerprint}, []string{b.RuleID, b.Fingerprint})
	})
}

func distinctModes(rules []RolloutRule) []policy.Enforcement {
	var out []policy.Enforcement

	for _, r := range rules {
		if !slices.Contains(out, r.Enforcement) {
			out = append(out, r.Enforcement)
		}
	}

	return out
}

func cmpStrings(a, b []string) int {
	for i := range a {
		if c := canonjson.CompareUTF16(a[i], b[i]); c != 0 {
			return c
		}
	}

	return 0
}

// failsOnUnknown says whether an unknown of the rule fails the run once its mode gates.
func failsOnUnknown(rule *RuleResult, mode policy.Enforcement) bool {
	return !gates(mode) && rule.Plan.EvidenceRequirements.OnUnknown == policy.UnknownFail
}

// obligationUnknown lists an unknown obligation with what is missing, from its unknown occurrence results.
func obligationUnknown(rule *RuleResult, result *ObligationResult) RolloutUnknown {
	out := RolloutUnknown{
		RuleID: rule.RuleID, ObligationID: result.ID, Subject: result.Subject, Reason: string(result.Reason), Detail: result.Detail,
		Missing: []RolloutMissing{}, WouldBlock: failsOnUnknown(rule, result.Enforcement),
	}

	for i := range result.Occurrences {
		occ := &result.Occurrences[i]
		if occ.Result != VerdictUnknown {
			continue
		}

		kind := occ.Kind
		if kind == "" {
			kind = result.Relation.Kind
		}

		family, _ := observation.FamilyForKind(kind)

		missing := missingFor(occ.Reason, family, occ.Detail, occ.NextAction)
		if !slices.Contains(out.Missing, missing) {
			out.Missing = append(out.Missing, missing)
		}
	}

	sortMissing(out.Missing)

	return out
}

// missingFor says what an unknown reason is missing.
func missingFor(reason UnknownReason, family, detail, nextAction string) RolloutMissing {
	kind, subject := MissingCoverage, family

	switch reason {
	case ReasonBindingGap, ReasonAmbiguousBinding:
		kind, subject = MissingBinding, ""
	case ReasonEnvironmentUnresolved:
		kind, subject = MissingEnvironment, ""
	case ReasonEvidenceConflict, ReasonSelectorPropertyUnknown, ReasonTargetNotDeclared, ReasonReferenceUnresolved,
		ReasonNoEvidenceSupplied, ReasonRenderedEvidenceMissing:
		kind = MissingEvidence
	case ReasonCoverageIncomplete, ReasonUnsupportedFamily, ReasonCapabilityRegression, ReasonLimitExceeded,
		ReasonTrustAnchorUnreadable, ReasonVerificationUnavailable,
		ReasonClaimNotEnabled, ReasonNoEvaluatorAvailable, ReasonEnforcementNotDeclared, ReasonReviewedModeNotAvailable:
		kind = MissingCoverage
	}

	return RolloutMissing{Kind: kind, Subject: subject, Detail: detail, NextAction: nextAction}
}

func sortMissing(missing []RolloutMissing) {
	slices.SortFunc(missing, func(a, b RolloutMissing) int {
		return cmpStrings([]string{a.Kind, a.Subject, a.Detail}, []string{b.Kind, b.Subject, b.Detail})
	})
}

// ruleUnknown lists an unknown rule of the as-built families: its reason and the coverage or binding it lacks.
func ruleUnknown(rule *RuleResult) RolloutUnknown {
	out := RolloutUnknown{RuleID: rule.RuleID, Reason: "unknown", Missing: []RolloutMissing{}, WouldBlock: failsOnUnknown(rule, rule.Enforcement)}

	if len(rule.Reasons) > 0 {
		out.Reason, out.Detail = rule.Reasons[0].Code, rule.Reasons[0].Message
	}

	if cov := rule.Coverage; cov != nil {
		if cov.UnboundEndpoints > 0 {
			out.Missing = append(out.Missing, RolloutMissing{
				Kind:       MissingBinding,
				Detail:     fmt.Sprintf("%d candidate edges have an endpoint that no binding maps to a CALM element", cov.UnboundEndpoints),
				NextAction: NextActionResolveBinding,
			})
		}

		for i := range cov.Predicates {
			if p := &cov.Predicates[i]; !p.Complete() {
				out.Missing = append(out.Missing, RolloutMissing{Kind: MissingCoverage, Subject: p.Predicate, Detail: predicateDetail(p), NextAction: nextActionEvidence})
			}
		}

		if cov.Files.Ratio < cov.Required {
			out.Missing = append(out.Missing, RolloutMissing{
				Kind: MissingCoverage, Subject: "files", NextAction: nextActionConfig,
				Detail: fmt.Sprintf("file coverage %.2f is below the required %.2f", cov.Files.Ratio, cov.Required),
			})
		}
	}

	sortMissing(out.Missing)

	return out
}

func predicateDetail(p *observation.PredicateCoverage) string {
	detail := fmt.Sprintf("the coverage of %s is %s in %s", p.Predicate, p.Status, p.Scope.Project)
	if len(p.Reasons) > 0 {
		detail += ": " + strings.Join(p.Reasons, "; ")
	}

	return detail
}

// JSON gives the canonical JSON of the report.
func (r *RolloutReport) JSON() ([]byte, error) {
	data, err := canonjson.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encode the rollout report: %w", err)
	}

	return data, nil
}

// previewLimit is the number of unknown results and blocking findings that the text summary lists for a row.
const previewLimit = 5

// Text gives the text summary of the report. path is where the JSON report was written ("" says none).
func (r *RolloutReport) Text(path string) string {
	var out strings.Builder

	out.WriteString("rollout report")

	if path != "" {
		out.WriteString(" (" + path + ")")
	}

	out.WriteString("\n")

	if r.Rollback != nil {
		out.WriteString("  " + r.Rollback.Message + "\n")
	}

	if len(r.Families) == 0 {
		out.WriteString("  no rules\n")

		return out.String()
	}

	for i := range r.Families {
		writeRolloutRow(&out, &r.Families[i])
	}

	return out.String()
}

func writeRolloutRow(out *strings.Builder, fam *RolloutFamily) {
	family := fam.Family
	if family == "" {
		family = "(no family)"
	}

	environment := fam.Environment
	if environment == "" {
		environment = "(no environment)"
	}

	counts := fam.Verdicts
	fmt.Fprintf(out, "  %s / %s: %s; pass %d, violation %d, unknown %d, not-applicable %d, error %d",
		family, environment, fam.Enforcement, counts.Pass, counts.Violation, counts.Unknown, counts.NotApplicable, counts.Error)

	if fam.Superseded > 0 {
		fmt.Fprintf(out, ", superseded %d", fam.Superseded)
	}

	out.WriteString("\n")

	for i := range fam.Unknown {
		if i == previewLimit {
			fmt.Fprintf(out, "    unknown: and %d more\n", len(fam.Unknown)-previewLimit)

			break
		}

		unknown := &fam.Unknown[i]
		fmt.Fprintf(out, "    unknown %s %s: %s", unknown.RuleID, unknown.Subject, unknown.Reason)

		for _, missing := range unknown.Missing {
			fmt.Fprintf(out, "; missing %s", missing.Kind)

			if missing.Subject != "" {
				out.WriteString(" " + missing.Subject)
			}
		}

		out.WriteString("\n")
	}

	for i := range fam.WouldBlock {
		if i == previewLimit {
			fmt.Fprintf(out, "    would block if mandatory: and %d more\n", len(fam.WouldBlock)-previewLimit)

			break
		}

		block := &fam.WouldBlock[i]
		fmt.Fprintf(out, "    would block if mandatory: %s %s %s\n", block.RuleID, shortFingerprint(block.Fingerprint), block.Message)
	}
}

// shortFingerprint gives the first 12 hexadecimal characters of a fingerprint.
func shortFingerprint(fingerprint string) string {
	const shown = 12

	_, hex, found := strings.Cut(fingerprint, ":")
	if !found {
		hex = fingerprint
	}

	if len(hex) > shown {
		hex = hex[:shown]
	}

	return hex
}
