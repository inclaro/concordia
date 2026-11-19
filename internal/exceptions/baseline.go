package exceptions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
)

// BaselineVersion is the only value of Baseline.SchemaVersion.
const BaselineVersion = "1.0"

// BaselineEntry is one finding in the baseline.
type BaselineEntry struct {
	Fingerprint string `json:"fingerprint"`
	RuleID      string `json:"ruleId"`
	Owner       string `json:"owner"`
	Reason      string `json:"reason"`
	CreatedAt   string `json:"createdAt"`
	// ExpiresAt is optional. The empty text means no expiry.
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// CreatedFrom says which run made the baseline.
type CreatedFrom struct {
	// SourceRevision is null in JSON when the run had no source revision.
	SourceRevision  *string `json:"sourceRevision"`
	GraphGeneration string  `json:"graphGeneration"`
	PolicyDigest    string  `json:"policyDigest"`
}

// Baseline is the content of the baseline file.
type Baseline struct {
	SchemaVersion string          `json:"schemaVersion"`
	CreatedFrom   CreatedFrom     `json:"createdFrom"`
	Findings      []BaselineEntry `json:"findings"`
}

// PolicyDigestOf gives the digest of the policy pack digests: sha256 of the
// sorted digests, joined with a line feed.
func PolicyDigestOf(packDigests []string) string {
	sorted := slices.Clone(packDigests)
	slices.SortFunc(sorted, canonjson.CompareUTF16)

	return digest.Prefix + digest.SHA256HexString(strings.Join(sorted, "\n"))
}

// sortBaseline gives a copy of the baseline with the findings sorted by fingerprint.
func sortBaseline(src *Baseline) Baseline {
	sorted := *src

	sorted.Findings = slices.Clone(sorted.Findings)
	if sorted.Findings == nil {
		sorted.Findings = []BaselineEntry{}
	}

	slices.SortStableFunc(sorted.Findings, func(x, y BaselineEntry) int {
		return canonjson.CompareUTF16(x.Fingerprint, y.Fingerprint)
	})

	return sorted
}

// baselineProblem makes a CO-EXC-001 error for the baseline file.
func baselineProblem(uri, detail string) *diag.Error {
	return diag.Fail(diag.EXC001, diag.Detail(detail), diag.WithSource(diag.Source{URI: uri}))
}

// ParseBaselineJSON validates the JSON text of a baseline and gives the baseline
// with the findings sorted by fingerprint. Errors are CO-EXC-001 (exit code 2).
func ParseBaselineJSON(data []byte, uri string) (Baseline, error) {
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		return Baseline{}, baselineProblem(uri, "invalid JSON: "+err.Error())
	}

	return ParseBaseline(tree, uri)
}

// ParseBaseline validates a value that encoding/json decoded to "any" as a
// baseline. The uri is for the diagnostics. The findings are sorted by
// fingerprint. Errors are CO-EXC-001 (exit code 2).
func ParseBaseline(data any, uri string) (Baseline, error) {
	root, isObject := data.(map[string]any)
	if !isObject {
		return Baseline{}, baselineProblem(uri, "not an object")
	}

	if version, _ := root["schemaVersion"].(string); version != BaselineVersion {
		return Baseline{}, baselineProblem(uri, `schemaVersion must be "`+BaselineVersion+`"`)
	}

	createdFrom, ok := parseCreatedFrom(root["createdFrom"])
	if !ok {
		return Baseline{}, baselineProblem(uri, "createdFrom must have sourceRevision, graphGeneration, policyDigest")
	}

	list, ok := root["findings"].([]any)
	if !ok {
		return Baseline{}, baselineProblem(uri, "findings must be an array")
	}

	seen := make(map[string]struct{}, len(list))
	findings := make([]BaselineEntry, 0, len(list))

	for i, item := range list {
		entry, err := parseBaselineEntry(item, i, uri)
		if err != nil {
			return Baseline{}, err
		}

		if _, dup := seen[entry.Fingerprint]; dup {
			return Baseline{}, baselineProblem(uri, "duplicate fingerprint "+entry.Fingerprint)
		}

		seen[entry.Fingerprint] = struct{}{}

		findings = append(findings, entry)
	}

	result := Baseline{SchemaVersion: BaselineVersion, CreatedFrom: createdFrom, Findings: findings}

	return sortBaseline(&result), nil
}

func parseCreatedFrom(value any) (CreatedFrom, bool) {
	obj, ok := value.(map[string]any)
	if !ok {
		return CreatedFrom{}, false
	}

	generation, okGeneration := obj["graphGeneration"].(string)
	policy, okPolicy := obj["policyDigest"].(string)

	if !okGeneration || !okPolicy {
		return CreatedFrom{}, false
	}

	out := CreatedFrom{GraphGeneration: generation, PolicyDigest: policy}

	switch revision := obj["sourceRevision"].(type) {
	case nil:
		if _, present := obj["sourceRevision"]; !present {
			return CreatedFrom{}, false
		}
	case string:
		out.SourceRevision = &revision
	default:
		return CreatedFrom{}, false
	}

	return out, true
}

func parseBaselineEntry(item any, index int, uri string) (BaselineEntry, error) {
	where := "findings/" + strconv.Itoa(index)

	obj, ok := item.(map[string]any)
	if !ok {
		return BaselineEntry{}, baselineProblem(uri, where+" not an object")
	}

	values := make(map[string]string, len(baselineRequiredKeys))

	for _, key := range baselineRequiredKeys {
		text, isText := obj[key].(string)
		if !isText || text == "" {
			return BaselineEntry{}, baselineProblem(uri, where+"/"+key+" missing")
		}

		values[key] = text
	}

	if !fingerprintPattern.MatchString(values["fingerprint"]) {
		return BaselineEntry{}, baselineProblem(uri, where+"/fingerprint not a sha256 fingerprint")
	}

	entry := BaselineEntry{
		Fingerprint: values["fingerprint"],
		RuleID:      values["ruleId"],
		Owner:       values["owner"],
		Reason:      values["reason"],
		CreatedAt:   values["createdAt"],
	}

	if raw, present := obj["expiresAt"]; present {
		expires, isText := raw.(string)
		if !isText {
			return BaselineEntry{}, baselineProblem(uri, where+"/expiresAt must be a string")
		}

		entry.ExpiresAt = expires
	}

	return entry, nil
}

// baselineRequiredKeys are the text keys that each baseline entry must have.
var baselineRequiredKeys = []string{"fingerprint", "ruleId", "owner", "reason", "createdAt"}

// ReadBaseline reads and validates a baseline file (an OS path).
func ReadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller chooses the baseline file: reading it is the function.
	if err != nil {
		return Baseline{}, baselineProblem(path, "cannot read: "+err.Error())
	}

	return ParseBaselineJSON(data, path)
}

// WriteBaseline writes a baseline file (an OS path) as canonical JSON, with the
// findings sorted by fingerprint. Missing directories are created.
func WriteBaseline(path string, b *Baseline) error {
	text, err := canonjson.Marshal(sortBaseline(b))
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}

	if err := fsutil.WriteFileAtomic(filepath.Clean(path), text); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}

	return nil
}
