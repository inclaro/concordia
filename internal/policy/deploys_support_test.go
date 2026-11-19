package policy_test

import (
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

const deploysRule = "    subject: {calmNodes: [billing]}\n    forbid: {target: {calmNodes: [fulfillment]}, edges: [deploys]}\n"

func compileDeploys(t *testing.T, provider *policy.ProviderCaps) policy.CompileResult {
	t.Helper()

	res, err := policy.CompilePolicies([]policy.PolicyFile{pack(rule(deploysRule))}, model(), *provider, nil)
	if err != nil {
		t.Fatalf("CompilePolicies: %v", err)
	}

	return res
}

// EVD-07: the `deploys` token is `deploys-artifact`, and a profile that supports the
// family gives the rule evidence, so CO-POL-009 is not raised for it.
func TestDeploysTokenIsSupportedWhenTheProfileSupportsDeploysArtifact(t *testing.T) {
	t.Parallel()

	provider := observation.NewCbmProvider(observation.CbmProviderOptions{
		Client: observation.NewFakeClient(nil, "0.12.1"), ExecutableVersion: "0.12.1",
	}).Capabilities(t.Context())

	res := compileDeploys(t, &policy.ProviderCaps{
		SupportedCanonicalKinds: provider.SupportedCanonicalKinds, Profile: provider.Profile, Families: provider.Families,
	})

	if state := provider.Families["deploys-artifact"]; state.Level == observation.SupportUnsupported {
		t.Fatalf("profile family deploys-artifact = %+v", state)
	}

	wantCodes(t, &res)

	plan := res.Plans[0]
	if !slices.Equal(plan.Traversal.EdgeKinds, kinds("deploys-artifact")) || len(plan.UnsupportedEdgeKinds) != 0 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestDeploysTokenGivesPOL009WhenTheProfileMarksTheFamilyUnsupported(t *testing.T) {
	t.Parallel()

	res := compileDeploys(t, &policy.ProviderCaps{
		SupportedCanonicalKinds: []observation.CanonicalEdgeKind{"deploys-artifact", "imports"},
		Families: map[string]observation.FamilyState{
			"deploys-artifact": {Level: observation.SupportUnsupported, Maturity: observation.MaturityAdvisory},
		},
	})

	wantCodes(t, &res, diag.POL009)

	if got := res.Plans[0].UnsupportedEdgeKinds; !slices.Equal(got, kinds("deploys-artifact")) {
		t.Errorf("unsupported = %v", got)
	}
}

func TestDeploysTokenGivesPOL009WhenTheAdapterHasNoSuchKind(t *testing.T) {
	t.Parallel()

	res := compileDeploys(t, &policy.ProviderCaps{SupportedCanonicalKinds: []observation.CanonicalEdgeKind{"imports"}})

	wantCodes(t, &res, diag.POL009)
}
