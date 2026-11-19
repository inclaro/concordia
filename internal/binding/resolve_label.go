package binding

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
)

var (
	labelSegment = regexp.MustCompile(`^[A-Za-z0-9_.+-]+$`)
	// labelProject is the first segment of a label for a project other than the primary one:
	// the paths of its files have the prefix `@<name>/`.
	labelProject = regexp.MustCompile(`^@[A-Za-z0-9_.-]+$`)
	labelName    = regexp.MustCompile(`^[A-Za-z0-9_.+=,@~*-]+$`)
)

// BuildFiles gives the build files (BUILD, BUILD.bazel, BUCK) of files, sorted.
func BuildFiles(files []string) []string {
	var out []string

	for _, f := range files {
		switch path.Base(f) {
		case "BUILD", "BUILD.bazel", "BUCK":
			out = append(out, f)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// parseLabel parses //..., //a/b, //a/b:name and //a/b/... labels. For a project other than the
// primary one, the package starts with `@<name>`, as in //@pay/a/b.
func parseLabel(value string) (pkg string, recursive, ok bool) {
	rest, found := strings.CutPrefix(value, "//")
	if !found {
		return "", false, false
	}

	if rest == "..." {
		return "", true, true
	}

	rest, recursive, ok = splitLabelTarget(rest)
	if !ok {
		return "", false, false
	}

	if rest == "" || !validLabelSegments(rest) {
		return "", false, false
	}

	return rest, recursive, true
}

// splitLabelTarget removes the `:name` or the `/...` that ends a label. The
// result is the package, whether the label is recursive, and false for a bad name.
func splitLabelTarget(rest string) (pkg string, recursive, ok bool) {
	if p, name, hasName := strings.Cut(rest, ":"); hasName {
		return p, false, labelName.MatchString(name)
	}

	pkg, recursive = strings.CutSuffix(rest, "/...")

	return pkg, recursive, true
}

// validLabelSegments says whether each segment of a label package is valid. The
// first one may name a project (`@<name>`).
func validLabelSegments(pkg string) bool {
	for i, seg := range strings.Split(pkg, "/") {
		if i == 0 && labelProject.MatchString(seg) {
			continue
		}

		if !validLabelSegment(seg) {
			return false
		}
	}

	return true
}

// validLabelSegment says whether seg is a name, not `.` or `..`.
func validLabelSegment(seg string) bool {
	return !slices.Contains([]string{".", ".."}, seg) && labelSegment.MatchString(seg)
}

// buildOwner gives the deepest build package directory that contains file.
func buildOwner(pkgs map[string]struct{}, file string) (string, bool) {
	for dir := path.Dir(file); ; dir = path.Dir(dir) {
		d := dir
		if d == "." {
			d = ""
		}

		if _, ok := pkgs[d]; ok {
			return d, true
		}

		if d == "" {
			return "", false
		}
	}
}

// inBuildPackage says whether the build package owner is the package pkg of a
// build target label, or (for a recursive label) below it.
func inBuildPackage(owner, pkg string, recursive bool) bool {
	if !recursive {
		return owner == pkg
	}

	return pkg == "" || owner == pkg || strings.HasPrefix(owner, pkg+"/")
}
