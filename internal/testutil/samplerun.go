//nolint:mnd,goconst // fixed sample data for the reporter tests.
package testutil

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// Fingerprints of the findings of SampleRun.
var (
	FPActive    = "sha256:7c4d" + strings.Repeat("0", 57) + "91a"
	FPBaselined = "sha256:1111" + strings.Repeat("2", 57) + "333"
	FPWaived    = "sha256:aaaa" + strings.Repeat("b", 57) + "ccc"
	FPExpired   = "sha256:dddd" + strings.Repeat("e", 57) + "fff"
)

var (
	samplePack  = policy.PackRef{ID: "commerce-boundaries", Version: "1.0.0"}
	otherPack   = policy.PackRef{ID: "other-pack", Version: "2.0.0"}
	sampleStrs  = func(s ...string) []string { return s }
	emptyAllow  = &policy.BoundaryAllow{TargetSourceRoots: []string{}, TargetCalmNodes: []string{}, EdgeKinds: []observation.CanonicalEdgeKind{}, TargetTags: []string{}}
	importsOnly = policy.Traversal{Direction: "outbound", EdgeKinds: []observation.CanonicalEdgeKind{observation.KindImports}, MinimumDepth: 1, MaximumDepth: 1}
)

func observedNode(qualifiedName, filePath, label string, startLine int) observation.ObservedNode {
	return observation.ObservedNode{ObservedID: "cbm:demo:" + qualifiedName, Label: label, QualifiedName: qualifiedName, FilePath: filePath, StartLine: startLine}
}

func importSteps(fromNode, toNode *observation.ObservedNode, line int) []conformance.EvidenceStep {
	from, to := *fromNode, *toNode

	return []conformance.EvidenceStep{
		{Node: &from},
		{Edge: &conformance.StepEdge{
			Kind: observation.KindImports, NativeKind: "IMPORTS", Confidence: observation.ConfidenceSyntactic, ResolutionMethod: "cbm:IMPORTS",
			From: from.ObservedID, To: to.ObservedID, Location: &conformance.Location{Path: from.FilePath, Line: line},
		}},
		{Node: &to},
	}
}

func ref(s string) *string { return &s }

func sampleFinding(invoice, store *observation.ObservedNode, fingerprint, ruleID string, mod func(*conformance.Finding)) conformance.Finding {
	finding := conformance.Finding{
		Fingerprint: fingerprint,
		RuleID:      ruleID,
		Severity:    policy.SeverityError,
		Kind:        conformance.KindForbiddenDependency,
		Message:     "forbidden dependency billing-service -> fulfillment-service",
		Source: conformance.Endpoint{ArchitectureID: "billing-service", Observed: &conformance.ObservedRef{
			QualifiedName: invoice.QualifiedName, FilePath: invoice.FilePath, Label: invoice.Label,
		}},
		Target: conformance.Endpoint{ArchitectureID: "fulfillment-service", Observed: &conformance.ObservedRef{
			QualifiedName: store.QualifiedName, FilePath: store.FilePath, Label: store.Label,
		}},
		Edge:             &conformance.EdgeRef{Kind: observation.KindImports, NativeKind: "IMPORTS", Confidence: observation.ConfidenceSyntactic, ResolutionMethod: "cbm:IMPORTS"},
		EvidencePath:     importSteps(invoice, store, 18),
		Location:         &conformance.Location{Path: invoice.FilePath, Line: 18},
		RelatedLocations: []conformance.Location{{Path: "services/billing/src/Refund.ts", Line: 3}},
		Owner:            ref("team-billing"),
		Disposition:      exceptions.DispositionActive,
		Expected:         "no dependency from billing-service to fulfillment-service",
		Observed:         "IMPORTS edge between the two services",
		NextAction:       "fix implementation",
		Related:          []conformance.RelatedRef{},
		Area:             conformance.AreaConformance,
		ProviderLimits:   conformance.ProviderLimits{Families: []conformance.FamilyLimit{}},
	}
	if mod != nil {
		mod(&finding)
	}

	return finding
}

func samplePlan(ruleID, name string, predicate *policy.Predicate, pack policy.PackRef, mod func(*policy.RulePlan)) policy.RulePlan {
	p := policy.RulePlan{
		RuleID: ruleID, Name: name, Severity: policy.SeverityError, Pack: pack,
		Subjects: sampleStrs("billing-service"), Targets: []string{}, Traversal: importsOnly, Predicate: *predicate,
		EvidenceRequirements: policy.EvidenceRequirements{MinimumConfidence: observation.ConfidenceSyntactic, MinimumCoverage: 0.95, OnUnknown: policy.UnknownFail},
		UnsupportedEdgeKinds: []observation.CanonicalEdgeKind{}, Aggregation: "edge",
	}
	if mod != nil {
		mod(&p)
	}

	return p
}

func sampleCoverage(ratio, required float64) *conformance.Coverage {
	const total = 100

	covered := int(ratio*total + 0.5) //nolint:mnd // rounds half up like Math.round.

	return &conformance.Coverage{
		Files: observation.FileCoverage{
			Total: total, Covered: covered, Ratio: ratio, Excluded: []string{"services/legacy/Legacy.kt"}, Stale: []string{}, Unindexed: []string{},
			Languages: map[string]observation.LanguageCoverage{"ts": {Total: total, Covered: covered}},
		},
		Required: required,
	}
}

func sampleRule(p *policy.RulePlan, verdict conformance.RuleVerdict, findingCount int, mod func(*conformance.RuleResult)) conformance.RuleResult {
	r := conformance.RuleResult{
		RuleID: p.RuleID, Name: p.Name, Severity: p.Severity, Policy: p.Pack, Family: p.Predicate.Family, Verdict: verdict,
		Reasons: []conformance.Reason{}, Subjects: p.Subjects, Targets: p.Targets, Plan: *p, FindingCount: findingCount,
	}
	if mod != nil {
		mod(&r)
	}

	return r
}

// SampleRun is a check run with every verdict and every disposition. Rules and findings are
// sorted as the pipeline sorts them. It lives here, not in `internal/reporting`, because the CLI
// tests use it too.
func SampleRun() *conformance.Run {
	invoice := observedNode("services.billing.src.InvoiceService", "services/billing/src/InvoiceService.ts", "Class", 18)
	store := observedNode("services.fulfillment.src.internal.ShipmentStore", "services/fulfillment/src/internal/ShipmentStore.ts", "File", 1)

	boundary := &policy.Predicate{Family: policy.FamilyBoundary, Allow: emptyAllow}
	declOnly := &policy.Predicate{Family: policy.FamilyDeclaredOnly}

	bnd := samplePlan("CO-BND-001", "Billing uses Fulfillment contracts only", boundary, samplePack, func(p *policy.RulePlan) {
		p.Owner = "team-billing"
		p.Targets = sampleStrs("fulfillment-service")
		p.Rationale = "Billing must depend on the published fulfillment contract."
		p.Remediation = "Import from packages/fulfillment-contract."
	})
	cov := samplePlan("CO-COV-001", "Legacy must not call Billing", boundary, samplePack, func(p *policy.RulePlan) {
		p.Subjects = sampleStrs("legacy-service")
		p.Targets = sampleStrs("billing-service")
		p.Traversal.EdgeKinds = []observation.CanonicalEdgeKind{observation.KindCalls}
		p.EvidenceRequirements = policy.EvidenceRequirements{MinimumConfidence: observation.ConfidenceResolved, MinimumCoverage: 0.9, OnUnknown: policy.UnknownFail}
	})
	cyc := samplePlan("CO-CYC-001", "Services are acyclic", &policy.Predicate{Family: policy.FamilyAcyclic, CollapseBy: policy.CollapseByArchitectureBinding}, samplePack,
		func(p *policy.RulePlan) { p.Subjects = sampleStrs("billing-service", "fulfillment-service") })
	decl := samplePlan("CO-DECL-001", "Dependencies are declared", declOnly, samplePack, func(p *policy.RulePlan) { p.Severity = policy.SeverityWarning })
	lyr := samplePlan("CO-LYR-001", "Billing sits above Fulfillment", &policy.Predicate{Family: policy.FamilyLayer}, samplePack,
		func(p *policy.RulePlan) { p.Layers = [][]string{{"billing-service"}, {"fulfillment-service"}} })
	notApplicable := samplePlan("CO-NA-001", "Nothing selected", &policy.Predicate{Family: policy.FamilyObservedRequired}, samplePack,
		func(p *policy.RulePlan) { p.Subjects = []string{} })
	broken := samplePlan("CO-ERR-001", "Broken rule", declOnly, samplePack, nil)
	warnUnknown := samplePlan("CO-WARN-001", "Unknown but only a warning", declOnly, otherPack, func(p *policy.RulePlan) {
		p.EvidenceRequirements = policy.EvidenceRequirements{MinimumConfidence: observation.ConfidenceSyntactic, MinimumCoverage: 0.95, OnUnknown: policy.UnknownWarn}
	})

	findings := []conformance.Finding{
		sampleFinding(&invoice, &store, FPActive, "CO-BND-001", nil),
		sampleFinding(&invoice, &store, FPBaselined, "CO-BND-001", func(f *conformance.Finding) {
			f.Location = &conformance.Location{Path: "services/billing/src/Refund.ts", Line: 3}
			f.RelatedLocations = []conformance.Location{}
			f.Disposition = exceptions.DispositionBaselined
			f.DispositionRef = ref("baseline")
		}),
		sampleFinding(&invoice, &store, FPExpired, "CO-CYC-001", func(f *conformance.Finding) {
			f.Kind = conformance.KindCycle
			f.Message = "dependency cycle: billing-service -> fulfillment-service -> billing-service"
			f.Source = conformance.Endpoint{ArchitectureID: "billing-service"}
			f.Target = conformance.Endpoint{ArchitectureID: "billing-service,fulfillment-service"}
			f.EvidencePath = slices.Concat(importSteps(&invoice, &store, 18), importSteps(&store, &invoice, 7))
			f.RelatedLocations = []conformance.Location{}
			f.Disposition = exceptions.DispositionExpiredWaiver
			f.DispositionRef = ref("WV-2")
		}),
		sampleFinding(&invoice, &store, FPWaived, "CO-LYR-001", func(f *conformance.Finding) {
			f.Kind = conformance.KindLayerViolation
			f.Severity = policy.SeverityWarning
			f.Message = "layer violation"
			f.RelatedLocations = []conformance.Location{}
			f.Disposition = exceptions.DispositionWaived
			f.DispositionRef = ref("WV-1")
		}),
	}
	slices.SortFunc(findings, func(a, b conformance.Finding) int {
		if c := canonjson.CompareUTF16(a.RuleID, b.RuleID); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Fingerprint, b.Fingerprint)
	})

	reason := func(code, msg string) func(*conformance.RuleResult) {
		return func(r *conformance.RuleResult) { r.Reasons = []conformance.Reason{{Code: code, Message: msg}} }
	}
	withCoverage := func(ratio, required float64, mod func(*conformance.RuleResult)) func(*conformance.RuleResult) {
		return func(r *conformance.RuleResult) {
			r.Coverage = sampleCoverage(ratio, required)

			if mod != nil {
				mod(r)
			}
		}
	}

	rules := []conformance.RuleResult{
		sampleRule(&bnd, conformance.VerdictViolation, 2, withCoverage(0.987, 0.95, nil)),
		sampleRule(&cov, conformance.VerdictUnknown, 0, withCoverage(0.5, 0.9, reason("CO-CON-021", "minimum coverage not met: 0.500 < 0.9"))),
		sampleRule(&cyc, conformance.VerdictViolation, 1, withCoverage(0.987, 0.95, nil)),
		sampleRule(&decl, conformance.VerdictPass, 0, withCoverage(0.987, 0.95, nil)),
		sampleRule(&broken, conformance.VerdictError, 0, reason("CO-CON-099", "rule evaluation error: boom")),
		sampleRule(&lyr, conformance.VerdictViolation, 1, withCoverage(0.987, 0.95, nil)),
		sampleRule(&notApplicable, conformance.VerdictNotApplicable, 0, reason("CO-CON-022", "rule selects no subject")),
		sampleRule(&warnUnknown, conformance.VerdictUnknown, 0, withCoverage(0.99, 0.95, reason("CO-CON-023", "evidence below the confidence threshold: 2 edge(s)"))),
	}
	slices.SortFunc(rules, func(a, b conformance.RuleResult) int { return canonjson.CompareUTF16(a.RuleID, b.RuleID) })

	digestOf := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }

	run := &conformance.Run{
		SchemaVersion: conformance.ResultSchemaVersion,
		Run: conformance.RunInfo{
			ID: "20261231T120000Z-deadbeef", StartedAt: "2026-12-31T12:00:00.000Z", FinishedAt: "2026-12-31T12:00:01.000Z",
			SourceRevision: ref("abc123"), DirtyTreeDigest: digestOf("0"), GraphGeneration: "cbm:demo:0123456789abcdef", Project: "demo",
			Host: conformance.RunHost{Platform: "linux", Arch: "amd64", Go: "go1.26.0"},
		},
		Toolchain: conformance.RunToolchain{
			Concordia: "0.1.0", CalmSchema: "1.2", Cbm: conformance.CbmToolchain{ExecutableVersion: "0.12.1", AdapterSchema: "concordia-cbm/v2"},
		},
		Inputs: conformance.RunInputs{
			Workspace:       conformance.InputRef{Path: "concordia.yaml", Digest: digestOf("1")},
			Architecture:    conformance.InputRef{Path: "architecture/commerce.calm.json", Digest: digestOf("2")},
			Patterns:        []conformance.InputRef{},
			Decorators:      []conformance.InputRef{},
			Policies:        []conformance.InputRef{{Path: "policies/boundaries.policy.yaml", Digest: digestOf("3")}},
			Bindings:        []conformance.InputRef{},
			QueryPlanDigest: strings.Repeat("f", 64),
		},
		Summary:        conformance.Summarize(rules, findings, diag.SeverityError, policy.UnknownFail),
		Rules:          rules,
		Findings:       findings,
		Reconciliation: []conformance.ReconciliationEntry{},
		Controls:       []conformance.ControlResult{},
		Exceptions: conformance.RunExceptions{
			ResolvedBaselineEntries: []string{},
			OrphanedWaivers:         []string{},
			Waivers: []conformance.AppliedWaiver{
				{ID: "WV-1", Rule: "CO-LYR-001", Finding: FPWaived, Owner: "team-billing", Reason: "Migration tracked in ARCH-932", Issue: "ARCH-932", Expires: "2027-12-31", State: "active"},
				{ID: "WV-2", Rule: "CO-CYC-001", Finding: FPExpired, Owner: "team-billing", Reason: "Temporary", Expires: "2026-01-31", State: "expired"},
			},
		},
		Diagnostics: []diag.Diagnostic{},
	}
	run.SettleAreas()

	return run
}
