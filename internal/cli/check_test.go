package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

func TestCheckWritesThePrettyResultAndGivesTheRunExitCode(t *testing.T) {
	t.Parallel()

	res := newChkSetup(t, nil, nil).run(t, "check", "--no-color")
	if res.Code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	for _, want := range []string{"FAIL CO-BND-001 Billing uses fulfillment contracts only", "UNKNOWN CO-COV-001"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, res.Stdout)
		}
	}

	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if got, want := lines[len(lines)-1], "1 active error(s), 0 baselined, 0 waived, 0 expired waiver(s), 2 unknown"; got != want {
		t.Errorf("last line = %q, want %q", got, want)
	}
}

func TestCheckFormatJSONWritesTheCanonicalJSONToStdout(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	res := s.run(t, "check", "--format", "json")

	var doc struct {
		Summary struct {
			ExitCode int `json:"exitCode"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil || doc.Summary.ExitCode != 1 {
		t.Fatalf("stdout is not the result: err = %v, stdout = %q", err, res.Stdout)
	}

	if want := readFile(t, s.dir, "build/concordia/results.json"); res.Stdout != want {
		t.Errorf("stdout differs from build/concordia/results.json")
	}
}

// chkSARIFDoc is the part of a SARIF report that the check tests read.
type chkSARIFDoc struct {
	Runs []struct {
		Results []any `json:"results"`
	} `json:"runs"`
}

// hasOneResult reports whether the report has one run with one result.
func (doc *chkSARIFDoc) hasOneResult() bool {
	return len(doc.Runs) == 1 && len(doc.Runs[0].Results) == 1
}

func TestCheckFormatSARIFWithOutputWritesTheFile(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	res := s.run(t, "check", "--format", "sarif", "--output", "r.sarif")
	if res.Stdout != "" {
		t.Errorf("stdout = %q, want empty", res.Stdout)
	}

	var doc chkSARIFDoc
	if err := json.Unmarshal([]byte(readFile(t, s.dir, "r.sarif")), &doc); err != nil || !doc.hasOneResult() {
		t.Errorf("unexpected SARIF: err = %v, doc = %+v", err, doc)
	}
}

func TestCheckFormatJUnitHasOneTestCaseForEachRule(t *testing.T) {
	t.Parallel()

	res := newChkSetup(t, nil, nil).run(t, "check", "--format", "junit")
	if got := strings.Count(res.Stdout, "<testcase "); got != 3 {
		t.Errorf("testcases = %d, want 3:\n%s", got, res.Stdout)
	}
}

func TestCheckRejectsAnUnsupportedFormatBeforeAnyWork(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)

	res := s.run(t, "check", "--format", "yaml")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if len(s.graph.Queries) != 0 {
		t.Errorf("queries = %v, want none", s.graph.Queries)
	}
}

func TestCheckFailOnWarningTurnsAWarningFindingIntoExit1(t *testing.T) {
	t.Parallel()

	policyText := strings.Replace(chkPolicy,
		"id: CO-DECL-001\n    name: Dependencies are declared\n    severity: error",
		"id: CO-DECL-001\n    name: Dependencies are declared\n    severity: warning", 1)

	graph := chkGraph()
	storeNode, billingNode := testutil.FileNode("2", chkStoreFile), testutil.FileNode("1", chkBillingFile)
	graph.Edges = append(graph.Edges, testutil.FakeEdge{Type: "IMPORTS", From: &storeNode, To: &billingNode})
	s := newChkSetup(t, map[string]string{"policies/demo.policy.yaml": policyText}, graph)

	if res := s.run(t, "check", "--rule", "CO-DECL-001"); res.Code != 0 {
		t.Fatalf("without --fail-on: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if res := s.run(t, "check", "--rule", "CO-DECL-001", "--fail-on", "warning"); res.Code != 1 {
		t.Errorf("--fail-on warning: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestCheckFailOnFatalIsAUsageError(t *testing.T) {
	t.Parallel()

	res := runCLI(t, "check", "--fail-on", "fatal")
	if res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// The options of check (the port of test/reporting/cli-wiring.test.ts).

func TestCheckCollectsRepeatableOptionsAndParsesNumbers(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, map[string]string{
		"p1.policy.yaml": chkPolicy,
		"b.json":         `{"schemaVersion":"1.0","createdFrom":{"sourceRevision":null,"graphGeneration":"g","policyDigest":"d"},"findings":[]}`,
		"w.yaml":         "apiVersion: concordia.io/v1alpha1\nkind: ExceptionRegister\nwaivers: []\n",
	}, nil)

	res := s.run(t, "check", "--format", "json", "--rule", "CO-BND-001", "--rule", "CO-DECL-001", "--policy", "p1.policy.yaml",
		"--minimum-coverage", "0.8", "--unknown", "warn", "--use-baseline", "b.json", "--waivers", "w.yaml",
		"--evidence-dir", "ev", "--project", "demo", "--architecture", "architecture/a.calm.json")
	if res.Code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var doc struct {
		Summary struct {
			UnknownPolicy string `json:"unknownPolicy"`
		} `json:"summary"`
		Rules []struct {
			RuleID string `json:"ruleId"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatal(err)
	}

	ruleIDs := make([]string, len(doc.Rules))
	for i, r := range doc.Rules {
		ruleIDs[i] = r.RuleID
	}

	if want := []string{"CO-BND-001", "CO-DECL-001"}; !slices.Equal(ruleIDs, want) {
		t.Errorf("rules = %+v, want the two selected rules", doc.Rules)
	}

	if doc.Summary.UnknownPolicy != "warn" {
		t.Errorf("unknownPolicy = %q, want warn", doc.Summary.UnknownPolicy)
	}

	if !exists(s.dir, "ev") {
		t.Errorf("--evidence-dir wrote no directory")
	}
}

func TestCheckChangedTakesTheReferenceAsArgument(t *testing.T) {
	t.Parallel()

	res := newChkSetup(t, nil, nil).run(t, "check", "--changed", "origin/main", "--format", "json")
	if !slices.Contains([]int{0, 1, 4}, res.Code) {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var doc struct {
		Run struct {
			Changed *struct {
				Ref string `json:"ref"`
			} `json:"changed"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatal(err)
	}

	if doc.Run.Changed == nil || doc.Run.Changed.Ref != "origin/main" {
		t.Errorf("run.changed = %+v, want ref origin/main", doc.Run.Changed)
	}
}

func TestCheckRejectsAPositionalArgumentWithoutChanged(t *testing.T) {
	t.Parallel()

	if res := runCLI(t, "check", "extra"); res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestCheckTakesFormatAndOutputAfterTheCommand(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, nil, nil)
	s.run(t, "check", "--format", "sarif", "--output", "r.sarif")

	if !exists(s.dir, "r.sarif") {
		t.Errorf("--output after the command was not used")
	}
}

func TestCheckRejectsBadOptionValuesAsUsageErrors(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"check", "--minimum-coverage", "2"},
		{"check", "--minimum-coverage", "abc"},
		{"check", "--unknown", "maybe"},
	} {
		if res := runCLI(t, args...); res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
			t.Errorf("%v: exit code = %d, stderr = %q", args, res.Code, res.Stderr)
		}
	}
}

func TestExplainNeedsTheRuleID(t *testing.T) {
	t.Parallel()

	if res := runCLI(t, "explain"); res.Code != 2 || !strings.Contains(res.Stderr, "CO-WRK-010") {
		t.Errorf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func TestWatchRejectsASmallDebounce(t *testing.T) {
	t.Parallel()

	res := newChkSetup(t, nil, nil).run(t, "watch", "--debounce", "100")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

const chkEnvironments = "environments:\n  - {id: prod, sources: [k8s/prod/**]}\n  - {id: dev, sources: [k8s/dev/**]}\n  - {id: stage, sources: [k8s/stage/**]}\n"

type chkEnvInputs struct {
	Inputs struct {
		Environments chkEnvSet `json:"environments"`
	} `json:"inputs"`
}

// chkEnvSet is the environment set that a run records in its inputs.
type chkEnvSet struct {
	Digest   string   `json:"digest"`
	Selected []string `json:"selected"`
	Excluded []string `json:"excluded"`
}

// recordsSelection reports whether the set has the digest of a set, and the comma-joined selected and excluded ids.
func (e chkEnvSet) recordsSelection(selected, excluded string) bool {
	return strings.Join(e.Selected, ",") == selected && strings.Join(e.Excluded, ",") == excluded && strings.HasPrefix(e.Digest, "sha256:")
}

func TestCheckEnvironmentSelectsAndRecordsTheSet(t *testing.T) {
	t.Parallel()

	s := newChkSetup(t, map[string]string{"concordia.yaml": chkWorkspaceText(chkEnvironments)}, nil)

	res := s.run(t, "check", "--format", "json", "--environment", "prod", "--environment", "stage")
	if res.Code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var doc chkEnvInputs
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatal(err)
	}

	env := doc.Inputs.Environments
	if !env.recordsSelection("prod,stage", "dev") {
		t.Errorf("environments = %+v", env)
	}

	var manifest chkEnvInputs
	if err := json.Unmarshal([]byte(readFile(t, s.dir, ".concordia/runs/"+runIDOf(t, res.Stdout)+"/manifest.json")), &manifest); err != nil {
		t.Fatal(err)
	}

	if manifest.Inputs.Environments.Digest != env.Digest || strings.Join(manifest.Inputs.Environments.Excluded, ",") != "dev" {
		t.Errorf("manifest environments = %+v", manifest.Inputs.Environments)
	}
}

func TestCheckEnvironmentDefaultsToAllDeclared(t *testing.T) {
	t.Parallel()

	res := newChkSetup(t, map[string]string{"concordia.yaml": chkWorkspaceText(chkEnvironments)}, nil).run(t, "check", "--format", "json")

	var doc chkEnvInputs
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatal(err)
	}

	if env := doc.Inputs.Environments; strings.Join(env.Selected, ",") != "prod,dev,stage" || len(env.Excluded) != 0 {
		t.Errorf("environments = %+v", env)
	}
}

func TestCheckUnknownEnvironmentGivesWRK017Exit2(t *testing.T) {
	t.Parallel()

	res := newChkSetup(t, map[string]string{"concordia.yaml": chkWorkspaceText(chkEnvironments)}, nil).run(t, "check", "--environment", "qa")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, want 2: %s", res.Code, res.Stderr)
	}

	wantContains(t, res.Stderr, "CO-WRK-017", "qa")
}

func runIDOf(t *testing.T, out string) string {
	t.Helper()

	var doc struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Run.ID == "" {
		t.Fatalf("no run id: %v", err)
	}

	return doc.Run.ID
}

// chkRollbackWorkspace is the check workspace with a `check.rollback` that expires on the given day.
func chkRollbackWorkspace(expires string) string {
	text := chkWorkspaceText("")

	return strings.Replace(text, "check: {minimumCoverage: 0.5, unknown: fail, failOn: error}\n",
		"check:\n  minimumCoverage: 0.5\n  unknown: fail\n  failOn: error\n  rollback: {reason: false positives, authorizedBy: jane, expires: \""+expires+"\"}\n", 1)
}

func TestCheckExpiredRollbackGivesWRK018Exit2(t *testing.T) {
	t.Parallel()

	res := newChkSetup(t, map[string]string{"concordia.yaml": chkRollbackWorkspace("2020-01-01")}, nil).run(t, "check")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, want 2: %s", res.Code, res.Stderr)
	}

	wantContains(t, res.Stderr, "CO-WRK-018", "2020-01-01", "jane")
}

// chkRollbackSummary is the rollback of the summary of a check result.
type chkRollbackSummary struct {
	Reason       string `json:"reason"`
	AuthorizedBy string `json:"authorizedBy"`
	Expires      string `json:"expires"`
	Message      string `json:"message"`
}

// recordsRollbackOf reports whether the rollback has the given reason, authority and expiry, and says that new
// enforcement was disabled.
func (rb chkRollbackSummary) recordsRollbackOf(reason, authorizedBy, expires string) bool {
	return rb.Reason == reason && rb.AuthorizedBy == authorizedBy && rb.Expires == expires &&
		strings.Contains(rb.Message, "new enforcement was disabled")
}

// MIG-06: an active rollback is recorded in the summary of the results.
func TestCheckActiveRollbackIsRecordedInTheSummary(t *testing.T) {
	t.Parallel()

	res := newChkSetup(t, map[string]string{"concordia.yaml": chkRollbackWorkspace("2099-01-01")}, nil).run(t, "check", "--format", "json")

	var doc struct {
		Summary struct {
			Rollback chkRollbackSummary `json:"rollback"`
		} `json:"summary"`
	}

	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatalf("exit code %d, stdout %q, stderr %q: %v", res.Code, res.Stdout, res.Stderr, err)
	}

	rb := doc.Summary.Rollback
	if !rb.recordsRollbackOf("false positives", "jane", "2099-01-01") {
		t.Errorf("rollback = %+v", rb)
	}
}

const chkViewHead = "environments:\n  - {id: prod, sources: [deploy/prod/**]}\n"

func TestCheckRejectsAViewWithAnUnknownEnvironmentWithExit2(t *testing.T) {
	t.Parallel()

	view := chkViewHead + "views:\n  - id: v1\n    kind: container\n    origin: generated\n    owner: team\n    scope: [billing-service]\n" +
		"    abstraction: container\n    environment: prod-eu\n    output: {path: docs/architecture/views/v1.dsl}\n"

	res := newChkSetup(t, map[string]string{"concordia.yaml": chkWorkspaceText(view)}, nil).run(t, "check", "--no-color")
	if res.Code != 2 {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}

	if got := res.Stdout + res.Stderr; !strings.Contains(got, "CO-DGM-001") || !strings.Contains(got, "prod-eu") {
		t.Errorf("output lacks the CO-DGM-001 about prod-eu: %q", got)
	}
}

func TestCheckPrintsDefaultViewSuggestionsWithoutChangingTheExitCode(t *testing.T) {
	t.Parallel()

	res := newChkSetup(t, map[string]string{"concordia.yaml": chkWorkspaceText(chkViewHead)}, nil).run(t, "check", "--format", "json")
	if res.Code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if got := res.Stdout; !strings.Contains(got, "CO-DGM-005") {
		t.Errorf("no suggestion in %q", got)
	}
}

// chkViewsDoc is the part of the check result that the views tests read.
type chkViewsDoc struct {
	Toolchain map[string]any `json:"toolchain"`
	Areas     struct {
		DiagramCurrency struct {
			Verdict string `json:"verdict"`
		} `json:"diagramCurrency"`
	} `json:"areas"`
	Findings []struct {
		RuleID      string `json:"ruleId"`
		Kind        string `json:"kind"`
		Disposition string `json:"disposition"`
		NextAction  string `json:"nextAction"`
	} `json:"findings"`
	Views []struct {
		ID       string `json:"id"`
		Presence struct {
			Status string `json:"status"`
		} `json:"presence"`
	} `json:"views"`
}

// presenceActions gives the kind of the view-presence finding for each next action, and reports the findings that are
// not active view-absent ones.
func (doc *chkViewsDoc) presenceActions(t *testing.T) map[string]string {
	t.Helper()

	actions := map[string]string{}

	for _, f := range doc.Findings {
		if f.RuleID != "VIEW-PRES-001" {
			continue
		}

		if f.Kind != "view-absent" || f.Disposition != "active" {
			t.Errorf("finding = %+v", f)
		}

		actions[f.NextAction] = f.Kind
	}

	return actions
}

// requireViewsViolated checks the two declared views, and that their presence is a violation for nothing is committed.
func (doc *chkViewsDoc) requireViewsViolated(t *testing.T) {
	t.Helper()

	if len(doc.Views) != 2 || doc.Views[0].ID != "payments-context" || doc.Views[1].ID != "view-a" {
		t.Fatalf("views = %+v", doc.Views)
	}

	for _, v := range doc.Views {
		if v.Presence.Status != "violation" {
			t.Errorf("view %s: presence %s, want violation (nothing is committed)", v.ID, v.Presence.Status)
		}
	}

	if doc.Areas.DiagramCurrency.Verdict != "violation" {
		t.Errorf("areas.diagramCurrency = %s", doc.Areas.DiagramCurrency.Verdict)
	}
}

// A declared view is evaluated by `check`: its results are in `views`, the findings of its rules follow the
// existing exit-code path, and `check` writes no view file (spec "Check does not write").
func TestCheckEvaluatesTheDeclaredViewsAndWritesNoViewFile(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, viewsText(generatedView("view-a", viewAPath, ""), authoredContextView))

	res := s.run(t, "check", "--format", "json")
	if res.Code != 1 {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	var doc chkViewsDoc
	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatalf("stdout %q: %v", res.Stdout, err)
	}

	doc.requireViewsViolated(t)

	actions := doc.presenceActions(t)

	if _, ok := actions["regenerate view"]; !ok {
		t.Errorf("no `regenerate view` finding for the generated view: %v", actions)
	}

	if _, ok := actions["supply evidence"]; !ok {
		t.Errorf("no `supply evidence` finding for the authored view: %v", actions)
	}

	if _, present := doc.Toolchain["structurizr"]; present {
		t.Error("toolchain.structurizr without a verified export")
	}

	if exists(s.dir, viewAPath) || exists(s.dir, viewAPath+".manifest.json") {
		t.Error("check wrote a view file")
	}
}

// Without a tracked revision presence is `unknown`, and `check.unknown` decides (spec "Workspace is not a Git repository").
func TestCheckPresenceIsUnknownOutsideAGitRepository(t *testing.T) {
	t.Parallel()

	s := viewsSetup(t, viewsText(generatedView("view-a", viewAPath, "")))
	s.cfg.Deps.NewGit = func(string) workspace.GitPort { return &workspace.FakeGit{} }

	res := s.run(t, "check", "--format", "json")

	var doc struct {
		Rules []struct {
			RuleID  string `json:"ruleId"`
			Verdict string `json:"verdict"`
		} `json:"rules"`
	}

	if err := json.Unmarshal([]byte(res.Stdout), &doc); err != nil {
		t.Fatalf("exit code %d, stdout %q, stderr %q: %v", res.Code, res.Stdout, res.Stderr, err)
	}

	for _, r := range doc.Rules {
		if r.RuleID == "VIEW-PRES-001" && r.Verdict != "unknown" {
			t.Errorf("VIEW-PRES-001 = %s, want unknown", r.Verdict)
		}
	}
}
