package policy

import (
	"embed"
	"sync"

	"github.com/inclaro/concordia/internal/jsonschema"
)

//go:embed schemas/policy.schema.json
var schemaFS embed.FS

// PackSchemaID is the ID of the policy pack schema.
const PackSchemaID = "https://concordia.io/schemas/v1alpha1/policy-pack.schema.json"

// packSchema is the compiled schema of a policy pack. It checks the shape of a pack; compile.go
// checks the rules that need a clear message (one predicate, subject, allow).
var packSchema = sync.OnceValue(func() *jsonschema.Schema {
	doc, err := schemaFS.ReadFile("schemas/policy.schema.json")
	if err != nil {
		panic("policy: embedded schema missing: " + err.Error())
	}

	schema, err := jsonschema.Compile(PackSchemaID, doc)
	if err != nil {
		panic("policy: embedded schema does not compile: " + err.Error())
	}

	return schema
})
