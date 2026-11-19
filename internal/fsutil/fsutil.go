// Package fsutil has file helpers. Paths inside the tool use "/" as separator;
// OS paths are converted with filepath.FromSlash at the point of use.
package fsutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	dirMode    = 0o755
	fileMode   = 0o644
	suffixSize = 4
)

// WriteFileAtomic writes a file atomically: temporary file in the same
// directory, fsync, rename. Missing parent directories are created. path is an OS path.
// An existing file keeps its permissions; a new file gets 0644.
func WriteFileAtomic(path string, data []byte) error {
	mode := os.FileMode(fileMode)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	return WriteFileAtomicMode(path, data, mode)
}

// WriteFileAtomicMode is WriteFileAtomic with explicit permissions for the
// file, for example 0755 for a script.
func WriteFileAtomicMode(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}

	var suffix [suffixSize]byte

	_, _ = rand.Read(suffix[:])
	tmp := fmt.Sprintf("%s.%d.%s.tmp", path, os.Getpid(), hex.EncodeToString(suffix[:]))

	if err := writeSynced(tmp, data, mode); err != nil {
		_ = os.Remove(tmp)

		return err
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}

	return nil
}

func writeSynced(path string, data []byte, mode os.FileMode) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // the caller chooses the path: writing it is the function.
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}

	defer func() {
		if cerr := file.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()

	if _, err = file.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	if err = file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}

	return nil
}

// ToPosixRelative gives path p relative to root, with "/" as the separator.
// Both are OS paths.
func ToPosixRelative(root, p string) (string, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return "", fmt.Errorf("relative path of %s to %s: %w", p, root, err)
	}

	return filepath.ToSlash(rel), nil
}

// ToPosix changes all path separators of p to "/", also the backslashes of Windows paths.
func ToPosix(p string) string {
	return strings.ReplaceAll(filepath.ToSlash(p), `\`, "/")
}
