package cli

import "github.com/spf13/cobra"

// commandConstructors lists the constructors of the top-level commands, one
// line for each command. A new command adds its line here (the order is the
// order of `src/cli/commands/index-registry.ts`: model, index, bind, baseline,
// waiver, init, doctor, clean, render, check, explain) and its own file
// `<command>.go`.
//
// A constructor gets the Host, builds the cobra command (and its subcommands)
// and sets `RunE: host.Action(handler)`.
func commandConstructors() []func(host *Host) *cobra.Command {
	return []func(host *Host) *cobra.Command{
		newModelCmd,
		newIndexCmd,
		newBindCmd,
		newBaselineCmd,
		newWaiverCmd,
		newInitCmd,
		newDoctorCmd,
		newCleanCmd,
		newRenderCmd,
		newCheckCmd,
		newExplainCmd,
		newTraceCmd,
		newPolicyCmd,
		newLockCmd,
		newExportCmd,
		newDiffCmd,
		newWatchCmd,
		newAdrCmd,
		newAssessmentCmd,
		newAssuranceCmd,
		newCbmCmd,
	}
}
