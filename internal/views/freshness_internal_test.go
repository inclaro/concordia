package views

import (
	"errors"
	"fmt"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/projection"
)

func TestClassifyExportFailure(t *testing.T) {
	t.Parallel()

	include := &projection.IncludeUnavailableError{Path: "docs/shared.dsl", Err: errors.New("no such file")}

	for _, tc := range []struct {
		err    error
		name   string
		reason string
		status Status
	}{
		{err: fmt.Errorf("export: %w", include), name: "include", reason: ReasonIncludeUnavailable, status: StatusUnknown},
		{err: diag.Fail(diag.PRJ001, diag.Params{"format": "export"}), name: "no CLI", reason: ReasonGeneratorUnavailable, status: StatusUnknown},
		{err: diag.Fail(diag.DGM004, diag.Detail("found 1.0, pinned 2025.11.09")), name: "other version", reason: ReasonGeneratorUnavailable, status: StatusUnknown},
		{err: diag.Fail(diag.PRJ002, diag.Detail("exit code 1")), name: "CLI failed", reason: ReasonGeneratorFailed, status: StatusError},
		{err: diag.Fail(diag.DGM003, diag.Detail("!include")), name: "directive", reason: ReasonGeneratorFailed, status: StatusError},
		{err: errors.New("boom"), name: "other", reason: ReasonGeneratorFailed, status: StatusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reason, detail, status := classifyExportFailure(tc.err)
			if reason != tc.reason || status != tc.status || detail == "" {
				t.Errorf("classifyExportFailure = %q, %q, %s", reason, detail, status)
			}
		})
	}
}
