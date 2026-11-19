package integration_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
)

// A render that fails because of its content is a coverage gap of its environment (design D13, spec
// rendered-infrastructure-evidence "Render failure becomes a coverage gap"): the other environment goes on, the rules
// that need the failed environment are `unknown`, and the exit code follows the `onUnknown` of the rule.

const renderFailureStaging = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: payments
spec:
  selector:
    matchLabels:
      app: api
  template:
    metadata:
      labels:
        app: api
    spec:
      containers:
        - name: app
          image: registry.example.com/payments/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`

// renderFailureWorld renders staging from a valid directory and production from productionPath, with the files that
// the test adds. The `assurance` pack is on and only ASR-REN-001 runs, so that no other rule decides the result.
func renderFailureWorld(t *testing.T, productionPath string, extra map[string]string) *deployment {
	t.Helper()

	spec := (&provWorld{noRendering: true}).spec(t)
	spec.files["assurance-render/staging/deployment.yaml"] = renderFailureStaging
	maps := spec.files

	for name, text := range extra {
		maps[name] = text
	}

	maps["concordia.yaml"] += "  rendering:\n    sources:\n" +
		"      - {environment: staging, tool: plain, path: assurance-render/staging}\n" +
		"      - {environment: production, tool: plain, path: " + productionPath + "}\n"

	edit := spec.edit
	spec.edit = func(opts *conformance.CheckOptions) {
		edit(opts)

		opts.Rules = []string{"ASR-REN-001"}
	}

	return deploymentRun(t, spec)
}

func renderedEntryOf(t *testing.T, run *conformance.Run, environment string) *conformance.RenderedEntry {
	t.Helper()

	for i := range run.Assurance.Rendered {
		if run.Assurance.Rendered[i].Environment == environment {
			return &run.Assurance.Rendered[i]
		}
	}

	t.Fatalf("no rendered entry for %s", environment)

	return nil
}

// renderFailureEntryFacts is what a rendered entry says about the coverage of one environment.
type renderFailureEntryFacts struct {
	coverage assurance.Coverage
	digest   string
	count    int
	hasGaps  bool
}

func renderFailureEntryFactsOf(entry *conformance.RenderedEntry) renderFailureEntryFacts {
	return renderFailureEntryFacts{coverage: entry.Coverage, digest: entry.OutputDigest, count: entry.ResourceCount, hasGaps: len(entry.Gaps) != 0}
}

func renderFailureRequireEntries(t *testing.T, run *conformance.Run) {
	t.Helper()

	staging := renderedEntryOf(t, run, "staging")
	if staging.Coverage != assurance.CoverageComplete || staging.ResourceCount != 1 {
		t.Errorf("staging = %+v, want complete with one resource", staging)
	}

	production := renderedEntryOf(t, run, "production")
	if got := renderFailureEntryFactsOf(production); got != (renderFailureEntryFacts{coverage: assurance.CoverageFailed, hasGaps: true}) {
		t.Errorf("production = %+v, want failed with a gap and no output", production)
	}
}

func renderFailureRequireAdapterCoverage(t *testing.T, run *conformance.Run) {
	t.Helper()

	// The coverage of the rendering adapter shows the failure for production only, with the loss mode `unrendered-template`.
	set := occurrenceRecord(t, run, observation.FamilyRenderedResourceSet, "production")
	if set.Status != observation.CoverageFailed || !set.HasLossMode(observation.LossUnrenderedTemplate) {
		t.Errorf("production rendered-resource-set = %+v", set)
	}

	if set := occurrenceRecord(t, run, observation.FamilyRenderedResourceSet, "staging"); set.Status != observation.CoverageComplete {
		t.Errorf("staging rendered-resource-set = %+v", set)
	}
}

// renderFailureHoldsEvidence reports whether a render of production holds a resource or an occurrence.
func renderFailureHoldsEvidence(res *render.Result) bool {
	return res.Environment == "production" && len(res.Resources)+len(res.Occurrences) != 0
}

func renderFailureRequireNoPartialEvidence(t *testing.T, run *conformance.Run) {
	t.Helper()

	// A failed environment never has a partial resource set used as evidence.
	for _, res := range run.AssuranceEvidence.Evidence.Renders {
		if renderFailureHoldsEvidence(res) {
			t.Errorf("failed render holds evidence: %+v", res)
		}
	}
}

// renderFailureRequireVerdicts checks the rule: `pass` for staging and `unknown` for production; the reason says that
// the evidence is not usable.
func renderFailureRequireVerdicts(t *testing.T, rule *conformance.RuleResult) {
	t.Helper()

	verdicts := map[string]conformance.RuleVerdict{}
	reasons := map[string]conformance.UnknownReason{}

	for i := range rule.Obligations {
		o := &rule.Obligations[i]
		verdicts[o.Environment], reasons[o.Environment] = o.Verdict, o.Reason
	}

	if verdicts["staging"] != conformance.VerdictPass || verdicts["production"] != conformance.VerdictUnknown {
		t.Fatalf("ASR-REN-001 by environment = %v", verdicts)
	}

	usable := []conformance.UnknownReason{conformance.ReasonCoverageIncomplete, conformance.ReasonRenderedEvidenceMissing}
	if r := reasons["production"]; !slices.Contains(usable, r) {
		t.Errorf("production reason = %s", r)
	}

	if rule.Verdict != conformance.VerdictUnknown {
		t.Errorf("rule verdict = %s, want unknown", rule.Verdict)
	}
}

// renderFailureMandatory gives the rule as a mandatory rule with the disposition of an unknown result.
func renderFailureMandatory(rule *conformance.RuleResult, onUnknown policy.UnknownDisposition) conformance.RuleResult {
	gating := *rule
	gating.Enforcement = policy.EnforcementMandatory
	gating.Obligations = slices.Clone(rule.Obligations)

	for i := range gating.Obligations {
		gating.Obligations[i].Enforcement = policy.EnforcementMandatory
	}

	gating.Plan.EvidenceRequirements.OnUnknown = onUnknown

	return gating
}

// renderFailureRequireExitCodes checks that the exit code follows `onUnknown`: the rule is advisory (an assurance claim
// is never mandatory in this release), so the run does not gate; the same rule at `mandatory` gates with exit 4 under
// `fail` and does not under `warn`.
func renderFailureRequireExitCodes(t *testing.T, run *conformance.Run, rule *conformance.RuleResult) {
	t.Helper()

	if run.Summary.ExitCode != diag.OK {
		t.Errorf("exit code = %d, want 0 for an advisory rule", run.Summary.ExitCode)
	}

	for _, tc := range []struct {
		onUnknown policy.UnknownDisposition
		want      int
	}{{policy.UnknownFail, diag.Unknown}, {policy.UnknownWarn, diag.OK}, {policy.UnknownIgnore, diag.OK}} {
		gating := renderFailureMandatory(rule, tc.onUnknown)

		if code := conformance.ComputeExitCode([]conformance.RuleResult{gating}, nil, diag.SeverityError); code != tc.want {
			t.Errorf("mandatory with onUnknown %s: exit %d, want %d", tc.onUnknown, code, tc.want)
		}
	}

	if got := rule.Plan.EvidenceRequirements.OnUnknown; got != policy.UnknownFail {
		t.Errorf("onUnknown of the rule = %s, want fail (check.unknown)", got)
	}
}

func assertFailedProduction(t *testing.T, d *deployment) {
	t.Helper()

	run := d.run

	renderFailureRequireEntries(t, run)
	renderFailureRequireAdapterCoverage(t, run)
	renderFailureRequireNoPartialEvidence(t, run)

	rule := provRuleOf(t, run, "ASR-REN-001")
	renderFailureRequireVerdicts(t, rule)
	renderFailureRequireExitCodes(t, run, rule)
}

func TestOneEnvironmentFailsToRenderAndTheOtherGoesOn(t *testing.T) {
	t.Parallel()

	// A source that cannot be rendered because of its content: a document that is not a Kubernetes object.
	d := renderFailureWorld(t, "assurance-render/production", map[string]string{
		"assurance-render/production/deployment.yaml": "kind: [unterminated\n  name: {\n",
	})

	production := renderedEntryOf(t, d.run, "production")
	if production.Coverage == assurance.CoverageComplete {
		t.Fatalf("production = %+v, want a coverage below complete", production)
	}

	// An unparseable document is partial, not failed; a failed environment is covered by the missing source below.
	rule := provRuleOf(t, d.run, "ASR-REN-001")
	for _, o := range rule.Obligations {
		if o.Environment == "production" && o.Verdict != conformance.VerdictUnknown {
			t.Errorf("production obligation = %s, want unknown", o.Verdict)
		}

		if o.Environment == "staging" && o.Verdict != conformance.VerdictPass {
			t.Errorf("staging obligation = %s, want pass", o.Verdict)
		}
	}

	if warns := slices.ContainsFunc(d.run.Diagnostics, func(dg diag.Diagnostic) bool { return dg.Code == diag.PRV005 }); !warns {
		t.Error("no CO-PRV-005 warning for the render that did not complete")
	}
}

func TestAMissingDeclaredSourceIsTheSameCoverageGap(t *testing.T) {
	t.Parallel()

	d := renderFailureWorld(t, "assurance-render/does-not-exist", nil)

	assertFailedProduction(t, d)

	gap := renderedEntryOf(t, d.run, "production").Gaps[0]
	if !strings.Contains(gap.Path+gap.Detail, "does-not-exist") {
		t.Errorf("gap = %+v, want it to name the declared source", gap)
	}
}

// renderFailureSARIFURIs gives the artifact URI of the first location of every SARIF result of the rule.
func renderFailureSARIFURIs(t *testing.T, sarif []byte, rule string) []string {
	t.Helper()

	var doc struct {
		Runs []struct {
			Results []struct {
				RuleID    string `json:"ruleId"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}

	if err := json.Unmarshal(sarif, &doc); err != nil {
		t.Fatal(err)
	}

	var uris []string

	for _, result := range doc.Runs[0].Results {
		if result.RuleID == rule && len(result.Locations) > 0 {
			uris = append(uris, result.Locations[0].PhysicalLocation.ArtifactLocation.URI)
		}
	}

	return uris
}

// A finding about an environment that failed to render has no file of its own: SARIF locates it at the declared source
// of the environment, and never at a temporary path (spec "Origin unknown").
func TestSARIFLocatesAFailedRenderAtTheDeclaredSource(t *testing.T) {
	t.Parallel()

	d := renderFailureWorld(t, "assurance-render/does-not-exist", nil)

	sarif, err := reporting.Render(reporting.FormatSARIF, d.run, reporting.PrettyOptions{})
	if err != nil {
		t.Fatal(err)
	}

	uris := renderFailureSARIFURIs(t, sarif, "ASR-REN-001")

	if !slices.Contains(uris, "assurance-render/does-not-exist") {
		t.Errorf("ASR-REN-001 locations = %v, want the declared source of production", uris)
	}

	for _, uri := range uris {
		if strings.HasPrefix(uri, "/") || strings.Contains(uri, d.w.dir) {
			t.Errorf("location %q is not a repository path", uri)
		}
	}
}
