// Package jsonschema validates JSON values against JSON Schema documents.
//
// Schemas are registered by their "$id" (draft 2020-12, 2019-09, 7, 6) or "id"
// (draft 4), so they can refer to each other with "$ref". Format assertion is on.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ErrNoID is given when a schema document has no "$id" or "id".
var ErrNoID = errors.New("schema has no $id")

// Error is one schema violation.
type Error struct {
	// InstancePath is the JSON Pointer of the failing value ("" is the root).
	InstancePath string
	// Message says what is wrong. The wording is the wording of the validator library.
	Message string
}

// Registry holds schemas by ID.
type Registry struct {
	compiler *jsonschema.Compiler
}

// Schema is a compiled schema.
type Schema struct {
	schema *jsonschema.Schema
}

// NewRegistry makes an empty registry. The default draft is 2020-12; a
// "$schema" keyword selects another draft. Format assertion is on.
func NewRegistry() *Registry {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()

	return &Registry{compiler: c}
}

// AllowVocabulary accepts a vocabulary that a metaschema requires and that
// defines no assertion keyword (as a validator that ignores `$vocabulary`
// does). Without it, a schema whose `$schema` requires the vocabulary does not
// compile. Never use it for a vocabulary with assertion keywords: they would
// be accepted and not checked.
func (r *Registry) AllowVocabulary(url string) {
	r.compiler.RegisterVocabulary(&jsonschema.Vocabulary{
		URL:     url,
		Compile: func(*jsonschema.CompilerContext, map[string]any) (jsonschema.SchemaExt, error) { return nil, nil }, //nolint:nilnil // no extension keyword to compile.
	})
}

// Add registers a schema document under its "$id" (or "id" for draft 4). It
// gives the ID.
func (r *Registry) Add(doc []byte) (string, error) {
	tree, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return "", fmt.Errorf("parse schema: %w", err)
	}

	id := schemaID(tree)
	if id == "" {
		return "", ErrNoID
	}

	if err := r.compiler.AddResource(id, tree); err != nil {
		return "", fmt.Errorf("add schema %s: %w", id, err)
	}

	return id, nil
}

// AddFS registers every "*.json" file directly inside dir of fsys.
func (r *Registry) AddFS(fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("read schema directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		doc, err := fs.ReadFile(fsys, path.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read schema %s: %w", entry.Name(), err)
		}

		if _, err := r.Add(doc); err != nil {
			return fmt.Errorf("%s: %w", entry.Name(), err)
		}
	}

	return nil
}

// Compile compiles the registered schema with the given ID.
func (r *Registry) Compile(id string) (*Schema, error) {
	s, err := r.compiler.Compile(id)
	if err != nil {
		return nil, fmt.Errorf("compile schema %s: %w", id, err)
	}

	return &Schema{schema: s}, nil
}

// Compile makes a registry from the documents and compiles the schema with the given ID.
func Compile(id string, docs ...[]byte) (*Schema, error) {
	r := NewRegistry()

	for _, d := range docs {
		if _, err := r.Add(d); err != nil {
			return nil, err
		}
	}

	return r.Compile(id)
}

func schemaID(tree any) string {
	obj, ok := tree.(map[string]any)
	if !ok {
		return ""
	}

	for _, key := range []string{"$id", "id"} {
		if s, ok := obj[key].(string); ok && s != "" {
			return s
		}
	}

	return ""
}

// Validate checks an instance. The instance is a Go value that encoding/json
// can encode (maps, slices, strings, numbers, bools, nil, json.RawMessage).
// It gives the violations without duplicates (same pointer and message), or
// nil when the instance is valid.
func (s *Schema) Validate(instance any) []Error {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(instance); err != nil {
		return []Error{{Message: "instance cannot be encoded as JSON: " + err.Error()}}
	}

	return s.ValidateJSON(buf.Bytes())
}

// ValidateJSON checks JSON text.
func (s *Schema) ValidateJSON(data []byte) []Error {
	tree, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return []Error{{Message: "invalid JSON: " + err.Error()}}
	}

	err = s.schema.Validate(tree)
	if err == nil {
		return nil
	}

	var verr *jsonschema.ValidationError
	if !errors.As(err, &verr) {
		return []Error{{Message: err.Error()}}
	}

	return unique(leaves(verr, nil))
}

// leaves gives the violations at the leaves of the error tree. The inner nodes
// only say which keyword or reference holds the leaves.
func leaves(verr *jsonschema.ValidationError, out []Error) []Error {
	if len(verr.Causes) == 0 {
		unit := verr.BasicOutput()
		msg := ""

		if unit.Error != nil {
			msg = unit.Error.String()
		}

		return append(out, Error{InstancePath: unit.InstanceLocation, Message: msg})
	}

	for _, cause := range verr.Causes {
		out = leaves(cause, out)
	}

	return out
}

func unique(list []Error) []Error {
	seen := make(map[Error]struct{}, len(list))
	out := make([]Error, 0, len(list))

	for _, e := range list {
		if _, dup := seen[e]; dup {
			continue
		}

		seen[e] = struct{}{}

		out = append(out, e)
	}

	return out
}
