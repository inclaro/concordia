package adr_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/testutil"
)

const registerHeader = "apiVersion: concordia.io/v1alpha1\nkind: DecisionRegister\ndecisions:\n"

const validDecision = `  - id: ADR-0003
    path: docs/adr/0003-orders-own-db.md
    categories: [system-boundaries]
    governs:
      - ref: calm:node:orders-db
      - select: {tags: [orders-data]}
    enforcement: required
    enforcedBy:
      - rule: CORE-DB-001
        pack: {id: core, version: 1.0.0}
`

func loadFiles(t *testing.T, files map[string]string, globs ...string) (*adr.Register, error) {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, files)

	return adr.LoadRegister(dir, globs)
}

func wantDiag(t *testing.T, err error, code string) *diag.Error {
	t.Helper()

	var de *diag.Error
	if !errors.As(err, &de) {
		t.Fatalf("error = %v, want %s with exit code 2", err, code)
	}

	if de.Diagnostic.Code != code {
		t.Fatalf("error = %v, want %s with exit code 2", err, code)
	}

	if de.ExitCode != diag.InvalidInput {
		t.Fatalf("error = %v, want %s with exit code 2", err, code)
	}

	return de
}

func loadValidEntries(t *testing.T) (hist, active adr.Entry) {
	t.Helper()

	reg, err := loadFiles(t, map[string]string{"gov/decisions.yaml": registerHeader + validDecision +
		"  - id: adr-1\n    expect: historical\n    governs: [{ref: 'calm:interface:a/b'}]\n" +
		"    released:\n      - {ref: 'calm:node:old', reason: retired}\n      - {rule: 'CORE-DB-001@core/1.0.0', reason: moved}\n"}, "gov/decisions.yaml")
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}

	registerWantCount(t, "files", len(reg.Files), 1)
	registerWantEqual(t, "digest prefix", strings.HasPrefix(reg.Files[0].Digest, "sha256:"), true)
	registerWantCount(t, "entries", len(reg.Entries), 2)

	ids := reg.Entries[0].ID + "," + reg.Entries[1].ID
	if ids != "ADR-1,ADR-3" {
		t.Fatalf("entries not sorted by canonical ID: %+v", reg.Entries)
	}

	return reg.Entries[0], reg.Entries[1]
}

// releasedRuleText gives the rule of a released item in ref form, "<nil>" when the item has no rule.
func releasedRuleText(rel adr.Release) string {
	if rel.Rule == nil {
		return "<nil>"
	}

	return rel.Rule.String()
}

// selectTagsText gives the tags of the selector of a governs item, "<nil>" when the item has no selector.
func selectTagsText(ref adr.GovernsRef) string {
	if ref.Select == nil {
		return "<nil>"
	}

	return strings.Join(ref.Select.Tags, ",")
}

func checkHistoricalEntry(t *testing.T, hist *adr.Entry) {
	t.Helper()

	registerWantEqual(t, "historical expect", hist.Expect, adr.ExpectHistorical)
	registerWantCount(t, "released items", len(hist.Released), 2)
	registerWantEqual(t, "released[1] rule", releasedRuleText(hist.Released[1]), "CORE-DB-001@core/1.0.0")
	registerWantEqual(t, "released[0] reason", hist.Released[0].Reason, "retired")
}

func checkActiveEntry(t *testing.T, active *adr.Entry) {
	t.Helper()

	registerWantEqual(t, "active expect", active.Expect, adr.ExpectActive)
	registerWantEqual(t, "active enforcement", active.Enforcement, adr.EnforcementRequired)
	registerWantEqual(t, "active path", active.Path, "docs/adr/0003-orders-own-db.md")
	registerWantCount(t, "governs items", len(active.Governs), 2)
	registerWantEqual(t, "governs[1] select tags", selectTagsText(active.Governs[1]), "orders-data")
	registerWantCount(t, "enforcedBy items", len(active.EnforcedBy), 1)
	registerWantEqual(t, "enforcedBy[0]", active.EnforcedBy[0].String(), "CORE-DB-001@core/1.0.0")
}

// checkActiveLines checks the line numbers of the entry and its items.
func checkActiveLines(t *testing.T, active *adr.Entry) {
	t.Helper()

	registerWantEqual(t, "entry file", active.File, "gov/decisions.yaml")
	registerWantEqual(t, "entry line", active.Line, 4)
	registerWantEqual(t, "governs[0] line", active.Governs[0].Line, 8)
	registerWantEqual(t, "governs[1] line", active.Governs[1].Line, 9)
	registerWantEqual(t, "enforcedBy[0] line", active.EnforcedBy[0].Line, 12)
}

// Scenario: Valid register is loaded.
func TestLoadRegisterValid(t *testing.T) {
	t.Parallel()

	hist, active := loadValidEntries(t)

	checkHistoricalEntry(t, &hist)
	checkActiveEntry(t, &active)
	checkActiveLines(t, &active)
	registerWantEqual(t, "EntryKey", adr.EntryKey(&active), "ADR-3\x00")
}

func TestLoadRegisterGlobAndMissingFile(t *testing.T) {
	t.Parallel()

	reg, err := loadFiles(t, map[string]string{
		"gov/a.yaml": registerHeader + validDecision,
		"gov/b.yaml": registerHeader + "  - {id: ADR-9, governs: [{ref: 'calm:node:x'}]}\n",
	}, "gov/*.yaml")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	registerWantCount(t, "files", len(reg.Files), 2)
	registerWantCount(t, "entries", len(reg.Entries), 2)

	_, err = loadFiles(t, nil, "gov/missing.yaml")
	_ = wantDiag(t, err, diag.ADR001)
}

func TestLoadRegisterInvalid(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		// Scenario: Unknown key is rejected.
		"unknown root key":  "apiVersion: concordia.io/v1alpha1\nkind: DecisionRegister\ndecisions: []\nextra: 1\n",
		"unknown entry key": registerHeader + "  - {id: ADR-1, governs: [{ref: 'calm:node:x'}], owner: me}\n",
		// Scenario: Decision without applicability is rejected.
		"no governs":         registerHeader + "  - {id: ADR-1}\n",
		"empty governs":      registerHeader + "  - {id: ADR-1, governs: []}\n",
		"wrong kind":         "apiVersion: concordia.io/v1alpha1\nkind: Other\ndecisions: []\n",
		"bad expect":         registerHeader + "  - {id: ADR-1, expect: maybe, governs: [{ref: 'calm:node:x'}]}\n",
		"bad enforcement":    registerHeader + "  - {id: ADR-1, enforcement: strict, governs: [{ref: 'calm:node:x'}]}\n",
		"bad ref":            registerHeader + "  - {id: ADR-1, governs: [{ref: 'node:x'}]}\n",
		"unknown select key": registerHeader + "  - {id: ADR-1, governs: [{select: {names: [a]}}]}\n",
		"ref and select":     registerHeader + "  - {id: ADR-1, governs: [{ref: 'calm:node:x', select: {tags: [a]}}]}\n",
		"unknown pack key":   registerHeader + "  - {id: ADR-1, governs: [{ref: 'calm:node:x'}], enforcedBy: [{rule: R-1, pack: {id: core, version: 1, x: 1}}]}\n",
		"yaml syntax":        "decisions: [\n",
		// Scenario: Release on an active decision.
		"released on active":   registerHeader + "  - {id: ADR-1, governs: [{ref: 'calm:node:x'}], released: [{ref: 'calm:node:x', reason: r}]}\n",
		"released on explicit": registerHeader + "  - {id: ADR-1, expect: active, governs: [{ref: 'calm:node:x'}], released: [{ref: 'calm:node:x', reason: r}]}\n",
		"released both":        registerHeader + "  - {id: ADR-1, expect: historical, governs: [{ref: 'calm:node:x'}], released: [{ref: 'calm:node:x', rule: 'R-1@core/1', reason: r}]}\n",
		"released neither":     registerHeader + "  - {id: ADR-1, expect: historical, governs: [{ref: 'calm:node:x'}], released: [{reason: r}]}\n",
		"released no reason":   registerHeader + "  - {id: ADR-1, expect: historical, governs: [{ref: 'calm:node:x'}], released: [{ref: 'calm:node:x'}]}\n",
		"released bad rule":    registerHeader + "  - {id: ADR-1, expect: historical, governs: [{ref: 'calm:node:x'}], released: [{rule: 'CORE-DB-001', reason: r}]}\n",
	}

	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := loadFiles(t, map[string]string{"r.yaml": text}, "r.yaml")
			de := wantDiag(t, err, diag.ADR001)

			if de.Diagnostic.Source == nil || de.Diagnostic.Source.URI != "r.yaml" {
				t.Errorf("source = %+v, want r.yaml", de.Diagnostic.Source)
			}
		})
	}
}

// Scenario: Register ID in another form is rejected.
// A register ID must be ADR-<number>: any other form could never match a decision, so it fails validation and names
// the ID instead of leaving the decision silently absent.
func TestLoadRegisterRejectsIDsThatAreNotADRNumbers(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"ADR 7", "ADR_0007", "ADR7", "7", "0007", "DEC-7", "ADR-", "ADR-7a"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			text := registerHeader + "  - {id: '" + id + "', governs: [{ref: 'calm:node:x'}]}\n"
			_, err := loadFiles(t, map[string]string{"r.yaml": text}, "r.yaml")

			de := wantDiag(t, err, diag.ADR001)
			registerWantContains(t, "message", de.Diagnostic.Message, `"`+id+`"`, "ADR-<number>")
			registerWantEqual(t, "line", registerSourceOf(t, de).Line, 4)
		})
	}
}

// registerWantContains reports each part that the text lacks.
func registerWantContains(t *testing.T, what, text string, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("%s = %q, want it to contain %q", what, text, part)
		}
	}
}

// registerWantEqual reports a value that differs from the wanted one.
func registerWantEqual[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// registerWantCount stops the test when the count differs: the checks that follow index into the items.
func registerWantCount(t *testing.T, what string, got, want int) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %d, want %d", what, got, want)
	}
}

// registerSourceOf gives the source of the diagnostic and stops the test when there is none.
func registerSourceOf(t *testing.T, de *diag.Error) *diag.Source {
	t.Helper()

	if de.Diagnostic.Source == nil {
		t.Fatalf("diagnostic = %+v, want a source", de.Diagnostic)
	}

	return de.Diagnostic.Source
}

// Scenario: Register ID spellings are one decision.
// The prefix in any case and the number with or without leading zeros are the same decision; results name it in
// canonical form.
func TestLoadRegisterNormalizesIDs(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"ADR-0007", "adr-007", "Adr-7", " ADR-7 "} {
		reg, err := loadFiles(t, map[string]string{"r.yaml": registerHeader + "  - {id: '" + id + "', governs: [{ref: 'calm:node:x'}]}\n"}, "r.yaml")
		if err != nil || reg.Entries[0].ID != "ADR-7" {
			t.Errorf("id %q: entries %+v, err %v; want ADR-7", id, reg, err)
		}
	}
}

func TestLoadRegisterReportsALine(t *testing.T) {
	t.Parallel()

	_, err := loadFiles(t, map[string]string{"r.yaml": registerHeader + "  - {id: ADR-1, governs: [{ref: 'calm:node:x'}]}\n  - {id: ADR-2}\n"}, "r.yaml")
	de := wantDiag(t, err, diag.ADR001)

	if de.Diagnostic.Source.Line != 5 {
		t.Errorf("line = %d, want 5", de.Diagnostic.Source.Line)
	}
}

// registerEntryText is a register entry with the given ID and one governs ref.
func registerEntryText(id string) string {
	return "  - {id: " + id + ", governs: [{ref: 'calm:node:x'}]}\n"
}

// Scenario: Duplicate registered ID.
func TestLoadRegisterDuplicateIDsInOneFile(t *testing.T) {
	t.Parallel()

	_, err := loadFiles(t, map[string]string{
		"a.yaml": registerHeader + registerEntryText("ADR-0003") + registerEntryText("adr-3"),
	}, "a.yaml")
	de := wantDiag(t, err, diag.ADR002)

	registerWantContains(t, "message", de.Diagnostic.Message, "a.yaml:4", "a.yaml:5", "ADR-3")
}

func TestLoadRegisterDuplicateIDsInTwoFiles(t *testing.T) {
	t.Parallel()

	_, err := loadFiles(t, map[string]string{
		"a.yaml": registerHeader + registerEntryText("ADR-3"),
		"b.yaml": registerHeader + registerEntryText("ADR-0003"),
	}, "a.yaml", "b.yaml")
	de := wantDiag(t, err, diag.ADR002)

	registerWantContains(t, "message", de.Diagnostic.Message, "a.yaml:4", "b.yaml:4")
}

func TestLoadRegisterSameIDInTwoProjectsIsNotADuplicate(t *testing.T) {
	t.Parallel()

	reg, err := loadFiles(t, map[string]string{"a.yaml": registerHeader +
		"  - {id: ADR-3, project: orders, governs: [{ref: 'calm:node:x'}]}\n  - {id: ADR-3, project: billing, governs: [{ref: 'calm:node:x'}]}\n"}, "a.yaml")
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}

	registerWantCount(t, "entries", len(reg.Entries), 2)
	registerWantEqual(t, "first project", reg.Entries[0].Project, "billing")
}

func TestLoadRegisterDoesNotWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteTree(t, dir, map[string]string{"r.yaml": registerHeader + validDecision})

	before, _ := os.ReadFile(filepath.Join(dir, "r.yaml"))

	if _, err := adr.LoadRegister(dir, []string{"r.yaml"}); err != nil {
		t.Fatal(err)
	}

	after, _ := os.ReadFile(filepath.Join(dir, "r.yaml"))
	if !bytes.Equal(before, after) {
		t.Error("register file changed")
	}
}
