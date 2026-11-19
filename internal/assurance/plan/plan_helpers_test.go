package plan_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/diag"
)

const fixtures = "../../../test/fixtures/assurance/plans"

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func read(t *testing.T, name string) *plan.Plan {
	t.Helper()

	got, diags := plan.Read(fixture(t, name), "plans/"+name)
	if got == nil || len(diags) != 0 {
		t.Fatalf("Read(%s) = %v, %+v", name, got, diags)
	}

	return got
}

// readInline reads plan JSON that a test holds as text, and fails when the reader rejects it.
func readInline(t *testing.T, data string) *plan.Plan {
	t.Helper()

	p, diags := plan.Read([]byte(data), "p.json")
	if p == nil || len(diags) != 0 {
		t.Fatalf("Read = %v, %+v", p, diags)
	}

	return p
}

func resource(t *testing.T, p *plan.Plan, address string) plan.Resource {
	t.Helper()

	for i := range p.Resources {
		if p.Resources[i].Address == address {
			return p.Resources[i]
		}
	}

	t.Fatalf("no resource %s in %v", address, p.Resources)

	return plan.Resource{}
}

func marshal(t *testing.T, value any) []byte {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return encoded
}

// objectAt is the value as an object, or nil when it is not one.
func objectAt(value any) map[string]any {
	object, _ := value.(map[string]any)

	return object
}

func addressesOf(p *plan.Plan) []string {
	addresses := make([]string, 0, len(p.Resources))
	for i := range p.Resources {
		addresses = append(addresses, p.Resources[i].Address)
	}

	return addresses
}

func propertiesOf(p *plan.Plan) []string {
	properties := make([]string, 0, len(p.Redactions))
	for _, r := range p.Redactions {
		properties = append(properties, r.Property)
	}

	return properties
}

// isLogsResource is whether the resource is the planned creation of terraform_data.logs.
func isLogsResource(r *plan.Resource) bool {
	if r.Mode != "managed" || r.Type != "terraform_data" {
		return false
	}

	return r.Name == "logs" && slices.Equal(r.Actions, []string{"create"})
}

// isLogsInput is whether the planned input of terraform_data.logs has the values of the fixture.
func isLogsInput(input map[string]any) bool {
	if input["name"] != "logs-bucket" || input["versioning"] != true {
		return false
	}

	return reflect.DeepEqual(input["tags"], []any{"audit"})
}

// isCreateFixtureHeader is whether the plan has the version, coverage and digest of terraform-1.2-create.json.
func isCreateFixtureHeader(p *plan.Plan) bool {
	if p.FormatVersion != "1.2" || p.ToolVersion != "1.17.0" {
		return false
	}

	return p.Coverage == assurance.CoverageComplete && strings.HasPrefix(p.Digest, "sha256:")
}

// isKnownAttributeGap is whether the gap names an attribute that the plan knows, which is never a gap.
func isKnownAttributeGap(g assurance.Gap) bool {
	return strings.Contains(g.Detail, "input.engine") || strings.Contains(g.Detail, "tags[0]")
}

// isBareDestroy is whether the plan lists three resources and has neither gaps nor redactions.
func isBareDestroy(p *plan.Plan) bool {
	if len(p.Resources) != 3 {
		return false
	}

	return len(p.Gaps) == 0 && len(p.Redactions) == 0
}

// isPRV008Warning is whether the diagnostics are exactly one CO-PRV-008 warning.
func isPRV008Warning(diags []diag.Diagnostic) bool {
	if len(diags) != 1 {
		return false
	}

	return diags[0].Code == diag.PRV008 && diags[0].Severity == diag.SeverityWarning
}

// isRejectionOf is whether the diagnostic names the detail and the file of the read.
func isRejectionOf(d *diag.Diagnostic, detail string) bool {
	if !strings.Contains(d.Message, detail) {
		return false
	}

	return d.Source != nil && d.Source.URI == "plans/p.json"
}

// acceptsVersion is whether the read gave an empty plan of the format version and no diagnostics.
func acceptsVersion(p *plan.Plan, diags []diag.Diagnostic, version string) bool {
	if p == nil || len(diags) != 0 {
		return false
	}

	return p.FormatVersion == version && len(p.Resources) == 0
}

// isIncompleteCoverage is whether the plan is partial with the one gap of an incomplete plan.
func isIncompleteCoverage(p *plan.Plan) bool {
	if p.Coverage != assurance.CoveragePartial || len(p.Gaps) != 1 {
		return false
	}

	return p.Gaps[0].Kind == plan.GapIncompletePlan
}

// assertRejected reads data and checks that the reader gives no plan and one warning that names the detail.
func assertRejected(t *testing.T, data []byte, wantDetail string) {
	t.Helper()

	p, diags := plan.Read(data, "plans/p.json")
	if p != nil {
		t.Fatalf("plan = %+v", p)
	}

	if !isPRV008Warning(diags) {
		t.Fatalf("diagnostics = %+v", diags)
	}

	if !isRejectionOf(&diags[0], wantDetail) {
		t.Errorf("diagnostic = %+v", diags[0])
	}
}

// assertNoneIn fails for each secret that the encoded output holds.
func assertNoneIn(t *testing.T, encoded []byte, secrets ...string) {
	t.Helper()

	for _, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the output holds %q", secret)
		}
	}
}
