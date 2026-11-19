package observation

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
)

// OtelServicePrefix is the prefix of the observed ID of a runtime service node.
const OtelServicePrefix = "otel:service:"

// OtelAdapterSchema is the adapter schema of the runtime-trace evidence.
const OtelAdapterSchema = "concordia-otel/v1"

const (
	traceScanInitialBuffer = 1 << 20  // initial line buffer size in bytes
	traceScanMaxLine       = 64 << 20 // longest accepted trace line in bytes
	hoursPerDay            = 24
	floatBits              = 64
)

// RuntimeInput is the input of LoadRuntimeTraces.
type RuntimeInput struct {
	Now             time.Time
	Root            string // absolute workspace root
	Environment     string // "" = all environments
	GraphGeneration string
	Files           []string      // workspace-relative trace files, sorted
	MaxAge          time.Duration // 0 = no age limit
}

// RuntimeEvidence is the evidence of the runtime traces.
type RuntimeEvidence struct {
	Nodes   []ObservedNode
	Skipped string // "" when no span was skipped
	Edges   []*EvidenceEdge
}

type otlpAttribute struct {
	Value struct {
		StringValue string `json:"stringValue"`
	} `json:"value"`
	Key string `json:"key"`
}

type otlpSpan struct {
	TraceID      string          `json:"traceId"`
	SpanID       string          `json:"spanId"`
	ParentSpanID string          `json:"parentSpanId"`
	End          json.RawMessage `json:"endTimeUnixNano"`
}

type otlpRequest struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []otlpAttribute `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Spans []otlpSpan `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

type spanKey struct{ trace, span string }

type pairKey struct{ from, to string }

type pairInfo struct {
	file  string
	line  int
	count int
}

type pendingSpan struct {
	service string
	parent  string
	file    string
	trace   string
	line    int
}

func attribute(attrs []otlpAttribute, key string) string {
	for i := range attrs {
		if attrs[i].Key == key {
			return attrs[i].Value.StringValue
		}
	}

	return ""
}

func unixNano(raw json.RawMessage) (int64, bool) {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" {
		return 0, false
	}

	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false
	}

	return n, true
}

// LoadRuntimeTraces reads OTLP/JSON trace files (one ExportTraceServiceRequest per line)
// and makes service nodes and parent-span edges from them.
func LoadRuntimeTraces(input *RuntimeInput) (RuntimeEvidence, error) {
	scan := traceScan{services: map[spanKey]string{}, serviceSet: map[string]struct{}{}}

	for _, file := range input.Files {
		if err := scan.readFile(input, file); err != nil {
			return RuntimeEvidence{}, err
		}
	}

	return RuntimeEvidence{
		Nodes:   scan.serviceNodes(),
		Edges:   scan.callEdges(input.GraphGeneration),
		Skipped: scan.skippedSummary(input.MaxAge),
	}, nil
}

// traceScan accumulates the spans seen while reading the trace files.
type traceScan struct {
	services   map[spanKey]string
	serviceSet map[string]struct{}
	pending    []pendingSpan
	otherEnv   int
	tooOld     int
}

// readFile scans one trace file line by line.
func (s *traceScan) readFile(input *RuntimeInput, file string) error {
	data, err := os.ReadFile(filepath.Join(input.Root, filepath.FromSlash(file)))
	if err != nil {
		return diag.Fail(diag.OBS012, diag.Detail(file+": "+err.Error()))
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, traceScanInitialBuffer), traceScanMaxLine)

	for line := 1; scanner.Scan(); line++ {
		text := bytes.TrimSpace(scanner.Bytes())
		if len(text) == 0 {
			continue
		}

		var req otlpRequest
		if err := json.Unmarshal(text, &req); err != nil {
			return diag.Fail(diag.OBS012, diag.Detail(fmt.Sprintf("%s:%d: %s", file, line, err)))
		}

		s.addRequest(input, &req, file, line)
	}

	if err := scanner.Err(); err != nil {
		return diag.Fail(diag.OBS012, diag.Detail(file+": "+err.Error()))
	}

	return nil
}

// addRequest records the spans of one ExportTraceServiceRequest.
func (s *traceScan) addRequest(input *RuntimeInput, req *otlpRequest, file string, line int) {
	for _, rs := range req.ResourceSpans {
		service := attribute(rs.Resource.Attributes, "service.name")

		env := attribute(rs.Resource.Attributes, "deployment.environment.name")
		if env == "" {
			env = attribute(rs.Resource.Attributes, "deployment.environment")
		}

		for _, ss := range rs.ScopeSpans {
			for i := range ss.Spans {
				s.addSpan(input, &ss.Spans[i], service, env, pendingSpan{file: file, line: line})
			}
		}
	}
}

// addSpan counts or records one span; origin carries its file and line.
func (s *traceScan) addSpan(input *RuntimeInput, span *otlpSpan, service, env string, origin pendingSpan) {
	if input.Environment != "" && env != input.Environment {
		s.otherEnv++

		return
	}

	if end, ok := unixNano(span.End); input.MaxAge > 0 && ok && input.Now.Sub(time.Unix(0, end)) > input.MaxAge {
		s.tooOld++

		return
	}

	if service == "" {
		return
	}

	s.serviceSet[service] = struct{}{}
	s.services[spanKey{span.TraceID, span.SpanID}] = service

	if span.ParentSpanID != "" {
		origin.service = service
		origin.parent = span.ParentSpanID
		origin.trace = span.TraceID
		s.pending = append(s.pending, origin)
	}
}

// skippedSummary describes the spans that were not used ("" when none).
func (s *traceScan) skippedSummary(maxAge time.Duration) string {
	var skipped []string
	if s.otherEnv > 0 {
		skipped = append(skipped, fmt.Sprintf("%d span(s) of other environments", s.otherEnv))
	}

	if s.tooOld > 0 {
		skipped = append(skipped, fmt.Sprintf("%d span(s) older than %s days", s.tooOld, strconv.FormatFloat(maxAge.Hours()/hoursPerDay, 'f', -1, floatBits)))
	}

	return strings.Join(skipped, ", ")
}

// serviceNodes makes one node per service seen, sorted by observed ID.
func (s *traceScan) serviceNodes() []ObservedNode {
	nodes := make([]ObservedNode, 0, len(s.serviceSet))

	for name := range s.serviceSet {
		nodes = append(nodes, ObservedNode{ObservedID: OtelServicePrefix + name, Label: "Service", QualifiedName: name})
	}

	slices.SortFunc(nodes, func(a, b ObservedNode) int { return canonjson.CompareUTF16(a.ObservedID, b.ObservedID) })

	return nodes
}

// callEdges makes one edge per distinct caller/callee service pair, sorted.
func (s *traceScan) callEdges(graphGeneration string) []*EvidenceEdge {
	pairs := map[pairKey]*pairInfo{}

	for i := range s.pending {
		span := &s.pending[i]

		parent, ok := s.services[spanKey{span.trace, span.parent}]
		if !ok || parent == span.service {
			continue
		}

		key := pairKey{OtelServicePrefix + parent, OtelServicePrefix + span.service}
		if info, seen := pairs[key]; seen {
			info.count++
		} else {
			pairs[key] = &pairInfo{file: span.file, line: span.line, count: 1}
		}
	}

	edges := make([]*EvidenceEdge, 0, len(pairs))

	for key, info := range pairs {
		sum := sha256.Sum256([]byte(key.from + "\x00" + key.to))
		edges = append(edges, &EvidenceEdge{
			ID:               "otel-edge:" + hex.EncodeToString(sum[:])[:16],
			From:             key.from,
			To:               key.to,
			Kind:             KindCallsService,
			NativeKind:       "OTEL_CALL",
			Confidence:       ConfidenceRuntime,
			ResolutionMethod: "otel-parent-span",
			SourceSpan:       &SourceSpan{Path: info.file, Line: info.line},
			Analyzer:         AnalyzerDescriptor{Name: "otel-traces", Version: "1", AdapterSchema: OtelAdapterSchema},
			GraphGeneration:  graphGeneration,
			Properties:       map[string]string{"spanCount": strconv.Itoa(info.count)},
		})
	}

	slices.SortFunc(edges, func(a, b *EvidenceEdge) int {
		if c := canonjson.CompareUTF16(a.From, b.From); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.To, b.To)
	})

	return edges
}
