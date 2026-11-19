package adr

// EvaluateApplicability gives the applicability of a registered decision: state, evidence reason, and whether
// its status lags the history. The facts of uncommitted text are unknown (ADR-01, ADR-05): a presence state other
// than `committed` gives `unknown`, with the presence reason when the presence is `unverifiable` (no revision,
// no capability, incomplete extraction, duplicate ID) and `not-committed` otherwise. A committed decision takes
// the result of the history. The entry and the lifecycle are accepted for the contract of the caller; the
// history already holds the lifecycle classes. Expect handling belongs to the replacement review.
func EvaluateApplicability(
	_ *Entry, presence PresenceResult, h *History, _ *Lifecycle, //nolint:gocritic // fixed contract: by value
) (state ApplicabilityState, reason string, statusLags bool) {
	if presence.State == PresenceCommitted {
		return h.Applicability(presence.Path)
	}

	if presence.State == PresenceUnverifiable {
		return ApplicabilityUnknown, presence.Reason, false
	}

	return ApplicabilityUnknown, EvidenceNotCommitted, false
}
