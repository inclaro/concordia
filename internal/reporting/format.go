// Package reporting renders a conformance run as pretty text, JSON, JUnit XML or SARIF.
// Every reporter is a pure function of the run.
package reporting

import (
	"fmt"
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/redact"
)

const (
	noncharFFFE   = 0xFFFE
	noncharFFFF   = 0xFFFF
	hexKeep       = 7
	hexHead       = 4
	hexTail       = 3
	percentFactor = 100
)

// Percent formats a ratio as `98.7%`.
func Percent(ratio float64) string {
	return fmt.Sprintf("%.1f%%", ratio*percentFactor)
}

// ShortFingerprint is `7c4d…91a`: the first 4 and last 3 characters of the hex digest.
func ShortFingerprint(fp string) string {
	hex := strings.TrimPrefix(fp, "sha256:")
	if len(hex) <= hexKeep {
		return hex
	}

	return hex[:hexHead] + "…" + hex[len(hex)-hexTail:]
}

// isShownUnknown says whether the run reports an unknown rule: not when its effective policy is `ignore`.
func isShownUnknown(rule *conformance.RuleResult) bool {
	return rule.Verdict == conformance.VerdictUnknown && rule.Plan.EvidenceRequirements.OnUnknown != policy.UnknownIgnore
}

// xmlEscape escapes text for an XML attribute or text node. Control characters, which XML 1.0
// forbids and a terminal would act on, are first replaced by a visible escape (redact.Sanitize).
func xmlEscape(s string) string {
	var out strings.Builder

	for _, char := range redact.Sanitize(s) {
		switch char {
		case noncharFFFE, noncharFFFF:
			continue
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '"':
			out.WriteString("&quot;")
		case '\'':
			out.WriteString("&apos;")
		default:
			out.WriteRune(char)
		}
	}

	return out.String()
}
