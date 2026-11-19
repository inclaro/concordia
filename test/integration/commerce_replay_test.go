package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/reporting"
	"github.com/inclaro/concordia/internal/workspace"
)

// These tests run the commerce fixture against the CBM graph that
// `mise run record-cbm` recorded (cmd/record-cbm). They replay the recording
// and need no codebase-memory-mcp. The recording was made in another
// directory: the key of the index_repository call does not contain the
// repository path.

const (
	fixtureDir    = "../fixtures/commerce"
	recordingFile = "../fixtures/cbm-recordings/commerce.json"

	projectName  = "concordia-fixture-commerce"
	billingFile  = "services/billing/src/InvoiceService.ts"
	legacyFile   = "services/legacy/src/Legacy.kt"
	lockFile     = ".concordia.lock"
	resultsFile  = "build/concordia/results.json"
	contractPath = "packages/fulfillment-contract"

	// fixedNow is the clock of the tests that compare two runs.
	fixedNow = "2026-06-01T12:00:00Z"
	runID    = "20260601T120000Z-00000000"

	workspaceFile = "concordia.yaml"
	exitViolation = 1
)

var (
	errNetwork = errors.New("network access is not permitted")

	generationPattern = regexp.MustCompile(`^cbm:` + projectName + `:[0-9a-f]{16}$`)
	// volatileRunKeys are the keys of `run` that differ between two runs with the same clock.
	volatileRunKeys = []string{"id", "startedAt", "finishedAt", "host"}
)

func loadRecording(tb testing.TB) observation.Recording {
	tb.Helper()

	data, err := os.ReadFile(recordingFile)
	if err != nil {
		tb.Fatalf("read the recording: %v", err)
	}

	var rec observation.Recording
	if err = json.Unmarshal(data, &rec); err != nil {
		tb.Fatalf("parse the recording: %v", err)
	}

	return rec
}

// commerce is a temporary copy of the commerce fixture, with replayed CBM and
// a fake git at the recorded revision.
type commerce struct {
	rec   observation.Recording
	dir   string
	files []string
}

func newCommerce(t *testing.T) *commerce {
	t.Helper()

	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatalf("copy the fixture: %v", err)
	}

	var files []string

	err := fs.WalkDir(os.DirFS(fixtureDir), ".", func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() {
			files = append(files, p)
		}

		return walkErr
	})
	if err != nil {
		t.Fatalf("list the fixture: %v", err)
	}

	return &commerce{rec: loadRecording(t), dir: dir, files: files}
}

func (c *commerce) abs(rel string) string {
	return filepath.Join(c.dir, filepath.FromSlash(rel))
}

func (c *commerce) exists(rel string) bool {
	_, err := os.Stat(c.abs(rel))

	return err == nil
}

func (c *commerce) replay() *observation.ReplayClient {
	return observation.NewReplayClient(c.rec)
}

func (c *commerce) git() *workspace.FakeGit {
	return &workspace.FakeGit{Rev: c.rec.HeadSHA, Files: c.files}
}

func (c *commerce) env(offline bool) conformance.Env {
	return conformance.Env{
		Logger:        logger.New(logger.Options{Out: io.Discard, RunID: "run", Quiet: true}),
		RunID:         runID,
		WorkspacePath: c.abs(workspaceFile),
		Cwd:           c.dir,
		ToolVersion:   "test",
		Offline:       offline,
	}
}

func (c *commerce) deps() conformance.Deps {
	return conformance.Deps{
		CreateClient: func(*workspace.Workspace, *logger.Logger) observation.Client { return c.replay() },
		CreateGit:    func(string) workspace.GitPort { return c.git() },
		RenderReport: reporting.Renderer(reporting.PrettyOptions{}),
	}
}

func fixedClock() time.Time {
	now, err := time.Parse(time.RFC3339, fixedNow)
	if err != nil {
		panic(err)
	}

	return now
}

// check runs the pipeline without writing files. edit changes the options.
func (c *commerce) check(ctx context.Context, edit func(*conformance.CheckOptions)) (*conformance.Run, error) {
	opts := &conformance.CheckOptions{Env: c.env(false), Deps: c.deps(), SkipReports: true}
	if edit != nil {
		edit(opts)
	}

	return conformance.RunCheck(ctx, opts)
}

func (c *commerce) mustCheck(t *testing.T, edit func(*conformance.CheckOptions)) *conformance.Run {
	t.Helper()

	run, err := c.check(t.Context(), edit)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}

	return run
}

// failureOf gives the diagnostic error of a failed check.
func (c *commerce) failureOf(t *testing.T, edit func(*conformance.CheckOptions)) *diag.Error {
	t.Helper()

	_, err := c.check(t.Context(), edit)

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("expected a *diag.Error, got %v", err)
	}

	return failure
}

type cliResult struct {
	stdout string
	stderr string
	code   int
}

// run runs the CLI in the fixture copy. The CBM client replays the recording, git is fake.
func (c *commerce) run(t *testing.T, args ...string) cliResult {
	t.Helper()

	var stdout, stderr bytes.Buffer

	stdio := cli.IO{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Env:    map[string]string{"CONCORDIA_NOW": fixedNow},
		Cwd:    c.dir,
	}
	deps := cli.Deps{
		NewClient: func(*workspace.Workspace, *cli.Context) observation.Client { return c.replay() },
		NewGit:    func(string) workspace.GitPort { return c.git() },
	}

	code := cli.Run(t.Context(), args, &stdio, deps, cli.BuildInfo{Version: "test"})

	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func verdictsOf(run *conformance.Run) map[string]conformance.RuleVerdict {
	verdicts := map[string]conformance.RuleVerdict{}
	for i := range run.Rules {
		verdicts[run.Rules[i].RuleID] = run.Rules[i].Verdict
	}

	return verdicts
}

func findingsOf(run *conformance.Run, ruleID string) []conformance.Finding {
	var out []conformance.Finding

	for i := range run.Findings {
		if run.Findings[i].RuleID == ruleID {
			out = append(out, run.Findings[i])
		}
	}

	return out
}

func kindsOf(findings []conformance.Finding) []conformance.FindingKind {
	kinds := make([]conformance.FindingKind, 0, len(findings))
	for i := range findings {
		kinds = append(kinds, findings[i].Kind)
	}

	return kinds
}

// stable is the canonical JSON of a run without the volatile fields of `run`.
func stable(t *testing.T, run *conformance.Run) string {
	t.Helper()

	text, err := conformance.CanonicalJSON(run)
	if err != nil {
		t.Fatal(err)
	}

	return withoutVolatile(t, text)
}

// withoutVolatile removes the volatile keys of `run` from a results document.
func withoutVolatile(t *testing.T, text []byte) string {
	t.Helper()

	tree := decodeObject(t, text)

	info, ok := tree["run"].(map[string]any)
	if !ok {
		t.Fatal("results have no run object")
	}

	for _, key := range volatileRunKeys {
		delete(info, key)
	}

	out, err := canonjson.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

func decodeObject(t *testing.T, text []byte) map[string]any {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()

	var tree map[string]any
	if err := dec.Decode(&tree); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, text)
	}

	return tree
}

// cmrRevisionOf gives the source revision of the run, empty when it has none.
func cmrRevisionOf(run *conformance.Run) string {
	if run.Run.SourceRevision == nil {
		return ""
	}

	return *run.Run.SourceRevision
}

func cmrRequireRunInfo(t *testing.T, run *conformance.Run, head string) {
	t.Helper()

	if run.Summary.ExitCode != exitViolation {
		t.Errorf("exit code = %d, want %d", run.Summary.ExitCode, exitViolation)
	}

	if got := cmrRevisionOf(run); got != head {
		t.Errorf("sourceRevision = %v, want %s", run.Run.SourceRevision, head)
	}

	if !generationPattern.MatchString(run.Run.GraphGeneration) {
		t.Errorf("graphGeneration = %q", run.Run.GraphGeneration)
	}
}

// cmrRequireBoundaryFindings checks the forbidden direct dependency: two calls edges and an imports edge of
// InvoiceService.ts, nothing for the contract package. CBM 0.12.1 resolves `store.reserve()` in InvoiceService.issue
// (lsp_ts_method) in addition to the `ShipmentStore` class reference that 0.9.0 gave.
func cmrRequireBoundaryFindings(t *testing.T, run *conformance.Run) {
	t.Helper()

	bnd := findingsOf(run, "CO-BND-001")
	edgeKinds := make([]string, 0, len(bnd))

	for i := range bnd {
		finding := &bnd[i]

		if finding.Edge == nil {
			t.Fatalf("finding %d has no edge", i)
		}

		edgeKinds = append(edgeKinds, string(finding.Edge.Kind))

		assertForbiddenDependency(t, finding)
	}

	slices.Sort(edgeKinds)

	if !slices.Equal(edgeKinds, []string{"calls", "calls", "imports"}) {
		t.Errorf("CO-BND-001 edge kinds = %v, want [calls calls imports]", edgeKinds)
	}

	encoded, err := json.Marshal(bnd)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(encoded), contractPath) {
		t.Errorf("CO-BND-001 findings mention %s", contractPath)
	}
}

func cmrRequireOtherFindings(t *testing.T, run *conformance.Run) {
	t.Helper()

	if got := kindsOf(findingsOf(run, "CO-CYC-001")); !slices.Equal(got, []conformance.FindingKind{conformance.KindCycle}) {
		t.Errorf("CO-CYC-001 kinds = %v, want [cycle]", got)
	}

	wantRoute := [3]string{string(conformance.KindUndeclared), "fulfillment-service", "billing-service"}

	declared := findingsOf(run, "CO-DECL-001")
	for i := range declared {
		finding := &declared[i]

		gotRoute := [3]string{string(finding.Kind), finding.Source.ArchitectureID, finding.Target.ArchitectureID}
		if gotRoute != wantRoute {
			t.Errorf("CO-DECL-001 finding = %s %s -> %s", finding.Kind, finding.Source.ArchitectureID, finding.Target.ArchitectureID)
		}
	}

	if got := kindsOf(findingsOf(run, "CO-LYR-001")); !slices.Equal(got, []conformance.FindingKind{conformance.KindLayerViolation}) {
		t.Errorf("CO-LYR-001 kinds = %v, want [layer-violation]", got)
	}

	if got := findingsOf(run, "CO-COV-001"); len(got) != 0 {
		t.Errorf("CO-COV-001 findings = %d, want 0", len(got))
	}
}

func TestCommerceGivesTheExpectedVerdictsAndFindings(t *testing.T) {
	c := newCommerce(t)
	run := c.mustCheck(t, nil)

	want := map[string]conformance.RuleVerdict{
		"CO-BND-001":  conformance.VerdictViolation,
		"CO-COV-001":  conformance.VerdictUnknown,
		"CO-CYC-001":  conformance.VerdictViolation,
		"CO-DECL-001": conformance.VerdictViolation,
		"CO-LYR-001":  conformance.VerdictViolation,
	}
	if got := verdictsOf(run); !maps.Equal(got, want) {
		t.Fatalf("verdicts = %v, want %v", got, want)
	}

	cmrRequireRunInfo(t, run, c.rec.HeadSHA)
	cmrRequireBoundaryFindings(t, run)
	cmrRequireOtherFindings(t, run)

	if run.Summary.Active != len(run.Findings) {
		t.Errorf("active = %d, findings = %d", run.Summary.Active, len(run.Findings))
	}

	if run.Summary.UnknownRules != 1 {
		t.Errorf("unknownRules = %d, want 1", run.Summary.UnknownRules)
	}
}

// cmrForbiddenFacts is what a forbidden dependency finding of the billing service says.
type cmrForbiddenFacts struct {
	kind        conformance.FindingKind
	source      string
	target      string
	location    string
	disposition exceptions.Disposition
	owner       string
}

func cmrForbiddenFactsOf(finding *conformance.Finding) cmrForbiddenFacts {
	facts := cmrForbiddenFacts{
		kind: finding.Kind, source: finding.Source.ArchitectureID, target: finding.Target.ArchitectureID,
		disposition: finding.Disposition,
	}

	if finding.Location != nil {
		facts.location = finding.Location.Path
	}

	if finding.Owner != nil {
		facts.owner = *finding.Owner
	}

	return facts
}

func assertForbiddenDependency(t *testing.T, finding *conformance.Finding) {
	t.Helper()

	want := cmrForbiddenFacts{
		kind: conformance.KindForbiddenDependency, source: "billing-service", target: "fulfillment-service",
		location: billingFile, disposition: exceptions.DispositionActive, owner: "team-billing",
	}
	if got := cmrForbiddenFactsOf(finding); got != want {
		t.Errorf("finding = %+v, want %+v", got, want)
	}

	if observed := finding.Target.Observed; observed != nil && strings.Contains(observed.FilePath, contractPath) {
		t.Errorf("target file = %s", observed.FilePath)
	}
}

// cmrRuleResult gives the result of the rule in the run, or nil.
func cmrRuleResult(run *conformance.Run, ruleID string) *conformance.RuleResult {
	for i := range run.Rules {
		if run.Rules[i].RuleID == ruleID {
			return &run.Rules[i]
		}
	}

	return nil
}

func TestCommerceKeepsTheExcludedLegacyFileInTheCoverageTotal(t *testing.T) {
	c := newCommerce(t)
	run := c.mustCheck(t, nil)

	cov := cmrRuleResult(run, "CO-COV-001")
	if cov == nil {
		t.Fatal("no CO-COV-001 result")
	}

	if cov.Verdict != conformance.VerdictUnknown {
		t.Errorf("verdict = %s, want unknown", cov.Verdict)
	}

	codes := make([]string, 0, len(cov.Reasons))
	for _, reason := range cov.Reasons {
		codes = append(codes, reason.Code)
	}

	if !slices.Equal(codes, []string{diag.CON021, diag.CON026}) {
		t.Errorf("reason codes = %v, want [%s %s]", codes, diag.CON021, diag.CON026)
	}

	if cov.Coverage == nil {
		t.Fatal("CO-COV-001 has no coverage")
	}

	files := cov.Coverage.Files
	if !slices.Equal(files.Excluded, []string{legacyFile}) {
		t.Errorf("excluded = %v, want [%s]", files.Excluded, legacyFile)
	}

	if got := [2]int{files.Total, files.Covered}; got != [2]int{2, 1} {
		t.Errorf("files total/covered = %d/%d, want 2/1", files.Total, files.Covered)
	}
}

func TestCommerceRuleOptionExitsWithUnknownCode(t *testing.T) {
	failing := newCommerce(t).run(t, "--format", "json", "check", "--rule", "CO-COV-001")
	if failing.code != diag.Unknown {
		t.Errorf("--rule CO-COV-001 exit code = %d, want %d\n%s", failing.code, diag.Unknown, failing.stderr)
	}

	warning := newCommerce(t).run(t, "--format", "json", "check", "--rule", "CO-COV-001", "--unknown", string(policy.UnknownWarn))
	if warning.code != diag.OK {
		t.Errorf("--unknown warn exit code = %d, want %d\n%s", warning.code, diag.OK, warning.stderr)
	}
}

func TestCommerceCheckCommandWritesTheReports(t *testing.T) {
	c := newCommerce(t)

	junit := c.run(t, "--format", "junit", "check")
	if junit.code != exitViolation {
		t.Fatalf("junit exit code = %d, want %d\n%s", junit.code, exitViolation, junit.stderr)
	}

	if got := strings.Count(junit.stdout, "<testcase "); got != 5 {
		t.Errorf("testcase count = %d, want 5", got)
	}

	if !c.exists(resultsFile) {
		t.Errorf("%s was not written", resultsFile)
	}

	pretty := newCommerce(t).run(t, "--format", "pretty", "check")
	if pretty.code != exitViolation {
		t.Fatalf("pretty exit code = %d, want %d\n%s", pretty.code, exitViolation, pretty.stderr)
	}

	for _, want := range []string{"FAIL CO-BND-001", "UNKNOWN CO-COV-001", "  imports "} {
		if !strings.Contains(pretty.stdout, want) {
			t.Errorf("pretty output has no %q", want)
		}
	}
}

func TestCommerceTwoRunsWithAFixedClockGiveTheSameResult(t *testing.T) {
	results := make([]string, 0, 2)

	for range 2 {
		run := newCommerce(t).mustCheck(t, func(opts *conformance.CheckOptions) { opts.Deps.Now = fixedClock })
		results = append(results, stable(t, run))
	}

	if results[0] != results[1] {
		t.Error("two runs with a fixed clock differ")
	}

	if !strings.Contains(results[0], "CO-BND-001") {
		t.Error("the result has no CO-BND-001")
	}
}

func TestCommerceWrittenResultsAreStableAndEqualToStdout(t *testing.T) {
	texts := make([]string, 0, 2)

	for range 2 {
		c := newCommerce(t)

		res := c.run(t, "--format", "json", "check")
		if res.code != exitViolation {
			t.Fatalf("exit code = %d, want %d\n%s", res.code, exitViolation, res.stderr)
		}

		fileText, err := os.ReadFile(c.abs(resultsFile))
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(decodeObject(t, fileText), decodeObject(t, []byte(res.stdout))) {
			t.Error("the results file differs from stdout")
		}

		texts = append(texts, withoutVolatile(t, fileText))
	}

	if texts[0] != texts[1] {
		t.Error("the results files differ after the volatile fields are removed")
	}
}

// guardNetwork makes each network use of the process count and fail. The
// returned counter is the number of attempts.
func guardNetwork(t *testing.T) *atomic.Int32 {
	t.Helper()

	var attempts atomic.Int32

	priorTransport, priorResolver := http.DefaultTransport, net.DefaultResolver

	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)

		return nil, errNetwork
	})
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			attempts.Add(1)

			return nil, errNetwork
		},
	}

	t.Cleanup(func() {
		http.DefaultTransport, net.DefaultResolver = priorTransport, priorResolver
	})

	return &attempts
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// FR-014: a run completes with the network disabled.
func TestCommerceCompletesWithTheNetworkDisabled(t *testing.T) {
	c := newCommerce(t)

	// The first run puts the url-mapped control resource into the lock (a local file write).
	if _, err := c.check(t.Context(), func(opts *conformance.CheckOptions) { opts.SkipReports = false }); err != nil {
		t.Fatalf("first run: %v", err)
	}

	if !c.exists(lockFile) {
		t.Fatalf("%s was not written", lockFile)
	}

	attempts := guardNetwork(t)

	// Same directory, so the lock of the first run is there: an offline run completes.
	run, err := c.check(t.Context(), func(opts *conformance.CheckOptions) { opts.Env = c.env(true) })
	if err != nil {
		t.Fatalf("offline run: %v", err)
	}

	if run.Summary.ExitCode != exitViolation {
		t.Errorf("exit code = %d, want %d", run.Summary.ExitCode, exitViolation)
	}

	if got := attempts.Load(); got != 0 {
		t.Errorf("network attempts = %d, want 0", got)
	}
}

func TestCommerceOfflineWithoutLockGivesINT012(t *testing.T) {
	c := newCommerce(t)

	if c.exists(lockFile) {
		t.Fatalf("%s exists before the run", lockFile)
	}

	failure := c.failureOf(t, func(opts *conformance.CheckOptions) { opts.Env = c.env(true) })
	if failure.ExitCode != diag.Network || failure.Diagnostic.Code != diag.INT012 {
		t.Errorf("failure = %d %s, want %d %s", failure.ExitCode, failure.Diagnostic.Code, diag.Network, diag.INT012)
	}

	// The failed offline run did not write a lock.
	if c.exists(lockFile) {
		t.Errorf("%s was written by a failed offline run", lockFile)
	}
}

func TestCommerceStaleGraphGivesOBS008(t *testing.T) {
	c := newCommerce(t)

	failure := c.failureOf(t, func(opts *conformance.CheckOptions) {
		opts.Deps.CreateGit = func(string) workspace.GitPort {
			return &workspace.FakeGit{Rev: strings.Repeat("f", 40)}
		}
	})
	if failure.ExitCode != diag.AdapterFailure || failure.Diagnostic.Code != diag.OBS008 {
		t.Errorf("failure = %d %s, want %d %s", failure.ExitCode, failure.Diagnostic.Code, diag.AdapterFailure, diag.OBS008)
	}
}

// writeIndexRecord writes the coverage record of an index run for the project at the revision.
func (c *commerce) writeIndexRecord(t *testing.T, project, revision string) {
	t.Helper()

	data, err := json.Marshal(map[string]string{"project": project, "sourceRevision": revision})
	if err != nil {
		t.Fatal(err)
	}

	path := c.abs(conformance.CoverageFile)
	if err = os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The graph is older than the checkout.
func TestCommerceIndexedRevisionWinsOverTheLiveHead(t *testing.T) {
	c := newCommerce(t)
	zeros := strings.Repeat("0", 40)

	c.writeIndexRecord(t, projectName, zeros)

	_, err := c.check(t.Context(), nil)

	var failure *diag.Error
	if !errors.As(err, &failure) {
		t.Fatalf("expected a *diag.Error, got %v", err)
	}

	if failure.ExitCode != diag.AdapterFailure || failure.Diagnostic.Code != diag.OBS008 {
		t.Errorf("failure = %d %s, want %d %s", failure.ExitCode, failure.Diagnostic.Code, diag.AdapterFailure, diag.OBS008)
	}

	if !strings.Contains(err.Error(), zeros) {
		t.Errorf("error %q does not name the indexed revision", err)
	}

	// A record of the same revision, or of another project, does not stop the run.
	c.writeIndexRecord(t, projectName, c.rec.HeadSHA)

	if got := c.mustCheck(t, nil).Summary.ExitCode; got != exitViolation {
		t.Errorf("same revision: exit code = %d, want %d", got, exitViolation)
	}

	c.writeIndexRecord(t, "other", zeros)

	if got := c.mustCheck(t, nil).Summary.ExitCode; got != exitViolation {
		t.Errorf("other project: exit code = %d, want %d", got, exitViolation)
	}
}

// cmrRequireEdgeShape checks one fetched edge: both nodes are there, the generation is the one of the snapshot, and the
// confidence and the resolution method follow the native kind.
func cmrRequireEdgeShape(t *testing.T, result *observation.EvidenceQueryResult, ref *observation.GraphSnapshotRef, edge *observation.EvidenceEdge) {
	t.Helper()

	for _, end := range []string{edge.From, edge.To} {
		if _, ok := result.Nodes[end]; !ok {
			t.Errorf("edge %s: node %s is missing", edge.ID, end)
		}
	}

	if edge.GraphGeneration != ref.GraphGeneration {
		t.Errorf("edge %s: generation = %s, want %s", edge.ID, edge.GraphGeneration, ref.GraphGeneration)
	}

	if edge.NativeKind == "IMPORTS" && edge.Confidence != observation.ConfidenceSyntactic {
		t.Errorf("IMPORTS edge %s: confidence = %s, want syntactic", edge.ID, edge.Confidence)
	}

	if edge.NativeKind == "CALLS" && !strings.HasPrefix(edge.ResolutionMethod, "cbm:CALLS:") {
		t.Errorf("CALLS edge %s: resolution method = %s", edge.ID, edge.ResolutionMethod)
	}
}

func cmrRequireEdgeKinds(t *testing.T, edges []*observation.EvidenceEdge) {
	t.Helper()

	kinds := map[observation.CanonicalEdgeKind]bool{}
	for _, edge := range edges {
		kinds[edge.Kind] = true
	}

	for _, kind := range []observation.CanonicalEdgeKind{observation.KindImports, observation.KindCalls, observation.KindDefines} {
		if !kinds[kind] {
			t.Errorf("no %s edge", kind)
		}
	}
}

func cmrRequireImports(t *testing.T, result *observation.EvidenceQueryResult) {
	t.Helper()

	var imports []string

	for _, edge := range result.Edges {
		if edge.Kind == observation.KindImports {
			imports = append(imports, result.Nodes[edge.From].FilePath+" -> "+result.Nodes[edge.To].FilePath)
		}
	}

	for _, want := range []string{
		"services/billing/src/InvoiceService.ts -> services/fulfillment/src/internal/ShipmentStore.ts",
		"services/fulfillment/src/notify.ts -> services/billing/src/InvoiceService.ts",
	} {
		if !slices.Contains(imports, want) {
			t.Errorf("imports do not contain %q", want)
		}
	}
}

// cmrRequireSortedEdges checks that edges are sorted for a stable run.
func cmrRequireSortedEdges(t *testing.T, result *observation.EvidenceQueryResult) {
	t.Helper()

	order := make([]string, 0, len(result.Edges))
	for _, edge := range result.Edges {
		order = append(order, edge.NativeKind+"\x00"+result.Nodes[edge.From].QualifiedName+"\x00"+result.Nodes[edge.To].QualifiedName)
	}

	if !slices.IsSortedFunc(order, canonjson.CompareUTF16) {
		t.Error("edges are not sorted by (native kind, source, target)")
	}
}

func TestRecordingNormalizesImportsCallsAndDefinesEdges(t *testing.T) {
	c := newCommerce(t)
	ctx := t.Context()

	conn, err := observation.Connect(ctx, c.replay(), nil)
	if err != nil {
		t.Fatal(err)
	}

	ref, err := conn.Provider.Freeze(ctx, observation.FreezeRequest{Project: projectName, GitRevision: c.rec.HeadSHA})
	if err != nil {
		t.Fatal(err)
	}

	result, err := conn.Provider.FetchEdges(ctx, ref, []string{"CALLS", "DEFINES", "DEFINES_METHOD", "IMPORTS"})
	if err != nil {
		t.Fatal(err)
	}

	for _, edge := range result.Edges {
		cmrRequireEdgeShape(t, &result, ref, edge)
	}

	cmrRequireEdgeKinds(t, result.Edges)
	cmrRequireImports(t, &result)
	cmrRequireSortedEdges(t, &result)

	if err = conn.Provider.VerifyFrozen(ctx, ref); err != nil {
		t.Errorf("VerifyFrozen: %v", err)
	}
}

// cmrIndexOutput is the JSON that `index` prints.
type cmrIndexOutput struct {
	GraphGeneration string `json:"graphGeneration"`
	Files           struct {
		Total   int `json:"total"`
		Covered int `json:"covered"`
	} `json:"files"`
}

func cmrRequireIndexOutput(t *testing.T, stdout string) {
	t.Helper()

	var out cmrIndexOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("index output: %v\n%s", err, stdout)
	}

	if !strings.HasPrefix(out.GraphGeneration, "cbm:"+projectName+":") {
		t.Errorf("graphGeneration = %q", out.GraphGeneration)
	}

	if out.Files.Total <= 0 {
		t.Errorf("files.total = %d, want > 0", out.Files.Total)
	}
}

func (c *commerce) requireIndexCoverage(t *testing.T) {
	t.Helper()

	data, err := os.ReadFile(c.abs(conformance.CoverageFile))
	if err != nil {
		t.Fatal(err)
	}

	var cov struct {
		Project  string   `json:"project"`
		Excluded []string `json:"excluded"`
	}
	if err = json.Unmarshal(data, &cov); err != nil {
		t.Fatal(err)
	}

	if cov.Project != projectName {
		t.Errorf("coverage project = %q, want %q", cov.Project, projectName)
	}

	if !slices.Equal(cov.Excluded, []string{legacyFile}) {
		t.Errorf("coverage excluded = %v, want [%s]", cov.Excluded, legacyFile)
	}
}

func TestCommerceIndexReplaysFromAnotherDirectory(t *testing.T) {
	c := newCommerce(t)

	// The repository path is not the directory of the recording (and it is not part of the call key).
	res := c.run(t, "--format", "json", "index", t.TempDir())
	if res.code != diag.OK {
		t.Fatalf("index exit code = %d, want %d\n%s", res.code, diag.OK, res.stderr)
	}

	cmrRequireIndexOutput(t, res.stdout)
	c.requireIndexCoverage(t)
}

// cmrRecordingGaps names what the recording lacks.
func cmrRecordingGaps(rec *observation.Recording) []string {
	var gaps []string

	if rec.Version == "" {
		gaps = append(gaps, "version")
	}

	if rec.HeadSHA == "" {
		gaps = append(gaps, "headSha")
	}

	if len(rec.Calls) == 0 {
		gaps = append(gaps, "calls")
	}

	return gaps
}

func TestRecordingHasTheVersionAndTheProject(t *testing.T) {
	rec := loadRecording(t)

	if gaps := cmrRecordingGaps(&rec); len(gaps) != 0 {
		t.Fatalf("recording is incomplete: version %q, headSha %q, %d calls", rec.Version, rec.HeadSHA, len(rec.Calls))
	}

	data, err := os.ReadFile(recordingFile)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(data, []byte(projectName)) {
		t.Errorf("recording does not contain %s", projectName)
	}
}
