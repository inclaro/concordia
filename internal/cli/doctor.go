package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/cbmpin"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/procexec"
	"github.com/inclaro/concordia/internal/projection"
	"github.com/inclaro/concordia/internal/remote"
	"github.com/inclaro/concordia/internal/workspace"
)

type capabilityStatus string

const (
	statusAvailable    capabilityStatus = "available"
	statusDegraded     capabilityStatus = "degraded"
	statusMissing      capabilityStatus = "missing"
	statusIncompatible capabilityStatus = "incompatible"

	probeTimeout   = 15 * time.Second
	ownerWritable  = 0o200
	namePad        = 15
	statusPad      = 12
	capWorkspace   = "workspace"
	capCbm         = "cbm"
	capGit         = "git"
	capLock        = "lock"
	capCalmSchema  = "calm-schema"
	capJava        = "java"
	capStructurizr = "structurizr"
	capCosign      = "cosign"
	capHelm        = workspace.RenderToolHelm
	capKustomize   = workspace.RenderToolKustomize
	capNetwork     = "network-policy"
)

// requiredCapabilities make `doctor` fail (exit code 5) when they are missing or incompatible.
var requiredCapabilities = []string{capWorkspace, capCalmSchema, capCbm}

var (
	gitVersionPattern    = regexp.MustCompile(`(\d+\.\d+(\.\d+)?)`)
	javaVersionPattern   = regexp.MustCompile(`version "?([\d._]+)`)
	cosignVersionPattern = regexp.MustCompile(`GitVersion:\s+v?(\S+)`)
)

type capability struct {
	// Profile is the capability profile of the cbm capability.
	Profile *capabilityProfile `json:"profile,omitempty"`
	Name    string             `json:"name"`
	Status  capabilityStatus   `json:"status"`
	Version string             `json:"version,omitempty"`
	Detail  string             `json:"detail,omitempty"`
	// Path is the resolved path of the executable of the cbm capability.
	Path string `json:"path,omitempty"`
	// SHA256 is `sha256:<hex>` of the executable file of the cbm capability.
	SHA256 string `json:"sha256,omitempty"`
	// Required is true for an optional capability that the workspace needs: a declared rendering source runs the
	// renderer. A required capability that is missing or incompatible makes `doctor` fail, as the fixed ones do.
	Required bool `json:"required,omitempty"`
}

// capabilityProfile is the `profile` object of the cbm capability: the
// capability profile that applies to the build, with the support of each family.
type capabilityProfile struct {
	ID       string          `json:"id,omitempty"`
	Digest   string          `json:"digest,omitempty"`
	Families []profileFamily `json:"families"`
	// Applies is false when no profile is pinned to the build; the families
	// are then the no-profile families (code only).
	Applies bool `json:"applies"`
}

type profileFamily struct {
	Family    string   `json:"family"`
	Level     string   `json:"level"`
	Maturity  string   `json:"maturity"`
	LossModes []string `json:"lossModes"`
}

func newCapabilityProfile(profile *observation.CapabilityProfile) *capabilityProfile {
	out := &capabilityProfile{Families: []profileFamily{}}

	var states map[string]observation.FamilyState

	if profile != nil {
		out.Applies, out.ID, out.Digest = true, profile.ID, profile.Digest
		states = observation.DeclaredFamilies(profile)
	} else {
		states = observation.NoProfileFamilies()
	}

	for _, name := range slices.Sorted(maps.Keys(states)) {
		st := states[name]
		out.Families = append(out.Families, profileFamily{
			Family: name, Level: string(st.Level), Maturity: string(st.Maturity), LossModes: nonNilStrings(st.LossModes),
		})
	}

	return out
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}

	return in
}

// summary gives the profile line of the text output: identifier, digest and the family counts.
func (p *capabilityProfile) summary() string {
	counts := map[string]int{}
	for _, f := range p.Families {
		counts[f.Level]++
	}

	head := "no capability profile for this build"
	if p.Applies {
		head = "profile " + p.ID + " " + p.Digest
	}

	return fmt.Sprintf("%s (%d supported, %d partial, %d unsupported)", head, counts["supported"], counts["partial"], counts["unsupported"])
}

type doctorOptions struct {
	json   bool
	strict bool
}

func newDoctorCmd(host *Host) *cobra.Command {
	var opts doctorOptions

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the toolchain: workspace, CALM schemas, CBM, git, optional Java, Structurizr and cosign",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return doctorHandler(ctx, app, opts)
		}),
	}

	cmd.Flags().BoolVar(&opts.json, "json", false, "machine-readable output")
	cmd.Flags().BoolVar(&opts.strict, "strict", false, "degraded capabilities also give exit code 5")

	return cmd
}

func probe(ctx context.Context, app *Context, cmd string, args ...string) *ProbeResult {
	if app.Deps.Probe != nil {
		return app.Deps.Probe(ctx, cmd, args)
	}

	res, err := procexec.Run(ctx, cmd, args, procexec.Options{Timeout: probeTimeout})
	if err != nil || res.TimedOut {
		return nil
	}

	return &ProbeResult{Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr}
}

func firstLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

// writable says whether the owner can write to a directory.
func writable(dir string) bool {
	info, err := os.Stat(dir)

	return err == nil && info.IsDir() && info.Mode().Perm()&ownerWritable != 0
}

// message gives the message of a diagnostic error, or the text of another error.
func message(err error) string {
	var failure *diag.Error
	if errors.As(err, &failure) {
		return failure.Diagnostic.Message
	}

	return err.Error()
}

func checkWorkspace(wsp *workspace.Workspace, loadErr error) capability {
	if wsp == nil {
		detail := "workspace not loaded"
		if loadErr != nil {
			detail = message(loadErr)
		}

		return capability{Name: capWorkspace, Status: statusMissing, Detail: detail}
	}

	if writable(filepath.Join(wsp.Root, "build")) || writable(wsp.Root) {
		return capability{Name: capWorkspace, Status: statusAvailable, Detail: wsp.Path}
	}

	return capability{Name: capWorkspace, Status: statusDegraded, Detail: "build/ is not writable"}
}

type projectEntry struct {
	Name string `json:"name"`
}

// projectsPage is the page size of `list_projects` (the CBM default).
const projectsPage = 50

type listProjects struct {
	Projects []projectEntry `json:"projects"`
	HasMore  bool           `json:"has_more"` //nolint:tagliatelle // the field name of CBM.
}

// listAllProjects reads all pages of `list_projects`.
func listAllProjects(ctx context.Context, client observation.Client) ([]projectEntry, error) {
	var all []projectEntry

	for {
		page, err := observation.CallAs[listProjects](ctx, client, "list_projects", map[string]any{
			observation.ArgFormat: observation.FormatJSON,
			"limit":               projectsPage,
			"offset":              len(all),
		})
		if err != nil {
			return nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
		}

		all = append(all, page.Projects...)

		if !page.HasMore || len(page.Projects) == 0 {
			return all, nil
		}
	}
}

// cbmUnavailable gives the capability for a CBM whose version could not be read.
func cbmUnavailable(err error) capability {
	status := statusMissing

	var failure *diag.Error
	if errors.As(err, &failure) && failure.Diagnostic.Code == diag.OBS004 {
		status = statusIncompatible
	}

	return capability{Name: capCbm, Status: status, Detail: message(err)}
}

// unindexedProject gives the first project of the workspace that CBM has not indexed, or "".
func unindexedProject(wsp *workspace.Workspace, projects []projectEntry) string {
	if wsp == nil {
		return ""
	}

	for _, wp := range wsp.Observation.Projects {
		name := wp.Name

		if !slices.ContainsFunc(projects, func(p projectEntry) bool { return p.Name == name }) {
			return name
		}
	}

	return ""
}

// checkCbm checks the CBM executable. The capability also names the file that was found: its resolved path and its
// sha256, as `lock update` pins them. A client that has no executable (a test fake) gives neither. A file whose
// sha256 is not the pinned build is `degraded`: Concordia is not tested with it.
func checkCbm(ctx context.Context, client observation.Client, wsp *workspace.Workspace, pin *cbmpin.Pin) capability {
	item := checkCbmBuild(ctx, client, wsp)
	if item.Version == "" {
		return item
	}

	identity, err := observation.IdentifyExecutable(client)
	if err != nil {
		if item.Status == statusAvailable {
			item.Status, item.Detail = statusDegraded, "cannot identify the executable: "+message(err)
		}

		return item
	}

	item.Path, item.SHA256 = identity.Path, identity.SHA256
	markTestedBuild(&item, pin)

	return item
}

func checkCbmBuild(ctx context.Context, client observation.Client, wsp *workspace.Workspace) capability {
	version, err := client.Version(ctx)
	if err != nil {
		return cbmUnavailable(err)
	}

	assessment, _, err := observation.AssessBuild(version)
	if err != nil {
		return capability{Name: capCbm, Status: statusIncompatible, Version: version, Detail: message(err)}
	}

	projects, err := listAllProjects(ctx, client)
	if err != nil {
		return capability{Name: capCbm, Status: statusDegraded, Version: version, Detail: "list_projects failed: " + message(err)}
	}

	if name := unindexedProject(wsp, projects); name != "" {
		return capability{Name: capCbm, Status: statusDegraded, Version: version, Detail: fmt.Sprintf("project '%s' is not indexed; run concordia index", name)}
	}

	profile := newCapabilityProfile(assessment.Profile)

	if assessment.Support == observation.VersionDegraded {
		return capability{Name: capCbm, Status: statusDegraded, Version: version, Detail: "CBM version is newer than the tested 0.12.x; no capability profile", Profile: profile}
	}

	detail := "no capability profile for this build; code families only"
	if assessment.Profile != nil {
		detail = "profile " + assessment.Profile.ID
	}

	return capability{Name: capCbm, Status: statusAvailable, Version: version, Detail: detail, Profile: profile}
}

// lockProfileMismatch gives the mismatch between the profile of the lock and the cbm profile, or "".
func lockProfileMismatch(lock *workspace.Lockfile, cbm *capability) string {
	if cbm.Profile != nil && cbm.Profile.Applies {
		found := &workspace.LockProfile{ID: cbm.Profile.ID, Digest: cbm.Profile.Digest}

		return workspace.ProfileMismatch(lock.Resolved.CBM.Profile, found)
	}

	if cbm.Version == "" {
		return ""
	}

	return workspace.ProfileMismatch(lock.Resolved.CBM.Profile, nil)
}

// lockMissingResources counts the locked resources of the cache that are not in it.
func lockMissingResources(root string, lock *workspace.Lockfile) int {
	missing := 0

	for _, res := range lock.Resources {
		if !strings.HasPrefix(res.Path, remote.CacheDir) {
			continue
		}

		cached, ok := remote.CachePath(root, res.SHA256)
		if !ok {
			missing++

			continue
		}

		if _, statErr := os.Stat(cached); statErr != nil {
			missing++
		}
	}

	return missing
}

// lockDegradedDetail gives why the lock does not match what the checks found, or "".
func lockDegradedDetail(wsp *workspace.Workspace, lock *workspace.Lockfile, cbm *capability, renderers []rendererState) string {
	if cbm.Version != "" && lock.Resolved.CBM.ExecutableVersion != cbm.Version {
		return fmt.Sprintf("lock has cbm %s, found %s", lock.Resolved.CBM.ExecutableVersion, cbm.Version)
	}

	if workspace.CBMDigestDiffers(lock.Resolved.CBM.SHA256, cbm.SHA256) {
		return fmt.Sprintf("lock has cbm sha256 %s, found %s (%s)", lock.Resolved.CBM.SHA256, cbm.SHA256, cbm.Path)
	}

	if detail := lockProfileMismatch(lock, cbm); detail != "" {
		return detail
	}

	if detail := rendererPinMismatch(lock, renderers); detail != "" {
		return detail
	}

	if missing := lockMissingResources(wsp.Root, lock); missing > 0 {
		return fmt.Sprintf("%d locked resource(s) not in the cache", missing)
	}

	return ""
}

func checkLock(wsp *workspace.Workspace, cbm *capability, renderers []rendererState) capability {
	if wsp == nil {
		return capability{Name: capLock, Status: statusDegraded, Detail: "no workspace"}
	}

	lock, found, err := workspace.ReadLock(workspace.LockPathFor(wsp.Root))
	if err != nil {
		return capability{Name: capLock, Status: statusIncompatible, Detail: message(err)}
	}

	if !found {
		return capability{Name: capLock, Status: statusDegraded, Detail: workspace.LockFileName + " is missing"}
	}

	if detail := lockDegradedDetail(wsp, lock, cbm, renderers); detail != "" {
		return capability{Name: capLock, Status: statusDegraded, Detail: detail}
	}

	return capability{Name: capLock, Status: statusAvailable}
}

func checkGit(ctx context.Context, app *Context) capability {
	res := probe(ctx, app, "git", "--version")
	if res == nil || res.Code != 0 {
		return capability{Name: capGit, Status: statusMissing, Detail: "optional: git not found; directories are read as plain directories"}
	}

	version := gitVersionPattern.FindString(res.Stdout)
	if version == "" {
		version = firstLine(res.Stdout)
	}

	return capability{Name: capGit, Status: statusAvailable, Version: version}
}

func checkJava(ctx context.Context, app *Context) capability {
	res := probe(ctx, app, "java", "-version")
	if res == nil || res.Code != 0 {
		return capability{Name: capJava, Status: statusMissing, Detail: "optional: java not found"}
	}

	version := statusUnknown
	if m := javaVersionPattern.FindStringSubmatch(res.Stderr + "\n" + res.Stdout); m != nil {
		version = m[1]
	}

	return capability{Name: capJava, Status: statusAvailable, Version: version}
}

// structurizrCapability gives the capability for a Structurizr CLI that answered with this version output.
func structurizrCapability(output string) capability {
	version := projection.ParseCLIVersion(output)
	if version == "" {
		version = "unknown"
	}

	if version != projection.PinnedStructurizrCLI {
		return capability{
			Name: capStructurizr, Status: statusDegraded, Version: version,
			Detail: "warning: committed view exports need Structurizr CLI " + projection.PinnedStructurizrCLI + "; their freshness is unknown with this version",
		}
	}

	return capability{Name: capStructurizr, Status: statusAvailable, Version: version}
}

func checkStructurizr(ctx context.Context, app *Context) capability {
	candidates := []string{app.IO.Getenv("STRUCTURIZR_CLI"), "structurizr-cli", "structurizr.sh", "structurizr"}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}

		if res := probe(ctx, app, candidate, "version"); res != nil && res.Code == 0 {
			return structurizrCapability(res.Stdout)
		}
	}

	return capability{Name: capStructurizr, Status: statusMissing, Detail: "optional: Structurizr CLI not found"}
}

func checkCosign(ctx context.Context, app *Context) capability {
	candidate := app.IO.Getenv("COSIGN")
	if candidate == "" {
		candidate = "cosign"
	}

	if res := probe(ctx, app, candidate, "version"); res != nil && res.Code == 0 {
		version := "unknown"
		if m := cosignVersionPattern.FindStringSubmatch(res.Stdout + "\n" + res.Stderr); m != nil {
			version = m[1]
		}

		return capability{Name: capCosign, Status: statusAvailable, Version: version}
	}

	return capability{Name: capCosign, Status: statusMissing, Detail: "optional: cosign not found"}
}

func collectCapabilities(ctx context.Context, app *Context) []capability {
	wsp, loadErr := app.LoadWorkspace()
	if loadErr != nil {
		wsp = nil
	}

	cbm := checkCbm(ctx, app.NewClient(wsp), wsp, app.cbmPin())
	renderers := probeRenderers(ctx, app, wsp)

	network := "local-only"
	if app.Opts.Offline {
		network = "offline"
	}

	caps := []capability{
		checkWorkspace(wsp, loadErr),
		{Name: capCalmSchema, Status: statusAvailable, Version: strings.Join(workspace.CalmSchemaVersions, ",")},
		cbm,
		checkGit(ctx, app),
		checkJava(ctx, app),
		checkStructurizr(ctx, app),
		checkCosign(ctx, app),
	}

	for i := range renderers {
		caps = append(caps, renderers[i].capability())
	}

	return append(caps,
		capability{Name: capNetwork, Status: statusAvailable, Detail: network},
		checkLock(wsp, &cbm, renderers),
	)
}

// isMissingRequired says whether a capability is required and missing or incompatible.
func isMissingRequired(item *capability) bool {
	required := slices.Contains(requiredCapabilities, item.Name) || item.Required

	return required && (item.Status == statusMissing || item.Status == statusIncompatible)
}

func doctorExitCode(caps []capability, strict bool) int {
	for i := range caps {
		if isMissingRequired(&caps[i]) {
			return diag.Toolchain
		}
	}

	if strict && slices.ContainsFunc(caps, func(item capability) bool { return item.Status == statusDegraded }) {
		return diag.Toolchain
	}

	return diag.OK
}

func prettyCapabilities(caps []capability) string {
	var out strings.Builder

	for _, item := range caps {
		var parts []string

		for _, p := range []string{item.Version, item.Detail} {
			if p != "" {
				parts = append(parts, p)
			}
		}

		line := fmt.Sprintf("%-*s %-*s %s", namePad, item.Name, statusPad, item.Status, strings.Join(parts, " - "))
		out.WriteString(strings.TrimRight(line, " "))
		out.WriteString("\n")

		if item.SHA256 != "" {
			out.WriteString("  executable " + item.Path + " " + item.SHA256 + "\n")
		}

		if item.Profile != nil {
			out.WriteString(prettyProfile(item.Profile))
		}
	}

	return out.String()
}

// prettyProfile gives the detail lines under the cbm capability: the profile
// and the loss modes of each family that has some.
func prettyProfile(p *capabilityProfile) string {
	var out strings.Builder

	out.WriteString("  " + p.summary() + "\n")

	for _, f := range p.Families {
		if len(f.LossModes) > 0 {
			fmt.Fprintf(&out, "  %s (%s, %s): %s\n", f.Family, f.Level, f.Maturity, strings.Join(f.LossModes, ", "))
		}
	}

	return out.String()
}

func doctorHandler(ctx context.Context, app *Context, opts doctorOptions) (int, error) {
	caps := collectCapabilities(ctx, app)

	text := prettyCapabilities(caps) + prettyContracts(claims.Default())

	if opts.json || app.Opts.Format == modelFormatJSON {
		data, err := canonjson.Marshal(map[string]any{"capabilities": caps, "claims": newClaimRegistryView(claims.Default())})
		if err != nil {
			return diag.Internal, fmt.Errorf("encode capabilities: %w", err)
		}

		text = string(data)
	}

	if err := app.WriteOutput(text, ""); err != nil {
		return diag.Internal, err
	}

	return doctorExitCode(caps, opts.strict), nil
}
