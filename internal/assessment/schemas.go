package assessment

import (
	"embed"
	"sync"

	"github.com/inclaro/concordia/internal/jsonschema"
)

//go:embed schemas/*.schema.json
var schemaFS embed.FS

// Schema IDs of the four document kinds.
const (
	AssessmentSchemaID = "https://concordia.io/schemas/v1alpha1/assessment.schema.json"
	AssumptionSchemaID = "https://concordia.io/schemas/v1alpha1/assumption.schema.json"
	ApprovalSchemaID   = "https://concordia.io/schemas/v1alpha1/approval.schema.json"
	RevocationSchemaID = "https://concordia.io/schemas/v1alpha1/revocation.schema.json"
)

// schemaOnce compiles one embedded schema on first use.
func schemaOnce(file, id string) func() *jsonschema.Schema {
	return sync.OnceValue(func() *jsonschema.Schema {
		doc, err := schemaFS.ReadFile("schemas/" + file)
		if err != nil {
			panic("assessment: embedded schema missing: " + err.Error())
		}

		schema, err := jsonschema.Compile(id, doc)
		if err != nil {
			panic("assessment: embedded schema does not compile: " + err.Error())
		}

		return schema
	})
}

var (
	assessmentSchema = schemaOnce("assessment.schema.json", AssessmentSchemaID)
	assumptionSchema = schemaOnce("assumption.schema.json", AssumptionSchemaID)
	approvalSchema   = schemaOnce("approval.schema.json", ApprovalSchemaID)
	revocationSchema = schemaOnce("revocation.schema.json", RevocationSchemaID)
)

// schemaOf gives the compiled schema of a document kind, or nil for an unknown kind.
func schemaOf(kind string) *jsonschema.Schema {
	switch kind {
	case KindAssessment:
		return assessmentSchema()
	case KindAssumption:
		return assumptionSchema()
	case KindApproval:
		return approvalSchema()
	case KindRevocation:
		return revocationSchema()
	default:
		return nil
	}
}

// Kinds is the closed list of document kinds.
var Kinds = []string{KindAssessment, KindAssumption, KindApproval, KindRevocation}
