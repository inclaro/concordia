package policy

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
)

// CompileResult is the result of CompilePolicies.
type CompileResult struct {
	// Plans are sorted by rule ID. Rules with an error diagnostic are absent.
	Plans []RulePlan
	// Packs are sorted by pack ID, then path.
	Packs []PolicyPackRef
	// RuleIDs are the IDs of all rules that the packs declare, before the --rule filter. Sorted.
	RuleIDs []string
	// DeclaredRules are the rules that the packs declare with the pack of each, before the --rule filter. Sorted by rule ID.
	DeclaredRules []DeclaredRule
	Diagnostics   []diag.Diagnostic
}

const (
	defaultCoverage   = 0.95
	defaultMaxDepth   = 4
	idParam           = "id"
	apiVersion        = "concordia.io/v1alpha1"
	directionOutbound = "outbound"
	directionInbound  = "inbound"
)

var predicateKeys = []string{
	"forbid", "layerOrder", "declaredOnly", "observedRequired", "require",
	"denyPath", "allowOnly", "independent", "publicApiOnly", "ownership", "budget", "obligation", "viewCurrency", "decisions",
	"assessmentCurrency",
}

var selectorKeys = []string{"calmNodes", "nodeTypes", "tags", "ref"}

// CompilePolicies compiles policy packs into rule plans.
//
// Error diagnostics (CO-POL-001..005) do not stop the compile: the rule (or the
// whole pack, for schema errors) is left out of Plans and the diagnostic is in
// Diagnostics. Use ThrowOnErrors to stop the run. An unknown overrides.RuleIDs
// entry gives a CO-WRK-010 *diag.Error. A nil overrides sets nothing.
//
// Precedence of minimumConfidence, minimumCoverage and onUnknown (highest first):
// CLI flag, control configuration, rule `evidence`, workspace `check`, default.
func CompilePolicies(files []PolicyFile, model *intent.ArchitectureModel, caps ProviderCaps, overrides *CompileOverrides) (CompileResult, error) {
	if overrides == nil {
		overrides = &CompileOverrides{}
	}

	run := &compilation{
		index:     newNodeIndex(model),
		caps:      caps,
		overrides: overrides,
		controls:  ControlOverrides(model),
		checkIDs:  knownCheckIDs(model, overrides.KnownCheckIDs),
		packs:     []PolicyPackRef{},
		rules:     ruleSet{owner: map[string]string{}, ids: []string{}},
	}

	sorted := slices.Clone(files)
	slices.SortStableFunc(sorted, func(a, b PolicyFile) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	for _, file := range sorted {
		if err := run.addFile(file); err != nil {
			return CompileResult{}, err
		}
	}

	return run.result()
}

// compilation is the state of one CompilePolicies run.
type compilation struct {
	overrides   *CompileOverrides
	controls    ControlOverrideMap
	checkIDs    map[string]struct{}
	index       nodeIndex
	caps        ProviderCaps
	rules       ruleSet
	packs       []PolicyPackRef
	diagnostics []diag.Diagnostic
}

// addFile compiles the rules of one policy file. A file that does not parse adds its problems and no rules.
func (r *compilation) addFile(file PolicyFile) error {
	pack, problems, err := parsePack(file)
	if err != nil {
		return err
	}

	if len(problems) > 0 {
		r.diagnostics = append(r.diagnostics, problems...)

		return nil
	}

	disabled, overrideProblems := applyOverrides(file, &pack)
	r.diagnostics = append(r.diagnostics, overrideProblems...)

	ref := PackRef{ID: pack.Metadata.ID, Version: pack.Metadata.Version}
	r.packs = append(r.packs, PolicyPackRef{
		ID: ref.ID, Version: ref.Version, Path: file.Path, Use: file.Use, Digest: digest.OfBytes([]byte(file.Text)),
		Precedence: pack.Precedence,
	})

	comp := &compiler{
		uri: file.Path, pack: ref, named: pack.Selectors, index: r.index, caps: r.caps,
		overrides: *r.overrides, controls: r.controls, diagnostics: &r.diagnostics, checkIDs: r.checkIDs,
	}

	r.rules.add(comp, file.Path, &pack, disabled)

	return nil
}

// result applies the --rule filter and sorts what the files gave.
func (r *compilation) result() (CompileResult, error) {
	kept, err := filterRules(r.rules.plans, r.rules.ids, r.overrides.RuleIDs)
	if err != nil {
		return CompileResult{}, err
	}

	slices.SortStableFunc(kept, func(a, b RulePlan) int { return canonjson.CompareUTF16(a.RuleID, b.RuleID) })
	slices.SortStableFunc(r.packs, func(a, b PolicyPackRef) int {
		return cmp.Or(canonjson.CompareUTF16(a.ID, b.ID), canonjson.CompareUTF16(a.Path, b.Path))
	})

	return CompileResult{
		Plans:         nonNil(kept),
		Packs:         r.packs,
		RuleIDs:       uniqSorted(r.rules.ids),
		DeclaredRules: sortedDeclared(r.rules.declared),
		Diagnostics:   diag.Sort(r.diagnostics),
	}, nil
}

// sortedDeclared gives the declared rules sorted by rule ID. A rule ID is declared once, so the order is total.
func sortedDeclared(declared []DeclaredRule) []DeclaredRule {
	out := slices.Clone(declared)
	slices.SortStableFunc(out, func(a, b DeclaredRule) int { return canonjson.CompareUTF16(a.RuleID, b.RuleID) })

	if out == nil {
		return []DeclaredRule{}
	}

	return out
}

// ruleSet collects the rules of all the packs of one compile.
type ruleSet struct {
	// owner is the path of the file that declared each rule ID.
	owner map[string]string
	// ids are the IDs of all declared rules, also of the disabled ones.
	ids      []string
	declared []DeclaredRule
	plans    []RulePlan
}

// add compiles the rules of one pack. A rule ID that an earlier pack declared
// gives CO-POL-003. A disabled rule is declared but not compiled.
func (rs *ruleSet) add(comp *compiler, path string, pack *rawPack, disabled map[string]bool) {
	for i := range pack.Rules {
		rule := &pack.Rules[i]

		if !rs.declare(comp, path, rule, i) || disabled[rule.ID] {
			continue
		}

		plan, d := comp.compileRule(rule, i)
		if d != nil {
			*comp.diagnostics = append(*comp.diagnostics, *d)

			continue
		}

		rs.plans = append(rs.plans, plan)
	}
}

// declare records the rule as declared by the file. It says false, and gives CO-POL-003, when an earlier rule has the ID.
func (rs *ruleSet) declare(comp *compiler, path string, rule *rawRule, index int) bool {
	if prior, dup := rs.owner[rule.ID]; dup {
		*comp.diagnostics = append(*comp.diagnostics, diag.New(diag.POL003, diag.Params{idParam: rule.ID},
			diag.WithSource(diag.Source{URI: path, Pointer: "/rules/" + strconv.Itoa(index) + "/id"}),
			diag.WithSuggestion("first declared in "+prior)))

		return false
	}

	rs.owner[rule.ID] = path
	rs.ids = append(rs.ids, rule.ID)
	rs.declared = append(rs.declared, DeclaredRule{RuleID: rule.ID, Pack: comp.pack})

	return true
}

func filterRules(plans []RulePlan, all, wanted []string) ([]RulePlan, error) {
	if wanted == nil {
		return slices.Clone(plans), nil
	}

	if i := slices.IndexFunc(wanted, func(id string) bool { return !slices.Contains(all, id) }); i >= 0 {
		return nil, diag.Fail(diag.WRK010, diag.Detail("unknown rule '"+wanted[i]+"' for --rule"))
	}

	var kept []RulePlan

	for i := range plans {
		if slices.Contains(wanted, plans[i].RuleID) {
			kept = append(kept, plans[i])
		}
	}

	return kept, nil
}

// ThrowOnErrors gives the first error diagnostic as a *diag.Error (the exit
// code comes from the catalog; the other errors are its Cause). It gives nil
// when there is no error diagnostic. Other diagnostics are left to the caller.
func ThrowOnErrors(diagnostics []diag.Diagnostic) error {
	first := slices.IndexFunc(diagnostics, func(d diag.Diagnostic) bool { return d.Severity == diag.SeverityError })
	if first < 0 {
		return nil
	}

	head := diagnostics[first]

	if cause := errorsExcept(diagnostics, first); len(cause) > 0 {
		head.Cause = cause
	}

	code, ok := diag.ExitCodeOf(head.Code)
	if !ok {
		code = diag.InvalidInput
	}

	return &diag.Error{Diagnostic: head, ExitCode: code}
}

// errorsExcept gives the error diagnostics other than the one at index skip.
func errorsExcept(diagnostics []diag.Diagnostic, skip int) []diag.Diagnostic {
	var out []diag.Diagnostic

	for i := range diagnostics {
		if i != skip && diagnostics[i].Severity == diag.SeverityError {
			out = append(out, diagnostics[i])
		}
	}

	return out
}

// ReadPolicyFiles reads policy files. The paths are relative to root (or
// absolute OS paths). A missing file gives a CO-POL-001 *diag.Error.
func ReadPolicyFiles(root string, paths []string) ([]PolicyFile, error) {
	out := make([]PolicyFile, 0, len(paths))

	for _, p := range paths {
		file, err := readPolicyFile(root, p)
		if err != nil {
			return nil, err
		}

		out = append(out, file)
	}

	return out, nil
}

// readPolicyFile reads the policy file at path p, which is relative to root or absolute.
func readPolicyFile(root, p string) (PolicyFile, error) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, filepath.FromSlash(p))
	}

	text, err := os.ReadFile(abs) //nolint:gosec // the policy paths come from the workspace file.
	if err != nil {
		return PolicyFile{}, diag.Fail(diag.POL001, diag.Detail("cannot read policy file: "+err.Error()),
			diag.WithSource(diag.Source{URI: fsutil.ToPosix(p)}))
	}

	rel, relErr := fsutil.ToPosixRelative(root, abs)
	if relErr != nil {
		rel = fsutil.ToPosix(abs)
	}

	return PolicyFile{Path: rel, Text: string(text)}, nil
}
