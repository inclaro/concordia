package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/workspace"
)

// The date layout of `--expires`.
const expiresDateLayout = "2006-01-02"

// authorityFor gives the authority of an approval: the flag, else the authority of the required assessment that
// names the assessment and its environment.
func authorityFor(wsp *workspace.Workspace, doc *assessment.Assessment, flag string) string {
	if flag != "" {
		return flag
	}

	if wsp.Assessments == nil {
		return ""
	}

	for i := range wsp.Assessments.Required {
		r := &wsp.Assessments.Required[i]
		if requiredNamesAssessment(r, doc) {
			return r.Authority
		}
	}

	return ""
}

// requiredNamesAssessment reports whether a required assessment names the assessment and its environment.
func requiredNamesAssessment(r *workspace.RequiredAssessment, doc *assessment.Assessment) bool {
	if r.Assessment != doc.ID {
		return false
	}

	return r.Environment == "" || r.Environment == doc.Environment
}

// mechanismHint says what the reviewer does next, by the mechanism of the authority in the working tree.
func mechanismHint(wsp *workspace.Workspace, authority string) string {
	if hasSignedAuthority(wsp, authority) {
		return "Commit the record and its signature bundle together. The signature, not the location, proves the authority.\n"
	}

	return "Concordia counts this record only when it is on the trust anchor: merge it into the anchor branch " +
		"(a change that touches only this file), then rebuild the candidate on the anchor.\n"
}

// hasSignedAuthority reports whether the working tree names the authority with the signed mechanism.
func hasSignedAuthority(wsp *workspace.Workspace, authority string) bool {
	if wsp.Assessments == nil {
		return false
	}

	return slices.ContainsFunc(wsp.Assessments.Authorities, func(a workspace.Authority) bool {
		return a.ID == authority && a.Mechanism == workspace.MechanismSigned
	})
}

// parseExpiry reads --expires and --condition. A date is the last day of the approval: the approval expires at
// the start of the next day (UTC). The time must be after now.
func parseExpiry(expires string, conditions []string, now time.Time) (*assessment.Expiry, error) {
	if expires == "" && len(conditions) == 0 {
		return nil, nil //nolint:nilnil // no expiry is a valid answer.
	}

	out := &assessment.Expiry{Conditions: slices.Clone(conditions)}

	if expires == "" {
		return out, nil
	}

	at, err := parseExpiresAt(expires)
	if err != nil {
		return nil, err
	}

	if !at.After(now) {
		return nil, refuse("--expires '" + expires + "' is not in the future")
	}

	out.ExpiresAt = at

	return out, nil
}

// parseExpiresAt reads a date (YYYY-MM-DD, the start of the next day) or an RFC 3339 time.
func parseExpiresAt(expires string) (time.Time, error) {
	if day, err := time.Parse(expiresDateLayout, expires); err == nil {
		return day.AddDate(0, 0, 1), nil
	}

	if at, err := time.Parse(time.RFC3339, expires); err == nil {
		return at.UTC(), nil
	}

	return time.Time{}, refuse("--expires '" + expires + "' is not a date (YYYY-MM-DD) or an RFC 3339 time")
}

// subjectSummary is the text that the reviewer examines: the assessment, its dependency items, the bound
// assumptions and evidence, and the digest to confirm.
func subjectSummary(doc *assessment.Assessment, b *assessment.Binding) string { //nolint:varnamelen // b is the one binding that the function prints.
	var out strings.Builder

	fmt.Fprintf(&out, "Assessment %s (%s, environment %s, outcome %s)\n", doc.Key(), doc.Type, doc.Environment, doc.Outcome)

	if doc.Title != "" {
		fmt.Fprintf(&out, "Title: %s\n", doc.Title)
	}

	fmt.Fprintf(&out, "Version digest: %s\n", doc.Digest)
	fmt.Fprintf(&out, "Digest contract: %s, capability profile: %s\n", b.ContractVersion, b.CapabilityProfile)

	fmt.Fprintf(&out, "Dependencies (%d):\n", len(b.Subject.Dependencies))

	for _, it := range b.Subject.Dependencies {
		fmt.Fprintf(&out, "  %s %s %s\n", it.Kind, it.Identity, it.Digest)
	}

	fmt.Fprintf(&out, "Assumptions: %d, evidence: %d\n", len(b.Assumptions), len(b.Evidence))

	if b.Delta != nil {
		fmt.Fprintf(&out, "Change since the previous approval: %d added, %d removed, %d modified\n",
			len(b.Delta.Added), len(b.Delta.Removed), len(b.Delta.Modified))
	}

	fmt.Fprintf(&out, "Subject digest: %s\n", b.Subject.Digest)

	return out.String()
}

// unknownSummary lists the causes that stop a subject from being computed.
func unknownSummary(causes []assessment.UnknownCause) string {
	parts := make([]string, 0, len(causes))
	for _, c := range causes {
		parts = append(parts, c.Reason+": "+c.Detail)
	}

	return strings.Join(parts, "; ")
}
