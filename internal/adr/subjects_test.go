package adr_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
)

func subjectModel() *intent.ArchitectureModel {
	return &intent.ArchitectureModel{
		Nodes: []intent.NodeDeclaration{
			{ID: "orders", NodeType: "service", Tags: []string{"orders-data"}, Interfaces: []string{"http"}},
			{ID: "orders-db", NodeType: "database", Tags: []string{"orders-data"}, Interfaces: []string{"sql", "admin"}},
			{ID: "billing", NodeType: "service", Tags: []string{"billing"}, Interfaces: []string{}},
		},
		Relationships: []intent.RelationshipDeclaration{{ID: "orders-to-db", Kind: intent.RelConnects, Source: "orders", Targets: []string{"orders-db"}}},
	}
}

func governs(items ...adr.GovernsRef) *adr.Entry {
	return &adr.Entry{ID: "ADR-1", Governs: items, File: "r.yaml", Line: 3}
}

func ref(r string) adr.GovernsRef { return adr.GovernsRef{Ref: r, Line: 5} }

// Scenario: Node, relationship, and interface references.
func TestResolveSubjectsRefKinds(t *testing.T) {
	t.Parallel()

	got, err := adr.ResolveSubjects(subjectModel(), governs(
		ref("calm:node:orders-db"), ref("calm:relationship:orders-to-db"), ref("calm:interface:orders-db/sql"), ref("calm:node:orders-db")))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"calm:interface:orders-db/sql", "calm:node:orders-db", "calm:relationship:orders-to-db"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Scenario: Reference to an unknown subject.
func TestResolveSubjectsUnknownIDs(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"calm:node:ghost":                 "ghost",
		"calm:relationship:ghost":         "ghost",
		"calm:interface:orders-db/ghost":  "orders-db/ghost",
		"calm:interface:ghost/sql":        "ghost",
		"calm:interface:billing/sql":      "billing/sql",
		"calm:interface:orders/admin":     "orders/admin",
		"calm:rule:something-else-entire": "calm:rule",
	}

	for r, named := range cases {
		t.Run(r, func(t *testing.T) {
			t.Parallel()

			_, err := adr.ResolveSubjects(subjectModel(), governs(ref("calm:node:orders"), ref(r)))
			de := wantDiag(t, err, diag.ADR003)

			if !strings.Contains(de.Diagnostic.Message, named) || de.Diagnostic.Source.Line != 5 || de.Diagnostic.Source.URI != "r.yaml" {
				t.Errorf("diagnostic %+v does not name %q at r.yaml:5", de.Diagnostic, named)
			}
		})
	}
}

func TestResolveSubjectsSelectors(t *testing.T) {
	t.Parallel()

	sel := func(s adr.Selector) adr.GovernsRef { return adr.GovernsRef{Select: &s, Line: 7} }

	got, err := adr.ResolveSubjects(subjectModel(), governs(
		sel(adr.Selector{Tags: []string{"orders-data"}}), sel(adr.Selector{NodeTypes: []string{"service"}}), ref("calm:relationship:orders-to-db")))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"calm:node:billing", "calm:node:orders", "calm:node:orders-db", "calm:relationship:orders-to-db"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v (sorted, selector expanded to nodes)", got, want)
	}

	got, err = adr.ResolveSubjects(subjectModel(), governs(sel(adr.Selector{CalmNodes: []string{"billing", "ghost"}, Tags: []string{"orders-data"}})))
	if err != nil || !slices.Equal(got, []string{"calm:node:billing", "calm:node:orders", "calm:node:orders-db"}) {
		t.Errorf("union: got %v, %v", got, err)
	}
}

// Scenario: Selector resolves to nothing.
func TestResolveSubjectsEmptySelector(t *testing.T) {
	t.Parallel()

	for name, s := range map[string]adr.Selector{
		"unknown tag": {Tags: []string{"nope"}},
		"unknown id":  {CalmNodes: []string{"ghost"}},
		"no keys":     {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := adr.ResolveSubjects(subjectModel(), governs(adr.GovernsRef{Select: &s, Line: 7}))
			de := wantDiag(t, err, diag.ADR003)

			if !strings.Contains(de.Diagnostic.Message, "matches no") || de.Diagnostic.Source.Line != 7 {
				t.Errorf("diagnostic = %+v", de.Diagnostic)
			}
		})
	}
}

func TestResolveSubjectsChecksReleasedSubjects(t *testing.T) {
	t.Parallel()

	entry := governs(ref("calm:node:orders"))
	entry.Expect = adr.ExpectHistorical
	entry.Released = []adr.Release{{Ref: "calm:node:billing", Reason: "r"}, {Rule: &adr.RuleRef{Rule: "CORE-X-1", PackID: "core", PackVersion: "1"}, Reason: "r"}}

	got, err := adr.ResolveSubjects(subjectModel(), entry)
	if err != nil || !slices.Equal(got, []string{"calm:node:orders"}) {
		t.Fatalf("known released: %v, %v", got, err)
	}

	entry.Released[0].Ref, entry.Released[0].Line = "calm:node:retired", 9
	_, err = adr.ResolveSubjects(subjectModel(), entry)
	de := wantDiag(t, err, diag.ADR003)

	if !strings.Contains(de.Diagnostic.Message, "retired") || de.Diagnostic.Source.Line != 9 {
		t.Errorf("diagnostic = %+v", de.Diagnostic)
	}
}
