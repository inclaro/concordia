package views

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// The manifest of a generated view (design D7).
const (
	// ManifestSchema is the schema tag of the manifest.
	ManifestSchema = "concordia-view-manifest/1"
	// GeneratorName is the generator identity that the manifest records.
	GeneratorName = "concordia"
	// DSLContract is the contract of the committed DSL that Concordia writes. A change of what the DSL generator
	// writes for the same inputs is a new contract.
	DSLContract = "concordia-dsl/1"
	// ConfinementScrubbedEnv records how the Structurizr process is confined: a scratch directory, a replaced
	// environment with no proxy variable and no inherited credential, and no hard network denial (design D9).
	ConfinementScrubbedEnv = "scrubbed-env"
	// FormatDSL is the format of the main output of a view.
	FormatDSL = "dsl"
	// CanonicalizationNone is the canonicalization of the DSL: it is deterministic and written as it is.
	CanonicalizationNone = "none"
)

// Manifest is the view manifest: plain evidence of how a generated view was made. It refers to the inputs and the
// artifacts only. It has no timestamp, no output and no digest depends on it, and the check never trusts it for
// equality: freshness recomputes every field except the provenance (design D7, D8).
type Manifest struct {
	// DirtyTree is the provenance of the working tree, `null` outside a Git repository.
	DirtyTree *ManifestDirtyTree `json:"dirtyTree"`
	Schema    string             `json:"schema"`
	View      ManifestView       `json:"view"`
	// Sources are the authored sources of the view. A generated view has none: its inputs are the projections.
	Sources        []ManifestSource  `json:"sources"`
	Outputs        []ManifestOutput  `json:"outputs"`
	Inputs         ManifestInputs    `json:"inputs"`
	SemanticDigest string            `json:"semanticDigest"`
	Generator      ManifestGenerator `json:"generator"`
	// Revisions are the revisions of the repositories that the view was made from. Provenance, not compared.
	Revisions []ManifestRevision `json:"revisions"`
}

// ManifestView identifies the view that the manifest belongs to.
type ManifestView struct {
	Environment       *string  `json:"environment"`
	ID                string   `json:"id"`
	DefinitionVersion string   `json:"definitionVersion"`
	DefinitionDigest  string   `json:"definitionDigest"`
	Kind              string   `json:"kind"`
	Scope             []string `json:"scope"`
}

// ManifestSource is an authored source file of the view with its digest.
type ManifestSource struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// ManifestOutput is one artifact: the DSL and each export, with the digest of the committed bytes.
type ManifestOutput struct {
	Path             string `json:"path"`
	Format           string `json:"format"`
	Digest           string `json:"digest"`
	Canonicalization string `json:"canonicalization"`
}

// ManifestInputs are the digests of the inputs of the view and the versions of the contracts. The digest of the
// observed projection is `null` (an explicit missing value) when the view compares only against the declared
// projection.
type ManifestInputs struct {
	ObservedProjection   *string `json:"observedProjection"`
	ModelProjection      string  `json:"modelProjection"`
	Bindings             string  `json:"bindings"`
	Controls             string  `json:"controls"`
	Policies             string  `json:"policies"`
	NormalizationVersion string  `json:"normalizationVersion"`
	DigestContract       string  `json:"digestContract"`
	ProjectionContract   string  `json:"projectionContract"`
}

// ManifestGenerator is the identity and the configuration of the generator.
type ManifestGenerator struct {
	// Structurizr is set when the view has an export: the CLI version and how the process was confined. `null`
	// for a view with no export.
	Structurizr *ManifestStructurizr `json:"structurizr"`
	Name        string               `json:"name"`
	Version     string               `json:"version"`
	DSLContract string               `json:"dslContract"`
}

// ManifestStructurizr is the configuration of the Structurizr CLI that made the exports.
type ManifestStructurizr struct {
	Version     string `json:"version"`
	Confinement string `json:"confinement"`
}

// ManifestRevision is the revision of one repository that the view was generated from.
type ManifestRevision struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
}

// ManifestDirtyTree is the dirty-tree provenance: the digest of the uncommitted files, without the files that
// Concordia writes itself (the view outputs and manifests among them).
type ManifestDirtyTree struct {
	Digest string `json:"digest"`
}

// Provenance is the part of the manifest that records where the view was generated. It is not compared by
// freshness: the commit that contains the manifest has another revision by construction.
type Provenance struct {
	DirtyTree *ManifestDirtyTree
	Revisions []ManifestRevision
}

// ManifestOptions are the settings of BuildManifest.
type ManifestOptions struct {
	// ToolVersion is the Concordia version (`generator.version`).
	ToolVersion string
	// StructurizrVersion is the CLI version that made the exports. It is required when there are exports.
	StructurizrVersion string
	// Exports are the regenerated exports of the view, in declaration order.
	Exports []ExportArtifact
	// Provenance is the revisions and the dirty tree. The zero value records none.
	Provenance Provenance
}

// BuildManifest builds the manifest of a generated view: the view, the digest of the DSL and of each export, the
// input digests, the semantic digest, and the generator. The result depends on the generated view and on the
// options only; it never reads the manifest file that it replaces.
func BuildManifest(generated *Generated, opts *ManifestOptions) *Manifest {
	view := generated.View

	var environment *string
	if view.Environment != "" {
		environment = &view.Environment
	}

	outputs := make([]ManifestOutput, 0, 1+len(opts.Exports))
	outputs = append(outputs, ManifestOutput{Path: view.Output.Path, Format: FormatDSL, Digest: generated.DSLDigest(), Canonicalization: CanonicalizationNone})

	for _, e := range opts.Exports {
		outputs = append(outputs, ManifestOutput{Path: e.Path, Format: e.Format, Digest: e.Digest, Canonicalization: e.Canonicalization})
	}

	var observed *string
	if generated.Digests.Observed != "" {
		observed = &generated.Digests.Observed
	}

	generator := ManifestGenerator{Name: GeneratorName, Version: opts.ToolVersion, DSLContract: DSLContract}
	if len(opts.Exports) > 0 {
		generator.Structurizr = &ManifestStructurizr{Version: opts.StructurizrVersion, Confinement: ConfinementScrubbedEnv}
	}

	return &Manifest{
		Schema: ManifestSchema,
		View: ManifestView{
			ID: view.ID, DefinitionVersion: DefinitionVersion, DefinitionDigest: generated.Digests.Definition, Kind: string(view.Kind),
			Scope: sortedCopy(view.Scope), Environment: environment,
		},
		Sources: []ManifestSource{},
		Outputs: outputs,
		Inputs: ManifestInputs{
			ModelProjection: generated.Digests.Model, ObservedProjection: observed, Bindings: generated.Digests.Binding,
			Controls: generated.Digests.Control, Policies: generated.Digests.Policy, NormalizationVersion: generated.Digests.Normalization, DigestContract: DigestContract,
			ProjectionContract: ProjectionContract,
		},
		SemanticDigest: generated.Digests.Semantic,
		Generator:      generator,
		Revisions:      nonNil(opts.Provenance.Revisions),
		DirtyTree:      opts.Provenance.DirtyTree,
	}
}

// Marshal gives the canonical JSON of the manifest: sorted keys, no timestamp.
func (m *Manifest) Marshal() ([]byte, error) {
	return canonjson.Marshal(m)
}

// ParseManifest reads a manifest. The manifest is untrusted input: an unknown key or a value of another type is
// an error, and nothing in it is used as truth.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("read the view manifest: %w", err)
	}

	return &m, nil
}

// Output gives the output of the manifest with the given path.
func (m *Manifest) Output(path string) (ManifestOutput, bool) {
	i := slices.IndexFunc(m.Outputs, func(o ManifestOutput) bool { return o.Path == path })
	if i < 0 {
		return ManifestOutput{}, false
	}

	return m.Outputs[i], true
}

// Names of the digests that DifferingDigests reports.
const (
	DigestDefinition    = "definition"
	DigestModel         = "modelProjection"
	DigestObserved      = "observedProjection"
	DigestBindings      = "bindings"
	DigestControls      = "controls"
	DigestPolicies      = "policies"
	DigestNormalization = "normalizationVersion"
	DigestSemantic      = "semantic"
)

// DifferingDigests gives the names of the input digests (and the semantic digest) of the manifest that differ
// from want, in a stable order. The contract versions are not here: see ContractChanged.
func (m *Manifest) DifferingDigests(want *Manifest) []string {
	var out []string

	add := func(name string, differs bool) {
		if differs {
			out = append(out, name)
		}
	}

	add(DigestDefinition, m.View.DefinitionDigest != want.View.DefinitionDigest)
	add(DigestModel, m.Inputs.ModelProjection != want.Inputs.ModelProjection)
	add(DigestObserved, !equalOptional(m.Inputs.ObservedProjection, want.Inputs.ObservedProjection))
	add(DigestBindings, m.Inputs.Bindings != want.Inputs.Bindings)
	add(DigestControls, m.Inputs.Controls != want.Inputs.Controls)
	add(DigestPolicies, m.Inputs.Policies != want.Inputs.Policies)
	add(DigestNormalization, m.Inputs.NormalizationVersion != want.Inputs.NormalizationVersion)
	add(DigestSemantic, m.SemanticDigest != want.SemanticDigest)

	return out
}

// ContractChanged says whether the manifest was made with other versions of the digest algorithm or of the
// projection contract than the ones of this Concordia (design D8; MIG-05).
func (m *Manifest) ContractChanged() bool {
	return m.Inputs.DigestContract != DigestContract || m.Inputs.ProjectionContract != ProjectionContract
}

func equalOptional(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return *a == *b
}

// GitFactory gives the Git port of a directory.
type GitFactory func(root string) workspace.GitPort

// CollectProvenance reads the provenance of a render: the revision of each project and the dirty-tree digest of
// the workspace. The dirty-tree digest leaves out the files that Concordia writes itself
// (conformance.GeneratedPathFilter: the run outputs, and the declared outputs, exports and manifests of the
// generated views), so writing the view files does not change it. A workspace outside a Git repository has no
// revision and no dirty-tree digest.
func CollectProvenance(ctx context.Context, wsp *workspace.Workspace, newGit GitFactory) (Provenance, error) {
	var out Provenance

	for i := range wsp.Observation.Projects {
		project := &wsp.Observation.Projects[i]

		revision, err := newGit(wsp.AbsProjectRoot(project)).Revision(ctx)
		if err != nil {
			return Provenance{}, fmt.Errorf("git revision of project %s: %w", project.Name, err)
		}

		if revision != "" {
			out.Revisions = append(out.Revisions, ManifestRevision{Repository: project.Name, Revision: revision})
		}
	}

	slices.SortFunc(out.Revisions, func(a, b ManifestRevision) int { return canonjson.CompareUTF16(a.Repository, b.Repository) })

	tree, err := DirtyTree(ctx, wsp, newGit(wsp.Root))
	if err != nil {
		return Provenance{}, err
	}

	out.DirtyTree = tree

	return out, nil
}

// DirtyTree gives the dirty-tree provenance of the workspace, without the files that Concordia writes itself. It
// gives nil outside a Git repository.
func DirtyTree(ctx context.Context, wsp *workspace.Workspace, git workspace.GitPort) (*ManifestDirtyTree, error) {
	revision, err := git.Revision(ctx)
	if err != nil {
		return nil, fmt.Errorf("git revision: %w", err)
	}

	if revision == "" {
		return nil, nil //nolint:nilnil // no repository: the dirty tree is an explicit missing value.
	}

	dirty, err := git.DirtyFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}

	dirty = slices.DeleteFunc(slices.Clone(dirty), conformance.GeneratedPathFilter(wsp))

	sum, err := workspace.DirtyTreeDigest(wsp.Root, dirty)
	if err != nil {
		return nil, fmt.Errorf("dirty-tree digest: %w", err)
	}

	return &ManifestDirtyTree{Digest: sum}, nil
}

// ArtifactDigest is the digest of the bytes of a committed artifact.
func ArtifactDigest(data []byte) string { return digest.OfBytes(data) }

// DiagramPaths gives the repository paths that the evidence nodes hold a diagram or a diagram element for. It is
// the CBM side of the presence cross-check (design D4): the input of PresenceInput.IndexedPaths.
func DiagramPaths(nodes map[string]observation.ObservedNode) []string {
	seen := map[string]bool{}

	for id := range nodes {
		n := nodes[id]
		if (n.Label == labelDiagram || n.Label == labelDiagramElement) && n.FilePath != "" {
			seen[n.FilePath] = true
		}
	}

	return sortedKeys(seen)
}
