package cbmpin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fetch"
)

// DownloadURLEnv is the environment variable that replaces the https directory that holds the release archives
// (for a mirror, or for a test).
const DownloadURLEnv = "CONCORDIA_CBM_DOWNLOAD_URL"

const (
	// maxArchiveBytes is the largest archive that Install downloads. The pinned archives are about 50 MB.
	maxArchiveBytes = 512 << 20
	// maxBinaryBytes is the largest binary that Install extracts. The pinned binaries are about 330 MB.
	maxBinaryBytes = 1 << 30
	// downloadTimeout limits the whole download, because the archives are large.
	downloadTimeout = 10 * time.Minute
	binaryMode      = 0o755
	dirMode         = 0o755
	mebibyte        = 1 << 20
)

// Request is one installation of the pinned build.
type Request struct {
	// Client makes the download. nil gives a client that follows only https redirects and has no
	// time limit of its own (Install limits the download with the context).
	Client *http.Client
	// URL is the https URL of the archive.
	URL string
	// Dir is the install directory. Install creates it when it is missing.
	Dir string
	// Asset is the pinned archive and binary.
	Asset Asset
}

// ArchiveURL gives the URL of the archive of an asset. The base is the https directory that holds the archives;
// an empty base is the GitHub release of the pin.
func (p *Pin) ArchiveURL(base string, asset *Asset) string {
	if base == "" {
		base = p.DefaultBaseURL()
	}

	return strings.TrimRight(base, "/") + "/" + asset.Archive
}

// Install downloads the archive, checks its digest, extracts the binary, checks the digest of the binary, and
// puts it in the install directory with mode 0755. The file appears at once or not at all: Install writes a
// temporary file in the same directory and renames it. It gives the path of the binary. Every failure is
// CO-OBS-006, and nothing is left in the directory.
func Install(ctx context.Context, req *Request) (string, error) {
	path, err := install(ctx, req)
	if err != nil {
		return "", diag.Fail(diag.OBS006, diag.Detail(err.Error()))
	}

	return path, nil
}

func install(ctx context.Context, req *Request) (string, error) {
	if !strings.HasPrefix(req.URL, "https://") {
		return "", errors.New("only https URLs are used")
	}

	if err := os.MkdirAll(req.Dir, dirMode); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", req.Dir, err)
	}

	archive, err := os.CreateTemp("", "concordia-cbm-archive-*")
	if err != nil {
		return "", fmt.Errorf("cannot create a temporary file: %w", err)
	}

	defer func() {
		_ = archive.Close()
		_ = os.Remove(archive.Name())
	}()

	if err := download(ctx, req, archive); err != nil {
		return "", err
	}

	staged, err := stage(archive, &req.Asset, req.Dir)
	if err != nil {
		return "", err
	}

	defer func() { _ = os.Remove(staged) }()

	target := filepath.Join(req.Dir, req.Asset.Binary)
	if err := os.Rename(staged, target); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", target, err)
	}

	return target, nil
}

// newClient gives the client for a download without a client: it follows only https redirects, and the
// context limits the time.
func newClient() *http.Client {
	client := fetch.NewClient()
	client.Timeout = 0

	return client
}

// download writes the archive to the file and checks its digest. It does not put the URL in an error.
func download(ctx context.Context, req *Request, dst io.Writer) error {
	client := req.Client
	if client == nil {
		client = newClient()
	}

	sum := sha256.New()
	limits := fetch.Limits{MaxBytes: maxArchiveBytes, Timeout: downloadTimeout}

	if err := fetch.Stream(ctx, client, req.URL, io.MultiWriter(dst, sum), limits); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if got := digestOf(sum); got != req.Asset.ArchiveSHA256 {
		return fmt.Errorf("the archive %s has the digest %s, the pin has %s", req.Asset.Archive, got, req.Asset.ArchiveSHA256)
	}

	return nil
}

// digestOf gives `sha256:<hex>` of a hash.
func digestOf(sum hash.Hash) string {
	return digest.Prefix + hex.EncodeToString(sum.Sum(nil))
}

// copyLimited copies at most limit bytes. A longer source is an error.
func copyLimited(dst io.Writer, src io.Reader, limit int64) error {
	n, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return fmt.Errorf("cannot read the data: %w", err)
	}

	if n > limit {
		return fmt.Errorf("the data is larger than %d MiB", limit/mebibyte)
	}

	return nil
}

// stage extracts the binary of the archive to a temporary file in dir, checks its digest, and sets its mode.
// It gives the path of the temporary file. On an error it removes the file.
func stage(archive *os.File, asset *Asset, dir string) (string, error) {
	out, err := os.CreateTemp(dir, "."+asset.Binary+"-*")
	if err != nil {
		return "", fmt.Errorf("cannot write in %s: %w", dir, err)
	}

	name := out.Name()

	err = extractBinary(archive, asset, out)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Chmod(name, binaryMode)
	}

	if err != nil {
		_ = os.Remove(name)

		return "", err
	}

	return name, nil
}

// extractBinary writes the binary of the archive to dst and checks its digest.
func extractBinary(archive *os.File, asset *Asset, dst io.Writer) error {
	sum := sha256.New()
	out := io.MultiWriter(dst, sum)

	var err error

	if strings.HasSuffix(asset.Archive, ".zip") {
		err = copyFromZip(archive, asset.Binary, out)
	} else {
		err = copyFromTarGz(archive, asset.Binary, out)
	}

	if err != nil {
		return err
	}

	if got := digestOf(sum); got != asset.BinarySHA256 {
		return fmt.Errorf("the binary %s has the digest %s, the pin has %s", asset.Binary, got, asset.BinarySHA256)
	}

	return nil
}
