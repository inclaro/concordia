package observation

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/logger"
)

// ExecutableClient is a Client that starts a CBM executable. It gives the name or path of the executable as it is
// configured. A client without an executable (a fake) does not have this method and has no executable identity.
type ExecutableClient interface {
	Executable() string
}

// ExecutableIdentity is the CBM executable that a client starts: its resolved path and the digest of the file.
// The zero value says that the client has no executable.
type ExecutableIdentity struct {
	// Path is the absolute path of the executable file, with symbolic links resolved.
	Path string
	// SHA256 is `sha256:<hex>` of the executable file.
	SHA256 string
}

// IdentifyExecutable finds the executable of the client and hashes the file, so the digest is the digest of the
// program that runs. A client that has no executable gives the zero identity. A missing executable gives CO-OBS-003.
func IdentifyExecutable(client Client) (ExecutableIdentity, error) {
	owner, ok := client.(ExecutableClient)
	if !ok {
		return ExecutableIdentity{}, nil
	}

	return ResolveExecutable(owner.Executable())
}

// ResolveExecutable finds the executable `name` as exec.LookPath does, resolves symbolic links, and hashes the
// file. A missing executable gives CO-OBS-003.
func ResolveExecutable(name string) (ExecutableIdentity, error) {
	found, err := exec.LookPath(name)
	if err != nil {
		return ExecutableIdentity{}, diag.Fail(diag.OBS003, diag.Params{"executable": name})
	}

	resolved, err := filepath.EvalSymlinks(found)
	if err != nil {
		return ExecutableIdentity{}, diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("cannot resolve the CBM executable '%s': %s", name, err)))
	}

	sum, err := digest.OfFile(resolved)
	if err != nil {
		return ExecutableIdentity{}, diag.Fail(diag.OBS001, diag.Detail(fmt.Sprintf("cannot read the CBM executable '%s': %s", name, err)))
	}

	return ExecutableIdentity{Path: resolved, SHA256: sum}, nil
}

// Connection is a connected observation: the client, the provider on top of it
// and the CBM version.
type Connection struct {
	Client   Client
	Provider *CbmProvider
	// Version is the version text of CBM, for example `0.12.1`.
	Version string
	// Executable is the resolved path of the CBM executable. It is empty when the client has no executable.
	Executable string
	// SHA256 is `sha256:<hex>` of the CBM executable file. It is empty when the client has no executable.
	SHA256 string
	// Assessment is the decision about the build, with its profile.
	Assessment BuildAssessment
	// Diagnostics are version warnings (CO-OBS-004 for a degraded version).
	// Errors are returned as an error.
	Diagnostics []diag.Diagnostic
}

// Connect reads and checks the CBM version of the client, hashes its executable and makes the
// provider. It gives CO-OBS-003 (executable missing) or CO-OBS-004 (version
// not compatible). log may be nil.
func Connect(ctx context.Context, client Client, log *logger.Logger) (*Connection, error) {
	version, err := client.Version(ctx)
	if err != nil {
		return nil, err
	}

	assessment, diagnostics, err := AssessBuild(version)
	if err != nil {
		return nil, err
	}

	identity, err := IdentifyExecutable(client)
	if err != nil {
		return nil, err
	}

	if log != nil {
		log.Diagnostics(diagnostics)
	}

	return &Connection{
		Client:      client,
		Provider:    NewCbmProvider(CbmProviderOptions{Client: client, ExecutableVersion: version, Logger: log}),
		Version:     version,
		Executable:  identity.Path,
		SHA256:      identity.SHA256,
		Assessment:  assessment,
		Diagnostics: diagnostics,
	}, nil
}
