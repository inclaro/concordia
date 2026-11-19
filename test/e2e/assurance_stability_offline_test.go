package e2e_test

import (
	"strings"
	"testing"
)

// Task 11.3 offline, with a trusted root: no network is used, the bundle carries what cosign needs, and the statements
// are verified with the trusted root that the workspace declares. Two runs agree.
func TestAssuranceOfflineWithTrustedRoot(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	w := asrLineageWorld(t)
	w.verification = "    trustedRoot: .sigstore/trusted_root.json\n"
	w.extraFiles = map[string]string{".sigstore/trusted_root.json": "{}\n"}
	r := w.build()

	first, res := r.check("--offline")
	if res.code != 0 {
		t.Fatalf("exit code = %d\n%s", res.code, res.stderr)
	}

	for _, container := range []string{"app", "sidecar"} {
		if entry := lineageOf(t, first, container); !asrStabilityAuthenticated(entry) {
			t.Errorf("%s lineage = %s", container, entry.text())
		}
	}

	asrStabilityRequireTrustedRootCommands(t, r)

	second, secondRes := r.check("--offline")
	asrStabilityRequireRunsAgree(t, "two offline runs differ", asrStabilityRun{first, res}, asrStabilityRun{second, secondRes})

	// The same inputs online give the same statement results and lineage.
	online, _ := r.check()
	if a, b := first.at("assurance", "lineage").text(), online.at("assurance", "lineage").text(); a != b {
		t.Errorf("the lineage differs between offline and online: %s", firstDifference(a, b))
	}
}

// asrStabilityAuthenticated tells whether a lineage entry passes with a statement that its producer authenticated.
func asrStabilityAuthenticated(entry jv) bool {
	if entry.at("verdict").str() != "pass" {
		return false
	}

	return entry.at("statement", "integrity").str() == "producer-authenticated"
}

// asrStabilityRequireTrustedRootCommands says that cosign was started, and that every verification used the trusted root.
func asrStabilityRequireTrustedRootCommands(t *testing.T, r *asrRepo) {
	t.Helper()

	commands := r.cosign.commands(t)
	if r.cosign.starts(t) == 0 || !strings.Contains(commands, "--trusted-root") {
		t.Errorf("cosign was not started with the trusted root:\n%s", commands)
	}

	for _, line := range strings.Split(strings.TrimSpace(commands), "\n") {
		if !strings.Contains(line, "--trusted-root") {
			t.Errorf("a verification without the trusted root under --offline: %s", line)
		}
	}
}

// Task 11.3 offline, without a trusted root: cosign is not started, the statements are not authenticated, the claims
// that need them are unknown (not an error), and the result is the same on every run.
func TestAssuranceOfflineWithoutTrustedRootIsUnknown(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	r := asrLineageWorld(t).build()

	first, res := r.check("--offline")

	// Unknown is not an error: no exit code of a failed toolchain, input, or network.
	if res.code != 0 {
		t.Errorf("exit code = %d, want 0 (the rules are advisory)\n%s", res.code, res.stderr)
	}

	if n := r.cosign.starts(t); n != 0 {
		t.Errorf("cosign was started %d times under --offline without a trusted root:\n%s", n, r.cosign.commands(t))
	}

	for _, container := range []string{"app", "sidecar"} {
		entry := lineageOf(t, first, container)
		if entry.at("verdict").str() != "unknown" || entry.at("statement").present() {
			t.Errorf("%s lineage = %s", container, entry.text())
		}
	}

	asrStabilityRequireUnauthenticatedProvenance(t, first)

	// The independent claims continue, and the explanation names the reason.
	if got := first.rule("ASR-REN-001").at("verdict").str(); got != "pass" {
		t.Errorf("ASR-REN-001 = %s", got)
	}

	containsAll(t, r.explain("ASR-PRV-001").output(), "Verdict: unknown")

	second, secondRes := r.check("--offline")
	asrStabilityRequireRunsAgree(t, "two offline runs differ", asrStabilityRun{first, res}, asrStabilityRun{second, secondRes})

	if n := r.cosign.starts(t); n != 0 {
		t.Errorf("cosign was started %d times", n)
	}
}

// asrStabilityRequireUnauthenticatedProvenance says that the provenance statement is not authenticated, not admissible,
// and names the offline reason.
func asrStabilityRequireUnauthenticatedProvenance(t *testing.T, doc jv) {
	t.Helper()

	provenance := doc.at("assurance", "statements").where("kind", "build-provenance")
	authenticated := provenance.at("integrity").str() == "producer-authenticated"
	admitted := provenance.at("admissible").text() != "false"
	named := strings.Contains(provenance.at("details").text(), "offline-verification-unavailable")

	if authenticated || admitted || !named {
		t.Errorf("provenance statement = %s", provenance.text())
	}
}
