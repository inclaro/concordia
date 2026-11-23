package cloudfw_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
	"github.com/inclaro/concordia/internal/workspace"
)

// Addresses of the Azure fixture.
const (
	azureVM        = "azurerm_linux_virtual_machine.app"
	azureDBNIC     = "azurerm_network_interface.db"
	azureDBPE      = "azurerm_private_endpoint.db"
	azureVNet      = "azurerm_virtual_network.main"
	azureAppNSG    = "azurerm_network_security_group.app"
	azureDBNSG     = "azurerm_network_security_group.db"
	azureDBNICNSG  = "azurerm_network_security_group.db_nic"
	azureVNetOut   = azureAppNSG + " AllowVnetOutBound (priority 65000)"
	azureVNetNICIn = azureDBNICNSG + " AllowVnetInBound (priority 65000)"
)

// azureScenario is one query against the Azure fixture plan, with an optional change of the loaded input.
type azureScenario struct {
	mutate func(in *cloudfw.Input)
	name   string
	from   string
	to     string
	want   cloudfw.Verdict
	reason string
	rules  string
	gaps   string
	port   int
}

func azureScenarios() []azureScenario {
	return slices.Concat(azureAllowScenarios(), azureDenyScenarios(), azureUnknownScenarios(), azureReviewScenarios())
}

func azureAllowScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "application security group reference unknown after apply resolves by configuration and allows",
			from: azureVM, to: azureDBNIC, port: 5432, want: cloudfw.VerdictAllowed,
			rules: azureRules(azureVNetOut, azureDBNSG+" allow-postgres (priority 300)", azureVNetNICIn),
		},
		{
			name: "source prefix that contains the source subnet allows a port of a port list",
			from: azureVM, to: azureDBNIC, port: 6379, want: cloudfw.VerdictAllowed,
			rules: azureRules(azureVNetOut, azureDBNSG+" allow-redis (priority 310)", azureVNetNICIn),
		},
		{
			name: "standalone rule found by NSG name allows",
			from: azureVM, to: azureDBNIC, port: 8443, want: cloudfw.VerdictAllowed,
			rules: azureRules(azureVNetOut, azureVNetNICIn, "azurerm_network_security_rule.https allow-https (priority 340)"),
		},
		{
			name: "private endpoint without a NIC is checked at its subnet NSG",
			from: azureVM, to: azureDBPE, port: 5432, want: cloudfw.VerdictAllowed,
			rules: azureRules(azureVNetOut, azureDBNSG+" allow-postgres (priority 300)"),
		},
		{
			name: "without custom rules the default AllowVnetInBound allows traffic in one virtual network",
			from: azureVM, to: azureDBNIC, port: 5432, want: cloudfw.VerdictAllowed,
			mutate: azureWithoutDBRules,
			rules:  azureRules(azureVNetOut, azureDBNSG+" AllowVnetInBound (priority 65000)", azureVNetNICIn),
		},
	}
}

func azureDenyScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "no rule allows the port: the custom deny-all decides",
			from: azureVM, to: azureDBNIC, port: 9999, want: cloudfw.VerdictDenied,
			rules: azureRules(azureDBNSG + " deny-all-in (priority 4096)"),
		},
		{
			name: "deny at priority 100 comes before an allow at priority 200",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictDenied,
			rules: azureRules(azureDBNSG + " deny-ssh (priority 100)"),
		},
		{
			name: "NIC NSG denies what the subnet NSG allows",
			from: azureVM, to: azureDBNIC, port: 6380, want: cloudfw.VerdictDenied,
			rules: azureRules(azureDBNICNSG + " deny-redis-tls (priority 100)"),
		},
	}
}

func azureUnknownScenarios() []azureScenario {
	return []azureScenario{
		{
			name: "source prefix that covers part of the source subnet",
			from: azureVM, to: azureDBNIC, port: 8080, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			rules: azureRules(azureDBNSG + " allow-web-half (priority 320)"),
			gaps:  evaluate.GapAddressDependent + ": 10.1.1.0/25 covers only part of 10.1.1.0/24",
		},
		{
			name: "service tag is not evaluated",
			from: azureVM, to: azureDBNIC, port: 443, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			rules: azureRules(azureDBNSG + " allow-storage (priority 330)"),
			gaps:  evaluate.GapUnsupportedKind + ": service tag Storage",
		},
		{
			name: "group reference unknown after apply without configuration references",
			from: azureVM, to: azureDBNIC, port: 5432, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonCoverageIncomplete,
			mutate: azureWithoutReferences(azureDBNSG),
			rules:  azureRules(azureDBNSG + " allow-postgres (priority 300)"),
			gaps: evaluate.GapUnknownAfterApply + ": " + azureDBNSG +
				".security_rule[2].source_application_security_group_ids",
		},
		{
			name: "endpoint type that is not supported",
			from: azureVNet, to: azureDBNIC, port: 5432, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			rules: "",
			gaps:  evaluate.GapUnsupportedResource + ": " + azureVNet + ": resource type azurerm_virtual_network is not supported",
		},
		{
			name: "deny limited to a source port at priority 100 before an allow at priority 200",
			from: azureVM, to: azureDBNIC, port: 22, want: cloudfw.VerdictUnknown, reason: evaluate.ReasonUnsupportedConstruct,
			mutate: azureDenySSHFromSourcePort,
			rules:  azureRules(azureDBNSG + " deny-ssh (priority 100)"),
			gaps:   evaluate.GapUnsupportedKind + ": source port range",
		},
	}
}

func TestAzureScenarios(t *testing.T) {
	t.Parallel()

	for _, sc := range azureScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			azureAssert(t, &sc)
		})
	}
}

func azureAssert(t *testing.T, sc *azureScenario) {
	t.Helper()

	in := azureLoad(t)
	if sc.mutate != nil {
		sc.mutate(in)
	}

	got := cloudfw.New(in).Evaluate(&cloudfw.Query{
		From: cloudfw.Endpoint{Address: sc.from}, To: cloudfw.Endpoint{Address: sc.to}, Protocol: cloudfw.ProtocolTCP, Port: sc.port,
	})

	if got.Verdict != sc.want || got.Reason != sc.reason {
		t.Fatalf("verdict = %s (%q), want %s (%q); gaps %v", got.Verdict, got.Reason, sc.want, sc.reason, got.Gaps)
	}

	if rules := azureRuleText(got.Rules); rules != sc.rules {
		t.Errorf("rules = %q, want %q", rules, sc.rules)
	}

	if gaps := azureGapText(&got); gaps != sc.gaps {
		t.Errorf("gaps = %q, want %q", gaps, sc.gaps)
	}

	azureAssertAssumptions(t, &got)
}

// azureAssertAssumptions checks that only a denied result rests on the completeness assumption.
func azureAssertAssumptions(t *testing.T, got *cloudfw.Result) {
	t.Helper()

	want := []string{}
	if got.Verdict == cloudfw.VerdictDenied {
		want = []string{workspace.AssumeFirewallRulesInPlan}
	}

	if !slices.Equal(got.Assumptions, want) {
		t.Errorf("assumptions = %v, want %v", got.Assumptions, want)
	}
}

func azureRules(rules ...string) string { return strings.Join(rules, "; ") }

func azureRuleText(refs []cloudfw.RuleRef) string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Address+" "+r.Rule)
	}

	return strings.Join(out, "; ")
}

func azureGapText(res *cloudfw.Result) string {
	out := make([]string, 0, len(res.Gaps))
	for _, g := range res.Gaps {
		out = append(out, g.Kind+": "+g.Detail)
	}

	return strings.Join(out, "; ")
}

// azureLoad reads the Azure fixture plan through the plan reader.
func azureLoad(t *testing.T) *cloudfw.Input {
	t.Helper()

	file := filepath.Join("..", "..", "..", "..", "test", "fixtures", "assurance", "plans", "cloudfw-azure.json")

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	p, diags := plan.Read(data, "cloudfw-azure.json")
	if p == nil {
		t.Fatalf("plan not read: %v", diags)
	}

	return &cloudfw.Input{Resources: p.Resources, Coverage: p.Coverage, Digest: p.Digest}
}

// azureResource is the resource at the address of the input.
func azureResource(in *cloudfw.Input, address string) *plan.Resource {
	i := slices.IndexFunc(in.Resources, func(r plan.Resource) bool { return r.Address == address })

	return &in.Resources[i]
}

// azureWithoutReferences drops the configuration references of a resource.
func azureWithoutReferences(address string) func(in *cloudfw.Input) {
	return func(in *cloudfw.Input) { azureResource(in, address).References = nil }
}

// azureWithoutDBRules drops the inline rules of the database NSG, so only the default rules apply.
func azureWithoutDBRules(in *cloudfw.Input) {
	nsg := azureResource(in, azureDBNSG)
	nsg.Values = maps.Clone(nsg.Values)
	nsg.Values["security_rule"] = []any{}
	nsg.Unknown = slices.DeleteFunc(slices.Clone(nsg.Unknown), func(p string) bool { return strings.HasPrefix(p, "security_rule") })
}

// azureDenySSHFromSourcePort limits the deny-ssh rule (priority 100) of the database NSG to source port 1234.
func azureDenySSHFromSourcePort(in *cloudfw.Input) {
	nsg := azureResource(in, azureDBNSG)
	list, _ := nsg.Values["security_rule"].([]any)
	rules := slices.Clone(list)
	first, _ := rules[0].(map[string]any)
	denySSH := maps.Clone(first)
	denySSH["source_port_range"] = "1234"
	rules[0] = denySSH
	nsg.Values = maps.Clone(nsg.Values)
	nsg.Values["security_rule"] = rules
}
