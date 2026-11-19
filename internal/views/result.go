package views

// Status is the verdict of one view check (spec view-currency: "Four checks give independent results").
// Each check of a view has its own status; a pass of one never changes another.
type Status string

// The verdicts of a view check.
const (
	StatusPass          Status = "pass"
	StatusViolation     Status = "violation"
	StatusUnknown       Status = "unknown"
	StatusNotApplicable Status = "not-applicable"
	StatusError         Status = "error"
)

// Location is a place in a repository file: the path is slash separated and relative to the workspace root; Line is
// 1-based and 0 when the place has no line.
type Location struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}
