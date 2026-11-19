package quality

import (
	"fmt"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
)

// checkDuplicates reports the second and later declaration of one id, naming both locations.
func checkDuplicates(set *Set) []*diag.Error {
	errs := duplicateDeclarations("scenario", set.Scenarios,
		func(s *Scenario) string { return s.ID }, func(s *Scenario) Location { return s.Location })

	return append(errs, duplicateDeclarations("finding", set.Findings,
		func(f *Finding) string { return f.ID }, func(f *Finding) Location { return f.Location })...)
}

// duplicateDeclarations walks the items in file order and reports every item whose id was already declared.
func duplicateDeclarations[T any](kind string, items []T, idOf func(T) string, locationOf func(T) Location) []*diag.Error {
	var errs []*diag.Error

	ordered := slices.Clone(items)
	slices.SortStableFunc(ordered, func(a, b T) int { return compareLocation(locationOf(a), locationOf(b)) })

	seen := map[string]Location{}

	for _, item := range ordered {
		id, loc := idOf(item), locationOf(item)

		if first, dup := seen[id]; dup {
			errs = append(errs, duplicate(kind, id, first, loc))

			continue
		}

		seen[id] = loc
	}

	return errs
}

func duplicate(kind, id string, first, second Location) *diag.Error {
	return diag.Fail(diag.QUA001, diag.Detail(fmt.Sprintf("%s %s is declared twice (%s:%d and %s:%d)",
		kind, id, first.File, first.Line, second.File, second.Line)), at(second))
}

// checkReferences resolves the scope, the structural rules, and the scenarios of the findings.
func checkReferences(set *Set, known Known) []*diag.Error {
	errs := make([]*diag.Error, 0, len(set.Scenarios)+len(set.Findings))

	ids := map[string]struct{}{}
	for _, sc := range set.Scenarios {
		ids[sc.ID] = struct{}{}
	}

	for _, scenario := range set.Scenarios {
		errs = append(errs, scenarioReferenceErrors(scenario, known)...)
	}

	for _, f := range set.Findings {
		errs = append(errs, findingReferenceErrors(f, ids)...)
	}

	return errs
}

// scenarioReferenceErrors names the scope elements the model lacks and the rules no policy defines.
func scenarioReferenceErrors(scenario *Scenario, known Known) []*diag.Error {
	var errs []*diag.Error

	for i, element := range scenario.Scope {
		if _, ok := known.Elements[element]; !ok {
			errs = append(errs, unresolvedReference(scenario.Location, scenario.scopeLines, i, fmt.Sprintf(
				"scenario %s: the scope names %s, which the architecture model does not contain", scenario.ID, element)))
		}
	}

	for i, rule := range scenario.Criterion.Rules {
		if _, ok := known.Rules[rule]; !ok {
			errs = append(errs, unresolvedReference(scenario.Location, scenario.ruleLines, i, fmt.Sprintf(
				"scenario %s: the rule %s is not defined by any loaded policy", scenario.ID, rule)))
		}
	}

	return errs
}

// findingReferenceErrors names the scenarios of the finding that are not declared.
func findingReferenceErrors(f *Finding, scenarioIDs map[string]struct{}) []*diag.Error {
	var errs []*diag.Error

	for i, id := range f.Scenarios {
		if _, ok := scenarioIDs[id]; !ok {
			errs = append(errs, unresolvedReference(f.Location, f.scenarioLines, i, fmt.Sprintf(
				"finding %s: the scenario %s is not declared", f.ID, id)))
		}
	}

	return errs
}

// unresolvedReference makes a CO-QUA-002 error at the line of the i-th reference, or at the item when that line
// is unknown.
func unresolvedReference(item Location, refLines []int, i int, detail string) *diag.Error {
	return diag.Fail(diag.QUA002, diag.Detail(detail), at(Location{File: item.File, Line: lineAt(refLines, i, item.Line)}))
}

func lineAt(list []int, i, fallback int) int {
	if i < len(list) && list[i] > 0 {
		return list[i]
	}

	return fallback
}

func at(loc Location) diag.Option {
	return diag.WithSource(diag.Source{URI: loc.File, Line: loc.Line})
}

func compareLocation(a, b Location) int {
	if c := canonjson.CompareUTF16(a.File, b.File); c != 0 {
		return c
	}

	return a.Line - b.Line
}
