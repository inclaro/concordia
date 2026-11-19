package packs_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
)

type coreRule struct {
	id       string
	severity policy.RuleSeverity
	family   policy.PredicateFamily
}

// coreRules are the rules of the core pack in file order. A rule ID is never reused or renumbered.
var coreRules = []coreRule{
	{"CORE-CYC-001", policy.SeverityError, policy.FamilyAcyclic},
	{"CORE-DECL-001", policy.SeverityError, policy.FamilyDeclaredOnly},
	{"CORE-DB-001", policy.SeverityError, policy.FamilyBoundary},
	{"CORE-BND-001", policy.SeverityError, policy.FamilyBoundary},
	{"CORE-LYR-001", policy.SeverityError, policy.FamilyLayer},
	{"CORE-LYR-002", policy.SeverityError, policy.FamilyBoundary},
	{"CORE-LYR-003", policy.SeverityWarning, policy.FamilyBoundary},
	{"CORE-SEC-001", policy.SeverityError, policy.FamilyBoundary},
	{"CORE-OBS-001", policy.SeverityWarning, policy.FamilyObservedRequired},
}

func TestNamesListsCore(t *testing.T) {
	t.Parallel()

	if !slices.Contains(packs.Names(), "core") {
		t.Fatalf("Names() = %v, want core", packs.Names())
	}

	if _, ok := packs.Source("no-such-pack"); ok {
		t.Fatal("Source of an unknown pack must report false")
	}
}

func TestCorePackCompilesWithoutDiagnostics(t *testing.T) {
	t.Parallel()

	result := compileCleanPack(t, "core")

	if len(result.Plans) != len(coreRules) {
		t.Fatalf("plans: got %d, want %d", len(result.Plans), len(coreRules))
	}
}

// assertCoreRule checks one compiled rule of the core pack against its expected shape.
func assertCoreRule(t *testing.T, want coreRule, plan *policy.RulePlan) {
	t.Helper()

	if plan.Severity != want.severity {
		t.Errorf("%s severity %s, want %s", want.id, plan.Severity, want.severity)
	}

	if plan.Predicate.Family != want.family {
		t.Errorf("%s family %s, want %s", want.id, plan.Predicate.Family, want.family)
	}

	requirePlanPack(t, plan, "core")
	requireTrimmedDocs(t, plan)

	if len(plan.UnsupportedEdgeKinds) > 0 {
		t.Errorf("%s uses edge kinds that the adapter does not support: %v", want.id, plan.UnsupportedEdgeKinds)
	}
}

func TestCoreRulesAreStable(t *testing.T) {
	t.Parallel()

	plans := compilePack(t, "core").Plans
	byID := make(map[string]*policy.RulePlan, len(plans))

	for i := range plans {
		byID[plans[i].RuleID] = &plans[i]
	}

	idPattern := regexp.MustCompile(`^CORE-[A-Z]+-\d{3}$`)

	for _, want := range coreRules {
		if !idPattern.MatchString(want.id) {
			t.Errorf("rule ID %s does not match CORE-<AREA>-<NNN>", want.id)
		}

		plan, ok := byID[want.id]
		if !ok {
			t.Errorf("rule %s is missing", want.id)

			continue
		}

		assertCoreRule(t, want, plan)
	}
}

func TestCoreBoundaryAllowsContractTags(t *testing.T) {
	t.Parallel()

	plans := compilePack(t, "core").Plans
	for i := range plans {
		plan := &plans[i]

		switch plan.RuleID {
		case "CORE-BND-001":
			if got := plan.Predicate.Allow.TargetTags; !slices.Equal(got, []string{"contract", "shared-kernel"}) {
				t.Errorf("CORE-BND-001 allowed tags: %v", got)
			}
		case "CORE-LYR-003":
			if got := plan.Predicate.Allow.TargetTags; !slices.Equal(got, []string{"shared"}) {
				t.Errorf("CORE-LYR-003 allowed tags: %v", got)
			}
		}
	}
}

func TestNamesListsTheBuiltinPacks(t *testing.T) {
	t.Parallel()

	want := []string{"adr-governance", "assurance", "core", "infrastructure", "review-currency", "views"}
	if got := packs.Names(); !slices.Equal(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

// coreDigest is the digest of the core pack as released. A built-in pack is pinned by its bytes: a new pack never
// changes it.
const coreDigest = "sha256:4e2ab84b1f2dd14c8b5ffa9015e2fd05922c627a26f275e59dd4db36155fc8c2"

func TestCorePackDigestIsUnchanged(t *testing.T) {
	t.Parallel()

	if got := digest.OfBytes(packText(t, "core")); got != coreDigest {
		t.Errorf("core digest = %s, want %s", got, coreDigest)
	}
}

func TestReviewCurrencyPackIsNotPartOfCore(t *testing.T) {
	t.Parallel()

	text := string(packText(t, "core"))

	if strings.Contains(text, "REV-") || strings.Contains(text, "assessmentCurrency") {
		t.Error("the core pack holds a review rule")
	}
}

// recordRuleID notes that the pack declares the rule and reports a rule ID that another pack already holds or that
// breaks the CORE- prefix.
func recordRuleID(t *testing.T, seen map[string]string, pack, id string) {
	t.Helper()

	if other, dup := seen[id]; dup {
		t.Errorf("rule %s is in %s and %s", id, other, pack)
	}

	seen[id] = pack

	if strings.HasPrefix(id, "CORE-") && pack != "core" {
		t.Errorf("pack %s reuses the CORE- prefix: %s", pack, id)
	}
}

func TestRuleIDsAreUniqueAcrossPacks(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}

	for _, name := range packs.Names() {
		text, _ := packs.Source(name)

		info, problems := policy.DescribePack(policy.PolicyFile{Path: packs.Prefix + name, Text: string(text)})
		if len(problems) > 0 {
			t.Fatalf("pack %s: %+v", name, problems)
		}

		for _, rule := range info.Rules {
			recordRuleID(t, seen, name, rule.ID)
		}
	}
}
