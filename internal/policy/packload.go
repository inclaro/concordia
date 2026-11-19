package policy

import (
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy/packs"
	"github.com/inclaro/concordia/internal/workspace"
	"github.com/inclaro/concordia/internal/yamlparse"
)

const remoteMarker = "://"

// RuleInfo is the short description of a rule of a pack. Family, Enforcement and
// Obligation show the declaration of a typed rule (Obligation is nil for the other
// rules). The description needs no architecture model: the relation targets are empty.
type RuleInfo struct {
	Obligation  *ObligationSpec `json:"obligation,omitempty"`
	ID          string          `json:"id"`
	Severity    RuleSeverity    `json:"severity"`
	Name        string          `json:"name"`
	Family      string          `json:"family,omitempty"`
	Enforcement Enforcement     `json:"enforcement,omitempty"`
}

// PackInfo is the short description of a policy pack.
type PackInfo struct {
	Precedence *Precedence `json:"precedence,omitempty"`
	ID         string      `json:"id"`
	Version    string      `json:"version"`
	Rules      []RuleInfo  `json:"rules"`
}

// ResolvePacks reads the packs of `policies.packs` and checks their pins. A
// built-in pack comes from the binary. A file pack is a file under root (or an
// absolute path) and its digest must be the `sha256` of the entry. The result
// has one PolicyFile for each pack that resolved, in the order of uses, with the
// rule overrides of the entry. The diagnostics (CO-POL-010 to CO-POL-014) are
// errors: use ThrowOnErrors to stop the run. This function never uses the network.
func ResolvePacks(root string, uses []workspace.PolicyPackUse) ([]PolicyFile, []diag.Diagnostic) {
	files := make([]PolicyFile, 0, len(uses))

	var problems []diag.Diagnostic

	for i := range uses {
		file, problem := resolvePack(root, &uses[i])
		if problem != nil {
			problems = append(problems, *problem)

			continue
		}

		files = append(files, file)
	}

	return files, problems
}

func resolvePack(root string, use *workspace.PolicyPackUse) (PolicyFile, *diag.Diagnostic) {
	switch {
	case strings.HasPrefix(use.Use, packs.Prefix):
		return resolveBuiltin(use)
	case strings.Contains(use.Use, remoteMarker):
		d := diag.New(diag.POL014, diag.Params{"source": use.Use}, diag.WithSource(diag.Source{URI: use.Use}))

		return PolicyFile{}, &d
	default:
		return resolveFilePack(root, use)
	}
}

func resolveBuiltin(use *workspace.PolicyPackUse) (PolicyFile, *diag.Diagnostic) {
	name := strings.TrimPrefix(use.Use, packs.Prefix)
	at := diag.WithSource(diag.Source{URI: use.Use})

	text, ok := packs.Source(name)
	if !ok {
		d := diag.New(diag.POL012, diag.Params{"name": name}, at)

		return PolicyFile{}, &d
	}

	if use.SHA256 != "" {
		d := diag.New(diag.POL013, diag.Detail(use.Use+" is built in: remove its sha256, the binary pins it"), at)

		return PolicyFile{}, &d
	}

	return PolicyFile{Path: use.Use, Use: use.Use, Text: string(text), Overrides: use.Rules}, nil
}

func resolveFilePack(root string, use *workspace.PolicyPackUse) (PolicyFile, *diag.Diagnostic) {
	display := fsutil.ToPosix(use.Use)
	where := diag.WithSource(diag.Source{URI: display})

	if use.SHA256 == "" {
		d := diag.New(diag.POL013, diag.Detail(display+" needs a sha256 pin"),
			diag.WithSuggestion("run `concordia policy add "+display+" --force` to pin the file"), where)

		return PolicyFile{}, &d
	}

	abs := use.Use
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, filepath.FromSlash(display))
	}

	text, err := os.ReadFile(abs) //nolint:gosec // the pack path comes from the workspace file, which the user controls.
	if err != nil {
		d := diag.New(diag.POL011, diag.Params{"path": display}, where)

		return PolicyFile{}, &d
	}

	if sum := digest.OfBytes(text); sum != use.SHA256 {
		d := diag.New(diag.POL010, diag.Detail(fmt.Sprintf("%s has %s, the workspace pins %s", display, sum, use.SHA256)), where)

		return PolicyFile{}, &d
	}

	return PolicyFile{Path: display, Use: display, Text: string(text), Overrides: use.Rules}, nil
}

// parsePack parses and validates a pack text. A problem with the text gives
// diagnostics (CO-POL-001); the error is for an internal failure.
func parsePack(file PolicyFile) (rawPack, []diag.Diagnostic, error) {
	raw, problems := yamlparse.Parse([]byte(file.Text))
	if len(problems) > 0 {
		return rawPack{}, syntaxDiagnostics(file, problems), nil
	}

	if found := schemaDiagnostics(raw, file.Path); len(found) > 0 {
		return rawPack{}, found, nil
	}

	pack, err := decodePack(raw)

	return pack, nil, err
}

// DescribePack validates a pack text against the pack schema and gives its
// metadata and rules. It needs no architecture model. A pack with problems
// gives CO-POL-001 diagnostics.
func DescribePack(file PolicyFile) (PackInfo, []diag.Diagnostic) {
	pack, problems, err := parsePack(file)
	if err != nil {
		return PackInfo{}, []diag.Diagnostic{diag.New(diag.POL001, diag.Detail(err.Error()), diag.WithSource(diag.Source{URI: file.Path}))}
	}

	if len(problems) > 0 {
		return PackInfo{}, problems
	}

	info := PackInfo{
		ID: pack.Metadata.ID, Version: pack.Metadata.Version, Rules: make([]RuleInfo, len(pack.Rules)), Precedence: pack.Precedence,
	}

	var found []diag.Diagnostic

	comp := &compiler{
		uri: file.Path, pack: PackRef{ID: pack.Metadata.ID, Version: pack.Metadata.Version}, named: pack.Selectors,
		diagnostics: &found, modelFree: true,
	}

	for i := range pack.Rules {
		rule := &pack.Rules[i]

		var problem *diag.Diagnostic

		info.Rules[i], problem = describeRule(comp, rule, "/rules/"+strconv.Itoa(i))
		if problem != nil {
			found = append(found, *problem)
		}
	}

	if len(found) > 0 {
		return PackInfo{}, diag.Sort(found)
	}

	return info, nil
}

// describeRule gives the metadata of a rule and the problem that its declaration has, if any.
func describeRule(comp *compiler, rule *rawRule, pointer string) (RuleInfo, *diag.Diagnostic) {
	info := RuleInfo{
		ID: rule.ID, Severity: rule.Severity, Name: rule.Name, Family: rule.Family, Enforcement: rule.Enforcement,
	}

	switch {
	case rule.Decisions != nil:
		_, problem := comp.decisionsShape(rule, pointer)

		return info, problem
	case rule.Obligation != nil:
		built, problem := comp.obligationShape(rule, pointer)
		if problem != nil {
			return info, problem
		}

		info.Obligation = built.obligation
	}

	return info, nil
}

// OverrideProblems gives a CO-POL-015 error for each override of the file that
// names a rule that the pack does not have. The order is the order of the rule IDs.
func OverrideProblems(file PolicyFile, info PackInfo) []diag.Diagnostic {
	ids := make([]string, 0, len(file.Overrides))

	for id := range file.Overrides {
		if !slices.ContainsFunc(info.Rules, func(r RuleInfo) bool { return r.ID == id }) {
			ids = append(ids, id)
		}
	}

	slices.SortFunc(ids, canonjson.CompareUTF16)

	out := make([]diag.Diagnostic, len(ids))

	for i, id := range ids {
		out[i] = diag.New(diag.POL015, diag.Params{idParam: id, "pack": info.ID},
			diag.WithSource(diag.Source{URI: file.Path, Pointer: "/rules/" + id}))
	}

	return out
}

// applyOverrides changes the rules of a pack by the overrides of its file. It
// gives the IDs of the disabled rules and the CO-POL-015 errors of the
// overrides that name an unknown rule.
func applyOverrides(file PolicyFile, pack *rawPack) (disabled map[string]bool, problems []diag.Diagnostic) {
	if len(file.Overrides) == 0 {
		return nil, nil
	}

	info := PackInfo{ID: pack.Metadata.ID, Rules: make([]RuleInfo, len(pack.Rules))}
	for i := range pack.Rules {
		info.Rules[i].ID = pack.Rules[i].ID
	}

	problems = OverrideProblems(file, info)
	disabled = map[string]bool{}

	for i := range pack.Rules {
		rule := &pack.Rules[i]

		override, ok := file.Overrides[rule.ID]
		if !ok {
			continue
		}

		if applyRuleOverride(rule, override) {
			disabled[rule.ID] = true
		}
	}

	return disabled, problems
}

// applyRuleOverride changes a rule by its override and says whether the override disables the rule.
func applyRuleOverride(rule *rawRule, override workspace.RuleOverride) bool {
	if override.Severity != "" {
		rule.Severity = RuleSeverity(override.Severity)
	}

	if override.Owner != "" {
		rule.Owner = override.Owner
	}

	if override.Evidence != nil {
		rule.Evidence = mergeEvidence(rule.Evidence, override.Evidence)
	}

	return override.Enabled != nil && !*override.Enabled
}

// mergeEvidence sets the fields of over on a copy of base. `minimumCoverage`
// and `coverage.files` are two names for one value: a coverage in over replaces
// both fields of base, and `minimumCoverage` wins when over has both. Each other
// value of over replaces the value of base; `edgeKinds` replaces by key.
func mergeEvidence(base *rawEvidence, over *workspace.RuleEvidenceOverride) *rawEvidence {
	merged := rawEvidence{}
	if base != nil {
		merged = *base
	}

	merged.Coverage = mergeCoverage(merged.Coverage, over, &merged)

	if over.MinimumConfidence != "" {
		merged.MinimumConfidence = observation.Confidence(over.MinimumConfidence)
	}

	if over.OnUnknown != "" {
		merged.OnUnknown = UnknownDisposition(over.OnUnknown)
	}

	if len(over.EdgeKinds) > 0 {
		kinds := make(map[string]observation.Confidence, len(merged.EdgeKinds)+len(over.EdgeKinds))
		maps.Copy(kinds, merged.EdgeKinds)

		for kind, confidence := range over.EdgeKinds {
			kinds[kind] = observation.Confidence(confidence)
		}

		merged.EdgeKinds = kinds
	}

	return &merged
}

// mergeCoverage gives the `coverage` block after the override. A file ratio in over replaces
// `minimumCoverage` (set on merged) and `coverage.files`; bytes and supported languages replace
// their values. The base block is not changed.
func mergeCoverage(base *rawCoverage, over *workspace.RuleEvidenceOverride, merged *rawEvidence) *rawCoverage {
	coverage := rawCoverage{}
	if base != nil {
		coverage = *base
	}

	if files := overrideCoverageFiles(over); files != nil {
		merged.MinimumCoverage = files
		coverage.Files = nil
	}

	if over.Coverage != nil {
		if over.Coverage.Bytes != nil {
			coverage.Bytes = over.Coverage.Bytes
		}

		if over.Coverage.SupportedLanguages != nil {
			coverage.SupportedLanguages = over.Coverage.SupportedLanguages
		}
	}

	if coverage == (rawCoverage{}) {
		return nil
	}

	return &coverage
}

// overrideCoverageFiles gives the file ratio of the override: `minimumCoverage` wins over `coverage.files`.
// It is nil when the override has neither.
func overrideCoverageFiles(over *workspace.RuleEvidenceOverride) *float64 {
	if over.MinimumCoverage == nil && over.Coverage != nil {
		return over.Coverage.Files
	}

	return over.MinimumCoverage
}

// LoadWorkspaceFiles reads the policy sources that the workspace configures:
// the files that match `policies.files`, then the resolved `policies.packs`. A
// file that a pack entry names is not read a second time as a plain file (the
// default glob matches the vendor directory). The pack diagnostics are
// returned in problems; the error is a *diag.Error for an unreadable file.
func LoadWorkspaceFiles(wsp *workspace.Workspace) (files []PolicyFile, problems []diag.Diagnostic, err error) {
	expanded, err := binding.ExpandFileGlobs(wsp.Root, wsp.Policies.Files)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // already has context.
	}

	pinned := map[string]bool{}

	for i := range wsp.Policies.Packs {
		if use := wsp.Policies.Packs[i].Use; !strings.HasPrefix(use, packs.Prefix) {
			pinned[path.Clean(fsutil.ToPosix(use))] = true
		}
	}

	expanded = slices.DeleteFunc(expanded, func(p string) bool { return pinned[path.Clean(p)] })

	files, err = ReadPolicyFiles(wsp.Root, expanded)
	if err != nil {
		return nil, nil, err
	}

	packFiles, problems := ResolvePacks(wsp.Root, wsp.Policies.Packs)

	return append(files, packFiles...), problems, nil
}
