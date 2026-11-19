package conformance

import (
	"embed"
	"sync"

	"github.com/inclaro/concordia/internal/jsonschema"
)

//go:embed schemas/results.schema.json
var schemaFS embed.FS

// ResultSchemaID is the ID of the embedded `results.json` schema (`schemaVersion` 1.1).
const ResultSchemaID = "https://concordia.io/schemas/v1alpha1/results-1.1.schema.json"

var resultSchema = sync.OnceValue(func() *jsonschema.Schema {
	doc, err := schemaFS.ReadFile("schemas/results.schema.json")
	if err != nil {
		panic("conformance: embedded results schema missing: " + err.Error())
	}

	schema, err := jsonschema.Compile(ResultSchemaID, doc)
	if err != nil {
		panic("conformance: embedded results schema does not compile: " + err.Error())
	}

	return schema
})

// ResultSchema gives the embedded JSON Schema of `results.json` (`schemaVersion` 1.1).
func ResultSchema() []byte {
	doc, err := schemaFS.ReadFile("schemas/results.schema.json")
	if err != nil {
		panic("conformance: embedded results schema missing: " + err.Error())
	}

	return doc
}

// ValidateResultJSON validates a `results.json` document against the embedded 1.1 schema. It gives no errors for a valid document.
func ValidateResultJSON(data []byte) []jsonschema.Error {
	return resultSchema().ValidateJSON(data)
}
