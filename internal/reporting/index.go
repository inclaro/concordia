package reporting

import (
	"strings"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// Report format names.
const (
	FormatPretty = "pretty"
	FormatJSON   = "json"
	FormatJUnit  = "junit"
	FormatSARIF  = "sarif"
)

// Formats lists the report formats.
var Formats = []string{FormatPretty, FormatJSON, FormatJUnit, FormatSARIF}

// IsReportFormat says whether s names a report format.
func IsReportFormat(s string) bool {
	for _, f := range Formats {
		if f == s {
			return true
		}
	}

	return false
}

// Render renders a run in one format. An unsupported format gives CO-WRK-010.
// The SARIF `tool.driver.version` is run.Toolchain.Concordia (the binary version).
func Render(format string, run *conformance.Run, opts PrettyOptions) ([]byte, error) {
	switch format {
	case FormatJSON:
		return conformance.CanonicalJSON(run)
	case FormatJUnit:
		return []byte(RenderJUnit(run)), nil
	case FormatSARIF:
		return RenderSARIF(run)
	case FormatPretty:
		return []byte(RenderPretty(run, opts)), nil
	default:
		return nil, diag.Fail(diag.WRK010, diag.Detail("unsupported format '"+format+"' ("+strings.Join(Formats, ", ")+")"))
	}
}

// Renderer adapts Render to the `conformance.Deps.RenderReport` signature.
func Renderer(opts PrettyOptions) func(workspace.ReportFormat, *conformance.Run) ([]byte, error) {
	return func(format workspace.ReportFormat, run *conformance.Run) ([]byte, error) {
		return Render(string(format), run, opts)
	}
}
