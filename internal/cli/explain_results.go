package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/redact"
	"github.com/inclaro/concordia/internal/workspace"
)

const minPrefixLength = 4

// explainRun reads the results file (--results, else the JSON output of the
// workspace), or runs the pipeline without reports when there is no results file.
func explainRun(ctx context.Context, app *Context, resultsArg string) (run *conformance.Run, root sourceRoots, err error) {
	wsp, loadErr := app.LoadWorkspace()
	if loadErr != nil && !tolerableWorkspaceError(loadErr, resultsArg) {
		return nil, sourceRoots{}, loadErr
	}

	root = newSourceRoots(filepath.Dir(app.WorkspacePath))
	if wsp != nil {
		root = sourceRootsOf(wsp)
	}

	text, err := readExplainResults(explainResultsFile(app, wsp, resultsArg), resultsArg)
	if err != nil {
		return nil, sourceRoots{}, err
	}

	if text == nil {
		run, err = runPipeline(ctx, app)

		return run, root, err
	}

	run, err = decodeRun(text)
	if err == nil && wsp != nil {
		err = conformance.RequireCompleteRun(wsp.Root, run.Run.ID)
	}

	return run, root, err
}

// tolerableWorkspaceError says whether a workspace load error leaves `explain` able to read the named results file.
func tolerableWorkspaceError(loadErr error, resultsArg string) bool {
	var failure *diag.Error

	return resultsArg != "" && errors.As(loadErr, &failure)
}

// explainResultsFile gives the results file: --results, else the JSON output of the workspace, else "".
func explainResultsFile(app *Context, wsp *workspace.Workspace, resultsArg string) string {
	switch {
	case resultsArg != "":
		return app.Abs(resultsArg)
	case wsp != nil:
		return wsp.ResolveInRoot(jsonReportPath(wsp))
	}

	return ""
}

// jsonReportPath gives the path of the first JSON output of the workspace, else the default report path.
func jsonReportPath(wsp *workspace.Workspace) string {
	for _, o := range wsp.Report.Outputs {
		if o.Format == workspace.ReportJSON {
			return o.Path
		}
	}

	return workspace.DefaultReportPath
}

// readExplainResults reads the results file. An unreadable file is an error only when --results names it.
// Without a file, or with an unreadable default one, it gives nil.
func readExplainResults(file, resultsArg string) ([]byte, error) {
	if file == "" {
		return nil, nil
	}

	text, err := os.ReadFile(file) //nolint:gosec // the user names the results file.
	if err == nil {
		return text, nil
	}

	if resultsArg != "" {
		return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("cannot read results %s: %s", resultsArg, err)))
	}

	return nil, nil
}

// decodeRun reads a results file. It checks the parts that `explain` reads: the file is Concordia's own output.
func decodeRun(text []byte) (*conformance.Run, error) {
	if !json.Valid(text) {
		syntax := json.Unmarshal(text, new(any))

		return nil, diag.Fail(diag.WRK010, diag.Detail("results file is not JSON: "+syntax.Error()))
	}

	notResult := diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("results file is not a Concordia result (schemaVersion %s)", conformance.ResultSchemaVersion)))

	var probe map[string]json.RawMessage
	if json.Unmarshal(text, &probe) != nil || !looksLikeResult(probe) {
		return nil, notResult
	}

	var run conformance.Run
	if err := json.Unmarshal(text, &run); err != nil {
		return nil, diag.Fail(diag.WRK010, diag.Detail("results file is not a Concordia result: "+err.Error()))
	}

	return &run, nil
}

// looksLikeResult says whether the top-level members of a file have the schema version and the shapes of a Concordia result.
func looksLikeResult(probe map[string]json.RawMessage) bool {
	var version string

	_ = json.Unmarshal(probe["schemaVersion"], &version)

	return version == conformance.ResultSchemaVersion &&
		isJSONKind(probe["rules"], '[') && isJSONKind(probe["findings"], '[') && isJSONKind(probe["exceptions"], '{')
}

func isJSONKind(raw json.RawMessage, open byte) bool {
	trimmed := bytes.TrimSpace(raw)

	return len(trimmed) > 0 && trimmed[0] == open
}

func hexOf(fingerprint string) string { return strings.TrimPrefix(fingerprint, "sha256:") }

// selectFinding picks the finding of a full fingerprint or a unique prefix of its hex digest (at least 4 characters).
func selectFinding(rule *conformance.RuleResult, findings []*conformance.Finding, wanted string) (*conformance.Finding, error) {
	hex := hexOf(wanted)

	var hits []*conformance.Finding

	if len(hex) >= minPrefixLength {
		for _, f := range findings {
			if strings.HasPrefix(hexOf(f.Fingerprint), hex) {
				hits = append(hits, f)
			}
		}
	}

	switch len(hits) {
	case 0:
		return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("rule %s has no finding '%s'", rule.RuleID, wanted)))
	case 1:
		return hits[0], nil
	default:
		return nil, diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("finding prefix '%s' is not unique for rule %s", wanted, rule.RuleID)))
	}
}

// pickRuleAndFindings finds the rule, and its findings or the one finding that --finding names.
func pickRuleAndFindings(run *conformance.Run, ruleID, wanted string) (*conformance.RuleResult, []*conformance.Finding, error) {
	index := slices.IndexFunc(run.Rules, func(r conformance.RuleResult) bool { return r.RuleID == ruleID })
	if index < 0 {
		return nil, nil, unknownRuleFailure(run, ruleID)
	}

	rule := &run.Rules[index]
	own := findingsOfRule(run, rule.RuleID)

	if wanted == "" {
		return rule, own, nil
	}

	one, err := selectFinding(rule, own, wanted)
	if err != nil {
		return nil, nil, err
	}

	return rule, []*conformance.Finding{one}, nil
}

// unknownRuleFailure says that the run has no such rule, and which rules it has.
func unknownRuleFailure(run *conformance.Run, ruleID string) error {
	ids := make([]string, len(run.Rules))
	for i := range run.Rules {
		ids[i] = run.Rules[i].RuleID
	}

	known := strings.Join(ids, ", ")
	if known == "" {
		known = noneText
	}

	return diag.Fail(diag.WRK010, diag.Detail(fmt.Sprintf("unknown rule '%s'", ruleID)), diag.WithSuggestion("rules in the results: "+known))
}

// findingsOfRule gives the findings of the run for one rule.
func findingsOfRule(run *conformance.Run, ruleID string) []*conformance.Finding {
	var own []*conformance.Finding

	for i := range run.Findings {
		if run.Findings[i].RuleID == ruleID {
			own = append(own, &run.Findings[i])
		}
	}

	return own
}

// explainParts gathers what the output adds to the findings: the decision of each governance finding, the source
// contexts with --source, and the redaction records of both.
func explainParts(run *conformance.Run, root sourceRoots, selected []*conformance.Finding, withSource bool) (decisions map[string]*decisionExplanation, contexts map[string][]sourceContext, records []redact.Record) {
	decisions = map[string]*decisionExplanation{}
	contexts = map[string][]sourceContext{}

	for _, finding := range selected {
		if explained := explainDecision(run, finding); explained != nil {
			decisions[finding.Fingerprint] = explained
			records = append(records, explained.records...)
		}

		if withSource {
			contexts[finding.Fingerprint] = contextsOf(root, finding)
			records = append(records, sourceRedactionRecords(contexts[finding.Fingerprint])...)
		}
	}

	return decisions, contexts, records
}

// sourceRedactionRecords gives the redaction records of the source contexts, for the explain output.
func sourceRedactionRecords(contexts []sourceContext) []redact.Record {
	var records []redact.Record

	for _, c := range contexts {
		for _, r := range c.Redactions {
			r.Output = outputExplain
			records = append(records, r)
		}
	}

	return records
}

// contextsOf gives the source contexts of the locations of a finding, one for each path and line.
func contextsOf(root sourceRoots, finding *conformance.Finding) []sourceContext {
	var locations []conformance.Location

	add := func(l conformance.Location) {
		key := func(x conformance.Location) string { return x.Path + ":" + strconv.Itoa(x.Line) }

		if i := slices.IndexFunc(locations, func(x conformance.Location) bool { return key(x) == key(l) }); i >= 0 {
			locations[i] = l

			return
		}

		locations = append(locations, l)
	}

	if finding.Location != nil {
		add(*finding.Location)
	}

	for _, l := range finding.RelatedLocations {
		add(l)
	}

	out := []sourceContext{}

	for i := range locations {
		if c := readContext(root, &locations[i]); c != nil {
			out = append(out, *c)
		}
	}

	return out
}
