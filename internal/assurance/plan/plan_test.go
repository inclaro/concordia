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

// terraform-1.2-references.json is a handwritten plan whose group IDs are unknown until apply and whose
// `configuration` section names the groups that each attribute refers to.
func TestReferencesResolveAnUnknownGroupIDThroughTheConfiguration(t *testing.T) {
	t.Parallel()

	p := read(t, "terraform-1.2-references.json")
	rule := resource(t, p, "aws_vpc_security_group_ingress_rule.x")

	if !slices.Contains(rule.Unknown, "referenced_security_group_id") {
		t.Fatalf("unknown = %v", rule.Unknown)
	}

	want := map[string][]string{
		"referenced_security_group_id": {"aws_security_group.gateway"},
		"security_group_id":            {"aws_security_group.db"},
	}
	if !reflect.DeepEqual(rule.References, want) {
		t.Errorf("references = %v, want %v", rule.References, want)
	}

	// A nested block has the path form of Unknown; a local stays as an opaque entry; a data source keeps its address.
	db := resource(t, p, "aws_security_group.db")

	wantDB := map[string][]string{
		"ingress[0].security_groups": {"aws_security_group.gateway", "local.extra_groups"},
		"vpc_id":                     {"data.aws_vpc.main"},
	}
	if !reflect.DeepEqual(db.References, wantDB) {
		t.Errorf("references = %v, want %v", db.References, wantDB)
	}

	// A variable is kept as an opaque entry, never as its value.
	gateway := resource(t, p, "aws_security_group.gateway")
	if want := map[string][]string{"vpc_id": {"var.vpc_id"}}; !reflect.DeepEqual(gateway.References, want) {
		t.Errorf("references = %v, want %v", gateway.References, want)
	}
}

func TestReferencesInAModuleAreAbsoluteAndShared(t *testing.T) {
	t.Parallel()

	p := read(t, "terraform-1.2-references.json")

	want := map[string][]string{"vpc_id": {"module.net.aws_vpc.this"}}
	for _, address := range []string{"module.net.aws_security_group.web[0]", "module.net.aws_security_group.web[1]"} {
		if web := resource(t, p, address); !reflect.DeepEqual(web.References, want) {
			t.Errorf("%s references = %v, want %v", address, web.References, want)
		}
	}
}

func TestReferencesNeverCarryAConstantValue(t *testing.T) {
	t.Parallel()

	if !strings.Contains(string(fixture(t, "terraform-1.2-references.json")), `"constant_value": "hunter2-config-secret"`) {
		t.Fatal("the fixture no longer holds the secret as a constant value")
	}

	encoded := marshal(t, read(t, "terraform-1.2-references.json"))
	if strings.Contains(string(encoded), "hunter2") {
		t.Fatalf("the output holds a constant value: %s", encoded)
	}
}

func TestReferencesAreNilWithoutAUsableConfiguration(t *testing.T) {
	t.Parallel()

	change := `"resource_changes":[{"address":"a_b.c","mode":"managed","type":"a_b","name":"c",
		"change":{"actions":["create"],"after":{},"after_unknown":{"id":true},"after_sensitive":{}}}]`

	for _, tc := range []struct{ name, configuration string }{
		{"missing", ""},
		{"malformed", `,"configuration":{"root_module":{"resources":"not a list"}}`},
		{"null", `,"configuration":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := readInline(t, `{"format_version":"1.2",`+change+tc.configuration+`}`)
			if p.Resources[0].References != nil {
				t.Errorf("references = %v, want nil", p.Resources[0].References)
			}
		})
	}
}

// A reference through a variable, a local, `each.key` or `each.value` can carry a resource ID that the plan does not
// name, so it stays in the list as an opaque entry: a consumer must not take the rest of the list for the whole of it.
func TestReferencesKeepOpaqueVariableLocalAndEachEntries(t *testing.T) {
	t.Parallel()

	p := readInline(t, `{"format_version":"1.2",
		"resource_changes":[{"address":"module.net.aws_instance.app[\"a\"]","module_address":"module.net","mode":"managed",
			"type":"aws_instance","name":"app","change":{"actions":["create"],"after":{},"after_unknown":{"vpc_security_group_ids":true},"after_sensitive":{}}}],
		"configuration":{"root_module":{"module_calls":{"net":{"module":{"resources":[{"address":"aws_instance.app",
			"expressions":{
				"vpc_security_group_ids":{"references":["aws_security_group.this.id","aws_security_group.this",
					"var.extra_security_group_ids","local.groups[0]","each.value.id","each.value","each.key","count.index",
					"path.module","self.id","terraform.workspace"]}
			}}]}}}}}}`)

	want := map[string][]string{"vpc_security_group_ids": {
		"module.net.aws_security_group.this", "module.net.each.key", "module.net.each.value", "module.net.local.groups",
		"module.net.var.extra_security_group_ids",
	}}
	if got := p.Resources[0].References; !reflect.DeepEqual(got, want) {
		t.Errorf("references = %v, want %v", got, want)
	}
}

// A reference that reads an attribute that does not identify its resource, such as a tag, gives a value that may be
// the ID of any resource: the reader keeps the attribute in the entry. An identifying attribute (`id`, `name`) is cut.
func TestReferencesKeepNonIdentityAttributes(t *testing.T) {
	t.Parallel()

	p := readInline(t, `{"format_version":"1.2",
		"resource_changes":[{"address":"aws_instance.app","mode":"managed","type":"aws_instance","name":"app",
			"change":{"actions":["create"],"after":{},"after_unknown":{"vpc_security_group_ids":true},"after_sensitive":{}}}],
		"configuration":{"root_module":{"resources":[{"address":"aws_instance.app","expressions":{
			"vpc_security_group_ids":{"references":["aws_security_group.a.tags[\"peer_id\"]","aws_security_group.a.tags",
				"aws_security_group.a","aws_security_group.b.id","aws_security_group.b","var.x.id","local.y.z"]}
		}}]}}}`)

	want := map[string][]string{"vpc_security_group_ids": {
		"aws_security_group.a", "aws_security_group.a.tags", "aws_security_group.b", "local.y", "var.x",
	}}
	if got := p.Resources[0].References; !reflect.DeepEqual(got, want) {
		t.Errorf("references = %v, want %v", got, want)
	}
}

func TestReferencesNameAttributeEntries(t *testing.T) {
	t.Parallel()

	for address, want := range map[string]bool{
		"aws_security_group.a.tags":            true,
		"module.net.aws_security_group.a.tags": true,
		"aws_security_group.a":                 false,
		"data.aws_vpc.main.tags":               false,
		"var.extra":                            false,
		"each.key":                             false,
		"module.net":                           false,
	} {
		if got := plan.IsAttributeReference(address); got != want {
			t.Errorf("IsAttributeReference(%q) = %v, want %v", address, got, want)
		}
	}
}

func TestReferencesNameOpaqueEntries(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		address string
		want    bool
	}{
		{"var.extra", true},
		{"local.groups", true},
		{"each.value", true},
		{"each.key", true},
		{"module.net.var.extra", true},
		{"module.a.module.b.local.groups", true},
		{"module.net.each.value", true},
		{"aws_security_group.this", false},
		{"module.net.aws_security_group.this", false},
		{"data.aws_vpc.main", false},
		{"module.var", false},
		{"module.net", false},
	} {
		t.Run(tc.address, func(t *testing.T) {
			t.Parallel()

			if got := plan.IsOpaqueReference(tc.address); got != tc.want {
				t.Errorf("IsOpaqueReference(%q) = %v, want %v", tc.address, got, tc.want)
			}
		})
	}
}

func TestConfigAddressRemovesInstanceKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, address, want string }{
		{"no key", "aws_security_group.db", "aws_security_group.db"},
		{"count index", "aws_security_group.web[1]", "aws_security_group.web"},
		{"module and string key", `module.net["a"].aws_instance.app["x.y]"]`, "module.net.aws_instance.app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := plan.ConfigAddress(tc.address); got != tc.want {
				t.Errorf("ConfigAddress(%q) = %q, want %q", tc.address, got, tc.want)
			}
		})
	}
}
