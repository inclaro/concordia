package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/logger"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// CoverageFile is the workspace-relative path of the coverage record that `index` writes.
const CoverageFile = "build/concordia/coverage.json"

// CoverageFileFor gives the workspace-relative coverage record of a project:
// CoverageFile for the primary project, else build/concordia/coverage-<name>.json.
func CoverageFileFor(primary bool, name string) string {
	if primary {
		return CoverageFile
	}

	return "build/concordia/coverage-" + name + ".json"
}

// IndexRecordFile is the workspace-relative path of the index result record that `index` writes.
const IndexRecordFile = "build/concordia/index-result.json"

// IndexRecordFileFor gives the workspace-relative index result record of a project:
// IndexRecordFile for the primary project, else build/concordia/index-result-<name>.json.
func IndexRecordFileFor(primary bool, name string) string {
	if primary {
		return IndexRecordFile
	}

	return "build/concordia/index-result-" + name + ".json"
}

// WriteIndexRecord writes the index result record of a project (canonical JSON) to a workspace-relative file.
func WriteIndexRecord(root, file string, rec *observation.IndexRecord) error {
	data, err := canonjson.Marshal(rec.Normalized())
	if err != nil {
		return fmt.Errorf("encode the index result: %w", err)
	}

	if err = fsutil.WriteFileAtomic(filepath.Join(root, filepath.FromSlash(file)), data); err != nil {
		return diag.Fail(diag.REP001, diag.Params{paramPath: file, paramDetail: err.Error()})
	}

	return nil
}

// ReadIndexRecord gives the index result record of a project from a workspace-relative
// file. It gives nil and the reason when the record is not available: no file, a file
// that cannot be read, a record of another project, or a record of another graph
// generation (the index result then describes another graph than the one of the run).
func ReadIndexRecord(root, file, project, generation string) (found *observation.IndexRecord, reason string) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file))) //nolint:gosec // the path is below the workspace root.
	if err != nil {
		return nil, "no index result recorded (run `concordia index`)"
	}

	var rec observation.IndexRecord
	if err = json.Unmarshal(data, &rec); err != nil || rec.Project != project {
		return nil, "the recorded index result cannot be read for this project"
	}

	if rec.GraphGeneration != generation {
		return nil, "the recorded index result is for another graph generation"
	}

	rec = rec.Normalized()
	found = &rec

	return found, ""
}

// ReadIndexedRevision gives the git revision that the last `index` recorded in the
// coverage file (workspace-relative path coverageFile). It gives "" when there is no coverage file, it is not readable, or it
// is for another project. CBM reports the live git head in `index_status`, so only
// this record shows that the graph is older than the checkout.
func ReadIndexedRevision(root, coverageFile, project string) string {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(coverageFile))) //nolint:gosec // the path is below the workspace root.
	if err != nil {
		return ""
	}

	var rec struct {
		Project        any `json:"project"`
		SourceRevision any `json:"sourceRevision"`
	}

	if json.Unmarshal(data, &rec) != nil {
		return ""
	}

	name, okName := rec.Project.(string)
	rev, okRev := rec.SourceRevision.(string)

	if !okName || !okRev || name != project {
		return ""
	}

	return rev
}

// DefaultClient makes the real CBM client from the workspace settings. Each call
// writes a trace event (no arguments, so that no source path reaches a trace).
// The caller closes the client to end its CBM process.
func DefaultClient(ws *workspace.Workspace, log *logger.Logger) *observation.ProcessClient {
	opts := observation.ProcessOptionsFrom(&ws.Observation)
	opts.OnCall = func(tool string) {
		if log != nil {
			log.Trace(logger.TraceEvent{Phase: "observation", Event: "call", AdapterCommand: "tools/call " + tool})
		}
	}

	return observation.NewProcessClient(opts)
}
