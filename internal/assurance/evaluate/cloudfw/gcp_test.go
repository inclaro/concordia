package cloudfw_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

const (
	gcpFixture = "../../../../test/fixtures/assurance/plans/cloudfw-gcp.json"
	gcpWeb     = "google_compute_instance.web"
	gcpDB      = "google_compute_instance.db"
	gcpPeer    = "google_compute_instance.peer"
	gcpBatch   = "google_compute_instance_template.batch"
	gcpImplied = "implied priority 65535 "
)

// gcpScenario is one query against the GCP fixture plan, after an optional mutation of the loaded input.
type gcpScenario struct {
	mutate   func(in *cloudfw.Input)
	name     string
	from, to string
	protocol string
	want     cloudfw.Verdict
	// gap is the expected gap kind of an unknown result, "" when the result has no gaps.
	gap string
	// rules is the expected `<address> <rule>` list of the deciding rules, joined by ", "; "-" when not checked.
	rules string
	port  int
}

func TestGCPFirewallSemantics(t *testing.T) {
	t.Parallel()

	for _, sc := range slices.Concat(gcpVerdictScenarios(), gcpUnknownScenarios(), gcpReviewScenarios()) {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			gcpAssert(t, &sc)
		})
	}
}

// gcpVerdictScenarios are queries that the rules of the plan decide.
func gcpVerdictScenarios() []gcpScenario {
	return []gcpScenario{
		{
			name: "tag reference allows the port", from: gcpWeb, to: gcpDB, port: 5432, want: cloudfw.VerdictAllowed,
			rules: "google_compute_firewall.allow_web_to_db priority 1000 allow, " + gcpImplied + "allow",
		},
		{
			name: "no rule allows the port", from: gcpWeb, to: gcpDB, port: 22, want: cloudfw.VerdictDenied,
			rules: gcpImplied + "deny",
		},
		{
			name: "network reference unknown after apply resolved by configuration", from: gcpBatch, to: gcpDB,
			port: 53, protocol: cloudfw.ProtocolUDP, want: cloudfw.VerdictAllowed,
			rules: "google_compute_firewall.allow_batch_dns priority 1000 allow, " + gcpImplied + "allow",
		},
		{
			name: "rule for another protocol does not allow", from: gcpBatch, to: gcpDB, port: 53,
			want: cloudfw.VerdictDenied, rules: gcpImplied + "deny",
		},
		{
			name: "deny and allow at equal priority", from: gcpWeb, to: gcpDB, port: 6379, want: cloudfw.VerdictDenied,
			rules: "google_compute_firewall.deny_web_redis priority 900 deny",
		},
		{
			name: "egress deny at the source", from: gcpWeb, to: gcpDB, port: 3306, want: cloudfw.VerdictDenied,
			rules: "google_compute_firewall.deny_web_mysql_out priority 1000 deny",
		},
		{
			name: "service account source and target allow the port", from: gcpWeb, to: gcpDB, port: 9050,
			want:  cloudfw.VerdictAllowed,
			rules: "google_compute_firewall.allow_metrics_accounts priority 1000 allow, " + gcpImplied + "allow",
		},
		{
			name: "rule without matching target tag does not apply", from: gcpDB, to: gcpWeb, port: 5432,
			want: cloudfw.VerdictDenied, rules: gcpImplied + "deny",
		},
		{
			name: "rule without matching source tag does not apply", from: gcpBatch, to: gcpDB, port: 5432,
			want: cloudfw.VerdictDenied, rules: gcpImplied + "deny",
		},
	}
}

// gcpUnknownScenarios are queries that the plan cannot decide.
func gcpUnknownScenarios() []gcpScenario {
	return []gcpScenario{
		{
			name: "CIDR that partly overlaps the source subnet", from: gcpWeb, to: gcpDB, port: 8080,
			want: cloudfw.VerdictUnknown, gap: evaluate.GapAddressDependent,
			rules: "google_compute_firewall.allow_app_range priority 1000 allow",
		},
		{
			name: "firewall policy in the plan", from: gcpWeb, to: gcpDB, port: 5432, mutate: gcpAddFirewallPolicy,
			want: cloudfw.VerdictUnknown, gap: evaluate.GapUnsupportedKind, rules: "",
		},
		{
			name: "endpoints in different networks", from: gcpWeb, to: gcpPeer, port: 22,
			want: cloudfw.VerdictUnknown, gap: evaluate.GapPeeringNotEvaluated, rules: "",
		},
		{
			name: "network unknown after apply without a configuration reference", from: gcpWeb, to: gcpDB, port: 5432,
			mutate: gcpDropReferences(gcpWeb), want: cloudfw.VerdictUnknown, gap: evaluate.GapUnknownAfterApply, rules: "",
		},
		{
			name: "unsupported endpoint type", from: gcpWeb, to: "google_compute_network.vpc", port: 22,
			want: cloudfw.VerdictUnknown, gap: evaluate.GapUnsupportedResource, rules: "",
		},
	}
}

func gcpAssert(t *testing.T, sc *gcpScenario) {
	t.Helper()

	in := gcpLoad(t)
	if sc.mutate != nil {
		sc.mutate(in)
	}

	q := &cloudfw.Query{From: cloudfw.Endpoint{Address: sc.from}, To: cloudfw.Endpoint{Address: sc.to},
		Protocol: sc.protocol, Port: sc.port}
	res := cloudfw.New(in).Evaluate(q)

	if res.Verdict != sc.want {
		t.Fatalf("verdict = %s, want %s (reason %q, gaps %v, rules %v)",
			res.Verdict, sc.want, res.Reason, res.Gaps, res.Rules)
	}

	gcpAssertGaps(t, sc, &res)
	gcpAssertAssumptions(t, &res)

	if got := gcpRules(&res); sc.rules != "-" && got != sc.rules {
		t.Errorf("rules = %q, want %q", got, sc.rules)
	}
}

func gcpAssertGaps(t *testing.T, sc *gcpScenario, res *cloudfw.Result) {
	t.Helper()

	if sc.gap == "" {
		if len(res.Gaps) != 0 {
			t.Errorf("gaps = %v, want none", res.Gaps)
		}

		return
	}

	if !slices.ContainsFunc(res.Gaps, func(g assurance.Gap) bool { return g.Kind == sc.gap }) || res.Reason == "" {
		t.Errorf("gaps = %v (reason %q), want a %s gap and a reason", res.Gaps, res.Reason, sc.gap)
	}
}

func gcpAssertAssumptions(t *testing.T, res *cloudfw.Result) {
	t.Helper()

	want := []string{}
	if res.Verdict == cloudfw.VerdictDenied {
		want = []string{"firewall-rules-complete-in-plan"}
	}

	if !slices.Equal(res.Assumptions, want) {
		t.Errorf("assumptions = %v, want %v", res.Assumptions, want)
	}
}

func gcpRules(res *cloudfw.Result) string {
	refs := make([]string, 0, len(res.Rules))
	for _, r := range res.Rules {
		refs = append(refs, r.Address+" "+r.Rule)
	}

	return strings.Join(refs, ", ")
}

// gcpLoad reads the GCP fixture through the plan reader.
func gcpLoad(t *testing.T) *cloudfw.Input {
	t.Helper()

	data, err := os.ReadFile(gcpFixture)
	if err != nil {
		t.Fatal(err)
	}

	p, diags := plan.Read(data, "cloudfw-gcp.json")
	if p == nil {
		t.Fatalf("plan rejected: %v", diags)
	}

	return planInput(p, "cloudfw-gcp.json")
}

// gcpAddFirewallPolicy adds a network firewall policy, a construct that the evaluator does not read.
func gcpAddFirewallPolicy(in *cloudfw.Input) {
	in.Plans[0].Resources = append(in.Plans[0].Resources, plan.Resource{
		Address: "google_compute_network_firewall_policy.main", Mode: "managed",
		Type: "google_compute_network_firewall_policy", Name: "main", Actions: []string{"create"},
		Values: map[string]any{"name": "main"},
	})
}

// gcpDropReferences removes the configuration references of a resource.
func gcpDropReferences(address string) func(in *cloudfw.Input) {
	return gcpEdit(address, func(res *plan.Resource) { res.References = nil })
}
