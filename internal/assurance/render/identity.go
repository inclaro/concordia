package render

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
)

// entry is one rendered document with what was read from it, before identities are settled.
type entry struct {
	// canonical is the redacted canonical JSON of the document.
	canonical   []byte
	occurrences []ContainerOccurrence
	resource    Resource
}

// conflict says that several documents of one render have one identity.
type conflict struct {
	ID ResourceID
	// Locations are the locations of all the documents, in render order.
	Locations []string
}

// settleIdentities applies the identity rule: documents with one scoped identity are one conflicting resource, not
// several deployed instances. It keeps the first document of each identity, marks it as conflicting when others
// exist, and drops the container occurrences of a conflicting workload (claims about it are unknown, so it must not
// look like one complete workload). The order of the kept entries is the order of their first document.
func settleIdentities(entries []entry) ([]entry, []conflict) {
	byID := make(map[ResourceID][]int, len(entries))

	for i := range entries {
		byID[entries[i].resource.ID] = append(byID[entries[i].resource.ID], i)
	}

	var (
		kept      []entry
		conflicts []conflict
	)

	for i := range entries {
		ident := entries[i].resource.ID
		group := byID[ident]

		if group[0] != i {
			continue
		}

		head := entries[i]

		if len(group) > 1 {
			head.resource.Conflict = true
			head.occurrences = nil

			item := conflict{ID: ident}
			for _, j := range group {
				item.Locations = append(item.Locations, entries[j].resource.Location)
			}

			conflicts = append(conflicts, item)
		}

		kept = append(kept, head)
	}

	slices.SortFunc(conflicts, func(a, b conflict) int { return CompareResourceIDs(a.ID, b.ID) })

	return kept, conflicts
}

// sortEntries puts the resources in a stable order: identity first, then the document content, so the order does
// not depend on the order of the files or of the renderer output.
func sortEntries(entries []entry) {
	slices.SortStableFunc(entries, func(a, b entry) int {
		if c := CompareResourceIDs(a.resource.ID, b.resource.ID); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(string(a.canonical), string(b.canonical))
	})
}

// kindRank orders container kinds inside one workload: containers, init containers, ephemeral containers.
func kindRank(kind string) int {
	switch kind {
	case KindContainer:
		return 0
	case KindInit:
		return 1
	default:
		return 2
	}
}

// sortOccurrences orders occurrences by workload identity, then kind, then container name.
func sortOccurrences(list []ContainerOccurrence) {
	slices.SortStableFunc(list, func(a, b ContainerOccurrence) int {
		if c := CompareResourceIDs(a.Workload, b.Workload); c != 0 {
			return c
		}

		if c := kindRank(a.Kind) - kindRank(b.Kind); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Container, b.Container)
	})
}
