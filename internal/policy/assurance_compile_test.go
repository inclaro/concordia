package policy_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
)

func effectiveAccessRule(dimension, expect, extra string) string {
	return rule("    require:\n      effectiveAccess:\n        dimension: " + dimension +
		"\n        from: {calmNodes: [billing]}\n        to: {calmNodes: [db]}\n        expect: " + expect + "\n" + extra)
}

func TestCompilesEffectiveAccess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, dimension, expect string
	}{
		{"network denied", "network", "denied"},
		{"network allowed", "network", "allowed"},
		{"authorization not granted", "authorization", "not-granted"},
		{"authorization granted", "authorization", "granted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := compileOne(t, pack(effectiveAccessRule(tc.dimension, tc.expect, "")))
			wantCodes(t, &res)

			p := res.Plans[0]
			if p.Predicate.Family != policy.FamilyEffectiveAccess || p.Predicate.Maturity != policy.MaturityAdvisory {
				t.Fatalf("predicate = %+v", p.Predicate)
			}

			if got := p.Predicate.EffectiveAccess; got.Dimension != tc.dimension || got.Expect != tc.expect || len(got.Ports) != 0 {
				t.Errorf("effective access = %+v", got)
			}

			if !slices.Equal(p.Subjects, []string{"billing"}) || !slices.Equal(p.Targets, []string{"db"}) {
				t.Errorf("subjects %v targets %v", p.Subjects, p.Targets)
			}

			if p.Predicate.Family.EdgeFindings() {
				t.Error("an effective-access rule must not be reducible to changed code")
			}
		})
	}
}

func TestEffectiveAccessKeepsSortedPorts(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(effectiveAccessRule("network", "denied", "        ports: [8080, 22]\n")))
	wantCodes(t, &res)

	if got := res.Plans[0].Predicate.EffectiveAccess.Ports; !slices.Equal(got, []int{22, 8080}) {
		t.Errorf("ports = %v", got)
	}
}

func TestRejectsAnInvalidEffectiveAccess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, rule string
	}{
		{"unknown dimension", effectiveAccessRule("bandwidth", "denied", "")},
		{"outcome of the other dimension", effectiveAccessRule("network", "granted", "")},
		{"unknown outcome", effectiveAccessRule("authorization", "maybe", "")},
		{"ports on authorization", effectiveAccessRule("authorization", "granted", "        ports: [22]\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := compileOne(t, pack(tc.rule))
			wantCodes(t, &res, diag.POL002)

			if got := policy.ThrowOnErrors(res.Diagnostics); got == nil {
				t.Fatal("no error")
			}

			if code, _ := diag.ExitCodeOf(res.Diagnostics[0].Code); code != diag.InvalidInput {
				t.Errorf("exit code = %d, want 2", code)
			}
		})
	}
}

func TestEffectiveAccessAndCheckResultRefuseARuleSubject(t *testing.T) {
	t.Parallel()

	res := compileOne(t, pack(rule("    subject: {calmNodes: [billing]}\n    require:\n      checkResult: {check: unit-tests}\n")))
	wantCodes(t, &res, diag.POL001)
}

func checkControl(checkIDs ...string) intent.ControlAttachment {
	return intent.ControlAttachment{
		ControlID: "c1", Scope: intent.ControlScope{Level: intent.ScopeNode, ID: "billing"}, Pointer: "/nodes/0/controls/c1",
		Config: &intent.ControlConfig{Engine: "concordia", CheckIDs: checkIDs},
	}
}

func TestCompilesCheckResult(t *testing.T) {
	t.Parallel()

	body := "    require:\n      checkResult:\n        check: unit-tests\n        environment: production\n        subject: {calmNodes: [billing]}\n"

	// A check that a control lists is known; so is one that the caller took from a result source.
	for _, tc := range []struct {
		name string
		m    *intent.ArchitectureModel
		o    policy.CompileOverrides
	}{
		{"control check-ids", model(checkControl("unit-tests")), policy.CompileOverrides{}},
		{"result source", model(), policy.CompileOverrides{KnownCheckIDs: []string{"unit-tests"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := compile(t, []policy.PolicyFile{pack(rule(body))}, tc.m, tc.o)
			wantCodes(t, &res)

			p := res.Plans[0]
			if p.Predicate.Family != policy.FamilyCheckResult || p.Predicate.Maturity != policy.MaturityAdvisory {
				t.Fatalf("predicate = %+v", p.Predicate)
			}

			if got := p.Predicate.CheckResult; got.Check != "unit-tests" || got.Environment != "production" {
				t.Errorf("check result = %+v", got)
			}

			if !slices.Equal(p.Subjects, []string{"billing"}) || len(p.Targets) != 0 {
				t.Errorf("subjects %v targets %v", p.Subjects, p.Targets)
			}

			if p.Predicate.Family.EdgeFindings() {
				t.Error("a check-result rule must not be reducible to changed code")
			}
		})
	}
}

func TestCheckResultWithoutASubjectHasNone(t *testing.T) {
	t.Parallel()

	m := model(checkControl("unit-tests"))
	res := compile(t, []policy.PolicyFile{pack(rule("    require:\n      checkResult: {check: unit-tests}\n"))}, m)
	wantCodes(t, &res)

	if p := res.Plans[0]; len(p.Subjects) != 0 || p.Predicate.CheckResult.Environment != "" {
		t.Errorf("plan = %+v", p)
	}
}

func TestRejectsACheckResultForAnUnknownCheck(t *testing.T) {
	t.Parallel()

	m := model(checkControl("sast"))
	res := compile(t, []policy.PolicyFile{pack(rule("    require:\n      checkResult: {check: unit-tests}\n"))}, m)
	wantCodes(t, &res, diag.POL005)

	if res.Diagnostics[0].Source == nil || res.Diagnostics[0].Source.Pointer != "/rules/0/require/checkResult/check" {
		t.Errorf("source = %+v", res.Diagnostics[0].Source)
	}

	if err := policy.ThrowOnErrors(res.Diagnostics); err == nil {
		t.Fatal("no error")
	}

	if len(res.Plans) != 0 {
		t.Errorf("plans = %+v", res.Plans)
	}
}

func TestControlPlanListsSortedCheckIDs(t *testing.T) {
	t.Parallel()

	m := model(checkControl("unit-tests", "sast", "unit-tests"))

	res, err := policy.ApplyControls(m, nil, policy.ApplyControlsOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}

	if got := res.Controls[0]; !slices.Equal(got.CheckIDs, []string{"sast", "unit-tests"}) || len(got.RuleIDs) != 0 {
		t.Errorf("control = %+v", got)
	}
}
