package reporting

import (
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
)

// A review finding names the required assessment and the assessment, and a quality finding names the scope of its
// scenario. Neither is shown as a pair of CALM nodes: they are not CALM nodes, and a quality scenario has none.
func TestEndpointsOfReviewAndQualityFindings(t *testing.T) {
	t.Parallel()

	review := endpointsText(&conformance.RuleResult{RuleID: "REV-APR-001"}, &conformance.Finding{
		RuleID: "REV-APR-001", Area: conformance.AreaAssessmentCurrency,
		Source: conformance.Endpoint{ArchitectureID: "billing-security"}, Target: conformance.Endpoint{ArchitectureID: "ASM-BILLING"},
	})
	if review != "required billing-security, assessment ASM-BILLING" {
		t.Errorf("review endpoints = %q", review)
	}

	quality := endpointsText(&conformance.RuleResult{RuleID: "quality:QS-1", Subjects: []string{"billing-service", "fulfillment-service"}},
		&conformance.Finding{RuleID: "quality:QS-1", Area: conformance.AreaQualityEvaluation})
	if quality != "scope billing-service, fulfillment-service" {
		t.Errorf("quality endpoints = %q", quality)
	}

	code := endpointsText(&conformance.RuleResult{RuleID: "CO-BND-001"}, &conformance.Finding{
		Source: conformance.Endpoint{ArchitectureID: "billing-service"}, Target: conformance.Endpoint{ArchitectureID: "fulfillment-service"},
	})
	if code != "billing-service -> fulfillment-service" {
		t.Errorf("code endpoints = %q", code)
	}
}
