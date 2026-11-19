// Package runid makes run IDs.
package runid

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const (
	stampLayout = "20060102T150405Z"
	randomBytes = 4
)

// New gives "<yyyymmddThhmmssZ>-<8 hex>" for the time at.
func New(at time.Time) string {
	var b [randomBytes]byte

	// rand.Read never returns an error (Go 1.24 and later).
	_, _ = rand.Read(b[:])

	return at.UTC().Format(stampLayout) + "-" + hex.EncodeToString(b[:])
}
