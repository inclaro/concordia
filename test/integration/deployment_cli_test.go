package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Task 5.5: `explain INFRA-…` prints the identity lines and the trace excerpt of the subject of a finding.
func TestExplainAnInfrastructureFindingPrintsTheTraceExcerpt(t *testing.T) {
	t.Parallel()

	d := deploymentRun(t, &deploymentSpec{})
	_, doc := enfCheckJSON(t, d)

	cases := []struct {
		name, rule, subject, status string
	}{
		{"A10 service with a workload and an open gap", "INFRA-TRC-004", "api-service", "partial"},
		{"A11 service with no workload", "INFRA-PLC-001", "worker-service", "unresolved"},
	}

	for _, c := range cases {
		f := enfFind(t, doc, c.rule, c.subject, "production")

		res := d.cli(t, "explain", c.rule, "--finding", f.Fingerprint)
		if res.code != 0 {
			t.Fatalf("%s: exit code %d, stderr %q", c.name, res.code, res.stderr)
		}

		requireContains(t, c.name, res.stdout,
			"  Environment: production",
			"  Trace excerpt:",
			"    Deployment trace: "+c.subject+" in production (cluster prod-eu): "+c.status,
			"declared configuration only. Not traced: deployment execution and runtime.",
		)
	}
}

// secretLine is the line of k8s/prod/deployment.yaml that holds the seeded environment value.
func secretLine(t *testing.T) int {
	t.Helper()

	for i, line := range strings.Split(string(mustRead(t, "../fixtures/infra/k8s/prod/deployment.yaml")), "\n") {
		if strings.Contains(line, "CONCORDIA-SEED-env-value-5d1e") {
			return i + 1
		}
	}

	t.Fatal("the fixture has no seeded environment value")

	return 0
}

// moveLocations rewrites the line of every location of a file in the results of a run.
func moveLocations(node any, path string, from, to int) {
	switch v := node.(type) {
	case map[string]any:
		if v["path"] == path && v["line"] == float64(from) {
			v["line"] = float64(to)
		}

		for _, child := range v {
			moveLocations(child, path, from, to)
		}
	case []any:
		for _, child := range v {
			moveLocations(child, path, from, to)
		}
	}
}

// Task 5.2, "source with a secret" and "source outside the workspace".
func TestTraceSourceIsRedactedAndConfined(t *testing.T) {
	t.Parallel()

	const seed = "CONCORDIA-SEED-env-value-5d1e"

	d := deploymentRun(t, &deploymentSpec{})
	if res := d.cli(t, "check"); res.code != 0 {
		t.Fatalf("check: exit code %d, stderr %q", res.code, res.stderr)
	}

	var results map[string]any
	if err := json.Unmarshal(mustRead(t, filepath.Join(d.w.dir, "build", "concordia", "results.json")), &results); err != nil {
		t.Fatal(err)
	}

	line := secretLine(t)

	// The provider gives line 1 for a manifest. A trace that points to the container env makes the line of the secret show.
	moveLocations(results["deploymentTrace"], "k8s/prod/deployment.yaml", 1, line)

	outside := filepath.Join(filepath.Dir(d.w.dir), "outside-"+strconv.Itoa(os.Getpid())+".yaml")
	if err := os.WriteFile(outside, []byte("OUTSIDE: CONTENT-NOT-TO-BE-READ\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Remove(outside) })

	moveLocations(results["deploymentTrace"], "k8s/prod/namespace.yaml", 1, 1)
	rewritePath(results["deploymentTrace"], "scripts/deploy.sh", "../"+filepath.Base(outside))

	text, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(filepath.Join(d.w.dir, "moved.json"), text, 0o600); err != nil {
		t.Fatal(err)
	}

	res := d.cli(t, "trace", "api-service", "--environment", "production", "--source", "--results", "moved.json")
	if res.code != 0 {
		t.Fatalf("trace: exit code %d, stderr %q", res.code, res.stderr)
	}

	if strings.Contains(res.stdout, seed) || strings.Contains(res.stderr, seed) {
		t.Errorf("the output shows the seeded value:\n%s", res.stdout)
	}

	requireContains(t, "trace --source", res.stdout, "[REDACTED:", "DB_PASSWORD")

	if strings.Contains(res.stdout, "CONTENT-NOT-TO-BE-READ") {
		t.Errorf("the output shows a file outside the workspace:\n%s", res.stdout)
	}

	requireContains(t, "trace --source", res.stdout, "at ../"+filepath.Base(outside)+":6")
	requireContains(t, "diagnostics", res.stderr, "CO-REP-005", "CO-REP-006")
}

func rewritePath(node any, from, to string) {
	switch v := node.(type) {
	case map[string]any:
		if v["path"] == from {
			v["path"] = to
		}

		for _, child := range v {
			rewritePath(child, from, to)
		}
	case []any:
		for _, child := range v {
			rewritePath(child, from, to)
		}
	}
}

var commandLine = regexp.MustCompile(`^concordia (.+?)(?:\s+# exit (\d+))?$`)

// howToBlocks gives the lines of the `sh` and `yaml` fences of the how-to.
func fences(doc, lang string) []string {
	out := make([]string, 0, 2)

	for _, m := range regexp.MustCompile("(?s)```"+lang+"\n(.*?)```").FindAllStringSubmatch(doc, -1) {
		out = append(out, m[1])
	}

	return out
}

// Task 5.7: the commands and the configuration of docs/how-to/trace-a-deployment.md run on the infra fixture workspace.
func TestTraceHowToRunsOnTheInfraWorkspace(t *testing.T) {
	t.Parallel()

	doc := string(mustRead(t, "../../docs/how-to/trace-a-deployment.md"))

	// The configuration that the guide shows is the configuration of the workspace.
	for name, want := range map[string]string{
		"environments": deploymentEnvironments,
		"bindings":     deploymentBindings,
		"deployable":   `"concordia": {"tags": ["deployable"]}`,
		"pack":         "    - use: builtin:infrastructure\n",
		"check":        deploymentCheck + enfTraceShadowStaging,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the how-to does not show the %s of the test workspace:\n%s", name, want)
		}
	}

	d := deploymentRun(t, &deploymentSpec{check: deploymentCheck + enfTraceShadowStaging})
	ran := 0

	for _, block := range fences(doc, "sh") {
		for _, line := range strings.Split(block, "\n") {
			m := commandLine.FindStringSubmatch(strings.TrimSpace(line))
			if m == nil {
				continue
			}

			want := 0
			if m[2] != "" {
				want, _ = strconv.Atoi(m[2])
			}

			res := d.cli(t, strings.Fields(m[1])...)
			if res.code != want {
				t.Errorf("concordia %s: exit code %d, want %d; stderr %q", m[1], res.code, want, res.stderr)
			}

			ran++
		}
	}

	if ran < 7 {
		t.Errorf("the how-to has %d commands, want at least 7", ran)
	}

	// The mode that the guide refuses is refused.
	refused := deploymentCheckError(t, &deploymentSpec{check: deploymentCheck + "  enforcement:\n    - {family: deployment-trace, environment: production, mode: mandatory}\n"})
	if refused == nil || !strings.Contains(refused.Error(), "CO-POL-018") {
		t.Errorf("mandatory deployment-trace: error = %v, want CO-POL-018", refused)
	}
}
