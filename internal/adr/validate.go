package adr

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// lifecycleInputSuffix follows the path of the workspace file in the input path of the lifecycle profile: the
// profile is a part of the workspace file, recorded as `<workspace file>#adr.lifecycle`.
const lifecycleInputSuffix = "#adr.lifecycle"

// defaultWorkspaceFile names the workspace file when the workspace has no path (a workspace built in memory).
const defaultWorkspaceFile = "concordia.yaml"

// lifecycleInputPath gives the input path of the lifecycle profile: the workspace file path relative to the
// workspace root, as the run records it in `inputs.workspace.path`, followed by `#adr.lifecycle`.
func lifecycleInputPath(wspace *workspace.Workspace) string {
	if wspace.Path == "" {
		return defaultWorkspaceFile + lifecycleInputSuffix
	}

	rel, err := fsutil.ToPosixRelative(wspace.Root, wspace.Path)
	if err != nil {
		rel = filepath.Base(wspace.Path)
	}

	return rel + lifecycleInputSuffix
}

// Declarations are the validated ADR governance declarations of a workspace.
type Declarations struct {
	Register  *Register
	Lifecycle *Lifecycle
	// Subjects are the resolved subject refs of each entry, keyed by EntryKey.
	Subjects map[string][]string
	Triggers []workspace.ADRTrigger
	// Inputs are the material inputs: one per register file and one for the lifecycle profile, sorted by path.
	Inputs []conformance.InputRef
}

// ValidateTriggers checks the scope triggers: unique IDs, at least one of `paths` and `architecture` per trigger,
// and valid globs. A problem gives a CO-ADR-005 error with exit code 2.
func ValidateTriggers(triggers []workspace.ADRTrigger) error {
	seen := map[string]struct{}{}

	for i := range triggers {
		trig := &triggers[i]
		fail := func(detail string) error {
			return diag.Fail(diag.ADR005, diag.Detail(fmt.Sprintf("adr.triggers[%d] %q: %s", i, trig.ID, detail)))
		}

		if _, dup := seen[trig.ID]; dup {
			return fail("duplicate trigger ID")
		}

		seen[trig.ID] = struct{}{}

		if len(trig.Paths) == 0 && len(trig.Architecture) == 0 {
			return fail("needs paths or architecture kinds")
		}

		for _, glob := range trig.Paths {
			if !doublestar.ValidatePattern(glob) {
				return fail(fmt.Sprintf("invalid glob %q", glob))
			}
		}
	}

	return nil
}

// LoadDeclarations loads and validates the ADR declarations of the workspace: the lifecycle profile, the triggers,
// the register (schema, duplicates), the subjects of each entry against the model, and the rule links against the
// compiled packs. It reads files only; no Git, CBM, or network. It gives nil and no error when the workspace has no
// `adr` key or lists no register file.
func LoadDeclarations(wspace *workspace.Workspace, model *intent.ArchitectureModel, compiled *policy.CompileResult) (*Declarations, error) {
	if wspace.ADR == nil || len(wspace.ADR.Files) == 0 {
		return nil, nil //nolint:nilnil // no `adr` configuration is a valid state: nothing is declared.
	}

	lifecycle, err := NewLifecycle(wspace.ADR.Lifecycle.Active, wspace.ADR.Lifecycle.StatusMap)
	if err != nil {
		return nil, err
	}

	if err := ValidateTriggers(wspace.ADR.Triggers); err != nil {
		return nil, err
	}

	reg, err := LoadRegister(wspace.Root, wspace.ADR.Files)
	if err != nil {
		return nil, err
	}

	subjects := make(map[string][]string, len(reg.Entries))

	for i := range reg.Entries {
		refs, resolveErr := ResolveSubjects(model, &reg.Entries[i])
		if resolveErr != nil {
			return nil, resolveErr
		}

		subjects[EntryKey(&reg.Entries[i])] = refs
	}

	if err := ValidateLinks(reg, compiled); err != nil {
		return nil, err
	}

	profile, err := canonjson.Marshal(map[string]any{
		"active":    lifecycle.Active(),
		"statusMap": wspace.ADR.Lifecycle.StatusMap,
	})
	if err != nil {
		return nil, fmt.Errorf("adr: digest lifecycle profile: %w", err)
	}

	inputs := make([]conformance.InputRef, 0, len(reg.Files)+1)
	for _, f := range reg.Files {
		inputs = append(inputs, conformance.InputRef{Path: f.Path, Digest: f.Digest})
	}

	inputs = append(inputs, conformance.InputRef{Path: lifecycleInputPath(wspace), Digest: digest.OfBytes(profile)})
	slices.SortFunc(inputs, func(a, b conformance.InputRef) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	triggers := wspace.ADR.Triggers
	if triggers == nil {
		triggers = []workspace.ADRTrigger{}
	}

	return &Declarations{Register: reg, Lifecycle: lifecycle, Subjects: subjects, Triggers: triggers, Inputs: inputs}, nil
}
