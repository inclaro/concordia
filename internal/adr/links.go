package adr

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/policy"
)

// ValidateLinks checks the `enforcedBy` links and the `released` rule refs of the register against the rules
// that the loaded packs declare. A link is valid when the pack id and version are loaded and declare the rule.
// The check uses CompileResult.DeclaredRules (every declared rule, before the `--rule` filter), so a filter never
// invalidates a link. An unknown rule, pack, or version gives a CO-ADR-004 error with exit code 2; a version
// mismatch names the loaded version.
func ValidateLinks(reg *Register, compiled *policy.CompileResult) error {
	declared, versions := loadedRules(compiled)

	for i := range reg.Entries {
		entry := &reg.Entries[i]

		for _, link := range entry.EnforcedBy {
			if err := checkLink(entry, link, declared, versions); err != nil {
				return err
			}
		}

		for j := range entry.Released {
			if rule := entry.Released[j].Rule; rule != nil {
				if err := checkLink(entry, *rule, declared, versions); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// loadedRules indexes the compiled packs: the rule IDs of each `<pack id>@<version>`, and the loaded versions of
// each pack ID. A pack that declares no rule is still loaded.
func loadedRules(compiled *policy.CompileResult) (declared map[string]map[string]struct{}, versions map[string][]string) {
	declared = map[string]map[string]struct{}{}
	versions = map[string][]string{}

	add := func(ref policy.PackRef) map[string]struct{} {
		key := ref.ID + "@" + ref.Version
		if declared[key] == nil {
			declared[key] = map[string]struct{}{}
		}

		if !slices.Contains(versions[ref.ID], ref.Version) {
			versions[ref.ID] = append(versions[ref.ID], ref.Version)
		}

		return declared[key]
	}

	for _, pack := range compiled.Packs {
		add(policy.PackRef{ID: pack.ID, Version: pack.Version})
	}

	for _, rule := range compiled.DeclaredRules {
		add(rule.Pack)[rule.RuleID] = struct{}{}
	}

	return declared, versions
}

func checkLink(entry *Entry, link RuleRef, declared map[string]map[string]struct{}, versions map[string][]string) error {
	fail := func(detail string) error {
		return diag.Fail(diag.ADR004, diag.Detail(fmt.Sprintf("decision %s: %s", entry.ID, detail)),
			diag.WithSource(diag.Source{URI: entry.File, Line: link.Line}))
	}

	loaded, packKnown := versions[link.PackID]
	if !packKnown {
		return fail(fmt.Sprintf("%s: pack %q is not loaded", link, link.PackID))
	}

	rules, versionKnown := declared[link.PackID+"@"+link.PackVersion]
	if !versionKnown {
		return fail(fmt.Sprintf("%s: pack %q version %q is not loaded; loaded version %s", link, link.PackID, link.PackVersion, strings.Join(loaded, ", ")))
	}

	if _, ok := rules[link.Rule]; !ok {
		return fail(fmt.Sprintf("%s: pack %q version %q declares no rule %q", link, link.PackID, link.PackVersion, link.Rule))
	}

	return nil
}
