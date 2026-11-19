package integration_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
)

// The INFRA-TRC rules of the `infrastructure` pack on the deployment overlay of the infra fixture
// (specs/deployment-trace, "Trace rules").

const (
	// trcArchitecture adds reports-service (workload k8s/prod/reports.yaml) and chart-service (the templated chart
	// workload) to the default model. Both are `deployable`.
	trcArchitecture = `{
  "$schema": "https://calm.finos.org/release/1.2/meta/calm.json",
  "nodes": [
    {"unique-id": "api-service", "node-type": "service", "name": "API service", "description": "The payments API.", "concordia": {"tags": ["deployable"]}},
    {"unique-id": "worker-service", "node-type": "service", "name": "Worker service", "description": "The payments worker.", "concordia": {"tags": ["deployable"]}},
    {"unique-id": "reports-service", "node-type": "service", "name": "Reports service", "description": "The reports service.", "concordia": {"tags": ["deployable"]}},
    {"unique-id": "chart-service", "node-type": "service", "name": "Chart service", "description": "The Helm chart API.", "concordia": {"tags": ["deployable"]}},
    {"unique-id": "prod-cluster", "node-type": "system", "name": "Production cluster", "description": "The production hosting target."}
  ],
  "relationships": [
    {"unique-id": "services-in-prod", "relationship-type": {"deployed-in": {"container": "prod-cluster", "nodes": ["api-service", "worker-service", "reports-service", "chart-service"]}}}
  ]
}
`

	// trcReportsBindings binds the reports workload of production and the reports image to reports-service.
	trcReportsBindings = `  - id: bind-reports-workload
    architectureRef: calm:node:reports-service
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: reports, namespace: payments}
  - id: bind-reports-image
    architectureRef: calm:node:reports-service
    match:
      any:
        - {kind: image, repository: registry.example.com/payments/reports}
`

	// trcChartBindings binds the templated chart workload of production to chart-service.
	trcChartBindings = `  - id: bind-chart-workload
    architectureRef: calm:node:chart-service
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: "<.Release.Name>-api"}
`

	// trcChartEnvironments adds the chart directory to the sources of production, so that its partly parsed template
	// limits the workload set of the environment.
	trcChartEnvironments = `environments:
  - id: production
    cluster: prod-eu
    sources: ["k8s/prod/**", "charts/**"]
  - id: staging
    sources: ["k8s/staging/**"]
`

	// trcWorkerlessBindings is the default binding list without the worker workload binding.
	trcWorkerlessBindings = `apiVersion: concordia.io/v1alpha1
kind: BindingSet
bindings:
  - id: bind-api-workload
    architectureRef: calm:node:api-service
    match:
      any:
        - {kind: workload, environment: production, resourceKind: Deployment, name: api, namespace: payments}
        - {kind: workload, environment: staging, resourceKind: Deployment, name: api, namespace: payments-staging}
  - id: bind-api-image
    architectureRef: calm:node:api-service
    match:
      any:
        - {kind: image, repository: registry.example.com/payments/api}
  - id: bind-payments-namespace
    architectureRef: calm:node:prod-cluster
    match:
      any:
        - {kind: infraResource, environment: production, address: core/Namespace/_/payments}
`
)

// trcOverlay is the spec of the overlay with reports-service and chart-service.
func trcOverlay() *deploymentSpec {
	return &deploymentSpec{architecture: trcArchitecture, extraBindings: trcReportsBindings}
}

// trcKey names one obligation: the rule, the subject, and the environment.
type trcKey struct {
	rule, subject, env string
}

// trcOutcome is the expected verdict and reason of an obligation, and the texts its detail or occurrences show.
type trcOutcome struct {
	verdict conformance.RuleVerdict
	reason  conformance.UnknownReason
	details []string
}

// trcWant checks the verdict, the reason, the next action, and the detail of an obligation, and returns it.
func trcWant(t *testing.T, d *deployment, key trcKey, want trcOutcome) *conformance.ObligationResult {
	t.Helper()

	ob := d.obligationOf(t, key.rule, key.subject, key.env)
	if ob.Verdict != want.verdict || ob.Reason != want.reason {
		t.Errorf("%s %s/%s = %s (reason %q), want %s (reason %q): %s", key.rule, key.subject, key.env, ob.Verdict, ob.Reason, want.verdict, want.reason, ob.Detail)
	}

	for _, text := range want.details {
		if !strings.Contains(ob.Detail, text) && !trcOccurrenceDetail(ob, text) {
			t.Errorf("%s %s/%s detail %q lacks %q", key.rule, key.subject, key.env, ob.Detail, text)
		}
	}

	return ob
}

func trcPass(t *testing.T, d *deployment, key trcKey) *conformance.ObligationResult {
	t.Helper()

	return trcWant(t, d, key, trcOutcome{verdict: conformance.VerdictPass})
}

func trcOccurrenceDetail(ob *conformance.ObligationResult, want string) bool {
	for i := range ob.Occurrences {
		if strings.Contains(ob.Occurrences[i].Detail, want) {
			return true
		}
	}

	return false
}

func trcScope(env string) observation.CoverageScope {
	return observation.CoverageScope{Environment: env}
}

// trcSubtest is one named parallel subtest of a trace rule.
type trcSubtest struct {
	run  func(t *testing.T)
	name string
}

func trcRunSubtests(t *testing.T, cases []trcSubtest) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t)
		})
	}
}

// trcDeclaredParts are the texts that `policy verify` shows for each INFRA-TRC rule.
func trcDeclaredParts() map[string][]string {
	return map[string][]string{
		"INFRA-TRC-001": {"family=deployment-trace", "required exists from-workload placed-in", "roles=parsed-source-fact", "completeness=complete", "passCompleteness=any"},
		"INFRA-TRC-002": {"family=deployment-trace", "required forAll from-workload uses-image", "roles=parsed-source-fact", "completeness=complete", "passCompleteness=partial"},
		"INFRA-TRC-003": {"family=deployment-trace", "required forAll from-image built-from", "roles=parsed-source-fact", "completeness=complete", "passCompleteness=any"},
		"INFRA-TRC-004": {"family=deployment-trace", "required forAll from-workload uses-image", "roles=parsed-source-fact", "completeness=complete"},
	}
}

// trcVerifyLines maps each INFRA-TRC rule id of the `policy verify` output to the rest of its line.
func trcVerifyLines(stdout string) map[string]string {
	lines := map[string]string{}

	for line := range strings.SplitSeq(stdout, "\n") {
		id, rest, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && strings.HasPrefix(id, "INFRA-TRC-") {
			lines[id] = rest
		}
	}

	return lines
}

func assertTrcDeclaredLines(t *testing.T, lines map[string]string, stdout string) {
	t.Helper()

	for id, parts := range trcDeclaredParts() {
		line, ok := lines[id]
		if !ok {
			t.Errorf("policy verify does not show %s:\n%s", id, stdout)

			continue
		}

		for _, part := range parts {
			if !strings.Contains(line, part) {
				t.Errorf("%s line lacks %q: %s", id, part, line)
			}
		}
	}
}

func TestTraceRulesAreDeclared(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})

	verify := d.cli(t, "policy", "verify")
	if verify.code != 0 {
		t.Fatalf("policy verify exit = %d: %s%s", verify.code, verify.stdout, verify.stderr)
	}

	lines := trcVerifyLines(verify.stdout)
	assertTrcDeclaredLines(t, lines, verify.stdout)

	// INFRA-TRC-004 leaves passCompleteness unset.
	if strings.Contains(lines["INFRA-TRC-004"], "passCompleteness") {
		t.Errorf("INFRA-TRC-004 sets passCompleteness: %s", lines["INFRA-TRC-004"])
	}

	// The same declarations as JSON.
	asJSON := d.cli(t, "policy", "verify", "--format", "json")
	for _, part := range []string{`"id": "INFRA-TRC-001"`, `"multiplicity": "exists"`, `"passCompleteness": "partial"`, `"direction": "from-image"`, `"kind": "built-from"`} {
		if !strings.Contains(asJSON.stdout, part) {
			t.Errorf("policy verify --format json lacks %s", part)
		}
	}
}

// trcUsesWorkload reports whether an occurrence result of production relies on a workload.
func trcUsesWorkload(occ *conformance.OccurrenceResult) bool {
	if occ.Result == conformance.VerdictPass {
		return true
	}

	return occ.Anchor != "" || occ.Class != conformance.OccurrenceAbsent
}

func TestTraceRulesGiveAResultInEachEnvironment(t *testing.T) {
	t.Parallel()

	// The worker workload exists in staging only: production has none. Concordia does not use it for production.
	d := deploymentRun(t, &deploymentSpec{})

	staging := trcPass(t, d, trcKey{"INFRA-TRC-001", "worker-service", "staging"})
	production := trcWant(t, d, trcKey{"INFRA-TRC-001", "worker-service", "production"}, trcOutcome{verdict: conformance.VerdictViolation})

	if staging.Environment == production.Environment {
		t.Fatalf("one result for both environments: %q", staging.Environment)
	}

	for i := range production.Occurrences {
		if trcUsesWorkload(&production.Occurrences[i]) {
			t.Errorf("production result uses a workload: %+v", production.Occurrences[i])
		}
	}

	if len(staging.Occurrences) == 0 || staging.Occurrences[0].Anchor == "" {
		t.Errorf("staging result has no workload: %+v", staging.Occurrences)
	}
}

func trcAssertWorkloadSetStatus(t *testing.T, d *deployment, want observation.CoverageStatus) {
	t.Helper()

	set := predicateRecord(t, d.run, "deployment-workload-set", trcScope("production"))
	if set.Status != want {
		t.Fatalf("production deployment-workload-set = %s, want %s", set.Status, want)
	}
}

func trcSingleDecisiveMatch(occs []conformance.OccurrenceResult) bool {
	if len(occs) != 1 {
		return false
	}

	return occs[0].Class == conformance.OccurrenceMatching && occs[0].Decisive
}

func trc001Present(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})
	ob := trcPass(t, d, trcKey{"INFRA-TRC-001", "api-service", "production"})

	if !trcSingleDecisiveMatch(ob.Occurrences) {
		t.Errorf("occurrences = %+v", ob.Occurrences)
	}

	trcPass(t, d, trcKey{"INFRA-TRC-001", "api-service", "staging"})
}

func trc001PresentWithPartialCoverage(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{environments: trcChartEnvironments})

	trcAssertWorkloadSetStatus(t, d, observation.CoveragePartial)
	trcPass(t, d, trcKey{"INFRA-TRC-001", "api-service", "production"})
}

func trc001AbsentCompleteCoverage(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	trcAssertWorkloadSetStatus(t, d, observation.CoverageComplete)
	trcWant(t, d, trcKey{"INFRA-TRC-001", "worker-service", "production"}, trcOutcome{verdict: conformance.VerdictViolation, details: []string{"complete"}})
}

// trcUnknownAbsence reports whether an occurrence is the unknown absence that asks for evidence.
func trcUnknownAbsence(occ *conformance.OccurrenceResult) bool {
	if occ.Result != conformance.VerdictUnknown || occ.Class != conformance.OccurrenceAbsent {
		return false
	}

	return occ.NextAction == "supply evidence"
}

func trc001AbsentIncompleteExtraction(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{environments: trcChartEnvironments})

	ob := trcWant(t, d, trcKey{"INFRA-TRC-001", "worker-service", "production"}, trcOutcome{
		verdict: conformance.VerdictUnknown, reason: conformance.ReasonCoverageIncomplete,
		details: []string{"placed-in is partial", "parse-partial"},
	})
	if len(ob.Occurrences) == 0 {
		t.Fatal("no occurrence result")
	}

	for i := range ob.Occurrences {
		if !trcUnknownAbsence(&ob.Occurrences[i]) {
			t.Errorf("occurrence = %+v", ob.Occurrences[i])
		}
	}
}

func trcHasUnboundWorkerNode(d *deployment) bool {
	nodes := d.run.Evidence.Nodes

	for id := range nodes {
		if nodes[id].FilePath == "k8s/staging/worker.yaml" && nodes[id].Label == "Resource" {
			return true
		}
	}

	return false
}

func trc001ServiceWithNoWorkloadBinding(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{bindings: trcWorkerlessBindings})
	absent := trcOutcome{verdict: conformance.VerdictViolation, details: []string{"absent"}}

	// The staging worker exists, but no binding names worker-service: Concordia does not bind by name.
	trcWant(t, d, trcKey{"INFRA-TRC-001", "worker-service", "staging"}, absent)
	trcWant(t, d, trcKey{"INFRA-TRC-001", "worker-service", "production"}, absent)

	// With an incomplete extraction the same absence is unknown.
	partial := deploymentRun(t, &deploymentSpec{bindings: trcWorkerlessBindings, environments: trcChartEnvironments})
	trcWant(t, partial, trcKey{"INFRA-TRC-001", "worker-service", "production"}, trcOutcome{verdict: conformance.VerdictUnknown, reason: conformance.ReasonCoverageIncomplete})

	// The workload stays visible as an entity without a binding.
	if !trcHasUnboundWorkerNode(d) {
		t.Error("the unbound worker workload is not among the observed nodes")
	}
}

func TestTrace001Presence(t *testing.T) {
	t.Parallel()

	trcRunSubtests(t, []trcSubtest{
		{name: "present", run: trc001Present},
		{name: "present with partial coverage", run: trc001PresentWithPartialCoverage},
		{name: "absent, complete coverage", run: trc001AbsentCompleteCoverage},
		{name: "absent, incomplete extraction", run: trc001AbsentIncompleteExtraction},
		{name: "service with no workload binding", run: trc001ServiceWithNoWorkloadBinding},
	})
}

func trc002ImagePresentDespiteMergedOccurrences(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	// k8s/prod/deployment.yaml has two containers of one repository: the provider kept one uses-image fact.
	uses := predicateRecord(t, d.run, "uses-image", trcScope("production"))
	if !uses.HasLossMode(observation.LossMergedOccurrences) {
		t.Fatalf("production uses-image has no merged-occurrences gap: %+v", uses.LossModes)
	}

	ob := trcPass(t, d, trcKey{"INFRA-TRC-002", "api-service", "production"})
	if len(ob.Occurrences) == 0 || ob.Occurrences[0].Class != conformance.OccurrenceMatching {
		t.Errorf("occurrences = %+v", ob.Occurrences)
	}

	trcPass(t, d, trcKey{"INFRA-TRC-002", "api-service", "staging"})
}

func trc002TemplatedImage(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, withTrcChart(trcOverlay()))

	ob := trcWant(t, d, trcKey{"INFRA-TRC-002", "chart-service", "production"}, trcOutcome{
		verdict: conformance.VerdictUnknown, reason: conformance.ReasonCoverageIncomplete,
		details: []string{"uses-image is partial"},
	})

	for i := range ob.Occurrences {
		if occ := &ob.Occurrences[i]; occ.Result == conformance.VerdictPass || occ.Class == conformance.OccurrenceMatching {
			t.Errorf("the image is guessed: %+v", ob.Occurrences[i])
		}
	}
}

func TestTrace002ImageReference(t *testing.T) {
	t.Parallel()

	trcRunSubtests(t, []trcSubtest{
		{name: "image present despite merged occurrences", run: trc002ImagePresentDespiteMergedOccurrences},
		{name: "templated image", run: trc002TemplatedImage},
	})
}

// withTrcChart puts the chart directory into the sources of production.
func withTrcChart(spec *deploymentSpec) *deploymentSpec {
	spec.environments = trcChartEnvironments
	spec.extraBindings += trcChartBindings

	return spec
}

// trcMatchingBuiltFrom reports whether the first occurrence is a matching built-from fact.
func trcMatchingBuiltFrom(occs []conformance.OccurrenceResult) bool {
	if len(occs) == 0 {
		return false
	}

	return occs[0].Class == conformance.OccurrenceMatching && occs[0].Kind == "built-from"
}

func trc003CandidatePresentInBothEnvironments(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	for _, env := range []string{"production", "staging"} {
		ob := trcPass(t, d, trcKey{"INFRA-TRC-003", "api-service", env})
		if !trcMatchingBuiltFrom(ob.Occurrences) {
			t.Errorf("%s occurrences = %+v", env, ob.Occurrences)
		}
	}

	// The build command names registry.example.com/payments/api:1.2.0.
	if len(evidenceEdges(d.run, "built-from")) == 0 {
		t.Error("no built-from fact")
	}
}

func trc003NoImageBinding(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})
	notApplicable := trcOutcome{verdict: conformance.VerdictNotApplicable, details: []string{"no image binding names the subject"}}

	for _, env := range []string{"production", "staging"} {
		ob := trcWant(t, d, trcKey{"INFRA-TRC-003", "worker-service", env}, notApplicable)
		if ob.Verdict == conformance.VerdictViolation {
			t.Errorf("%s: violation for a third-party image", env)
		}
	}
}

func trc003NoBuildCommand(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, trcOverlay())

	built := predicateRecord(t, d.run, "built-from", trcScope("production"))
	if !built.HasLossMode(observation.LossBuildLinkNeedsBuildCommand) {
		t.Fatalf("built-from lacks the loss mode: %+v", built.LossModes)
	}

	ob := trcWant(t, d, trcKey{"INFRA-TRC-003", "reports-service", "production"}, trcOutcome{
		verdict: conformance.VerdictUnknown, reason: conformance.ReasonCoverageIncomplete,
		details: []string{observation.LossBuildLinkNeedsBuildCommand},
	})
	if ob.Verdict == conformance.VerdictViolation {
		t.Error("an absent build link is never a violation")
	}
}

func TestTrace003BuildDefinition(t *testing.T) {
	t.Parallel()

	trcRunSubtests(t, []trcSubtest{
		{name: "candidate present in both environments", run: trc003CandidatePresentInBothEnvironments},
		{name: "no image binding", run: trc003NoImageBinding},
		{name: "no build command", run: trc003NoBuildCommand},
	})
}

func trc004MergedOccurrencesGap(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	// api-service/production has a uses-image fact (TRC-002 passes) and a merged-occurrences gap: TRC-004 is not pass.
	trcPass(t, d, trcKey{"INFRA-TRC-002", "api-service", "production"})

	ob := trcWant(t, d, trcKey{"INFRA-TRC-004", "api-service", "production"}, trcOutcome{
		verdict: conformance.VerdictUnknown, reason: conformance.ReasonCoverageIncomplete,
		details: []string{"merged-occurrences"},
	})
	if ob.Verdict == conformance.VerdictPass {
		t.Error("INFRA-TRC-004 passes with an open merged-occurrences gap")
	}
}

func trc004LossyUsesImageFacts(t *testing.T) {
	t.Helper()

	d := deploymentRun(t, &deploymentSpec{})

	// The staging uses-image coverage has no merged-occurrences gap, yet the profile marks the facts possibly lossy.
	uses := predicateRecord(t, d.run, "uses-image", trcScope("staging"))
	if uses.HasLossMode(observation.LossMergedOccurrences) {
		t.Fatalf("staging uses-image has a merged-occurrences gap: %+v", uses.LossModes)
	}

	for _, subject := range []string{"api-service", "worker-service"} {
		ob := d.obligationOf(t, "INFRA-TRC-004", subject, "staging")
		if ob.Verdict == conformance.VerdictPass {
			t.Errorf("INFRA-TRC-004 %s/staging passes", subject)
		}

		if ob.Verdict != conformance.VerdictUnknown || ob.Reason != conformance.ReasonCoverageIncomplete {
			t.Errorf("INFRA-TRC-004 %s/staging = %s (%s): %s", subject, ob.Verdict, ob.Reason, ob.Detail)
		}
	}
}

func TestTrace004OccurrenceCoverage(t *testing.T) {
	t.Parallel()

	trcRunSubtests(t, []trcSubtest{
		{name: "merged-occurrences gap", run: trc004MergedOccurrencesGap},
		{name: "lossy uses-image facts", run: trc004LossyUsesImageFacts},
	})
}
