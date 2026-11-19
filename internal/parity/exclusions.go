package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
)

// Kinds of an excluded run.
var exclusionKinds = []string{"needs-network", "calmhub", "not-a-validate-input", "validator-difference"}

// ExcludedRelease is a CALM release that Concordia does not support.
type ExcludedRelease struct {
	Release string `json:"release"`
	Reason  string `json:"reason"`
}

// ExcludedRun is a run that the comparison skips.
type ExcludedRun struct {
	Case   string `json:"case"`
	Run    string `json:"run"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// Exclusions is the file exclusions.json.
type Exclusions struct {
	ExcludedReleases []ExcludedRelease `json:"excludedReleases"`
	Runs             []ExcludedRun     `json:"runs"`
}

// LoadExclusions reads and checks exclusions.json.
func LoadExclusions(path string) (Exclusions, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path comes from the caller.
	if err != nil {
		return Exclusions{}, fmt.Errorf("read exclusions: %w", err)
	}

	var loaded Exclusions
	if err := json.Unmarshal(data, &loaded); err != nil {
		return Exclusions{}, fmt.Errorf("parse %s: %w", path, err)
	}

	releases := map[string]bool{}

	for _, release := range loaded.ExcludedReleases {
		if release.Reason == "" {
			return Exclusions{}, fmt.Errorf("%s: release %q has no reason", path, release.Release)
		}

		if releases[release.Release] {
			return Exclusions{}, fmt.Errorf("%s: duplicate release %q", path, release.Release)
		}

		releases[release.Release] = true
	}

	runs := map[[2]string]bool{}

	for _, excluded := range loaded.Runs {
		if excluded.Reason == "" {
			return Exclusions{}, fmt.Errorf("%s: run %s/%s has no reason", path, excluded.Case, excluded.Run)
		}

		if !slices.Contains(exclusionKinds, excluded.Kind) {
			return Exclusions{}, fmt.Errorf("%s: run %s/%s has unknown kind %q", path, excluded.Case, excluded.Run, excluded.Kind)
		}

		key := [2]string{excluded.Case, excluded.Run}
		if runs[key] {
			return Exclusions{}, fmt.Errorf("%s: duplicate run %s/%s", path, excluded.Case, excluded.Run)
		}

		runs[key] = true
	}

	return loaded, nil
}

// Run gives the exclusion of a run, if there is one.
func (e *Exclusions) Run(caseName, run string) (ExcludedRun, bool) {
	for _, r := range e.Runs {
		if r.Case == caseName && r.Run == run {
			return r, true
		}
	}

	return ExcludedRun{}, false
}

// Release says whether a release is excluded.
func (e *Exclusions) Release(release string) bool {
	return slices.ContainsFunc(e.ExcludedReleases, func(r ExcludedRelease) bool { return r.Release == release })
}
