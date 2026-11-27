package plan_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/redact"
)

// priorPlan is a plan whose resource changes each have the type, actions, values before the change, and the
// sensitivity mirror of those values.
func priorPlan(t *testing.T, changes ...string) *plan.Plan {
	t.Helper()

	return readInline(t, `{"format_version":"1.2","resource_changes":[`+strings.Join(changes, ",")+`]}`)
}

// priorChange is one resource change at `<typ>.r` with no values after the change unless the actions create.
func priorChange(typ, actions, before, beforeSensitive string) string {
	after := "null"
	if strings.Contains(actions, "create") || strings.Contains(actions, "update") {
		after = `{"description": "after"}`
	}

	return `{"address":"` + typ + `.r","mode":"managed","type":"` + typ + `","name":"r","change":{"actions":` + actions +
		`,"before":` + before + `,"before_sensitive":` + beforeSensitive + `,"after":` + after +
		`,"after_unknown":{},"after_sensitive":{}}}`
}

// A rule resource that the plan deletes or replaces keeps only the attributes that named its owner before the
// change; every other value before the change, and a sensitive owner value, never reaches the Plan.
func TestPriorKeepsOnlyTheOwnerOfADeletedOrReplacedRule(t *testing.T) {
	t.Parallel()

	awsBefore := `{"security_group_id":"sg-db","description":"hunter2-unrelated","cidr_blocks":["10.0.0.0/8"],` +
		`"type":"ingress"}`
	azureBefore := `{"network_security_group_name":"hunter2-nsg","resource_group_name":"rg-pay","name":"hunter2-name"}`

	p := priorPlan(t,
		priorChange("aws_security_group_rule", `["delete"]`, awsBefore, `{}`),
		priorChange("aws_vpc_security_group_ingress_rule", `["delete","create"]`, awsBefore, `{}`),
		priorChange("azurerm_network_security_rule", `["create","delete"]`, azureBefore, `{"network_security_group_name":true}`),
		priorChange("aws_vpc_security_group_egress_rule", `["update"]`, awsBefore, `{}`),
		priorChange("aws_security_group", `["delete"]`, awsBefore, `{}`),
	)

	assertNoneIn(t, marshal(t, p), "hunter2")

	want := map[string]map[string]string{
		"aws_security_group_rule.r":             {"security_group_id": "sg-db"},
		"aws_vpc_security_group_ingress_rule.r": {"security_group_id": "sg-db"},
		"azurerm_network_security_rule.r":       {"resource_group_name": "rg-pay"},
	}

	for i := range p.Resources {
		res := &p.Resources[i]
		if got := res.Prior; !maps.Equal(got, want[res.Address]) {
			t.Errorf("%s: prior = %v, want %v", res.Address, got, want[res.Address])
		}
	}

	record := redact.Record{
		File: "p.json", Property: "azurerm_network_security_rule.r.before.network_security_group_name",
		Class: redact.ClassSecretData,
	}
	if !slices.Contains(p.Redactions, record) {
		t.Errorf("redactions = %+v, want %+v", p.Redactions, record)
	}
}

// A sensitivity mirror that marks the whole object before the change keeps nothing.
func TestPriorOfAWhollySensitiveRuleIsNil(t *testing.T) {
	t.Parallel()

	p := priorPlan(t, priorChange("aws_security_group_rule", `["delete"]`, `{"security_group_id":"hunter2-sg"}`, `true`))

	assertNoneIn(t, marshal(t, p), "hunter2")

	if prior := p.Resources[0].Prior; prior != nil {
		t.Errorf("prior = %v, want nil", prior)
	}
}
