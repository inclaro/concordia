package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
)

const updateGoldenEnv = "CONCORDIA_UPDATE_GOLDEN"

type vector struct {
	Fn     string          `json:"fn"`
	Result string          `json:"result"`
	Input  json.RawMessage `json:"input"`
}

func fingerprintGolden() string {
	return filepath.Join("..", "..", "test", "golden", "fingerprints.json")
}

// TestFingerprintVectors checks that every stored input gives its stored fingerprint.
// `CONCORDIA_UPDATE_GOLDEN=1` rewrites the stored results; do that only together with a
// deliberate change of the fingerprint definition.
func TestFingerprintVectors(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(fingerprintGolden())
	if err != nil {
		t.Fatal(err)
	}

	var vectors []vector
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}

	if len(vectors) < 6 {
		t.Fatalf("expected at least 6 vectors, got %d", len(vectors))
	}

	if os.Getenv(updateGoldenEnv) != "" {
		stored := make([]map[string]any, len(vectors))

		for i := range vectors {
			vectors[i].Result = runVector(t, vectors[i])
			// A map encodes its keys sorted: fn, input, result.
			stored[i] = map[string]any{"fn": vectors[i].Fn, "input": vectors[i].Input, "result": vectors[i].Result}
		}

		var out bytes.Buffer

		enc := json.NewEncoder(&out)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")

		if err := enc.Encode(stored); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(fingerprintGolden(), out.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}

		return
	}

	for i, v := range vectors {
		got := runVector(t, v)
		if got != v.Result {
			t.Errorf("vector %d (%s %s): got %s, want %s", i, v.Fn, v.Input, got, v.Result)
		}
	}
}

type fingerprintInput struct {
	RuleID                   string `json:"ruleId"`
	SourceArchitectureID     string `json:"sourceArchitectureId"`
	TargetArchitectureID     string `json:"targetArchitectureId"`
	CanonicalEdgeKind        string `json:"canonicalEdgeKind"`
	SourceQualifiedName      string `json:"sourceQualifiedName"`
	TargetQualifiedName      string `json:"targetQualifiedName"`
	NormalizedRepositoryPath string `json:"normalizedRepositoryPath"`
}

func (in *fingerprintInput) parts() conformance.Parts { return conformance.Parts(*in) }

func runVector(t *testing.T, v vector) string {
	t.Helper()

	switch v.Fn {
	case "fingerprint":
		var in fingerprintInput

		decode(t, v.Input, &in)

		return conformance.Fingerprint(in.parts())
	case "typedFingerprint":
		var in struct {
			fingerprintInput
			Environment string `json:"environment"`
			Occurrence  string `json:"occurrence"`
		}

		decode(t, v.Input, &in)

		return conformance.TypedFingerprint(&conformance.TypedParts{
			Parts: in.parts(), Environment: in.Environment, Occurrence: in.Occurrence,
		})
	case "cycleFingerprint":
		var in struct {
			RuleID   string   `json:"ruleId"`
			CycleIDs []string `json:"cycleIds"`
		}

		decode(t, v.Input, &in)

		return conformance.CycleFingerprint(in.RuleID, in.CycleIDs)
	case "unobservedFingerprint":
		var in struct {
			RuleID         string   `json:"ruleId"`
			SourceID       string   `json:"sourceId"`
			RelationshipID string   `json:"relationshipId"`
			TargetIDs      []string `json:"targetIds"`
		}

		decode(t, v.Input, &in)

		return conformance.UnobservedFingerprint(in.RuleID, in.SourceID, in.TargetIDs, in.RelationshipID)
	default:
		t.Fatalf("unknown vector function %q", v.Fn)

		return ""
	}
}

func decode(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()

	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatal(err)
	}
}

func typedParts(env, occurrence string) *conformance.TypedParts {
	return &conformance.TypedParts{
		Parts: conformance.Parts{
			RuleID: "CO-POL-020", SourceArchitectureID: "a", TargetArchitectureID: "b", CanonicalEdgeKind: "routes-to",
			SourceQualifiedName: "q1", TargetQualifiedName: "q2", NormalizedRepositoryPath: "k8s/ingress.yaml",
		},
		Environment: env, Occurrence: occurrence,
	}
}

func TestTypedFingerprintSeparatesEnvironments(t *testing.T) {
	t.Parallel()

	staging := conformance.TypedFingerprint(typedParts("staging", ""))
	production := conformance.TypedFingerprint(typedParts("production", ""))

	if staging == production {
		t.Fatalf("one fingerprint %s for two environments", staging)
	}

	if again := conformance.TypedFingerprint(typedParts("staging", "")); again != staging {
		t.Fatalf("fingerprint is not stable: %s vs %s", again, staging)
	}
}

func TestTypedFingerprintSeparatesOccurrences(t *testing.T) {
	t.Parallel()

	api := conformance.TypedFingerprint(typedParts("production", "container=api"))
	worker := conformance.TypedFingerprint(typedParts("production", "container=worker"))

	if api == worker {
		t.Fatalf("one fingerprint %s for two occurrences", api)
	}
}

func TestFingerprintsCarryAVersionTagAndDiffer(t *testing.T) {
	t.Parallel()

	parts := typedParts("", "")
	plain := conformance.Fingerprint(parts.Parts)
	typed := conformance.TypedFingerprint(parts)

	if plain == typed {
		t.Fatal("a typed finding with no environment and no occurrence has the fingerprint of an untyped finding")
	}
}

func TestFingerprintUsesSemanticKind(t *testing.T) {
	t.Parallel()

	parts := typedParts("", "").Parts
	other := parts
	other.CanonicalEdgeKind = "uses-image"

	if conformance.Fingerprint(parts) == conformance.Fingerprint(other) {
		t.Fatal("two semantic kinds give one fingerprint")
	}
}
