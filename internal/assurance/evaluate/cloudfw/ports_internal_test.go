package cloudfw

import "testing"

// TestPortRangeOutsideTheValidPorts: a port range beyond 65535 does not parse.
func TestPortRangeOutsideTheValidPorts(t *testing.T) {
	t.Parallel()

	for spec, want := range map[string]tri{"1-65536": triMaybe, "0-65535": triYes, "5432": triYes, "*": triYes} {
		if got := matchPortSpec(spec, postgres, &side{}); got != want {
			t.Errorf("matchPortSpec(%q) = %v, want %v", spec, got, want)
		}
	}
}
