// Package intent loads and validates the CALM 1.2 or 1.3 architecture document and
// builds the architecture model that policies, bindings and conformance use.
package intent

import (
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/trust"
)

// C4Level is the C4 level of a node.
type C4Level string

// C4 levels.
const (
	C4Person         C4Level = "person"
	C4SoftwareSystem C4Level = "softwareSystem"
	C4Container      C4Level = "container"
	C4Component      C4Level = "component"
)

// ExtBinding is one item of the `concordia.bindings` extension of a CALM node.
type ExtBinding struct {
	// Kind is source-root, package, qualified-name, build-target, otel-service,
	// workload, image, route, infra-resource or compose-service.
	Kind string `json:"kind"`
	// Value is the selector of the five as-built kinds.
	Value string `json:"value"`
	// The identity fields of the deployed-resource kinds.
	Environment  string `json:"environment,omitempty"`
	Cluster      string `json:"cluster,omitempty"`
	APIGroup     string `json:"apiGroup,omitempty"`
	ResourceKind string `json:"resourceKind,omitempty"`
	Namespace    string `json:"namespace,omitempty"`
	Name         string `json:"name,omitempty"`
	Repository   string `json:"repository,omitempty"`
	Digest       string `json:"digest,omitempty"`
	Address      string `json:"address,omitempty"`
	File         string `json:"file,omitempty"`
	Service      string `json:"service,omitempty"`
	// Binding-level settings; empty means the default.
	Cardinality string `json:"cardinality,omitempty"`
	Agreement   string `json:"agreement,omitempty"`
	Status      string `json:"status,omitempty"`
	Authority   string `json:"authority,omitempty"`
}

// NodeDeclaration is a CALM node. Pointer is a JSON Pointer into the CALM document;
// Tags and Bindings are never nil.
type NodeDeclaration struct {
	ID          string       `json:"id"`
	NodeType    string       `json:"nodeType"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Owner       string       `json:"owner,omitempty"`
	C4Level     C4Level      `json:"c4Level,omitempty"`
	Pointer     string       `json:"pointer"`
	Tags        []string     `json:"tags"`
	Bindings    []ExtBinding `json:"bindings"`
	// Trust is the `concordia.trust` declaration (authority dimensions and data classification); nil when none.
	Trust *trust.Declaration `json:"trust,omitempty"`
	// Interfaces are the unique IDs of the interfaces the node declares, unique, in declaration order. Never nil.
	Interfaces []string `json:"interfaces"`
}

// RelationshipKind is the kind of a CALM relationship.
type RelationshipKind string

// Relationship kinds.
const (
	RelConnects   RelationshipKind = "connects"
	RelInteracts  RelationshipKind = "interacts"
	RelComposedOf RelationshipKind = "composed-of"
	RelDeployedIn RelationshipKind = "deployed-in"
	RelOptions    RelationshipKind = "options"
)

// RelationshipObservation is the `concordia.observation` extension of a relationship.
// AllowedEdgeKinds is nil when the document has none (or a bad one); an empty list
// that the document gave is not nil. MinimumConfidence is empty when not given.
type RelationshipObservation struct {
	MinimumConfidence observation.Confidence          `json:"minimumConfidence,omitempty"`
	AllowedEdgeKinds  []observation.CanonicalEdgeKind `json:"allowedEdgeKinds,omitempty"`
	Required          bool                            `json:"required"`
}

// RelationshipDeclaration is a CALM relationship. Source is the source node of
// `connects` or the actor of `interacts`. Targets (never nil) is the destination of
// `connects` (one item) or the nodes of the other kinds. Container is the container
// of `composed-of` and `deployed-in`.
type RelationshipDeclaration struct {
	Observation *RelationshipObservation `json:"observation,omitempty"`
	// Trust is the `concordia.trust` declaration (authority dimensions and data classification); nil when none.
	Trust       *trust.Declaration `json:"trust,omitempty"`
	ID          string             `json:"id"`
	Kind        RelationshipKind   `json:"kind"`
	Source      string             `json:"source,omitempty"`
	Container   string             `json:"container,omitempty"`
	Protocol    string             `json:"protocol,omitempty"`
	Description string             `json:"description,omitempty"`
	Pointer     string             `json:"pointer"`
	Targets     []string           `json:"targets"`
}

// FlowTransition is one step of a CALM flow. Direction is `source-to-destination`
// (the default) or `destination-to-source`.
type FlowTransition struct {
	RelationshipID string `json:"relationshipId"`
	Direction      string `json:"direction"`
	Pointer        string `json:"pointer"`
	Sequence       int    `json:"sequence"`
}

// FlowDeclaration is a CALM flow: an ordered list of transitions over relationships.
// Transitions are ordered by sequence number (document order for equal numbers); never nil.
type FlowDeclaration struct {
	ID          string           `json:"id"`
	Pointer     string           `json:"pointer"`
	Transitions []FlowTransition `json:"transitions"`
}

// ControlConfig is the Concordia control configuration (its engine is always `concordia`).
// RuleIDs and CheckIDs are in document order; a valid configuration has at least one of them
// (the schema enforces it). UnknownDisposition is fail, warn, ignore or empty.
// MinimumPolicyCoverage is nil when not set.
type ControlConfig struct {
	MinimumPolicyCoverage *float64
	ControlID             string
	Engine                string
	PolicyRef             string
	UnknownDisposition    string
	RuleIDs               []string
	CheckIDs              []string
}

// ScopeLevel is the place of a control in the document.
type ScopeLevel string

// Scope levels.
const (
	ScopeArchitecture ScopeLevel = "architecture"
	ScopeNode         ScopeLevel = "node"
	ScopeRelationship ScopeLevel = "relationship"
	ScopeFlow         ScopeLevel = "flow"
)

// ControlScope says where a control is attached.
type ControlScope struct {
	Level ScopeLevel `json:"level"`
	ID    string     `json:"id,omitempty"`
}

// ControlAttachment is one requirement of a CALM control. Config is set only when the
// requirement configuration has `engine: "concordia"`. Engine is the engine of the
// configuration when it has one (other engines are not-applicable).
type ControlAttachment struct {
	ControlID string       `json:"controlId"`
	Scope     ControlScope `json:"scope"`
	// Config is set only when the requirement configuration has `engine: "concordia"`.
	Config *ControlConfig `json:"-"`
	// Engine is the engine of the configuration when it has one (other engines are not-applicable).
	Engine  string `json:"engine,omitempty"`
	Pointer string `json:"pointer"`
}

// ArchitectureModel is the loaded CALM document. Digest is `sha256:<hex>` of the file
// bytes. SchemaVersion is the CALM release of the document ("1.2" or "1.3").
type ArchitectureModel struct {
	Metadata      map[string]any            `json:"metadata"`
	SourceURI     string                    `json:"sourceUri"`
	Digest        string                    `json:"digest"`
	SchemaVersion string                    `json:"schemaVersion"`
	Nodes         []NodeDeclaration         `json:"nodes"`
	Relationships []RelationshipDeclaration `json:"relationships"`
	Controls      []ControlAttachment       `json:"controls"`
	// Flows are the CALM flows, in document order; omitted when the document has none.
	Flows []FlowDeclaration `json:"flows,omitempty"`
	// ADRs are the entries of the top-level CALM `adrs` array, verbatim, in document order. They are candidates
	// for ADR governance only (never authority); omitted when the document has none.
	ADRs []string `json:"adrs,omitempty"`
}
