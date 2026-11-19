package deployment

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
)

// workloadStep lists the workloads of the subject in the environment with their hosting targets and the deployment
// actions that name their manifests. With no workload it carries the gap that says why.
func (b *traceBuilder) workloadStep(env Environment, subject string, workloads, unresolved []*Workload, record *observation.PredicateCoverage) StepTrace {
	step := StepTrace{
		Step: StepWorkload, Role: observation.RoleParsedSourceFact, Resolution: observation.ResolutionUnresolved,
		Entities: []Entity{}, Gaps: []Gap{}, Limits: []Limit{},
	}

	if len(workloads) == 0 {
		step.Gaps = append(step.Gaps, b.missingWorkloadGap(env, subject, unresolved, record))

		return step
	}

	step.Resolution = observation.ResolutionExplicitlyBound

	var targets, actions []Entity

	for _, workload := range workloads {
		step.Entities = append(step.Entities, workloadEntity(workload))
		step.Gaps = append(step.Gaps, workloadGaps(workload)...)

		for i := range workload.Placements {
			targets = append(targets, hostingEntity(&workload.Placements[i]))
		}

		actions = append(actions, b.actionEntities(env, workload)...)

		if workload.Ambiguous {
			step.Resolution = observation.ResolutionAmbiguous
		}
	}

	step.Entities = append(step.Entities, uniqueEntities(targets)...)
	step.Entities = append(step.Entities, uniqueEntities(actions)...)

	return step
}

func (b *traceBuilder) missingWorkloadGap(env Environment, subject string, unresolved []*Workload, record *observation.PredicateCoverage) Gap {
	if len(unresolved) > 0 {
		paths := make([]string, 0, len(unresolved))
		for _, workload := range unresolved {
			paths = append(paths, locationText(workload.Locations))
		}

		return Gap{
			Reason: ReasonEnvironmentUnresolved,
			Detail: fmt.Sprintf("%s has a bound workload in no environment source (%s); it cannot be placed in %s", subject, strings.Join(sortedStrings(paths), ", "), env.ID),
		}
	}

	if gap := coverageGap(record, "the workload set of "+env.ID); gap != nil {
		return *gap
	}

	return Gap{Reason: ReasonAbsent, Detail: fmt.Sprintf("no workload is bound to %s in %s, and the workload set of %s is complete", subject, env.ID, env.ID)}
}

func locationText(locations []Location) string {
	texts := make([]string, 0, len(locations))

	for _, loc := range locations {
		if loc.Line > 0 {
			texts = append(texts, fmt.Sprintf("%s:%d", loc.Path, loc.Line))
		} else {
			texts = append(texts, loc.Path)
		}
	}

	return strings.Join(texts, ", ")
}

func workloadEntity(workload *Workload) Entity {
	properties := map[string]string{propKind: workload.Kind, propName: workload.Name, propAPIGroup: workload.APIGroup}

	if workload.Namespace != observation.UnresolvedPart {
		properties[propNamespace] = workload.Namespace
	}

	if workload.Templated {
		properties[propTemplated] = "true"
	}

	return Entity{
		Identity: workload.Identity, Kind: KindWorkload, Locations: slices.Clone(workload.Locations),
		Properties: props(properties), Resolution: observation.ResolutionExplicitlyBound, Bindings: slices.Clone(workload.BindingIDs),
	}
}

func workloadGaps(workload *Workload) []Gap {
	var gaps []Gap

	if workload.Competing {
		gaps = append(gaps, Gap{
			Reason: ReasonEvidenceConflict,
			Detail: fmt.Sprintf("%s/%s is declared in %d places (%s); none of them wins", workload.Kind, workload.Name, len(workload.Locations), locationText(workload.Locations)),
		})
	}

	if workload.Ambiguous {
		gaps = append(gaps, Gap{
			Reason: ReasonAmbiguousBinding,
			Detail: fmt.Sprintf("bindings map %s/%s to several architecture nodes: %s", workload.Kind, workload.Name, strings.Join(workload.Subjects, ", ")),
		})
	}

	if workload.Templated {
		gaps = append(gaps, Gap{
			Reason: ReasonCoverageIncomplete, LossModes: []string{observation.LossUnrenderedTemplate},
			Detail: fmt.Sprintf("%s/%s is not rendered (%s): its name or namespace is a template", workload.Kind, workload.Name, locationText(workload.Locations)),
		})
	}

	return gaps
}

func hostingEntity(target *HostingTarget) Entity {
	return Entity{
		Identity: target.Identity, Kind: KindHostingTarget, Resolution: observation.ResolutionExact,
		Locations: slices.Clone(target.Locations), Properties: props(map[string]string{propKind: "Namespace", propName: target.Name}),
	}
}

// actionEntities gives the deployment actions of the environment that name a manifest of the workload. An action
// that names no manifest of the workload is not shown: Concordia does not say which workloads an action applies.
func (b *traceBuilder) actionEntities(env Environment, workload *Workload) []Entity {
	var out []Entity

	for i := range b.view.Actions {
		action := &b.view.Actions[i]
		if action.Environment != env.ID {
			continue
		}

		if !slices.ContainsFunc(workload.Locations, func(l Location) bool { return action.Covers(l.Path) }) {
			continue
		}

		out = append(out, actionEntity(action))
	}

	return out
}

func actionEntity(action *Action) Entity {
	return Entity{
		Identity: action.Tool + ":" + action.Target, Kind: KindDeploymentAction, Resolution: observation.ResolutionExact,
		Locations:  sortedLocations([]Location{action.Location}),
		Properties: props(map[string]string{propTool: action.Tool, propTarget: action.Target, propTargetKind: action.TargetKind}),
	}
}

func uniqueEntities(list []Entity) []Entity {
	sortEntities(list)

	return slices.CompactFunc(list, func(a, c Entity) bool { return a.Kind == c.Kind && a.Identity == c.Identity })
}

func sortEntities(list []Entity) {
	sort.SliceStable(list, func(i, j int) bool { return compareEntities(&list[i], &list[j]) < 0 })
}

func compareEntities(a, c *Entity) int {
	return cmp.Or(canonjson.CompareUTF16(a.Kind, c.Kind), canonjson.CompareUTF16(a.Identity, c.Identity), canonjson.CompareUTF16(a.Properties[propRepository], c.Properties[propRepository]))
}
