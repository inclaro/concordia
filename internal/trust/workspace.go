package trust

import "github.com/inclaro/concordia/internal/workspace"

// FromWorkspace gives the vocabulary of a workspace: the built-in dimensions, the
// `trust.dimensions` that it declares, and its `trust.classifications`. A nil
// workspace, or one without a `trust` block, gives the zero-configuration
// vocabulary (built-in dimensions, no classification).
func FromWorkspace(w *workspace.Workspace) Vocabulary {
	if w == nil || w.Trust == nil {
		return NewVocabulary(nil, nil)
	}

	return NewVocabulary(w.Trust.Dimensions, w.Trust.Classifications)
}
