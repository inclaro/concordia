package e2e_test

import (
	"encoding/json"
	"testing"
)

const (
	paymentsProject = "e2e-payments"
	ruleCloudNet    = "PAY-NET-002"
	openedByRule    = "aws_vpc_security_group_ingress_rule.ledger_from_gateway"
)

// paymentsVerdicts are the verdicts that the use-case pages and tutorials show for the payments example.
var paymentsVerdicts = map[string]string{
	"PAY-API-001":   verdictFail,
	"PAY-CYC-001":   verdictFail,
	"PAY-PATH-001":  verdictFail,
	"PAY-DB-001":    verdictFail,
	"CORE-SEC-001":  verdictFail,
	"CORE-DECL-001": verdictFail,
	"PAY-NET-001":   verdictFail,
	ruleCloudNet:    verdictFail,
	"PAY-RBAC-001":  verdictFail,
	"PAY-LEG-001":   "unknown",
	"PAY-TLS-001":   "unknown",
	"ASR-PRV-001":   "unknown",
	"GOV-ADR-001":   "pass",
	"GOV-ADR-004":   "pass",
}

// paymentsFinding is the part of a finding that the payments subtests read beyond checkFinding.
type paymentsFinding struct {
	RuleID   string `json:"ruleId"`
	Observed string `json:"observed"`
}

// paymentsRun is the index and check result that every payments subtest reads.
type paymentsRun struct {
	doc   *checkDoc
	check cliResult
}

// The bank payments example that the documentation uses. TestPayments indexes and checks the example once,
// before the subtests, so that a -run filter that selects one subtest still has the result.
func TestPayments(t *testing.T) {
	requireE2E(t)

	repo := newRepo(t, repoSpec{source: "../../docs/examples/payments", oldName: "payments-example", project: paymentsProject, dirName: "payments"})

	if res := repo.gu("index"); res.code != 0 {
		t.Fatalf("index: exit %d: %s", res.code, res.stderr)
	}

	run := new(paymentsRun)
	run.doc, run.check = repo.checkJSON(nil)

	t.Run("index and check: every planted finding gives the documented verdict", run.checkVerdicts)
	t.Run("check: the PAY-NET-002 finding names the security group rule that opens the port", run.cloudFirewallFinding)
}

func (p *paymentsRun) checkVerdicts(t *testing.T) {
	doc, res := p.doc, p.check

	if res.code != 1 {
		t.Fatalf("exit %d, want 1: %s", res.code, res.stderr)
	}

	for id, want := range paymentsVerdicts {
		rule := doc.rule(id)
		if rule == nil {
			t.Errorf("%s: rule not in the result", id)

			continue
		}

		if rule.Verdict != want {
			t.Errorf("%s: verdict %q, want %q", id, rule.Verdict, want)
		}
	}
}

func (p *paymentsRun) cloudFirewallFinding(t *testing.T) {
	var doc struct {
		Findings []paymentsFinding `json:"findings"`
	}

	if err := json.Unmarshal([]byte(p.check.stdout), &doc); err != nil {
		t.Fatal(err)
	}

	found := false

	for _, f := range doc.Findings {
		if f.RuleID == ruleCloudNet {
			found = true

			containsAll(t, f.Observed, "tcp/5432", openedByRule)
		}
	}

	if !found {
		t.Errorf("no %s finding", ruleCloudNet)
	}
}
