package plan_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/redact"
)

// terraform-1.2-create.json is the output of `terraform show -json` of Terraform 1.17.0 for a saved plan of three
// terraform_data resources (one in a module), one with a sensitive variable.
func TestReadsPlannedResourcesByAddress(t *testing.T) {
	t.Parallel()

	p := read(t, "terraform-1.2-create.json")

	addresses := addressesOf(p)

	want := []string{"module.child.terraform_data.inner", "terraform_data.db", "terraform_data.logs"}
	if !slices.Equal(addresses, want) {
		t.Fatalf("addresses = %v, want %v", addresses, want)
	}

	logs := resource(t, p, "terraform_data.logs")
	if !isLogsResource(&logs) {
		t.Errorf("logs = %+v", logs)
	}

	if !isLogsInput(objectAt(logs.Values["input"])) {
		t.Errorf("planned values = %v", logs.Values)
	}

	if inner := resource(t, p, "module.child.terraform_data.inner"); inner.Module != "module.child" {
		t.Errorf("module = %q", inner.Module)
	}

	if !isCreateFixtureHeader(p) {
		t.Errorf("plan = %+v", p)
	}
}

func TestAnAttributeThatIsUnknownUntilApplyIsAGapAndHasNoValue(t *testing.T) {
	t.Parallel()

	p := read(t, "terraform-1.2-create.json")
	db := resource(t, p, "terraform_data.db")

	if want := []string{"id", "input.owner", "output"}; !slices.Equal(db.Unknown, want) {
		t.Fatalf("unknown = %v, want %v", db.Unknown, want)
	}

	if _, guessed := db.Values["id"]; guessed {
		t.Error("an unknown attribute has a value")
	}

	if input := objectAt(db.Values["input"]); input["owner"] != nil || input["engine"] != "postgres" {
		t.Errorf("input = %v", db.Values["input"])
	}

	gap := assurance.Gap{
		Kind: plan.GapUnknownAfterApply, Detail: "terraform_data.db: input.owner is unknown until apply", Path: "plans/terraform-1.2-create.json",
	}
	if !slices.Contains(p.Gaps, gap) {
		t.Errorf("gaps = %+v, want %+v", p.Gaps, gap)
	}

	// A known attribute of the same resource is not a gap.
	for _, g := range p.Gaps {
		if isKnownAttributeGap(g) {
			t.Errorf("known attribute reported as a gap: %+v", g)
		}
	}
}

func TestASensitiveValueIsAbsentFromTheOutputAndRecorded(t *testing.T) {
	t.Parallel()

	// The fixture holds the secret in `variables`, `planned_values`, and `resource_changes`.
	if !strings.Contains(string(fixture(t, "terraform-1.2-create.json")), "hunter2-plan-secret") {
		t.Fatal("the fixture no longer holds the secret")
	}

	p := read(t, "terraform-1.2-create.json")

	encoded := marshal(t, p)
	if strings.Contains(string(encoded), "hunter2") {
		t.Fatalf("the output holds the sensitive value: %s", encoded)
	}

	input := objectAt(resource(t, p, "terraform_data.db").Values["input"])
	if input["password"] != redact.Marker(redact.ClassSecretData) {
		t.Errorf("password = %v", input["password"])
	}

	want := redact.Record{File: "plans/terraform-1.2-create.json", Property: "terraform_data.db.input.password", Class: redact.ClassSecretData}
	if !slices.Equal(p.Redactions, []redact.Record{want}) {
		t.Errorf("redactions = %+v", p.Redactions)
	}
}

func TestRedactsASensitiveSubtreeAndKeepsTheRest(t *testing.T) {
	t.Parallel()

	p := read(t, "handwritten-s3-update.json")

	assertNoneIn(t, marshal(t, p), "acme-secret-kms-key-material", "plain-db-password-1234")

	bucket := resource(t, p, "aws_s3_bucket.logs")
	if bucket.Values["server_side_encryption_configuration"] != redact.Marker(redact.ClassSecretData) {
		t.Errorf("subtree = %v", bucket.Values["server_side_encryption_configuration"])
	}

	if bucket.Values["bucket"] != "acme-logs" {
		t.Errorf("bucket = %v", bucket.Values["bucket"])
	}

	if want := []string{"arn", "cors_rule[0]", "id"}; !slices.Equal(bucket.Unknown, want) {
		t.Errorf("unknown = %v, want %v", bucket.Unknown, want)
	}

	properties := propertiesOf(p)

	want := []string{"aws_db_instance.main.password", "aws_s3_bucket.logs.server_side_encryption_configuration"}
	if !slices.Equal(properties, want) {
		t.Errorf("redacted = %v, want %v", properties, want)
	}
}

func TestKeepsModuleAndIndexInTheAddressAndNumbers(t *testing.T) {
	t.Parallel()

	p := read(t, "handwritten-s3-update.json")

	sg := resource(t, p, `module.net.aws_security_group.web["public"]`)
	if sg.Module != "module.net" || sg.Values["timeout"] != json.Number("30") {
		t.Errorf("security group = %+v", sg)
	}

	wantIngress := []any{map[string]any{
		"from_port": json.Number("443"), "to_port": json.Number("443"), "cidr_blocks": []any{"0.0.0.0/0"},
	}}
	if !reflect.DeepEqual(sg.Values["ingress"], wantIngress) {
		t.Errorf("ingress = %v", sg.Values["ingress"])
	}
}

func TestADestroyPlanHasResourcesWithoutValues(t *testing.T) {
	t.Parallel()

	p := read(t, "terraform-1.2-destroy.json")

	if !isBareDestroy(p) {
		t.Fatalf("plan = %+v", p)
	}

	for i := range p.Resources {
		r := &p.Resources[i]
		if r.Values != nil || !slices.Equal(r.Actions, []string{"delete"}) {
			t.Errorf("resource = %+v", *r)
		}
	}
}

func TestRejectsAFileThatIsNotAUsablePlan(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, file, data, wantDetail string
	}{
		{"unsupported format version", "unsupported-version.json", "", "format version 0.1"},
		{"state file", "terraform-1.0-state.json", "", "is a state"},
		{"not json", "", "not json", "not valid JSON"},
		{"no format version", "", `{"planned_values":{}}`, "no format_version"},
		{"later major version", "", `{"format_version":"2.0","planned_values":{}}`, "format version 2.0"},
		{"no plan content", "", `{"format_version":"1.2"}`, "not a plan"},
		{"errored", "", `{"format_version":"1.2","planned_values":{},"errored":true}`, "errored"},
		{"change without address", "", `{"format_version":"1.2","resource_changes":[{"change":{}}]}`, "resource_changes[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := []byte(tc.data)
			if tc.file != "" {
				data = fixture(t, tc.file)
			}

			assertRejected(t, data, tc.wantDetail)
		})
	}
}

func TestSupportedFormatVersionsAreAccepted(t *testing.T) {
	t.Parallel()

	for _, version := range plan.SupportedFormatVersions {
		p, diags := plan.Read([]byte(`{"format_version":"`+version+`","resource_changes":[]}`), "p.json")
		if !acceptsVersion(p, diags, version) {
			t.Errorf("version %s: %+v %+v", version, p, diags)
		}
	}
}

func TestAnIncompletePlanIsPartial(t *testing.T) {
	t.Parallel()

	p := readInline(t, `{"format_version":"1.2","complete":false,"resource_changes":[]}`)

	if !isIncompleteCoverage(p) {
		t.Errorf("plan = %+v", p)
	}
}

func TestRedactsACredentialThatThePlanDoesNotMarkSensitive(t *testing.T) {
	t.Parallel()

	p := readInline(t, `{"format_version":"1.2","resource_changes":[{"address":"x.y","mode":"managed","type":"x","name":"y","change":{
		"actions":["create"],"after":{"db_password":"unmarked-secret-value","region":"eu-west-1"},"after_unknown":{},"after_sensitive":{}}}]}`)

	values := p.Resources[0].Values
	if values["db_password"] != redact.Marker(redact.ClassSecretData) || values["region"] != "eu-west-1" {
		t.Errorf("values = %v", values)
	}

	if len(p.Redactions) != 1 || p.Redactions[0].Property != "x.y.db_password" {
		t.Errorf("redactions = %+v", p.Redactions)
	}
}

func TestReadingTwiceGivesTheSameResult(t *testing.T) {
	t.Parallel()

	data := fixture(t, "handwritten-s3-update.json")
	a, _ := plan.Read(data, "p.json")
	b, _ := plan.Read(data, "p.json")

	left := marshal(t, a)
	right := marshal(t, b)

	if !bytes.Equal(left, right) {
		t.Errorf("results differ:\n%s\n%s", left, right)
	}
}
