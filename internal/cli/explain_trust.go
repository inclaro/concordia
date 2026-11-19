package cli

import (
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/trust"
)

// crossingsBetween gives the crossings of the run whose flow goes from the CALM node source to the CALM node
// destination, in the order of the results. nil when the run has no trust section or no such flow.
func crossingsBetween(run *conformance.Run, source, destination string) []trust.Crossing {
	if run.Trust == nil || source == "" || destination == "" {
		return nil
	}

	var out []trust.Crossing

	for i := range run.Trust.Crossings {
		if run.Trust.Crossings[i].Source == source && run.Trust.Crossings[i].Destination == destination {
			out = append(out, run.Trust.Crossings[i])
		}
	}

	return out
}

// findingCrossings gives the crossings of the flow that a finding is about: from its source to its target.
func findingCrossings(run *conformance.Run, f *conformance.Finding) []trust.Crossing {
	return crossingsBetween(run, f.Source.ArchitectureID, f.Target.ArchitectureID)
}

// crossingLines shows crossings for `explain`: one header for each flow with its data classification, then one
// line for each dimension. The classification `unknown` is shown as is; it is never a default.
func crossingLines(crossings []trust.Crossing) []string {
	var lines []string

	last := ""

	for i := range crossings {
		crossing := &crossings[i]
		if crossing.Flow != last {
			last = crossing.Flow

			header := "  Trust crossings: " + crossing.Flow + " (" + string(crossing.Kind) + "), data classification " + crossing.Classification
			if crossing.ExternalDestination {
				header += ", external destination with unknown trust"
			}

			lines = append(lines, header)
		}

		lines = append(lines, "    "+crossing.Dimension+": "+crossing.Direction+" ("+string(crossing.State)+")")
	}

	return lines
}

// trustJSON gives the `trust` object of the JSON explanation: the crossings of the selected findings, once each,
// in the order of the results. nil (left out) when no selected finding has a crossing.
func trustJSON(run *conformance.Run, selected []*conformance.Finding) *conformance.TrustResult {
	seen := map[string]struct{}{}
	crossings := []trust.Crossing{}

	for _, f := range selected {
		found := findingCrossings(run, f)
		for i := range found {
			key := found[i].Flow + "\x00" + found[i].Dimension
			if _, dup := seen[key]; dup {
				continue
			}

			seen[key] = struct{}{}

			crossings = append(crossings, found[i])
		}
	}

	if len(crossings) == 0 {
		return nil
	}

	trust.Sort(crossings)

	return &conformance.TrustResult{Crossings: crossings}
}
