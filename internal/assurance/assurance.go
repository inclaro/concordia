// Package assurance holds the vocabulary that the R3 evidence adapters and evaluators share: integrity levels,
// admissibility details and results, the obligation context, and coverage states. The adapters live in the
// subpackages (render, plan, statement, admit, provenance, evaluate/netpol, evaluate/rbac, checkresult, quality,
// claims). This package imports none of them, and none of them imports internal/conformance.
package assurance

import (
	"slices"
	"strings"
	"time"
)

// Integrity is how far the content and the origin of a piece of evidence are verified. The levels are ordered.
type Integrity string

// Integrity levels, lowest first.
const (
	// IntegrityNotVerified: neither content nor producer is verified (for example a plan without a statement).
	IntegrityNotVerified Integrity = "content-not-verified"
	// IntegrityDigest: the content matches a digest, but no producer signature is verified.
	IntegrityDigest Integrity = "content-verified-by-digest"
	// IntegrityAuthenticated: the signature of a declared producer is verified and the subject digest matches.
	IntegrityAuthenticated Integrity = "producer-authenticated"
)

// Rank orders the levels: 0, 1, 2; -1 for an unknown value.
func (i Integrity) Rank() int {
	switch i {
	case IntegrityNotVerified:
		return 0
	case IntegrityDigest:
		return 1
	case IntegrityAuthenticated:
		return 2
	}

	return -1
}

// Text is the level as the specs and the terminal output write it, for example "producer authenticated".
func (i Integrity) Text() string { return strings.ReplaceAll(string(i), "-", " ") }

// Detail is an admissibility detail (design D6). Details are not unknown reasons: they explain why a statement
// cannot support a claim, and the claim carries one unknown reason of the closed set.
type Detail string

// Admissibility details.
const (
	DetailProducerNotTrusted             Detail = "producer-not-trusted"
	DetailProducerNotTrustedForClaim     Detail = "producer-not-trusted-for-claim"
	DetailIntegrityFailure               Detail = "integrity-failure"
	DetailNotAuthenticated               Detail = "not-authenticated"
	DetailDifferentRevision              Detail = "different-revision"
	DetailDifferentEnvironment           Detail = "different-environment"
	DetailStale                          Detail = "stale"
	DetailFailedRun                      Detail = "failed-run"
	DetailInvalidOutcome                 Detail = "invalid-outcome"
	DetailThresholdMismatch              Detail = "threshold-mismatch"
	DetailUnitMismatch                   Detail = "unit-mismatch"
	DetailSubjectNotEvaluatedState       Detail = "subject-not-evaluated-state"
	DetailOfflineVerificationUnavailable Detail = "offline-verification-unavailable"
)

// MissingField is the detail `missing-field:<name>`.
func MissingField(name string) Detail { return Detail("missing-field:" + name) }

// Admission is the result of the admissibility engine for one statement and one claim.
type Admission struct {
	Integrity Integrity `json:"integrity"`
	// Details are every reason the statement is not admissible, sorted and without duplicates. Empty when admissible.
	Details    []Detail `json:"details"`
	Admissible bool     `json:"admissible"`
}

// NewAdmission builds an Admission from the collected details: admissible exactly when there is none.
func NewAdmission(integrity Integrity, details []Detail) Admission {
	sorted := slices.Clone(details)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	if sorted == nil {
		sorted = []Detail{}
	}

	return Admission{Integrity: integrity, Details: sorted, Admissible: len(sorted) == 0}
}

// Context is what an obligation is evaluated against: the environment, the evaluated revision, the evaluation time
// (taken once at run start and recorded in the manifest), and whether the working tree has uncommitted changes.
type Context struct {
	Now         time.Time
	Environment string
	Revision    string
	Offline     bool
	DirtyTree   bool
}

// Coverage is the coverage state of one adapter input (a render, a plan, an evidence glob).
type Coverage string

// Coverage states.
const (
	CoverageComplete Coverage = "complete"
	CoveragePartial  Coverage = "partial"
	CoverageFailed   Coverage = "failed"
)

// Gap is one recorded coverage gap of an adapter input: a stable kind (for example `remote-base`,
// `unparseable-document`, `merged-occurrences`) and a human detail.
type Gap struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	// Path is the workspace-relative file the gap is about, if any.
	Path string `json:"path,omitempty"`
}
