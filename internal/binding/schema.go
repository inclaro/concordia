package binding

import (
	"embed"
	"sync"

	"github.com/inclaro/concordia/internal/jsonschema"
)

//go:embed schemas/binding.schema.json
var schemaFS embed.FS

// SchemaID is the ID of the BindingSet schema.
const SchemaID = "https://concordia.io/schemas/v1alpha1/binding-set.schema.json"

var bindingSchema = sync.OnceValue(func() *jsonschema.Schema {
	doc, err := schemaFS.ReadFile("schemas/binding.schema.json")
	if err != nil {
		panic("binding: embedded schema missing: " + err.Error())
	}

	schema, err := jsonschema.Compile(SchemaID, doc)
	if err != nil {
		panic("binding: embedded schema does not compile: " + err.Error())
	}

	return schema
})
