package assessment

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
)

// Document layout under an assessment path (design D2). The loader reads every `*.yaml` and `*.yml` file under
// the configured paths and tells the kind from the `kind` key, so the layout is the convention that the
// `assessment approve` and `assessment revoke` commands and the authority `paths` patterns rely on.
const (
	// DefaultPath is the default assessment directory (key `assessments.paths`).
	DefaultPath = "assessments"
	// AssumptionsDir, ApprovalsDir, and RevocationsDir are the subdirectories of the record kinds.
	AssumptionsDir = "assumptions"
	ApprovalsDir   = "approvals"
	RevocationsDir = "revocations"
)

// AssessmentPath gives `<base>/<id>/v<version>.yaml`, the file of an assessment version.
func AssessmentPath(base, id string, version int) string {
	return path.Join(base, id, fmt.Sprintf("v%d.yaml", version))
}

// AssumptionPath gives `<base>/assumptions/<id>.yaml`.
func AssumptionPath(base, id string) string { return path.Join(base, AssumptionsDir, id+".yaml") }

// ApprovalPath gives `<base>/approvals/<assessment>-v<version>-<seq>.yaml`.
func ApprovalPath(base, assessmentID string, version, seq int) string {
	return path.Join(base, ApprovalsDir, fmt.Sprintf("%s-v%d-%d.yaml", assessmentID, version, seq))
}

// RevocationPath gives `<base>/revocations/<approval id>.yaml`.
func RevocationPath(base, approvalID string) string {
	return path.Join(base, RevocationsDir, approvalID+".yaml")
}

// globMeta are the characters that make a path item a glob.
const globMeta = "*?[{"

// Load reads the assessment documents under the paths (workspace-relative "/" paths: a directory is read
// recursively, a file is read as is, and an item with glob characters is a glob). It validates every file
// against the closed schema of its kind and checks references and immutability. It returns the first problem as
// a *diag.Error with exit code 2 (CO-ASM-001, CO-ASM-002, CO-ASM-003); use LoadAll to get every problem.
func Load(root string, paths []string) (*Set, error) {
	set, errs := LoadAll(root, paths)
	if len(errs) > 0 {
		return nil, errs[0]
	}

	return set, nil
}

// LoadAll is Load that reports every problem it finds, in a fixed order: file problems in path order, then the
// cross-document problems. The Set holds the documents that are valid; it is never nil.
//
// Checks across documents: a duplicate assessment id and version, assumption id, approval id, or revocation id
// (CO-ASM-001); an assessment that lists an unknown assumption, an approval that binds an unknown assessment
// version, a revocation that names an unknown approval (CO-ASM-001, skipped when a file failed to load, because
// the target may be that file); an approved version whose file digest differs from the digest in a decision
// record (CO-ASM-002, see CheckApprovedVersionEdited).
func LoadAll(root string, paths []string) (*Set, []*diag.Error) {
	set := &Set{
		Assessments: []*Assessment{}, Assumptions: []*Assumption{}, Approvals: []*Approval{},
		Revocations: []*Revocation{}, Files: []File{},
	}

	files, err := expandPaths(root, paths)
	if err != nil {
		return set, []*diag.Error{err}
	}

	var errs []*diag.Error

	for _, file := range files {
		if loadErr := loadFile(root, file, set); loadErr != nil {
			errs = append(errs, loadErr)
		}
	}

	fileFailed := len(errs) > 0

	errs = append(errs, checkDuplicates(set)...)

	if !fileFailed {
		errs = append(errs, checkReferences(set)...)
	}

	errs = append(errs, editedErrors(set, set.Approvals)...)

	sortSet(set)

	return set, errs
}

// expandPaths gives the sorted unique files that the path items name.
func expandPaths(root string, items []string) ([]string, *diag.Error) {
	set := map[string]struct{}{}

	for _, item := range items {
		item = strings.TrimSuffix(strings.TrimPrefix(item, "./"), "/")

		matched, err := expandItem(root, item)
		if err != nil {
			return nil, err
		}

		for _, m := range matched {
			set[m] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out, nil
}

// expandItem gives the files of one path item: a glob, a directory (read recursively), or a file.
func expandItem(root, item string) ([]string, *diag.Error) {
	notValid := func(detail string) *diag.Error {
		return diag.Fail(diag.ASM001, diag.Detail(detail), diag.WithSource(diag.Source{URI: item}))
	}

	if strings.ContainsAny(item, globMeta) {
		matched, err := binding.ExpandFileGlobs(root, []string{item})
		if err != nil {
			return nil, notValid(err.Error())
		}

		return slices.DeleteFunc(matched, func(m string) bool { return !isYAML(m) }), nil
	}

	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(item)))

	switch {
	case err != nil:
		return nil, notValid("assessment path not found: " + item)
	case info.IsDir():
		matched, mErr := binding.ExpandFileGlobs(root, []string{item + "/**/*.yaml", item + "/**/*.yml"})
		if mErr != nil {
			return nil, notValid(mErr.Error())
		}

		return matched, nil
	case info.Mode().IsRegular():
		return []string{item}, nil
	default:
		return nil, notValid("assessment path is not a file or directory: " + item)
	}
}

func isYAML(name string) bool {
	ext := path.Ext(name)

	return ext == ".yaml" || ext == ".yml"
}

// loadFile reads one file into the set.
func loadFile(root, file string, set *Set) *diag.Error {
	text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file))) //nolint:gosec // the path matched assessments.paths.
	if err != nil {
		return invalid(file, "cannot read: "+err.Error(), "", nil)
	}

	src, readErr := read(file, text)
	if readErr != nil {
		return failureOf(readErr)
	}

	kind := src.kind()

	if buildErr := addDocument(set, kind, src); buildErr != nil {
		return failureOf(buildErr)
	}

	set.Files = append(set.Files, File{Path: file, Kind: kind, Digest: digest.OfBytes(text)})

	return nil
}

// addDocument builds the document of the given kind and adds it to its list in the set.
func addDocument(set *Set, kind string, src *source) error {
	switch kind {
	case KindAssessment:
		return appendBuilt(&set.Assessments, src, buildAssessment)
	case KindAssumption:
		return appendBuilt(&set.Assumptions, src, buildAssumption)
	case KindApproval:
		return appendBuilt(&set.Approvals, src, buildApproval)
	case KindRevocation:
		return appendBuilt(&set.Revocations, src, buildRevocation)
	default:
		return src.validate("") // names the missing or unknown kind.
	}
}

// appendBuilt builds one document from the source and appends it to the list.
func appendBuilt[T any](list *[]*T, src *source, build func(*source) (*T, error)) error {
	doc, err := build(src)
	if err != nil {
		return err
	}

	*list = append(*list, doc)

	return nil
}

// failureOf gives the *diag.Error of an error that the build functions return.
func failureOf(err error) *diag.Error {
	if d, ok := err.(*diag.Error); ok { //nolint:errorlint // the build functions return a *diag.Error itself.
		return d
	}

	return diag.Fail(diag.ASM001, diag.Detail(err.Error()))
}

// at gives the diagnostic source of a loaded document.
func at(loc Location) diag.Option {
	return diag.WithSource(diag.Source{URI: loc.File, Line: loc.Line})
}

// checkDuplicates reports the second and later declaration of the same id (and version), naming both locations.
func checkDuplicates(set *Set) []*diag.Error {
	ordered := slices.Clone(set.Assessments)
	slices.SortStableFunc(ordered, func(a, b *Assessment) int { return compareLoc(a.Location, b.Location) })

	errs := duplicatesOf("assessment", ordered, (*Assessment).Key, func(a *Assessment) Location { return a.Location })
	errs = append(errs, duplicatesOf("assumption", sortedBy(set.Assumptions, assumptionLoc), assumptionID, assumptionLoc)...)
	errs = append(errs, duplicatesOf("approval", sortedBy(set.Approvals, approvalLoc), approvalID, approvalLoc)...)

	return append(errs, duplicatesOf("revocation", sortedBy(set.Revocations, revocationLoc), revocationID, revocationLoc)...)
}

func assumptionLoc(a *Assumption) Location { return a.Location }
func assumptionID(a *Assumption) string    { return a.ID }
func approvalLoc(a *Approval) Location     { return a.Location }
func approvalID(a *Approval) string        { return a.ID }
func revocationLoc(r *Revocation) Location { return r.Location }
func revocationID(r *Revocation) string    { return r.ID }

// duplicatesOf reports the second and later declaration of a name in a list that is already in location order.
func duplicatesOf[T any](kind string, ordered []T, name func(T) string, loc func(T) Location) []*diag.Error {
	var errs []*diag.Error

	seen := map[string]T{}

	for _, doc := range ordered {
		key := name(doc)

		prev, ok := seen[key]
		if !ok {
			seen[key] = doc

			continue
		}

		first, second := loc(prev), loc(doc)
		errs = append(errs, diag.Fail(diag.ASM001, diag.Detail(fmt.Sprintf("%s %s is declared twice (%s:%d and %s:%d)",
			kind, key, first.File, first.Line, second.File, second.Line)), at(second)))
	}

	return errs
}

func sortedBy[T any](list []T, loc func(T) Location) []T {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b T) int { return compareLoc(loc(a), loc(b)) })

	return out
}

func compareLoc(a, b Location) int {
	if c := canonjson.CompareUTF16(a.File, b.File); c != 0 {
		return c
	}

	return a.Line - b.Line
}

// checkReferences reports a reference that no loaded document satisfies.
func checkReferences(set *Set) []*diag.Error {
	var errs []*diag.Error

	for _, a := range sortedBy(set.Assessments, func(a *Assessment) Location { return a.Location }) {
		for _, id := range a.Assumptions {
			if set.Assumption(id) == nil {
				errs = append(errs, diag.Fail(diag.ASM001, diag.Detail(fmt.Sprintf(
					"assessment %s lists the assumption %q, which no document declares", a.Key(), id)), at(a.Location)))
			}
		}
	}

	for _, a := range sortedBy(set.Approvals, func(a *Approval) Location { return a.Location }) {
		if set.Assessment(a.Assessment.ID, a.Assessment.Version) == nil {
			errs = append(errs, diag.Fail(diag.ASM001, diag.Detail(fmt.Sprintf(
				"approval %s binds the assessment version %s, which no document declares",
				a.ID, VersionKey(a.Assessment.ID, a.Assessment.Version))), at(a.Location)))
		}
	}

	for _, r := range sortedBy(set.Revocations, func(r *Revocation) Location { return r.Location }) {
		if set.Approval(r.Approval.ID) == nil {
			errs = append(errs, diag.Fail(diag.ASM001, diag.Detail(fmt.Sprintf(
				"revocation %s names the approval %q, which no document declares", r.ID, r.Approval.ID)), at(r.Location)))
		}
	}

	return errs
}

// CheckApprovedVersionEdited fails with CO-ASM-002 when a decision record binds an assessment version by digest
// and the loaded file of that version has another digest. The error names the version, the approval, and both
// digests. The approvals may come from the loaded set or from another source (for example the trust anchor);
// a record for a version that the set does not hold is not checked here. Only the first mismatch in approval
// order is returned.
func CheckApprovedVersionEdited(set *Set, approvals []*Approval) *diag.Error {
	if errs := editedErrors(set, approvals); len(errs) > 0 {
		return errs[0]
	}

	return nil
}

// editedErrors gives one CO-ASM-002 error for each edited assessment version, in approval order.
func editedErrors(set *Set, approvals []*Approval) []*diag.Error {
	var errs []*diag.Error

	reported := map[string]bool{}

	for _, rec := range approvals {
		a := editedVersion(set, rec)
		if a == nil || reported[a.Key()] {
			continue
		}

		reported[a.Key()] = true

		errs = append(errs, EditedError(a, rec))
	}

	return errs
}

// editedVersion gives the loaded assessment version that the record binds when its digest differs, else nil.
func editedVersion(set *Set, rec *Approval) *Assessment {
	a := set.Assessment(rec.Assessment.ID, rec.Assessment.Version)
	if a == nil || a.Digest == rec.Assessment.Digest {
		return nil
	}

	return a
}

// EditedError makes the CO-ASM-002 error for an assessment version whose digest differs from the digest that the
// decision record bound.
func EditedError(a *Assessment, rec *Approval) *diag.Error {
	return diag.Fail(diag.ASM002, diag.Params{"detail": fmt.Sprintf(
		"assessment %s was edited after the %s %s: the record binds the digest %s and the file has the digest %s; "+
			"save the change as the next version and do not edit an approved version",
		a.Key(), rec.Decision, rec.ID, rec.Assessment.Digest, a.Digest)}, at(a.Location))
}

// sortSet orders every list of the set.
func sortSet(set *Set) {
	slices.SortStableFunc(set.Assessments, func(a, b *Assessment) int {
		if c := canonjson.CompareUTF16(a.ID, b.ID); c != 0 {
			return c
		}

		return a.Version - b.Version
	})
	slices.SortStableFunc(set.Assumptions, func(a, b *Assumption) int { return canonjson.CompareUTF16(a.ID, b.ID) })
	slices.SortStableFunc(set.Approvals, func(a, b *Approval) int { return canonjson.CompareUTF16(a.ID, b.ID) })
	slices.SortStableFunc(set.Revocations, func(a, b *Revocation) int { return canonjson.CompareUTF16(a.ID, b.ID) })
	slices.SortStableFunc(set.Files, func(a, b File) int { return canonjson.CompareUTF16(a.Path, b.Path) })
}
