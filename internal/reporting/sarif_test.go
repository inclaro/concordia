package reporting_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/jsonschema"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/testutil"
)

type sarifDoc struct {
	Runs []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool struct {
		Driver sarifDriver `json:"driver"`
	} `json:"tool"`
	Original    map[string]any `json:"originalUriBaseIds"`
	Results     []sarifResult  `json:"results"`
	Invocations []invocation   `json:"invocations"`
}

type sarifDriver struct {
	Name    string                `json:"name"`
	Version string                `json:"version"`
	Rules   []sarifRuleDescriptor `json:"rules"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifRuleDescriptor struct {
	FullDescription      *sarifText        `json:"fullDescription"`
	Help                 *sarifText        `json:"help"`
	Properties           map[string]string `json:"properties"`
	ID                   string            `json:"id"`
	DefaultConfiguration struct {
		Level string `json:"level"`
	} `json:"defaultConfiguration"`
}

type sarifLocation struct {
	PhysicalLocation struct {
		Region *struct {
			StartLine int `json:"startLine"`
		} `json:"region"`
		ArtifactLocation struct {
			URI       string `json:"uri"`
			URIBaseID string `json:"uriBaseId"`
		} `json:"artifactLocation"`
	} `json:"physicalLocation"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
	Properties          map[string]any    `json:"properties"`
	Level               string            `json:"level"`
	BaselineState       string            `json:"baselineState"`
	Suppressions        []map[string]any  `json:"suppressions"`
	Locations           []sarifLocation   `json:"locations"`
	RelatedLocations    []sarifLocation   `json:"relatedLocations"`
	CodeFlows           []struct {
		ThreadFlows []struct {
			Locations []any `json:"locations"`
		} `json:"threadFlows"`
	} `json:"codeFlows"`
}

type invocation struct {
	Notifications []struct {
		Level      string `json:"level"`
		Descriptor struct {
			ID string `json:"id"`
		} `json:"descriptor"`
	} `json:"toolExecutionNotifications"`
	ExecutionSuccessful bool `json:"executionSuccessful"`
}

func sarifValidator(t *testing.T) *jsonschema.Schema {
	t.Helper()

	data, err := os.ReadFile("../../test/schemas/sarif-schema-2.1.0.json")
	if err != nil {
		t.Fatal(err)
	}

	reg := jsonschema.NewRegistry()

	id, err := reg.Add(data)
	if err != nil {
		t.Fatal(err)
	}

	schema, err := reg.Compile(id)
	if err != nil {
		t.Fatal(err)
	}

	return schema
}

func renderSarif(t *testing.T, run *conformance.Run) ([]byte, sarifDoc) {
	t.Helper()

	out, err := reporting.RenderSARIF(run)
	if err != nil {
		t.Fatal(err)
	}

	var doc sarifDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}

	return out, doc
}

func assertValid(t *testing.T, schema *jsonschema.Schema, data []byte) {
	t.Helper()

	if errs := schema.ValidateJSON(data); len(errs) > 0 {
		t.Errorf("SARIF schema errors: %v", errs)
	}
}

// sarifRuleIndex maps the ID of each rule descriptor to its place in the list.
func sarifRuleIndex(rules []sarifRuleDescriptor) map[string]int {
	byID := map[string]int{}
	for i, r := range rules {
		byID[r.ID] = i
	}

	return byID
}

// findResult is the result with the fingerprint, and fails the test when there is none.
func findResult(t *testing.T, run *sarifRun, fingerprint string) *sarifResult {
	t.Helper()

	for i := range run.Results {
		if run.Results[i].PartialFingerprints["concordia/v1"] == fingerprint {
			return &run.Results[i]
		}
	}

	t.Fatalf("no result for %s", fingerprint)

	return nil
}

// levelsByFingerprint maps the fingerprint of each result to its level.
func levelsByFingerprint(results []sarifResult) map[string]string {
	levels := map[string]string{}
	for i := range results {
		levels[results[i].PartialFingerprints["concordia/v1"]] = results[i].Level
	}

	return levels
}

// assertSarifDriver checks the tool name and version, and the descriptors that the rules of the sample run get.
func assertSarifDriver(t *testing.T, run *sarifRun) {
	t.Helper()

	equal(t, run.Tool.Driver.Name, "Concordia")
	equal(t, run.Tool.Driver.Version, "0.1.0")

	byID := sarifRuleIndex(run.Tool.Driver.Rules)
	bnd := run.Tool.Driver.Rules[byID["CO-BND-001"]]
	contains(t, bnd.FullDescription.Text, "published fulfillment contract")
	contains(t, bnd.Help.Text, "packages/fulfillment-contract")
	equal(t, bnd.Properties["family"], "boundary")
	equal(t, bnd.Properties["policy"], "commerce-boundaries@1.0.0")
	equal(t, run.Tool.Driver.Rules[byID["CO-DECL-001"]].DefaultConfiguration.Level, "warning")
}

// assertActiveResult checks the result of the active finding of the sample run.
func assertActiveResult(t *testing.T, active *sarifResult) {
	t.Helper()

	equal(t, active.Level, "error")
	equal(t, active.BaselineState, "new")
	equal(t, len(active.Suppressions), 0)

	pl := active.Locations[0].PhysicalLocation
	equal(t, pl.ArtifactLocation.URI, "services/billing/src/InvoiceService.ts")
	equal(t, pl.ArtifactLocation.URIBaseID, "SRCROOT")
	equal(t, pl.Region.StartLine, 18)
	equal(t, active.RelatedLocations[0].PhysicalLocation.ArtifactLocation.URI, "services/billing/src/Refund.ts")
	equal(t, len(active.CodeFlows[0].ThreadFlows[0].Locations), 3)
	equal(t, active.Properties["sourceArchitectureId"], any("billing-service"))
	equal(t, active.Properties["targetArchitectureId"], any("fulfillment-service"))
	equal(t, active.Properties["disposition"], any("active"))
	equal(t, active.Properties["owner"], any("team-billing"))
	equal(t, active.Properties["confidence"], any("syntactic"))
}

// assertBaselinedResult checks the result of the baselined finding of the sample run.
func assertBaselinedResult(t *testing.T, baselined *sarifResult) {
	t.Helper()

	equal(t, baselined.BaselineState, "unchanged")
	equal(t, len(baselined.Suppressions), 1)
	equal(t, baselined.Suppressions[0]["justification"], any("baseline"))
	equal(t, baselined.Suppressions[0]["kind"], any("external"))
	equal(t, baselined.Suppressions[0]["status"], any("accepted"))
}

// assertSarifInvocation checks the invocation of the sample run: it did not succeed, and its notifications are the
// unknown and erroring rules.
func assertSarifInvocation(t *testing.T, run *sarifRun) {
	t.Helper()

	equal(t, run.Invocations[0].ExecutionSuccessful, false)

	notes := make([]string, 0, len(run.Invocations[0].Notifications))
	for _, n := range run.Invocations[0].Notifications {
		notes = append(notes, n.Descriptor.ID+":"+n.Level)
	}

	equal(t, strings.Join(notes, ","), "CO-COV-001:warning,CO-ERR-001:error,CO-WARN-001:warning")
}

func TestSARIF(t *testing.T) {
	t.Parallel()

	schema := sarifValidator(t)

	t.Run("agrees with the SARIF 2.1.0 schema (draft-04)", func(t *testing.T) {
		t.Parallel()

		out, _ := renderSarif(t, testutil.SampleRun())
		assertValid(t, schema, out)
	})

	t.Run("agrees with the schema for a run with no rules and no findings", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		run.Rules, run.Findings = nil, nil
		run.Summary = conformance.Summarize(nil, nil, diag.SeverityError, policy.UnknownFail)
		out, _ := renderSarif(t, run)
		assertValid(t, schema, out)
	})

	t.Run("maps findings: level, locations, codeFlows, fingerprints, properties", func(t *testing.T) {
		t.Parallel()

		_, doc := renderSarif(t, testutil.SampleRun())
		run := &doc.Runs[0]
		assertSarifDriver(t, run)
		equal(t, len(run.Results), 4)
		assertActiveResult(t, findResult(t, run, testutil.FPActive))
		assertBaselinedResult(t, findResult(t, run, testutil.FPBaselined))

		waived := findResult(t, run, testutil.FPWaived)
		equal(t, waived.Level, "warning")
		equal(t, waived.Suppressions[0]["justification"], any("Migration tracked in ARCH-932"))

		expired := findResult(t, run, testutil.FPExpired)
		equal(t, len(expired.Suppressions), 0)
		equal(t, expired.Properties["disposition"], any("expired-waiver"))

		assertSarifInvocation(t, run)
		equal(t, len(run.Original), 0)
	})

	t.Run("executionSuccessful is true unless an internal error (exit 7)", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		run.Summary.ExitCode = 1
		_, doc := renderSarif(t, run)
		equal(t, doc.Runs[0].Invocations[0].ExecutionSuccessful, true)
	})

	t.Run("never writes an absolute path or a Windows separator", func(t *testing.T) {
		t.Parallel()

		out, _ := renderSarif(t, testutil.SampleRun())
		notContains(t, string(out), `"uri": "/`)
		notContains(t, string(out), `\\`)
	})

	t.Run("percent-encodes a path with a space", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		f := run.Findings[0]
		f.Location = &conformance.Location{Path: "services/my billing/a b.ts", Line: 2}
		f.RelatedLocations = []conformance.Location{}
		f.EvidencePath = []conformance.EvidenceStep{}
		run.Findings = []conformance.Finding{f}

		out, doc := renderSarif(t, run)
		equal(t, doc.Runs[0].Results[0].Locations[0].PhysicalLocation.ArtifactLocation.URI, "services/my%20billing/a%20b.ts")
		assertValid(t, schema, out)
	})
}

// infraFinding is a finding of a Kubernetes manifest: the evidence runs from the manifest of the workload to the image.
func infraFinding(mode policy.Enforcement) conformance.Finding {
	manifest := observation.ObservedNode{ObservedID: "cbm:demo:deployment", Label: "Resource", QualifiedName: "demo.k8s.prod.deployment.yaml.Deployment.api", FilePath: "k8s/prod/deployment.yaml", StartLine: 7}
	image := observation.ObservedNode{ObservedID: "cbm:demo:image", Label: "Image", QualifiedName: "__image__registry.example.com/payments/api"}
	loc := conformance.Location{Path: "k8s/prod/deployment.yaml", Line: 42}

	return conformance.Finding{
		Fingerprint: "sha256:" + strings.Repeat("a", 64), RuleID: "CO-TYP-001", Severity: policy.SeverityError, Kind: conformance.KindObligationViolation,
		Message: "obligation forbidden api (environment production): mutable image tag", Disposition: exceptions.DispositionActive,
		Source: conformance.Endpoint{ArchitectureID: "api"}, Target: conformance.Endpoint{ArchitectureID: "registry"},
		EvidencePath: []conformance.EvidenceStep{
			{Node: &manifest},
			{Edge: &conformance.StepEdge{Kind: observation.KindUsesImage, NativeKind: "USAGE", Confidence: observation.ConfidenceSyntactic, From: manifest.ObservedID, To: image.ObservedID, Location: &loc}},
			{Node: &image},
		},
		RelatedLocations: []conformance.Location{}, Location: &loc,
		ObligationID: "0123456789abcdef", Subject: "api", Environment: "production", Result: conformance.VerdictViolation,
		NextAction: "fix implementation", Area: conformance.AreaConformance, Enforcement: mode,
		Expected: "forbidden uses-image", Observed: "uses-image fact", Related: []conformance.RelatedRef{},
		ProviderLimits: conformance.ProviderLimits{Families: []conformance.FamilyLimit{}},
	}
}

// assertManifestLocation checks that a location is the manifest of the infrastructure finding, at the line of its edge.
func assertManifestLocation(t *testing.T, loc *sarifLocation) {
	t.Helper()

	equal(t, loc.PhysicalLocation.ArtifactLocation.URI, "k8s/prod/deployment.yaml")
	equal(t, loc.PhysicalLocation.Region.StartLine, 42)
}

func TestSARIFInfrastructureFindings(t *testing.T) {
	t.Parallel()

	schema := sarifValidator(t)

	t.Run("OUT-01 the location of a finding from a Kubernetes manifest is the manifest", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		run.Findings = []conformance.Finding{infraFinding(policy.EnforcementMandatory)}
		out, doc := renderSarif(t, run)
		assertValid(t, schema, out)
		assertManifestLocation(t, &doc.Runs[0].Results[0].Locations[0])
	})

	t.Run("a finding with no location of its own takes the place of its evidence, the manifest", func(t *testing.T) {
		t.Parallel()

		finding := infraFinding(policy.EnforcementMandatory)
		finding.Location = nil
		run := testutil.SampleRun()
		run.Findings = []conformance.Finding{finding}
		_, doc := renderSarif(t, run)
		assertManifestLocation(t, &doc.Runs[0].Results[0].Locations[0])
	})

	t.Run("properties carry the next action, the area, and the obligation", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		run.Findings = []conformance.Finding{infraFinding(policy.EnforcementMandatory)}
		_, doc := renderSarif(t, run)

		props := doc.Runs[0].Results[0].Properties
		equal(t, props["nextAction"], any("fix implementation"))
		equal(t, props["area"], any("conformance"))
		equal(t, props["enforcement"], any("mandatory"))
		equal(t, props["obligationId"], any("0123456789abcdef"))
		equal(t, props["subject"], any("api"))
		equal(t, props["environment"], any("production"))
		equal(t, props["result"], any("violation"))
	})

	t.Run("the level follows the enforcement: advisory is a note, mandatory keeps the severity", func(t *testing.T) {
		t.Parallel()

		run := testutil.SampleRun()
		advisory := infraFinding(policy.EnforcementAdvisory)
		advisory.Fingerprint = "sha256:" + strings.Repeat("b", 64)
		run.Findings = []conformance.Finding{infraFinding(policy.EnforcementMandatory), advisory}
		_, doc := renderSarif(t, run)

		levels := levelsByFingerprint(doc.Runs[0].Results)
		equal(t, levels["sha256:"+strings.Repeat("a", 64)], "error")
		equal(t, levels["sha256:"+strings.Repeat("b", 64)], "note")
	})
}
