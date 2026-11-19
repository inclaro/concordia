package parity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/workspace"
)

// Files of the parity directory (the --cases directory).
const (
	PinFile      = "upstream.json"
	RulesFile    = "upstream-rules.json"
	ReleasesFile = "upstream-releases.json"
	CasesDir     = "cases"
	upstreamCase = "upstream"

	commitFile     = ".concordia-commit"
	urlMappingFile = "url-to-local-file-mapping.json"
	runTimeout     = 120 * time.Second
)

// ruleFiles are the upstream Spectral rulesets.
var ruleFiles = []string{
	"shared/src/spectral/rules-architecture.ts",
	"shared/src/spectral/rules-pattern.ts",
	"shared/src/spectral/rules-timeline.ts",
}

// engineRules are the rule IDs of the upstream validation engine that are not Spectral rules.
var engineRules = []string{"json-schema", "control-requirement-validation", "control-legacy-naming", "node-details-validation"}

// ruleKey finds the key of a rule in a Spectral ruleset.
var ruleKey = regexp.MustCompile(`(?m)^\s+'([a-z0-9-]+)':\s*\{`)

// Pin is the file upstream.json.
type Pin struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`
}

// RecordOptions are the inputs of Record.
type RecordOptions struct {
	// Log receives progress messages.
	Log io.Writer
	// Upstream is the checkout with the built CALM CLI.
	Upstream string
	// Node is the Node.js executable.
	Node string
	// Cases is the parity directory (with upstream.json and cases/).
	Cases string
	// Schemas is the directory of the embedded CALM schemas (internal/intent/schemas).
	Schemas string
}

// Record syncs the CALM schemas, writes the upstream rule and release lists,
// imports the upstream fixtures as cases, and records the upstream outcome of
// each run of each case.
func Record(ctx context.Context, opts *RecordOptions) error {
	pin, err := readPin(opts.Cases)
	if err != nil {
		return err
	}

	if err := prepareRecord(opts, pin); err != nil {
		return err
	}

	for _, step := range []func(*RecordOptions) error{syncSchemas, writeReleases, writeRules, importFixtures} {
		if err := step(opts); err != nil {
			return err
		}
	}

	runs, cases, err := recordCases(ctx, opts, pin.Tag)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(opts.Log, "recorded %d runs in %d cases with %s\n", runs, cases, pin.Tag)

	return nil
}

// prepareRecord checks that the checkout is the pinned commit and makes the
// path of the Node.js executable absolute.
func prepareRecord(opts *RecordOptions, pin Pin) error {
	built, err := os.ReadFile(filepath.Join(opts.Upstream, commitFile))
	if err != nil || strings.TrimSpace(string(built)) != pin.Commit {
		return errors.New("the checkout is not the pinned commit; run mise run calm:build")
	}

	// The runs use the case directory as the working directory.
	if strings.ContainsRune(opts.Node, filepath.Separator) {
		if opts.Node, err = filepath.Abs(opts.Node); err != nil {
			return fmt.Errorf("node path: %w", err)
		}
	}

	return nil
}

func readPin(dir string) (Pin, error) {
	data, err := os.ReadFile(filepath.Join(dir, PinFile)) //nolint:gosec // the parity directory comes from the caller.
	if err != nil {
		return Pin{}, fmt.Errorf("read pin: %w", err)
	}

	var pin Pin
	if err := json.Unmarshal(data, &pin); err != nil {
		return Pin{}, fmt.Errorf("parse pin: %w", err)
	}

	return pin, nil
}

func writeJSON(file string, v any) error {
	data, err := canonjson.Marshal(v)
	if err != nil {
		return err //nolint:wrapcheck // canonjson errors name the cause.
	}

	if err := os.WriteFile(file, data, filePerm); err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}

	return nil
}

// syncSchemas copies the CALM release schemas of each supported release from
// the CALM CLI build to the embedded schema directory. A supported release that
// the CALM CLI does not bundle keeps its embedded copy.
func syncSchemas(opts *RecordOptions) error {
	for _, release := range workspace.CalmSchemaVersions {
		src := filepath.Join(opts.Upstream, "cli", "dist", "calm", "release", release, "meta")
		dst := filepath.Join(opts.Schemas, "calm-"+release)

		entries, err := os.ReadDir(src)
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(opts.Log, "upstream has no CALM %s schemas; keeping the embedded copy\n", release)

			continue
		}

		if err != nil {
			return fmt.Errorf("read upstream CALM %s schemas: %w", release, err)
		}

		names, err := copySchemas(entries, src, dst)
		if err != nil {
			return err
		}

		if err := removeStaleSchemas(dst, names); err != nil {
			return err
		}
	}

	return nil
}

// copySchemas copies the regular files of the upstream schema directory src to
// dst and gives their names.
func copySchemas(entries []os.DirEntry, src, dst string) (map[string]bool, error) {
	names := map[string]bool{}

	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}

		names[entry.Name()] = true

		data, err := os.ReadFile(filepath.Join(src, entry.Name())) //nolint:gosec // a file of the upstream build.
		if err != nil {
			return nil, fmt.Errorf("read upstream schema: %w", err)
		}

		if err := os.MkdirAll(dst, dirPerm); err != nil {
			return nil, fmt.Errorf("make %s: %w", dst, err)
		}

		if err := os.WriteFile(filepath.Join(dst, entry.Name()), data, filePerm); err != nil { //nolint:gosec // the name is a file name of the upstream build.
			return nil, fmt.Errorf("write schema: %w", err)
		}
	}

	return names, nil
}

// removeStaleSchemas removes the files of dst that are not in names.
func removeStaleSchemas(dst string, names map[string]bool) error {
	own, err := os.ReadDir(dst)
	if err != nil {
		return fmt.Errorf("read %s: %w", dst, err)
	}

	for _, entry := range own {
		if !names[entry.Name()] {
			if err := os.Remove(filepath.Join(dst, entry.Name())); err != nil {
				return fmt.Errorf("remove stale schema: %w", err)
			}
		}
	}

	return nil
}

func writeReleases(opts *RecordOptions) error {
	entries, err := os.ReadDir(filepath.Join(opts.Upstream, "cli", "dist", "calm", "release"))
	if err != nil {
		return fmt.Errorf("read upstream releases: %w", err)
	}

	releases := []string{}

	for _, entry := range entries {
		if entry.IsDir() {
			releases = append(releases, entry.Name())
		}
	}

	slices.SortFunc(releases, CompareNumeric)

	return writeJSON(filepath.Join(opts.Cases, ReleasesFile), releases)
}

// CompareNumeric compares two names with numeric-aware order: digit runs
// compare as numbers.
func CompareNumeric(left, right string) int {
	for left != "" && right != "" {
		leftDigits, rightDigits := digitPrefix(left), digitPrefix(right)
		if leftDigits != "" && rightDigits != "" {
			leftNum, rightNum := strings.TrimLeft(leftDigits, "0"), strings.TrimLeft(rightDigits, "0")
			if c := len(leftNum) - len(rightNum); c != 0 {
				return c
			}

			if c := strings.Compare(leftNum, rightNum); c != 0 {
				return c
			}

			left, right = left[len(leftDigits):], right[len(rightDigits):]

			continue
		}

		if left[0] != right[0] {
			return int(left[0]) - int(right[0])
		}

		left, right = left[1:], right[1:]
	}

	return len(left) - len(right)
}

func digitPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}

	return s[:i]
}

// RuleIDs gives the rule keys of a Spectral ruleset source text.
func RuleIDs(source string) []string {
	matches := ruleKey.FindAllStringSubmatch(source, -1)
	out := make([]string, 0, len(matches))

	for _, m := range matches {
		out = append(out, m[1])
	}

	return out
}

func writeRules(opts *RecordOptions) error {
	rules := slices.Clone(engineRules)

	for _, file := range ruleFiles {
		data, err := os.ReadFile(filepath.Join(opts.Upstream, filepath.FromSlash(file)))
		if err != nil {
			return fmt.Errorf("read rules: %w", err)
		}

		ids := RuleIDs(string(data))
		if len(ids) == 0 {
			return fmt.Errorf("no rules found in %s", file)
		}

		rules = append(rules, ids...)
	}

	slices.Sort(rules)

	return writeJSON(filepath.Join(opts.Cases, RulesFile), slices.Compact(rules))
}
