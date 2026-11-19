package adr_test

import (
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/adr"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

func compiledPacks() *policy.CompileResult {
	return &policy.CompileResult{
		Packs:   []policy.PolicyPackRef{{ID: "core", Version: "1.0.0"}, {ID: "adr-governance", Version: "1.0.0"}},
		RuleIDs: []string{"CORE-DB-001", "CORE-DB-002"},
		DeclaredRules: []policy.DeclaredRule{
			{RuleID: "CORE-DB-001", Pack: policy.PackRef{ID: "core", Version: "1.0.0"}},
			{RuleID: "CORE-DB-002", Pack: policy.PackRef{ID: "core", Version: "1.0.0"}},
		},
	}
}

func linkRegister(enforced adr.RuleRef, released ...adr.Release) *adr.Register {
	return &adr.Register{Entries: []adr.Entry{{
		ID: "ADR-3", File: "r.yaml", Line: 4, Expect: adr.ExpectHistorical,
		EnforcedBy: []adr.RuleRef{enforced}, Released: released,
	}}}
}

func link(rule, pack, version string) adr.RuleRef {
	return adr.RuleRef{Rule: rule, PackID: pack, PackVersion: version, Line: 12}
}

// Scenario: Link to a loaded rule version.
func TestValidateLinksLoadedVersionIsValid(t *testing.T) {
	t.Parallel()

	if err := adr.ValidateLinks(linkRegister(link("CORE-DB-001", "core", "1.0.0")), compiledPacks()); err != nil {
		t.Errorf("ValidateLinks: %v", err)
	}
}

func TestValidateLinksFailures(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		ref   adr.RuleRef
		named []string
	}{
		// Scenario: Link to an unknown rule.
		"unknown rule":     {link("CORE-DB-999", "core", "1.0.0"), []string{"CORE-DB-999"}},
		"rule of the pack": {link("CORE-DB-001", "adr-governance", "1.0.0"), []string{"CORE-DB-001"}},
		// Scenario: Link to a pack version that is not loaded.
		"version mismatch":  {link("CORE-DB-001", "core", "0.9.0"), []string{"0.9.0", "loaded version 1.0.0"}},
		"unknown pack":      {link("CORE-DB-001", "platform", "1.0.0"), []string{"platform"}},
		"version of a pack": {link("CORE-DB-001", "adr-governance", "2.0.0"), []string{"loaded version 1.0.0"}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := adr.ValidateLinks(linkRegister(tc.ref), compiledPacks())
			de := wantDiag(t, err, diag.ADR004)

			for _, want := range tc.named {
				if !strings.Contains(de.Diagnostic.Message, want) {
					t.Errorf("message %q lacks %q", de.Diagnostic.Message, want)
				}
			}

			if de.Diagnostic.Source.URI != "r.yaml" || de.Diagnostic.Source.Line != 12 {
				t.Errorf("source = %+v", de.Diagnostic.Source)
			}
		})
	}
}

func TestValidateLinksRuleFilterDoesNotInvalidate(t *testing.T) {
	t.Parallel()

	// A `--rule CORE-DB-002` filter keeps one plan; RuleIDs and DeclaredRules still list every declared rule.
	compiled := compiledPacks()
	compiled.Plans = []policy.RulePlan{{RuleID: "CORE-DB-002"}}

	if err := adr.ValidateLinks(linkRegister(link("CORE-DB-001", "core", "1.0.0")), compiled); err != nil {
		t.Errorf("a filtered rule must stay a valid link: %v", err)
	}
}

// Scenario: Released rule is not loaded.
func TestValidateLinksReleasedRuleRefs(t *testing.T) {
	t.Parallel()

	released := func(r adr.RuleRef) adr.Release { return adr.Release{Rule: &r, Reason: "moved", Line: 20} }
	good := link("CORE-DB-001", "core", "1.0.0")

	if err := adr.ValidateLinks(linkRegister(good, released(good)), compiledPacks()); err != nil {
		t.Errorf("valid released rule: %v", err)
	}

	for name, bad := range map[string]adr.RuleRef{
		"unknown rule": link("CORE-NOPE-1", "core", "1.0.0"),
		"version":      link("CORE-DB-001", "core", "2.0.0"),
		"pack":         link("CORE-DB-001", "platform", "1.0.0"),
	} {
		err := adr.ValidateLinks(linkRegister(good, released(bad)), compiledPacks())
		_ = wantDiag(t, err, diag.ADR004)

		_ = name
	}

	// A subject release is not a rule link.
	if err := adr.ValidateLinks(linkRegister(good, adr.Release{Ref: "calm:node:x", Reason: "r"}), compiledPacks()); err != nil {
		t.Errorf("subject release: %v", err)
	}
}
