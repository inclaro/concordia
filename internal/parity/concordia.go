package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/inclaro/concordia/internal/cli"
	"github.com/inclaro/concordia/internal/diag"
)

// Permissions of the files and directories that the package writes.
const (
	filePerm = 0o600
	dirPerm  = 0o750
)

// RunConcordia runs `concordia model validate --format json` for one run of a
// case in a copy of the case directory and gives the outcome.
func RunConcordia(ctx context.Context, caseDir string, run *Run) (Outcome, error) {
	diags, err := ConcordiaDiagnostics(ctx, caseDir, run)
	if err != nil {
		return Outcome{}, err
	}

	docs := map[string]any{}

	for _, file := range []string{run.Architecture, run.Pattern, run.Timeline} {
		if file == "" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(caseDir, filepath.FromSlash(file))) //nolint:gosec // a file of the case.
		if err != nil {
			continue
		}

		var doc any
		if json.Unmarshal(data, &doc) == nil {
			docs[path.Clean(file)] = doc
		}
	}

	return FromConcordia(diags, docs), nil
}

// ConcordiaDiagnostics runs `concordia model validate --format json` for one run
// of a case in a copy of the case directory and gives the diagnostics.
func ConcordiaDiagnostics(ctx context.Context, caseDir string, run *Run) ([]diag.Diagnostic, error) {
	dir, err := os.MkdirTemp("", "concordia-parity-*")
	if err != nil {
		return nil, fmt.Errorf("make temp dir: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	if err := CopyDir(caseDir, dir, nil); err != nil {
		return nil, err
	}

	var workspaceFile strings.Builder

	workspaceFile.WriteString("metadata: {name: parity}\n")

	if run.URLMapping != "" {
		quoted, err := json.Marshal(run.URLMapping)
		if err != nil {
			return nil, fmt.Errorf("encode url mapping: %w", err)
		}

		fmt.Fprintf(&workspaceFile, "architecture: {urlMapping: %s}\n", quoted)
	}

	workspaceFile.WriteString("observation:\n  projects:\n    - {name: parity, root: .}\n")

	if err := os.WriteFile(filepath.Join(dir, cli.DefaultWorkspaceFile), []byte(workspaceFile.String()), filePerm); err != nil {
		return nil, fmt.Errorf("write workspace file: %w", err)
	}

	// Offline: the comparison never fetches; a run that needs the network is excluded.
	args := []string{"--offline", "model", "validate", "--format", "json"}

	switch run.Mode {
	case ModeArchitecture:
		args = append(args, "--architecture", run.Architecture)
	case ModeArchitectureWithPattern:
		args = append(args, "--architecture", run.Architecture, "--pattern", run.Pattern)
	case ModePattern:
		args = append(args, "--pattern-only", "--pattern", run.Pattern)
	case ModeTimeline:
		args = append(args, "--timeline", run.Timeline)
	default:
		return nil, fmt.Errorf("unknown mode %q", run.Mode)
	}

	var stdout, stderr bytes.Buffer

	cli.Run(ctx, args, &cli.IO{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Env: map[string]string{}, Cwd: dir,
	}, cli.Deps{}, cli.BuildInfo{Version: "parity"})

	var doc struct {
		Diagnostics []diag.Diagnostic `json:"diagnostics"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		return nil, fmt.Errorf("decode concordia output: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	return doc.Diagnostics, nil
}

// CopyDir copies the regular files below src to dst and keeps the relative
// paths. keep, when not nil, selects the files (by the "/" path relative to src).
func CopyDir(src, dst string, keep func(rel string) bool) error {
	err := filepath.WalkDir(src, func(entry string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, entry)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)
		if dirEntry.IsDir() {
			return os.MkdirAll(target, dirPerm)
		}

		if !dirEntry.Type().IsRegular() || (keep != nil && !keep(filepath.ToSlash(rel))) {
			return nil
		}

		data, err := os.ReadFile(entry) //nolint:gosec // the path comes from the walk of src.
		if err != nil {
			return err
		}

		if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
			return err
		}

		return os.WriteFile(target, data, filePerm) //nolint:gosec // the target is below dst.
	})
	if err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}

	return nil
}
