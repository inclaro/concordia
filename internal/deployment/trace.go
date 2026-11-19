package deployment

import (
	"cmp"
	"maps"
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/observation"
)

// BuildTrace builds the `deploymentTrace` section: one trace for each trace subject and each selected environment,
// the unbound workloads of each environment, the workloads with no environment, and the summary. The status of a
// trace comes from the evidence and the coverage records only; the rule results are listed next to it and never
// change it. With no declared environment the section is `not-configured` and has no trace.
func BuildTrace(input *Input) *Trace {
	trace := &Trace{
		Status: StatusNotConfigured, Levels: Levels(), Environments: []EnvironmentTrace{},
		UnresolvedEnvironmentWorkloads: []UnresolvedWorkload{}, UnresolvedEnvironmentActions: []Entity{}, Summary: newSummary(),
	}

	if input == nil || len(input.Declared) == 0 {
		return trace
	}

	trace.Status = StatusConfigured
	builder := newTraceBuilder(input)

	for _, env := range input.environments() {
		trace.Environments = append(trace.Environments, builder.environment(env))
	}

	slices.SortFunc(trace.Environments, func(a, b EnvironmentTrace) int { return canonjson.CompareUTF16(a.Name, b.Name) })

	trace.UnresolvedEnvironmentWorkloads = builder.unresolvedWorkloads()
	trace.UnresolvedEnvironmentActions = builder.unresolvedActions(input.Declared)
	trace.Summary = summarize(trace.Environments)
	trace.Levels = levelsOf(trace.Environments)

	return trace
}

func newSummary() Summary {
	byStatus := map[Status]int{}
	for _, status := range Statuses() {
		byStatus[status] = 0
	}

	return Summary{ByStatus: byStatus}
}

func summarize(environments []EnvironmentTrace) Summary {
	summary := newSummary()
	summary.Environments = len(environments)

	for i := range environments {
		for j := range environments[i].Subjects {
			summary.Subjects++
			summary.ByStatus[environments[i].Subjects[j].Status]++
		}
	}

	return summary
}

// traceBuilder holds what the traces of a run share.
type traceBuilder struct {
	input      *Input
	view       *View
	coverage   map[coverageKey]*observation.PredicateCoverage
	subjectSet map[string]struct{}
	subjects   []string
}

type coverageKey struct{ predicate, project, environment string }

func newTraceBuilder(input *Input) *traceBuilder {
	builder := &traceBuilder{
		input: input, view: Extract(input), coverage: map[coverageKey]*observation.PredicateCoverage{},
		subjects: traceSubjects(input), subjectSet: map[string]struct{}{},
	}

	for _, subject := range builder.subjects {
		builder.subjectSet[subject] = struct{}{}
	}

	for i := range input.Coverage {
		rec := &input.Coverage[i]
		builder.coverage[coverageKey{rec.Predicate, rec.Scope.Project, rec.Scope.Environment}] = rec
	}

	return builder
}

// environment builds the record of one declared environment.
func (b *traceBuilder) environment(env Environment) EnvironmentTrace {
	record := b.recordOf(observation.FamilyDeploymentWorkloadSet, env.ID)

	out := EnvironmentTrace{
		Name: env.ID, Cluster: env.Cluster, Coverage: coverageStateOf(record),
		Subjects: make([]SubjectTrace, 0, len(b.subjects)), UnboundWorkloads: []UnboundWorkload{},
		Actions: b.environmentActions(env), Evaluated: b.input.evaluatedState(env.ID),
	}

	for _, subject := range b.subjects {
		out.Subjects = append(out.Subjects, b.subject(env, subject, record))
	}

	for i := range b.view.Workloads {
		workload := &b.view.Workloads[i]
		if workload.Environment == env.ID && !b.attached(workload) {
			out.UnboundWorkloads = append(out.UnboundWorkloads, b.unbound(env, workload))
		}
	}

	return out
}

// attached says whether a trace subject owns the workload.
func (b *traceBuilder) attached(workload *Workload) bool {
	return slices.ContainsFunc(workload.Subjects, func(subject string) bool {
		_, ok := b.subjectSet[subject]

		return ok
	})
}

func (b *traceBuilder) unresolvedWorkloads() []UnresolvedWorkload {
	out := []UnresolvedWorkload{}

	for i := range b.view.Workloads {
		workload := &b.view.Workloads[i]
		if workload.EnvironmentResolved() {
			continue
		}

		out = append(out, UnresolvedWorkload{Identity: workload.Identity, Kind: workload.Kind, Locations: workload.Locations})
	}

	return out
}

// subject builds the trace of one subject in one environment.
func (b *traceBuilder) subject(env Environment, subject string, record *observation.PredicateCoverage) SubjectTrace {
	workloads, unresolved := b.workloadsOf(env, subject)
	occurrences := b.occurrencesOf(env, workloads)
	builds, buildStep := b.buildDefinitionStep(env, occurrences)

	steps := []StepTrace{
		b.serviceStep(subject),
		b.workloadStep(env, subject, workloads, unresolved, record),
		b.imageStep(env, workloads),
		buildStep,
		b.sourceStep(subject, workloads, builds),
	}

	trace := SubjectTrace{
		ArchitectureID: subject, Environment: env.ID, Level: LevelDeclared, Steps: steps,
		Gaps: []Gap{}, Limits: []Limit{}, Rules: b.rulesOf(env, subject), Evaluated: b.input.evaluatedEntities(env.ID, subject),
	}

	for i := range steps {
		for _, gap := range steps[i].Gaps {
			gap.Step = steps[i].Step
			trace.Gaps = append(trace.Gaps, gap)
		}

		trace.Limits = mergeLimits(trace.Limits, steps[i].Limits)
	}

	trace.Status = statusOf(workloads, &steps[1], &trace)

	return trace
}

// statusOf gives the status of a trace (deployment-trace spec, "Trace status by evidence").
func statusOf(workloads []*Workload, workloadStep *StepTrace, trace *SubjectTrace) Status {
	if len(workloads) == 0 {
		// A step with no entity has the gap that says why: a definite absence is unresolved, anything else is unknown.
		if slices.ContainsFunc(workloadStep.Gaps, func(g Gap) bool { return g.Reason == ReasonAbsent }) {
			return StatusUnresolved
		}

		return StatusUnknown
	}

	if len(trace.Gaps) > 0 {
		return StatusPartial
	}

	return StatusComplete
}

func mergeLimits(into, more []Limit) []Limit {
	for _, limit := range more {
		if !slices.ContainsFunc(into, func(l Limit) bool { return l.ID == limit.ID }) {
			into = append(into, limit)
		}
	}

	return into
}

// workloadsOf gives the workloads of the environment that the bindings map to the subject, and the workloads with
// no environment that they map to it (they take part in no environment trace).
func (b *traceBuilder) workloadsOf(env Environment, subject string) (inEnvironment, unresolved []*Workload) {
	for i := range b.view.Workloads {
		workload := &b.view.Workloads[i]
		if !slices.Contains(workload.Subjects, subject) {
			continue
		}

		switch {
		case workload.Environment == env.ID:
			inEnvironment = append(inEnvironment, workload)
		case !workload.EnvironmentResolved():
			unresolved = append(unresolved, workload)
		}
	}

	return inEnvironment, unresolved
}

func (b *traceBuilder) rulesOf(env Environment, subject string) []RuleLink {
	out := []RuleLink{}

	for _, link := range b.input.Rules {
		if link.Subject == subject && link.Environment == env.ID {
			out = append(out, link)
		}
	}

	slices.SortFunc(out, func(a, c RuleLink) int {
		return cmp.Or(canonjson.CompareUTF16(a.RuleID, c.RuleID), canonjson.CompareUTF16(a.ObligationID, c.ObligationID))
	})

	return out
}

// serviceStep is the logical service: declared intent of the model.
func (b *traceBuilder) serviceStep(subject string) StepTrace {
	entity := Entity{
		Identity: subject, Kind: KindService, Resolution: observation.ResolutionExact, Locations: []Location{},
		Properties: props(map[string]string{propNodeType: "", propName: ""}),
	}

	if b.input.Model != nil {
		for i := range b.input.Model.Nodes {
			if node := &b.input.Model.Nodes[i]; node.ID == subject {
				entity.Properties = props(map[string]string{propNodeType: node.NodeType, propName: node.Name})
			}
		}
	}

	return StepTrace{
		Step: StepService, Role: observation.RoleDeclaredIntent, Resolution: observation.ResolutionExact,
		Entities: []Entity{entity}, Gaps: []Gap{},
		Limits: []Limit{{ID: LimitDeclaredLevel, Detail: "The trace shows the configuration as it is declared in manifests and Dockerfiles. It does not show rendered output, how a deployment was carried out, or the state of a cluster."}},
	}
}

// occurrence is a container occurrence of a workload of the trace.
type occurrence struct {
	workload *Workload
	ContainerOccurrence
}

func (b *traceBuilder) occurrencesOf(_ Environment, workloads []*Workload) []occurrence {
	var out []occurrence

	for _, workload := range workloads {
		for i := range workload.Occurrences {
			out = append(out, occurrence{workload: workload, ContainerOccurrence: workload.Occurrences[i]})
		}
	}

	return out
}

// repositoriesOf gives the sorted distinct repositories of the resolved occurrences.
func repositoriesOf(occurrences []occurrence) []string {
	set := map[string]struct{}{}

	for i := range occurrences {
		if !occurrences[i].Unresolved {
			set[occurrences[i].Image.Repository] = struct{}{}
		}
	}

	return sortedStrings(slices.Collect(maps.Keys(set)))
}

// environmentActions gives the deployment actions that the sources of the environment hold. They are shown whether
// or not a workload of a subject is in the file that the action names.
func (b *traceBuilder) environmentActions(env Environment) []Entity {
	out := []Entity{}

	for i := range b.view.Actions {
		if action := &b.view.Actions[i]; action.Environment == env.ID {
			out = append(out, actionEntity(action))
		}
	}

	return uniqueEntities(out)
}

// unresolvedActions gives the deployment actions whose location belongs to no declared environment.
func (b *traceBuilder) unresolvedActions(declared []Environment) []Entity {
	out := []Entity{}

	for i := range b.view.Actions {
		action := &b.view.Actions[i]
		if !slices.ContainsFunc(declared, func(env Environment) bool { return env.ID == action.Environment }) {
			out = append(out, actionEntity(action))
		}
	}

	return uniqueEntities(out)
}
