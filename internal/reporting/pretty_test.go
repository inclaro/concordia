package reporting_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

// prettySnapshot is the pretty report of the sample run, without color.
func prettySnapshot() string {
	return strings.Join([]string{
		"Conformance: error (3 violation, 2 unknown, 1 error, 1 pass, 1 notApplicable, 4 findings)",
		"",
		"FAIL CO-BND-001 Billing uses Fulfillment contracts only",
		"  Why it matters: Billing must depend on the published fulfillment contract.",
		"  Owner: team-billing",
		"  billing-service -> fulfillment-service: 1 finding(s)",
		"    services/billing/src/InvoiceService.ts:18  imports services/fulfillment/src/internal/ShipmentStore.ts",
		"  Fix: Import from packages/fulfillment-contract.",
		"  1 baselined finding(s) not shown (use --verbose)",
		"",
		"UNKNOWN CO-COV-001 Legacy must not call Billing",
		"  Cannot judge: minimum coverage not met: 0.500 < 0.9 (CO-CON-021)",
		"  Coverage: 50.0% (required 90.0%)",
		"",
		"FAIL CO-CYC-001 Services are acyclic",
		"  Owner: team-billing",
		"  cycle through billing-service, fulfillment-service: 1 finding(s)",
		"    services/billing/src/InvoiceService.ts:18  imports services/fulfillment/src/internal/ShipmentStore.ts  [expired-waiver (WV-2, expired 2026-01-31)]",
		"                                               then services/fulfillment/src/internal/ShipmentStore.ts imports services.billing.src.InvoiceService in services/billing/src/InvoiceService.ts",
		"    Next action: fix implementation",
		"",
		"ERROR CO-ERR-001 Broken rule",
		"  Error: rule evaluation error: boom (CO-CON-099)",
		"",
		"FAIL CO-LYR-001 Billing sits above Fulfillment",
		"  1 waived finding(s) not shown (use --verbose)",
		"",
		"UNKNOWN CO-WARN-001 Unknown but only a warning",
		"  Cannot judge: evidence below the confidence threshold: 2 edge(s) (CO-CON-023)",
		"  Coverage: 99.0% (required 95.0%)",
		"  On unknown: warn",
		"",
		"Not configured: Deployment trace, ADR governance, Diagram currency, Assessment currency, Quality evaluation",
		"",
		"1 active error(s), 1 baselined, 1 waived, 1 expired waiver(s), 2 unknown",
		"",
	}, "\n")
}

// setOnUnknown sets what the rule of the run does with an unknown verdict.
func setOnUnknown(run *conformance.Run, ruleID string, onUnknown policy.UnknownDisposition) {
	for i := range run.Rules {
		if run.Rules[i].RuleID == ruleID {
			run.Rules[i].Plan.EvidenceRequirements.OnUnknown = onUnknown
		}
	}
}

// cleanRun is the sample run with only its passing rules and no findings.
func cleanRun() *conformance.Run {
	run := testutil.SampleRun()
	clean := *run
	clean.Rules = nil

	for i := range run.Rules {
		if run.Rules[i].Verdict == conformance.VerdictPass {
			clean.Rules = append(clean.Rules, run.Rules[i])
		}
	}

	clean.Findings = nil
	clean.Areas = conformance.Areas{}
	clean.Summary = conformance.Summarize(clean.Rules, nil, diag.SeverityError, policy.UnknownFail)
	clean.SettleAreas()

	return &clean
}

func TestPretty(t *testing.T) {
	t.Parallel()

	t.Run("writes the layout for a failing rule and the summary line", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(testutil.SampleRun(), plain)
		contains(t, text, "FAIL CO-BND-001 Billing uses Fulfillment contracts only")
		contains(t, text, strings.Join([]string{
			"  Why it matters: Billing must depend on the published fulfillment contract.",
			"  Owner: team-billing",
			"  billing-service -> fulfillment-service: 1 finding(s)",
			"    services/billing/src/InvoiceService.ts:18  imports services/fulfillment/src/internal/ShipmentStore.ts",
			"  Fix: Import from packages/fulfillment-contract.",
		}, "\n"))
		equal(t, lastLine(text), "1 active error(s), 1 baselined, 1 waived, 1 expired waiver(s), 2 unknown")
	})

	t.Run("matches the snapshot of the fixture run", func(t *testing.T) {
		t.Parallel()

		equal(t, reporting.RenderPretty(testutil.SampleRun(), plain), prettySnapshot())
	})

	t.Run("--verbose shows baselined and waived findings in full, with the full fingerprint", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(testutil.SampleRun(), reporting.PrettyOptions{Verbose: true})
		contains(t, text, "[baselined (baseline)]")
		contains(t, text, "[waived (WV-1, expires 2027-12-31)]")
		contains(t, text, "fingerprint "+testutil.FPBaselined)
		contains(t, text, "evidence IMPORTS syntactic cbm:demo:services.billing.src.InvoiceService -> cbm:demo:services.fulfillment.src.internal.ShipmentStore")
		notContains(t, text, "not shown")
	})

	t.Run("onUnknown=ignore hides the unknown rule and its count", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		setOnUnknown(run, "CO-COV-001", policy.UnknownIgnore)

		text := reporting.RenderPretty(run, plain)
		notContains(t, text, "CO-COV-001")
		contains(t, lastLine(text), "1 unknown")
	})

	t.Run("uses no ANSI color unless asked, and still writes the status words with color", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(testutil.SampleRun(), plain)
		notContains(t, text, "\x1b[")

		colored := reporting.RenderPretty(testutil.SampleRun(), reporting.PrettyOptions{Color: true})
		contains(t, colored, "\x1b[")
		equal(t, regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(colored, ""), text)
	})

	t.Run("a clean run shows the conformance section, the areas that are not configured, and the summary line", func(t *testing.T) {
		t.Parallel()

		equal(t, reporting.RenderPretty(cleanRun(), plain), strings.Join([]string{
			"Conformance: pass (1 pass)",
			"",
			"Not configured: Deployment trace, ADR governance, Diagram currency, Assessment currency, Quality evaluation",
			"",
			"0 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 0 unknown",
			"",
		}, "\n"))
	})
}

// accessRun is the sample run with CO-BND-001 as an effective-access rule, whose findings are network violations that
// the evaluator observed in the plan of the production environment.
func accessRun() *conformance.Run {
	run := testutil.SampleRun()

	rule := slices.IndexFunc(run.Rules, func(r conformance.RuleResult) bool { return r.RuleID == "CO-BND-001" })
	run.Rules[rule].Family = policy.FamilyEffectiveAccess

	for i := range run.Findings {
		if run.Findings[i].RuleID == "CO-BND-001" {
			setAccessEvidence(&run.Findings[i])
		}
	}

	return run
}

// setAccessEvidence makes a finding a network violation that the evaluator observed in the plan of production.
func setAccessEvidence(finding *conformance.Finding) {
	finding.Location = &conformance.Location{Path: "deploy/production"}
	finding.EvidencePath = []conformance.EvidenceStep{}
	finding.Observed = "open: tcp/5432 from aws_lb.public to aws_db_instance.ledger " +
		"(aws_vpc_security_group_ingress_rule.ledger_from_gateway rule)"
}

func TestEffectiveAccessEvidence(t *testing.T) {
	t.Parallel()

	t.Run("an effective-access finding shows what is open under its location", func(t *testing.T) {
		t.Parallel()

		contains(t, reporting.RenderPretty(accessRun(), plain), strings.Join([]string{
			"  billing-service -> fulfillment-service: 1 finding(s)",
			"    deploy/production",
			"      open: tcp/5432 from aws_lb.public to aws_db_instance.ledger (aws_vpc_security_group_ingress_rule.ledger_from_gateway rule)",
			"  Fix: Import from packages/fulfillment-contract.",
		}, "\n"))
	})

	t.Run("--verbose keeps the fingerprint after the evidence", func(t *testing.T) {
		t.Parallel()

		contains(t, reporting.RenderPretty(accessRun(), reporting.PrettyOptions{Verbose: true}), strings.Join([]string{
			"    deploy/production",
			"      open: tcp/5432 from aws_lb.public to aws_db_instance.ledger (aws_vpc_security_group_ingress_rule.ledger_from_gateway rule)",
			"      fingerprint " + testutil.FPActive,
		}, "\n"))
	})

	t.Run("a finding of another family does not show its observed text", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(testutil.SampleRun(), plain)
		contains(t, text, strings.Join([]string{
			"  billing-service -> fulfillment-service: 1 finding(s)",
			"    services/billing/src/InvoiceService.ts:18  imports services/fulfillment/src/internal/ShipmentStore.ts",
			"  Fix: Import from packages/fulfillment-contract.",
		}, "\n"))
		notContains(t, text, "IMPORTS edge between the two services")
	})
}

// unknownAreaRun is a run whose conformance area passes while a later area is `unknown`.
func unknownAreaRun() *conformance.Run {
	run := testutil.SampleRun()
	passing := make([]conformance.RuleResult, 0, 1)

	for i := range run.Rules {
		if run.Rules[i].Verdict == conformance.VerdictPass {
			passing = append(passing, run.Rules[i])
		}
	}

	run.Rules, run.Findings = passing, nil
	run.Summary = conformance.Summarize(passing, nil, diag.SeverityError, policy.UnknownFail)
	run.Areas = conformance.Areas{DeploymentTrace: conformance.AreaResult{
		Verdict: conformance.AreaVerdict(conformance.VerdictUnknown), Counts: map[string]int{"unknown": 3, "complete": 7},
	}}
	run.SettleAreas()

	return run
}

func TestAreasAreShownSeparately(t *testing.T) {
	t.Parallel()

	t.Run("OUT-03 an unknown area is not hidden by a passing conformance area", func(t *testing.T) {
		t.Parallel()

		run := unknownAreaRun()
		equal(t, run.Areas.Conformance.Verdict, conformance.AreaVerdict(conformance.VerdictPass))
		equal(t, run.Summary.Verdict, conformance.VerdictUnknown)

		text := reporting.RenderPretty(run, plain)
		contains(t, text, "Conformance: pass (1 pass)")
		contains(t, text, "Deployment trace: unknown (3 unknown, 7 complete)")
		contains(t, text, "Unknown area(s): Deployment trace")
		notContains(t, text, "Deployment trace: pass")
	})

	t.Run("each configured area is a section of its own, in the order of the report", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(unknownAreaRun(), plain)
		conformanceAt := strings.Index(text, "Conformance: pass")
		traceAt := strings.Index(text, "Deployment trace: unknown")
		notConfiguredAt := strings.Index(text, "Not configured: ADR governance, Diagram currency, Assessment currency, Quality evaluation")

		if !inOrder(conformanceAt, traceAt, notConfiguredAt) {
			t.Errorf("section order %d, %d, %d in:\n%s", conformanceAt, traceAt, notConfiguredAt, text)
		}
	})

	t.Run("an area that is not configured says so and never shows pass", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		equal(t, run.Areas.ADRGovernance.Verdict, conformance.AreaNotConfigured)
		equal(t, len(run.Areas.ADRGovernance.Counts), 0)
		notContains(t, reporting.RenderPretty(run, plain), "ADR governance: pass")
	})

	t.Run("results.json has the six area keys", func(t *testing.T) {
		t.Parallel()

		var doc struct {
			Areas map[string]struct {
				Counts  map[string]int `json:"counts"`
				Verdict string         `json:"verdict"`
			} `json:"areas"`
		}

		decodeJSON(t, renderJSON(t, unknownAreaRun()), &doc)

		equal(t, len(doc.Areas), 6)
		equal(t, doc.Areas["conformance"].Verdict, "pass")
		equal(t, doc.Areas["deploymentTrace"].Verdict, "unknown")
		equal(t, doc.Areas["adrGovernance"].Verdict, "not-configured")
		equal(t, doc.Areas["diagramCurrency"].Verdict, "not-configured")
		equal(t, doc.Areas["assessmentCurrency"].Verdict, "not-configured")
		equal(t, doc.Areas["qualityEvaluation"].Verdict, "not-configured")
	})
}

// tracedStatus is the status of the subject at index i of a trace: the first three are `unknown`, then one is
// `unresolved`, one is `partial`, and the rest are `complete`.
func tracedStatus(i int) deployment.Status {
	switch {
	case i < 3:
		return deployment.StatusUnknown
	case i == 3:
		return deployment.StatusUnresolved
	case i == 4:
		return deployment.StatusPartial
	}

	return deployment.StatusComplete
}

// tracedRun is unknownAreaRun with a trace of `subjects` subjects in one environment: three are `unknown`, one
// is `unresolved`, one is `partial`, and the rest are `complete`.
func tracedRun(subjects int) *conformance.Run {
	run := unknownAreaRun()
	byStatus := map[deployment.Status]int{}
	traces := make([]deployment.SubjectTrace, subjects)

	for i := range traces {
		status := tracedStatus(i)
		byStatus[status]++
		traces[i] = deployment.SubjectTrace{ArchitectureID: fmt.Sprintf("service-%02d", i), Environment: "production", Level: deployment.LevelDeclared, Status: status}
	}

	run.DeploymentTrace = deployment.Trace{
		Status:       deployment.StatusConfigured,
		Levels:       deployment.Levels(),
		Environments: []deployment.EnvironmentTrace{{Name: "production", Subjects: traces}},
		Summary:      deployment.Summary{ByStatus: byStatus, Subjects: subjects, Environments: 1},
	}

	return run
}

func TestDeploymentTraceAreaDetail(t *testing.T) {
	t.Parallel()

	t.Run("summary denominators: every subject counts and unknown ones show next to a passing conformance area", func(t *testing.T) {
		t.Parallel()

		text := reporting.RenderPretty(tracedRun(10), plain)
		contains(t, text, "Conformance: pass (1 pass)")
		contains(t, text, "Evidence: declared configuration only. Not traced: deployment execution and runtime.")
		contains(t, text, "Subjects: 10 in 1 environment(s): 5 complete, 1 partial, 1 unresolved, 3 unknown")
		contains(t, text, "unknown: service-00 in production")
		contains(t, text, "unresolved: service-03 in production")
		contains(t, text, "Unknown area(s): Deployment trace")
		notContains(t, text, "complete: service")
	})

	t.Run("the list of open subjects is capped", func(t *testing.T) {
		t.Parallel()

		run := tracedRun(30)
		for i := 5; i < 30; i++ {
			run.DeploymentTrace.Environments[0].Subjects[i].Status = deployment.StatusUnknown
		}

		text := reporting.RenderPretty(run, plain)
		contains(t, text, "… and 10 more")
		notContains(t, text, "service-29")
	})

	t.Run("No environment declared", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		run.DeploymentTrace = deployment.Trace{Status: deployment.StatusNotConfigured, Levels: deployment.Levels()}
		run.Areas.DeploymentTrace = conformance.AreaResult{Verdict: conformance.AreaNotConfigured, Counts: map[string]int{}}
		run.SettleAreas()

		text := reporting.RenderPretty(run, plain)
		contains(t, text, "Not configured: Deployment trace")
		contains(t, text, "No environment declared")
		notContains(t, text, "Deployment trace: pass")
	})
}
