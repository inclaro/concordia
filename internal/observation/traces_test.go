package observation_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
)

var traceNow = time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)

// traceLine makes one export request with one resource and the spans "service|span|parent|end-time".
func traceLine(service, env string, spans ...[3]string) string {
	items := make([]string, 0, len(spans))
	for _, s := range spans {
		items = append(items, fmt.Sprintf(`{"traceId":"t1","spanId":%q,"parentSpanId":%q,"endTimeUnixNano":%q}`, s[0], s[1], s[2]))
	}

	return fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":%q}},`+
		`{"key":"deployment.environment.name","value":{"stringValue":%q}}]},"scopeSpans":[{"spans":[%s]}]}]}`,
		service, env, strings.Join(items, ","))
}

func writeTrace(t *testing.T, lines ...string) string {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "t.jsonl"), []byte(strings.Join(lines, "\n")+"\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return root
}

func nanos(at time.Time) string { return strconv.FormatInt(at.UnixNano(), 10) }

func TestRuntimeTracesGiveOneEdgeForAParentAndChildSpan(t *testing.T) {
	t.Parallel()

	end := nanos(traceNow)
	root := writeTrace(t,
		traceLine("billing", "prod", [3]string{"s1", "", end}),
		traceLine("fulfillment", "prod", [3]string{"s2", "s1", end}),
	)

	got, err := observation.LoadRuntimeTraces(&observation.RuntimeInput{
		Root: root, Files: []string{"t.jsonl"}, Now: traceNow, GraphGeneration: "g1",
	})
	if err != nil {
		t.Fatal(err)
	}

	attrRequireEqual(t, "node count", len(got.Nodes), 2)
	attrRequireEqual(t, "first node", got.Nodes[0].ObservedID, "otel:service:billing")
	attrRequireEqual(t, "edge count", len(got.Edges), 1)

	e := got.Edges[0]
	attrEqual(t, "edge from", e.From, "otel:service:billing")
	attrEqual(t, "edge to", e.To, "otel:service:fulfillment")
	attrEqual(t, "edge kind", e.Kind, observation.KindCallsService)
	attrEqual(t, "edge confidence", e.Confidence, observation.ConfidenceRuntime)
	attrEqual(t, "edge spanCount", e.Properties["spanCount"], "1")
	attrEqual(t, "edge source line", e.SourceSpan.Line, 2)
}

func TestRuntimeTracesEnvironmentFilterSkipsSpans(t *testing.T) {
	t.Parallel()

	end := nanos(traceNow)
	root := writeTrace(t,
		traceLine("billing", "prod", [3]string{"s1", "", end}),
		traceLine("fulfillment", "dev", [3]string{"s2", "s1", end}),
	)

	got, err := observation.LoadRuntimeTraces(&observation.RuntimeInput{
		Root: root, Files: []string{"t.jsonl"}, Environment: "prod", Now: traceNow,
	})
	if err != nil {
		t.Fatal(err)
	}

	attrEqual(t, "edge count", len(got.Edges), 0)
	attrEqual(t, "skipped", got.Skipped, "1 span(s) of other environments")
}

func TestRuntimeTracesAgeFilterSkipsOldSpans(t *testing.T) {
	t.Parallel()

	old := nanos(traceNow.Add(-72 * time.Hour))
	root := writeTrace(t, traceLine("billing", "prod", [3]string{"s1", "", old}))

	got, err := observation.LoadRuntimeTraces(&observation.RuntimeInput{
		Root: root, Files: []string{"t.jsonl"}, MaxAge: 24 * time.Hour, Now: traceNow,
	})
	if err != nil {
		t.Fatal(err)
	}

	attrEqual(t, "node count", len(got.Nodes), 0)
	attrEqual(t, "skipped", got.Skipped, "1 span(s) older than 1 days")
}

func TestRuntimeTracesBadLineGivesOBS012WithFileAndLine(t *testing.T) {
	t.Parallel()

	root := writeTrace(t, traceLine("billing", "prod"), "{not json")

	_, err := observation.LoadRuntimeTraces(&observation.RuntimeInput{Root: root, Files: []string{"t.jsonl"}, Now: traceNow})

	var derr *diag.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %v", err)
	}

	attrRequireEqual(t, "diagnostic code", derr.Diagnostic.Code, diag.OBS012)

	if !strings.Contains(derr.Diagnostic.Message, "t.jsonl:2:") {
		t.Errorf("message = %q", derr.Diagnostic.Message)
	}
}
