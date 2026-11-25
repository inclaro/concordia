package plan_test

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// The reader records the keys that the configuration sets for each resource: top-level keys, the keys inside
// nested blocks in the path form of References, and the resources of module calls by their absolute address.
func TestConfiguredListsTheKeysThatTheConfigurationSets(t *testing.T) {
	t.Parallel()

	modules := read(t, "terraform-1.2-references.json")
	if got := resource(t, modules, "module.net.aws_security_group.web[1]").Configured; !slices.Equal(got, []string{"name", "vpc_id"}) {
		t.Errorf("module resource configured = %v", got)
	}

	data, err := os.ReadFile("../../../test/fixtures/assurance/plans/real/aws-create/plan.json")
	if err != nil {
		t.Fatal(err)
	}

	recorded, diags := plan.Read(data, "aws-create/plan.json")
	if recorded == nil {
		t.Fatalf("plan not read: %v", diags)
	}

	want := []string{
		"ami", "instance_type", "network_interface", "network_interface[0].device_index",
		"network_interface[0].network_interface_id",
	}
	if got := resource(t, recorded, "aws_instance.bastion").Configured; !slices.Equal(got, want) {
		t.Errorf("nested block configured = %v, want %v", got, want)
	}
}

// Configured holds key names only, never a value, and is nil for a resource that the configuration does not cover.
func TestConfiguredHoldsNoValueAndIsNilWithoutConfiguration(t *testing.T) {
	t.Parallel()

	p := read(t, "terraform-1.2-references.json")
	for i := range p.Resources {
		for _, key := range p.Resources[i].Configured {
			if strings.Contains(key, "hunter2") {
				t.Errorf("%s: configured key %q holds a value", p.Resources[i].Address, key)
			}
		}
	}

	bare := readInline(t, `{"format_version":"1.2","resource_changes":[{"address":"a_b.c","mode":"managed","type":"a_b",`+
		`"name":"c","change":{"actions":["create"],"after":{},"after_unknown":{},"after_sensitive":{}}}]}`)
	if got := bare.Resources[0].Configured; got != nil {
		t.Errorf("configured without a configuration section = %v, want nil", got)
	}
}

// A block written once (a single or a map nested block) is an object in the configuration: its attributes and their
// references are read with the path `<block>.<attribute>` (review round 1, finding 12). The reader also keeps the
// provider configuration of the resource.
func TestConfigurationReadsSingleNestedBlocks(t *testing.T) {
	t.Parallel()

	p := readInline(t, `{"format_version":"1.2","resource_changes":[{"address":"a_b.c","mode":"managed","type":"a_b",
		"name":"c","change":{"actions":["create"],"after":{"settings":{}},"after_unknown":{"settings":{"group_id":true}},
		"after_sensitive":{}}}],"configuration":{"root_module":{"resources":[{"address":"a_b.c","provider_config_key":"a",
		"expressions":{"settings":{"group_id":{"references":["aws_security_group.web.id","aws_security_group.web"]},
		"note":{"constant_value":"hunter2"}}}}]}}}`)

	res := p.Resources[0]
	if got := res.References["settings.group_id"]; !slices.Equal(got, []string{"aws_security_group.web"}) {
		t.Errorf("references = %v", res.References)
	}

	if want := []string{"settings", "settings.group_id", "settings.note"}; !slices.Equal(res.Configured, want) {
		t.Errorf("configured = %v, want %v", res.Configured, want)
	}

	if res.ProviderConfig != "a" {
		t.Errorf("provider config = %q, want a", res.ProviderConfig)
	}

	assertNoneIn(t, marshal(t, p), "hunter2")
}

// An element of a list that is unknown until apply keeps its position as null, so that the known elements keep
// their indexes (review round 1, finding 13).
func TestUnknownListElementKeepsItsPosition(t *testing.T) {
	t.Parallel()

	p := readInline(t, `{"format_version":"1.2","resource_changes":[{"address":"a_b.c","mode":"managed","type":"a_b",
		"name":"c","change":{"actions":["create"],"after":{"ids":[null,"sg-known"]},
		"after_unknown":{"ids":[true,false]},"after_sensitive":{}}}]}`)

	res := p.Resources[0]
	if got := res.Values["ids"]; !reflect.DeepEqual(got, []any{nil, "sg-known"}) {
		t.Errorf("ids = %#v, want [nil sg-known]", got)
	}

	if !slices.Equal(res.Unknown, []string{"ids[0]"}) {
		t.Errorf("unknown = %v, want [ids[0]]", res.Unknown)
	}
}
