package e2e_test

import (
	"slices"
	"strings"
	"testing"
)

// asrStabilityExitCase is a workspace edit that stops the run with the exit code and diagnostic that the catalog gives.
type asrStabilityExitCase struct {
	edit func(*asrWorld)
	name string
	diag string
	args []string
	env  []string
	code int
}

// Task 11.4: the paths that the adapters add stay in the exit-code set, and each stops the run with the code that the
// catalog gives its diagnostic. A run that decides (pass, violation, unknown) uses 0, 1, and 4.
func TestAssuranceExitCodesStayInTheContract(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	for _, c := range asrStabilityExitCases(t) {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			asrStabilityRunExitCase(t, &c)
		})
	}

	t.Run("signing offline", func(t *testing.T) {
		t.Parallel()

		asrStabilitySigningOffline(t)
	})

	t.Run("assurance validate", func(t *testing.T) {
		t.Parallel()

		r := asrLineageWorld(t).build()

		res := r.cli(r.env, "assurance", "validate")
		asrExitInSet(t, res)

		if res.code != 0 {
			t.Errorf("assurance validate: exit %d\n%s", res.code, res.output())
		}
	})
}

// asrStabilityExitCases are the cases of the exit-code contract that a workspace edit gives.
func asrStabilityExitCases(t *testing.T) []asrStabilityExitCase {
	t.Helper()

	return []asrStabilityExitCase{
		{
			name: "mandatory mode on an assurance rule", code: 2, diag: "CO-POL-018",
			edit: func(w *asrWorld) { w.enforcement = "  enforcement:\n    - {rules: [ASR-PRV-001], mode: mandatory}\n" },
		},
		{name: "unknown key of the assurance block", edit: func(w *asrWorld) { w.extra = "  surprise: true\n" }, code: 2, diag: "CO-PRV-001"},
		{
			name: "path outside the workspace root", code: 2, diag: "CO-PRV-002",
			edit: func(w *asrWorld) {
				w.extra = "  plans:\n    - {environment: production, tool: terraform, file: ../outside/plan.json}\n"
			},
		},
		{
			name: "environment that is not declared", code: 2, diag: "CO-WRK-017",
			edit: func(w *asrWorld) {
				w.extra = "  evaluators:\n    - {environment: nowhere, kinds: [network-policy]}\n"
			},
		},
		{
			name: "renderer that is not pinned or found", code: 5, diag: "CO-PRV-003",
			edit: func(w *asrWorld) {
				w.rendering = "      - {environment: production, tool: kustomize, path: kustomize/overlays/prod}\n"
			},
		},
		{name: "scenario file that is not valid", code: 2, diag: "CO-QUA-001", edit: func(w *asrWorld) { w.scenarios = "kind: QualityScenarioSet\nscenarios: 3\n" }},
		{
			name: "scenario scope that does not exist", code: 2, diag: "CO-QUA-002",
			edit: func(w *asrWorld) {
				w.scenarios = strings.Replace(asrScenarios, "scope: [api-service]\n    mode: measured", "scope: [no-such-element]\n    mode: measured", 1)
			},
		},
		{
			name: "cosign missing", code: 5, diag: "CO-WRK-014", edit: func(*asrWorld) {},
			env: []string{"COSIGN=", "PATH=" + pathWithout(t, "cosign")},
		},
	}
}

// asrStabilityRunExitCase indexes a valid workspace, replaces the workspace and the scenario file by the case's, and
// requires the exit code and the diagnostic.
func asrStabilityRunExitCase(t *testing.T, c *asrStabilityExitCase) {
	t.Helper()

	w := &asrWorld{t: t, attestations: []string{asrShop}}
	r := w.build()

	c.edit(w)
	r.write("concordia.yaml", w.workspace())

	if w.scenarios != "" {
		r.write("quality/scenarios.yaml", w.scenarios)
	}

	env := r.env
	if c.env != nil {
		env = append(slices.Clone(r.env), c.env...)
	}

	args := c.args
	if args == nil {
		args = []string{"check"}
	}

	var res cliResult

	asrExclusive(func() { res = r.cli(env, args...) })
	asrExitInSet(t, res)

	if res.code != c.code || !strings.Contains(res.output(), c.diag) {
		t.Errorf("exit %d, want %d with %s:\n%s", res.code, c.code, c.diag, res.output())
	}
}

func asrStabilitySigningOffline(t *testing.T) {
	t.Helper()

	r := asrLineageWorld(t).build()
	if _, res := r.check(); res.code != 0 {
		t.Fatalf("check: exit %d\n%s", res.code, res.stderr)
	}

	res := r.cli(r.env, "--offline", "export", "bundle", "--sign")
	asrExitInSet(t, res)

	if res.code != 6 || !strings.Contains(res.output(), "CO-WRK-016") {
		t.Errorf("exit %d, want 6 with CO-WRK-016:\n%s", res.code, res.output())
	}
}
