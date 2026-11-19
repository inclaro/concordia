//nolint:mnd,goconst // fixed sample data for the reporter, explain, and bundle tests.
package testutil

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/policy"
)

// Fingerprints and places of the governance findings of ADRRun(true), the A12 run.
var (
	FPADRSuperseded      = "sha256:a120" + strings.Repeat("3", 57) + "002"
	FPADRRuleUnjustified = "sha256:a120" + strings.Repeat("7", 57) + "003"
)

// Places of ADRRun.
const (
	ADRPath         = "docs/adr/0003-use-postgresql.md"
	ADRStatusLine   = 3
	ADRRegisterPath = "docs/adr/register.yaml"
	ADRSuccessor    = "docs/adr/0007-use-cockroachdb.md"
)

var adrPack = policy.PackRef{ID: "adr-governance", Version: "1.0.0"}

// adrRuleSpec is one rule of the `adr-governance` pack in the sample.
type adrRuleSpec struct {
	id, name, obligation string
	verdict              conformance.RuleVerdict
	findings             int
}

// ADRRun is a check run with a decision register, built by hand. With violating it is the A12 run: ADR-0003 is
// accepted but superseded by ADR-0007 (which the register does not list, so it gets no scope), and the mandatory
// rule CORE-DB-001 is justified only by ADR-0003. GOV-ADR-002 and GOV-ADR-003 each have one finding. Without it,
// the register has one active, committed, enforced decision and every governance rule passes.
func ADRRun(violating bool) *conformance.Run {
	run := SampleRun()

	passing := make([]conformance.RuleResult, 0, 1)

	for i := range run.Rules {
		if run.Rules[i].Verdict == conformance.VerdictPass {
			passing = append(passing, run.Rules[i])
		}
	}

	verdict := func(failing conformance.RuleVerdict) conformance.RuleVerdict {
		if violating {
			return failing
		}

		return conformance.VerdictPass
	}

	specs := []adrRuleSpec{
		{"GOV-ADR-001", "Required decisions are committed in the evaluated revision", "presence", conformance.VerdictPass, 0},
		{"GOV-ADR-002", "A registered decision is active for the architecture it governs", "applicability", verdict(conformance.VerdictViolation), 1},
		{"GOV-ADR-003", "A mandatory rule is justified by an active decision", "justification", verdict(conformance.VerdictViolation), 1},
		{"GOV-ADR-004", "A decision that requires enforcement links a mandatory rule", "enforcement", conformance.VerdictPass, 0},
		{"GOV-ADR-005", "The supersession history of decisions is consistent", "history", conformance.VerdictPass, 0},
		{"GOV-ADR-006", "An architecturally significant change has a decision", "scope", conformance.VerdictNotApplicable, 0},
	}

	rules := slices.Clone(passing)

	for _, spec := range specs {
		count := 0
		if violating {
			count = spec.findings
		}

		plan := samplePlan(spec.id, spec.name, &policy.Predicate{Family: policy.FamilyDecisionGovernance, Decisions: spec.obligation}, adrPack,
			func(p *policy.RulePlan) { p.Subjects = []string{} })

		rules = append(rules, sampleRule(&plan, spec.verdict, count, func(r *conformance.RuleResult) {
			if spec.verdict == conformance.VerdictNotApplicable {
				r.Reasons = []conformance.Reason{{Code: "CO-ADR-009", Message: "no trigger is configured"}}
			}
		}))
	}

	slices.SortFunc(rules, func(a, b conformance.RuleResult) int { return canonjson.CompareUTF16(a.RuleID, b.RuleID) })

	var findings []conformance.Finding

	summary := adrSummary(violating)

	if violating {
		findings = adrFindings()
		for i := range findings {
			summary.Findings = append(summary.Findings, findings[i].Fingerprint)
		}
	}

	run.Rules, run.Findings = rules, findings
	run.Exceptions.Waivers = []conformance.AppliedWaiver{}
	run.Summary = conformance.Summarize(rules, findings, diag.SeverityError, policy.UnknownFail)
	run.Inputs.Decisions = []conformance.InputRef{
		{Path: ADRRegisterPath, Digest: "sha256:" + strings.Repeat("4", 64)},
		{Path: "concordia.yaml#adr.lifecycle", Digest: "sha256:" + strings.Repeat("5", 64)},
	}
	run.DecisionSummary = summary
	run.Areas = conformance.Areas{}
	run.SettleAreas()

	return run
}

func adrFindings() []conformance.Finding {
	decision := &conformance.FindingDecision{
		ID: "ADR-3", Title: "Use PostgreSQL for orders", Status: "accepted", StatusText: "Accepted",
		Path: ADRPath, Line: ADRStatusLine, Successors: []string{"ADR-7"}, Predecessors: []string{},
		Governs: []string{"calm:node:orders-db"}, EnforcedBy: []string{"CORE-DB-001@core/1.0.0"},
		CandidateSuccessors: []conformance.CandidateSuccessor{{ID: "ADR-7", Path: ADRSuccessor, Applied: false}},
	}

	base := func(fingerprint, ruleID, kind, message, expected, observed, source, target string, loc *conformance.Location, d *conformance.FindingDecision) conformance.Finding {
		return conformance.Finding{
			Fingerprint: fingerprint, RuleID: ruleID, Severity: policy.SeverityError, Kind: conformance.FindingKind(kind),
			Message: message, Source: conformance.Endpoint{ArchitectureID: source}, Target: conformance.Endpoint{ArchitectureID: target},
			EvidencePath: []conformance.EvidenceStep{}, Location: loc, RelatedLocations: []conformance.Location{},
			Disposition: exceptions.DispositionActive, Expected: expected, Observed: observed, NextAction: "get review",
			Related: []conformance.RelatedRef{{Kind: "decision", ID: "ADR-3"}}, Area: conformance.AreaADRGovernance,
			ProviderLimits: conformance.ProviderLimits{Families: []conformance.FamilyLimit{}}, Decision: d,
			Enforcement: policy.EnforcementMandatory,
		}
	}

	superseded := base(FPADRSuperseded, "GOV-ADR-002", "decision-superseded",
		"ADR-3 is accepted but superseded by ADR-7: the subjects and rules it governs need a review",
		"decision ADR-3 is active", "superseded by ADR-7 (committed, effective link)", "ADR-3", "calm:node:orders-db,ADR-7",
		&conformance.Location{Path: ADRPath, Line: ADRStatusLine}, decision)
	unjustified := base(FPADRRuleUnjustified, "GOV-ADR-003", "rule-unjustified",
		"mandatory rule CORE-DB-001 is justified only by the superseded decision ADR-3",
		"a mandatory rule is linked from an active decision", "every linking decision is inactive", "CORE-DB-001", "ADR-3",
		&conformance.Location{Path: ADRRegisterPath, Line: 12}, nil)

	findings := []conformance.Finding{superseded, unjustified}
	slices.SortFunc(findings, func(a, b conformance.Finding) int {
		return canonjson.CompareUTF16(a.RuleID+a.Fingerprint, b.RuleID+b.Fingerprint)
	})

	return findings
}

func adrSummary(violating bool) *conformance.DecisionSummary {
	entry := conformance.DecisionEntry{
		ID: "ADR-3", Title: "Use PostgreSQL for orders", Path: ADRPath, Line: ADRStatusLine, Status: "accepted", StatusText: "Accepted",
		Expect: "active", Categories: []string{"system-boundaries"},
		Presence:      conformance.DecisionState{State: "committed"},
		Applicability: conformance.DecisionApplicability{State: "active"},
		Enforcement:   conformance.DecisionEnforcement{State: "enforced", Required: true, Rules: []string{"CORE-DB-001@core/1.0.0"}},
		Governs:       []string{"calm:node:orders-db"}, Released: []string{}, Successors: []string{}, Predecessors: []string{},
	}
	summary := &conformance.DecisionSummary{
		ProviderSupport: "supported",
		History:         conformance.DecisionHistory{Links: []conformance.HistoryLink{}},
		RuleJustification: []conformance.RuleJustification{
			{Rule: "CORE-DB-001@core/1.0.0", Mandatory: true, LinkedBy: []string{"ADR-3"}, Result: "pass"},
		},
		Triggers:   []conformance.TriggerResult{},
		Candidates: []conformance.DecisionCandidate{},
		Findings:   []string{},
	}

	if violating {
		entry.Applicability = conformance.DecisionApplicability{State: "superseded", Reason: "superseded-by ADR-7", StatusLags: true}
		entry.Successors = []string{"ADR-7"}
		summary.History.Links = []conformance.HistoryLink{{
			From: "ADR-7", To: "ADR-3", FromPath: ADRSuccessor, ToPath: ADRPath, Provenance: "both", Effect: "effective",
		}}
		summary.RuleJustification[0].Result = "violation"
		summary.Candidates = []conformance.DecisionCandidate{{Kind: "unregistered-decision", ID: "ADR-7", Path: ADRSuccessor}}
	}

	summary.Decisions = []conformance.DecisionEntry{entry}

	return summary
}
