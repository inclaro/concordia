package trust

import "sort"

// Declaration is the trust context that one CALM node, CALM relationship, or
// binding entry declares: at most one value per dimension and at most one data
// classification. An empty Classification means "not declared".
type Declaration struct {
	// Dimensions maps a dimension name to its value.
	Dimensions map[string]string `json:"dimensions,omitempty"`
	// Classification is a value of the workspace `trust.classifications` list.
	Classification string `json:"classification,omitempty"`
}

// IsZero says whether the declaration assigns nothing.
func (d Declaration) IsZero() bool {
	return len(d.Dimensions) == 0 && d.Classification == ""
}

// Value gives the declared value of a dimension, or "" when there is none.
func (d Declaration) Value(dimension string) string {
	return d.Dimensions[dimension]
}

// Merge combines declarations of the same element (a CALM node and the binding
// entries that bind to it). A dimension that two declarations assign to different
// values, and a classification on which they differ, are dropped, because Concordia
// cannot tell which one is meant; the dropped names (sorted, "classification" for
// the classification) are the second result. Equal values merge silently.
func Merge(declarations ...Declaration) (merged Declaration, conflicts []string) {
	values := map[string]string{}
	dropped := map[string]bool{}
	class := ""
	classDropped := false

	for _, decl := range declarations {
		for name, value := range decl.Dimensions {
			if value == "" {
				continue
			}

			if prior, seen := values[name]; seen && prior != value {
				dropped[name] = true
			}

			values[name] = value
		}

		switch {
		case decl.Classification == "":
		case class != "" && class != decl.Classification:
			classDropped = true
		default:
			class = decl.Classification
		}
	}

	for name := range dropped {
		delete(values, name)
		conflicts = append(conflicts, name)
	}

	sort.Strings(conflicts)

	if classDropped {
		class = ""

		conflicts = append(conflicts, "classification")
	}

	if len(values) > 0 {
		merged.Dimensions = values
	}

	merged.Classification = class

	return merged, conflicts
}
