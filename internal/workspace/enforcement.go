package workspace

import (
	"fmt"
	"time"

	"github.com/inclaro/concordia/internal/diag"
)

// Enforcement modes that `check.enforcement` can set.
const (
	ModeMandatory = "mandatory"
	ModeAdvisory  = "advisory"
	ModeShadow    = "shadow"
)

// DateLayout is the layout of a date in the workspace file (`check.rollback.expires`).
const DateLayout = "2006-01-02"

// EnforcementEntry is one entry of `check.enforcement`. It selects rules by family, pack, or rule IDs, and
// optionally by environment, and sets the mode. The selectors that an entry gives must all match; the first
// entry that matches a rule and an environment wins.
type EnforcementEntry struct {
	// Family is the `family` of the rule.
	Family string `json:"family,omitempty"`
	// Pack is the ID of the policy pack of the rule.
	Pack string `json:"pack,omitempty"`
	// Environment is a declared environment ID. Empty: every environment.
	Environment string `json:"environment,omitempty"`
	// Mode is `mandatory`, `advisory`, or `shadow`.
	Mode string `json:"mode"`
	// Rules are rule IDs.
	Rules []string `json:"rules,omitempty"`
}

// Rollback is `check.rollback`: while it is active, the rules that use typed obligations run as `shadow`.
type Rollback struct {
	Reason       string `json:"reason"`
	AuthorizedBy string `json:"authorizedBy"`
	// Expires is the last day of the rollback, YYYY-MM-DD (UTC).
	Expires string `json:"expires"`
}

// ExpiryEnd gives the end of the rollback: the start of the day after Expires, UTC. The rollback is active
// before that time.
func (r *Rollback) ExpiryEnd() (time.Time, error) {
	day, err := time.Parse(DateLayout, r.Expires)
	if err != nil {
		return time.Time{}, fmt.Errorf("check.rollback.expires: '%s' is not a date (YYYY-MM-DD): %w", r.Expires, err)
	}

	return day.AddDate(0, 0, 1), nil
}

// Active says whether the rollback is in force at the time.
func (r *Rollback) Active(at time.Time) bool {
	end, err := r.ExpiryEnd()

	return err == nil && at.Before(end)
}

// enforcementOf reads `check.enforcement`. It gives nil when the workspace sets none.
func enforcementOf(chk object) []EnforcementEntry {
	list, _ := chk["enforcement"].([]any)
	if len(list) == 0 {
		return nil
	}

	out := make([]EnforcementEntry, 0, len(list))

	for _, item := range list {
		e, _ := item.(object)
		out = append(out, EnforcementEntry{
			Family:      str(e, "family", ""),
			Pack:        str(e, "pack", ""),
			Rules:       strList(e, "rules", nil),
			Environment: str(e, "environment", ""),
			Mode:        str(e, "mode", ""),
		})
	}

	return out
}

// rollbackOf reads `check.rollback`. It gives nil when the workspace sets none.
func rollbackOf(chk object) *Rollback {
	r, ok := chk["rollback"].(object)
	if !ok {
		return nil
	}

	return &Rollback{Reason: str(r, "reason", ""), AuthorizedBy: str(r, "authorizedBy", ""), Expires: str(r, "expires", "")}
}

// checkRollback rejects an `expires` that is not a calendar date. The schema only checks the shape.
func checkRollback(ws *Workspace, path string) *diag.Error {
	if ws.Check.Rollback == nil {
		return nil
	}

	if _, err := ws.Check.Rollback.ExpiryEnd(); err != nil {
		return invalid(err.Error(), path, nil, 0)
	}

	return nil
}
