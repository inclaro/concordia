package policy_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
	"github.com/inclaro/concordia/internal/testutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	vendorPath = "policies/vendor/demo-1.0.0.policy.yaml"
	builtinUse = "builtin:core"
)

func vendorText() string {
	return pack(rule("    subject: {nodeTypes: [service]}\n    declaredOnly: {edges: [IMPORTS]}\n")).Text
}

func sumOf(text string) string { return digest.OfBytes([]byte(text)) }

func resolveCodes(problems []diag.Diagnostic) []string {
	out := make([]string, len(problems))
	for i := range problems {
		out[i] = problems[i].Code
	}

	return out
}

func TestResolvePacksBuiltin(t *testing.T) {
	t.Parallel()

	overrides := map[string]workspace.RuleOverride{"CORE-CYC-001": {Severity: "warning"}}

	files, problems := policy.ResolvePacks(t.TempDir(), []workspace.PolicyPackUse{{Use: builtinUse, Rules: overrides}})
	requireOneResolvedPack(t, files, problems)

	want, _ := packs.Source("core")

	got := [4]string{files[0].Path, files[0].Use, files[0].Text, files[0].Overrides["CORE-CYC-001"].Severity}
	if got != [4]string{builtinUse, builtinUse, string(want), "warning"} {
		t.Errorf("file = %+v", files[0])
	}
}

// requireOneResolvedPack stops the test unless exactly one pack was resolved without problems.
func requireOneResolvedPack(t *testing.T, files []policy.PolicyFile, problems []diag.Diagnostic) {
	t.Helper()

	if len(problems) != 0 {
		t.Fatalf("files %d problems %+v", len(files), problems)
	}

	if len(files) != 1 {
		t.Fatalf("files %d problems %+v", len(files), problems)
	}
}

func TestResolvePacksPathWithMatchingDigest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{vendorPath: vendorText()})

	files, problems := policy.ResolvePacks(root, []workspace.PolicyPackUse{{Use: vendorPath, SHA256: sumOf(vendorText())}})
	requireOneResolvedPack(t, files, problems)

	got := [3]string{files[0].Path, files[0].Use, files[0].Text}
	if got != [3]string{vendorPath, vendorPath, vendorText()} {
		t.Errorf("file = %+v", files[0])
	}
}

type packProblemCase struct {
	name string
	use  workspace.PolicyPackUse
	code string
	text string
}

func packProblemCases() []packProblemCase {
	zeroDigest := "sha256:" + strings.Repeat("0", 64)

	return []packProblemCase{
		{"digest mismatch", workspace.PolicyPackUse{Use: vendorPath, SHA256: zeroDigest}, diag.POL010, "pins"},
		{"missing file", workspace.PolicyPackUse{Use: "policies/vendor/none.policy.yaml", SHA256: zeroDigest}, diag.POL011, "none.policy.yaml"},
		{"missing sha256", workspace.PolicyPackUse{Use: vendorPath}, diag.POL013, "needs a sha256"},
		{"unknown builtin", workspace.PolicyPackUse{Use: "builtin:nope"}, diag.POL012, "nope"},
		{"sha256 on builtin", workspace.PolicyPackUse{Use: builtinUse, SHA256: zeroDigest}, diag.POL013, "built in"},
		{"https source", workspace.PolicyPackUse{Use: "https://example.org/pack.policy.yaml", SHA256: zeroDigest}, diag.POL014, "https://example.org"},
		{"http source", workspace.PolicyPackUse{Use: "http://example.org/pack.policy.yaml"}, diag.POL014, "http://example.org"},
	}
}

func TestResolvePacksProblems(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	testutil.WriteTree(t, root, map[string]string{vendorPath: vendorText()})

	cases := packProblemCases()
	for i := range cases {
		tc := &cases[i]

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			requireOnePackProblem(t, root, tc)
		})
	}
}

// requireOnePackProblem resolves the pack of tc and checks the single problem it gives.
func requireOnePackProblem(t *testing.T, root string, tc *packProblemCase) {
	t.Helper()

	files, problems := policy.ResolvePacks(root, []workspace.PolicyPackUse{tc.use})
	if len(files) != 0 || len(problems) != 1 {
		t.Fatalf("files %d problems %+v", len(files), problems)
	}

	got := problems[0]
	if got.Code != tc.code {
		t.Errorf("problem = %+v", got)
	}

	if got.Severity != diag.SeverityError {
		t.Errorf("problem = %+v", got)
	}

	if !strings.Contains(got.Message, tc.text) {
		t.Errorf("problem = %+v", got)
	}

	if code, _ := diag.ExitCodeOf(got.Code); code != diag.InvalidInput {
		t.Errorf("exit code %d", code)
	}

	if err := policy.ThrowOnErrors(problems); err == nil {
		t.Error("ThrowOnErrors gave nil")
	}
}

func TestResolvePacksReportsEveryProblemAndKeepsTheGoodPacks(t *testing.T) {
	t.Parallel()

	files, problems := policy.ResolvePacks(t.TempDir(), []workspace.PolicyPackUse{
		{Use: "builtin:nope"}, {Use: builtinUse}, {Use: vendorPath},
	})

	if len(files) != 1 || files[0].Use != builtinUse {
		t.Errorf("files = %+v", files)
	}

	if got := resolveCodes(problems); !slices.Equal(got, []string{diag.POL012, diag.POL013}) {
		t.Errorf("codes = %v", got)
	}
}

func withOverrides(text string, overrides map[string]workspace.RuleOverride) policy.PolicyFile {
	return policy.PolicyFile{Path: "builtin:demo", Use: "builtin:demo", Text: text, Overrides: overrides}
}

func twoRules() string {
	return pack(
		rule("    subject: {nodeTypes: [service]}\n    declaredOnly: {edges: [IMPORTS]}\n    owner: team-a\n    evidence: {minimumCoverage: 0.9, onUnknown: fail}\n", "CO-A-001") +
			rule("    subject: {nodeTypes: [service]}\n    declaredOnly: {edges: [IMPORTS]}\n", "CO-A-002"),
	).Text
}

func planOf(t *testing.T, res *policy.CompileResult, id string) policy.RulePlan {
	t.Helper()

	i := slices.IndexFunc(res.Plans, func(p policy.RulePlan) bool { return p.RuleID == id })
	if i < 0 {
		t.Fatalf("no plan for %s in %v", id, res.RuleIDs)
	}

	return res.Plans[i]
}

func TestOverridesChangeSeverityOwnerAndEvidence(t *testing.T) {
	t.Parallel()

	coverage := 0.8
	res := compile(t, []policy.PolicyFile{withOverrides(twoRules(), map[string]workspace.RuleOverride{
		"CO-A-001": {Severity: "warning", Owner: "team-platform", Evidence: &workspace.RuleEvidenceOverride{
			MinimumCoverage: &coverage, OnUnknown: "warn", MinimumConfidence: "resolved",
		}},
		"CO-A-002": {Severity: "info"},
	})}, model())

	wantCodes(t, &res)

	first := planOf(t, &res, "CO-A-001")
	requireFirstOverriddenPlan(t, &first)

	second := planOf(t, &res, "CO-A-002")
	if second.Severity != policy.SeverityInfo {
		t.Errorf("second = %+v", second)
	}

	if second.Owner != "" {
		t.Errorf("second = %+v", second)
	}
}

// requireFirstOverriddenPlan checks the severity, owner, evidence and pack of the plan whose rule the overrides change in full.
func requireFirstOverriddenPlan(t *testing.T, first *policy.RulePlan) {
	t.Helper()

	if first.Severity != policy.SeverityWarning {
		t.Errorf("severity %q owner %q", first.Severity, first.Owner)
	}

	if first.Owner != "team-platform" {
		t.Errorf("severity %q owner %q", first.Severity, first.Owner)
	}

	ev := first.EvidenceRequirements
	if ev.MinimumCoverage != 0.8 {
		t.Errorf("evidence = %+v", ev)
	}

	if ev.OnUnknown != policy.UnknownWarn {
		t.Errorf("evidence = %+v", ev)
	}

	if ev.MinimumConfidence != "resolved" {
		t.Errorf("evidence = %+v", ev)
	}

	if got := [2]string{first.Pack.ID, first.Pack.Version}; got != [2]string{"demo", "1.0.0"} {
		t.Errorf("pack = %+v", first.Pack)
	}
}

func TestOverrideEvidenceIsPartial(t *testing.T) {
	t.Parallel()

	res := compile(t, []policy.PolicyFile{withOverrides(twoRules(), map[string]workspace.RuleOverride{
		"CO-A-001": {Evidence: &workspace.RuleEvidenceOverride{OnUnknown: "ignore"}},
	})}, model())

	ev := planOf(t, &res, "CO-A-001").EvidenceRequirements
	if ev.OnUnknown != policy.UnknownIgnore || ev.MinimumCoverage != 0.9 {
		t.Errorf("evidence = %+v, want onUnknown ignore and the rule coverage 0.9", ev)
	}
}

func TestOverrideCoverageFilesReplacesMinimumCoverageOfTheRule(t *testing.T) {
	t.Parallel()

	files := 0.5
	res := compile(t, []policy.PolicyFile{withOverrides(twoRules(), map[string]workspace.RuleOverride{
		"CO-A-001": {Evidence: &workspace.RuleEvidenceOverride{Coverage: &workspace.RuleCoverageOverride{Files: &files}}},
	})}, model())

	wantCodes(t, &res)

	if got := planOf(t, &res, "CO-A-001").EvidenceRequirements.MinimumCoverage; got != 0.5 {
		t.Errorf("coverage = %v", got)
	}
}

func TestDisabledRuleIsDroppedButStaysKnown(t *testing.T) {
	t.Parallel()

	off := false
	on := true
	res := compile(t, []policy.PolicyFile{withOverrides(twoRules(), map[string]workspace.RuleOverride{
		"CO-A-001": {Enabled: &off},
		"CO-A-002": {Enabled: &on},
	})}, model())

	wantCodes(t, &res)

	if len(res.Plans) != 1 || res.Plans[0].RuleID != "CO-A-002" {
		t.Errorf("plans = %+v", res.Plans)
	}

	if !slices.Equal(res.RuleIDs, []string{"CO-A-001", "CO-A-002"}) {
		t.Errorf("rule IDs = %v, a disabled rule is still declared by its pack", res.RuleIDs)
	}
}

func TestOverrideForUnknownRuleIsAnError(t *testing.T) {
	t.Parallel()

	res := compile(t, []policy.PolicyFile{withOverrides(twoRules(), map[string]workspace.RuleOverride{
		"CO-A-001": {Severity: "info"}, "CO-TYPO-001": {Severity: "info"}, "CO-TYPO-002": {},
	})}, model())

	wantCodes(t, &res, diag.POL015, diag.POL015)

	if !strings.Contains(res.Diagnostics[0].Message, "CO-TYPO-001") || !strings.Contains(res.Diagnostics[0].Message, "demo") {
		t.Errorf("message = %q", res.Diagnostics[0].Message)
	}

	if err := policy.ThrowOnErrors(res.Diagnostics); err == nil {
		t.Error("ThrowOnErrors gave nil")
	}
}

func TestSameRuleIDInTwoPacksIsADuplicate(t *testing.T) {
	t.Parallel()

	one := withOverrides(twoRules(), nil)
	two := policy.PolicyFile{Path: "policies/vendor/other.policy.yaml", Use: "policies/vendor/other.policy.yaml", Text: twoRules()}

	res := compile(t, []policy.PolicyFile{one, two}, model())

	wantCodes(t, &res, diag.POL003, diag.POL003)
}

func TestCompiledPackRefsCarryTheUseValue(t *testing.T) {
	t.Parallel()

	plain := pack(rule("    subject: {nodeTypes: [service]}\n    declaredOnly: {edges: [IMPORTS]}\n", "CO-P-001"))
	res := compile(t, []policy.PolicyFile{plain, withOverrides(twoRules(), nil)}, model())

	if len(res.Packs) != 2 {
		t.Fatalf("packs = %+v", res.Packs)
	}

	for _, ref := range res.Packs {
		switch ref.Path {
		case plain.Path:
			if ref.Use != "" {
				t.Errorf("a plain file has use %q", ref.Use)
			}
		case "builtin:demo":
			if ref.Use != "builtin:demo" || ref.Digest != sumOf(twoRules()) {
				t.Errorf("pack ref = %+v", ref)
			}
		default:
			t.Errorf("unexpected pack %+v", ref)
		}
	}
}

func TestDescribePack(t *testing.T) {
	t.Parallel()

	info, problems := policy.DescribePack(policy.PolicyFile{Path: "p", Text: twoRules()})
	if len(problems) != 0 {
		t.Fatalf("info %+v problems %+v", info, problems)
	}

	if got := [3]string{info.ID, info.Version, strconv.Itoa(len(info.Rules))}; got != [3]string{"demo", "1.0.0", "2"} {
		t.Fatalf("info %+v problems %+v", info, problems)
	}

	if info.Rules[0].ID != "CO-A-001" {
		t.Fatalf("info %+v problems %+v", info, problems)
	}
}

func TestDescribePackRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		text string
	}{
		{"another kind", "kind: Other\n"},
		{"syntax error", "a: [\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, problems := policy.DescribePack(policy.PolicyFile{Path: "p", Text: tc.text})
			if len(problems) == 0 {
				t.Fatalf("problems = %+v", problems)
			}

			if problems[0].Code != diag.POL001 {
				t.Errorf("problems = %+v", problems)
			}
		})
	}
}

func TestOverrideProblemsListsUnknownRulesInOrder(t *testing.T) {
	t.Parallel()

	file := withOverrides(twoRules(), map[string]workspace.RuleOverride{"CO-Z-001": {}, "CO-A-001": {}, "CO-B-001": {}})

	info, _ := policy.DescribePack(file)

	got := policy.OverrideProblems(file, info)
	if len(got) != 2 {
		t.Fatalf("problems = %+v", got)
	}

	for i, id := range []string{"CO-B-001", "CO-Z-001"} {
		if !strings.Contains(got[i].Message, id) {
			t.Errorf("problems = %+v", got)
		}
	}
}

func TestLoadWorkspaceFilesReadsFilesAndPacksAndSkipsPinnedFilesOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	vendor := strings.Replace(vendorText(), "CO-X-001", "CO-V-001", 1)
	loose := strings.Replace(vendorText(), "CO-X-001", "CO-L-001", 1)

	testutil.WriteTree(t, root, map[string]string{
		"policies/local.policy.yaml": strings.Replace(vendorText(), "CO-X-001", "CO-M-001", 1),
		vendorPath:                   vendor,
		"policies/vendor/loose-1.0.0.policy.yaml": loose,
		"policies/vendor/not-a-policy.txt":        "x",
		"concordia.yaml":                          "x: 1\n",
	})

	wsp := &workspace.Workspace{
		Root: root,
		Policies: workspace.Policies{
			Files: []string{"policies/**/*.policy.yaml"},
			Packs: []workspace.PolicyPackUse{{Use: vendorPath, SHA256: sumOf(vendor)}, {Use: builtinUse}},
		},
	}

	files, problems, err := policy.LoadWorkspaceFiles(wsp)
	if err != nil || len(problems) != 0 {
		t.Fatalf("err %v problems %+v", err, problems)
	}

	got := make([]string, len(files))
	for i := range files {
		got[i] = files[i].Path
	}

	want := []string{"policies/local.policy.yaml", "policies/vendor/loose-1.0.0.policy.yaml", vendorPath, builtinUse}
	if !slices.Equal(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}

	if files[2].Use != vendorPath || files[0].Use != "" {
		t.Errorf("use values = %q %q", files[2].Use, files[0].Use)
	}
}

func TestLoadWorkspaceFilesGivesPackProblemsWithoutFailing(t *testing.T) {
	t.Parallel()

	wsp := &workspace.Workspace{
		Root:     t.TempDir(),
		Policies: workspace.Policies{Files: []string{}, Packs: []workspace.PolicyPackUse{{Use: "builtin:nope"}}},
	}

	files, problems, err := policy.LoadWorkspaceFiles(wsp)
	if err != nil {
		t.Fatalf("files %v problems %+v err %v", files, problems, err)
	}

	if len(files) != 0 || len(problems) != 1 {
		t.Fatalf("files %v problems %+v err %v", files, problems, err)
	}

	if problems[0].Code != diag.POL012 {
		t.Fatalf("files %v problems %+v err %v", files, problems, err)
	}
}

func TestOverrideAddsBytesLanguagesAndEdgeKinds(t *testing.T) {
	t.Parallel()

	bytes, langs := 0.9, 0.7
	res := compile(t, []policy.PolicyFile{withOverrides(twoRules(), map[string]workspace.RuleOverride{
		"CO-A-001": {Evidence: &workspace.RuleEvidenceOverride{
			Coverage: &workspace.RuleCoverageOverride{Bytes: &bytes, SupportedLanguages: &langs},
		}},
	})}, model())

	wantCodes(t, &res)

	req := planOf(t, &res, "CO-A-001").EvidenceRequirements
	if req.MinimumByteCoverage == nil {
		t.Fatalf("requirements = %+v", req)
	}

	if *req.MinimumByteCoverage != 0.9 {
		t.Errorf("requirements = %+v", req)
	}

	if req.LanguageThreshold() != 0.7 {
		t.Errorf("requirements = %+v", req)
	}

	if req.MinimumCoverage != 0.9 {
		t.Errorf("requirements = %+v", req)
	}
}

func TestOverrideEdgeKindsReplaceTheRuleValuesByKey(t *testing.T) {
	t.Parallel()

	text := pack(rule("    subject: {nodeTypes: [service]}\n    declaredOnly: {edges: [IMPORTS, CALLS]}\n    evidence: {edgeKinds: {imports: resolved, calls: heuristic}}\n", "CO-A-001")).Text
	res := compile(t, []policy.PolicyFile{withOverrides(text, map[string]workspace.RuleOverride{
		"CO-A-001": {Evidence: &workspace.RuleEvidenceOverride{EdgeKinds: map[string]string{"calls": "runtime"}}},
	})}, model())

	wantCodes(t, &res)

	req := planOf(t, &res, "CO-A-001").EvidenceRequirements
	if req.MinimumConfidenceFor("calls") != "runtime" || req.MinimumConfidenceFor("imports") != "resolved" {
		t.Errorf("edge kinds = %+v", req.EdgeKindConfidence)
	}
}
