package render

import (
	"fmt"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/diag"
)

// digestPrefix is the number of characters of a digest that a diagnostic shows.
const digestPrefix = 12

// maxListedDifferences bounds the resource identities that a non-determinism diagnostic names.
const maxListedDifferences = 5

// CompareRenders compares two renders of the same source (design D18: `assurance validate` renders twice; a run
// never does). It gives one CO-PRV-014 warning when the output digests or the coverage differ, which means that a
// template function made the output random or time-dependent (for example `randAlphaNum` or `now`), or that the
// renderer is not deterministic. Two renders that both failed are not compared: there is no output to differ.
// The function is pure: it reads the two results and starts nothing.
func CompareRenders(first, second *Result) []diag.Diagnostic {
	if first == nil || second == nil {
		return nil
	}

	if first.Coverage == assurance.CoverageFailed && second.Coverage == assurance.CoverageFailed {
		return nil
	}

	if first.Coverage == second.Coverage && first.OutputDigest == second.OutputDigest {
		return nil
	}

	var detail strings.Builder

	fmt.Fprintf(&detail, "environment %s (%s): the two renders differ", first.Environment, first.Path)

	if first.Coverage != second.Coverage {
		fmt.Fprintf(&detail, "; coverage %s, then %s", first.Coverage, second.Coverage)
	} else {
		fmt.Fprintf(&detail, "; output digest %s, then %s", short(first.OutputDigest), short(second.OutputDigest))
	}

	if changed := differingResources(first, second); len(changed) > 0 {
		listed := changed
		if len(listed) > maxListedDifferences {
			listed = listed[:maxListedDifferences]
		}

		detail.WriteString("; differing resources: " + strings.Join(listed, ", "))

		if len(changed) > len(listed) {
			fmt.Fprintf(&detail, " and %d more", len(changed)-len(listed))
		}
	}

	return []diag.Diagnostic{newDiagnostic(diag.PRV014, detail.String(), first.Path)}
}

// differingResources lists the identities that are in only one render or have a different document digest. Both
// lists are sorted, so the result is too.
func differingResources(first, second *Result) []string {
	index := func(r *Result) map[ResourceID]string {
		digests := make(map[ResourceID]string, len(r.Resources))
		for i := range r.Resources {
			digests[r.Resources[i].ID] = r.Resources[i].Digest
		}

		return digests
	}

	left, right := index(first), index(second)

	var out []string

	for i := range first.Resources {
		res := &first.Resources[i]
		if digest, ok := right[res.ID]; !ok || digest != res.Digest {
			out = append(out, res.ID.String())
		}
	}

	for i := range second.Resources {
		if _, ok := left[second.Resources[i].ID]; !ok {
			out = append(out, second.Resources[i].ID.String())
		}
	}

	return out
}

func short(digest string) string {
	if len(digest) > digestPrefix {
		return digest[:digestPrefix]
	}

	return digest
}
