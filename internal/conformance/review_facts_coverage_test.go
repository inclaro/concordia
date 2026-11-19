package conformance_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// Scenario: Empty match under complete coverage.
func TestReviewFactsEmptyMatchUnderCompleteCoverage(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)

	for _, sel := range []impact.Selector{
		{Kind: impact.KindSecretReference, Environment: "staging"},
		{Kind: impact.KindPublicEntryPoint, Match: map[string][]string{"host": {"nope.example.com"}}},
	} {
		match := items(t, facts, &sel)
		if rvAnyOf(match.State != impact.MatchEmpty, len(match.Items) != 0, match.Coverage != "complete") {
			t.Errorf("%s %v: %+v", sel.Kind, sel.Match, match)
		}
	}
}

// rvRequireUndeclaredSecret fails the test unless the production secret references are unknown because of the
// undeclared target, with the one declared reference kept as an item.
func rvRequireUndeclaredSecret(t *testing.T, facts impact.FactSource) {
	t.Helper()

	match := items(t, facts, &impact.Selector{Kind: impact.KindSecretReference, Environment: "production"})
	if match.State != impact.MatchUnknown || match.Reason != impact.ReasonTargetNotDeclared {
		t.Fatalf("secret-reference: %+v", match)
	}

	if rvAnyOf(!slices.Equal(match.LossModes, []string{observation.LossTargetNotDeclared}), match.Coverage != "partial", len(match.Items) != 1) {
		t.Errorf("loss modes %v, coverage %s, items %v", match.LossModes, match.Coverage, identities(&match))
	}

	// A selection that names the undeclared reference holds no item and is never an empty match.
	named := items(t, facts, &impact.Selector{
		Kind: impact.KindSecretReference, Environment: "production", Match: map[string][]string{"reference": {"core/Secret/payments/missing-secret"}},
	})
	if rvAnyOf(named.State != impact.MatchUnknown, named.Reason != impact.ReasonTargetNotDeclared, len(named.Items) != 0) {
		t.Errorf("named reference: %+v", named)
	}
}

// Scenario: Undeclared target is unknown.
func TestReviewFactsUndeclaredTargetIsUnknown(t *testing.T) {
	t.Parallel()

	gap := observation.CoverageGap{
		Reason: observation.LossTargetNotDeclared, Path: rvProdDeploy, Environment: "production", Workload: "Deployment/api",
		ReferenceMode: "env", Target: "missing-secret", TargetKind: "Secret", Line: 20,
	}
	facts := (&rvWorld{gaps: []observation.CoverageGap{gap}}).facts(t)

	rvRequireUndeclaredSecret(t, facts)

	// The gap is about a Secret: it does not limit configuration references, and not the staging environment.
	if other := items(t, facts, &impact.Selector{Kind: impact.KindConfigurationReference, Environment: "production"}); other.State != impact.MatchFound {
		t.Errorf("configuration-reference: %+v", other)
	}

	if staging := items(t, facts, &impact.Selector{Kind: impact.KindSecretReference, Environment: "staging"}); staging.State != impact.MatchEmpty {
		t.Errorf("staging: %+v", staging)
	}
}

// Scenario: Two containers share an image repository.
func TestReviewFactsMergedOccurrencesAreUnknown(t *testing.T) {
	t.Parallel()

	gap := observation.CoverageGap{
		Reason: observation.LossMergedOccurrences, Path: rvProdDeploy, Environment: "production", Workload: "Deployment/api",
		Container: "sidecar", Detail: "container has no uses-image fact",
	}
	facts := (&rvWorld{gaps: []observation.CoverageGap{gap}}).facts(t)

	match := items(t, facts, &impact.Selector{Kind: impact.KindImplementation, Environment: "production", Identities: []string{rvIdentity}})
	if match.State != impact.MatchUnknown || match.Reason != impact.ReasonCoverageIncomplete {
		t.Fatalf("implementation: %+v", match)
	}

	if match.Coverage != "partial" || !slices.Equal(match.LossModes, []string{observation.LossMergedOccurrences}) {
		t.Errorf("coverage %s, loss modes %v", match.Coverage, match.LossModes)
	}

	if len(match.Items) != 1 || match.Items[0].Facets["subject"][0] != "registry.example.com/payments/api:1.2.0" {
		t.Errorf("surviving occurrence = %v", match.Items)
	}

	// The staging workload has no merged occurrence.
	if staging := items(t, facts, &impact.Selector{Kind: impact.KindImplementation, Environment: "staging"}); staging.State != impact.MatchFound {
		t.Errorf("staging: %+v", staging)
	}
}

// rvEntryPointDigest gives the identity and the sealed digest of the one public entry point of the environment.
func rvEntryPointDigest(t *testing.T, facts impact.FactSource, env string) (identity, sum string) {
	t.Helper()

	match := items(t, facts, &impact.Selector{Kind: impact.KindPublicEntryPoint, Environment: env})
	if len(match.Items) != 1 {
		t.Fatalf("%s entry points = %v", env, identities(&match))
	}

	sealed, err := impact.V1().Seal(match.Items[0], func(string) (string, bool, error) { return "", false, nil })
	if err != nil {
		t.Fatal(err)
	}

	return match.Items[0].Identity, sealed.Digest
}

// Scenario: Same host and path in two environments.
func TestReviewFactsRouteIdentityIsTheScopedIngress(t *testing.T) {
	t.Parallel()

	base := (&rvWorld{}).facts(t)
	changed := (&rvWorld{stagingBackend: "api-v2"}).facts(t)

	prodIdentity, prodBefore := rvEntryPointDigest(t, base, "production")
	_, prodAfter := rvEntryPointDigest(t, changed, "production")
	stagingIdentity, stagingBefore := rvEntryPointDigest(t, base, "staging")
	_, stagingAfter := rvEntryPointDigest(t, changed, "staging")

	if rvAnyOf(!strings.Contains(prodIdentity, "k8s:production/"), !strings.Contains(stagingIdentity, "k8s:staging/"), prodIdentity == stagingIdentity) {
		t.Errorf("identities %s, %s", prodIdentity, stagingIdentity)
	}

	if prodBefore != prodAfter {
		t.Error("a change to the staging Ingress changed the digest of the production entry point")
	}

	if stagingBefore == stagingAfter {
		t.Error("a change to the staging Ingress backend did not change its digest")
	}

	// Host and path are facets of the item, taken from the Route node that routes to its own Service.
	entry := items(t, base, &impact.Selector{Kind: impact.KindPublicEntryPoint, Environment: "production"}).Items[0]
	if !slices.Equal(entry.Facets["host"], []string{"api.example.com"}) || !slices.Equal(entry.Facets["path"], []string{"/"}) {
		t.Errorf("facets = %v", entry.Facets)
	}
}

func TestReviewFactsSelectorsByCalmNodeAndIdentity(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)

	byNode := items(t, facts, &impact.Selector{Kind: impact.KindSecretReference, CalmNodes: []string{"api-service"}})
	if len(byNode.Items) != 1 {
		t.Errorf("by CALM node: %v", identities(&byNode))
	}

	byOther := items(t, facts, &impact.Selector{Kind: impact.KindSecretReference, CalmNodes: []string{"a-service"}})
	if byOther.State != impact.MatchEmpty {
		t.Errorf("other CALM node: %+v", byOther)
	}

	byWorkload := items(t, facts, &impact.Selector{Kind: impact.KindRuntimeIdentity, Identities: []string{rvIdentity}})
	if len(byWorkload.Items) != 1 {
		t.Errorf("by workload identity: %v", identities(&byWorkload))
	}
}

func TestReviewFactsRuntimeIdentityCarriesRoleReferences(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)
	match := items(t, facts, &impact.Selector{Kind: impact.KindRuntimeIdentity, Identities: []string{rvIdentity}})

	if got := match.Items[0].Facets["roleReferences"]; !slices.Equal(got, []string{"rbac.authorization.k8s.io/Role/payments/api-role"}) {
		t.Errorf("roleReferences = %v", got)
	}
}

func TestReviewFactsExternalDestination(t *testing.T) {
	t.Parallel()

	facts := (&rvWorld{}).facts(t)
	match := items(t, facts, &impact.Selector{Kind: impact.KindExternalDestination})

	if len(match.Items) != 1 || !strings.HasPrefix(match.Items[0].Identity, "a-service->") {
		t.Fatalf("external destinations = %v", identities(&match))
	}
}

// rvDigestWorld is the review facts of a workspace with one file, `auth/authorize.go`, in the working tree and another
// version of it at the revision `abc`.
func rvDigestWorld(t *testing.T) *conformance.ReviewFacts {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "auth"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "auth", "authorize.go"), []byte("package auth\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	git := &workspace.FakeGit{FilesAt: map[string]map[string]string{"abc": {"auth/authorize.go": "package auth // v1\n"}}}

	return (&rvWorld{root: root, git: git}).facts(t)
}

// rvRequireWorkingTreeDigests fails the test unless the empty revision reads the working tree, below the root only.
func rvRequireWorkingTreeDigests(t *testing.T, facts impact.FactSource) {
	t.Helper()

	sum, found, err := facts.FileDigest(t.Context(), "", "auth/authorize.go")
	if rvAnyOf(err != nil, !found, sum != digest.OfBytes([]byte("package auth\n"))) {
		t.Errorf("working tree: %q %v %v", sum, found, err)
	}

	if _, found, err = facts.FileDigest(t.Context(), "", "auth/missing.go"); rvAnyOf(err != nil, found) {
		t.Errorf("missing file: %v %v", found, err)
	}

	if _, _, err = facts.FileDigest(t.Context(), "", "../escape"); err == nil {
		t.Error("a path outside the root was read")
	}
}

// rvRequirePathItemSeals fails the test unless a path selector gives a file item whose digest covers the file.
func rvRequirePathItemSeals(t *testing.T, facts impact.FactSource) {
	t.Helper()

	match := items(t, facts, &impact.Selector{Kind: impact.KindImplementation, Match: map[string][]string{"path": {"auth/authorize.go"}}})
	if len(match.Items) != 1 || !slices.Equal(match.Items[0].ImplementationRefs, []string{"auth/authorize.go"}) {
		t.Fatalf("path items = %+v", match)
	}

	sealed, err := impact.V1().Seal(match.Items[0], impact.FileDigesterAt(t.Context(), facts, ""))
	if rvAnyOf(err != nil, sealed.Digest == "", sealed.Unknown != nil) {
		t.Errorf("sealed = %+v, %v", sealed, err)
	}
}

// Scenario: SEC-03 authorization code change without topology change.
func TestReviewFactsFileDigest(t *testing.T) {
	t.Parallel()

	facts := rvDigestWorld(t)

	rvRequireWorkingTreeDigests(t, facts)

	sum, found, err := facts.FileDigest(t.Context(), "abc", "auth/authorize.go")
	if rvAnyOf(err != nil, !found, sum != digest.OfBytes([]byte("package auth // v1\n"))) {
		t.Errorf("revision: %q %v %v", sum, found, err)
	}

	rvRequirePathItemSeals(t, facts)
}
