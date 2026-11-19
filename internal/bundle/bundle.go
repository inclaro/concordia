// Package bundle writes and reads the conformance bundle: a deterministic gzip tar archive with a manifest.
package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
)

const (
	// ManifestPath is the path of the manifest in the archive.
	ManifestPath = "manifest.json"
	// SchemaVersion is the manifest `schemaVersion`.
	SchemaVersion = "1.0"
	// Kind is the manifest `kind`.
	Kind = "ConformanceBundle"

	maxFileSize = 256 << 20
	// maxTotalSize and maxEntries bound a whole archive, so a bundle of many allowed files cannot exhaust memory or disk.
	maxTotalSize = 1 << 30
	maxEntries   = 10_000
	// maxHeaderBytes is the stream allowance per entry for tar headers, PAX records and padding.
	maxHeaderBytes = 4 << 10
	fileMode       = 0o644
)

// maxStreamSize bounds the decompressed tar stream, so header records that tar.Reader consumes
// inside Next (local PAX headers, long names) count too. A variable so tests can lower it.
var maxStreamSize int64 = maxTotalSize + maxEntries*maxHeaderBytes

// Entry is one file of the bundle.
type Entry struct {
	Path string
	Data []byte
}

// ManifestFile lists one file of the bundle.
type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Integrity levels, the `integrity.level` values.
const (
	// LevelDigest says that the digests of the files and of the manifest are recorded.
	LevelDigest = "digest"
	// AlgorithmSHA256 is the `integrity.algorithm` of this version.
	AlgorithmSHA256 = "sha256"

	// Availability values of a reference.
	Included    = "included"
	Unavailable = "unavailable"
	// Reasons of an unavailable reference.
	ReasonNotIncluded = "not-included"
	ReasonMissing     = "missing"
	// ReasonNotReproducible marks a reference that can no longer be resolved or whose content differs from the
	// recorded digest: historical reproduction is not available for it.
	ReasonNotReproducible = "not-reproducible"
)

// Integrity is the digest metadata of a bundle. The digest of a file is `sha256:<hex>`. ManifestDigest is
// the digest of the canonical manifest with `integrity.manifestDigest` set to "".
type Integrity struct {
	Files          map[string]string `json:"files"`
	Algorithm      string            `json:"algorithm"`
	ManifestDigest string            `json:"manifestDigest"`
	Level          string            `json:"level"`
}

// Reference is a content-addressed reference that a result depends on.
type Reference struct {
	Kind         string `json:"kind"`
	Digest       string `json:"digest"`
	Availability string `json:"availability"`
	// Reason is set when the reference is unavailable (`not-included` or `missing`).
	Reason string `json:"reason,omitempty"`
}

// Reproduction says whether the result can be reproduced from the bundle.
type Reproduction struct {
	Reason string `json:"reason,omitempty"`
	// Unavailable lists the kinds of the unavailable references, sorted.
	Unavailable []string `json:"unavailable"`
	Available   bool     `json:"available"`
}

// Redaction is one record of the redaction pass: where a value was removed and its class, never the value.
// Path is the bundle file that holds the marker. File and Line are the source file and the 1-based line
// of the value, Property is the key whose value was removed; each is empty when the pass does not know it.
type Redaction struct {
	Path     string `json:"path"`
	File     string `json:"file,omitempty"`
	Property string `json:"property,omitempty"`
	Class    string `json:"class"`
	Line     int    `json:"line,omitempty"`
	Count    int    `json:"count"`
}

// SourceRevision is the source state of one project.
type SourceRevision struct {
	Revision        *string `json:"revision"`
	Project         string  `json:"project"`
	DirtyTreeDigest string  `json:"dirtyTreeDigest"`
	Dirty           bool    `json:"dirty"`
}

// DigestRef is a path with its digest and an optional identity.
type DigestRef struct {
	Path    string `json:"path,omitempty"`
	ID      string `json:"id,omitempty"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// Provenance holds the OUT-04 R0 items. Later changes add keys; no key is removed or renamed.
type Provenance struct {
	// Evaluators identify the tools that produced the result, by name.
	Evaluators map[string]string `json:"evaluators"`
	// Contracts are the versions of the contracts of the result (result schema, bundle schema; others are additive).
	Contracts map[string]string `json:"contracts"`
	// Calm is the CALM release (schema version) and the digest of the architecture document.
	Calm CalmRef `json:"calm"`
	// Workspace is the active workspace profile (empty when none) and the digest of the workspace file.
	Workspace WorkspaceRef `json:"workspace"`
	// BuildDigest is the digest of the Concordia executable; empty when it cannot be read.
	BuildDigest string `json:"buildDigest"`
	// StartedAt and FinishedAt are ISO 8601 UTC timestamps of the run.
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	// Graph is the graph snapshots and the CBM versions.
	Graph GraphRef `json:"graph"`
	// Sources are the source revisions and dirty state, by project.
	Sources []SourceRevision `json:"sources"`
	// Policies are the policy files with their digests and, where known, pack id and version.
	Policies []DigestRef `json:"policies"`
	// Decisions are the decision register files and the lifecycle profile that the ADR governance checks read, with
	// their digests. The lifecycle profile is part of the workspace file: its path is `<workspace>#adr.lifecycle`
	// and the bundle holds no file under that path. Left out when the workspace has no register.
	Decisions []DigestRef `json:"decisions,omitempty"`
	// Environments is the environment selection of the run.
	Environments EnvironmentsRef `json:"environments"`
	// Review is the review evaluation: the digest contract, the authority configuration, and the trust anchor
	// revision that verification used. Left out when the run evaluated no assessment.
	Review *ReviewRef `json:"review,omitempty"`
	// Exceptions are the exception registers that the run read, and what it used.
	Exceptions ExceptionsRef `json:"exceptions"`
}

// ReviewRef identifies what the review evaluation of the run compared under.
type ReviewRef struct {
	ContractVersion   string `json:"contractVersion"`
	CapabilityProfile string `json:"capabilityProfile"`
	// AuthorityDigest is the digest of the authority configuration that verification used.
	AuthorityDigest string `json:"authorityDigest"`
	// TrustAnchor is the revision of the trust anchor; empty when it could not be read.
	TrustAnchor string `json:"trustAnchor"`
}

// EnvironmentsRef is the environment selection of the run: ids, and the digest of the declared environments.
type EnvironmentsRef struct {
	Digest   string   `json:"digest"`
	Selected []string `json:"selected"`
	Excluded []string `json:"excluded"`
}

// CalmRef is the CALM release and document digest.
type CalmRef struct {
	Release string `json:"release"`
	Digest  string `json:"digest"`
}

// WorkspaceRef is the workspace file and active profile.
type WorkspaceRef struct {
	Profile string `json:"profile"`
	Path    string `json:"path"`
	Digest  string `json:"digest"`
}

// GraphRef is the graph snapshot identity of the run.
type GraphRef struct {
	CBMVersion string `json:"cbmVersion"`
	// CBMSHA256 is `sha256:<hex>` of the CBM executable file of the run. It is left out when the run has none.
	CBMSHA256     string `json:"cbmSha256,omitempty"`
	AdapterSchema string `json:"adapterSchema"`
	// Profile is the capability profile of the CBM build. When no profile
	// applied, Applies is false and the field is still written.
	Profile   ProfileRef `json:"profile"`
	Snapshots []string   `json:"snapshots"`
}

// ProfileRef is the capability profile of the provider build of the run.
type ProfileRef struct {
	ID      string `json:"id,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Applies bool   `json:"applies"`
}

// ExceptionsRef is the exception registers of the run.
type ExceptionsRef struct {
	Baseline *DigestRef `json:"baseline,omitempty"`
	Waivers  *DigestRef `json:"waivers,omitempty"`
	// WaiversUsed are the IDs of the waivers that findings refer to.
	WaiversUsed []string `json:"waiversUsed"`
}

// Manifest is `manifest.json`.
type Manifest struct {
	SchemaVersion string          `json:"schemaVersion"`
	Kind          string          `json:"kind"`
	CreatedAt     string          `json:"createdAt"`
	Concordia     string          `json:"concordia"`
	Toolchain     json.RawMessage `json:"toolchain"`
	Run           json.RawMessage `json:"run"`
	Options       BundleOptions   `json:"options"`
	Provenance    Provenance      `json:"provenance"`
	// Files are sorted by path. The manifest is not listed. Write sets them from the entries.
	Files []ManifestFile `json:"files"`
	// References are the content-addressed references that the result depends on. The caller sets them.
	References []Reference `json:"references"`
	// Redactions list what the redaction pass removed. The caller sets them; empty when nothing was redacted.
	Redactions []Redaction `json:"redactions"`
	// RedactionRules is the version of the redaction rule set that made the bundle.
	RedactionRules string `json:"redactionRules,omitempty"`
	// Views list the files of the declared views, sorted by view ID. The caller sets them; a workspace without
	// declared views has none and the key is left out.
	Views []View `json:"views,omitempty"`
	// Integrity is set by Write.
	Integrity Integrity `json:"integrity"`
	// Reproduction is set by Write from References.
	Reproduction Reproduction `json:"reproduction"`
}

// The states of a view and of one of its files in the bundle. A file that is not in the tracked revision is
// `absent`: the bundle lists it and holds no content for it.
const (
	StatePresent = "present"
	StateAbsent  = "absent"

	// RoleProjection is the role of the semantic projection of a view; the other roles are the roles of the
	// view files (`manifest`, `source`, `output`, `export`).
	RoleProjection = "projection"
	// ViewsDir is the directory of the bundle that holds the files of the declared views.
	ViewsDir = "views/"
	// ScopedWaiversPath is the file of the bundle with the scoped waivers of the run: scope, approver,
	// verification result, expiry, and the findings that each waiver covered.
	ScopedWaiversPath = "exceptions/scoped.json"
	// AssessmentsDir and ApprovalsDir hold the assessment and assumption files, and the approval and revocation
	// files, of the run, below their workspace-relative paths. AssessmentVerificationPath is the verification
	// result of the decision records, with the authority configuration used and the trust anchor revision.
	AssessmentsDir             = "assessments/"
	ApprovalsDir               = "approvals/"
	AssessmentVerificationPath = "verification/assessments.json"
)

// ViewFile lists one file of a declared view. Path is the repository path of a committed artifact and is empty
// for the projection, which the run computes. File is the path of the content in the bundle and SHA256 its digest;
// both are empty for an absent file.
type ViewFile struct {
	Role   string `json:"role"`
	Path   string `json:"path,omitempty"`
	State  string `json:"state"`
	File   string `json:"file,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// View lists the files that the bundle holds for one declared view. State is `absent` when a file that the view
// declares is not in the tracked revision, else `present`.
type View struct {
	ID     string     `json:"id"`
	Origin string     `json:"origin"`
	State  string     `json:"state"`
	Files  []ViewFile `json:"files"`
}

// BundleOptions are the options that made the bundle.
type BundleOptions struct {
	IncludeSourceSnippets string `json:"includeSourceSnippets"`
	IncludeGraph          bool   `json:"includeGraph"`
}

func invalid(detail string) *diag.Error {
	return diag.Fail(diag.REP004, diag.Detail(detail))
}

// finish sets the derived parts of the manifest: integrity (without the manifest digest), reproduction and empty lists.
func (m *Manifest) finish(files map[string]string) {
	m.Integrity = Integrity{Algorithm: AlgorithmSHA256, Files: files, Level: LevelDigest}

	if m.References == nil {
		m.References = []Reference{}
	}

	if m.Redactions == nil {
		m.Redactions = []Redaction{}
	}

	missing := []string{}

	for _, r := range m.References {
		if r.Availability != Included {
			missing = append(missing, r.Kind)
		}
	}

	slices.Sort(missing)

	m.Reproduction = Reproduction{Available: len(missing) == 0, Unavailable: missing}
	if len(missing) > 0 {
		m.Reproduction.Reason = "full reproduction is not available: " + strings.Join(missing, ", ") + " not in the bundle"
	}
}

// manifestDigestOf gives the digest of the canonical manifest text with `integrity.manifestDigest` set to "".
func manifestDigestOf(text []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()

	var tree map[string]any
	if err := dec.Decode(&tree); err != nil {
		return "", invalid("manifest.json: " + err.Error())
	}

	if integrity, ok := tree["integrity"].(map[string]any); ok {
		integrity["manifestDigest"] = ""
	}

	canon, err := canonjson.Marshal(tree)
	if err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}

	return digest.OfBytes(canon), nil
}

// Write writes the archive. The first entry is the manifest, then the entries sorted by
// path. The same input gives the same bytes. Write sets manifest.Files from the entries.
func Write(dst io.Writer, source *Manifest, entries []Entry) error {
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b Entry) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	text, err := manifestText(source, sorted)
	if err != nil {
		return err
	}

	return writeArchive(dst, append([]Entry{{Path: ManifestPath, Data: text}}, sorted...))
}

// manifestText gives the canonical text of the manifest of the sorted entries, with its manifest digest.
func manifestText(source *Manifest, sorted []Entry) ([]byte, error) {
	manifest := *source
	manifest.Files = make([]ManifestFile, 0, len(sorted))
	files := make(map[string]string, len(sorted))

	for _, e := range sorted {
		sum := digest.OfBytes(e.Data)
		manifest.Files = append(manifest.Files, ManifestFile{Path: e.Path, SHA256: sum, Size: int64(len(e.Data))})
		files[e.Path] = sum
	}

	manifest.finish(files)

	text, err := canonjson.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}

	sum, err := manifestDigestOf(text)
	if err != nil {
		return nil, err
	}

	manifest.Integrity.ManifestDigest = sum

	if text, err = canonjson.Marshal(manifest); err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}

	return text, nil
}

// writeArchive writes the entries as a reproducible gzip tar archive.
func writeArchive(dst io.Writer, all []Entry) error {
	gzw := gzip.NewWriter(dst)
	gzw.ModTime = time.Time{}
	gzw.Name = ""
	tarw := tar.NewWriter(gzw)

	for _, e := range all {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg, Name: e.Path, Mode: fileMode, Size: int64(len(e.Data)),
			ModTime: time.Unix(0, 0), Format: tar.FormatPAX,
		}
		if err := tarw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write bundle header %s: %w", e.Path, err)
		}

		if _, err := tarw.Write(e.Data); err != nil {
			return fmt.Errorf("write bundle file %s: %w", e.Path, err)
		}
	}

	if err := tarw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}

	if err := gzw.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}

	return nil
}

func safePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}

	if strings.Contains(p, "\\") {
		return false
	}

	return !slices.Contains(strings.Split(p, "/"), "..")
}

// Open extracts the archive at archivePath into dir, checks it against its manifest and gives the manifest.
// It rejects an archive with more than maxEntries entries (directories count) or more than maxTotalSize
// bytes of content.
func Open(archivePath, dir string) (*Manifest, error) {
	manifest, _, err := open(archivePath, dir)

	return manifest, err
}

// tarOf reads the gzip tar archive at archivePath. The decompressed stream is cut at maxStreamSize.
func tarOf(archivePath string) (*tar.Reader, error) {
	data, err := os.ReadFile(archivePath) //nolint:gosec // the caller chooses the archive.
	if err != nil {
		return nil, invalid(fmt.Sprintf("cannot read %s: %s", archivePath, err))
	}

	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, invalid(fmt.Sprintf("%s is not a gzip archive: %s", archivePath, err))
	}

	return tar.NewReader(&capReader{r: gzr, left: maxStreamSize}), nil
}

// errStreamTooLarge is the error of a capReader past its limit.
var errStreamTooLarge = errors.New("the decompressed archive is larger than the limit")

// capReader gives errStreamTooLarge once the stream has more than left bytes. A stream of exactly
// left bytes ends with io.EOF.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(buf []byte) (int, error) {
	if c.left <= 0 {
		var probe [1]byte
		if n, err := c.r.Read(probe[:]); n == 0 && err != nil {
			return 0, err
		}

		return 0, errStreamTooLarge
	}

	if int64(len(buf)) > c.left {
		buf = buf[:c.left]
	}

	n, err := c.r.Read(buf)
	c.left -= int64(n)

	return n, err
}

// open is Open that also gives the text of manifest.json. An empty dir checks the archive and writes nothing.
func open(archivePath, dir string) (*Manifest, []byte, error) {
	tarr, err := tarOf(archivePath)
	if err != nil {
		return nil, nil, err
	}

	var used budget

	ext := &extraction{dir: dir, seen: map[string]struct{}{}}

	for {
		hdr, content, err := used.next(tarr)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, nil, err
		}

		if err := ext.accept(hdr.Name, content); err != nil {
			return nil, nil, err
		}
	}

	if err := ext.finish(); err != nil {
		return nil, nil, err
	}

	return ext.manifest, ext.text, nil
}

// budget counts what the archive uses against maxEntries and maxTotalSize.
type budget struct {
	entries int
	total   int64
}

// next gives the next file of the archive and its content, skipping directories, or io.EOF at the end.
// Every header counts against maxEntries, so an archive of directory headers alone is bounded too.
func (b *budget) next(archive *tar.Reader) (*tar.Header, []byte, error) {
	for {
		hdr, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil, nil, io.EOF
		}

		if err != nil {
			return nil, nil, invalid(fmt.Sprintf("cannot read the archive: %s", err))
		}

		if err := b.entry(); err != nil {
			return nil, nil, err
		}

		if hdr.Typeflag == tar.TypeDir {
			continue
		}

		content, err := b.read(archive, hdr)

		return hdr, content, err
	}
}

// read reads the entry with readEntry and counts its bytes. The caller counted the entry.
func (b *budget) read(archive *tar.Reader, hdr *tar.Header) ([]byte, error) {
	content, err := readEntry(archive, hdr)
	if err != nil {
		return nil, err
	}

	return content, b.bytes(len(content))
}

func (b *budget) entry() error {
	b.entries++
	if b.entries > maxEntries {
		return invalid(fmt.Sprintf("the archive has more than %d entries", maxEntries))
	}

	return nil
}

func (b *budget) bytes(n int) error {
	b.total += int64(n)
	if b.total > maxTotalSize {
		return invalid(fmt.Sprintf("the archive content is larger than %d bytes", int64(maxTotalSize)))
	}

	return nil
}

// allSeen checks that the archive has every file that the manifest lists.
func allSeen(manifest *Manifest, seen map[string]struct{}) error {
	for _, f := range manifest.Files {
		if _, ok := seen[f.Path]; !ok {
			return invalid(fmt.Sprintf("file %s is in the manifest, but not in the archive", f.Path))
		}
	}

	return nil
}

// readEntry checks that a tar entry is a regular file with an allowed path and size, and gives its content.
func readEntry(archive *tar.Reader, hdr *tar.Header) ([]byte, error) {
	if hdr.Typeflag != tar.TypeReg {
		return nil, invalid(fmt.Sprintf("entry %s is not a regular file", hdr.Name))
	}

	if !safePath(hdr.Name) {
		return nil, invalid(fmt.Sprintf("entry path '%s' is not allowed", hdr.Name))
	}

	if hdr.Size > maxFileSize {
		return nil, invalid(fmt.Sprintf("entry %s is larger than %d bytes", hdr.Name, maxFileSize))
	}

	content, err := io.ReadAll(io.LimitReader(archive, maxFileSize+1))
	if err != nil {
		return nil, invalid(fmt.Sprintf("cannot read entry %s: %s", hdr.Name, err))
	}

	if int64(len(content)) > maxFileSize {
		return nil, invalid(fmt.Sprintf("entry %s is larger than %d bytes", hdr.Name, maxFileSize))
	}

	return content, nil
}

// extraction checks the entries of an archive against its manifest and extracts them into dir.
// An empty dir checks and writes nothing.
type extraction struct {
	manifest *Manifest
	listed   map[string]ManifestFile
	seen     map[string]struct{}
	dir      string
	text     []byte
}

// accept checks the next file of the archive. The first one must be the manifest, the others
// must be listed in it.
func (x *extraction) accept(name string, content []byte) error {
	if x.manifest == nil {
		return x.acceptManifest(name, content)
	}

	return x.acceptListed(name, content)
}

// acceptManifest reads the first entry of the archive, which must be the manifest,
// extracts it and keeps the manifest with its file list by path.
func (x *extraction) acceptManifest(name string, content []byte) error {
	if name != ManifestPath {
		return invalid("the first entry is not " + ManifestPath)
	}

	manifest := &Manifest{}
	if err := json.Unmarshal(content, manifest); err != nil {
		return invalid("manifest.json: " + err.Error())
	}

	listed := make(map[string]ManifestFile, len(manifest.Files))
	for _, f := range manifest.Files {
		if !safePath(f.Path) {
			return invalid(fmt.Sprintf("manifest path '%s' is not allowed", f.Path))
		}

		listed[f.Path] = f
	}

	if err := extract(x.dir, ManifestPath, content); err != nil {
		return err
	}

	x.manifest = manifest
	x.listed = listed
	x.text = content

	return nil
}

// acceptListed checks an entry after the manifest against the manifest (listed, once, size, digest),
// then extracts it.
func (x *extraction) acceptListed(name string, content []byte) error {
	want, ok := x.listed[name]
	if !ok {
		return invalid(fmt.Sprintf("file %s is not in the manifest", name))
	}

	if _, dup := x.seen[name]; dup {
		return invalid(fmt.Sprintf("file %s appears two times", name))
	}

	x.seen[name] = struct{}{}

	if int64(len(content)) != want.Size {
		return invalid(fmt.Sprintf("file %s has size %d, the manifest says %d", name, len(content), want.Size))
	}

	if digest.OfBytes(content) != want.SHA256 {
		return invalid(fmt.Sprintf("file %s does not match the digest in the manifest", name))
	}

	return extract(x.dir, name, content)
}

// finish checks that the archive had a manifest and every file that it lists.
func (x *extraction) finish() error {
	if x.manifest == nil {
		return invalid("the archive has no " + ManifestPath)
	}

	return allSeen(x.manifest, x.seen)
}

func extract(dir, name string, content []byte) error {
	if dir == "" {
		return nil
	}

	if err := fsutil.WriteFileAtomic(filepath.Join(dir, filepath.FromSlash(name)), content); err != nil {
		return fmt.Errorf("extract %s: %w", name, err)
	}

	return nil
}
