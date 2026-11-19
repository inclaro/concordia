// Package views holds the architecture view contract: the definitions that the workspace
// declares, their validation, and the checks that decide whether each view exists, is
// fresh, and corresponds to the architecture and the implementation.
package views

import (
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// The definition types are the workspace types: the workspace loader fills them and
// validates them, and internal/workspace cannot import this package (this package
// imports internal/intent, which imports internal/workspace).
type (
	// Definition is one declared view (an entry of `views`).
	Definition = workspace.View
	// Kind is the kind of a view.
	Kind = workspace.ViewKind
	// Origin says who writes the diagram of a view.
	Origin = workspace.ViewOrigin
	// Abstraction is the abstraction level of a view.
	Abstraction = workspace.ViewAbstraction
	// Coverage lists the CALM IDs that a view must show.
	Coverage = workspace.ViewCoverage
	// Source is the authored diagram of a view.
	Source = workspace.ViewSource
	// Selector picks one diagram block by title or anchor.
	Selector = workspace.ViewSelector
	// Output is the committed output of a generated view.
	Output = workspace.ViewOutput
	// Export is one export of a generated view.
	Export = workspace.ViewExport
	// ProjectionRules are the projection rules of a view.
	ProjectionRules = workspace.ViewProjection
	// Mapping maps diagram elements to CALM IDs.
	Mapping = workspace.ViewMapping
	// Depicts is the supplemental mapping of a raster view.
	Depicts = workspace.ViewDepicts
	// Comparison says what the correspondence check compares.
	Comparison = workspace.ViewComparison
)

// View kinds, origins and abstraction levels.
const (
	KindSystemContext = workspace.ViewSystemContext
	KindContainer     = workspace.ViewContainer
	KindComponent     = workspace.ViewComponent
	KindDeployment    = workspace.ViewDeployment
	KindTrust         = workspace.ViewTrust
	KindDataFlow      = workspace.ViewDataFlow

	OriginGenerated = workspace.OriginGenerated
	OriginAuthored  = workspace.OriginAuthored

	AbstractionSystem     = workspace.AbstractionSystem
	AbstractionContainer  = workspace.AbstractionContainer
	AbstractionComponent  = workspace.AbstractionComponent
	AbstractionDeployment = workspace.AbstractionDeployment
)

// ValidateDefinitions checks the definitions that need no other input. Each problem is a
// CO-DGM-001 diagnostic (exit code 2) that names the view; uri is the workspace file.
// `workspace.Load` runs it, so a loaded workspace has valid definitions.
func ValidateDefinitions(defs []Definition, uri string) []diag.Diagnostic {
	return workspace.ValidateViews(defs, uri)
}
