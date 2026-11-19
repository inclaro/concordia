package cbmpin_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/cbmpin"
	"github.com/inclaro/concordia/internal/digest"
)

const (
	hexA = "8c3d0341064301e78ecc84cf7348d4e2c6562a0f57448243ea560aaa34de36c1"
	hexB = "77745491f0a9c346d6da23eed4d92b5feaa31a27a883c9faa4fa9f47c8183705"
)

// row joins the fields of a pin row with tabs.
func row(fields ...string) string { return strings.Join(fields, "\t") }

// pinText joins rows to the text of a pin file.
func pinText(rows ...string) string { return strings.Join(rows, "\n") + "\n" }

func validRows() []string {
	return []string{
		"# a comment",
		row("version", "v1.2.3"),
		row("repository", "acme/cbm"),
		"",
		row("linux-amd64", "cbm-linux-amd64.tar.gz", hexA, "cbm", hexB),
		row("windows-amd64", "cbm-windows-amd64.zip", hexB, "cbm.exe", hexA),
	}
}

func TestEmbeddedPinIsValidAndHasEveryPlatform(t *testing.T) {
	t.Parallel()

	pin := cbmpin.Default()

	if !strings.HasPrefix(pin.Version(), "v") || pin.Repository() != "DeusData/codebase-memory-mcp" {
		t.Errorf("version = %q, repository = %q", pin.Version(), pin.Repository())
	}

	want := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}
	if got := pin.Platforms(); !slices.Equal(got, want) {
		t.Fatalf("platforms = %v, want %v", got, want)
	}

	for _, platform := range want {
		goos, goarch, _ := strings.Cut(platform, "-")

		asset, ok := pin.Asset(goos, goarch)
		if !ok {
			t.Fatalf("no asset for %s", platform)
		}

		checkEmbeddedAsset(t, goos, &asset)
	}
}

// checkEmbeddedAsset checks the shape of one pinned asset: the archive type, the binary name and the digests.
func checkEmbeddedAsset(t *testing.T, goos string, asset *cbmpin.Asset) {
	t.Helper()

	wantArchive, wantBinary := ".tar.gz", "codebase-memory-mcp"
	if goos == "windows" {
		wantArchive, wantBinary = ".zip", "codebase-memory-mcp.exe"
	}

	if !strings.HasSuffix(asset.Archive, wantArchive) || asset.Binary != wantBinary {
		t.Errorf("%s: archive = %q, binary = %q", asset.Platform, asset.Archive, asset.Binary)
	}

	if goos == "linux" && !strings.HasSuffix(asset.Archive, "-portable.tar.gz") {
		t.Errorf("%s: archive %q is not the portable build", asset.Platform, asset.Archive)
	}

	for _, sum := range []string{asset.ArchiveSHA256, asset.BinarySHA256} {
		if !strings.HasPrefix(sum, digest.Prefix) || len(sum) != len(digest.Prefix)+64 {
			t.Errorf("%s: digest %q is not sha256:<64 hex>", asset.Platform, sum)
		}
	}
}

func TestParseReadsRowsCommentsAndWindowsLineEndings(t *testing.T) {
	t.Parallel()

	pin, err := cbmpin.Parse([]byte(strings.ReplaceAll(pinText(validRows()...), "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}

	asset, ok := pin.Asset("windows", "amd64")
	if !ok || asset.Binary != "cbm.exe" || asset.ArchiveSHA256 != "sha256:"+hexB || asset.BinarySHA256 != "sha256:"+hexA {
		t.Errorf("asset = %+v, %v", asset, ok)
	}

	if _, ok := pin.Asset("linux", "arm64"); ok {
		t.Error("an asset that is not in the file was found")
	}

	if pin.Version() != "v1.2.3" || pin.Repository() != "acme/cbm" {
		t.Errorf("version = %q, repository = %q", pin.Version(), pin.Repository())
	}
}

// withHead gives a pin file text with a valid version and repository row, and the rows.
func withHead(rows ...string) string {
	return pinText(append([]string{row("version", "v1.2.3"), row("repository", "acme/cbm")}, rows...)...)
}

func TestParseRefusesMalformedPins(t *testing.T) {
	t.Parallel()

	linux := row("linux-amd64", "cbm-linux-amd64.tar.gz", hexA, "cbm", hexB)
	platform := func(archive, sumA, binary, sumB string) string {
		return row("linux-amd64", archive, sumA, binary, sumB)
	}

	cases := []struct{ name, text string }{
		{"empty file", ""},
		{"no version", pinText(row("repository", "acme/cbm"), linux)},
		{"no repository", pinText(row("version", "v1.2.3"), linux)},
		{"no platform", withHead()},
		{"version twice", withHead(row("version", "v1.2.4"), linux)},
		{"repository twice", withHead(row("repository", "a/b"), linux)},
		{"version without v", pinText(row("version", "1.2.3"), row("repository", "acme/cbm"), linux)},
		{"repository without slash", pinText(row("version", "v1.2.3"), row("repository", "acme"), linux)},
		{"platform twice", withHead(linux, linux)},
		{"too few fields", withHead(row("linux-amd64", "cbm.tar.gz", hexA, "cbm"))},
		{"too many fields", withHead(linux + "\textra")},
		{"spaces, not tabs", withHead(strings.ReplaceAll(linux, "\t", " "))},
		{"platform without goarch", withHead(row("linux", "cbm.tar.gz", hexA, "cbm", hexB))},
		{"short digest", withHead(platform("cbm.tar.gz", hexA[:60], "cbm", hexB))},
		{"upper-case digest", withHead(platform("cbm.tar.gz", strings.ToUpper(hexA), "cbm", hexB))},
		{"digest with prefix", withHead(platform("cbm.tar.gz", "sha256:"+hexA, "cbm", hexB))},
		{"archive with a slash", withHead(platform("a/cbm.tar.gz", hexA, "cbm", hexB))},
		{"archive with dots", withHead(platform("../cbm.tar.gz", hexA, "cbm", hexB))},
		{"binary with a slash", withHead(platform("cbm.tar.gz", hexA, "bin/cbm", hexB))},
		{"archive type", withHead(platform("cbm.tar.xz", hexA, "cbm", hexB))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if pin, err := cbmpin.Parse([]byte(tc.text)); err == nil {
				t.Errorf("Parse gave no error, pin = %+v", pin)
			}
		})
	}
}

func TestArchiveURL(t *testing.T) {
	t.Parallel()

	pin, err := cbmpin.Parse([]byte(pinText(validRows()...)))
	if err != nil {
		t.Fatal(err)
	}

	asset, _ := pin.Asset("linux", "amd64")

	if got, want := pin.ArchiveURL("", &asset), "https://github.com/acme/cbm/releases/download/v1.2.3/cbm-linux-amd64.tar.gz"; got != want {
		t.Errorf("default URL = %q, want %q", got, want)
	}

	if got, want := pin.ArchiveURL("https://mirror.example/cbm/", &asset), "https://mirror.example/cbm/cbm-linux-amd64.tar.gz"; got != want {
		t.Errorf("mirror URL = %q, want %q", got, want)
	}
}
