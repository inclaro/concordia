package projection_test

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/projection"
)

// placementModel declares: system `sys` composed of service `a`, and `a` deployed in `cluster-a`. `cluster-b` is a
// second deployment target that CALM does not use.
func placementModel(declared bool) *intent.ArchitectureModel {
	rels := []intent.RelationshipDeclaration{
		rel("r0", intent.RelComposedOf, func(r *intent.RelationshipDeclaration) {
			r.Container = "sys"
			r.Targets = []string{"a", "b"}
		}),
	}

	rels = append(rels, rel("r1", intent.RelConnects, func(r *intent.RelationshipDeclaration) {
		r.Source = "a"
		r.Targets = []string{"b"}
		r.Description = "calls"
	}))

	if declared {
		rels = append(rels, rel("d1", intent.RelDeployedIn, func(r *intent.RelationshipDeclaration) {
			r.Container = "cluster-a"
			r.Targets = []string{"a"}
		}))
	}

	return model(
		[]intent.NodeDeclaration{
			node("sys", "system"), node("a", "service"), node("b", "service"), node("cluster-a", "network"), node("cluster-b", "network"),
		},
		rels...,
	)
}

func record(env, container, workload string) deployment.Placement {
	return deployment.Placement{Node: "a", Container: container, Environment: env, Workload: workload, HostingTarget: "ns/" + container}
}

func placedInCoverage(env string, status observation.CoverageStatus) observation.PredicateCoverage {
	return observation.PredicateCoverage{
		Predicate: string(observation.KindPlacedIn), Scope: observation.CoverageScope{Environment: env}, Status: status,
		LossModes: []observation.LossMode{}, Reasons: []string{},
	}
}

func completeIn(envs ...string) []observation.PredicateCoverage {
	out := make([]observation.PredicateCoverage, len(envs))
	for i, env := range envs {
		out[i] = placedInCoverage(env, observation.CoverageComplete)
	}

	return out
}

func renderEnvs(t *testing.T, mdl *intent.ArchitectureModel, opts *projection.DSLOptions) (string, []diag.Diagnostic) {
	t.Helper()

	var diagnostics []diag.Diagnostic

	opts.Diagnostics = &diagnostics

	dsl, err := projection.ToStructurizrDSL(mdl, nil, *opts)
	if err != nil {
		t.Fatal(err)
	}

	return dsl, diagnostics
}

// envBlock gives the lines of the deploymentEnvironment block of env, "" when the DSL has none.
func envBlock(dsl, env string) string {
	_, rest, found := strings.Cut(dsl, "    deploymentEnvironment "+projection.DSLString(env)+" {\n")
	if !found {
		return ""
	}

	block, _, _ := strings.Cut(rest, "\n    }\n")

	return block
}

var instanceTagPattern = regexp.MustCompile(`(?:containerInstance|softwareSystemInstance) (\w+) \{\n\s*tags "([^"]+)"`)

// instanceTags gives "ident status" for each instance of the block, in order.
func instanceTags(block string) []string {
	matches := instanceTagPattern.FindAllStringSubmatch(block, -1)
	out := make([]string, 0, len(matches))

	for _, m := range matches {
		out = append(out, m[1]+" "+strings.TrimPrefix(m[2], "concordia:"))
	}

	return out
}

// skips gives the CO-PRJ-008 diagnostics.
func skips(diagnostics []diag.Diagnostic) []diag.Diagnostic {
	return slices.DeleteFunc(slices.Clone(diagnostics), func(d diag.Diagnostic) bool { return d.Code != "CO-PRJ-008" })
}

// deploymentEnvTestCase is a named subtest body.
type deploymentEnvTestCase struct {
	run  func(t *testing.T)
	name string
}

// deploymentEnvTestRunCases runs each case as a parallel subtest.
func deploymentEnvTestRunCases(t *testing.T, cases []deploymentEnvTestCase) {
	t.Helper()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t)
		})
	}
}

// deploymentEnvTestPlaced wraps placements in a PlacementSet.
func deploymentEnvTestPlaced(placements ...deployment.Placement) deployment.PlacementSet {
	return deployment.PlacementSet{Placements: placements}
}

// deploymentEnvTestGaps wraps gaps in a PlacementSet.
func deploymentEnvTestGaps(gaps ...deployment.PlacementGap) deployment.PlacementSet {
	return deployment.PlacementSet{Gaps: gaps}
}

// deploymentEnvTestDeploymentView selects the deployment view only.
func deploymentEnvTestDeploymentView() map[projection.ViewKind]bool {
	return map[projection.ViewKind]bool{projection.ViewDeployment: true}
}

// deploymentEnvTestSortedTags gives the sorted instance tags of the block of env.
func deploymentEnvTestSortedTags(dsl, env string) []string {
	got := instanceTags(envBlock(dsl, env))
	slices.Sort(got)

	return got
}

// deploymentEnvTestWantTags reports got, labelled, when it differs from want.
func deploymentEnvTestWantTags(t *testing.T, label string, got []string, want ...string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %v", label, got)
	}
}

// deploymentEnvTestWantContains reports each want that the DSL lacks.
func deploymentEnvTestWantContains(t *testing.T, what, dsl string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(dsl, want) {
			t.Errorf("%s lacks %s:\n%s", what, want, dsl)
		}
	}
}

// deploymentEnvTestRender renders with the options and fails the test on error.
func deploymentEnvTestRender(t *testing.T, mdl *intent.ArchitectureModel, run *projection.RunView, opts *projection.DSLOptions) string {
	t.Helper()

	dsl, err := projection.ToStructurizrDSL(mdl, run, *opts)
	if err != nil {
		t.Fatal(err)
	}

	return dsl
}

// deploymentEnvTestBlocksInOrder: both environments have a block, and first precedes second.
func deploymentEnvTestBlocksInOrder(dsl, first, second string) bool {
	if envBlock(dsl, first) == "" || envBlock(dsl, second) == "" {
		return false
	}

	return strings.Index(dsl, "deploymentEnvironment "+projection.DSLString(first)) <=
		strings.Index(dsl, "deploymentEnvironment "+projection.DSLString(second))
}

// deploymentEnvTestDefaultIsPlain: the DSL has the Default environment, no instance tags and no status styles.
func deploymentEnvTestDefaultIsPlain(dsl string) bool {
	if !strings.Contains(dsl, `deploymentEnvironment "Default" {`) {
		return false
	}

	return len(instanceTags(dsl)) == 0 && !strings.Contains(dsl, `element "concordia:`)
}

// deploymentEnvTestViewRendered: the deployment view of env is written.
func deploymentEnvTestViewRendered(dsl, env string) bool {
	return strings.Contains(dsl, "deployment * "+projection.DSLString(env))
}

// deploymentEnvTestOmitted: neither the block nor the view of env is written.
func deploymentEnvTestOmitted(dsl, env string) bool {
	return envBlock(dsl, env) == "" && !deploymentEnvTestViewRendered(dsl, env)
}

// deploymentEnvTestBoundGapRendered: the block has the hosting target of a gap and its unknown instance, the view is
// written and no environment is skipped.
func deploymentEnvTestBoundGapRendered(dsl string, diagnostics []diag.Diagnostic) bool {
	block := envBlock(dsl, "prod")
	if !strings.Contains(block, `deploymentNode "Namespace/orders"`) || !slices.Equal(instanceTags(block), []string{"gu_a unknown"}) {
		return false
	}

	return deploymentEnvTestViewRendered(dsl, "prod") && len(skips(diagnostics)) == 0
}

func TestDeploymentEnvironments(t *testing.T) {
	t.Parallel()

	deploymentEnvTestRunCases(t, []deploymentEnvTestCase{
		{deploymentEnvTestTwoEnvironments, "Two environments"},
		{deploymentEnvTestSameNames, "Same names in two environments"},
		{deploymentEnvTestUnresolvedEnvironment, "Unknown environment stays unresolved"},
		{deploymentEnvTestAsBuilt, "As-built workspace"},
		{deploymentEnvTestAgree, "Declared and observed agree"},
		{deploymentEnvTestObservedOnly, "Observed only"},
		{deploymentEnvTestThreeReplicas, "Three replicas, one off"},
		{deploymentEnvTestPackDisabled, "Pack disabled"},
	})
}

func deploymentEnvTestTwoEnvironments(t *testing.T) {
	t.Helper()

	dsl, diagnostics := renderEnvs(t, placementModel(true), &projection.DSLOptions{
		Environments: []string{"staging", "prod"},
		Placements:   deploymentEnvTestPlaced(record("staging", "cluster-a", "Deployment/a"), record("prod", "cluster-a", "Deployment/a")),
	})

	if !deploymentEnvTestBlocksInOrder(dsl, "prod", "staging") {
		t.Errorf("want prod then staging:\n%s", dsl)
	}

	if strings.Contains(dsl, `"Default"`) || len(skips(diagnostics)) != 0 {
		t.Errorf("unexpected Default environment or diagnostics %v:\n%s", codes(diagnostics), dsl)
	}

	for _, env := range []string{"prod", "staging"} {
		deploymentEnvTestWantTags(t, env+" instances", instanceTags(envBlock(dsl, env)), "gu_a conforming")
	}

	for _, want := range []string{`deployment * "prod" "deployment-prod" {`, `deployment * "staging" "deployment-staging" {`, `element "concordia:violating" {`} {
		if !strings.Contains(dsl, want) {
			t.Errorf("DSL lacks %s", want)
		}
	}
}

func deploymentEnvTestSameNames(t *testing.T) {
	t.Helper()

	dsl, _ := renderEnvs(t, placementModel(true), &projection.DSLOptions{
		Environments: []string{"prod", "staging"}, Coverage: completeIn("prod", "staging"),
		// Only staging holds the workload; prod holds a workload of the same name in a wrong target.
		Placements: deploymentEnvTestPlaced(record("staging", "cluster-a", "Deployment/orders"), record("prod", "cluster-b", "Deployment/orders")),
	})

	deploymentEnvTestWantTags(t, "staging instances", instanceTags(envBlock(dsl, "staging")), "gu_a conforming")
	// prod: the declared instance is not conforming and the same-named workload is violating in cluster-b.
	deploymentEnvTestWantTags(t, "prod instances", deploymentEnvTestSortedTags(dsl, "prod"), "gu_a declared-only", "gu_a violating")
}

func deploymentEnvTestUnresolvedEnvironment(t *testing.T) {
	t.Helper()

	dsl, _ := renderEnvs(t, placementModel(true), &projection.DSLOptions{
		Environments: []string{"prod"}, Coverage: completeIn("prod"),
		Placements: deploymentEnvTestGaps(deployment.PlacementGap{
			Reason: deployment.ReasonEnvironmentUnresolved, Side: deployment.SideWorkload, Node: "a", Identity: "Deployment/a",
		}),
	})

	if got := instanceTags(envBlock(dsl, "prod")); !slices.Equal(got, []string{"gu_a declared-only"}) {
		t.Errorf("an unresolved fact reached prod: %v", got)
	}
}

func deploymentEnvTestAsBuilt(t *testing.T) {
	t.Helper()

	dsl, _ := renderEnvs(t, deploymentModel(), &projection.DSLOptions{
		// Placements of a declared environment never show in `Default`.
		Placements: deploymentEnvTestPlaced(record("prod", "host", "Deployment/a")),
	})

	if !deploymentEnvTestDefaultIsPlain(dsl) {
		t.Errorf("Default environment has status tags or styles:\n%s", dsl)
	}
}

func deploymentEnvTestAgree(t *testing.T) {
	t.Helper()

	dsl, _ := renderEnvs(t, placementModel(true), &projection.DSLOptions{
		Environments: []string{"prod"}, Placements: deploymentEnvTestPlaced(record("prod", "cluster-a", "Deployment/a")),
	})

	block := envBlock(dsl, "prod")
	if got := instanceTags(block); !slices.Equal(got, []string{"gu_a conforming"}) || strings.Count(block, `deploymentNode "CLUSTER-A"`) != 1 {
		t.Errorf("block:\n%s", block)
	}

	if !strings.Contains(block, `tags "concordia:deployment,concordia:conforming"`) {
		t.Errorf("the deployment target has no status tag:\n%s", block)
	}
}

func deploymentEnvTestObservedOnly(t *testing.T) {
	t.Helper()

	dsl, diagnostics := renderEnvs(t, placementModel(false), &projection.DSLOptions{
		Views: deploymentEnvTestDeploymentView(), Environments: []string{"prod"},
		Placements: deploymentEnvTestPlaced(record("prod", "cluster-a", "Deployment/a")),
	})

	block := envBlock(dsl, "prod")
	if got := instanceTags(block); !slices.Equal(got, []string{"gu_a observed-only"}) || !strings.Contains(block, `deploymentNode "CLUSTER-A"`) {
		t.Errorf("block:\n%s", block)
	}

	if len(skips(diagnostics)) != 0 {
		t.Errorf("an observed placement is skipped: %v", diagnostics)
	}
}

func deploymentEnvTestThreeReplicas(t *testing.T) {
	t.Helper()

	dsl, _ := renderEnvs(t, placementModel(true), &projection.DSLOptions{
		Environments: []string{"prod"}, Coverage: completeIn("prod"),
		Placements: deploymentEnvTestPlaced(
			record("prod", "cluster-a", "Deployment/a-1"), record("prod", "cluster-a", "Deployment/a-2"),
			record("prod", "cluster-b", "Deployment/a-3"),
		),
	})

	deploymentEnvTestWantTags(t, "instances", deploymentEnvTestSortedTags(dsl, "prod"), "gu_a conforming", "gu_a conforming", "gu_a violating")

	if !strings.Contains(envBlock(dsl, "prod"), `tags "concordia:deployment,concordia:violating"`) {
		t.Errorf("the target of the off replica is not violating")
	}
}

func deploymentEnvTestPackDisabled(t *testing.T) {
	t.Helper()

	// The DSL reads no rule result: the same placement with and without rule results and findings is one output.
	opts := &projection.DSLOptions{
		Environments: []string{"prod"}, Placements: deploymentEnvTestPlaced(record("prod", "cluster-a", "Deployment/a")),
	}

	plain := deploymentEnvTestRender(t, placementModel(true), nil, opts)
	run := &projection.RunView{Rules: []conformance.RuleResult{{Subjects: []string{"a"}, Verdict: conformance.VerdictViolation}}}
	withRules := deploymentEnvTestRender(t, placementModel(true), run, opts)

	deploymentEnvTestWantTags(t, "instances", instanceTags(envBlock(plain, "prod")), "gu_a conforming")

	if envBlock(plain, "prod") != envBlock(withRules, "prod") {
		t.Errorf("rule results change the deployment environment")
	}
}

func TestDeploymentSkip(t *testing.T) {
	t.Parallel()

	deploymentEnvTestRunCases(t, []deploymentEnvTestCase{
		{deploymentEnvTestSkipObserved, "Observed placement, no deployed-in"},
		{deploymentEnvTestSkipNoPlacement, "No placement at all"},
		{deploymentEnvTestSkipBoundGap, "Gap only, with a bound node"},
		{deploymentEnvTestSkipNoHostingTarget, "Bound node, no known hosting target"},
		{deploymentEnvTestSkipUnboundGap, "Gaps with no bound node"},
		{deploymentEnvTestSkipNoEnvironments, "Without environments and without deployed-in"},
	})
}

func deploymentEnvTestSkipObserved(t *testing.T) {
	t.Helper()

	dsl, diagnostics := renderEnvs(t, placementModel(false), &projection.DSLOptions{
		Views: deploymentEnvTestDeploymentView(), Environments: []string{"prod"},
		Placements: deploymentEnvTestPlaced(record("prod", "cluster-a", "Deployment/a")),
	})

	if envBlock(dsl, "prod") == "" || len(skips(diagnostics)) != 0 {
		t.Errorf("diagnostics %v:\n%s", codes(diagnostics), dsl)
	}
}

func deploymentEnvTestSkipNoPlacement(t *testing.T) {
	t.Helper()

	dsl, diagnostics := renderEnvs(t, placementModel(false), &projection.DSLOptions{
		Views: deploymentEnvTestDeploymentView(), Environments: []string{"staging", "prod"},
		Placements: deploymentEnvTestPlaced(record("prod", "cluster-a", "Deployment/a")),
	})

	if !deploymentEnvTestOmitted(dsl, "staging") || envBlock(dsl, "prod") == "" {
		t.Errorf("staging is not omitted alone:\n%s", dsl)
	}

	if got := skips(diagnostics); len(got) != 1 || !strings.Contains(got[0].Message, "staging") {
		t.Errorf("diagnostics = %v", got)
	}
}

func deploymentEnvTestSkipBoundGap(t *testing.T) {
	t.Helper()

	dsl, diagnostics := renderEnvs(t, placementModel(false), &projection.DSLOptions{
		Views: deploymentEnvTestDeploymentView(), Environments: []string{"prod"},
		Placements: deploymentEnvTestGaps(deployment.PlacementGap{
			Environment: "prod", Reason: deployment.ReasonBindingGap, Side: deployment.SideHostingTarget, Node: "a", Identity: "Namespace/orders",
		}),
	})

	if !deploymentEnvTestBoundGapRendered(dsl, diagnostics) {
		t.Errorf("diagnostics %v:\n%s", diagnostics, dsl)
	}
}

func deploymentEnvTestSkipNoHostingTarget(t *testing.T) {
	t.Helper()

	dsl, _ := renderEnvs(t, placementModel(false), &projection.DSLOptions{
		Environments: []string{"prod"},
		Placements: deploymentEnvTestGaps(deployment.PlacementGap{
			Environment: "prod", Reason: deployment.ReasonAmbiguousBinding, Side: deployment.SideWorkload, Node: "a", Identity: "Deployment/shared",
		}),
	})

	if !strings.Contains(envBlock(dsl, "prod"), `deploymentNode "unresolved hosting target"`) {
		t.Errorf("DSL:\n%s", dsl)
	}
}

func deploymentEnvTestSkipUnboundGap(t *testing.T) {
	t.Helper()

	dsl, diagnostics := renderEnvs(t, placementModel(false), &projection.DSLOptions{
		Views: deploymentEnvTestDeploymentView(), Environments: []string{"prod"},
		Placements: deploymentEnvTestGaps(deployment.PlacementGap{
			Environment: "prod", Reason: deployment.ReasonBindingGap, Side: deployment.SideWorkload, Identity: "Deployment/x",
		}),
	})

	if !deploymentEnvTestOmitted(dsl, "prod") || len(skips(diagnostics)) != 0 {
		t.Errorf("diagnostics %v:\n%s", diagnostics, dsl)
	}
}

func deploymentEnvTestSkipNoEnvironments(t *testing.T) {
	t.Helper()

	dsl, diagnostics := renderEnvs(t, placementModel(false), &projection.DSLOptions{Views: deploymentEnvTestDeploymentView()})
	if strings.Contains(dsl, "deployment *") || len(skips(diagnostics)) == 0 {
		t.Errorf("diagnostics %v:\n%s", codes(diagnostics), dsl)
	}
}

// deploymentEnvTestStatusCase is one placement-status scenario: the instance tags expected in env.
type deploymentEnvTestStatusCase struct {
	name     string
	env      string
	set      deployment.PlacementSet
	coverage []observation.PredicateCoverage
	want     []string
}

func deploymentEnvTestStatusCases() []deploymentEnvTestStatusCase {
	gap := func(env string, reason deployment.Reason, side, node string) deployment.PlacementGap {
		return deployment.PlacementGap{Environment: env, Reason: reason, Side: side, Node: node, Identity: "Deployment/" + node}
	}

	return []deploymentEnvTestStatusCase{
		{"Declared only", "prod", deployment.PlacementSet{}, completeIn("prod"), []string{"gu_a declared-only"}},
		{"Declared, extraction not complete", "prod", deployment.PlacementSet{}, []observation.PredicateCoverage{placedInCoverage("prod", observation.CoveragePartial)}, []string{"gu_a unknown"}},
		{
			"Only in another environment: staging", "staging",
			deployment.PlacementSet{Placements: []deployment.Placement{record("staging", "cluster-a", "Deployment/a")}},
			completeIn("prod"),
			[]string{"gu_a conforming"},
		},
		{
			"Only in another environment: prod, complete", "prod",
			deployment.PlacementSet{Placements: []deployment.Placement{record("staging", "cluster-a", "Deployment/a")}},
			completeIn("prod"),
			[]string{"gu_a declared-only"},
		},
		{
			"Only in another environment: prod, incomplete", "prod",
			deployment.PlacementSet{Placements: []deployment.Placement{record("staging", "cluster-a", "Deployment/a")}},
			nil,
			[]string{"gu_a unknown"},
		},
		{
			"Wrong target", "prod",
			deployment.PlacementSet{Placements: []deployment.Placement{record("prod", "cluster-b", "Deployment/a")}},
			completeIn("prod"),
			[]string{"gu_a declared-only", "gu_a violating"},
		},
		{
			"Reverse direction", "prod",
			deployment.PlacementSet{Placements: []deployment.Placement{{
				Node: "a", Container: "cluster-a", Environment: "prod", Workload: "Deployment/cluster-a", HostingTarget: "ns/a", Reverse: true,
			}}},
			completeIn("prod"),
			[]string{"gu_a declared-only", "gu_a violating"},
		},
		{"Incomplete extraction", "prod", deployment.PlacementSet{}, nil, []string{"gu_a unknown"}},
		{
			"Unbound hosting target", "prod",
			deployment.PlacementSet{Gaps: []deployment.PlacementGap{gap("prod", deployment.ReasonBindingGap, deployment.SideHostingTarget, "a")}},
			completeIn("prod"),
			[]string{"gu_a unknown"},
		},
		{
			"Ambiguous binding", "prod",
			deployment.PlacementSet{Gaps: []deployment.PlacementGap{
				gap("prod", deployment.ReasonAmbiguousBinding, deployment.SideWorkload, "a"),
				gap("prod", deployment.ReasonAmbiguousBinding, deployment.SideWorkload, "b"),
			}},
			completeIn("prod"),
			[]string{"gu_a unknown", "gu_b unknown"},
		},
		{
			"Conforming whatever the coverage", "prod",
			deployment.PlacementSet{Placements: []deployment.Placement{record("prod", "cluster-a", "Deployment/a")}},
			nil,
			[]string{"gu_a conforming"},
		},
	}
}

func TestPlacementStatusTags(t *testing.T) {
	t.Parallel()

	for _, test := range deploymentEnvTestStatusCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dsl, _ := renderEnvs(t, placementModel(true), &projection.DSLOptions{
				Environments: []string{"prod", "staging"}, Placements: test.set, Coverage: test.coverage,
			})

			got := deploymentEnvTestSortedTags(dsl, test.env)
			if !slices.Equal(got, test.want) {
				t.Errorf("instances = %v, want %v", got, test.want)
			}
		})
	}
}

// deploymentEnvTestRunView is a run with one finding of the disposition, a reconciliation and a rule violation.
func deploymentEnvTestRunView(disposition exceptions.Disposition) *projection.RunView {
	return &projection.RunView{
		Findings: []conformance.Finding{{
			Fingerprint: "sha256:aa", Disposition: disposition,
			Source: conformance.Endpoint{ArchitectureID: "a"}, Target: conformance.Endpoint{ArchitectureID: "b"},
		}},
		Reconciliation: []conformance.ReconciliationEntry{
			{From: "a", To: "b", Classification: conformance.ClassConforming, EdgeCount: 1},
			{From: "b", To: "a", Classification: conformance.ClassUndeclared, ObservedKinds: []observation.CanonicalEdgeKind{observation.KindImports}, EdgeCount: 1},
		},
		Rules: []conformance.RuleResult{{Subjects: []string{"a"}, Verdict: conformance.VerdictViolation}},
	}
}

// deploymentEnvTestRenderWaiver renders the placement model with a run of the disposition.
func deploymentEnvTestRenderWaiver(t *testing.T, disposition exceptions.Disposition, committed bool) string {
	t.Helper()

	return deploymentEnvTestRender(t, placementModel(true), deploymentEnvTestRunView(disposition), &projection.DSLOptions{
		Committed: committed, Environments: []string{"prod"}, Coverage: completeIn("prod"),
		Placements: deploymentEnvTestPlaced(record("prod", "cluster-b", "Deployment/a")),
		ViewIDs:    map[projection.ViewKind]string{projection.ViewDeployment: "prod-deployment"},
	})
}

// TestWaiverExpiryLeavesTheCommittedDSLIdentical: a waiver that expires changes the disposition of a finding, and
// nothing else. The committed DSL does not change; the ad-hoc DSL does (it shows the finding).
func TestWaiverExpiryLeavesTheCommittedDSLIdentical(t *testing.T) {
	t.Parallel()

	waived := deploymentEnvTestRenderWaiver(t, exceptions.DispositionWaived, true)
	expired := deploymentEnvTestRenderWaiver(t, exceptions.DispositionExpiredWaiver, true)

	if waived != expired {
		t.Errorf("a waiver expiry changes the committed DSL:\nwaived:\n%s\nexpired:\n%s", waived, expired)
	}

	for _, absent := range []string{"concordia.status", "concordia.findings", "sha256:aa"} {
		if strings.Contains(waived, absent) {
			t.Errorf("committed DSL has run state %q", absent)
		}
	}

	deploymentEnvTestWantContains(t, "committed DSL", waived,
		`gu_a -> gu_b "[conforming] calls"`, `gu_b -> gu_a "[observed-only] observed only"`, `deployment * "prod" "prod-deployment" {`)

	if deploymentEnvTestRenderWaiver(t, exceptions.DispositionWaived, false) == deploymentEnvTestRenderWaiver(t, exceptions.DispositionExpiredWaiver, false) {
		t.Error("the ad-hoc DSL ignores the disposition")
	}
}

var (
	deploymentEnvTestDocRow   = regexp.MustCompile("(?m)^\\| `concordia:([a-z-]+)` \\|")
	deploymentEnvTestStyleRow = regexp.MustCompile(`element "concordia:([a-z-]+)" \{`)
)

// deploymentEnvTestTagSet gives the first group of each match of the pattern in the text, as a set.
func deploymentEnvTestTagSet(pattern *regexp.Regexp, text string) map[string]bool {
	set := map[string]bool{}

	for _, m := range pattern.FindAllStringSubmatch(text, -1) {
		set[m[1]] = true
	}

	return set
}

// deploymentEnvTestDocumentedTags gives the status tags of the placement table of the how-to.
func deploymentEnvTestDocumentedTags(t *testing.T) map[string]bool {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "how-to", "render-diagrams.md"))
	if err != nil {
		t.Fatal(err)
	}

	_, section, found := strings.Cut(string(data), "## Read the placement status")
	if !found {
		t.Fatal("the how-to has no placement status section")
	}

	section, _, _ = strings.Cut(section, "\n## ")

	return deploymentEnvTestTagSet(deploymentEnvTestDocRow, section)
}

// TestDocumentedStatusTagsAreTheStatusConstants: every status tag of the placement table in the how-to is one of the
// five tags that the DSL writes, and the table lists all of them.
func TestDocumentedStatusTagsAreTheStatusConstants(t *testing.T) {
	t.Parallel()

	documented := deploymentEnvTestDocumentedTags(t)

	dsl, _ := renderEnvs(t, placementModel(true), &projection.DSLOptions{Environments: []string{"prod"}})
	styled := deploymentEnvTestTagSet(deploymentEnvTestStyleRow, dsl)

	if len(styled) != 5 || !maps.Equal(documented, styled) {
		t.Errorf("documented tags %v, styled tags %v", documented, styled)
	}

	for _, status := range deployment.PlacementStatuses() {
		if !documented[string(status)] {
			t.Errorf("the how-to does not document %s", status)
		}
	}
}
