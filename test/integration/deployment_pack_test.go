package integration_test

import (
	"strings"
	"testing"
)

// The `infrastructure` pack on the workspace of the deployment tests: `policy list` names it, and `policy verify` shows the
// declaration of each rule, with `deployedIn`, `from-image`, and `passCompleteness` where the rule declares them.

func TestPolicyListAndVerifyShowTheInfrastructurePack(t *testing.T) {
	t.Parallel()

	d := &deployment{w: newDeploymentWorkspace(t, &deploymentSpec{})}
	d.rec = d.w.rec

	list := d.cli(t, "policy", "list", "--rules")
	if list.code != 0 {
		t.Fatalf("policy list: exit %d, stderr %q", list.code, list.stderr)
	}

	for _, want := range []string{"builtin:infrastructure", "6 rules", "INFRA-PLC-001", "INFRA-PLC-002", "INFRA-TRC-001", "INFRA-TRC-002", "INFRA-TRC-003", "INFRA-TRC-004"} {
		if !strings.Contains(list.stdout, want) {
			t.Errorf("policy list lacks %q:\n%s", want, list.stdout)
		}
	}

	verify := d.cli(t, "policy", "verify")
	if verify.code != 0 {
		t.Fatalf("policy verify: exit %d, stdout %q, stderr %q", verify.code, verify.stdout, verify.stderr)
	}

	lines := map[string]string{}

	for line := range strings.SplitSeq(verify.stdout, "\n") {
		for _, id := range []string{"INFRA-PLC-001", "INFRA-PLC-002", "INFRA-TRC-001", "INFRA-TRC-002", "INFRA-TRC-003", "INFRA-TRC-004"} {
			if strings.Contains(line, id) {
				lines[id] = line
			}
		}
	}

	for id, wants := range map[string][]string{
		"INFRA-PLC-001": {"required forAll", "placed-in", "deployedIn=true", "passCompleteness=any"},
		"INFRA-PLC-002": {"forbidden forAll", "placed-in", "deployedIn=false"},
		"INFRA-TRC-001": {"required exists", "from-workload", "placed-in", "passCompleteness=any"},
		"INFRA-TRC-002": {"required forAll", "from-workload", "uses-image", "passCompleteness=partial"},
		"INFRA-TRC-003": {"required forAll", "from-image", "built-from", "passCompleteness=any"},
		"INFRA-TRC-004": {"required forAll", "from-workload", "uses-image", "completeness=complete"},
	} {
		line, ok := lines[id]
		if !ok {
			t.Errorf("policy verify does not show %s:\n%s", id, verify.stdout)

			continue
		}

		for _, want := range wants {
			if !strings.Contains(line, want) {
				t.Errorf("%s declaration %q lacks %q", id, line, want)
			}
		}
	}

	if strings.Contains(lines["INFRA-TRC-004"], "passCompleteness") || strings.Contains(lines["INFRA-PLC-002"], "passCompleteness") {
		t.Errorf("a rule without passCompleteness shows it: %q, %q", lines["INFRA-TRC-004"], lines["INFRA-PLC-002"])
	}
}
