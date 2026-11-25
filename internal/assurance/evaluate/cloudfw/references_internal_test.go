package cloudfw

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// dbGroup has two inline ingress blocks whose referenced groups are unknown until apply. The configuration lists the
// app block first, the plan lists the bastion block first (set order).
func dbGroup(references map[string][]string) plan.Resource {
	db := resourceOf("aws_security_group.db", typeSecurityGroup, map[string]any{
		"ingress": []any{map[string]any{"from_port": "22"}, map[string]any{"from_port": "5432"}},
	})
	db.Unknown = []string{"ingress[0].security_groups", "ingress[1].security_groups"}
	db.References = references

	return db
}

// TestBlockRefsIgnoresConfigurationOrder: an inline-block reference must not be matched by the
// configuration index, because the plan lists set elements in hash order.
func TestBlockRefsIgnoresConfigurationOrder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		references map[string][]string
		name       string
		wantGap    string
		want       []string
		wantOK     bool
	}{
		{
			name: "blocks name different groups",
			references: map[string][]string{
				"ingress[0].security_groups": {sgApp}, "ingress[1].security_groups": {sgBastion},
			},
			wantGap: evaluate.GapUnresolvedReference,
		},
		{
			name: "every block names the same group",
			references: map[string][]string{
				"ingress[0].security_groups": {sgApp}, "ingress[1].security_groups": {sgApp},
			},
			want: []string{sgApp}, wantOK: true,
		},
		{
			name:       "fewer blocks with references than unknown blocks",
			references: map[string][]string{"ingress[0].security_groups": {sgApp}},
			wantGap:    evaluate.GapUnresolvedReference,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := coreEvaluator(dbGroup(tc.references),
				resourceOf(sgApp, typeSecurityGroup, map[string]any{}),
				resourceOf(sgBastion, typeSecurityGroup, map[string]any{}))
			check := &side{}

			found, ok := e.blockRefsOfType(e.resource("aws_security_group.db"),
				blockElement{list: "ingress", field: "security_groups", index: 0}, check, typeSecurityGroup)

			if ok != tc.wantOK || !slices.Equal(addresses(found), tc.want) {
				t.Fatalf("got %v %v, want %v %v (gaps %v)", addresses(found), ok, tc.want, tc.wantOK, check.gaps)
			}

			if tc.wantGap != "" && !slices.Contains(gapKinds(check), tc.wantGap) {
				t.Fatalf("gaps %v, want kind %s", check.gaps, tc.wantGap)
			}
		})
	}
}

// TestListHasUnknownElements: a wholly unknown element is dropped from Values.
func TestListHasUnknownElements(t *testing.T) {
	t.Parallel()

	res := resourceOf("aws_security_group.db", typeSecurityGroup, map[string]any{"ingress": []any{}})
	res.Unknown = []string{"egress[0].cidr_blocks", "ingress[1]"}

	for path, want := range map[string]bool{"ingress": true, "egress": false, "ingress[1]": false} {
		if got := listHasUnknownElements(&res, path); got != want {
			t.Errorf("listHasUnknownElements(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestRefsOfTypeRejectsOtherTypes: `vpc_security_group_ids = [aws_vpc.main.default_security_group_id]`
// must not resolve to the VPC as if it were the group.
func TestRefsOfTypeRejectsOtherTypes(t *testing.T) {
	t.Parallel()

	instance := resourceOf("aws_instance.web", "aws_instance", map[string]any{"subnet_id": "subnet-1"})
	instance.Unknown = []string{"vpc_security_group_ids"}
	instance.References = map[string][]string{"vpc_security_group_ids": {"aws_vpc.main"}}

	e := coreEvaluator(instance,
		resourceOf("aws_vpc.main", "aws_vpc", map[string]any{}),
		resourceOf("aws_vpc.other", "aws_vpc", map[string]any{"id": "subnet-1"}))

	t.Run("list", func(t *testing.T) {
		t.Parallel()

		check := &side{}

		found, ok := e.refsOfType(e.resource("aws_instance.web"), "vpc_security_group_ids", check, typeSecurityGroup)
		requireUnresolved(t, addresses(found), ok, check)
	})

	t.Run("single by known id", func(t *testing.T) {
		t.Parallel()

		check := &side{}

		found, ok := e.refOfType(e.resource("aws_instance.web"), "subnet_id", check, "aws_subnet")
		if found != nil {
			t.Fatalf("got %s, want nil", found.Address)
		}

		requireUnresolved(t, nil, ok, check)
	})
}

// requireUnresolved fails the test unless nothing resolved, ok is false and the side has an unresolved-reference gap.
func requireUnresolved(t *testing.T, found []string, ok bool, check *side) {
	t.Helper()

	if ok || len(found) != 0 {
		t.Fatalf("got %v %v, want none and false", found, ok)
	}

	if !slices.Contains(gapKinds(check), evaluate.GapUnresolvedReference) {
		t.Fatalf("gaps %v, want %s", check.gaps, evaluate.GapUnresolvedReference)
	}
}

// TestOpaqueReferences: a reference through `var.` or `local.` hides a resource, so the list is not
// resolved by the resource references alone. The plans go through the plan reader.
func TestOpaqueReferences(t *testing.T) {
	t.Parallel()

	extraList := readPlanDoc(t, &planDoc{
		changes: []planChange{
			{address: awsWebInstance, after: `{"ami": "ami-1"}`, unknown: `{"vpc_security_group_ids": true}`},
			{address: "aws_security_group.this", after: `{"id": "sg-this", "ingress": [], "egress": []}`},
			{address: awsDBGroup, after: `{"id": "sg-db", "egress": [], "ingress": [{"protocol": "tcp", "from_port": 5432,
				"to_port": 5432, "cidr_blocks": ["0.0.0.0/0"]}]}`},
		},
		config: []configResource{{address: awsWebInstance, expressions: `{"vpc_security_group_ids": {"references": [
			"aws_security_group.this.id", "aws_security_group.this", "var.extra_security_group_ids"]}}`}},
	})

	clientBlock := readPlanDoc(t, &planDoc{
		changes: []planChange{
			{address: "aws_security_group.client", after: `{"id": "sg-client", "ingress": [], "egress": [
				{"protocol": "-1", "from_port": 0, "to_port": 0, "cidr_blocks": ["0.0.0.0/0"]}]}`},
			{address: "aws_security_group.internal", after: `{"id": "sg-internal", "ingress": [], "egress": []}`},
			{address: awsDBGroup, after: `{"id": "sg-db", "egress": [], "ingress": [
				{"protocol": "tcp", "from_port": 5432, "to_port": 5432, "security_groups": ["sg-internal"]},
				{"protocol": "tcp", "from_port": 5432, "to_port": 5432}]}`, unknown: `{"ingress": [{}, {"security_groups": true}]}`},
		},
		config: []configResource{{address: awsDBGroup, expressions: `{"ingress": [
			{"security_groups": {"references": ["aws_security_group.internal.id", "aws_security_group.internal"]}},
			{"security_groups": {"references": ["var.client_sg_id"]}}]}`}},
	})

	runQueryCases(t, []queryCase{
		{
			name: "concat of a group and a variable", resources: extraList, from: awsWebInstance, to: awsDBGroup, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": " + fmt.Sprintf(opaqueFormat, "var.extra_security_group_ids")},
		},
		{
			name: "inline block through a variable", resources: clientBlock, from: "aws_security_group.client", to: awsDBGroup,
			want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": " + awsDBGroup + ".ingress[*].security_groups: the blocks " +
				"name different resources, and the plan does not keep the configuration order of the blocks"},
		},
	})
}

// planChange is one resource that a plan JSON creates: its planned values and its `after_unknown` ("" for none).
type planChange struct {
	address, after, unknown string
}

// configResource is one resource of the configuration of a plan JSON, with its expressions.
type configResource struct {
	address, expressions string
}

// planDoc is a plan JSON: its resource changes and its configuration resources.
type planDoc struct {
	changes []planChange
	config  []configResource
}

// json is the plan JSON of the document.
func (doc *planDoc) json() string {
	changes := make([]string, 0, len(doc.changes))
	for _, c := range doc.changes {
		changes = append(changes, c.json())
	}

	resources := make([]string, 0, len(doc.config))
	for _, r := range doc.config {
		resources = append(resources, r.json())
	}

	return `{"format_version": "1.2", "terraform_version": "1.17.0", "complete": true,
		"resource_changes": [` + strings.Join(changes, ",") + `],
		"configuration": {"root_module": {"resources": [` + strings.Join(resources, ",") + `]}}}`
}

func (c *planChange) json() string {
	typ, name, _ := strings.Cut(c.address, ".")
	unknown := c.unknown

	if unknown == "" {
		unknown = "{}"
	}

	return `{"address": "` + c.address + `", "mode": "managed", "type": "` + typ + `", "name": "` + name + `",
		"change": {"actions": ["create"], "after": ` + c.after + `, "after_unknown": ` + unknown + `, "after_sensitive": {}}}`
}

func (r *configResource) json() string {
	typ, name, _ := strings.Cut(r.address, ".")

	return `{"address": "` + r.address + `", "mode": "managed", "type": "` + typ + `", "name": "` + name + `",
		"expressions": ` + r.expressions + `}`
}

// readPlanDoc reads the plan JSON of the document through the plan reader.
func readPlanDoc(t *testing.T, doc *planDoc) []plan.Resource {
	t.Helper()

	data := []byte(doc.json())
	if !json.Valid(data) {
		t.Fatalf("invalid plan JSON: %s", data)
	}

	p, diags := plan.Read(data, "review.json")
	if p == nil {
		t.Fatalf("plan not read: %v", diags)
	}

	return p.Resources
}
