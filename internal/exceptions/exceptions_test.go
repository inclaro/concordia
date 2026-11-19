package exceptions_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
)

const (
	ruleOne      = "R-1"
	ruleTwo      = "R-2"
	idOne        = "WV-1"
	codeExpired  = "CO-EXC-006"
	codeSoon     = "CO-EXC-007"
	registerFile = "waivers.yaml"
	notAMapping  = "- a\n"
)

func ptr[T any](v T) *T {
	return &v
}

func fp(n int) string {
	text := strings.Repeat("0", 64) + string(rune('0'+n))

	return "sha256:" + text[len(text)-64:]
}

func finding(n int) exceptions.Key {
	return exceptions.Key{Fingerprint: fp(n), RuleID: ruleOne}
}

func baselineOf(ns ...int) *exceptions.Baseline {
	b := &exceptions.Baseline{
		SchemaVersion: "1.0",
		CreatedFrom:   exceptions.CreatedFrom{GraphGeneration: "g", PolicyDigest: "d"},
	}

	for _, n := range ns {
		b.Findings = append(b.Findings, exceptions.BaselineEntry{
			Fingerprint: fp(n), RuleID: ruleOne, Owner: "o", Reason: "r", CreatedAt: "2026-01-01T00:00:00Z",
		})
	}

	return b
}

func waiver(n int, expires, id string) exceptions.Waiver {
	return exceptions.Waiver{ID: id, Finding: fp(n), Rule: ruleOne, Owner: "o", Reason: "r", Expires: expires}
}

func codes(ds []diag.Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Code)
	}

	return out
}

func at(t *testing.T, text string) time.Time {
	t.Helper()

	v, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatal(err)
	}

	return v
}

func asDiag(t *testing.T, err error) *diag.Error {
	t.Helper()

	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("error %v is not a *diag.Error", err)
	}

	return de
}
