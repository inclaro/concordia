package policy_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// TestProviderCapsCarryProfileFamilies checks that ProviderCaps takes the
// capability profile and the family support of the provider (CO-OBS-017).
func TestProviderCapsCarryProfileFamilies(t *testing.T) {
	provider := observation.NewCbmProvider(observation.CbmProviderOptions{
		Client: observation.NewFakeClient(nil, "0.12.1"), ExecutableVersion: "0.12.1",
	})
	caps := provider.Capabilities(t.Context())

	pc := policy.ProviderCaps{SupportedCanonicalKinds: caps.SupportedCanonicalKinds, Profile: caps.Profile, Families: caps.Families}

	if pc.Profile == nil || pc.Profile.ID != "cbm-0.12.1" {
		t.Fatalf("profile = %+v", pc.Profile)
	}

	if got := pc.Families["routes-to"]; got.Level != observation.SupportPartial || got.Maturity != observation.MaturityAdvisory {
		t.Fatalf("routes-to = %+v", got)
	}

	if got := pc.Families["configures-key-symbol"]; got.Level != observation.SupportUnsupported {
		t.Fatalf("configures-key-symbol = %+v", got)
	}
}
