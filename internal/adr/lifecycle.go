package adr

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
)

// allowedActive are the statuses that a lifecycle profile may list as active (design D4).
var allowedActive = []string{StatusAccepted, StatusImplemented}

// pendingStatuses are the statuses of a successor that does not supersede yet.
var pendingStatuses = []string{StatusProposed, StatusDraft}

// knownStatuses are the CBM-normalized statuses that a `statusMap` may map to.
var knownStatuses = []string{
	StatusAccepted, StatusImplemented, StatusProposed, StatusDraft, StatusRejected,
	StatusDeprecated, StatusSuperseded, StatusAbandoned, StatusUnknown,
}

// Lifecycle maps the status of a decision to a lifecycle class. The original status and status text are never
// changed; Classify gives the mapped status separately.
type Lifecycle struct {
	// statusMap is keyed by the lower-cased status text.
	statusMap map[string]string
	active    []string
}

// NewLifecycle checks a lifecycle profile: `active` may list only `accepted` and `implemented` (default:
// `accepted`), and `statusMap` maps an exact status text (case-insensitive) to a CBM status. A forbidden active
// status or a mapping to an unknown status gives a CO-ADR-005 error with exit code 2.
func NewLifecycle(active []string, statusMap map[string]string) (*Lifecycle, error) {
	if len(active) == 0 {
		active = []string{StatusAccepted}
	}

	for _, status := range active {
		if !slices.Contains(allowedActive, status) {
			return nil, diag.Fail(diag.ADR005, diag.Detail(fmt.Sprintf(
				"adr.lifecycle.active lists %q; only %s may be active", status, strings.Join(allowedActive, " and "))))
		}
	}

	mapped := make(map[string]string, len(statusMap))

	for text, status := range statusMap {
		if !slices.Contains(knownStatuses, status) {
			return nil, diag.Fail(diag.ADR005, diag.Detail(fmt.Sprintf(
				"adr.lifecycle.statusMap maps %q to unknown status %q", text, status)))
		}

		key := strings.ToLower(strings.TrimSpace(text))
		if prev, dup := mapped[key]; dup && prev != status {
			return nil, diag.Fail(diag.ADR005, diag.Detail(fmt.Sprintf(
				"adr.lifecycle.statusMap maps %q to both %q and %q", text, prev, status)))
		}

		mapped[key] = status
	}

	sorted := slices.Clone(active)
	slices.Sort(sorted)

	return &Lifecycle{statusMap: mapped, active: slices.Compact(sorted)}, nil
}

// Active gives the active statuses of the profile, sorted.
func (l *Lifecycle) Active() []string { return slices.Clone(l.active) }

// Classify gives the lifecycle class of a decision. statusMap applies first, on the exact status text
// (case-insensitive); mapped is the status it gave, "" when no mapping applied. An empty status is `unknown`.
func (l *Lifecycle) Classify(status, statusText string) (mapped string, class LifecycleClass) {
	effective := status
	if to, ok := l.statusMap[strings.ToLower(strings.TrimSpace(statusText))]; ok && statusText != "" {
		mapped, effective = to, to
	}

	switch {
	case slices.Contains(l.active, effective):
		return mapped, ClassActive
	case slices.Contains(pendingStatuses, effective):
		return mapped, ClassPending
	default:
		return mapped, ClassInactive
	}
}
