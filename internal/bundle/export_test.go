package bundle

import "testing"

// SetMaxStreamSize lowers the decompressed stream limit for one test.
func SetMaxStreamSize(t *testing.T, n int64) {
	t.Helper()

	old := maxStreamSize
	maxStreamSize = n

	t.Cleanup(func() { maxStreamSize = old })
}
