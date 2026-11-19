package fsutil

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot is the error that Confine wraps when a path leaves its root: by an absolute
// path, by "..", or by a symbolic link. A missing file gives the error of the operating system
// (errors.Is(err, fs.ErrNotExist)), not this one.
var ErrOutsideRoot = errors.New("path leaves the root")

// Confine resolves the "/"-separated path rel below root and gives the absolute OS path of the
// file after symbolic links are resolved. It refuses an empty path, a path with a NUL byte, an
// absolute path (also a Windows drive or a leading backslash), a ".." element, and a path whose
// resolved target is not below the resolved root. Each refusal wraps ErrOutsideRoot. The file
// must exist: a missing file gives the operating system's not-exist error.
//
// The result is for reading right away. Confine cannot stop a race in which another process
// swaps a link between this check and the read.
func Confine(root, rel string) (string, error) {
	if rel == "" || strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("%w: %q is not a file path", ErrOutsideRoot, rel)
	}

	unified := strings.ReplaceAll(rel, `\`, "/")

	if isAbsoluteRel(rel, unified) {
		return "", fmt.Errorf("%w: %q is an absolute path", ErrOutsideRoot, rel)
	}

	if hasDotDotElement(unified) {
		return "", fmt.Errorf("%w: %q has a '..' element", ErrOutsideRoot, rel)
	}

	realRoot, err := resolveRoot(root)
	if err != nil {
		return "", err
	}

	target, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(unified)))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", rel, err)
	}

	if !isBelow(realRoot, target) {
		return "", fmt.Errorf("%w: %q resolves outside of the root", ErrOutsideRoot, rel)
	}

	return target, nil
}

// isAbsoluteRel reports whether rel names an absolute path on any platform. The argument unified
// is rel with backslashes turned into slashes.
func isAbsoluteRel(rel, unified string) bool {
	return strings.HasPrefix(unified, "/") || filepath.IsAbs(filepath.FromSlash(rel)) || filepath.VolumeName(rel) != ""
}

// hasDotDotElement reports whether the "/"-separated path has a ".." element.
func hasDotDotElement(unified string) bool {
	for part := range strings.SplitSeq(unified, "/") {
		if part == ".." {
			return true
		}
	}

	return false
}

// resolveRoot gives the absolute path of root after symbolic links are resolved.
func resolveRoot(root string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root %s: %w", root, err)
	}

	realRoot, err = filepath.Abs(realRoot)
	if err != nil {
		return "", fmt.Errorf("resolve root %s: %w", root, err)
	}

	return realRoot, nil
}

// isBelow reports whether target is the directory realRoot or a path below it.
func isBelow(realRoot, target string) bool {
	inside, err := filepath.Rel(realRoot, target)
	if err != nil {
		return false
	}

	escapes := inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator))

	return !escapes && !filepath.IsAbs(inside)
}
