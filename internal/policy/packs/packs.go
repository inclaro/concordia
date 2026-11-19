// Package packs holds the policy packs that ship inside the binary.
package packs

import (
	"embed"
	"path"
	"slices"
	"strings"
)

// Prefix is the source prefix of a built-in pack, for example "builtin:core".
const Prefix = "builtin:"

const suffix = ".policy.yaml"

//go:embed *.policy.yaml
var files embed.FS

// Names gives the names of the built-in packs in sorted order.
func Names() []string {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), suffix))
	}

	slices.Sort(names)

	return names
}

// Source gives the YAML text of the built-in pack with this name.
func Source(name string) ([]byte, bool) {
	data, err := files.ReadFile(path.Clean(name) + suffix)
	if err != nil {
		return nil, false
	}

	return data, true
}
