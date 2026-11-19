package cbmpin

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// entryName gives the clean name of an archive entry. An entry that is an absolute path, has a drive letter, or
// has a `..` element is refused: a release archive has no such entry. Both `/` and `\` separate the elements.
func entryName(name string) (string, error) {
	unified := strings.ReplaceAll(name, "\\", "/")

	unsafe := unified == "" || strings.HasPrefix(unified, "/") || (len(unified) > 1 && unified[1] == ':')

	for part := range strings.SplitSeq(unified, "/") {
		if part == ".." {
			unsafe = true
		}
	}

	if unsafe {
		return "", fmt.Errorf("the archive has an entry with an unsafe path: %q", name)
	}

	return path.Clean(unified), nil
}

// archiveError gives the error of an archive reader. The readers of the standard library can refuse an
// entry with an insecure path themselves (GODEBUG); that is the same refusal as entryName.
func archiveError(err, insecure error) error {
	if errors.Is(err, insecure) {
		return fmt.Errorf("the archive has an entry with an unsafe path: %w", err)
	}

	return fmt.Errorf("the archive is not valid: %w", err)
}

// copyFromTarGz writes the content of the entry `want` of a .tar.gz archive to dst. The archive must hold the
// entry once, as a regular file. The names of all entries are checked.
func copyFromTarGz(archive *os.File, want string, dst io.Writer) error {
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("cannot read the archive: %w", err)
	}

	gz, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("the archive is not valid: %w", err)
	}

	defer func() { _ = gz.Close() }()

	reader := tar.NewReader(gz)
	found := false

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return archiveError(err, tar.ErrInsecurePath)
		}

		name, err := entryName(header.Name)
		if err != nil {
			return err
		}

		if name != want {
			continue
		}

		if found || header.Typeflag != tar.TypeReg {
			return fmt.Errorf("the entry %s is not one regular file", want)
		}

		found = true

		if err := copyLimited(dst, reader, maxBinaryBytes); err != nil {
			return fmt.Errorf("cannot extract %s: %w", want, err)
		}
	}

	if !found {
		return fmt.Errorf("the archive has no entry %s", want)
	}

	return nil
}

// copyFromZip writes the content of the entry `want` of a .zip archive to dst. The rules are those of copyFromTarGz.
func copyFromZip(archive *os.File, want string, dst io.Writer) error {
	info, err := archive.Stat()
	if err != nil {
		return fmt.Errorf("cannot read the archive: %w", err)
	}

	reader, err := zip.NewReader(archive, info.Size())
	if err != nil {
		return archiveError(err, zip.ErrInsecurePath)
	}

	found := false

	for _, file := range reader.File {
		name, err := entryName(file.Name)
		if err != nil {
			return err
		}

		if name != want {
			continue
		}

		if found || !file.Mode().IsRegular() {
			return fmt.Errorf("the entry %s is not one regular file", want)
		}

		found = true

		if err := copyZipEntry(file, dst); err != nil {
			return fmt.Errorf("cannot extract %s: %w", want, err)
		}
	}

	if !found {
		return fmt.Errorf("the archive has no entry %s", want)
	}

	return nil
}

// copyZipEntry writes the content of one entry of a zip archive to dst.
func copyZipEntry(file *zip.File, dst io.Writer) error {
	content, err := file.Open()
	if err != nil {
		return err //nolint:wrapcheck // the caller adds the name of the entry.
	}

	defer func() { _ = content.Close() }()

	return copyLimited(dst, content, maxBinaryBytes)
}
