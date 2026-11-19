package bundle

import (
	"fmt"
	"slices"
	"strings"
)

// Integrity level texts that `export verify` shows.
const (
	// LevelTextDigest is shown for a bundle that passes the digest check.
	LevelTextDigest = "content verified by digest"
	// LevelTextAuthenticated is shown when the signature of the bundle is also verified.
	LevelTextAuthenticated = "producer authenticated"
)

// Verify checks the archive at archivePath: the digest of each listed file, that no file is missing or
// extra, the integrity metadata and the manifest digest. A mismatch gives CO-REP-004 and names the file.
// The digest check is not authentication: the caller says "producer authenticated" only after it
// verified the signature.
func Verify(archivePath string) (*Manifest, error) {
	// An empty directory: check sizes and digests only, write nothing.
	manifest, text, err := open(archivePath, "")
	if err != nil {
		return nil, err
	}

	if manifest.Kind != Kind {
		return nil, invalid(fmt.Sprintf("manifest kind is '%s', want '%s'", manifest.Kind, Kind))
	}

	integrity := manifest.Integrity
	if integrity.Algorithm != AlgorithmSHA256 {
		return nil, invalid(fmt.Sprintf("integrity algorithm is '%s', want '%s'", integrity.Algorithm, AlgorithmSHA256))
	}

	for _, f := range manifest.Files {
		if got, ok := integrity.Files[f.Path]; !ok || got != f.SHA256 {
			return nil, invalid(fmt.Sprintf("file %s: integrity.files does not match the file list of the manifest", f.Path))
		}
	}

	if len(integrity.Files) != len(manifest.Files) {
		extra := []string{}

		for p := range integrity.Files {
			if !slices.ContainsFunc(manifest.Files, func(f ManifestFile) bool { return f.Path == p }) {
				extra = append(extra, p)
			}
		}

		slices.Sort(extra)

		return nil, invalid(fmt.Sprintf("integrity.files lists files that are not in the file list: %v", extra))
	}

	if err := checkViews(manifest); err != nil {
		return nil, err
	}

	want, err := manifestDigestOf(text)
	if err != nil {
		return nil, err
	}

	if integrity.ManifestDigest != want {
		return nil, invalid("manifest.json does not match integrity.manifestDigest")
	}

	return manifest, nil
}

// checkViews checks the view entries against the file list: a present file names a listed bundle file under
// views/ with the digest of the entry, an absent file names no content, and the state of a view is `absent`
// exactly when one of its files is.
func checkViews(manifest *Manifest) error {
	listed := make(map[string]string, len(manifest.Files))
	for _, file := range manifest.Files {
		listed[file.Path] = file.SHA256
	}

	seen := make(map[string]struct{}, len(manifest.Views))

	for i := range manifest.Views {
		view := &manifest.Views[i]
		if _, dup := seen[view.ID]; dup {
			return invalid(fmt.Sprintf("view %s is listed twice", view.ID))
		}

		seen[view.ID] = struct{}{}

		if err := checkView(view, listed); err != nil {
			return err
		}
	}

	return nil
}

// checkView checks one view entry; listed maps the bundle files to their digests.
func checkView(view *View, listed map[string]string) error {
	absent := false

	for _, file := range view.Files {
		switch file.State {
		case StatePresent:
			sum, ok := listed[file.File]
			if !ok || !strings.HasPrefix(file.File, ViewsDir) || sum != file.SHA256 {
				return invalid(fmt.Sprintf("view %s: %s file %s does not match the file list of the manifest", view.ID, file.Role, file.File))
			}
		case StateAbsent:
			if file.File != "" || file.SHA256 != "" {
				return invalid(fmt.Sprintf("view %s: absent %s file %s has content", view.ID, file.Role, file.Path))
			}

			absent = true
		default:
			return invalid(fmt.Sprintf("view %s: %s file %s has the unknown state '%s'", view.ID, file.Role, file.Path, file.State))
		}
	}

	want := StatePresent
	if absent {
		want = StateAbsent
	}

	if view.State != want {
		return invalid(fmt.Sprintf("view %s: state is '%s', want '%s'", view.ID, view.State, want))
	}

	return nil
}
