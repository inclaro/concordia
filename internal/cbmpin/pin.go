// Package cbmpin holds the codebase-memory-mcp (CBM) release that Concordia is tested with, and installs it.
//
// The pin file `cbm-pin.tsv` is the only source of the pinned version and digests. The `concordia` binary embeds it
// (`concordia cbm install` and `doctor` read it), and the mise task `tools:cbm` reads the same file.
package cbmpin

import (
	_ "embed" // the pin file is part of the binary.
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/digest"
)

//go:embed cbm-pin.tsv
var embeddedPin []byte

const (
	rowVersion    = "version"
	rowRepository = "repository"
	platformCols  = 5
	hexLength     = 64
)

var (
	hexPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	platformPattern = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
	// fileNamePattern limits an archive or binary name to one path element.
	fileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	versionPattern  = regexp.MustCompile(`^v\d[A-Za-z0-9._-]*$`)
	repoPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
)

// Asset is the pinned release archive of one platform and the binary inside it.
type Asset struct {
	// Platform is `<goos>-<goarch>`, for example `linux-amd64`.
	Platform string
	// Archive is the file name of the release archive (`.tar.gz` or `.zip`).
	Archive string
	// ArchiveSHA256 is `sha256:<hex>` of the archive.
	ArchiveSHA256 string
	// Binary is the name of the executable in the archive and in the install directory.
	Binary string
	// BinarySHA256 is `sha256:<hex>` of the executable.
	BinarySHA256 string
}

// Pin is the pinned CBM release: its version and the assets of all platforms.
type Pin struct {
	assets     map[string]Asset
	version    string
	repository string
}

// Version gives the release tag, for example `v0.12.1`.
func (p *Pin) Version() string { return p.version }

// Repository gives the GitHub repository of the release, for example `DeusData/codebase-memory-mcp`.
func (p *Pin) Repository() string { return p.repository }

// Platforms gives the platforms that have a pinned asset, sorted.
func (p *Pin) Platforms() []string {
	names := make([]string, 0, len(p.assets))
	for name := range p.assets {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// Asset gives the pinned asset of a platform. The bool is false when the pin has no asset for it.
func (p *Pin) Asset(goos, goarch string) (Asset, bool) {
	asset, ok := p.assets[goos+"-"+goarch]

	return asset, ok
}

// DefaultBaseURL gives the https directory of the GitHub release that holds the archives.
func (p *Pin) DefaultBaseURL() string {
	return "https://github.com/" + p.repository + "/releases/download/" + p.version
}

// Default gives the pin that is embedded in the binary. It panics when the embedded file is not valid;
// the tests of this package make sure that it is.
var Default = sync.OnceValue(func() *Pin {
	pin, err := Parse(embeddedPin)
	if err != nil {
		panic("cbmpin: the embedded cbm-pin.tsv is not valid: " + err.Error())
	}

	return pin
})

// Parse reads a pin file. Blank lines and lines that start with `#` are ignored. A line that is not
// understood, a duplicate row, a digest that is not 64 lower-case hex digits, or a missing version,
// repository or platform is an error.
func Parse(data []byte) (*Pin, error) {
	pin := &Pin{assets: map[string]Asset{}}

	for number, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if err := pin.addRow(strings.Split(line, "\t")); err != nil {
			return nil, fmt.Errorf("line %d: %w", number+1, err)
		}
	}

	switch {
	case pin.version == "":
		return nil, errors.New("no version row")
	case pin.repository == "":
		return nil, errors.New("no repository row")
	case len(pin.assets) == 0:
		return nil, errors.New("no platform row")
	}

	return pin, nil
}

// addRow adds one row of the pin file.
func (p *Pin) addRow(fields []string) error {
	switch fields[0] {
	case rowVersion:
		return setOnce(&p.version, fields, versionPattern, rowVersion)
	case rowRepository:
		return setOnce(&p.repository, fields, repoPattern, rowRepository)
	}

	asset, err := parseAsset(fields)
	if err != nil {
		return err
	}

	if _, dup := p.assets[asset.Platform]; dup {
		return fmt.Errorf("platform %s is twice in the file", asset.Platform)
	}

	p.assets[asset.Platform] = asset

	return nil
}

// setOnce sets the value of a `<name> <value>` row. A second row of the same name is an error.
func setOnce(target *string, fields []string, pattern *regexp.Regexp, name string) error {
	if len(fields) != 2 || !pattern.MatchString(fields[1]) {
		return fmt.Errorf("the %s row needs one valid value", name)
	}

	if *target != "" {
		return fmt.Errorf("the %s row is twice in the file", name)
	}

	*target = fields[1]

	return nil
}

// parseAsset reads a `<platform> <archive> <archive sha256> <binary> <binary sha256>` row.
func parseAsset(fields []string) (Asset, error) {
	if len(fields) != platformCols {
		return Asset{}, fmt.Errorf("row '%s' needs %d tab-separated fields, found %d", fields[0], platformCols, len(fields))
	}

	asset := Asset{
		Platform:      fields[0],
		Archive:       fields[1],
		ArchiveSHA256: digest.Prefix + fields[2],
		Binary:        fields[3],
		BinarySHA256:  digest.Prefix + fields[4],
	}

	switch {
	case !platformPattern.MatchString(asset.Platform):
		return Asset{}, fmt.Errorf("'%s' is not a platform (<goos>-<goarch>)", asset.Platform)
	case !fileNamePattern.MatchString(asset.Archive):
		return Asset{}, fmt.Errorf("platform %s: archive name '%s' is not valid", asset.Platform, asset.Archive)
	case !fileNamePattern.MatchString(asset.Binary):
		return Asset{}, fmt.Errorf("platform %s: binary name '%s' is not valid", asset.Platform, asset.Binary)
	case !hexPattern.MatchString(fields[2]) || !hexPattern.MatchString(fields[4]):
		return Asset{}, fmt.Errorf("platform %s: a digest is not %d lower-case hex digits", asset.Platform, hexLength)
	case !strings.HasSuffix(asset.Archive, ".tar.gz") && !strings.HasSuffix(asset.Archive, ".zip"):
		return Asset{}, fmt.Errorf("platform %s: archive '%s' is not a .tar.gz or .zip file", asset.Platform, asset.Archive)
	}

	return asset, nil
}
