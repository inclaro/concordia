package render_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/redact"
)

const updateGoldenEnv = "CONCORDIA_UPDATE_GOLDEN"

// golden compares the canonical JSON of v with test/golden/assurance/<name>. CONCORDIA_UPDATE_GOLDEN=1 rewrites it.
func golden(t *testing.T, name string, v any) {
	t.Helper()

	got, err := canonjson.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(repoRoot(t), "test", "golden", "assurance", name)

	if os.Getenv(updateGoldenEnv) != "" {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(file, got, 0o644); err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with %s=1 to create it)", name, err, updateGoldenEnv)
	}

	if !bytes.Equal(want, got) {
		t.Errorf("golden %s differs (run with %s=1 to update it)\n--- want\n%s\n--- got\n%s", name, updateGoldenEnv, want, got)
	}
}

// seeds are the fake secret values of the plain fixture. They must not be in any output.
var seeds = []string{
	"ASSURANCE-SEED-secret-9a1c",
	"ASSURANCE-SEED-env-value-4b7e",
	"QVNTVVJBTkNFLVNFRUQtZGF0YS0yZjZk",
}

// TestSecretValuesNeverEnterTheEvidence is the golden check of A38 for rendered evidence: no password appears in
// the serialized result or in the redacted documents; the Secret name and key names stay; every redaction is
// recorded without its value.
func TestSecretValuesNeverEnterTheEvidence(t *testing.T) {
	t.Parallel()

	res := renderPlainFixture(t, "production", "plain")

	serialized, err := canonjson.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}

	documents, err := json.Marshal(documentsOf(res))
	if err != nil {
		t.Fatal(err)
	}

	for _, seed := range seeds {
		if strings.Contains(string(serialized), seed) || strings.Contains(string(documents), seed) {
			t.Errorf("the value %q is in the evidence", seed)
		}
	}

	secret := res.Resource(&render.ResourceID{Environment: "production", Cluster: "prod-eu", Kind: "Secret", Namespace: "payments", Name: "api-secrets"})
	if secret == nil {
		t.Fatal("the Secret was dropped: its name must stay")
	}

	if strings.Join(secret.DataKeys, ",") != "password,token" {
		t.Errorf("secret keys = %v", secret.DataKeys)
	}

	// The unquoted marker is a one-item YAML flow list; its text is the marker.
	stringData, ok := secret.Document["stringData"].(map[string]any)
	if !ok {
		t.Fatalf("stringData = %v", secret.Document["stringData"])
	}

	if got := fmt.Sprint(stringData["password"]); got != redact.Marker(redact.ClassSecretData) {
		t.Errorf("stringData.password = %v", got)
	}

	golden(t, "plain-redactions.json", res.Redactions)
}

// documentsOf gives the redacted documents of a result, the only place where document content is kept.
func documentsOf(res *render.Result) []map[string]any {
	out := make([]map[string]any, 0, len(res.Resources))
	for i := range res.Resources {
		out = append(out, res.Resources[i].Document)
	}

	return out
}

func TestRedactionsNeverHoldAValue(t *testing.T) {
	t.Parallel()

	res := renderPlainFixture(t, "production", "plain")
	if len(res.Redactions) == 0 {
		t.Fatal("no redaction record")
	}

	for _, rec := range res.Redactions {
		raw, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}

		for _, seed := range seeds {
			if strings.Contains(string(raw), seed) {
				t.Errorf("record %s holds a value", raw)
			}
		}

		if rec.File == "" || rec.Class == "" {
			t.Errorf("record %+v has no file or class", rec)
		}
	}
}
