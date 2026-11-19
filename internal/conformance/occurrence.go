package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/workspace"
)

// TypedContext is what the evaluation of typed rules needs besides the evidence and the bindings: the
// workspace environments, the run selection, the loaded bindings, the capability state, and the precedence lists.
// The zero value declares no environment and has no regression, no family state, and no precedence.
type TypedContext struct {
	// Workspace gives the declared environments, and the projects that name a repository. nil: none.
	Workspace *workspace.Workspace
	// Environments is the environment selection of the run. nil selects all declared environments.
	Environments *RunEnvironments
	// Bindings are the loaded bindings. Results record the ID, status, and authority of the bindings that they used.
	Bindings *binding.Loaded
	// Regressions are the families that the lock profile supported and this run does not (observation.CapabilityRegressions).
	Regressions []observation.CapabilityRegression
	// Families is the support of each family in this run (observation.Capabilities.Families). nil: the coverage records decide.
	Families map[string]observation.FamilyState
	// Precedences are the `precedence` lists of the policy packs, in pack order.
	Precedences []policy.NamedPrecedence
	// Conflicts are the groups of competing declarations of one resource identity (observation.Conflicts). The
	// entities of a certain group (same environment and namespace) are not instances and no declaration wins: an
	// obligation that is anchored on one of them is `unknown` with the reason `evidence-conflict`.
	Conflicts []observation.Conflict
}

// OccurrenceClass says how an observed fact (or the absence of one) relates to an obligation.
type OccurrenceClass string

// Occurrence classes.
const (
	// OccurrenceMatching is a fact of the relation of the obligation: the relation kind, the direction, the target, and the environment.
	OccurrenceMatching OccurrenceClass = "matching"
	// OccurrenceAbsent is the absence of the necessary relation (`required`), or of any forbidden relation (`forbidden`).
	OccurrenceAbsent OccurrenceClass = "absent"
	// OccurrenceWrongDirection is a fact of the relation that runs in the reverse direction. It never satisfies the obligation.
	OccurrenceWrongDirection OccurrenceClass = "wrong-direction"
	// OccurrenceWrongEnvironment is a fact of the relation in an environment that the rule does not cover. It never satisfies the obligation.
	OccurrenceWrongEnvironment OccurrenceClass = "wrong-environment"
	// OccurrenceRoleMismatch is a fact whose evidence role the rule does not permit. It never satisfies the obligation.
	OccurrenceRoleMismatch OccurrenceClass = "role-mismatch"
	// OccurrenceNotPermitted is an observed relation that no rule of the pair allows or forbids and that the
	// allowed relation of the pair does not cover.
	OccurrenceNotPermitted OccurrenceClass = "not-permitted"
	// OccurrenceBindingGap is a fact with an endpoint that no binding maps to a CALM element.
	OccurrenceBindingGap OccurrenceClass = "binding-gap"
	// OccurrenceConflict is a set of facts of one role that disagree about one property.
	OccurrenceConflict OccurrenceClass = "evidence-conflict"
)

// BindingRef names a binding that a result relied on, with the status and the authority that it declares.
type BindingRef struct {
	ID        string `json:"id"`
	Status    string `json:"status,omitempty"`
	Authority string `json:"authority,omitempty"`
}

// OccurrenceResult is the result of one evidence occurrence (or of one absence) for one obligation. Two
// occurrences on one pair of endpoints give two results; a summary by pair never replaces them.
type OccurrenceResult struct {
	// bound is the fact. nil for an absence.
	bound *BoundEdge
	// key is the identity of the fact (observation.EdgeKey), used to say that some obligation decided it.
	key string
	// Edge is the fact, without its endpoints.
	Edge *EdgeRef `json:"edge,omitempty"`
	// Source and Target are the endpoints of the fact.
	Source   *ObservedRef `json:"source,omitempty"`
	Target   *ObservedRef `json:"target,omitempty"`
	Location *Location    `json:"location,omitempty"`
	// Bindings are the bindings that bound the subject-side entity: the evidence of the binding, its status, and its authority.
	Bindings []BindingRef `json:"bindings,omitempty"`
	// Anchor is the observed entity of the subject (a workload, for a `*-workload` direction). Empty for an absence with no entity.
	Anchor       string `json:"anchor,omitempty"`
	ObligationID string `json:"obligationId"`
	// Result is `pass`, `violation`, `unknown`, or `not-applicable`.
	Result RuleVerdict `json:"result"`
	// Reason is the one reason of an `unknown` result, from the closed set. Empty for the others.
	Reason  UnknownReason   `json:"reason,omitempty"`
	Class   OccurrenceClass `json:"class"`
	Subject string          `json:"subject"`
	// Environment is the environment of the obligation.
	Environment string                        `json:"environment,omitempty"`
	Kind        observation.CanonicalEdgeKind `json:"kind"`
	// Role is the evidence role of the fact.
	Role observation.EvidenceRole `json:"role,omitempty"`
	// Occurrence is the occurrence discriminator of the fact, for example a container name.
	Occurrence string `json:"occurrence,omitempty"`
	// Detail says why: what blocks a definite result, or what the fact is.
	Detail string `json:"detail,omitempty"`
	// NextAction is `resolve binding`, `supply evidence`, `fix analysis configuration`, or `fix implementation`.
	NextAction string `json:"nextAction,omitempty"`
	// Sources lists the places of the facts that disagree (class evidence-conflict).
	Sources []Location `json:"sources,omitempty"`
	// Decisive is false for a result that the verdict of the obligation does not use: a fact that another fact of the
	// same entity outweighs (an unknown selector next to a fact that satisfies), or an entity that another satisfying
	// entity outweighs under agreement `any` or multiplicity `exists`.
	Decisive bool `json:"decisive"`
}

// ObligationResult is the verdict of one obligation and the results of the occurrences behind it.
type ObligationResult struct {
	// Verdict is the verdict of the obligation: the worst of its decisive occurrence results (error, violation,
	// unknown, pass); `not-applicable` when nothing is decisive.
	Verdict RuleVerdict `json:"verdict"`
	// Reason is the one reason code of an `unknown` obligation (the first of its decisive results in the order of the closed set).
	Reason UnknownReason `json:"reason,omitempty"`
	// Detail is the detail of the result that gave Reason, or the reason of an error.
	Detail string `json:"detail,omitempty"`
	// Occurrences are the results of each occurrence, in the order of the evidence. Never nil.
	Occurrences []OccurrenceResult `json:"occurrences"`
	// SupersededBy lists the precedence entries that placed another rule above this one for this obligation. The
	// verdict stays visible and the obligation is left out of the verdict of the rule and of the gate.
	SupersededBy []policy.Supersession `json:"supersededBy,omitempty"`
	// Enforcement is the mode of the rule in the environment of the obligation (EnforcementConfig.Resolve). The gates
	// read it: only a `mandatory` obligation counts.
	Enforcement policy.Enforcement `json:"enforcement,omitempty"`
	Obligation
}

// Superseded says whether a precedence list placed another rule above the rule of this obligation.
func (r *ObligationResult) Superseded() bool { return len(r.SupersededBy) > 0 }

// factSlot indexes the facts by kind and one endpoint.
type factSlot struct {
	kind observation.CanonicalEdgeKind
	id   string
}

// ObligationEvaluator evaluates obligations against the facts of one run. It indexes the facts by kind and
// endpoint once, so that the cost of an obligation does not grow with the number of facts of the run.
type ObligationEvaluator struct {
	input        *EvaluateInput
	typed        *TypedContext
	nodes        map[string]observation.ObservedNode
	from         map[factSlot][]*observation.EvidenceEdge
	to           map[factSlot][]*observation.EvidenceEdge
	anchors      map[string][]string
	conflicted   map[string]*observation.Conflict
	excluded     func(string) bool
	bindingsByID map[string]*binding.Binding
	regressions  map[string]observation.CapabilityRegression
	nodesByID    map[string]*intent.NodeDeclaration
	// imageSubjects are the CALM IDs that an active `image` binding names.
	imageSubjects map[string]bool
	declared      []string
}

// NewObligationEvaluator indexes the evidence of the input. The input is read, never changed.
func NewObligationEvaluator(input *EvaluateInput) *ObligationEvaluator {
	evaluator := &ObligationEvaluator{
		input: input, typed: &input.Typed, nodes: input.Evidence.Nodes,
		from: map[factSlot][]*observation.EvidenceEdge{}, to: map[factSlot][]*observation.EvidenceEdge{},
		anchors: map[string][]string{}, conflicted: map[string]*observation.Conflict{}, excluded: excludedFile(&input.ReconcileInput),
		bindingsByID: map[string]*binding.Binding{}, regressions: map[string]observation.CapabilityRegression{},
		nodesByID: map[string]*intent.NodeDeclaration{}, imageSubjects: map[string]bool{},
	}

	evaluator.indexFacts()
	evaluator.indexDeclarations()

	return evaluator
}

// EvaluateObligation evaluates one obligation of a typed plan against the evidence of the input: one
// OccurrenceResult for each admissible fact (or for the absence of one), and the verdict. A `not-permitted`
// relation (see OccurrenceNotPermitted) is found among the facts that no result of this obligation decided.
// Use ObligationEvaluator.EvaluateAll to evaluate the obligations of a run together.
func EvaluateObligation(input *EvaluateInput, obl *Obligation, plan *policy.RulePlan) ObligationResult {
	results := NewObligationEvaluator(input).EvaluateAll([]PlannedObligation{{Obligation: *obl, Plan: plan}})

	return results[0]
}

// PlannedObligation is an obligation with the plan of its rule.
type PlannedObligation struct {
	Plan *policy.RulePlan
	Obligation
}

// EvaluateAll evaluates the obligations together and gives the results in the same order. An `allowed`
// obligation also reports the observed relations of its pair that no obligation decides and that the allowed
// relation does not cover, because policy that allows one relation does not allow the others.
func (e *ObligationEvaluator) EvaluateAll(obligations []PlannedObligation) []ObligationResult {
	results := make([]ObligationResult, len(obligations))
	decided := map[string]struct{}{}

	for i := range obligations {
		results[i] = e.evaluate(&obligations[i].Obligation, obligations[i].Plan)
		collectDecidedFacts(&results[i], decided)
	}

	for i := range obligations {
		e.addNotPermitted(&obligations[i], &results[i], decided)
	}

	return results
}

// collectDecidedFacts adds the keys of the facts that a result decided to the set.
func collectDecidedFacts(result *ObligationResult, decided map[string]struct{}) {
	for j := range result.Occurrences {
		if occ := &result.Occurrences[j]; occ.key != "" && decidesFact(occ.Class) {
			decided[occ.key] = struct{}{}
		}
	}
}

// addNotPermitted adds the relations that an `allowed` obligation does not cover to its result, unless it failed.
func (e *ObligationEvaluator) addNotPermitted(planned *PlannedObligation, result *ObligationResult, decided map[string]struct{}) {
	if planned.Type != policy.ObligationAllowed || result.Verdict == VerdictError {
		return
	}

	if extra := e.notPermitted(&planned.Obligation, planned.Plan, decided); len(extra) > 0 {
		result.Occurrences = append(result.Occurrences, extra...)
		finish(result)
	}
}

// connectionKinds are the kinds of a connection between two entities. Policy that allows one connection
// relation does not allow the others between the same entities.
var connectionKinds = []observation.CanonicalEdgeKind{
	observation.KindCallsService, observation.KindCallsGRPC, observation.KindCallsGraphQL, observation.KindCallsTRPC,
	observation.KindConfiguredConnection, observation.KindRoutesTo, observation.KindDispatchesAsync, observation.KindDataFlows,
}

func isConnectionKind(kind observation.CanonicalEdgeKind) bool {
	return slices.Contains(connectionKinds, kind)
}

// decidesFact says whether a result of this class means that an obligation decided the fact that it shows. A fact
// that only appears as a reverse, other-environment, or not permitted relation is still undecided.
func decidesFact(class OccurrenceClass) bool {
	return class != OccurrenceWrongDirection && class != OccurrenceWrongEnvironment && class != OccurrenceNotPermitted
}

// workloadKinds are the Kubernetes kinds of a workload.
var workloadKinds = []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod"}

func isWorkload(n *observation.ObservedNode) bool {
	return n.Label == "Resource" && slices.Contains(workloadKinds, observation.ResourceKindOf(n))
}

// obligationRun is the evaluation of one obligation.
type obligationRun struct {
	e    *ObligationEvaluator
	ob   *Obligation
	plan *policy.RulePlan
	spec *policy.ObligationSpec
	// blocks are the claim gates by the completeness that the claim needs; a gate that is in the map was computed.
	blocks map[policy.Completeness]*claimBlock
	out    []OccurrenceResult
	// sawInapplicable says that the selector of the rule was false for a fact of the obligation.
	sawInapplicable bool
}

// claimBlock says why a claim (a pass or an absence) cannot be made.
type claimBlock struct {
	reason UnknownReason
	detail string
}

func (e *ObligationEvaluator) evaluate(obl *Obligation, plan *policy.RulePlan) ObligationResult {
	result := ObligationResult{Obligation: *obl, Occurrences: []OccurrenceResult{}}

	if plan == nil || plan.Obligation == nil {
		result.Verdict = VerdictError
		result.Detail = "the rule of obligation " + obl.ID + " has no obligation contract"

		return result
	}

	run := &obligationRun{e: e, ob: obl, plan: plan, spec: plan.Obligation}

	if obl.EnvironmentUndeclared {
		run.add(run.entity(OccurrenceAbsent, VerdictUnknown, ReasonEnvironmentUnresolved,
			fmt.Sprintf("the rule scope names the environment %q, which the workspace does not declare", obl.Environment)))
	} else {
		run.run()
	}

	result.Occurrences = run.out
	finish(&result)

	return result
}

// finish computes the verdict, the reason, and the detail from the decisive results.
func finish(result *ObligationResult) {
	var verdicts []RuleVerdict

	result.Reason, result.Detail = "", ""
	best := len(unknownReasons)

	for i := range result.Occurrences {
		occ := &result.Occurrences[i]
		if !occ.Decisive {
			continue
		}

		verdicts = append(verdicts, occ.Result)

		if occ.Result != VerdictUnknown {
			continue
		}

		if rank := slices.Index(unknownReasons, occ.Reason); rank >= 0 && rank < best {
			best = rank
			result.Reason, result.Detail = occ.Reason, occ.Detail
		}
	}

	result.Verdict = WorstVerdict(verdicts)

	if result.Verdict != VerdictUnknown {
		result.Reason, result.Detail = "", ""
	}
}

// entity makes a result for the subject itself: there is no fact to show.
func (r *obligationRun) entity(class OccurrenceClass, result RuleVerdict, reason UnknownReason, detail string) OccurrenceResult {
	occ := OccurrenceResult{
		ObligationID: r.ob.ID, Result: result, Reason: reason, Class: class, Subject: r.ob.Subject, Environment: r.ob.Environment,
		Kind: r.ob.Relation.Kind, Detail: detail, Decisive: true,
	}

	switch result {
	case VerdictViolation:
		occ.NextAction = nextActionFix
	case VerdictUnknown:
		occ.NextAction = nextActionFor(reason)
	case VerdictPass, VerdictNotApplicable, VerdictError:
	}

	if r.pointsAtModel(class, result) {
		occ.Location = &Location{Path: r.e.input.Model.SourceURI}
	}

	return occ
}

// pointsAtModel says whether the result of an absence shows the CALM model as its location.
func (r *obligationRun) pointsAtModel(class OccurrenceClass, result RuleVerdict) bool {
	return r.e.input.Model != nil && class == OccurrenceAbsent && result != VerdictNotApplicable
}

// Next actions of typed results (the vocabulary of the OUT-02 finding field).
const (
	nextActionFix      = "fix implementation"
	nextActionEvidence = "supply evidence"
	nextActionConfig   = "fix analysis configuration"
)

func nextActionFor(reason UnknownReason) string {
	switch reason {
	case ReasonBindingGap, ReasonAmbiguousBinding:
		return NextActionResolveBinding
	case ReasonLimitExceeded, ReasonUnsupportedFamily, ReasonCapabilityRegression, ReasonTrustAnchorUnreadable, ReasonVerificationUnavailable,
		ReasonClaimNotEnabled, ReasonNoEvaluatorAvailable, ReasonEnforcementNotDeclared, ReasonReviewedModeNotAvailable:
		// A claim that no contract enables, an evaluator that is not set up, an assumption that the workspace does not
		// declare, and a mode that needs `assessments` are each fixed in the workspace, not by more evidence.
		return nextActionConfig
	case ReasonCoverageIncomplete, ReasonEnvironmentUnresolved, ReasonEvidenceConflict, ReasonSelectorPropertyUnknown,
		ReasonTargetNotDeclared, ReasonReferenceUnresolved, ReasonNoEvidenceSupplied, ReasonRenderedEvidenceMissing:
		return nextActionEvidence
	}

	return nextActionEvidence
}

func (r *obligationRun) add(occ OccurrenceResult) { r.out = append(r.out, occ) } //nolint:gocritic // the results are built as values and kept in a slice of values.

// occurrenceOf makes a result for one fact. anchor is the observed entity of the subject.
func (r *obligationRun) occurrenceOf(anchor string, bound *BoundEdge, class OccurrenceClass, result RuleVerdict, reason UnknownReason, detail string) OccurrenceResult {
	occ := r.entity(class, result, reason, detail)
	loc := edgeLocation(bound)
	occ.bound, occ.Anchor = bound, anchor
	occ.key = observation.EdgeKeyOf(bound.Edge).String()
	occ.Edge = edgeRef(bound)
	occ.Source, occ.Target = observedRef(&bound.From), observedRef(&bound.To)
	occ.Location = &loc
	occ.Role = bound.Edge.Role
	occ.Occurrence = bound.Edge.Occurrence
	occ.Bindings = r.e.bindingRefs(anchor)

	return occ
}

// bindingRefs gives the bindings that bound the observed entity, with their status and authority.
func (e *ObligationEvaluator) bindingRefs(observedID string) []BindingRef {
	ids := e.input.BindingSet.UsedBy[observedID]
	if len(ids) == 0 {
		return nil
	}

	refs := make([]BindingRef, 0, len(ids))

	for _, id := range ids {
		ref := BindingRef{ID: id}
		if b := e.bindingsByID[id]; b != nil {
			ref.Status, ref.Authority = b.Status, b.Authority
		}

		refs = append(refs, ref)
	}

	return refs
}

// existential says whether one satisfying entity satisfies the others: multiplicity `exists`, or the bindings of the subject say agreement `any`.
func (r *obligationRun) existential() bool {
	return r.spec.Multiplicity == policy.MultiplicityExists || r.e.input.BindingSet.AgreementOf(r.ob.Subject) == binding.AgreementAny
}

// repositoryOf gives the project of the observed entity. Empty when the workspace is not known.
func (r *obligationRun) repositoryOf(n *observation.ObservedNode) string {
	if ws := r.e.input.Typed.Workspace; ws != nil && n.FilePath != "" {
		return projectOfPath(ws, n.FilePath)
	}

	return ""
}

// sortFacts orders facts by native kind, names, line, and ID.
func sortFacts(facts []*BoundEdge) {
	slices.SortFunc(facts, func(a, b *BoundEdge) int {
		return cmp.Or(compareEdges(a, b), compareStrings(a.Edge.Occurrence, b.Edge.Occurrence))
	})
}

// joinSorted gives the sorted, comma separated values.
func joinSorted(values []string) string {
	sorted := slices.Clone(values)
	slices.SortFunc(sorted, canonjson.CompareUTF16)

	return strings.Join(sorted, ", ")
}
