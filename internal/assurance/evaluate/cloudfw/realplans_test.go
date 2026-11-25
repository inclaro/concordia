package cloudfw_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// realPlansDir holds plans that `terraform show -json` recorded from the configurations next to them. See its README.
const realPlansDir = "../../../../test/fixtures/assurance/plans/real"

// realScenario is one query against a recorded plan and the verdict that the provider's real semantics give.
type realScenario struct {
	name     string
	from, to string
	// gap is a gap kind that the result must have; empty for none checked.
	gap string
	// skip names a documented limit of the evaluator on real plans; the scenario keeps the verdict of the
	// provider's real semantics.
	skip string
	want cloudfw.Verdict
	port int
}

// realEvaluator reads a recorded plan through the plan reader and indexes it.
func realEvaluator(t *testing.T, setup string) *cloudfw.Evaluator {
	t.Helper()

	file := filepath.Join(realPlansDir, setup, "plan.json")

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}

	p, diags := plan.Read(data, file)
	if p == nil {
		t.Fatalf("plan %s not read: %v", file, diags)
	}

	if p.Coverage != assurance.CoverageComplete {
		t.Fatalf("plan %s coverage = %s, want complete", file, p.Coverage)
	}

	return cloudfw.New(&cloudfw.Input{
		Environment: "production", Coverage: p.Coverage, Digest: p.Digest,
		Plans: []cloudfw.Plan{{File: file, Resources: p.Resources}},
	})
}

// realRun runs the scenarios of one recorded plan.
func realRun(t *testing.T, setup string, scenarios []realScenario) {
	t.Helper()

	ev := realEvaluator(t, setup)

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			if sc.skip != "" {
				t.Skip("documented limit: " + sc.skip)
			}

			res := ev.Evaluate(&cloudfw.Query{
				From: cloudfw.Endpoint{Address: sc.from}, To: cloudfw.Endpoint{Address: sc.to},
				Protocol: cloudfw.ProtocolTCP, Port: sc.port,
			})
			realAssert(t, &sc, &res)
		})
	}
}

func realAssert(t *testing.T, sc *realScenario, res *cloudfw.Result) {
	t.Helper()

	if res.Verdict != sc.want {
		t.Fatalf("verdict = %s (reason %q, rules %v, gaps %v), want %s", res.Verdict, res.Reason, res.Rules, res.Gaps, sc.want)
	}

	if sc.gap != "" && !slices.ContainsFunc(res.Gaps, func(g assurance.Gap) bool { return g.Kind == sc.gap }) {
		t.Errorf("gaps = %v, want a %s gap", res.Gaps, sc.gap)
	}
}

// Documented limits: real plans do not hold the evidence that these scenarios need, so the evaluator answers
// `unknown` where the provider's real semantics give the expected verdict. The scenarios keep that verdict.
const (
	limitMergedInlineRefs = "the configuration of an inline rule list (aws_security_group ingress/egress, " +
		"azurerm_network_security_group security_rule) has one merged `references` list for all blocks, and a " +
		"dynamic block's references are not in the plan, so an unknown group or ASG id in an inline block never resolves"
	limitDynamicBlocks = "a block list that the configuration does not show (no static blocks) may still be set by a " +
		"dynamic block, which the plan's configuration omits, so the computed `ingress`/`egress`, " +
		"`network_interface`, or `attachment` list that is unknown until apply is not read as empty"
	limitKnownVariable = "a reference through a variable or local value never resolves, even when the plan records " +
		"the variable's value"
)

func TestRealPlanAWSCreate(t *testing.T) {
	t.Parallel()

	realRun(t, "aws-create", []realScenario{
		{
			name: "standalone rule with an unknown group id allows the port", from: "aws_lb.public",
			to: "aws_db_instance.ledger", port: 5432, want: cloudfw.VerdictAllowed,
		},
		{
			name: "no rule allows the port", from: "aws_lb.public", to: "aws_db_instance.ledger", port: 22,
			want: cloudfw.VerdictDenied,
		},
		{
			name: "two inline blocks name different groups", from: "aws_lb.public", to: "aws_db_instance.ledger",
			port: 6432, want: cloudfw.VerdictUnknown, gap: "unresolved-reference",
		},
		{
			name: "attached interfaces carry the groups of a separate attachment", from: "aws_network_interface.ops",
			to: "aws_network_interface.bastion", port: 22, want: cloudfw.VerdictAllowed,
		},
		{
			name: "inline block names one group with an unknown id", from: "aws_network_interface.ops",
			to: "aws_elasticache_cluster.cache", port: 6379, want: cloudfw.VerdictAllowed, skip: limitMergedInlineRefs,
		},
		{
			name: "instance source and inline block naming its group", from: "aws_instance.app",
			to: "aws_elasticache_cluster.cache", port: 6379, want: cloudfw.VerdictAllowed, skip: limitMergedInlineRefs,
		},
		{
			name: "group without inline or standalone ingress rules", from: "aws_network_interface.ops",
			to: "aws_lb.public", port: 443, want: cloudfw.VerdictDenied, skip: limitDynamicBlocks,
		},
		{
			name: "instance in a group with empty inline lists", from: "aws_network_interface.ops",
			to: "aws_instance.locked", port: 443, want: cloudfw.VerdictDenied, skip: limitDynamicBlocks,
		},
		{
			name: "instance whose interface comes from a network_interface block", from: "aws_network_interface.ops",
			to: "aws_instance.bastion", port: 22, want: cloudfw.VerdictAllowed,
		},
		{
			name: "load balancer in two subnets inside the rule prefix", from: "aws_lb.public",
			to: "aws_network_interface.ops", port: 22, want: cloudfw.VerdictAllowed,
		},
		{
			name: "group list concatenated with an empty variable", from: "aws_lb.public",
			to: "aws_db_instance.reports", port: 5432, want: cloudfw.VerdictAllowed, skip: limitKnownVariable,
		},
	})
}

func TestRealPlanAWSState(t *testing.T) {
	t.Parallel()

	realRun(t, "aws-state", []realScenario{
		{
			name: "rule resource that the plan deletes no longer allows", from: "aws_network_interface.gateway",
			to: "aws_network_interface.ledger", port: 5432, want: cloudfw.VerdictDenied,
		},
		{
			name: "rule resource that the plan adds names the group of a conflicting sg attachment",
			from: "aws_network_interface.ops", to: "aws_network_interface.ledger", port: 5432,
			want: cloudfw.VerdictUnknown, gap: "unresolved-reference",
		},
		{
			name: "rule resource that the plan narrows still allows", from: "aws_network_interface.gateway",
			to: "aws_network_interface.bastion", port: 22, want: cloudfw.VerdictAllowed,
		},
		{
			name: "group with an empty egress list in state", from: "aws_network_interface.ledger",
			to: "aws_network_interface.bastion", port: 22, want: cloudfw.VerdictDenied,
		},
		{
			name: "instance reached through its attached secondary interface", from: "aws_network_interface.gateway",
			to: "aws_instance.app", port: 22, want: cloudfw.VerdictAllowed,
		},
		{
			name: "interface update conflicts with the group of an sg attachment", from: "aws_network_interface.gateway",
			to: "aws_network_interface.ops", port: 22, want: cloudfw.VerdictUnknown, gap: "unresolved-reference",
		},
		{
			name: "interface update conflicts with the group of an sg attachment, standalone rule",
			from: "aws_network_interface.gateway", to: "aws_network_interface.ops", port: 443,
			want: cloudfw.VerdictUnknown, gap: "unresolved-reference",
		},
		{
			name: "instance whose interface comes from a network_interface block", from: "aws_network_interface.gateway",
			to: "aws_instance.bastion", port: 22, want: cloudfw.VerdictAllowed,
		},
		{
			name: "instance with two interfaces whose addresses are known", from: "aws_instance.app",
			to: "aws_network_interface.bastion", port: 22, want: cloudfw.VerdictAllowed,
		},
	})
}

func TestRealPlanGCPCreate(t *testing.T) {
	t.Parallel()

	realRun(t, "gcp-create", []realScenario{
		{
			name: "egress deny and implied ingress deny", from: "google_compute_instance.app",
			to: "google_compute_instance.ledger", port: 9000, want: cloudfw.VerdictDenied,
		},
		{
			name: "implied ingress deny", from: "google_compute_instance.app", to: "google_compute_instance.ledger",
			port: 22, want: cloudfw.VerdictDenied,
		},
		{
			name: "source tag of another instance does not match", from: "google_compute_instance.batch",
			to: "google_compute_instance.ledger", port: 5432, want: cloudfw.VerdictDenied,
		},
		{
			name: "source tag allows the port", from: "google_compute_instance.app", to: "google_compute_instance.ledger",
			port: 5432, want: cloudfw.VerdictAllowed,
		},
		{
			name: "source service account allows the port", from: "google_compute_instance.app",
			to: "google_compute_instance.ledger", port: 9100, want: cloudfw.VerdictAllowed,
		},
		{
			name: "source range allows a port of a range", from: "google_compute_instance.app",
			to: "google_compute_instance.batch", port: 8050, want: cloudfw.VerdictAllowed,
		},
		{
			name: "source range from another subnetwork", from: "google_compute_instance.ledger",
			to: "google_compute_instance.batch", port: 8050, want: cloudfw.VerdictAllowed,
		},
		{
			name: "deny before allow at equal priority on an IPv4-only default stack",
			from: "google_compute_instance.app", to: "google_compute_instance.batch", port: 22,
			want: cloudfw.VerdictDenied,
		},
	})
}

func TestRealPlanAzureCreate(t *testing.T) {
	t.Parallel()

	realRun(t, "azure-create", []realScenario{
		{
			name: "standalone rule with a subnet prefix allows the port", from: "azurerm_network_interface.batch",
			to: "azurerm_network_interface.ledger", port: 443, want: cloudfw.VerdictAllowed,
		},
		{
			name: "inline VirtualNetwork deny", from: "azurerm_network_interface.batch",
			to: "azurerm_network_interface.ledger", port: 22, want: cloudfw.VerdictDenied,
		},
		{
			name: "private endpoint in a subnet with network policies enabled", from: "azurerm_linux_virtual_machine.app",
			to: "azurerm_private_endpoint.ledger", port: 443, want: cloudfw.VerdictAllowed,
		},
		{
			name: "private endpoint behind the inline deny", from: "azurerm_network_interface.batch",
			to: "azurerm_private_endpoint.ledger", port: 22, want: cloudfw.VerdictDenied,
		},
		{
			name: "default outbound rule allows the virtual network", from: "azurerm_network_interface.ledger",
			to: "azurerm_network_interface.batch", port: 22, want: cloudfw.VerdictAllowed,
		},
		{
			name: "no NSG on the path", from: "azurerm_network_interface.app", to: "azurerm_network_interface.batch",
			port: 8080, want: cloudfw.VerdictAllowed,
		},
		{
			name: "inline rule allows an application security group", from: "azurerm_linux_virtual_machine.app",
			to: "azurerm_network_interface.ledger", port: 5432, want: cloudfw.VerdictAllowed, skip: limitMergedInlineRefs,
		},
		{
			name: "NIC outside the application security group", from: "azurerm_network_interface.batch",
			to: "azurerm_network_interface.ledger", port: 5432, want: cloudfw.VerdictDenied,
		},
	})
}

// TestRealPlanAWSDynamic runs queries against `dynamic` blocks, which the configuration of a plan does not show. No
// answer may treat a list that a dynamic block may set as empty, or resolve a group that only a dynamic block names.
func TestRealPlanAWSDynamic(t *testing.T) {
	t.Parallel()

	realRun(t, "aws-dynamic", []realScenario{
		{
			name: "dynamic block over a known list names a group that the plan does not show",
			from: "aws_network_interface.extra", to: "aws_security_group.dyn_unknown", port: 5432,
			want: cloudfw.VerdictUnknown, gap: "unknown-after-apply",
		},
		{
			name: "dynamic network_interface block over a list of unknown length", from: "aws_network_interface.extra",
			to: "aws_instance.dyn_nic_unknown_length", port: 22, want: cloudfw.VerdictUnknown,
			gap: "unknown-after-apply",
		},
		{
			name: "dynamic block over an empty list looks like no block", from: "aws_network_interface.extra",
			to: "aws_security_group.dyn_empty", port: 22, want: cloudfw.VerdictUnknown, gap: "unknown-after-apply",
		},
	})
}

// TestRealPlanAWSMixed runs queries against inline rule lists whose configuration has one merged reference list: a
// static block and a dynamic block that name different groups look like two blocks that name one group.
func TestRealPlanAWSMixed(t *testing.T) {
	t.Parallel()

	realRun(t, "aws-mixed", []realScenario{
		{
			name: "static block names group a, dynamic block names group b", from: "aws_security_group.b",
			to: "aws_security_group.mixed", port: 6432, want: cloudfw.VerdictUnknown, gap: "unresolved-reference",
		},
		{
			name: "two static blocks name groups a and b", from: "aws_security_group.b",
			to: "aws_security_group.static_two", port: 6432, want: cloudfw.VerdictUnknown, gap: "unresolved-reference",
		},
		{
			name: "one static block names group a beside a CIDR block", from: "aws_security_group.a",
			to: "aws_security_group.static_one", port: 5432, want: cloudfw.VerdictUnknown, gap: "unresolved-reference",
		},
	})
}
