package conformance

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	checkverdict "github.com/inclaro/concordia/internal/assurance/checkresult/verdict"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestPluralOf(t *testing.T) {
	t.Parallel()

	for kind, want := range map[string]string{
		"Secret": "secrets", "ConfigMap": "configmaps", "Ingress": "ingresses", "NetworkPolicy": "networkpolicies", "Deployment": "deployments",
		// A vowel before the final `y`, a kind that is already plural, and the `ch` ending.
		"Gateway": "gateways", "Endpoints": "endpoints", "Batch": "batches",
	} {
		if got := pluralOf(kind); got != want {
			t.Errorf("pluralOf(%s) = %s, want %s", kind, got, want)
		}
	}
}

// judge applies the three gates of the spec to the answers of one pair: an open answer needs no assumption, a closed
// one needs the declared enforcement, and an unknown one keeps its reason.
func TestJudgeAppliesTheExpectationAndTheAssumptionGate(t *testing.T) {
	t.Parallel()

	const enforced = workspace.AssumeNetworkPolicyEnforced

	open := probe{state: stateOpen, label: "tcp/8080", evidence: "NetworkPolicy shop/allow", assumptions: []string{enforced}}
	closed := probe{state: stateClosed, label: "tcp/22", evidence: "NetworkPolicy shop/deny", assumptions: []string{enforced}}
	unknown := probe{state: stateUnknown, label: "tcp/9", reason: ReasonCoverageIncomplete}

	tests := []struct {
		name       string
		expect     string
		want       RuleVerdict
		wantReason UnknownReason
		assumes    []string
		probes     []probe
	}{
		{"denied expected, all closed, declared", policy.ExpectDenied, VerdictPass, "", []string{enforced}, []probe{closed}},
		{"denied expected, all closed, not declared", policy.ExpectDenied, VerdictUnknown, ReasonEnforcementNotDeclared, nil, []probe{closed}},
		{"denied expected, one open", policy.ExpectDenied, VerdictViolation, "", []string{enforced}, []probe{closed, open}},
		{"denied expected, one open, not declared", policy.ExpectDenied, VerdictViolation, "", nil, []probe{closed, open}},
		{"denied expected, closed and unknown", policy.ExpectDenied, VerdictUnknown, ReasonCoverageIncomplete, []string{enforced}, []probe{closed, unknown}},
		{"denied expected, open and unknown", policy.ExpectDenied, VerdictViolation, "", []string{enforced}, []probe{open, unknown}},
		{"allowed expected, one open", policy.ExpectAllowed, VerdictPass, "", nil, []probe{closed, open}},
		{"allowed expected, all closed, declared", policy.ExpectAllowed, VerdictViolation, "", []string{enforced}, []probe{closed}},
		{"allowed expected, all closed, not declared", policy.ExpectAllowed, VerdictUnknown, ReasonEnforcementNotDeclared, nil, []probe{closed}},
		{"allowed expected, unknown", policy.ExpectAllowed, VerdictUnknown, ReasonCoverageIncomplete, []string{enforced}, []probe{unknown}},
		{"no answer", policy.ExpectDenied, VerdictUnknown, ReasonNoEvidenceSupplied, []string{enforced}, nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ask := &accessAsk{
				plan:        &policy.RulePlan{Predicate: policy.Predicate{EffectiveAccess: &policy.EffectiveAccess{Dimension: policy.DimensionNetwork, Expect: test.expect}}},
				declaration: &workspace.AssuranceEvaluator{Assumes: test.assumes},
			}

			got, reason, detail := ask.judge(test.probes)
			if got != test.want || reason != test.wantReason {
				t.Errorf("judge = %s %q (%s), want %s %q", got, reason, detail, test.want, test.wantReason)
			}
		})
	}
}

// With no port in the rule the queries cover the ports of the destination, the numbers that policies name, and one
// port that nothing names: a policy that allows every port must not be missed because the destination declares none.
func TestDestinationPortsIncludeAProbeOutsideEveryRule(t *testing.T) {
	t.Parallel()

	end := 12
	input := &netpol.Input{Policies: []netpol.NetworkPolicy{{Spec: netpol.PolicySpec{Ingress: []netpol.IngressRule{
		{Ports: []netpol.Port{{Port: netpol.PortNumber(1), EndPort: &end}, {Port: netpol.PortName("http")}}},
	}}}}}

	got := destinationPorts(input, &netpol.Workload{Ports: []netpol.ContainerPort{{ContainerPort: 8080}}})

	seen := map[string]bool{}
	for _, port := range got {
		seen[port.String()] = true
	}

	for _, want := range []string{"tcp/1", "tcp/12", "tcp/8080", "tcp/13"} {
		if !seen[want] {
			t.Errorf("ports %v lack %s (the probe is the lowest port that no rule or range covers)", got, want)
		}
	}
}

// A failed source gives its declared namespace the coverage failed; a failed source with no declared namespace makes
// every namespace of the environment unusable, because the missing documents may select or allow anything.
func TestEnvModelCoverageOfAFailedSource(t *testing.T) {
	t.Parallel()

	complete := &render.Result{
		Environment: "production", Tool: workspace.RenderToolPlain, Path: "a", Coverage: assurance.CoverageComplete,
		Resources: []render.Resource{{ID: render.ResourceID{Kind: "Deployment", Namespace: "shop", Name: "web"}}, {ID: render.ResourceID{Kind: "Deployment", Namespace: "pay", Name: "api"}}},
	}
	failedHelm := &render.Result{Environment: "production", Tool: workspace.RenderToolHelm, Path: "chart", Coverage: assurance.CoverageFailed}
	failedPlain := &render.Result{Environment: "production", Tool: workspace.RenderToolPlain, Path: "b", Coverage: assurance.CoverageFailed}

	cfg := &workspace.Assurance{Rendering: &workspace.AssuranceRendering{Sources: []workspace.RenderSource{
		{Environment: "production", Tool: workspace.RenderToolHelm, Path: "chart", Namespace: "shop"},
		{Environment: "production", Tool: workspace.RenderToolPlain, Path: "b"},
	}}}

	named := newEnvModel("production", []*render.Result{complete, failedHelm}, cfg)
	if named.namespaces["shop"] != assurance.CoverageFailed || named.namespaces["pay"] != assurance.CoverageComplete {
		t.Errorf("namespaces = %v, want shop failed and pay complete", named.namespaces)
	}

	unnamed := newEnvModel("production", []*render.Result{complete, failedPlain}, cfg)
	if unnamed.namespaces["shop"] != assurance.CoverageFailed || unnamed.namespaces["pay"] != assurance.CoverageFailed {
		t.Errorf("namespaces = %v, want every namespace failed", unnamed.namespaces)
	}

	if !named.usable() {
		t.Error("an environment with one complete render is usable")
	}

	if (&envModel{renders: []*render.Result{failedHelm}}).usable() {
		t.Error("an environment whose renders all failed is not usable")
	}
}

// The verdict of a check for a control is the worst over the environments that its results cover.
func TestExternalChecksTakeTheWorstEnvironment(t *testing.T) {
	t.Parallel()

	outcome := func(environment, v, reason string) evidence.CheckOutcome {
		return evidence.CheckOutcome{Environment: environment, Outcome: checkverdict.Outcome{Check: "unit-tests", Verdict: v, Reason: reason}}
	}

	ev := &evidence.Evidence{CheckOutcomes: []evidence.CheckOutcome{
		outcome("production", checkverdict.Pass, ""),
		outcome("staging", checkverdict.Unknown, checkverdict.ReasonNoEvidenceSupplied),
	}}

	got := externalChecks(ev, nil)["unit-tests"]
	if got.Verdict != VerdictUnknown || got.Reason != ReasonNoEvidenceSupplied {
		t.Errorf("check = %+v, want unknown no-evidence-supplied", got)
	}

	ev.CheckOutcomes = append(ev.CheckOutcomes, outcome("canary", checkverdict.Violation, ""))

	if got := externalChecks(ev, nil)["unit-tests"]; got.Verdict != VerdictViolation {
		t.Errorf("check = %+v, want violation", got)
	}

	if _, ok := externalChecks(ev, nil)["other"]; ok {
		t.Error("a check that no result names has an entry")
	}

	// Environments that `--environment` excludes have no verdict, so their results never decide the control.
	if got := externalChecks(ev, []string{"canary", "staging"})["unit-tests"]; got.Verdict != VerdictPass {
		t.Errorf("check with canary and staging excluded = %+v, want pass", got)
	}
}

// A check-result rule gives no outcome for an environment that `--environment` excludes, and is not applicable when
// every environment of its results is excluded.
func TestCheckOutcomesLeaveOutExcludedEnvironments(t *testing.T) {
	t.Parallel()

	ev := &evidence.Evidence{CheckOutcomes: []evidence.CheckOutcome{
		{Environment: "production", Outcome: checkverdict.Outcome{Check: "unit-tests", Verdict: checkverdict.Violation}},
		{Environment: "staging", Outcome: checkverdict.Outcome{Check: "unit-tests", Verdict: checkverdict.Pass}},
	}}
	plan := &policy.RulePlan{Predicate: policy.Predicate{CheckResult: &policy.CheckResultRequirement{Check: "unit-tests"}}}

	environmentsOf := func(excluded ...string) []string {
		c := &checker{assurance: &assuranceRun{ev: ev}, environments: RunEnvironments{Excluded: excluded}}

		outcomes, ok := c.checkOutcomes(plan)
		if !ok {
			t.Fatal("checkOutcomes gave no answer")
		}

		out := make([]string, 0, len(outcomes))
		for i := range outcomes {
			out = append(out, outcomes[i].Environment)
		}

		return out
	}

	if got := environmentsOf(); !slices.Equal(got, []string{"production", "staging"}) {
		t.Errorf("environments = %v, want production and staging", got)
	}

	if got := environmentsOf("production"); !slices.Equal(got, []string{"staging"}) {
		t.Errorf("environments with production excluded = %v, want staging", got)
	}

	if got := environmentsOf("production", "staging"); len(got) != 0 {
		t.Errorf("environments with both excluded = %v, want none (not applicable)", got)
	}
}

// The renderer digest of `assurance.rendered` keeps the one `sha256:` prefix that render.Identity carries, so the
// entry matches the `rendererDigest` schema pattern.
func TestRenderedEntryKeepsTheRendererDigest(t *testing.T) {
	t.Parallel()

	const sum = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	ev := &evidence.Evidence{Renders: []*render.Result{{
		Environment: "production", Tool: "helm", Path: "deploy/chart", Coverage: assurance.CoverageComplete,
		Renderer: &render.Identity{Name: "helm", Version: "v4.3.1", SHA256: sum},
	}}}

	entries := renderedEntries(ev)
	if len(entries) != 1 || entries[0].RendererDigest != sum || entries[0].ToolVersion != "v4.3.1" {
		t.Fatalf("entries = %+v, want the renderer digest %s", entries, sum)
	}
}

// Two sources rendered by one renderer list it once in `toolchain.assurance.renderers`, sorted by name.
func TestToolchainListsEachRendererOnce(t *testing.T) {
	t.Parallel()

	helm := render.Identity{Name: "helm", Version: "v4.3.1", SHA256: "sha256:" + strings.Repeat("1", 64)}
	kustomize := render.Identity{Name: "kustomize", Version: "v5.8.3", SHA256: "sha256:" + strings.Repeat("2", 64)}

	ev := &evidence.Evidence{}
	ev.Toolchain.Renderers = []render.Identity{kustomize, helm, helm}

	c := &checker{assurance: &assuranceRun{cfg: &workspace.Assurance{}, ev: ev, report: newAssuranceReport()}}
	run := &Run{}
	c.applyAssurance(run)

	if got := run.Toolchain.Assurance.Renderers; !slices.Equal(got, []render.Identity{helm, kustomize}) {
		t.Errorf("renderers = %+v, want helm then kustomize, once each", got)
	}
}
