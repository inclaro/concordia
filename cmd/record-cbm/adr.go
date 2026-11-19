package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	// adrRepoDirName is the directory (below the temporary directory) of the recorded decision repository.
	adrRepoDirName = "concordia-record-adr"
	// adrFixtureDir and adrOutputFile are relative to the module root.
	adrFixtureDir  = "test/fixtures/adr"
	adrOutputFile  = "test/fixtures/cbm-recordings/adr.json"
	adrCommitTitle = "adr fixture"
	// adrProject is the CBM project name of the decision recording.
	adrProject = "concordia-fixture-adr"
)

// recordADR records the decision fixture (test/fixtures/adr): the index call, the adapter (freeze, file list, all
// edges, the `ADR` nodes), the whole-graph probe, and the `index` and `check` commands of the workspace. The
// recording holds the `ADR` nodes and `SUPERSEDES` edges of the decision files and the supersession variants (O12).
func recordADR(ctx context.Context, root string) error {
	repoDir := filepath.Join(os.TempDir(), adrRepoDirName)

	fixture := filepath.Join(root, filepath.FromSlash(adrFixtureDir))
	if err := prepareRepo(ctx, fixture, repoDir, adrCommitTitle, nil); err != nil {
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
	deleteProject(ctx, raw, adrProject)

	defer func() {
		// The cleanup runs after an interrupt too.
		deleteProject(context.WithoutCancel(ctx), raw, adrProject)
	}()

	sink := observation.NewMemorySink(headSHA)
	client := observation.NewRecordingClient(raw, sink)

	version, err := client.Version(ctx)
	if err != nil {
		return fmt.Errorf("cbm version: %w", err)
	}

	if err := recordSession(ctx, client, &sessionRepo{dir: repoDir, project: adrProject, headSHA: headSHA}, version); err != nil {
		return err
	}

	// The commands of record start their own CBM process: end this one first.
	closeClient(raw)

	if err := record(ctx, repoDir, sink); err != nil {
		return err
	}

	recording := sink.ToRecording()

	data, err := canonjson.Marshal(recording)
	if err != nil {
		return fmt.Errorf("encode the recording: %w", err)
	}

	output := filepath.Join(root, filepath.FromSlash(adrOutputFile))
	if err = fsutil.WriteFileAtomic(output, data); err != nil {
		return fmt.Errorf("write the recording: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "recorded %d call(s) at %s -> %s\n", len(recording.Calls), headSHA, output)

	return nil
}
