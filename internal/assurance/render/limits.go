package render

import (
	"time"

	"github.com/inclaro/concordia/internal/workspace"
)

// Default rendering limits (design D12).
const (
	DefaultTimeout        = 120 * time.Second
	DefaultMaxOutputBytes = 64 << 20
	DefaultMaxResources   = 20000
)

// Limits bound one render: the time of the renderer, the bytes of rendered output, and the number of resources.
// A render that goes above a limit fails; its output is never cut to fit.
type Limits struct {
	Timeout        time.Duration
	MaxOutputBytes int64
	MaxResources   int
}

// LimitsFrom turns the workspace limits into Limits and fills the defaults for the values that are not set.
func LimitsFrom(w workspace.RenderingLimits) Limits {
	limits := Limits{
		Timeout:        time.Duration(w.TimeoutSeconds) * time.Second,
		MaxOutputBytes: w.MaxOutputBytes,
		MaxResources:   w.MaxResources,
	}

	if limits.Timeout <= 0 {
		limits.Timeout = DefaultTimeout
	}

	if limits.MaxOutputBytes <= 0 {
		limits.MaxOutputBytes = DefaultMaxOutputBytes
	}

	if limits.MaxResources <= 0 {
		limits.MaxResources = DefaultMaxResources
	}

	return limits
}
