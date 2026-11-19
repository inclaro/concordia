// Package digest calculates SHA-256 digests.
package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// Prefix starts each digest text.
const Prefix = "sha256:"

// SHA256Hex gives the lower-case hex SHA-256 of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// SHA256HexString gives the lower-case hex SHA-256 of a string.
func SHA256HexString(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

// OfBytes gives "sha256:<hex>" for data.
func OfBytes(data []byte) string {
	return Prefix + SHA256Hex(data)
}

// OfFile gives the digest of a file as "sha256:<hex>". path is an OS path.
func OfFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller chooses the path: reading it is the function.
	if err != nil {
		return "", fmt.Errorf("digest %s: %w", path, err)
	}

	return OfBytes(data), nil
}
