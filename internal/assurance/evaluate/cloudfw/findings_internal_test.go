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

// Addresses and IDs of the second-review scenarios.
const (
	reviewWeb   = "aws_instance.web"
	reviewApp   = "aws_security_group.app"
	reviewDB    = "aws_security_group.db"
	reviewExtra = "aws_security_group.extra"
	reviewNIC   = "aws_network_interface.extra"
	idApp       = "sg-app"
	idDB        = "sg-db"
	idExtra     = "sg-extra"
	postgres    = 5432
)

// reviewCase is one query and the verdict and gaps (`<kind>: <detail>`) that the result must have.
type reviewCase struct {
	name      string
	resources []plan.Resource
	from, to  string
	want      Verdict
	gaps      []string
}

// allTraffic is an inline rule that allows every protocol to and from every IPv4 address.
func allTraffic() map[string]any {
	return map[string]any{"protocol": "-1", "from_port": "0", "to_port": "0", "cidr_blocks": []any{"0.0.0.0/0"}}
}

// postgresFrom is an inline ingress rule for port 5432 with extra fields.
func postgresFrom(fields map[string]any) map[string]any {
	rule := map[string]any{"protocol": "tcp", "from_port": "5432", "to_port": "5432"}
	for key, value := range fields {
		rule[key] = value
	}

	return rule
}

// group is a security group with its inline rules.
func group(address, id string, ingress, egress []any) plan.Resource {
	return resourceOf(address, typeSecurityGroup, map[string]any{"id": id, "ingress": ingress, "egress": egress})
}

// openDB is the database group: it allows 5432 from every IPv4 address.
func openDB() plan.Resource {
	return group(reviewDB, idDB, []any{postgresFrom(map[string]any{"cidr_blocks": []any{"0.0.0.0/0"}})}, []any{})
}

func runReviewCases(t *testing.T, cases []reviewCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := coreEvaluator(tc.resources...).Evaluate(&Query{
				From: Endpoint{Address: tc.from}, To: Endpoint{Address: tc.to}, Protocol: ProtocolTCP, Port: postgres,
			})
			assertReview(t, &res, tc.want, tc.gaps)
		})
	}
}

func assertReview(t *testing.T, res *Result, want Verdict, gaps []string) {
	t.Helper()

	got := make([]string, 0, len(res.Gaps))
	for _, gap := range res.Gaps {
		got = append(got, gap.Kind+": "+gap.Detail)
	}

	if res.Verdict != want {
		t.Fatalf("verdict = %s (gaps %v), want %s", res.Verdict, got, want)
	}

	for _, gap := range gaps {
		if !slices.Contains(got, gap) {
			t.Errorf("gaps = %v, want %q", got, gap)
		}
	}
}

// TestChangedRuleResourceMirroredInline is finding B: a rule resource that the plan deletes still has a copy in the
// inline list of its group, so the copy must not allow.
func TestChangedRuleResourceMirroredInline(t *testing.T) {
	t.Parallel()

	inline := func(actions []string, values map[string]any) []plan.Resource {
		rule := resourceOf("aws_security_group_rule.db_from_app", "aws_security_group_rule", values)
		rule.Actions = actions

		return []plan.Resource{
			group(reviewApp, idApp, []any{}, []any{allTraffic()}),
			group(reviewDB, idDB, []any{postgresFrom(map[string]any{"security_groups": []any{idApp}})}, []any{}),
			rule,
		}
	}
	unchanged := map[string]any{
		"type": "ingress", "security_group_id": idDB, "protocol": "tcp", "from_port": "5432", "to_port": "5432",
		"source_security_group_id": idApp,
	}

	runReviewCases(t, []reviewCase{
		{
			name: "deleted rule resource", resources: inline([]string{"delete"}, nil), from: reviewApp, to: reviewDB,
			want: VerdictUnknown,
			gaps: []string{evaluate.GapUnknownAfterApply + ": " + reviewDB + ".ingress may repeat a rule that the plan changes"},
		},
		{
			name: "unchanged rule resource", resources: inline([]string{"no-op"}, unchanged), from: reviewApp, to: reviewDB,
			want: VerdictAllowed,
		},
	})
}

// TestInstanceNetworkInterfaceBlocks is finding D: the `network_interface` blocks of an instance attach interfaces
// whose groups count.
func TestInstanceNetworkInterfaceBlocks(t *testing.T) {
	t.Parallel()

	instance := func(references map[string][]string) []plan.Resource {
		web := resourceOf(reviewWeb, "aws_instance", map[string]any{
			"vpc_security_group_ids": []any{idApp}, "network_interface": []any{map[string]any{"device_index": "1"}},
		})
		web.Unknown = []string{"network_interface[0].network_interface_id"}
		web.References = references

		return []plan.Resource{
			web, group(reviewApp, idApp, []any{}, []any{}), group(reviewExtra, idExtra, []any{}, []any{allTraffic()}),
			resourceOf(reviewNIC, awsNetworkInterface, map[string]any{"id": "eni-extra", "security_groups": []any{idExtra}}),
			openDB(),
		}
	}

	runReviewCases(t, []reviewCase{
		{
			name:      "interface resolves by configuration",
			resources: instance(map[string][]string{"network_interface[0].network_interface_id": {reviewNIC}}),
			from:      reviewWeb, to: reviewDB, want: VerdictAllowed,
		},
		{
			name: "interface does not resolve", resources: instance(nil), from: reviewWeb, to: reviewDB, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": " + reviewWeb + ".network_interface[0].network_interface_id " +
				"does not resolve, so it may attach a security group to " + reviewWeb},
		},
	})
}

// TestAttachmentBlockWithUnknownInstance is finding E: a network interface whose `attachment` names an instance
// that is unknown until apply, without a configuration reference, may be attached to the endpoint.
func TestAttachmentBlockWithUnknownInstance(t *testing.T) {
	t.Parallel()

	floating := resourceOf("aws_network_interface.floating", awsNetworkInterface, map[string]any{
		"id": "eni-floating", "security_groups": []any{idExtra}, "attachment": []any{map[string]any{"device_index": "1"}},
	})
	floating.Unknown = []string{"attachment[0].instance"}

	runReviewCases(t, []reviewCase{{
		name: "attachment instance unknown without reference",
		resources: []plan.Resource{
			resourceOf(reviewWeb, "aws_instance", map[string]any{"id": "i-web", "vpc_security_group_ids": []any{idApp}}),
			group(reviewApp, idApp, []any{}, []any{}), group(reviewExtra, idExtra, []any{}, []any{allTraffic()}),
			floating, openDB(),
		},
		from: reviewWeb, to: reviewDB, want: VerdictUnknown,
		gaps: []string{
			evaluate.GapUnknownAfterApply + ": aws_network_interface.floating.attachment[*].instance",
			evaluate.GapUnresolvedReference + ": aws_network_interface.floating.attachment does not resolve, so it may " +
				"attach a security group to " + reviewWeb,
		},
	}})
}

// TestOpaqueReferences is finding A: a reference through `var.` or `local.` hides a resource, so the list is not
// resolved by the resource references alone. The plans go through the plan reader.
func TestOpaqueReferences(t *testing.T) {
	t.Parallel()

	extraList := readReviewPlan(t, &reviewDoc{
		changes: []planChange{
			{address: reviewWeb, after: `{"ami": "ami-1"}`, unknown: `{"vpc_security_group_ids": true}`},
			{address: "aws_security_group.this", after: `{"id": "sg-this", "ingress": [], "egress": []}`},
			{address: reviewDB, after: `{"id": "sg-db", "egress": [], "ingress": [{"protocol": "tcp", "from_port": 5432,
				"to_port": 5432, "cidr_blocks": ["0.0.0.0/0"]}]}`},
		},
		config: []configResource{{address: reviewWeb, expressions: `{"vpc_security_group_ids": {"references": [
			"aws_security_group.this.id", "aws_security_group.this", "var.extra_security_group_ids"]}}`}},
	})

	clientBlock := readReviewPlan(t, &reviewDoc{
		changes: []planChange{
			{address: "aws_security_group.client", after: `{"id": "sg-client", "ingress": [], "egress": [
				{"protocol": "-1", "from_port": 0, "to_port": 0, "cidr_blocks": ["0.0.0.0/0"]}]}`},
			{address: "aws_security_group.internal", after: `{"id": "sg-internal", "ingress": [], "egress": []}`},
			{address: reviewDB, after: `{"id": "sg-db", "egress": [], "ingress": [
				{"protocol": "tcp", "from_port": 5432, "to_port": 5432, "security_groups": ["sg-internal"]},
				{"protocol": "tcp", "from_port": 5432, "to_port": 5432}]}`, unknown: `{"ingress": [{}, {"security_groups": true}]}`},
		},
		config: []configResource{{address: reviewDB, expressions: `{"ingress": [
			{"security_groups": {"references": ["aws_security_group.internal.id", "aws_security_group.internal"]}},
			{"security_groups": {"references": ["var.client_sg_id"]}}]}`}},
	})

	runReviewCases(t, []reviewCase{
		{
			name: "concat of a group and a variable", resources: extraList, from: reviewWeb, to: reviewDB, want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": " + fmt.Sprintf(opaqueFormat, "var.extra_security_group_ids")},
		},
		{
			name: "inline block through a variable", resources: clientBlock, from: "aws_security_group.client", to: reviewDB,
			want: VerdictUnknown,
			gaps: []string{evaluate.GapUnresolvedReference + ": " + reviewDB + ".ingress[*].security_groups: the blocks " +
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

// reviewDoc is a plan JSON: its resource changes and its configuration resources.
type reviewDoc struct {
	changes []planChange
	config  []configResource
}

// json is the plan JSON of the document.
func (doc *reviewDoc) json() string {
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

// readReviewPlan reads the plan JSON of the document through the plan reader.
func readReviewPlan(t *testing.T, doc *reviewDoc) []plan.Resource {
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
