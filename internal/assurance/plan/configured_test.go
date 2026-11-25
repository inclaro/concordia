package plan_test

import (
	"os"
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
