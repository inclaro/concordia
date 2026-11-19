// Package exceptions handles the two exception registers of a workspace: the
// baseline file (JSON) and the waiver file (YAML), and the disposition of
// findings against them.
package exceptions

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
)

const (
	// waiversKey is the key of the waiver list in the register file.
	waiversKey = "waivers"
	// yamlIndent is the indent of the register file.
	yamlIndent = 2
	// maxMonth and maxDay are the limits that JavaScript Date.parse applies to an ISO date.
	maxMonth = 12
	maxDay   = 31
	// dateParts is the number of parts of a YYYY-MM-DD date.
	dateParts = 3
	// decimalBase is the base of the number at the end of a waiver ID.
	decimalBase = 10
	// uint64Bits is the bit size of the number at the end of a waiver ID.
	uint64Bits = 64
	// registerSkeleton is the content of a new waiver file.
	registerSkeleton = "apiVersion: concordia.io/v1alpha1\nkind: ExceptionRegister\nwaivers: []\n"
)

var (
	fingerprintPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	datePattern        = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	idNumberPattern    = regexp.MustCompile(`(\d+)$`)

	// requiredWaiverKeys is the list of text keys that each waiver must have. Each waiver also needs exactly one
	// of "finding" and "scope".
	requiredWaiverKeys = []string{idKey, ruleKey, ownerKey, reasonKey, expiresKey}

	// scopedWaiverKeys is the closed list of keys of a scoped waiver. A finding waiver ignores unknown keys, so that
	// the registers of earlier versions stay valid.
	scopedWaiverKeys = []string{
		idKey, ruleKey, scopeKey, ownerKey, reasonKey, expiresKey, issueKey, approverKey, evidenceKey,
	}
	// scopeKeys, approverKeys, and evidenceKeys are the closed key lists of the objects of a scoped waiver.
	scopeKeys    = []string{subjectKey, environmentKey, obligationKey}
	approverKeys = []string{"identity", "authority"}
	evidenceKeys = []string{"path", "sha256"}
)

const (
	// idKey is the key of the ID of a waiver.
	idKey = "id"
	// ownerKey and the following are the keys of the other parts of a waiver.
	ownerKey    = "owner"
	reasonKey   = "reason"
	issueKey    = "issue"
	findingKey  = "finding"
	ruleKey     = "rule"
	scopeKey    = "scope"
	approverKey = "approver"
	evidenceKey = "compensatingEvidence"
	// subjectKey, environmentKey, and obligationKey are the keys of a scope.
	subjectKey     = "subject"
	environmentKey = "environment"
	obligationKey  = "obligation"
	// wildcardChars are the characters that a scope value must not have.
	wildcardChars = "*?"
	// expiresKey is the key of the expiry date of a waiver.
	expiresKey = "expires"
	// tagStr is the YAML tag of text.
	tagStr = "!!str"
	// tagMap and tagSeq are the YAML tags of a mapping and a sequence.
	tagMap = "!!map"
	tagSeq = "!!seq"
	// dayLength is the length of a day for the expiry window.
	dayLength = 24 * time.Hour
)

// Waiver is one entry of the waiver file. It is either a finding waiver (Finding is set, no Scope) or a scoped
// waiver (Scope and Approver are set, Finding is empty).
type Waiver struct {
	ID string `json:"id" yaml:"id"`
	// Finding is the fingerprint of a finding waiver. Empty for a scoped waiver.
	Finding string `json:"finding,omitempty" yaml:"finding,omitempty"`
	Rule    string `json:"rule"              yaml:"rule"`
	Owner   string `json:"owner"             yaml:"owner"`
	Reason  string `json:"reason"            yaml:"reason"`
	// Expires is a date, YYYY-MM-DD.
	Expires string `json:"expires" yaml:"expires"`
	// Issue is the optional issue reference. The empty text means no issue.
	Issue string `json:"issue,omitempty" yaml:"issue,omitempty"`
	// Scope is set for a scoped waiver. It names at least one of subject, environment, and obligation.
	Scope *Scope `json:"scope,omitempty" yaml:"scope,omitempty"`
	// Approver is the approver of a scoped waiver. nil for a finding waiver.
	Approver *Approver `json:"approver,omitempty" yaml:"approver,omitempty"`
	// CompensatingEvidence lists the optional compensating evidence of a scoped waiver, in file order.
	CompensatingEvidence []Evidence `json:"compensatingEvidence,omitempty" yaml:"compensatingEvidence,omitempty"`
}

// Scope is the scope of a scoped waiver: the findings of the rule that it covers. A key that is empty is not part
// of the scope, so it does not narrow the match.
type Scope struct {
	Subject     string `json:"subject,omitempty"     yaml:"subject,omitempty"`
	Environment string `json:"environment,omitempty" yaml:"environment,omitempty"`
	// Obligation is a full obligation ID.
	Obligation string `json:"obligation,omitempty" yaml:"obligation,omitempty"`
}

// Approver names who accepted a scoped waiver and under which authority.
type Approver struct {
	Identity  string `json:"identity"  yaml:"identity"`
	Authority string `json:"authority" yaml:"authority"`
}

// Evidence is one compensating evidence reference: a workspace file and its digest.
type Evidence struct {
	Path string `json:"path" yaml:"path"`
	// SHA256 is the digest of the file, "sha256:<64 hex>".
	SHA256 string `json:"sha256" yaml:"sha256"`
}

// IsScoped reports whether the waiver is a scoped waiver.
func (w *Waiver) IsScoped() bool { return w.Scope != nil }

// CanonicalJSON gives the bytes that approver verification compares and that a signed waiver is signed over: the
// canonical JSON of the whole entry. Empty optional keys are left out.
func (w *Waiver) CanonicalJSON() ([]byte, error) {
	out, err := canonjson.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("encode waiver %s: %w", w.ID, err)
	}

	return out, nil
}

// Digest gives the digest of CanonicalJSON: "sha256:<64 hex>".
func (w *Waiver) Digest() (string, error) {
	data, err := w.CanonicalJSON()
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(data)

	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ParseOption changes how ParseWaivers and ReadWaivers validate a register.
type ParseOption func(*parseConfig)

type parseConfig struct {
	now           time.Time
	maxExpiryDays int
}

// WithMaxExpiry makes a scoped waiver invalid when its expiry day is more than days days after the day of now
// (`exceptions.maxExpiryDays`). A days value of 0 or less sets no limit. A finding waiver is never limited.
func WithMaxExpiry(days int, now time.Time) ParseOption {
	return func(c *parseConfig) { c.maxExpiryDays, c.now = days, now }
}

// ReadWaivers reads and validates a waiver file (an OS path).
func ReadWaivers(path string, opts ...ParseOption) ([]Waiver, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller chooses the waiver file: reading it is the function.
	if err != nil {
		return nil, waiverProblem(path, "cannot read: "+err.Error(), "", 0)
	}

	return ParseWaivers(string(data), path, opts...)
}

// waiverEnd is the end of the expiry day: expires + 1 day at 00:00Z.
func waiverEnd(w *Waiver) (time.Time, bool) {
	day, ok := ParseDay(w.Expires)
	if !ok {
		return time.Time{}, false
	}

	return day.AddDate(0, 0, 1), true
}

// IsWaiverActive reports whether a waiver is active at a time: now < expires + 1 day at 00:00Z.
// A waiver with a date that is not valid is never active.
func IsWaiverActive(w *Waiver, at time.Time) bool {
	end, ok := waiverEnd(w)

	return ok && at.Before(end)
}

// NextWaiverID gives the ID for a new waiver: "WV-" and the highest number at
// the end of an existing ID, plus 1.
func NextWaiverID(waivers []Waiver) string {
	var highest uint64

	for i := range waivers {
		m := idNumberPattern.FindString(waivers[i].ID)
		if m == "" {
			continue
		}

		if n, err := strconv.ParseUint(m, decimalBase, uint64Bits); err == nil {
			highest = max(highest, n)
		}
	}

	return "WV-" + strconv.FormatUint(highest+1, decimalBase)
}

// Diagnostics gives the waiver diagnostics at a time: CO-EXC-006 for an expired
// waiver, CO-EXC-007 for a waiver that expires within expiringWithinDays days.
// A nil expiringWithinDays turns off the expiry warning.
func Diagnostics(list []Waiver, now time.Time, uri string, expiringWithinDays *float64) []diag.Diagnostic {
	out := make([]diag.Diagnostic, 0)
	src := diag.WithSource(diag.Source{URI: uri})

	for i := range list {
		waiver := &list[i]
		params := diag.Params{idKey: waiver.ID, expiresKey: waiver.Expires}

		end, valid := waiverEnd(waiver)

		switch {
		case !valid || !now.Before(end):
			out = append(out, diag.New(diag.EXC006, params, src))
		case expiringWithinDays != nil && float64(end.Sub(now))/float64(dayLength) <= *expiringWithinDays:
			out = append(out, diag.New(diag.EXC007, params, src))
		}
	}

	return out
}
