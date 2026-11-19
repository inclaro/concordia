// Package parity compares `concordia model validate` with `calm validate` of
// the CALM CLI. A case is a directory with documents and a file case.json; the
// recorded upstream outcome of each run is in expected.json of the case.
package parity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Validation modes of a run.
const (
	ModeArchitecture            = "architecture"
	ModeArchitectureWithPattern = "architecture-with-pattern"
	ModePattern                 = "pattern"
	ModeTimeline                = "timeline"
)

// File names in a case directory.
const (
	CaseFile     = "case.json"
	ExpectedFile = "expected.json"
)

// Case is the file case.json of one parity case.
type Case struct {
	Origin string `json:"origin"` // "concordia" or "upstream:<path in upstream>"
	Runs   []Run  `json:"runs"`
}

// Run is one validation in a case. Paths are relative to the case directory and use "/".
type Run struct {
	Name         string `json:"name"`
	Mode         string `json:"mode"` // "architecture", "architecture-with-pattern", "pattern", "timeline"
	Architecture string `json:"architecture,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	Timeline     string `json:"timeline,omitempty"`
	URLMapping   string `json:"urlMapping,omitempty"`
}

// LoadCase reads and checks the case.json of a case directory.
func LoadCase(dir string) (Case, error) {
	data, err := os.ReadFile(filepath.Join(dir, CaseFile)) //nolint:gosec // the case directory comes from the caller.
	if err != nil {
		return Case{}, fmt.Errorf("read case: %w", err)
	}

	var parsed Case
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Case{}, fmt.Errorf("parse %s: %w", filepath.Join(dir, CaseFile), err)
	}

	if len(parsed.Runs) == 0 {
		return Case{}, fmt.Errorf("%s: no runs", dir)
	}

	names := map[string]bool{}

	for i := range parsed.Runs {
		run := &parsed.Runs[i]
		if run.Name == "" {
			return Case{}, fmt.Errorf("%s: run %d has no name", dir, i)
		}

		if names[run.Name] {
			return Case{}, fmt.Errorf("%s: duplicate run name %q", dir, run.Name)
		}

		names[run.Name] = true

		if err := checkRun(dir, run); err != nil {
			return Case{}, fmt.Errorf("%s: run %q: %w", dir, run.Name, err)
		}
	}

	return parsed, nil
}

// checkRun checks that the run has exactly the files of its mode and that they exist.
func checkRun(dir string, run *Run) error {
	var want [3]bool // architecture, pattern, timeline

	switch run.Mode {
	case ModeArchitecture:
		want = [3]bool{true, false, false}
	case ModeArchitectureWithPattern:
		want = [3]bool{true, true, false}
	case ModePattern:
		want = [3]bool{false, true, false}
	case ModeTimeline:
		want = [3]bool{false, false, true}
	default:
		return fmt.Errorf("unknown mode %q", run.Mode)
	}

	fields := []struct {
		name  string
		value string
	}{{"architecture", run.Architecture}, {"pattern", run.Pattern}, {"timeline", run.Timeline}}

	for i, f := range fields {
		switch {
		case want[i] && f.value == "":
			return fmt.Errorf("mode %s needs %s", run.Mode, f.name)
		case !want[i] && f.value != "":
			return fmt.Errorf("mode %s does not allow %s", run.Mode, f.name)
		}
	}

	for _, file := range []string{run.Architecture, run.Pattern, run.Timeline, run.URLMapping} {
		if file == "" {
			continue
		}

		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file))); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("missing file %s", file)
			}

			return fmt.Errorf("stat %s: %w", file, err)
		}
	}

	return nil
}
