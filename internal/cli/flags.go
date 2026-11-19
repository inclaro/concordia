package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// choiceValue is a string flag that accepts only some values. It satisfies
// pflag.Value, so a bad value is a flag error (CO-WRK-010, exit code 2).
type choiceValue struct {
	target  *string
	allowed []string
}

func (v *choiceValue) String() string {
	if v.target == nil {
		return ""
	}

	return *v.target
}

func (v *choiceValue) Set(value string) error {
	if !slices.Contains(v.allowed, value) {
		return fmt.Errorf("allowed choices are %s", strings.Join(v.allowed, ", "))
	}

	*v.target = value

	return nil
}

func (v *choiceValue) Type() string { return strings.Join(v.allowed, "|") }

// addChoiceFlag adds a string flag that accepts only the given choices to the
// local flags of cmd (or to its persistent flags when persistent is true).
func addChoiceFlag(cmd *cobra.Command, persistent bool, target *string, name, usage string, choices ...string) {
	flags := cmd.Flags()
	if persistent {
		flags = cmd.PersistentFlags()
	}

	flags.Var(&choiceValue{target: target, allowed: choices}, name, usage)
}
