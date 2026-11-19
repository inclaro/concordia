package diag_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
)

func TestDiagnosticThatQuotesSourceTextShowsTheMarkerInsteadOfTheSecret(t *testing.T) {
	t.Parallel()

	const token = "ghp_CONCORDIASEEDshelltoken0000000000000"

	d := diag.New(diag.WRK010, diag.Detail("line 3: export GITHUB_TOKEN="+token))

	text := diag.FormatDiagnostic(d)
	if strings.Contains(text, token) || !strings.Contains(text, "[REDACTED:token]") {
		t.Errorf("diagnostic = %q", text)
	}

	err := diag.Fail(diag.WRK010, diag.Detail("Authorization: Bearer "+token))
	if strings.Contains(err.Error(), token) {
		t.Errorf("error = %q", err.Error())
	}
}
