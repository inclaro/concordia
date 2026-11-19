package logger_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/logger"
)

const (
	seedToken = "ghp_CONCORDIASEEDshelltoken0000000000000"
	seedPEM   = "CONCORDIASEEDPEM"
)

func TestLogLinesAtEveryLevelHaveNoSecretValue(t *testing.T) {
	t.Parallel()

	log, buf := newLogger(logger.Options{Verbose: true, Trace: true})

	log.Error("failed with " + seedToken)
	log.Warn("warn " + seedToken)
	log.Info("info postgres://user:CONCORDIA-SEED-url-1a2b@host/db")
	log.Debug("debug Authorization: Bearer " + seedToken)
	log.Info("-----BEGIN PRIVATE KEY-----\n" + seedPEM + "\n-----END PRIVATE KEY-----")
	log.Diagnostic(diag.New(diag.WRK010, diag.Detail("line: export GITHUB_TOKEN="+seedToken)))
	log.Trace(logger.TraceEvent{Phase: phaseCheck, Event: "query " + seedToken})

	out := buf.String()
	for _, secret := range []string{seedToken, "CONCORDIA-SEED-url-1a2b", seedPEM} {
		if strings.Contains(out, secret) {
			t.Errorf("the log has %q:\n%s", secret, out)
		}
	}

	if !strings.Contains(out, "[REDACTED:token]") || !strings.Contains(out, "[REDACTED:private-key]") {
		t.Errorf("the log has no marker:\n%s", out)
	}
}
