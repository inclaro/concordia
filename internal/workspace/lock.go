package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
)

// CalmSchemaVersions are the supported CALM schema releases.
var CalmSchemaVersions = []string{"1.2", "1.3"}

// cbmDigestPattern is the form of the `resolved.cbm.sha256` of a lock: `sha256:` and 64 lower-case hex digits.
var cbmDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

const (
	// LockFileName is the name of the lockfile in the workspace root.
	LockFileName = ".concordia.lock"
	// AdapterSchema is the ID of the CBM adapter schema.
	AdapterSchema = "concordia-cbm/v2"
	// DefaultCalmSchemaVersion is the CALM schema release of a document without `$schema`,
	// and of a lock without `resolved.calm.schemaVersion`.
	DefaultCalmSchemaVersion = "1.2"
	// LatestCalmSchemaVersion is the CALM schema release that `init` writes.
	LatestCalmSchemaVersion = "1.3"

	lockVersion    = 1
	unknownVersion = "unknown"
	yamlIndent     = 2
)

// LockResource is a url-mapped resource in the lockfile.
type LockResource struct {
	URI string `yaml:"uri"`
	// Path is relative to the workspace root and uses "/".
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

// LockPack is a policy pack in the lockfile.
type LockPack struct {
	// ID and Version come from the pack metadata.
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	// Source is the `use` value of the workspace.
	Source string `yaml:"source"`
	// SHA256 is `sha256:<hex>` of the pack text.
	SHA256 string `yaml:"sha256"`
}

// LockCalm is the resolved CALM schema.
type LockCalm struct {
	SchemaVersion string `yaml:"schemaVersion"`
}

// LockProfile is the capability profile of the CBM build: its identifier and content digest.
type LockProfile struct {
	ID     string `yaml:"id"`
	Digest string `yaml:"digest"`
}

// LockCBM is the resolved CBM toolchain.
type LockCBM struct { //nolint:govet // the field order is the key order of the lockfile text.
	ExecutableVersion string `yaml:"executableVersion"`
	AdapterSchema     string `yaml:"adapterSchema"`
	// SHA256 is `sha256:<hex>` of the CBM executable file that was found when the lock was written. It is empty
	// for a lock that records none: such a lock promises nothing about the executable.
	SHA256 string `yaml:"sha256,omitempty"`
	// NormalizationVersion is the version of the evidence normalization table that applied when
	// the lock was written. It is empty for a lock that records none.
	NormalizationVersion string `yaml:"normalizationVersion,omitempty"`
	// Profile is the capability profile that applied when the lock was written.
	// It is nil for a lock that records none.
	Profile *LockProfile `yaml:"profile,omitempty"`
}

// LockStructurizr is the resolved Structurizr CLI.
type LockStructurizr struct {
	CLIVersion string `yaml:"cliVersion"`
}

// LockRenderer is the pin of one renderer: the version it reports and the digest of its executable.
type LockRenderer struct {
	Version string `yaml:"version"`
	// SHA256 is `sha256:<hex>` of the executable file.
	SHA256 string `yaml:"sha256"`
}

// LockRenderers are the pins of the renderers that a declared source uses. A tool that no source uses has no pin.
type LockRenderers struct {
	Helm      *LockRenderer `yaml:"helm,omitempty"`
	Kustomize *LockRenderer `yaml:"kustomize,omitempty"`
}

// LockResolved is the resolved toolchain.
type LockResolved struct { //nolint:govet // the field order is the key order of the lockfile text.
	Structurizr *LockStructurizr `yaml:"structurizr,omitempty"`
	Calm        LockCalm         `yaml:"calm"`
	CBM         LockCBM          `yaml:"cbm"`
	// Renderers is nil when no declared source needs a renderer.
	Renderers *LockRenderers `yaml:"renderers,omitempty"`
}

// Lockfile is the content of the lockfile.
type Lockfile struct { //nolint:govet // the field order is the key order of the lockfile text, which a reader expects to start with lockVersion.
	LockVersion int            `yaml:"lockVersion"`
	Resolved    LockResolved   `yaml:"resolved"`
	Resources   []LockResource `yaml:"resources"`
	// Packs are the policy packs that the workspace uses. The key is written only when there is a pack.
	Packs []LockPack `yaml:"packs,omitempty"`
}

// ResourceRef is a url-mapped resource found in this run.
type ResourceRef struct {
	URI string
	// AbsPath is the absolute OS path of the local file.
	AbsPath string
}

// PackRef is a policy pack used in this run.
type PackRef struct {
	ID      string
	Version string
	// Source is the `use` value.
	Source string
	// SHA256 is `sha256:<hex>` of the pack text.
	SHA256 string
}

// RendererPin is the identity of a renderer: its tool name (`helm` or `kustomize`), the version it reports, and
// `sha256:<hex>` of its executable. The zero Version and SHA256 say that the renderer was not found.
type RendererPin struct {
	Tool    string
	Version string
	SHA256  string
}

// Found says whether the renderer was found.
func (r RendererPin) Found() bool { return r.Version != "" || r.SHA256 != "" }

// Pin gives the lock entry of the renderer.
func (r RendererPin) Pin() LockRenderer { return LockRenderer{Version: r.Version, SHA256: r.SHA256} }

// VerifyContext is the input of VerifyLock.
type VerifyContext struct {
	// CBMProfile is the capability profile of the found CBM build, or nil
	// when none applies. It is compared with the profile of the lock.
	CBMProfile *LockProfile
	// Root is the absolute workspace root (OS path).
	Root string
	// LockPath is the absolute OS path of the lockfile.
	LockPath string
	// CBMVersion is the found CBM version, or "" when CBM is missing.
	CBMVersion string
	// CBMExecutable is the resolved path of the found CBM executable, or "" when the client has none.
	CBMExecutable string
	// CBMSHA256 is `sha256:<hex>` of the found CBM executable file, or "" when the client has none.
	CBMSHA256 string
	// CalmSchemaVersion is the CALM schema release of the architecture, or "".
	CalmSchemaVersion string
	// Resources are the url-mapped resources used by this run.
	Resources []ResourceRef
	// Packs are the policy packs of this run (the packs of `policies.packs`).
	Packs []PackRef
	// Renderers are the renderers that the declared sources of this run use, as found on this machine. Empty when no
	// source needs a renderer.
	Renderers []RendererPin
	Offline   bool
}

// SanitizeURI removes credentials (user, password) from a URI.
func SanitizeURI(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.User == nil {
		return uri
	}

	if _, hasPassword := parsed.User.Password(); parsed.User.Username() == "" && !hasPassword {
		return uri
	}

	parsed.User = nil

	return parsed.String()
}

// LockPathFor gives the OS path of the lockfile of a workspace root.
func LockPathFor(root string) string {
	return filepath.Join(root, LockFileName)
}

func lockFailure(detail, path string) *diag.Error {
	return diag.Fail(diag.WRK002, diag.Detail(detail), diag.WithSource(diag.Source{URI: path}))
}

// ReadLock reads a lockfile. found is false when the file does not exist. A
// bad lockfile gives CO-WRK-002.
func ReadLock(path string) (lock *Lockfile, found bool, err error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller chooses the lockfile path: reading it is the function.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, lockFailure("cannot read lockfile: "+err.Error(), path)
	}

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, false, lockFailure("lockfile YAML error: "+err.Error(), path)
	}

	l, ok := parseLock(normalize(raw))
	if !ok {
		return nil, false, lockFailure("lockfile has an unknown shape (lockVersion 1 expected)", path)
	}

	slices.SortStableFunc(l.Resources, func(a, b LockResource) int { return canonjson.CompareUTF16(a.URI, b.URI) })
	sortPacks(l.Packs)

	return l, true, nil
}

// parseLock checks the shape of a decoded lockfile.
func parseLock(raw any) (*Lockfile, bool) {
	doc, ok := raw.(object)
	if !ok || num(doc, "lockVersion", 0) != lockVersion {
		return nil, false
	}

	resolved := obj(doc, "resolved")

	cbm, cbmOK := parseLockCBM(obj(resolved, "cbm"))
	list, lok := doc["resources"].([]any)

	if !cbmOK || !lok {
		return nil, false
	}

	resources, valid := parseLockResources(list)
	if !valid {
		return nil, false
	}

	packs, valid := parseLockPacks(doc["packs"])
	if !valid {
		return nil, false
	}

	parsed := &Lockfile{
		LockVersion: lockVersion,
		Resolved: LockResolved{
			Calm: LockCalm{SchemaVersion: str(obj(resolved, "calm"), "schemaVersion", DefaultCalmSchemaVersion)},
			CBM:  cbm,
		},
		Resources: resources,
		Packs:     packs,
	}

	if structurizr, ok := resolved["structurizr"].(object); ok {
		parsed.Resolved.Structurizr = &LockStructurizr{CLIVersion: str(structurizr, "cliVersion", "")}
	}

	if renderers, ok := resolved["renderers"].(object); ok {
		parsed.Resolved.Renderers = &LockRenderers{
			Helm:      lockRenderer(renderers, RenderToolHelm),
			Kustomize: lockRenderer(renderers, RenderToolKustomize),
		}
	}

	return parsed, true
}

// parseLockCBM reads `resolved.cbm`. The version and the adapter schema are required. `sha256` is optional, and when it
// is there it has to be `sha256:` and 64 lower-case hex digits.
func parseLockCBM(cbm object) (LockCBM, bool) {
	version, vok := cbm["executableVersion"].(string)
	adapter, aok := cbm["adapterSchema"].(string)

	if !vok || !aok {
		return LockCBM{}, false
	}

	sum, sumOK := parseCBMDigest(cbm)
	if !sumOK {
		return LockCBM{}, false
	}

	parsed := LockCBM{
		ExecutableVersion: version, AdapterSchema: adapter, SHA256: sum,
		NormalizationVersion: str(cbm, "normalizationVersion", ""),
	}

	if profile, ok := cbm["profile"].(object); ok {
		parsed.Profile = &LockProfile{ID: str(profile, "id", ""), Digest: str(profile, "digest", "")}
	}

	return parsed, true
}

// parseCBMDigest reads the optional `sha256` of the CBM entry. A value that is not `sha256:` and 64 lower-case hex
// digits is not valid.
func parseCBMDigest(cbm object) (string, bool) {
	raw, present := cbm["sha256"]
	if !present {
		return "", true
	}

	sum, isText := raw.(string)

	return sum, isText && cbmDigestPattern.MatchString(sum)
}

// parseLockResources reads the `resources` list.
func parseLockResources(list []any) ([]LockResource, bool) {
	resources := make([]LockResource, 0, len(list))

	for _, item := range list {
		entry, ok := item.(object)
		if !ok {
			return nil, false
		}

		resources = append(resources, LockResource{URI: str(entry, "uri", ""), Path: str(entry, "path", ""), SHA256: str(entry, "sha256", "")})
	}

	return resources, true
}

// lockRenderer reads the pin of one renderer. nil when the lock has none.
func lockRenderer(renderers object, tool string) *LockRenderer {
	entry, ok := renderers[tool].(object)
	if !ok {
		return nil
	}

	return &LockRenderer{Version: str(entry, "version", ""), SHA256: str(entry, "sha256", "")}
}

// parseLockPacks reads the optional `packs` list. A missing key is no packs.
func parseLockPacks(raw any) ([]LockPack, bool) {
	if raw == nil {
		return nil, true
	}

	list, ok := raw.([]any)
	if !ok {
		return nil, false
	}

	packs := make([]LockPack, 0, len(list))

	for _, item := range list {
		entry, ok := item.(object)
		if !ok {
			return nil, false
		}

		packs = append(packs, LockPack{
			ID: str(entry, "id", ""), Version: str(entry, "version", ""), Source: str(entry, "source", ""), SHA256: str(entry, "sha256", ""),
		})
	}

	return packs, true
}

func sortPacks(packs []LockPack) {
	slices.SortStableFunc(packs, func(a, b LockPack) int { return canonjson.CompareUTF16(a.Source, b.Source) })
}

// WriteLock writes the lockfile atomically, with resources sorted by URI and
// packs sorted by source. The `packs` key is written only when there is a pack.
func WriteLock(path string, lock *Lockfile) error {
	ordered := Lockfile{
		LockVersion: lockVersion,
		Resolved:    lock.Resolved,
		Resources:   slices.Clone(lock.Resources),
		Packs:       slices.Clone(lock.Packs),
	}

	if ordered.Resources == nil {
		ordered.Resources = []LockResource{}
	}

	slices.SortStableFunc(ordered.Resources, func(a, b LockResource) int { return canonjson.CompareUTF16(a.URI, b.URI) })
	sortPacks(ordered.Packs)

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)

	if err := enc.Encode(ordered); err != nil {
		return fmt.Errorf("encode lockfile: %w", err)
	}

	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode lockfile: %w", err)
	}

	return fsutil.WriteFileAtomic(path, buf.Bytes())
}

// NewLock makes a lock for the found toolchain. An empty cbmVersion means CBM
// is missing; an empty structurizrVersion leaves the Structurizr entry out. A renderer that was not found is
// left out; the others are pinned.
func NewLock(cbmVersion, structurizrVersion string, renderers ...RendererPin) *Lockfile {
	if cbmVersion == "" {
		cbmVersion = unknownVersion
	}

	result := &Lockfile{
		LockVersion: lockVersion,
		Resolved: LockResolved{
			Calm: LockCalm{SchemaVersion: DefaultCalmSchemaVersion},
			CBM:  LockCBM{ExecutableVersion: cbmVersion, AdapterSchema: AdapterSchema},
		},
		Resources: []LockResource{},
	}

	if structurizrVersion != "" {
		result.Resolved.Structurizr = &LockStructurizr{CLIVersion: structurizrVersion}
	}

	for _, renderer := range renderers {
		if renderer.Found() {
			result.SetRenderer(renderer)
		}
	}

	return result
}

// Renderer gives the pin of a renderer tool, or nil when the lock has none.
func (l *Lockfile) Renderer(tool string) *LockRenderer {
	if l == nil || l.Resolved.Renderers == nil {
		return nil
	}

	switch tool {
	case RenderToolHelm:
		return l.Resolved.Renderers.Helm
	case RenderToolKustomize:
		return l.Resolved.Renderers.Kustomize
	}

	return nil
}

// SetRenderer pins a renderer, or replaces its pin. A tool other than helm and kustomize is ignored.
func (l *Lockfile) SetRenderer(renderer RendererPin) {
	pin := renderer.Pin()

	if l.Resolved.Renderers == nil {
		l.Resolved.Renderers = &LockRenderers{}
	}

	switch renderer.Tool {
	case RenderToolHelm:
		l.Resolved.Renderers.Helm = &pin
	case RenderToolKustomize:
		l.Resolved.Renderers.Kustomize = &pin
	}

	l.dropEmptyRenderers()
}

// DropRenderer removes the pin of a renderer tool.
func (l *Lockfile) DropRenderer(tool string) {
	if l.Resolved.Renderers == nil {
		return
	}

	switch tool {
	case RenderToolHelm:
		l.Resolved.Renderers.Helm = nil
	case RenderToolKustomize:
		l.Resolved.Renderers.Kustomize = nil
	}

	l.dropEmptyRenderers()
}

// dropEmptyRenderers makes the `renderers` key disappear when no tool is pinned.
func (l *Lockfile) dropEmptyRenderers() {
	if r := l.Resolved.Renderers; r != nil && r.Helm == nil && r.Kustomize == nil {
		l.Resolved.Renderers = nil
	}
}

// CheckRenderer compares the renderer found on this machine with its pin in the lock. It gives nil when they
// agree, else a CO-PRV-003 error (exit 5) that names the expected and the found identity: the renderer is missing,
// has no pin (a lock may be nil), or differs from its pin. An unpinned render would not be reproducible, so there is
// no trust-on-first-use; `lock update` writes the pin.
func CheckRenderer(lock *Lockfile, found RendererPin) *diag.Error {
	pin := lock.Renderer(found.Tool)

	var detail string

	switch {
	case !found.Found():
		detail = found.Tool + " was not found"
	case pin == nil:
		detail = found.Tool + " is not pinned in " + LockFileName + "; run `concordia lock update`"
	case *pin != found.Pin():
		detail = fmt.Sprintf("%s differs from its pin: lock has %s (%s), found %s (%s); if you trust the new build, run `concordia lock update`",
			found.Tool, pin.Version, pin.SHA256, found.Version, found.SHA256)
	default:
		return nil
	}

	return diag.Fail(diag.PRV003, diag.Detail(detail), diag.WithSource(diag.Source{URI: LockFileName}))
}

// ProfileMismatch says in words how the capability profile of a lock differs
// from the profile of the found build. It gives "" when the lock records no
// profile (an older lock; nothing was promised) or when both are equal.
func ProfileMismatch(locked, found *LockProfile) string {
	switch {
	case locked == nil:
		return ""
	case found == nil:
		return fmt.Sprintf("lock has capability profile %s (%s), found no profile", locked.ID, locked.Digest)
	case *locked != *found:
		return fmt.Sprintf("lock has capability profile %s (%s), found %s (%s)", locked.ID, locked.Digest, found.ID, found.Digest)
	}

	return ""
}

func lockDiag(detail string) diag.Diagnostic {
	return diag.New(diag.INT012, diag.Detail(detail), diag.WithSource(diag.Source{URI: LockFileName}))
}

// VerifyLock checks a lock against the toolchain, the url-mapped resources and
// the policy packs of this run.
//
//   - CBM version differs from the lock: CO-WRK-003 warning.
//   - Resource digest differs from the lock, or the file cannot be read: CO-INT-012 (error, exit 6).
//   - Resource not in the lock and Offline: CO-INT-012 (error, exit 6).
//   - Resource not in the lock and not Offline: add it and write the lock (atomic).
//   - Pack digest differs from the lock: CO-INT-012 (error, exit 6), as for a resource.
//   - Pack not in the lock: add it and write the lock, also with Offline (a pack is a local file: no network is used).
//
// lock may be nil (no lockfile). Then a new lock is made only when resources
// or packs need locking. It gives the (possibly updated) lock and the warnings. The
// input lock is not changed.
func VerifyLock(lock *Lockfile, ctx *VerifyContext) (*Lockfile, []diag.Diagnostic, error) {
	warnings := lockWarnings(lock, ctx)
	current := cloneLock(lock)

	var (
		failures []diag.Diagnostic
		changed  bool
	)

	resources := slices.Clone(ctx.Resources)
	slices.SortStableFunc(resources, func(a, b ResourceRef) int { return canonjson.CompareUTF16(a.URI, b.URI) })

	for _, res := range resources {
		failure, add := verifyResource(current, res, ctx.Root, ctx.Offline)

		switch {
		case failure != nil:
			failures = append(failures, *failure)
		case add != nil:
			current = lockOrNew(current, ctx)
			current.Resources = append(current.Resources, *add)
			changed = true
		}
	}

	for _, pack := range ctx.Packs {
		failure, add := verifyPack(current, pack)

		switch {
		case failure != nil:
			failures = append(failures, *failure)
		case add != nil:
			current = lockOrNew(current, ctx)
			current.Packs = append(current.Packs, *add)
			changed = true
		}
	}

	if err := failuresError(failures); err != nil {
		return nil, nil, err
	}

	if err := verifyRenderers(current, ctx.Renderers); err != nil {
		return nil, nil, err
	}

	if changed {
		if err := WriteLock(ctx.LockPath, current); err != nil {
			return nil, nil, err
		}
	}

	return current, warnings, nil
}

// verifyRenderers checks every renderer that this run uses against its pin. The first failure in tool order is the
// error, so the result does not depend on the order of the input.
func verifyRenderers(current *Lockfile, renderers []RendererPin) error {
	sorted := slices.Clone(renderers)
	slices.SortStableFunc(sorted, func(a, b RendererPin) int { return canonjson.CompareUTF16(a.Tool, b.Tool) })

	for _, renderer := range sorted {
		if err := CheckRenderer(current, renderer); err != nil {
			return err
		}
	}

	return nil
}

// lockWarnings gives the CO-WRK-003 warnings for the toolchain and CALM schema of this run that differ from the lock.
// It gives none when there is no lock.
func lockWarnings(lock *Lockfile, ctx *VerifyContext) []diag.Diagnostic {
	warnings := []diag.Diagnostic{}
	if lock == nil {
		return warnings
	}

	warnings = append(warnings, cbmWarnings(lock, ctx)...)

	if ctx.CalmSchemaVersion != "" && lock.Resolved.Calm.SchemaVersion != ctx.CalmSchemaVersion {
		warnings = append(warnings, diag.New(diag.WRK003,
			diag.Detail(fmt.Sprintf("lock has calm schema %s, architecture uses %s", lock.Resolved.Calm.SchemaVersion, ctx.CalmSchemaVersion)),
			diag.WithSource(diag.Source{URI: LockFileName})))
	}

	return warnings
}

// cbmWarnings gives the CO-WRK-003 warnings for the CBM build that differ from the lock: the version, the capability
// profile, and the executable digest. A lock without `sha256` promises nothing about the executable, so it gives no
// warning. It gives none when CBM is missing.
func cbmWarnings(lock *Lockfile, ctx *VerifyContext) []diag.Diagnostic {
	if ctx.CBMVersion == "" {
		return nil
	}

	var warnings []diag.Diagnostic

	warn := func(detail string) {
		warnings = append(warnings, diag.New(diag.WRK003, diag.Detail(detail), diag.WithSource(diag.Source{URI: LockFileName})))
	}

	if lock.Resolved.CBM.ExecutableVersion != ctx.CBMVersion {
		warn(fmt.Sprintf("CBM %s found, lock has %s", ctx.CBMVersion, lock.Resolved.CBM.ExecutableVersion))
	}

	if detail := ProfileMismatch(lock.Resolved.CBM.Profile, ctx.CBMProfile); detail != "" {
		warn(detail)
	}

	if CBMDigestDiffers(lock.Resolved.CBM.SHA256, ctx.CBMSHA256) {
		warn(fmt.Sprintf("CBM executable %s has digest %s, lock has %s; run `concordia lock update` to pin it", ctx.CBMExecutable, ctx.CBMSHA256, lock.Resolved.CBM.SHA256))
	}

	return warnings
}

// CBMDigestDiffers says whether the digest of the found CBM executable differs from the digest in the lock. A lock
// without a digest promised nothing, and an executable without a digest is not known: neither is a difference.
func CBMDigestDiffers(locked, found string) bool {
	return locked != "" && found != "" && locked != found
}

// lockOrNew gives current, or a new lock for this run when current is nil.
func lockOrNew(current *Lockfile, ctx *VerifyContext) *Lockfile {
	if current != nil {
		return current
	}

	fresh := NewLock(ctx.CBMVersion, "")
	if ctx.CBMProfile != nil {
		profile := *ctx.CBMProfile
		fresh.Resolved.CBM.Profile = &profile
	}

	fresh.Resolved.CBM.SHA256 = ctx.CBMSHA256

	if ctx.CalmSchemaVersion != "" {
		fresh.Resolved.Calm.SchemaVersion = ctx.CalmSchemaVersion
	}

	return fresh
}

// failuresError makes the CO-INT-012 error of the failures: the first is the error, the others are its causes.
// It gives nil when there is no failure.
func failuresError(failures []diag.Diagnostic) error {
	if len(failures) == 0 {
		return nil
	}

	first := failures[0]
	if len(failures) > 1 {
		first.Cause = failures[1:]
	}

	return &diag.Error{Diagnostic: first, ExitCode: diag.Network}
}

// verifyResource checks one resource against the lock. It gives a failure, or
// the entry to add to the lock (both nil when the lock has the resource with
// the right digest).
func verifyResource(current *Lockfile, res ResourceRef, root string, offline bool) (failure *diag.Diagnostic, add *LockResource) {
	uri := SanitizeURI(res.URI)

	rel, err := fsutil.ToPosixRelative(root, res.AbsPath)
	if err != nil {
		rel = fsutil.ToPosix(res.AbsPath)
	}

	fail := func(detail string) *diag.Diagnostic {
		d := lockDiag(detail)

		return &d
	}

	sum, err := digest.OfFile(res.AbsPath)
	if err != nil {
		return fail(fmt.Sprintf("mapped file for %s cannot be read: %s", uri, rel)), nil
	}

	if current != nil {
		if i := slices.IndexFunc(current.Resources, func(r LockResource) bool { return r.URI == uri }); i >= 0 {
			if entry := current.Resources[i]; entry.SHA256 != sum {
				return fail(fmt.Sprintf("digest of %s (%s) does not agree with the lock: %s != %s", uri, rel, sum, entry.SHA256)), nil
			}

			return nil, nil
		}
	}

	if offline {
		return fail(uri + " is not in the lock and --offline is set"), nil
	}

	return nil, &LockResource{URI: uri, Path: rel, SHA256: sum}
}

// verifyPack checks one policy pack against the lock. It gives a failure, or
// the entry to add to the lock (both nil when the lock has the pack with the
// right digest).
func verifyPack(current *Lockfile, pack PackRef) (failure *diag.Diagnostic, add *LockPack) {
	if current != nil {
		if i := slices.IndexFunc(current.Packs, func(p LockPack) bool { return p.Source == pack.Source }); i >= 0 {
			if entry := current.Packs[i]; entry.SHA256 != pack.SHA256 {
				d := lockDiag(fmt.Sprintf("digest of policy pack %s does not agree with the lock: %s != %s", pack.Source, pack.SHA256, entry.SHA256))
				d.Suggestion = "review the pack change; if you trust it, remove the pack entry from " + LockFileName + " and run again"

				return &d, nil
			}

			return nil, nil
		}
	}

	return nil, &LockPack{ID: pack.ID, Version: pack.Version, Source: pack.Source, SHA256: pack.SHA256}
}

// SetPack adds a pack to the lock, or replaces the entry with the same source.
func (l *Lockfile) SetPack(pack LockPack) {
	if i := slices.IndexFunc(l.Packs, func(p LockPack) bool { return p.Source == pack.Source }); i >= 0 {
		l.Packs[i] = pack

		return
	}

	l.Packs = append(l.Packs, pack)
}

func cloneLock(src *Lockfile) *Lockfile {
	if src == nil {
		return nil
	}

	dup := *src
	dup.Resources = slices.Clone(src.Resources)
	dup.Packs = slices.Clone(src.Packs)

	if src.Resolved.CBM.Profile != nil {
		profile := *src.Resolved.CBM.Profile
		dup.Resolved.CBM.Profile = &profile
	}

	if src.Resolved.Structurizr != nil {
		structurizr := *src.Resolved.Structurizr
		dup.Resolved.Structurizr = &structurizr
	}

	if src.Resolved.Renderers != nil {
		renderers := LockRenderers{}

		if helm := src.Resolved.Renderers.Helm; helm != nil {
			pin := *helm
			renderers.Helm = &pin
		}

		if kustomize := src.Resolved.Renderers.Kustomize; kustomize != nil {
			pin := *kustomize
			renderers.Kustomize = &pin
		}

		dup.Resolved.Renderers = &renderers
	}

	return &dup
}
