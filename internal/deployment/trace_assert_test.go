package deployment_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
)

// traceWantEqual reports a difference between a value of a trace and the expected one.
func traceWantEqual[T any](t *testing.T, what string, got, want T) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// traceWantSlice reports a difference between two lists; a nil list and an empty one are the same.
func traceWantSlice[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %+v, want %+v", what, got, want)
	}
}

// traceWantContains reports a list that lacks the wanted item.
func traceWantContains[T comparable](t *testing.T, what string, list []T, want T) {
	t.Helper()

	if !slices.Contains(list, want) {
		t.Errorf("%s = %+v lacks %+v", what, list, want)
	}
}

// traceProject gives one value for each item.
func traceProject[T, U any](items []T, project func(*T) U) []U {
	out := make([]U, 0, len(items))

	for i := range items {
		out = append(out, project(&items[i]))
	}

	return out
}

func firstPathOf(locations []deployment.Location) string {
	if len(locations) == 0 {
		return ""
	}

	return locations[0].Path
}

func firstLocationOf(locations []deployment.Location) deployment.Location {
	if len(locations) == 0 {
		return deployment.Location{}
	}

	return locations[0]
}

// firstLocationsOf gives the first location of each entity.
func firstLocationsOf(entities []deployment.Entity) []deployment.Location {
	return traceProject(entities, func(e *deployment.Entity) deployment.Location { return firstLocationOf(e.Locations) })
}

// firstPathsOf gives the path of the first location of each entity.
func firstPathsOf(entities []deployment.Entity) []string {
	return traceProject(entities, func(e *deployment.Entity) string { return firstPathOf(e.Locations) })
}

// allLocationsOf gives the locations of all entities.
func allLocationsOf(entities []deployment.Entity) []deployment.Location {
	var out []deployment.Location

	for i := range entities {
		out = append(out, entities[i].Locations...)
	}

	return out
}

func identitiesOf(entities []deployment.Entity) []string {
	return traceProject(entities, func(e *deployment.Entity) string { return e.Identity })
}

func lineagesOf(entities []deployment.Entity) []deployment.Lineage {
	return traceProject(entities, func(e *deployment.Entity) deployment.Lineage { return e.Lineage })
}

// propertiesOf gives the value of a property of each entity.
func propertiesOf(entities []deployment.Entity, key string) []string {
	return traceProject(entities, func(e *deployment.Entity) string { return e.Properties[key] })
}

// hasPropertyOf says for each entity whether it has a value of the property.
func hasPropertyOf(entities []deployment.Entity, key string) []bool {
	return traceProject(entities, func(e *deployment.Entity) bool { return e.Properties[key] != "" })
}

func limitIDsOf(limits []deployment.Limit) []string {
	return traceProject(limits, func(l *deployment.Limit) string { return l.ID })
}

// firstLossModes gives the loss modes of the first gap, nil when there is none.
func firstLossModes(gaps []deployment.Gap) []string {
	if len(gaps) == 0 {
		return nil
	}

	return gaps[0].LossModes
}

func stepsOf(sub *deployment.SubjectTrace) []deployment.Step {
	return traceProject(sub.Steps, func(s *deployment.StepTrace) deployment.Step { return s.Step })
}

// entityCountOf gives the number of entities of the kind in the step of the subject.
func entityCountOf(t *testing.T, sub *deployment.SubjectTrace, step deployment.Step, kind string) int {
	t.Helper()

	return len(entitiesOf(stepOf(t, sub, step), kind))
}

func reasonsOf(gaps []deployment.Gap) []deployment.Reason {
	return traceProject(gaps, func(g *deployment.Gap) deployment.Reason { return g.Reason })
}

// firstEntityOf gives the first entity of the step, a zero entity when there is none.
func firstEntityOf(step *deployment.StepTrace) *deployment.Entity {
	if len(step.Entities) == 0 {
		return &deployment.Entity{}
	}

	return &step.Entities[0]
}
