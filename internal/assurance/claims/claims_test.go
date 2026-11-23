package claims_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

func TestRegistryHasTheThirteenClaimsEachEnabledByOneContract(t *testing.T) {
	t.Parallel()

	registry := claims.Default()

	if got := len(claims.IDs()); got != 13 {
		t.Fatalf("%d claim ids, want 13", got)
	}

	checkEveryClaimIsEnabled(t, registry)

	// A claim and its contract: no contract lists a claim twice or a claim in both lists.
	contracts := registry.Contracts()
	for i := range contracts {
		checkContract(t, &contracts[i])
	}

	ids := make([]string, 0, len(contracts))

	for _, contract := range contracts {
		ids = append(ids, contract.ID)
	}

	if want := []string{"cloud-firewall", "network-policy", "plan", "provenance", "quality", "rbac", "rendering", "verification"}; !slices.Equal(ids, want) {
		t.Errorf("contract ids %v, want %v", ids, want)
	}
}

// isEnabledByAContract says whether the outcome is enabled by a contract, and has no reason.
func isEnabledByAContract(out *claims.Outcome) bool {
	if !out.Enabled {
		return false
	}

	return out.Contract != "" && out.Reason == ""
}

// isCompleteRequirement says whether the requirement names a level, asks for complete coverage, and has a valid
// integrity.
func isCompleteRequirement(req *claims.Requirement) bool {
	if req.Level == "" {
		return false
	}

	return req.Completeness == assurance.CoverageComplete && req.Integrity.Rank() >= 0
}

// checkEveryClaimIsEnabled checks that every registered claim is enabled by a contract with a complete requirement.
func checkEveryClaimIsEnabled(t *testing.T, registry *claims.Registry) {
	t.Helper()

	for _, id := range claims.IDs() {
		out := registry.Lookup(string(id))
		if !isEnabledByAContract(&out) {
			t.Errorf("claim %s: %+v", id, out)
		}

		if !isCompleteRequirement(&out.Requires) {
			t.Errorf("claim %s: requirement %+v", id, out.Requires)
		}
	}
}

// checkContract checks that the contract lists no claim twice, no claim in both lists, is experimental, and declares
// enabled claims, not enabled claims, limits, inputs, and fixtures.
func checkContract(t *testing.T, contract *claims.Contract) {
	t.Helper()

	seen := map[claims.ID]bool{}

	for _, enabled := range contract.Enabled {
		if seen[enabled.Claim] {
			t.Errorf("contract %s enables %s two times", contract.ID, enabled.Claim)
		}

		seen[enabled.Claim] = true

		if slices.Contains(contract.NotEnabled, string(enabled.Claim)) {
			t.Errorf("contract %s both enables and does not enable %s", contract.ID, enabled.Claim)
		}
	}

	if !contract.Experimental {
		t.Errorf("contract %s is not experimental before its fixtures are listed as passing", contract.ID)
	}

	declared := []int{len(contract.Enabled), len(contract.NotEnabled), len(contract.Limits), len(contract.Inputs), len(contract.Fixtures)}
	if slices.Contains(declared, 0) {
		t.Errorf("contract %s lacks enabled claims, not enabled claims, limits, inputs, or fixtures", contract.ID)
	}
}

// isNotEnabled says whether the outcome is the answer for a claim that no contract enables, with a detail.
func isNotEnabled(out *claims.Outcome) bool {
	if out.Enabled || out.Reason != claims.ReasonClaimNotEnabled {
		return false
	}

	return out.Contract == "" && out.Detail != ""
}

// isNotLoaded says whether the outcome is the answer for a registered claim whose contract is not loaded.
func isNotLoaded(out *claims.Outcome) bool {
	if out.Enabled || out.Reason != claims.ReasonClaimNotEnabled {
		return false
	}

	return strings.Contains(out.Detail, "no loaded contract")
}

func TestUnregisteredOrUnloadedClaimIsNotEnabled(t *testing.T) {
	t.Parallel()

	registry := claims.Default()

	for _, claim := range []string{"slsa-level", "test-adequacy", "absence-of-vulnerabilities", "", "network_isolation"} {
		out := registry.Lookup(claim)
		if !isNotEnabled(&out) {
			t.Errorf("claim %q: %+v", claim, out)
		}
	}

	// A registered claim whose contract is not loaded.
	partial, err := claims.New([]claims.Contract{mustContract(t, registry, "rendering")})
	if err != nil {
		t.Fatal(err)
	}

	if out := partial.Lookup("complete-resource-set"); !out.Enabled || out.Contract != "rendering" {
		t.Errorf("loaded claim: %+v", out)
	}

	if out := partial.Lookup("network-isolation"); !isNotLoaded(&out) {
		t.Errorf("unloaded claim: %+v", out)
	}

	// The contracts that say what they do not enable agree with the lookup of the names that are not registered.
	contracts := registry.Contracts()
	for i := range contracts {
		checkNotEnabledNames(t, registry, &contracts[i])
	}
}

// checkNotEnabledNames checks that the registry enables none of the names that the contract does not enable and that
// are not registered.
func checkNotEnabledNames(t *testing.T, registry *claims.Registry, contract *claims.Contract) {
	t.Helper()

	for _, name := range contract.NotEnabled {
		if !claims.Registered(claims.ID(name)) && registry.Lookup(name).Enabled {
			t.Errorf("contract %s names %s as not enabled, and the registry enables it", contract.ID, name)
		}
	}
}

func mustContract(t *testing.T, registry *claims.Registry, id string) claims.Contract {
	t.Helper()

	c, ok := registry.Contract(id)
	if !ok {
		t.Fatalf("no contract %s", id)
	}

	return c
}

func TestNewRefusesAnInvalidRegistry(t *testing.T) {
	t.Parallel()

	registry := claims.Default()
	rendering := mustContract(t, registry, "rendering")

	other := rendering
	other.ID = "other"

	unregistered := rendering
	unregistered.ID = "unregistered"
	unregistered.Enabled = []claims.Enablement{{Claim: "slsa-level"}}

	tests := []struct {
		name      string
		contracts []claims.Contract
	}{
		{"duplicate contract id", []claims.Contract{rendering, rendering}},
		{"claim enabled by two contracts", []claims.Contract{rendering, other}},
		{"claim outside the registry", []claims.Contract{unregistered}},
	}

	for _, tc := range tests {
		if _, err := claims.New(tc.contracts); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
}

func TestExperimentalContractsCapRuleSeverityAtWarning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		declared     policy.RuleSeverity
		want         policy.RuleSeverity
		experimental bool
	}{
		{policy.SeverityError, policy.SeverityWarning, true},
		{policy.SeverityWarning, policy.SeverityWarning, true},
		{policy.SeverityInfo, policy.SeverityInfo, true},
		{policy.SeverityError, policy.SeverityError, false},
		{policy.SeverityWarning, policy.SeverityWarning, false},
	}

	for _, tc := range tests {
		if got := claims.CapSeverity(tc.declared, tc.experimental); got != tc.want {
			t.Errorf("CapSeverity(%s, %v) = %s, want %s", tc.declared, tc.experimental, got, tc.want)
		}
	}

	registry := claims.Default()

	if got := registry.Lookup("exact-lineage").Severity(policy.SeverityError); got != policy.SeverityWarning {
		t.Errorf("rule needing an experimental claim: severity %s", got)
	}

	// A contract that passed its fixtures lifts the cap.
	rendering := mustContract(t, registry, "rendering")
	rendering.Experimental = false

	lifted, err := claims.New([]claims.Contract{rendering})
	if err != nil {
		t.Fatal(err)
	}

	if got := lifted.Lookup("complete-resource-set").Severity(policy.SeverityError); got != policy.SeverityError {
		t.Errorf("severity of a rule on a non-experimental contract: %s", got)
	}

	// A claim that is not enabled keeps the declared severity: the rule is unknown, not capped.
	if got := registry.Lookup("slsa-level").Severity(policy.SeverityError); got != policy.SeverityError {
		t.Errorf("severity of a rule on a claim that is not enabled: %s", got)
	}
}

// isSHA256Digest says whether the text is `sha256:` and 64 hex characters long.
func isSHA256Digest(digest string) bool {
	return strings.HasPrefix(digest, "sha256:") && len(digest) == len("sha256:")+64
}

func TestRegistryDigest(t *testing.T) {
	t.Parallel()

	registry := claims.Default()

	again, err := claims.New(registry.Contracts())
	if err != nil {
		t.Fatal(err)
	}

	if registry.Digest() != again.Digest() || !isSHA256Digest(registry.Digest()) {
		t.Errorf("digests %s and %s", registry.Digest(), again.Digest())
	}

	checkDigestIgnoresOrder(t, registry)
	checkDigestChangesWithContract(t, registry)
	checkContractsAreCopies(t, registry)
}

// checkDigestIgnoresOrder checks that the order of the input does not change the digest.
func checkDigestIgnoresOrder(t *testing.T, registry *claims.Registry) {
	t.Helper()

	reversed := registry.Contracts()
	slices.Reverse(reversed)

	reordered, err := claims.New(reversed)
	if err != nil || reordered.Digest() != registry.Digest() {
		t.Errorf("reordered digest %s err %v", reordered.Digest(), err)
	}
}

// checkDigestChangesWithContract checks that a change of a contract changes the digest.
func checkDigestChangesWithContract(t *testing.T, registry *claims.Registry) {
	t.Helper()

	changed := registry.Contracts()
	changed[0].Version += "+1"

	other, err := claims.New(changed)
	if err != nil || other.Digest() == registry.Digest() {
		t.Errorf("changed contract has the same digest (err %v)", err)
	}
}

// checkContractsAreCopies checks that changing a result of Contracts does not change the registry.
func checkContractsAreCopies(t *testing.T, registry *claims.Registry) {
	t.Helper()

	before := registry.Digest()
	registry.Contracts()[0].Limits[0] = "changed"

	if registry.Digest() != before {
		t.Error("a caller changed the registry through Contracts")
	}
}

// The registry is Concordia-owned and not part of the provider capability profile (design D23): the digest of the
// cbm-0.12.1 profile is the one it had before the registry existed, with or without contracts registered.
func TestRegisteringContractsDoesNotChangeTheCBMProfileDigest(t *testing.T) {
	t.Parallel()

	const cbmProfileDigest = "sha256:86fd92f588a5b2c26402226a41578b04cea3b156101a0d357af3217c3117a6d4"

	profile, ok := observation.ProfileFor("0.12.1", observation.AdapterSchema)
	if !ok || profile.ID != "cbm-0.12.1" {
		t.Fatal("no embedded profile cbm-0.12.1")
	}

	assertDigest := func(stage string) {
		t.Helper()

		computed, err := profile.ComputeDigest()
		if err != nil {
			t.Fatal(err)
		}

		if profile.Digest != cbmProfileDigest || computed != cbmProfileDigest {
			t.Errorf("%s: profile digest %s, computed %s, want %s", stage, profile.Digest, computed, cbmProfileDigest)
		}
	}

	assertDigest("before")

	registry := claims.Default()
	if _, err := claims.New(registry.Contracts()); err != nil {
		t.Fatal(err)
	}

	assertDigest("after registering the contracts")
}

// Every fixture that a contract names exists, so `doctor` never lists a fixture that is not there.
func TestContractFixturesExist(t *testing.T) {
	t.Parallel()

	for _, contract := range claims.Default().Contracts() {
		for _, fixture := range contract.Fixtures {
			if _, err := os.Stat(filepath.Join("..", "..", "..", filepath.FromSlash(fixture))); err != nil {
				t.Errorf("contract %s: fixture %s: %v", contract.ID, fixture, err)
			}
		}
	}
}
