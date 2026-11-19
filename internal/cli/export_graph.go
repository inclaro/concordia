package cli

import (
	"fmt"
	"maps"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/redact"
)

// exportedEdge is an evidence fact with its identity: the edge key, environment, occurrence,
// image reference, and the possibly-lossy mark with the profile loss modes.
type exportedEdge struct {
	*observation.EvidenceEdge
	Identity observation.EdgeIdentity `json:"identity"`
}

// graphJSON encodes the evidence graph. The property values of the edges pass through redaction:
// the property names stay, a secret value becomes a marker. The records have the output set.
func graphJSON(run *conformance.Run) ([]byte, []redact.Record, error) {
	nodes := make([]any, 0)
	edges := make([]any, 0)

	var records []redact.Record

	if run.Evidence != nil {
		nodes = evidenceNodes(run.Evidence)
		edges, records = exportedEdges(run.Evidence)
	}

	for i := range records {
		records[i].Output = graphEvidencePath
	}

	data, err := canonjson.Marshal(map[string]any{"graphGeneration": run.Run.GraphGeneration, "profile": run.Toolchain.Cbm.Profile, "nodes": nodes, "edges": edges})
	if err != nil {
		return nil, nil, fmt.Errorf("encode graph: %w", err)
	}

	return data, records, nil
}

// evidenceNodes gives the nodes of the evidence in the order of their IDs.
func evidenceNodes(evidence *observation.EvidenceQueryResult) []any {
	ids := make([]string, 0, len(evidence.Nodes))
	for id := range evidence.Nodes {
		ids = append(ids, id)
	}

	slices.SortFunc(ids, canonjson.CompareUTF16)

	nodes := make([]any, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, evidence.Nodes[id])
	}

	return nodes
}

// exportedEdges gives the edges of the evidence with their identity and with redacted property and facet values,
// and the records of the values that were removed.
func exportedEdges(evidence *observation.EvidenceQueryResult) ([]any, []redact.Record) {
	edges := make([]any, 0, len(evidence.Edges))

	var records []redact.Record

	for _, e := range evidence.Edges {
		c := *e

		var props, facets []redact.Record

		c.Properties, props = redactValues(c.Properties)
		c.Facets, facets = redactValues(c.Facets)

		records = append(records, props...)
		records = append(records, facets...)
		edges = append(edges, exportedEdge{EvidenceEdge: &c, Identity: observation.IdentityOf(&c)})
	}

	return edges, records
}

// redactValues redacts the values of a property map, in key order, and gives a new map.
func redactValues(values map[string]string) (map[string]string, []redact.Record) {
	if values == nil {
		return nil, nil
	}

	rules := redact.Default()
	out := make(map[string]string, len(values))

	var records []redact.Record

	for _, key := range slices.Sorted(maps.Keys(values)) {
		value, recs := rules.RedactValue(key, values[key])
		out[key] = value

		records = append(records, recs...)
	}

	return out, records
}
