package statement

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/redact"
)

// shape says which fields of a predicate are persisted. A leaf keeps the whole value (its strings are still
// redacted); an object keeps the listed fields and drops the rest; a list applies its shape to each element.
type shape struct {
	fields map[string]shape
	keep   bool
}

var leaf = shape{keep: true}

func object(fields map[string]shape) shape { return shape{fields: fields} }

var (
	workloadShape = object(map[string]shape{"namespace": leaf, "kind": leaf, fieldName: leaf})
	evaluator     = object(map[string]shape{fieldName: leaf, "version": leaf})
)

// persistedShapes are the adapter contracts of the persisted fields (spec artifact-provenance, "Persisted
// statements keep only declared fields"). Everything not listed is removed and the removal is recorded. For SLSA
// provenance that drops the external, internal, and system build parameters, the byproducts, and the annotations
// and contents of resolved dependencies: they can hold secrets, and Concordia reads none of them.
var persistedShapes = map[string]shape{
	PredicateSLSAProvenance: object(map[string]shape{
		"buildDefinition": object(map[string]shape{
			"buildType": leaf,
			"resolvedDependencies": object(map[string]shape{
				"uri": leaf, fieldName: leaf, fieldDigest: leaf,
			}),
		}),
		"runDetails": object(map[string]shape{
			"builder": object(map[string]shape{"id": leaf, "version": leaf}),
			"metadata": object(map[string]shape{
				"invocationId": leaf, "startedOn": leaf, "finishedOn": leaf,
			}),
		}),
	}),
	PredicateDeploymentRecord: object(map[string]shape{
		fieldEnvironment: leaf, "cluster": leaf, "context": leaf, "workload": workloadShape, fieldContainer: leaf,
		"image": leaf, fieldDigest: leaf, fieldOutcome: leaf, fieldTime: leaf, "deployer": leaf,
	}),
	PredicateWorkloadObservation: object(map[string]shape{
		fieldEnvironment: leaf, "cluster": leaf, "context": leaf, "workload": workloadShape, fieldContainer: leaf,
		fieldDigest: leaf, fieldTime: leaf, "observer": leaf,
	}),
	PredicateCheckResult: object(map[string]shape{
		"check": leaf, fieldOutcome: leaf, fieldRevision: leaf, fieldEnvironment: leaf, "evaluator": evaluator,
		"evidence": object(map[string]shape{fieldName: leaf, "uri": leaf, fieldDigest: leaf}),
		"time":     leaf, "window": object(map[string]shape{"start": leaf, "end": leaf}), "covers": leaf,
	}),
	PredicateMeasurement: object(map[string]shape{
		"scenario": leaf, "metric": leaf, "value": leaf, "unit": leaf, "threshold": leaf, "comparison": leaf,
		"testConditions": leaf, fieldRevision: leaf, fieldEnvironment: leaf, "evaluator": evaluator, fieldOutcome: leaf,
		"window": object(map[string]shape{"start": leaf, "end": leaf}),
	}),
	PredicatePlan: object(map[string]shape{
		fieldEnvironment: leaf, fieldRevision: leaf, "tool": leaf, fieldTime: leaf,
	}),
}

// Persisted is a statement as Concordia stores, logs, and bundles it: the declared fields, the digest of the file,
// and the record of every removal and redaction. It holds no field outside the adapter contract.
type Persisted struct {
	// Digest is the digest of the statement file as it was read, `sha256:<hex>`.
	Digest        string `json:"digest"`
	PredicateType string `json:"predicateType"`
	// Subject is the subject list, with redacted strings.
	Subject []Subject `json:"subject"`
	// Predicate holds the declared predicate fields. It is nil for a predicate type that Concordia does not read.
	Predicate map[string]any `json:"predicate,omitempty"`
	// Redactions say which fields were removed or had a value redacted. They never hold a value.
	Redactions []redact.Record `json:"redactions"`
}

// OutputPath is where the persisted copy of the statement lives in the evaluation bundle.
func OutputPath(statementDigest string) string {
	return "assurance/statements/" + strings.TrimPrefix(statementDigest, digest.Prefix) + ".json"
}

// Persist filters the statement to the declared fields of its predicate type and redacts the strings that remain.
// A predicate type that has no contract keeps no predicate field and records the removal of the whole predicate.
func Persist(loaded *Loaded, rules redact.Rules) (Persisted, error) {
	output := OutputPath(loaded.Digest)
	persisted := Persisted{
		Digest:        loaded.Digest,
		PredicateType: loaded.Statement.PredicateType,
		Redactions:    []redact.Record{},
	}

	record := func(rec redact.Record) {
		rec.Output = output
		persisted.Redactions = append(persisted.Redactions, rec)
	}

	for i, subject := range loaded.Statement.Subject {
		clean := Subject{Digest: map[string]string{}}
		clean.Name = redactText(rules, "subject.name", subject.Name, subjectPath(i, "name"), record)

		for alg, value := range subject.Digest {
			clean.Digest[alg] = redactText(rules, fieldDigest, value, subjectPath(i, "digest."+alg), record)
		}

		persisted.Subject = append(persisted.Subject, clean)
	}

	wanted, known := persistedShapes[loaded.Statement.PredicateType]
	if !known {
		if len(loaded.Statement.Predicate) > 0 {
			record(redact.Record{Property: "predicate", Class: redact.ClassOutsideContract})
		}

		return persisted, nil
	}

	var predicate any
	if err := json.Unmarshal(loaded.Statement.Predicate, &predicate); err != nil {
		return Persisted{}, fmt.Errorf("persist %s: %w", loaded.Path, err)
	}

	filtered, _ := filter(predicate, wanted, "", rules, record).(map[string]any)
	persisted.Predicate = filtered

	sortRecords(persisted.Redactions)

	return persisted, nil
}

// JSON gives the canonical JSON text of the persisted statement.
func (persisted *Persisted) JSON() ([]byte, error) {
	data, err := canonjson.Marshal(persisted)
	if err != nil {
		return nil, fmt.Errorf("persisted statement: %w", err)
	}

	return data, nil
}

func subjectPath(index int, field string) string {
	return "subject[" + strconv.Itoa(index) + "]." + field
}

// filter keeps the fields of value that the shape declares. A removed field is recorded at its path.
func filter(value any, wanted shape, path string, rules redact.Rules, record func(redact.Record)) any {
	switch typed := value.(type) {
	case map[string]any:
		if wanted.keep {
			return redactTree(typed, path, rules, record)
		}

		out := make(map[string]any, len(typed))

		for _, key := range sortedKeys(typed) {
			sub, ok := wanted.fields[key]
			child := joinPath(path, key)

			if !ok {
				record(redact.Record{Property: child, Class: redact.ClassOutsideContract})

				continue
			}

			out[key] = filter(typed[key], sub, child, rules, record)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, element := range typed {
			out[i] = filter(element, wanted, path+"["+strconv.Itoa(i)+"]", rules, record)
		}

		return out
	case string:
		return redactText(rules, lastKey(path), typed, path, record)
	}

	return value
}

// redactTree redacts the strings of a kept subtree.
func redactTree(tree map[string]any, path string, rules redact.Rules, record func(redact.Record)) map[string]any {
	out := make(map[string]any, len(tree))

	for _, key := range sortedKeys(tree) {
		out[key] = redactValue(tree[key], key, joinPath(path, key), rules, record)
	}

	return out
}

func redactValue(value any, name, path string, rules redact.Rules, record func(redact.Record)) any {
	switch typed := value.(type) {
	case map[string]any:
		return redactTree(typed, path, rules, record)
	case []any:
		out := make([]any, len(typed))
		for i, element := range typed {
			out[i] = redactValue(element, name, path+"["+strconv.Itoa(i)+"]", rules, record)
		}

		return out
	case string:
		return redactText(rules, name, typed, path, record)
	}

	return value
}

// redactText redacts one string. The record names the path in the statement, not the property name that the rule
// matched on.
func redactText(rules redact.Rules, name, text, path string, record func(redact.Record)) string {
	cleaned, records := rules.RedactValue(name, text)
	for _, rec := range records {
		rec.Property = path
		record(rec)
	}

	return cleaned
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}

func lastKey(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		path = path[i+1:]
	}

	if i := strings.Index(path, "["); i >= 0 {
		path = path[:i]
	}

	return path
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	slices.SortFunc(keys, canonjson.CompareUTF16)

	return keys
}

func sortRecords(records []redact.Record) {
	slices.SortStableFunc(records, func(a, b redact.Record) int {
		if c := canonjson.CompareUTF16(a.Property, b.Property); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Class, b.Class)
	})
}
