package adr

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diffs"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// Architecture change kinds of a trigger (the `architecture` list).
const (
	kindNodeAdded           = "node-added"
	kindNodeRemoved         = "node-removed"
	kindRelationshipAdded   = "relationship-added"
	kindRelationshipRemoved = "relationship-removed"
)

// ChangeSet is the frozen change set of a `check --changed` run.
type ChangeSet struct {
	// Base is the Git reference the change set is measured against.
	Base string
	// Files are the changed files, prefixed with the project prefix where the run has one, sorted.
	Files []string
}

// triggerRun is what every trigger of one EvaluateTriggers call is judged against: the change set, the architecture
// change from base to current (when it can be computed), and the register with its committed decision paths.
type triggerRun struct {
	change        *ChangeSet
	base, current *intent.ArchitectureModel
	reg           *Register
	decisionPaths map[string]string
	// archReason is why the architecture change is unknown; empty when it is known (or there is no change set).
	archReason string
	diff       diffs.IntentDiff
}

// EvaluateTriggers evaluates the configured triggers against a change set. The results are sorted by trigger ID.
//
// A trigger is raised when a changed file matches `paths`, or when the architecture change from base to current
// has a listed kind (narrowed by `nodeTypes`/`tags`; a relationship matches when one of its endpoints does). It is
// satisfied when a registered decision whose categories contain the trigger category has its committed file (the
// decisionPaths value of its EntryKey) in the change set: `pass`, naming the decisions. Raised and unsatisfied is a
// `violation`. A trigger that is not raised is a `pass` with Raised false (no ADR is required). Without a change set
// every trigger is `not-applicable`. When base cannot be loaded (baseErr or nil base) or current is nil, the
// architecture part is unknown: a raise by paths still decides, otherwise the result is `unknown` with a reason.
func EvaluateTriggers(
	triggers []workspace.ADRTrigger, change *ChangeSet, base *intent.ArchitectureModel, baseErr error,
	current *intent.ArchitectureModel, reg *Register, decisionPaths map[string]string,
) []conformance.TriggerResult {
	run := triggerRun{change: change, base: base, current: current, reg: reg, decisionPaths: decisionPaths}
	run.loadArchitectureDiff(baseErr)

	results := make([]conformance.TriggerResult, 0, len(triggers))
	for i := range triggers {
		results = append(results, run.evaluate(&triggers[i]))
	}

	slices.SortStableFunc(results, func(a, b conformance.TriggerResult) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	return results
}

// loadArchitectureDiff computes the architecture change, or records why it is unknown. Without a change set there is
// nothing to compute.
func (r *triggerRun) loadArchitectureDiff(baseErr error) {
	if r.change == nil {
		return
	}

	switch {
	case baseErr != nil:
		r.archReason = "base model unreadable: " + baseErr.Error()
	case r.base == nil:
		r.archReason = "base model unavailable"
	case r.current == nil:
		r.archReason = "current model unavailable"
	default:
		r.diff = diffs.Intent(r.base, r.current)
	}
}

// evaluate gives the result of one trigger.
func (r *triggerRun) evaluate(trig *workspace.ADRTrigger) conformance.TriggerResult {
	res := conformance.TriggerResult{
		ID: trig.ID, Category: trig.Category, Result: string(conformance.VerdictNotApplicable),
		RaisedBy: []string{}, SatisfiedBy: []string{},
	}

	if r.change == nil {
		res.Reason = "no change set"

		return res
	}

	raisedBy, unknownArch := r.raisedBy(trig)

	slices.SortFunc(raisedBy, canonjson.CompareUTF16)

	res.RaisedBy = slices.Compact(raisedBy)
	res.Raised = len(res.RaisedBy) > 0

	switch {
	case res.Raised:
		r.judgeRaised(&res, trig.Category)
	case unknownArch:
		res.Result = string(conformance.VerdictUnknown)
		res.Reason = r.archReason
	default:
		res.Result = string(conformance.VerdictPass)
	}

	return res
}

// raisedBy gives what raises the trigger, and whether its architecture part is unknown.
func (r *triggerRun) raisedBy(trig *workspace.ADRTrigger) (raisedBy []string, unknownArch bool) {
	raisedBy = matchPaths(trig.Paths, r.change.Files)

	if len(trig.Architecture) == 0 {
		return raisedBy, false
	}

	if r.archReason != "" {
		return raisedBy, true
	}

	return append(raisedBy, matchArchitecture(trig, &r.diff, r.base, r.current)...), false
}

// judgeRaised sets the verdict of a raised trigger: satisfied by the registered decisions of its category, or a
// violation.
func (r *triggerRun) judgeRaised(res *conformance.TriggerResult, category string) {
	res.SatisfiedBy = satisfiedBy(category, r.change, r.reg, r.decisionPaths)
	if len(res.SatisfiedBy) > 0 {
		res.Result = string(conformance.VerdictPass)

		return
	}

	res.Result = string(conformance.VerdictViolation)
	res.Reason = string(ReasonTriggerUnsatisfied)
}

// matchPaths gives the changed files that match one of the globs.
func matchPaths(globs, files []string) []string {
	out := []string{}

	if len(globs) == 0 {
		return out
	}

	match := fsutil.GlobMatcher(globs)

	for _, f := range files {
		if match(f) {
			out = append(out, f)
		}
	}

	return out
}

// triggerSide is one direction of an architecture change: the model of the side that has the declaration (current
// for added, base for removed) and the change kinds of that direction.
type triggerSide struct {
	model             *intent.ArchitectureModel
	nodeKind, relKind string
}

// kindOf gives the change kind of a declaration change type; false for a type that a trigger cannot list.
func (s triggerSide) kindOf(changeType string) (string, bool) {
	switch changeType {
	case "relationship":
		return s.relKind, true
	case "node":
		return s.nodeKind, true
	default:
		return "", false
	}
}

// keeps says whether the narrowing set keeps the declaration: a node is in the set, a relationship touches it.
func (s triggerSide) keeps(set map[string]bool, kind, id string) bool {
	if kind == s.nodeKind {
		return set[id]
	}

	return relationshipTouches(s.model, id, set)
}

// matchArchitecture gives `<kind>:<id>` for each declaration change that the trigger lists and its narrowing keeps.
func matchArchitecture(trig *workspace.ADRTrigger, diff *diffs.IntentDiff, base, current *intent.ArchitectureModel) []string {
	added := triggerSide{model: current, nodeKind: kindNodeAdded, relKind: kindRelationshipAdded}
	removed := triggerSide{model: base, nodeKind: kindNodeRemoved, relKind: kindRelationshipRemoved}

	return append(matchChanges(trig, diff.Added, added), matchChanges(trig, diff.Removed, removed)...)
}

// narrowingSet resolves the `nodeTypes`/`tags` narrowing of the trigger in the model. It is nil when the trigger
// does not narrow, and a (possibly empty) non-nil set when it does.
func narrowingSet(trig *workspace.ADRTrigger, model *intent.ArchitectureModel) map[string]bool {
	if len(trig.NodeTypes) == 0 && len(trig.Tags) == 0 {
		return nil
	}

	set := map[string]bool{}
	for _, id := range policy.ResolveNodeSelector(model, nil, trig.NodeTypes, trig.Tags) {
		set[id] = true
	}

	return set
}

// matchChanges keeps the node and relationship changes of one direction that the trigger lists. Narrowing by
// nodeTypes/tags uses the model of the side that has the declaration (current for added, base for removed).
func matchChanges(trig *workspace.ADRTrigger, changes []diffs.Change, side triggerSide) []string {
	var out []string

	set := narrowingSet(trig, side.model)

	for _, chg := range changes {
		kind, ok := side.kindOf(chg.Type)
		if !ok || !slices.Contains(trig.Architecture, kind) {
			continue
		}

		if set == nil || side.keeps(set, kind, chg.ID) {
			out = append(out, kind+":"+chg.ID)
		}
	}

	return out
}

// relationshipTouches reports whether an endpoint (source, container or target) of the relationship is in set.
func relationshipTouches(model *intent.ArchitectureModel, id string, set map[string]bool) bool {
	for i := range model.Relationships {
		rel := &model.Relationships[i]
		if rel.ID != id {
			continue
		}

		if set[rel.Source] || set[rel.Container] {
			return true
		}

		return slices.ContainsFunc(rel.Targets, func(t string) bool { return set[t] })
	}

	return false
}

// satisfiedBy gives the sorted canonical IDs of the registered decisions that declare the category and have
// their committed file in the change set.
func satisfiedBy(category string, change *ChangeSet, reg *Register, decisionPaths map[string]string) []string {
	out := []string{}

	if reg == nil {
		return out
	}

	changed := map[string]bool{}
	for _, f := range change.Files {
		changed[f] = true
	}

	for i := range reg.Entries {
		entry := &reg.Entries[i]
		if !slices.Contains(entry.Categories, category) {
			continue
		}

		if path := decisionPaths[EntryKey(entry)]; path != "" && changed[path] {
			out = append(out, entry.ID)
		}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return slices.Compact(out)
}
