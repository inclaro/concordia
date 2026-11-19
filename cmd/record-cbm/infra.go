package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/observation/cbmprobe"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	// infraRepoDirName is the directory (below the temporary directory) of the recorded infrastructure repository.
	infraRepoDirName = "concordia-record-infra"
	// infraFixtureDir is relative to the module root.
	infraFixtureDir = "test/fixtures/infra"
	// infraProject is the CBM project name of both infrastructure recordings.
	infraProject = "concordia-fixture-infra"

	cbmExecutable   = "codebase-memory-mcp"
	cbmTimeout      = 5 * time.Minute
	cbmIndexTimeout = 30 * time.Minute
	cbmRetries      = 1

	// cbmIgnoreFile re-includes a directory that CBM skips by default.
	cbmIgnoreFile = ".cbmignore"
)

// infraVariant is one recording of the infrastructure repository.
type infraVariant struct {
	// extra are files that are added to the repository before its commit.
	extra map[string]string
	// output is relative to the module root.
	output string
	// message is the commit message.
	message string
}

// infraVariants are the recordings of the infrastructure repository: as it is,
// and with `.cbmignore` `!deploy/` (CBM skips `deploy/` by default, O1).
func infraVariants() []infraVariant {
	return []infraVariant{
		{
			output:  "test/fixtures/cbm-recordings/infra.json",
			message: "infra fixture",
		},
		{
			output:  "test/fixtures/cbm-recordings/infra-reinclude-deploy.json",
			message: "infra fixture with deploy/ re-included",
			extra:   map[string]string{cbmIgnoreFile: "!deploy/\n"},
		},
	}
}

// recordInfra records every variant of the infrastructure repository.
func recordInfra(ctx context.Context, root string) error {
	for _, variant := range infraVariants() {
		if err := recordInfraVariant(ctx, root, variant); err != nil {
			return fmt.Errorf("%s: %w", variant.output, err)
		}
	}

	return nil
}

// recordInfraVariant indexes one variant with the real CBM, then runs the
// adapter (freeze, file list, all edges) and the whole-graph probe on it.
// Their queries are the ones that the replay tests and the normalization use.
func recordInfraVariant(ctx context.Context, root string, variant infraVariant) error {
	repoDir := filepath.Join(os.TempDir(), infraRepoDirName)

	fixture := filepath.Join(root, filepath.FromSlash(infraFixtureDir))
	if err := prepareRepo(ctx, fixture, repoDir, variant.message, variant.extra); err != nil {
		return err
	}

	defer func() { _ = os.RemoveAll(repoDir) }()

	headSHA, err := workspace.NewCliGit(repoDir).Revision(ctx)
	if err != nil {
		return fmt.Errorf("git revision: %w", err)
	}

	if headSHA == "" {
		return errors.New("no git revision after the commit")
	}

	raw := observation.NewProcessClient(observation.ProcessOptions{
		Executable:   cbmExecutable,
		Timeout:      cbmTimeout,
		IndexTimeout: cbmIndexTimeout,
		Retries:      cbmRetries,
	})

	defer closeClient(raw)

	// A project left by an earlier run must not change the first index call.
	deleteProject(ctx, raw, infraProject)

	defer func() {
		// The cleanup runs after an interrupt too.
		deleteProject(context.WithoutCancel(ctx), raw, infraProject)
	}()

	sink := observation.NewMemorySink(headSHA)
	client := observation.NewRecordingClient(raw, sink)

	version, err := client.Version(ctx)
	if err != nil {
		return fmt.Errorf("cbm version: %w", err)
	}

	if err := recordSession(ctx, client, &sessionRepo{dir: repoDir, project: infraProject, headSHA: headSHA}, version); err != nil {
		return err
	}

	recording := sink.ToRecording()

	data, err := canonjson.Marshal(recording)
	if err != nil {
		return fmt.Errorf("encode the recording: %w", err)
	}

	output := filepath.Join(root, filepath.FromSlash(variant.output))
	if err = fsutil.WriteFileAtomic(output, data); err != nil {
		return fmt.Errorf("write the recording: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "recorded %d call(s) at %s -> %s\n", len(recording.Calls), headSHA, output)

	return nil
}

// sessionRepo is the indexed repository of one recorded session.
type sessionRepo struct {
	dir, project, headSHA string
}

// recordSession makes the calls that go into one recording: the index call, the adapter (freeze, file list, all
// edges) and the whole-graph probe.
func recordSession(ctx context.Context, client observation.Client, repo *sessionRepo, version string) error {
	if _, err := cbmprobe.Index(ctx, client, repo.dir, repo.project); err != nil {
		return err
	}

	provider := observation.NewCbmProvider(observation.CbmProviderOptions{Client: client, ExecutableVersion: version})

	ref, err := provider.Freeze(ctx, observation.FreezeRequest{Project: repo.project, GitRevision: repo.headSHA})
	if err != nil {
		return fmt.Errorf("freeze: %w", err)
	}

	if _, err = provider.ListFiles(ctx, ref); err != nil {
		return fmt.Errorf("list files: %w", err)
	}

	// The adapter skips the types that it does not map, so ask for all of them.
	nativeKinds := make([]string, 0, len(ref.Schema.EdgeTypes))
	for _, edgeType := range ref.Schema.EdgeTypes {
		nativeKinds = append(nativeKinds, edgeType.Type)
	}

	if _, err = provider.FetchEdges(ctx, ref, nativeKinds); err != nil {
		return fmt.Errorf("fetch edges: %w", err)
	}

	if err = provider.VerifyFrozen(ctx, ref); err != nil {
		return fmt.Errorf("verify frozen: %w", err)
	}

	if _, err = cbmprobe.Read(ctx, client, repo.project); err != nil {
		return err
	}

	return nil
}
