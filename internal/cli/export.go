package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/inclaro/concordia/internal/bundle"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/cosign"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	snippetsNone       = "none"
	snippetsViolations = "violations"
	defaultBundlePath  = "build/concordia/conformance.tar.gz"
	snippetNumberWidth = 4
	graphEvidencePath  = "graph/evidence.json"
)

type exportBundleOptions struct {
	snippets     string
	signKey      string
	includeGraph bool
	sign         bool
}

func newExportCmd(host *Host) *cobra.Command {
	export := &cobra.Command{Use: "export", Short: "Export conformance results"}
	opts := exportBundleOptions{snippets: snippetsNone}

	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Write a conformance bundle (results, inputs, coverage, optional graph) as a tar.gz archive",
		Args:  cobra.NoArgs,
		RunE: host.Action(func(ctx context.Context, app *Context, _ []string) (int, error) {
			return diag.OK, exportBundle(ctx, app, &opts)
		}),
	}
	cmd.Flags().BoolVar(&opts.includeGraph, "include-graph", false, "add the observed nodes and edges of the run")
	addChoiceFlag(cmd, false, &opts.snippets, "include-source-snippets", "add source lines around live violations",
		snippetsNone, snippetsViolations)
	cmd.Flags().BoolVar(&opts.sign, "sign", false, "sign the bundle with cosign")
	cmd.Flags().StringVar(&opts.signKey, "sign-key", "", "cosign key reference (default: keyless signing)")

	export.AddCommand(cmd, newExportVerifyCmd(host))

	return export
}

func exportBundle(ctx context.Context, app *Context, opts *exportBundleOptions) error {
	cosignExe, err := exportSigner(app, opts)
	if err != nil {
		return err
	}

	wsp, run, err := exportCheckedRun(ctx, app)
	if err != nil {
		return err
	}

	job := &exportJob{app: app, wsp: wsp, run: run, opts: opts}

	archive, err := exportArchive(ctx, job)
	if err != nil {
		return err
	}

	out, err := exportWriteArchive(app, wsp, archive)
	if err != nil {
		return err
	}

	return exportSign(ctx, job, cosignExe, out)
}

// exportSign signs the written bundle with cosign when the options ask for it, and reports the signature path.
func exportSign(ctx context.Context, job *exportJob, cosignExe, out string) error {
	if !job.opts.sign {
		return nil
	}

	sig, err := cosign.SignBlob(ctx, cosignExe, out, job.opts.signKey)
	if err != nil {
		return err
	}

	job.app.Out(sig + "\n")

	return nil
}

// exportSigner gives the cosign executable when the bundle is to be signed, and the empty string otherwise.
func exportSigner(app *Context, opts *exportBundleOptions) (string, error) {
	if !opts.sign {
		return "", nil
	}

	// --offline decides before cosign is looked for, so a missing cosign never hides it.
	if app.Opts.Offline {
		return "", diag.Fail(diag.WRK016, diag.Detail(offlineSigning))
	}

	return cosignPath(app)
}

// cosignPath looks for the cosign executable in the environment of the command.
func cosignPath(app *Context) (string, error) {
	exe, err := cosign.Path(app.IO.Getenv)
	if err != nil {
		return "", err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return exe, nil
}

// exportCheckedRun loads the workspace, refuses it when its results are of an incomplete run, and runs the check that
// the bundle reports, keeping the evidence of the run.
func exportCheckedRun(ctx context.Context, app *Context) (*workspace.Workspace, *conformance.Run, error) {
	wsp, err := app.LoadWorkspace()
	if err != nil {
		return nil, nil, err
	}

	if err := requireCompleteResults(wsp); err != nil {
		return nil, nil, err
	}

	run, err := conformance.RunCheck(ctx, &conformance.CheckOptions{
		Env: checkEnv(app), Deps: checkDeps(app), SkipReports: true, KeepEvidence: true,
		// The projection of each declared view is computed from the state that the run keeps.
		KeepViewInput: len(wsp.Views) > 0,
	})
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return wsp, run, nil
}

// exportJob is what a bundle is made of: the workspace and the run to report, and the options of the export.
type exportJob struct {
	app  *Context
	wsp  *workspace.Workspace
	run  *conformance.Run
	opts *exportBundleOptions
}

// exportArchive gives the bytes of the bundle of the run.
func exportArchive(ctx context.Context, job *exportJob) ([]byte, error) {
	entries, records, viewEntries, err := bundleEntries(ctx, job)
	if err != nil {
		return nil, err
	}

	reportRedactions(job.app, records)

	manifest, err := bundleManifest(job.app, job.run, job.opts, entries, records)
	if err != nil {
		return nil, err
	}

	manifest.Views = viewEntries

	var buf bytes.Buffer
	if err := bundle.Write(&buf, &manifest, entries); err != nil {
		return nil, err //nolint:wrapcheck // a described write error.
	}

	return buf.Bytes(), nil
}

// exportWriteArchive writes the bundle to the output path and reports the path with the digest of the bundle.
func exportWriteArchive(app *Context, wsp *workspace.Workspace, archive []byte) (string, error) {
	out := filepath.Join(wsp.Root, filepath.FromSlash(defaultBundlePath))
	if app.Opts.Output != "" {
		out = app.Abs(app.Opts.Output)
	}

	if err := fsutil.WriteFileAtomic(out, archive); err != nil {
		return "", diag.Fail(diag.REP001, diag.Params{keyPath: out, "detail": err.Error()})
	}

	app.Out(out + " " + digest.OfBytes(archive) + "\n")

	return out, nil
}

// requireCompleteResults refuses the workspace when the run that wrote its JSON results is incomplete
// (CO-REP-004): an interrupted `check` must not be exported as if it had finished.
func requireCompleteResults(wsp *workspace.Workspace) error {
	runID, found := resultsRunID(wsp)
	if !found {
		return nil // no results file, no run to refuse.
	}

	return conformance.RequireCompleteRun(wsp.Root, runID) //nolint:wrapcheck // a *diag.Error that the caller reports.
}

// resultsRunID gives the ID of the run in the JSON results file of the workspace. The bool is false
// when there is no readable results file with a run.
func resultsRunID(wsp *workspace.Workspace) (string, bool) {
	text, err := os.ReadFile(wsp.ResolveInRoot(jsonReportPath(wsp)))
	if err != nil {
		return "", false
	}

	var probe struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}

	if json.Unmarshal(text, &probe) != nil {
		return "", false
	}

	return probe.Run.ID, probe.Run.ID != ""
}

type exportVerifyOptions struct {
	key             string
	identity        string
	issuer          string
	trustedRoot     string
	verifySignature bool
}

func newExportVerifyCmd(host *Host) *cobra.Command {
	var opts exportVerifyOptions

	cmd := &cobra.Command{
		Use:   "verify <bundle>",
		Short: "Verify the digests of a conformance bundle, and optionally its cosign signature",
		Args:  cobra.ExactArgs(1),
		RunE: host.Action(func(ctx context.Context, app *Context, args []string) (int, error) {
			return diag.OK, exportVerify(ctx, app, args[0], &opts)
		}),
	}
	cmd.Flags().BoolVar(&opts.verifySignature, "verify-signature", false, "verify the sigstore bundle <bundle>.sigstore.json with cosign")
	cmd.Flags().StringVar(&opts.key, "key", "", "cosign public key reference (default: keyless verification)")
	cmd.Flags().StringVar(&opts.identity, "certificate-identity", "", "signer identity for keyless verification")
	cmd.Flags().StringVar(&opts.issuer, "certificate-oidc-issuer", "", "OIDC issuer for keyless verification")
	cmd.Flags().StringVar(&opts.trustedRoot, "trusted-root", "", "Sigstore trusted root file; verifies without fetching it (needed with --offline)")

	return cmd
}

func exportVerify(ctx context.Context, app *Context, arg string, opts *exportVerifyOptions) error {
	cosignExe, err := exportVerifier(app, opts)
	if err != nil {
		return err
	}

	path := app.Abs(arg)
	if _, err := bundle.Verify(path); err != nil {
		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	level, err := exportVerifiedLevel(ctx, cosignExe, path, opts)
	if err != nil {
		return err
	}

	app.Out(path + ": " + level + "\n")

	return nil
}

// exportVerifier gives the cosign executable when the signature is to be verified, and the empty string otherwise.
func exportVerifier(app *Context, opts *exportVerifyOptions) (string, error) {
	if !opts.verifySignature {
		return "", nil
	}

	if app.Opts.Offline && opts.trustedRoot == "" {
		return "", diag.Fail(diag.WRK016, diag.Detail(offlineVerification))
	}

	return cosignPath(app)
}

// exportVerifiedLevel gives the assurance level of a bundle whose digests are right: the signature raises it
// when it is to be verified, and a wrong signature is an error.
func exportVerifiedLevel(ctx context.Context, cosignExe, path string, opts *exportVerifyOptions) (string, error) {
	if !opts.verifySignature {
		return bundle.LevelTextDigest, nil
	}

	if err := verifyBundleSignature(ctx, cosignExe, path, opts); err != nil {
		return "", err
	}

	return bundle.LevelTextAuthenticated, nil
}

// Details of CO-WRK-016. cosign 3 signs into the public transparency log, also with a key, and verifies a signature
// against the Sigstore trusted root, which it fetches over the network unless a trusted root file is given.
const (
	offlineSigning      = "signing writes to the transparency log"
	offlineVerification = "verification fetches the Sigstore trusted root"
)

// verifyBundleSignature verifies the sigstore bundle beside the exported bundle.
func verifyBundleSignature(ctx context.Context, cosignExe, path string, opts *exportVerifyOptions) error {
	err := cosign.VerifyBlob(ctx, cosignExe, path, cosign.BundlePath(path), cosign.VerifyOptions{
		Key: opts.key, Identity: opts.identity, Issuer: opts.issuer, TrustedRoot: opts.trustedRoot,
	})

	var failure *cosign.VerifyError
	if errors.As(err, &failure) {
		return failure.Diagnostic()
	}

	return err //nolint:wrapcheck // nil here: VerifyBlob gives only a *cosign.VerifyError.
}
