package policy_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/testutil"
)

func TestRuleFilterKeepsOnlyTheGivenPlans(t *testing.T) {
	t.Parallel()

	body := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"
	files := []policy.PolicyFile{pack(rule(body, "CO-A-001") + rule(body, "CO-B-001"))}

	res := compile(t, files, model(), policy.CompileOverrides{RuleIDs: []string{"CO-B-001"}})
	if len(res.Plans) != 1 {
		t.Fatalf("result = %+v", res)
	}

	if res.Plans[0].RuleID != "CO-B-001" {
		t.Fatalf("result = %+v", res)
	}

	if !slices.Equal(res.RuleIDs, []string{"CO-A-001", "CO-B-001"}) {
		t.Fatalf("result = %+v", res)
	}
}

func TestRuleFilterWithAnUnknownIDGivesWRK010(t *testing.T) {
	t.Parallel()

	body := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"
	files := []policy.PolicyFile{pack(rule(body, "CO-A-001"))}

	_, err := policy.CompilePolicies(files, model(), caps, &policy.CompileOverrides{RuleIDs: []string{"CO-NOPE-001"}})

	failure := requireFailure(t, err)
	if failure.Diagnostic.Code != diag.WRK010 {
		t.Fatalf("err = %v", err)
	}

	if failure.ExitCode != diag.InvalidInput {
		t.Fatalf("err = %v", err)
	}
}

func controlsFixture(t *testing.T) (*intent.ArchitectureModel, policy.CompileResult) {
	t.Helper()

	body := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"
	controls := []intent.ControlAttachment{
		{ControlID: "c1", Scope: intent.ControlScope{Level: intent.ScopeNode, ID: "billing"}, Config: &intent.ControlConfig{Engine: "concordia", RuleIDs: []string{"CO-A-001"}}, Pointer: "/nodes/0/controls/c1"},
		{ControlID: "c2", Scope: intent.ControlScope{Level: intent.ScopeArchitecture}, Config: &intent.ControlConfig{Engine: "concordia", RuleIDs: []string{"CO-B-001", "CO-GHOST-001"}}, Pointer: "/controls/c2"},
		{ControlID: "c3", Scope: intent.ControlScope{Level: intent.ScopeArchitecture}, Engine: "opa", Pointer: "/controls/c3"},
	}
	m := model(controls...)

	return m, compile(t, []policy.PolicyFile{pack(rule(body, "CO-A-001") + rule(body, "CO-B-001") + rule(body, "CO-C-001"))}, m)
}

func TestApplyControlsReportsUnknownRuleIDsAndMarksOtherEnginesNotApplicable(t *testing.T) {
	t.Parallel()

	m, compiled := controlsFixture(t)

	res, err := policy.ApplyControls(m, compiled.Plans, policy.ApplyControlsOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}

	requireGhostRuleDiagnostic(t, &res.Diagnostics[0])

	if len(res.Plans) != 3 {
		t.Errorf("plans = %d", len(res.Plans))
	}

	requireControlRows(t, res.Controls)
}

// requireGhostRuleDiagnostic checks the CO-POL-005 diagnostic of the control c2, which names a rule that no pack declares.
func requireGhostRuleDiagnostic(t *testing.T, d *diag.Diagnostic) {
	t.Helper()

	if d.Code != diag.POL005 {
		t.Errorf("diagnostic = %+v", *d)
	}

	if d.Source.URI != "architecture/a.calm.json" {
		t.Errorf("diagnostic = %+v", *d)
	}

	if d.Source.Pointer != "/controls/c2" {
		t.Errorf("diagnostic = %+v", *d)
	}
}

// requireControlRows checks the controls of controlsFixture: the rules, whether each applies, and the scope of the first.
func requireControlRows(t *testing.T, controls []policy.ControlPlan) {
	t.Helper()

	got := make([]string, 0, len(controls))
	for i := range controls {
		c := &controls[i]
		got = append(got, fmt.Sprintf("%s %v %v", c.ControlID, c.Applicable, c.RuleIDs))
	}

	want := []string{"c1 true [CO-A-001]", "c2 true [CO-B-001 CO-GHOST-001]", "c3 false []"}
	if !slices.Equal(got, want) {
		t.Errorf("controls = %v", got)
	}

	if controls[0].Scope != (intent.ControlScope{Level: intent.ScopeNode, ID: "billing"}) {
		t.Errorf("scope = %+v", controls[0].Scope)
	}
}

func TestApplyControlsControlKeepsOnlyTheRulesOfThatControl(t *testing.T) {
	t.Parallel()

	m, compiled := controlsFixture(t)

	res, err := policy.ApplyControls(m, compiled.Plans, policy.ApplyControlsOptions{ControlID: "c1"})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Plans) != 1 {
		t.Fatalf("result = %+v", res)
	}

	if res.Plans[0].RuleID != "CO-A-001" {
		t.Fatalf("result = %+v", res)
	}

	if len(res.Controls) != 1 {
		t.Fatalf("result = %+v", res)
	}

	if res.Controls[0].ControlID != "c1" {
		t.Fatalf("result = %+v", res)
	}
}

func TestApplyControlsControlWithAnUnknownIDGivesWRK010(t *testing.T) {
	t.Parallel()

	m, compiled := controlsFixture(t)

	_, err := policy.ApplyControls(m, compiled.Plans, policy.ApplyControlsOptions{ControlID: "zzz"})

	failure := requireFailure(t, err)
	if failure.Diagnostic.Code != diag.WRK010 {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyControlsUsesKnownRuleIDsWhenRuleFilteredThePlans(t *testing.T) {
	t.Parallel()

	m, _ := controlsFixture(t)
	body := "    subject: {calmNodes: [billing]}\n    declaredOnly: {edges: [IMPORTS]}\n"
	filtered := compile(t, []policy.PolicyFile{pack(rule(body, "CO-A-001") + rule(body, "CO-B-001"))}, m, policy.CompileOverrides{RuleIDs: []string{"CO-A-001"}})

	res, err := policy.ApplyControls(m, filtered.Plans, policy.ApplyControlsOptions{KnownRuleIDs: filtered.RuleIDs})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}

	if !strings.Contains(res.Diagnostics[0].Message, "CO-GHOST-001") {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestReadPolicyFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{"policies/a.yaml": "x: 1\n"})

	got, err := policy.ReadPolicyFiles(root, []string{"policies/a.yaml"})
	if err != nil {
		t.Fatal(err)
	}

	if want := []policy.PolicyFile{{Path: "policies/a.yaml", Text: "x: 1\n"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %+v", got)
	}

	_, err = policy.ReadPolicyFiles(root, []string{"policies/none.yaml"})

	failure := requireFailure(t, err)
	if failure.Diagnostic.Code != diag.POL001 {
		t.Fatalf("err = %v", err)
	}

	if failure.ExitCode != diag.InvalidInput {
		t.Fatalf("err = %v", err)
	}
}
