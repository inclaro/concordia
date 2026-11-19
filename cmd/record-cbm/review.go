package main

import (
	"context"
	"encoding/json"
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
	// reviewFixtureFile is relative to the module root.
	reviewFixtureFile = "test/fixtures/review/cbm-0.12.1/fixture.json"
	// reviewRepoDirName is the directory (below the temporary directory) of the recorded review repository.
	reviewRepoDirName = "concordia-record-review"
	// reviewStageDirName is the directory of the repository with the removed files, before it is committed.
	reviewStageDirName = "concordia-record-review-stage"
)

// reviewVariant is one recording of the review fixture (see fixture.json).
type reviewVariant struct {
	Name      string   `json:"name"`
	Recording string   `json:"recording"`
	Message   string   `json:"message"`
	Remove    []string `json:"remove"`
	Reuse     bool     `json:"reuse"`
}

// reviewFixture is fixture.json: the recorded repository and its variants.
type reviewFixture struct {
	Repository string          `json:"repository"`
	Project    string          `json:"project"`
	Variants   []reviewVariant `json:"variants"`
}

// recordReview records the variants of the review fixture that are not reused: the infrastructure repository with
// some files removed (a Secret manifest that a workload still refers to, O5). The recordings that are reused are not
// touched, so no other recording changes.
func recordReview(ctx context.Context, root string) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(reviewFixtureFile))) //nolint:gosec // a fixed path below the module root.
	if err != nil {
		return fmt.Errorf("read %s: %w", reviewFixtureFile, err)
	}

	var fixture reviewFixture
	if err = json.Unmarshal(data, &fixture); err != nil {
		return fmt.Errorf("parse %s: %w", reviewFixtureFile, err)
	}

	for i := range fixture.Variants {
		if fixture.Variants[i].Reuse {
			continue
		}

		if err = recordReviewVariant(ctx, root, &fixture, &fixture.Variants[i]); err != nil {
			return fmt.Errorf("%s: %w", fixture.Variants[i].Recording, err)
		}
	}

	return nil
}

// recordReviewVariant copies the repository, removes the files, commits it, and records the adapter session and the
// whole graph of the real CBM on it (recordSession), like the infra recording.
func recordReviewVariant(ctx context.Context, root string, fixture *reviewFixture, variant *reviewVariant) error {
	stage := filepath.Join(os.TempDir(), reviewStageDirName)
	repoDir := filepath.Join(os.TempDir(), reviewRepoDirName)

	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("remove %s: %w", stage, err)
	}

	defer func() { _ = os.RemoveAll(stage) }()

	if err := os.CopyFS(stage, os.DirFS(filepath.Join(root, filepath.FromSlash(fixture.Repository)))); err != nil {
		return fmt.Errorf("copy the repository: %w", err)
	}

	for _, file := range variant.Remove {
		if err := os.Remove(filepath.Join(stage, filepath.FromSlash(file))); err != nil {
			return fmt.Errorf("remove %s: %w", file, err)
		}
	}

	if err := prepareRepo(ctx, stage, repoDir, variant.Message, nil); err != nil {
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
		Executable: cbmExecutable, Timeout: cbmTimeout, IndexTimeout: cbmIndexTimeout, Retries: cbmRetries,
	})

	defer closeClient(raw)

	// A project left by an earlier run must not change the first index call.
	deleteProject(ctx, raw, fixture.Project)

	defer func() { deleteProject(context.WithoutCancel(ctx), raw, fixture.Project) }()

	sink := observation.NewMemorySink(headSHA)
	client := observation.NewRecordingClient(raw, sink)

	version, err := client.Version(ctx)
	if err != nil {
		return fmt.Errorf("cbm version: %w", err)
	}

	if err := recordSession(ctx, client, &sessionRepo{dir: repoDir, project: fixture.Project, headSHA: headSHA}, version); err != nil {
		return err
	}

	recording := sink.ToRecording()

	out, err := canonjson.Marshal(recording)
	if err != nil {
		return fmt.Errorf("encode the recording: %w", err)
	}

	output := filepath.Join(root, filepath.FromSlash(variant.Recording))
	if err = fsutil.WriteFileAtomic(output, out); err != nil {
		return fmt.Errorf("write the recording: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "recorded %d call(s) at %s -> %s\n", len(recording.Calls), headSHA, output)

	return nil
}
