package cloudfw_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
)

// awsRank orders verdicts by how much they allow.
func awsRank(v cloudfw.Verdict) int {
	switch v {
	case cloudfw.VerdictDenied:
		return 0
	case cloudfw.VerdictUnknown:
		return 1
	case cloudfw.VerdictAllowed:
		return 2
	}

	return -1
}

// awsGroups and awsInstances are the sizes of a generated world.
const (
	awsGroups    = 4
	awsInstances = 4
)

// awsPortsQueried are the destination ports of the generated queries.
var awsPortsQueried = []int{80, 81, 82, 443}

// awsResource is one generated resource change of a `show -json` plan.
type awsResource struct {
	after   map[string]any
	unknown map[string]any
	address string
}

// awsWorld is a generated set of security groups, instances, and rule resources.
type awsWorld struct {
	groups    []map[string]any
	instances []awsResource
	rules     []awsResource
}

type awsGen struct{ r *rand.Rand }

func (g awsGen) pick(items ...string) string { return items[g.r.IntN(len(items))] }

func (g awsGen) groupID() string { return fmt.Sprintf("sg-%d", g.r.IntN(awsGroups)) }

// inline is an inline ingress or egress block with a random protocol, port range, and peer.
func (g awsGen) inline() map[string]any {
	from := 80 + g.r.IntN(3)
	rule := map[string]any{
		"protocol": g.pick("tcp", "udp", "-1", "icmp"), "from_port": from, "to_port": from + g.r.IntN(2),
		"cidr_blocks": []any{}, "ipv6_cidr_blocks": []any{}, "security_groups": []any{}, "prefix_list_ids": []any{},
		"self": false,
	}

	switch g.r.IntN(5) {
	case 0:
		rule["cidr_blocks"] = []any{g.pick("0.0.0.0/0", "10.0.1.0/24", "10.0.0.0/8", "10.0.1.0/25", "bad")}
	case 1:
		rule["security_groups"] = []any{g.groupID()}
	case 2:
		rule["self"] = true
	case 3:
		rule["prefix_list_ids"] = []any{"pl-1"}
	default:
		rule["ipv6_cidr_blocks"] = []any{"::/0"}
	}

	return rule
}

// standalone is a rule resource of one of the three rule sources.
func (g awsGen) standalone(i int) awsResource {
	in := g.inline()
	owner := g.groupID()
	kind := g.pick("ingress", "egress")

	if g.r.IntN(2) == 0 {
		after := map[string]any{"type": kind, "security_group_id": owner}
		for _, key := range []string{"protocol", "from_port", "to_port", "cidr_blocks", "ipv6_cidr_blocks", "self", "prefix_list_ids"} {
			after[key] = in[key]
		}

		if groups, _ := in["security_groups"].([]any); len(groups) > 0 {
			after["source_security_group_id"] = groups[0]
		}

		return awsResource{address: fmt.Sprintf("aws_security_group_rule.r%d", i), after: after}
	}

	return g.vpcRule(i, kind, owner, in)
}

// vpcRule is the aws_vpc_security_group_*_rule form of an inline block. The VPC rule resources have no `self`: a
// block that names its own group references the owner group.
func (g awsGen) vpcRule(i int, kind, owner string, in map[string]any) awsResource {
	after := map[string]any{
		"security_group_id": owner, "ip_protocol": in["protocol"], "from_port": in["from_port"], "to_port": in["to_port"],
	}

	for key, field := range map[string]string{
		"cidr_blocks": "cidr_ipv4", "ipv6_cidr_blocks": "cidr_ipv6", "security_groups": "referenced_security_group_id",
		"prefix_list_ids": "prefix_list_id",
	} {
		if list, _ := in[key].([]any); len(list) > 0 {
			after[field] = list[0]
		}
	}

	if self, _ := in["self"].(bool); self {
		after["referenced_security_group_id"] = owner
	}

	return awsResource{address: fmt.Sprintf("aws_vpc_security_group_%s_rule.r%d", kind, i), after: after}
}

func (g awsGen) blocks() []any {
	count := g.r.IntN(3)
	out := make([]any, 0, count)

	for range count {
		out = append(out, g.inline())
	}

	return out
}

func (g awsGen) instance(i int) awsResource {
	res := awsResource{
		address: fmt.Sprintf("aws_instance.i%d", i),
		after:   map[string]any{"vpc_security_group_ids": []any{g.groupID(), g.groupID()}},
		unknown: map[string]any{"id": true},
	}

	if ip := g.pick("10.0.1.5", "10.0.2.5", ""); ip != "" {
		res.after["private_ip"] = ip
	} else {
		res.unknown["private_ip"] = true
	}

	return res
}

func (g awsGen) world() awsWorld {
	var w awsWorld

	for i := range awsGroups {
		w.groups = append(w.groups, map[string]any{"id": fmt.Sprintf("sg-%d", i), "ingress": g.blocks(), "egress": g.blocks()})
	}

	for i := range awsInstances {
		w.instances = append(w.instances, g.instance(i))
	}

	for i := range g.r.IntN(4) {
		w.rules = append(w.rules, g.standalone(i))
	}

	return w
}

// withRule is a copy of the world with one more allow rule: an inline block of a group or a rule resource.
func (g awsGen) withRule(w awsWorld) awsWorld {
	out := awsWorld{instances: w.instances, rules: append([]awsResource(nil), w.rules...)}

	for _, group := range w.groups {
		out.groups = append(out.groups, map[string]any{"id": group["id"], "ingress": group["ingress"], "egress": group["egress"]})
	}

	if g.r.IntN(2) == 0 {
		out.rules = append(out.rules, g.standalone(len(w.rules)+100))

		return out
	}

	group := out.groups[g.r.IntN(awsGroups)]
	block := g.pick("ingress", "egress")
	blocks, _ := group[block].([]any)
	group[block] = append(append([]any(nil), blocks...), g.inline())

	return out
}

// plan is the `show -json` document of the world.
func (w *awsWorld) plan(t *testing.T) []byte {
	t.Helper()

	resources := append([]awsResource(nil), w.instances...)
	for i, group := range w.groups {
		resources = append(resources, awsResource{address: fmt.Sprintf("aws_security_group.g%d", i), after: group})
	}

	resources = append(resources, w.rules...)

	changes := make([]any, 0, len(resources))
	for _, res := range resources {
		changes = append(changes, awsChange(res))
	}

	data, err := json.Marshal(map[string]any{
		"format_version": "1.2", "complete": true, "planned_values": map[string]any{}, "resource_changes": changes,
	})
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func awsChange(res awsResource) map[string]any {
	typ, name, _ := strings.Cut(res.address, ".")

	unknown := res.unknown
	if unknown == nil {
		unknown = map[string]any{}
	}

	return map[string]any{
		"address": res.address, "mode": "managed", "type": typ, "name": name,
		"change": map[string]any{"actions": []any{"create"}, "after": res.after, "after_unknown": unknown},
	}
}

// TestAWSMonotonicAddingAnAllowRule checks the monotonicity of the AWS evaluation: security group rules only allow,
// so an added rule never gives a query a more restrictive verdict (allowed -> denied or unknown, unknown -> denied).
func TestAWSMonotonicAddingAnAllowRule(t *testing.T) {
	t.Parallel()

	g := awsGen{r: rand.New(rand.NewPCG(7, 11))}
	// opened counts the queries that a rule made more permissive; without any the property would hold vacuously.
	opened := 0

	for iteration := range 200 {
		before := g.world()
		after := g.withRule(before)
		evBefore := awsEvaluator(awsReadPlan(t, before.plan(t)))
		evAfter := awsEvaluator(awsReadPlan(t, after.plan(t)))

		opened += awsCompare(t, iteration, evBefore, evAfter)
	}

	if opened == 0 {
		t.Fatal("no generated rule changed any verdict: the generator does not exercise the property")
	}
}

// awsQueries are the queries between every pair of generated instances on a few ports.
func awsQueries() []*cloudfw.Query {
	out := make([]*cloudfw.Query, 0, len(awsPortsQueried)*awsInstances*awsInstances)

	for i := range awsInstances * awsInstances {
		for _, port := range awsPortsQueried {
			out = append(out, &cloudfw.Query{
				From: cloudfw.Endpoint{Address: fmt.Sprintf("aws_instance.i%d", i/awsInstances)},
				To:   cloudfw.Endpoint{Address: fmt.Sprintf("aws_instance.i%d", i%awsInstances)}, Port: port,
			})
		}
	}

	return out
}

// awsCompare runs every query on both evaluators and fails when a verdict became more restrictive. It returns the
// number of queries that became more permissive.
func awsCompare(t *testing.T, iteration int, before, after *cloudfw.Evaluator) int {
	t.Helper()

	opened := 0

	for _, query := range awsQueries() {
		b, a := before.Evaluate(query), after.Evaluate(query)
		if awsRank(a.Verdict) < awsRank(b.Verdict) {
			t.Fatalf("iteration %d: %s -> %s:%d went from %s to %s",
				iteration, query.From.Address, query.To.Address, query.Port, b.Verdict, a.Verdict)
		}

		if awsRank(a.Verdict) > awsRank(b.Verdict) {
			opened++
		}
	}

	return opened
}
