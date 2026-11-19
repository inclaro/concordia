package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/inclaro/concordia/internal/cosign"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/workspace"
)

const recordDirMode = 0o750

// decisionFile is a record that is ready to write.
type decisionFile struct {
	kind, id, path, digest, cosign, signKey, mechanismHint string
	text, canonical                                        []byte
	sign                                                   bool
}

// withSigning sets the signing and next-step fields of a record that is ready to write.
func (f *decisionFile) withSigning(opts *assessmentDecisionOptions, cosignExe, hint string) *decisionFile {
	f.cosign = cosignExe
	f.signKey = opts.signKey
	f.sign = opts.sign
	f.mechanismHint = hint

	return f
}

// writeDecisionRecord writes the record file, and its signature bundle first when --sign is set, then says what
// to do next. It writes nothing else. A record that exists is never overwritten.
func writeDecisionRecord(ctx context.Context, app *Context, wsp *workspace.Workspace, f *decisionFile) error { //nolint:varnamelen // f is the one record that the function writes.
	abs, err := decisionRecordTarget(wsp, f)
	if err != nil {
		return err
	}

	if err := writeDecisionFiles(ctx, f, abs); err != nil {
		return err
	}

	app.Out(fmt.Sprintf("%s written: %s\nRecord digest: %s\n", f.kind, f.path, f.digest))

	if f.sign {
		app.Out("signature written: " + filepath.ToSlash(f.path) + cosign.BundleSuffix + "\n")
	}

	app.Out(f.mechanismHint)

	return nil
}

// decisionRecordTarget gives the absolute path of a record that does not exist yet and lies inside the workspace.
func decisionRecordTarget(wsp *workspace.Workspace, f *decisionFile) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(f.path)) {
		return "", refuse("the record path '" + f.path + "' is outside the workspace")
	}

	abs := filepath.Join(wsp.Root, filepath.FromSlash(f.path))

	if _, err := os.Stat(abs); err == nil {
		return "", refuse("the record file " + f.path + " exists: a record is never overwritten")
	}

	return abs, nil
}

// writeDecisionFiles writes the signature bundle when --sign is set, then the record. A record that fails to write
// leaves no bundle behind.
func writeDecisionFiles(ctx context.Context, f *decisionFile, abs string) error { //nolint:varnamelen // f is the one record that the function writes.
	bundle := cosign.BundlePath(abs)

	if f.sign {
		if err := signRecord(ctx, f, abs, bundle); err != nil {
			return err
		}
	}

	if err := fsutil.WriteFileAtomic(abs, f.text); err != nil {
		if f.sign {
			_ = os.Remove(bundle)
		}

		return diag.Fail(diag.REP001, diag.Params{keyPath: f.path, keyDetail: err.Error()})
	}

	return nil
}

// signRecord signs the canonical JSON of the record into the bundle beside the record file.
func signRecord(ctx context.Context, f *decisionFile, abs, bundle string) error { //nolint:varnamelen // f is the one record that the function signs.
	if err := os.MkdirAll(filepath.Dir(abs), recordDirMode); err != nil {
		return diag.Fail(diag.REP001, diag.Params{keyPath: f.path, keyDetail: err.Error()})
	}

	blob, err := writeCosignBlob(f.canonical)
	if err != nil {
		return err
	}

	defer func() { _ = os.Remove(blob) }()

	if err := cosign.SignBlobTo(ctx, f.cosign, blob, bundle, f.signKey); err != nil {
		_ = os.Remove(bundle)

		return err //nolint:wrapcheck // a *diag.Error that the caller reports.
	}

	return nil
}

// writeCosignBlob writes the canonical record to a temporary file that cosign signs, and gives its name. The
// caller removes the file; a file that is not completely written is removed here.
func writeCosignBlob(canonical []byte) (string, error) {
	blob, err := os.CreateTemp("", "concordia-record-*.json")
	if err != nil {
		return "", cosignBlobError(err)
	}

	if _, err := blob.Write(canonical); err != nil {
		_ = blob.Close()
		_ = os.Remove(blob.Name())

		return "", cosignBlobError(err)
	}

	if err := blob.Close(); err != nil {
		_ = os.Remove(blob.Name())

		return "", cosignBlobError(err)
	}

	return blob.Name(), nil
}

func cosignBlobError(err error) error {
	return diag.Fail(diag.REP003, diag.Detail("cannot write the record for cosign: "+err.Error()))
}
