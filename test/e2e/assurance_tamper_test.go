package e2e_test

import "testing"

// A38 (R3): evidence that was changed after signing, or that is not JSON, gives one warning for each bad file and no
// pass that depends on it, while independent claims continue and a good statement beside the bad ones is still used.
func TestAssuranceA38TamperedStatements(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	t.Run("no good statement", func(t *testing.T) {
		t.Parallel()

		asrTamperRequireNoGoodStatement(t)
	})

	t.Run("bad files among a valid one", func(t *testing.T) {
		t.Parallel()

		asrTamperRequireBadAmongValid(t)
	})
}

// A38 (R3): a Secret in a rendered manifest never shows its value in any output, while its name and key names stay.
func TestAssuranceA38SecretInRenderedManifest(t *testing.T) {
	requireE2E(t)
	t.Parallel()

	asrTamperRequireSecretHidden(t)
}
