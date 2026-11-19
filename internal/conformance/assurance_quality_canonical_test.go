package conformance_test

import (
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/conformance"
)

// The quality links compare decision ids by the canonical form of internal/adr (design D26). internal/adr imports
// this package, so the pipeline carries its own copy of the function; this test keeps the two equal.
func TestTheQualityLinksUseTheCanonicalIDOfTheADRPackage(t *testing.T) {
	t.Parallel()

	for _, id := range []string{
		"ADR-7", "ADR-0007", "adr-7", " ADR-0007 ", "ADR-0", "ADR-000", "RFC-12", "rfc-0012", "ADR_7", "ADR-", "7", "",
		"ADR-99999999999999999999999999", "ADR-0099999999999999999999999999", "A1-007",
	} {
		if got, want := conformance.CanonicalDecisionID(id), adr.CanonicalID(id); got != want {
			t.Errorf("canonical(%q) = %q, adr.CanonicalID = %q", id, got, want)
		}
	}
}
