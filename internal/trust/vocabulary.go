// Package trust holds the trust and data context of a workspace: the authority
// dimensions and data classifications that a CALM node, a CALM relationship, or a
// binding entry can declare, and the boundary crossings that Concordia derives for
// flows from those explicit values.
//
// Everything here is a pure function over declared values. A namespace name, a
// network location, a tag, or a file path never produces a value; a dimension or a
// classification that nobody declared stays `unknown`.
package trust

import (
	"slices"
	"sort"
	"strings"
)

// Built-in authority dimensions.
const (
	DimensionOrganization = "organization"
	DimensionAccount      = "account"
	DimensionPrincipal    = "principal"
	DimensionZone         = "zone"
	DimensionRegion       = "region"
)

// Unknown is the value that stands for "not declared", in a crossing end and in a
// data classification.
const Unknown = "unknown"

// BuiltinDimensions gives the built-in dimension names, in their fixed order.
func BuiltinDimensions() []string {
	return []string{DimensionOrganization, DimensionAccount, DimensionPrincipal, DimensionZone, DimensionRegion}
}

// Vocabulary is the set of dimension names and data classifications that a
// workspace accepts. The zero value accepts the built-in dimensions and no
// classification.
type Vocabulary struct {
	dimensions      []string
	classifications []string
}

// NewVocabulary builds the vocabulary of a workspace from its declared extra
// dimension names (`trust.dimensions`) and its `trust.classifications` list. The
// built-in dimensions come first, then the declared ones in their given order
// without duplicates. Classifications keep their given order without duplicates.
func NewVocabulary(declaredDimensions, classifications []string) Vocabulary {
	dimensions := BuiltinDimensions()

	for _, name := range declaredDimensions {
		if !slices.Contains(dimensions, name) {
			dimensions = append(dimensions, name)
		}
	}

	list := make([]string, 0, len(classifications))

	for _, class := range classifications {
		if !slices.Contains(list, class) {
			list = append(list, class)
		}
	}

	return Vocabulary{dimensions: dimensions, classifications: list}
}

// Dimensions gives every accepted dimension name (built-ins first). Never nil.
func (v Vocabulary) Dimensions() []string {
	if len(v.dimensions) == 0 {
		return BuiltinDimensions()
	}

	return slices.Clone(v.dimensions)
}

// Classifications gives the accepted data classifications. Never nil.
func (v Vocabulary) Classifications() []string {
	return append([]string{}, v.classifications...)
}

// HasDimension says whether name is an accepted dimension.
func (v Vocabulary) HasDimension(name string) bool {
	return slices.Contains(v.Dimensions(), name)
}

// HasClassification says whether class is in the classification list.
func (v Vocabulary) HasClassification(class string) bool {
	return slices.Contains(v.classifications, class)
}

// ProblemKind says what is wrong with a trust declaration.
type ProblemKind string

// Problem kinds.
const (
	// UndeclaredDimension is a dimension name that is neither built in nor declared.
	UndeclaredDimension ProblemKind = "undeclared-dimension"
	// UnlistedClassification is a classification that `trust.classifications` does not list.
	UnlistedClassification ProblemKind = "unlisted-classification"
	// EmptyValue is a dimension that is assigned an empty value.
	EmptyValue ProblemKind = "empty-value"
)

// Problem is one failure of Check.
type Problem struct {
	Kind ProblemKind
	// Name is the dimension name, or "classification".
	Name string
	// Value is the offending value (a classification), "" for a dimension problem.
	Value string
}

// Detail is the text of the problem for a CO-SEC-001 diagnostic.
func (p Problem) Detail() string {
	switch p.Kind {
	case UndeclaredDimension:
		return "trust dimension '" + p.Name + "' is not declared (built-in: " + strings.Join(BuiltinDimensions(), ", ") + "; add it to trust.dimensions)"
	case UnlistedClassification:
		return "data classification '" + p.Value + "' is not in trust.classifications"
	case EmptyValue:
		return "trust dimension '" + p.Name + "' has an empty value"
	}

	return string(p.Kind)
}

// Check validates a declaration against the vocabulary. Problems come sorted by
// dimension name, then the classification problem.
func (v Vocabulary) Check(decl Declaration) []Problem {
	var out []Problem

	names := make([]string, 0, len(decl.Dimensions))
	for name := range decl.Dimensions {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		switch {
		case !v.HasDimension(name):
			out = append(out, Problem{Kind: UndeclaredDimension, Name: name})
		case decl.Dimensions[name] == "":
			out = append(out, Problem{Kind: EmptyValue, Name: name})
		}
	}

	if decl.Classification != "" && !v.HasClassification(decl.Classification) {
		out = append(out, Problem{Kind: UnlistedClassification, Name: "classification", Value: decl.Classification})
	}

	return out
}

// Valid gives the part of the declaration that the vocabulary accepts and the
// problems of the rest. A rejected dimension or classification is dropped, so a
// later check never uses an undeclared name or an unlisted classification. The
// declaration d is not modified.
func (v Vocabulary) Valid(d Declaration) (Declaration, []Problem) {
	problems := v.Check(d)
	out := Declaration{Classification: d.Classification}

	if len(d.Dimensions) > 0 {
		out.Dimensions = make(map[string]string, len(d.Dimensions))
		for name, value := range d.Dimensions {
			out.Dimensions[name] = value
		}
	}

	for _, p := range problems {
		if p.Kind == UnlistedClassification {
			out.Classification = ""
		} else {
			delete(out.Dimensions, p.Name)
		}
	}

	if len(out.Dimensions) == 0 {
		out.Dimensions = nil
	}

	return out, problems
}

// Pointer gives the JSON Pointer (below the `trust` object at base) of the
// declaration part that the problem names.
func (p Problem) Pointer(base string) string {
	if p.Kind == UnlistedClassification {
		return base + "/classification"
	}

	return base + "/dimensions/" + escapePointer(p.Name)
}

func escapePointer(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
}
