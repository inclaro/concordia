package observation

import (
	"slices"
	"strings"
)

// matchInput is what the rules look at.
type matchInput struct {
	props       map[string]string
	sourceFile  string
	targetKind  string
	sourceClass LabelClass
}

func (d *Discriminators) matches(input *matchInput) bool {
	return d.propertiesAdmit(input.props) &&
		d.presenceAdmits(input.props) &&
		listAdmits(d.SourceClasses, input.sourceClass) &&
		listAdmits(d.SourceFiles, input.sourceFile) &&
		listAdmits(d.TargetKinds, input.targetKind)
}

// propertiesAdmit says whether the value of each listed property of the edge is one that the rule allows.
func (d *Discriminators) propertiesAdmit(props map[string]string) bool {
	for name, allowed := range d.Properties {
		if !slices.Contains(allowed, props[name]) {
			return false
		}
	}

	return true
}

// presenceAdmits says whether the edge has every property that the rule needs and none that the rule excludes.
func (d *Discriminators) presenceAdmits(props map[string]string) bool {
	return allPropertiesSet(props, d.Present, true) && allPropertiesSet(props, d.Absent, false)
}

// allPropertiesSet says whether every named property of the edge is set (or, for set false, none is).
func allPropertiesSet(props map[string]string, names []string, set bool) bool {
	for _, name := range names {
		if (props[name] != "") != set {
			return false
		}
	}

	return true
}

// listAdmits says whether a rule list allows a value: an empty list allows every value.
func listAdmits[T comparable](list []T, value T) bool {
	return len(list) == 0 || slices.Contains(list, value)
}

// key tells whether two rules give the same fact: the same kind and family.
func (r *RuleResult) key() string {
	return string(r.Kind) + "/" + r.Facets["family"]
}

// decision is the outcome of the rule table for one edge.
type decision struct {
	rule *NormalizationRule
	// ambiguity is the reason when no single rule decides.
	ambiguity string
}

// decide finds the rule of an edge among the rules of its native type. It
// gives an ambiguity (and no rule) when a discriminator is missing or
// unlisted, when discriminators point to different facts, or when several
// rules match with different results.
func decide(rules []*NormalizationRule, input *matchInput) decision {
	if reason := conflict(rules, input); reason != "" {
		return decision{ambiguity: reason}
	}

	var chosen *NormalizationRule

	for _, rule := range rules {
		if !rule.Discriminators.matches(input) {
			continue
		}

		switch {
		case chosen == nil:
			chosen = rule
		case chosen.Result.key() != rule.Result.key():
			return decision{ambiguity: "conflicting rules " + chosen.Name + " and " + rule.Name}
		}
	}

	if chosen == nil {
		return decision{ambiguity: "no rule matches " + describeEdge(input)}
	}

	return decision{rule: chosen}
}

// discriminatorSelection is one discriminator property of an edge, with the facts that its value selects.
type discriminatorSelection struct {
	name, value string
	facts       []string
}

// sharesFact says whether the two selections select at least one fact in common.
func (s *discriminatorSelection) sharesFact(other *discriminatorSelection) bool {
	return slices.ContainsFunc(s.facts, func(f string) bool { return slices.Contains(other.facts, f) })
}

func (s *discriminatorSelection) describe() string {
	return s.name + "=" + s.value + " (" + strings.Join(s.facts, ", ") + ")"
}

// factsSelectedBy gives the facts of the rules that list the value for the discriminator property, without duplicates.
func factsSelectedBy(rules []*NormalizationRule, name, value string) []string {
	var facts []string

	for _, rule := range rules {
		key := rule.Result.key()
		if slices.Contains(rule.Discriminators.Properties[name], value) && !slices.Contains(facts, key) {
			facts = append(facts, key)
		}
	}

	return facts
}

// discriminatorSelections gives the discriminator properties of the edge that select a fact.
func discriminatorSelections(rules []*NormalizationRule, input *matchInput) []discriminatorSelection {
	var selections []discriminatorSelection

	for _, name := range discriminatorProperties {
		value := input.props[name]
		if value == "" {
			continue
		}

		if facts := factsSelectedBy(rules, name, value); len(facts) > 0 {
			selections = append(selections, discriminatorSelection{name: name, value: value, facts: facts})
		}
	}

	return selections
}

// conflict gives the conflict of two discriminator properties of the edge that
// select different facts (for example `via=terraform` and `ref=env`), or "".
func conflict(rules []*NormalizationRule, input *matchInput) string {
	selections := discriminatorSelections(rules, input)

	for i := range selections {
		for j := i + 1; j < len(selections); j++ {
			if !selections[i].sharesFact(&selections[j]) {
				return "conflicting discriminators " + selections[i].describe() + " and " + selections[j].describe()
			}
		}
	}

	return ""
}

// describeEdge names the discriminators of an edge for the reason of an ambiguity.
func describeEdge(input *matchInput) string {
	parts := make([]string, 0, len(discriminatorProperties)+3)

	for _, name := range discriminatorProperties {
		if v := input.props[name]; v != "" {
			parts = append(parts, name+"="+v)
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "no discriminator")
	}

	parts = append(parts, "source "+string(input.sourceClass))

	if input.sourceFile != "" {
		parts = append(parts, "file "+input.sourceFile)
	}

	if input.targetKind != "" {
		parts = append(parts, "target kind "+input.targetKind)
	}

	return strings.Join(parts, ", ")
}
