package binding

import (
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/trust"
	"github.com/inclaro/concordia/internal/workspace"
)

const calmRefPrefix = "calm:node:"

// Loaded is the result of Load.
type Loaded struct {
	Bindings    []Binding
	Diagnostics []diag.Diagnostic
	// Files are the workspace-relative paths of the binding files that were read (sorted).
	Files []string
}

type rawBinding struct {
	Exclusive       *bool       `json:"exclusive"`
	Required        *bool       `json:"required"`
	Projection      *Projection `json:"projection"`
	Trust           *rawTrust   `json:"trust"`
	ID              string      `json:"id"`
	ArchitectureRef string      `json:"architectureRef"`
	Cardinality     string      `json:"cardinality"`
	Agreement       string      `json:"agreement"`
	Status          string      `json:"status"`
	Authority       string      `json:"authority"`
	Match           struct {
		Any []map[string]string `json:"any"`
	} `json:"match"`
}

// rawTrust is the `trust` object of a binding entry.
type rawTrust struct {
	Dimensions     map[string]string `json:"dimensions"`
	Classification string            `json:"classification"`
}

type rawBindingSet struct {
	Bindings []rawBinding `json:"bindings"`
}

// LoadOption configures Load.
type LoadOption func(*loadConfig)

type loadConfig struct {
	// environments are the declared environment ids; nil until set.
	environments map[string]struct{}
	// vocabulary is the trust vocabulary; nil until set (then the workspace `trust` block).
	vocabulary *trust.Vocabulary
}

// WithTrust overrides the trust vocabulary that Load validates the `trust` of a
// binding entry against. Without it Load uses the workspace `trust` block.
func WithTrust(vocabulary trust.Vocabulary) LoadOption {
	return func(c *loadConfig) { c.vocabulary = &vocabulary }
}

// WithEnvironments overrides the environment ids that Load accepts in a match.
// Without it Load uses the workspace `environments`. A match that names any
// other environment is rejected (CO-BND-002).
func WithEnvironments(ids []string) LoadOption {
	return func(c *loadConfig) {
		c.environments = make(map[string]struct{}, len(ids))
		for _, id := range ids {
			c.environments[id] = struct{}{}
		}
	}
}

// scope is what a match is validated against.
type scope struct {
	projects     map[string]struct{}
	environments map[string]struct{}
	vocabulary   trust.Vocabulary
}

// newScope gives the scope of a workspace, with the overrides of the options.
func newScope(wspace *workspace.Workspace, opts []LoadOption) scope {
	var cfg loadConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	projects := make(map[string]struct{}, len(wspace.Observation.Projects))
	for _, p := range wspace.Observation.Projects {
		projects[p.Name] = struct{}{}
	}

	if cfg.environments == nil {
		cfg.environments = make(map[string]struct{}, len(wspace.Environments))
		for i := range wspace.Environments {
			cfg.environments[wspace.Environments[i].ID] = struct{}{}
		}
	}

	vocabulary := trust.FromWorkspace(wspace)
	if cfg.vocabulary != nil {
		vocabulary = *cfg.vocabulary
	}

	return scope{projects: projects, environments: cfg.environments, vocabulary: vocabulary}
}

// Load loads the BindingSet files (`bindings.files` globs) and the CALM
// `concordia.bindings` items. Problems are diagnostics (CO-BND-001, -002, -006,
// -007, and CO-SEC-001 for a `trust` value outside the workspace vocabulary);
// a binding with a bad match is dropped. An error is a failure to read the
// workspace.
func Load(wspace *workspace.Workspace, model *intent.ArchitectureModel, opts ...LoadOption) (Loaded, error) {
	files, err := ExpandFileGlobs(wspace.Root, wspace.Bindings.Files)
	if err != nil {
		return Loaded{}, err
	}

	nodeIDs := make(map[string]struct{}, len(model.Nodes))
	for i := range model.Nodes {
		nodeIDs[model.Nodes[i].ID] = struct{}{}
	}

	scp := newScope(wspace, opts)
	loaded := Loaded{Files: files}

	bindings := fromFiles(wspace.Root, files, nodeIDs, &scp, &loaded.Diagnostics)
	bindings = append(bindings, fromModel(model, &scp, &loaded.Diagnostics)...)
	loaded.Diagnostics = append(loaded.Diagnostics, duplicateIDProblems(bindings)...)

	slices.SortStableFunc(bindings, func(a, b Binding) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	if bindings == nil {
		bindings = []Binding{}
	}

	loaded.Bindings = bindings

	return loaded, nil
}

// fromFiles makes the bindings of the BindingSet files, in file order. Their problems are added to diagnostics.
func fromFiles(root string, files []string, nodeIDs map[string]struct{}, scp *scope, diagnostics *[]diag.Diagnostic) []Binding {
	var bindings []Binding

	for _, file := range files {
		set, problems := readSet(root, file)
		*diagnostics = append(*diagnostics, problems...)

		for i := range set {
			site := matchSite{source: file, pointer: "/bindings/" + strconv.Itoa(i), scp: scp, diagnostics: diagnostics}
			bindings = append(bindings, fromRaw(&set[i], nodeIDs, site))
		}
	}

	return bindings
}

// duplicateIDProblems gives a CO-BND-006 diagnostic for each binding whose ID an earlier binding has.
func duplicateIDProblems(bindings []Binding) []diag.Diagnostic {
	var out []diag.Diagnostic

	seen := map[string]struct{}{}

	for i := range bindings {
		if _, dup := seen[bindings[i].ID]; dup {
			out = append(out, diag.New(diag.BND006, diag.Params{paramID: bindings[i].ID},
				diag.WithSource(diag.Source{URI: bindings[i].Source})))
		}

		seen[bindings[i].ID] = struct{}{}
	}

	return out
}

// fromRaw makes the binding of one entry of a BindingSet file; the site is the entry.
func fromRaw(raw *rawBinding, nodeIDs map[string]struct{}, site matchSite) Binding {
	ref := strings.TrimPrefix(raw.ArchitectureRef, calmRefPrefix)

	if _, ok := nodeIDs[ref]; !ok {
		site.report(diag.BND001, diag.Params{paramID: raw.ArchitectureRef}, site.withSource())
	}

	matches := []Match{}

	for j, item := range raw.Match.Any {
		if m, ok := itemMatch(item, site.at("/match/any/"+strconv.Itoa(j))); ok {
			matches = append(matches, m)
		}
	}

	return Binding{
		ID:              raw.ID,
		ArchitectureRef: ref,
		Exclusive:       raw.Exclusive == nil || *raw.Exclusive,
		Required:        raw.Required == nil || *raw.Required,
		Cardinality:     orDefault(raw.Cardinality, CardinalityMany),
		Agreement:       orDefault(raw.Agreement, AgreementAll),
		Status:          orDefault(raw.Status, StatusActive),
		Authority:       raw.Authority,
		Match:           matches,
		Projection:      raw.Projection,
		Trust:           trustOf(raw.Trust, site.at("/trust")),
		Source:          site.source,
	}
}

// trustOf validates the `trust` object of a binding entry against the workspace
// vocabulary (CO-SEC-001) and gives its valid part, or nil when nothing is left.
func trustOf(raw *rawTrust, site matchSite) *trust.Declaration {
	if raw == nil {
		return nil
	}

	decl, problems := site.scp.vocabulary.Valid(trust.Declaration{Classification: raw.Classification, Dimensions: raw.Dimensions})
	for _, p := range problems {
		site.report(diag.SEC001, diag.Detail(p.Detail()),
			diag.WithSource(diag.Source{URI: site.source, Pointer: p.Pointer(site.pointer)}))
	}

	if decl.IsZero() {
		return nil
	}

	return &decl
}

func orDefault(value, def string) string {
	if value == "" {
		return def
	}

	return value
}
