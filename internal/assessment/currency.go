package assessment

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/clock"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/workspace"
)

// CurrencyState is the currency of one assessment version: whether its approval still covers the system.
type CurrencyState string

// The five currency states (design D8). Every assessment version has exactly one.
const (
	CurrencyCurrent        CurrencyState = "current"
	CurrencyReviewRequired CurrencyState = "review-required"
	CurrencyExpired        CurrencyState = "expired"
	CurrencyRevoked        CurrencyState = "revoked"
	CurrencyUnknown        CurrencyState = "unknown"
)

// The details of a `review-required` result. They are not unknown reasons: `no approval`, `environment
// mismatch`, and `digest contract changed` are review-required details (typed-obligations, D22).
const (
	DetailNoApproval               = "no approval"
	DetailNoAssessment             = "no assessment"
	DetailApprovalRejected         = "approval rejected"
	DetailEnvironmentMismatch      = "environment mismatch"
	DetailScopeMismatch            = "scope mismatch"
	DetailTypeMismatch             = "type mismatch"
	DetailAuthorityMismatch        = "authority mismatch"
	DetailVersionDigestMismatch    = "assessment version digest mismatch"
	DetailSubjectDigestMismatch    = "subject digest mismatch"
	DetailAssumptionDigestMismatch = "assumption digest mismatch"
	DetailEvidenceDigestMismatch   = "evidence digest mismatch"
	DetailContractChanged          = impact.DetailContractChanged
	DetailProfileChanged           = "capability profile changed"
	DetailPolicyChanged            = "policy changed"
	DetailAuthorityChanged         = "trust authority changed"
	DetailNormalizationChanged     = "normalization version changed"
	DetailEvaluatorChanged         = "evaluator version changed"
	DetailDeltaInvalid             = "incremental approval invalid"
	DetailRevoked                  = "revoked"
	DetailExpired                  = "expired"
)

// The unknown reasons that a review obligation can carry (security-assessments, "Unknown currency has a closed
// reason"). ReasonTrustAnchorUnreadable and ReasonVerificationUnavailable are declared with the verifiers.
const (
	ReasonTargetNotDeclared    = impact.ReasonTargetNotDeclared
	ReasonReferenceUnresolved  = impact.ReasonReferenceUnresolved
	ReasonUnsupportedFamily    = impact.ReasonUnsupportedFamily
	ReasonLimitExceeded        = impact.ReasonLimitExceeded
	ReasonCoverageIncomplete   = impact.ReasonCoverageIncomplete
	reasonBindingGap           = "binding-gap"
	unknownReasonDetailNoFacts = "no fact source is configured"
)

// unknownOrder is the order in which causes decide the single reason of an `unknown` result.
var unknownOrder = []string{
	ReasonTrustAnchorUnreadable, ReasonVerificationUnavailable, ReasonUnsupportedFamily, ReasonTargetNotDeclared,
	ReasonReferenceUnresolved, ReasonCoverageIncomplete, ReasonLimitExceeded,
}

// UnknownCause is one cause of an `unknown` result.
type UnknownCause struct {
	// Reason is one of the closed reasons.
	Reason string `json:"reason"`
	// Detail says what is missing.
	Detail string `json:"detail"`
}

// ApprovalInfo describes the approval that a currency result rests on.
type ApprovalInfo struct {
	// DecidedAt is the decision time of the record.
	DecidedAt time.Time `json:"decidedAt"`
	// ExpiresAt is nil when the approval does not expire.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	ID        string     `json:"id"`
	Decision  Decision   `json:"decision"`
	Reviewer  string     `json:"reviewer"`
	Authority string     `json:"authority"`
	// Mechanism is the mechanism that verified the record.
	Mechanism string `json:"mechanism,omitempty"`
	// ContractVersion is the digest contract that the approval was bound under.
	ContractVersion string `json:"contractVersion"`
	// Reason is empty for an approval that counts, and says why not for one that does not.
	Reason string `json:"reason,omitempty"`
	// Conditions are the expiry conditions of the record. They are shown and never evaluated.
	Conditions []string `json:"conditions"`
	// Verified says that the authority of the record is verified.
	Verified bool `json:"verified"`
}

// NotApplied is a decision record of the assessment that failed verification. It is shown with its reason and
// counts for nothing.
type NotApplied struct {
	Record    string `json:"record"`
	Kind      string `json:"kind"`
	Authority string `json:"authority,omitempty"`
	Mechanism string `json:"mechanism,omitempty"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail,omitempty"`
}

// Analysis says how complete the comparison of one assessment was.
type Analysis struct {
	// Incomplete is set when a traversal limit stopped the analysis.
	Incomplete *impact.Incomplete `json:"incomplete,omitempty"`
	// Visited is the number of graph nodes that the traversal from the changed items visited.
	Visited int `json:"visited"`
	// ClosureSize is the number of nodes that the assessment depends on.
	ClosureSize int `json:"closureSize"`
	// Complete is false when a cause or a limit stopped the comparison.
	Complete bool `json:"complete"`
	// Widened says that a selector could not be resolved and the full declared scope was compared instead.
	Widened bool `json:"widened"`
}

// Currency is the result for one assessment version or one required entry.
type Currency struct {
	// Approval is the verified approval that the result rests on, nil when none applies.
	Approval *ApprovalInfo `json:"approval"`
	// Basis is the approval of an earlier version that the comparison used because the version has none.
	Basis string `json:"basis,omitempty"`
	// UnknownReason is the one closed reason of an `unknown` result, empty otherwise.
	UnknownReason string        `json:"unknownReason,omitempty"`
	State         CurrencyState `json:"state"`
	// Reasons are the short details of a review-required, expired, or revoked result, from the Detail* constants,
	// in order of discovery. Never nil.
	Reasons []string `json:"reasons"`
	// Details are the sentences that explain the reasons. Never nil.
	Details []string `json:"details"`
	// Causes are all causes of an unknown result, in reason order. Never nil.
	Causes []UnknownCause `json:"causes"`
	// NotApplied lists the decision records of the assessment that failed verification.
	NotApplied []NotApplied `json:"notApplied"`
	// Triggers are the reasons for review, one per assessment, merged over all changed dependencies.
	Triggers []impact.ReviewTrigger `json:"triggers"`
	// Undisposed are the removed items that no verified disposition accounts for: historical subjects that block
	// (rule REV-DSP-001, one obligation each). Never nil.
	Undisposed []impact.Change `json:"undisposed"`
	// Disposed are the removed items that a verified disposition accounts for. Never nil.
	Disposed []impact.Disposed `json:"disposed"`
	// Regressions are the capability losses that touch an approved dependency (rule REV-CAP-001). Never nil.
	Regressions []impact.Regression `json:"regressions"`
	Analysis    Analysis            `json:"analysis"`
}

// Result is the currency of one assessment version.
type Result struct {
	// State is the derived review state with the currency overlay: review-required, expired, and revoked replace
	// approved; an unknown currency keeps the state of the decision records.
	State       State   `json:"state"`
	File        string  `json:"file"`
	Title       string  `json:"title,omitempty"`
	Environment string  `json:"environment"`
	Outcome     Outcome `json:"outcome"`
	ID          string  `json:"id"`
	Digest      string  `json:"digest"`
	Type        Type    `json:"type"`
	Scope       Scope   `json:"scope"`
	// Subjects are the CALM ids, scenario ids, and the assessment id that CurrencyFor matches on, sorted.
	Subjects []string `json:"subjects"`
	// Required are the ids of the required entries that resolve to this version.
	Required []string `json:"required"`
	Currency Currency `json:"currency"`
	Line     int      `json:"line"`
	Version  int      `json:"version"`
	// Latest says that this is the highest version of its id.
	Latest bool `json:"latest"`
}

// Key gives `id@vN`.
func (r *Result) Key() string { return VersionKey(r.ID, r.Version) }

// RequiredResult is the currency of one required assessment entry.
type RequiredResult struct {
	ID          string                    `json:"id"`
	Assessment  string                    `json:"assessment"`
	Type        string                    `json:"type"`
	Environment string                    `json:"environment"`
	Authority   string                    `json:"authority"`
	Scope       workspace.AssessmentScope `json:"scope"`
	Currency    Currency                  `json:"currency"`
	// Version is the assessment version that the entry was evaluated against, 0 when no document exists.
	Version int `json:"version"`
}

// Evaluation is the outcome of one Evaluate call: the one evaluation that `check`, `assessment status`, and the
// consumers (views, quality evaluation) share. CurrencyFor reads it without evaluating again.
type Evaluation struct {
	// At is the evaluation time. Expiry is judged at it.
	At time.Time `json:"at"`
	// Incremental reports the cache of an incremental evaluation. nil in a full evaluation.
	Incremental *IncrementalReport `json:"incremental,omitempty"`
	// ContractVersion is the current digest contract.
	ContractVersion string `json:"contractVersion"`
	// CapabilityProfile is the id of the profile that supplied the evidence, empty without a fact source.
	CapabilityProfile string `json:"capabilityProfile"`
	// Assessments are the results per version, sorted by id and version.
	Assessments []Result `json:"assessments"`
	// Required are the results per required entry, sorted by id.
	Required []RequiredResult `json:"required"`
	// Verification is the outcome of authority verification for every record.
	Verification []Verification `json:"verification"`
	// Cycles are the dependency cycles that the traversals saw (info CO-ASM-009).
	Cycles []impact.Cycle `json:"cycles"`
	// Diagnostics are the warnings and notes of the evaluation, sorted.
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
	// Visited is the sum of the nodes that the traversals visited.
	Visited int `json:"visited"`
	// Invalidations is the number of assessment versions whose approval a change of the digest contract, policy,
	// trust authority, evaluator, or normalization made review-required.
	Invalidations int `json:"invalidations"`
	// IncompleteAnalyses is the number of assessment versions whose analysis is incomplete (a cause or a limit).
	IncompleteAnalyses int `json:"incompleteAnalyses"`
}

// EvalInput is the input of Evaluate.
type EvalInput struct {
	// Verifier verifies the approvals and revocations of the set. nil means that no record is verified.
	Verifier SetVerifier
	// Facts supplies the current dependency items. nil: a comparison that needs items is `unknown`.
	Facts impact.FactSource
	// Set holds the loaded documents (see Load).
	Set *Set
	// Now is the evaluation clock. nil: clock.Now (CONCORDIA_NOW).
	Now func() time.Time
	// Incremental turns on the incremental cache (design D15). nil is a full evaluation that reads and writes no file.
	Incremental *Incremental
	// Contracts are the known contract versions with their equivalence marks. Zero value: only Contract.
	Contracts impact.Registry
	// Revision is the revision that file digests of implementation, verification, and evidence refs are read at.
	// Empty is the working tree, as the FactSource defines it.
	Revision string
	// PolicyDigest is the digest of the policy set that selects the review rules. An approval records it; empty
	// means that the caller does not know it, and the policy is not compared.
	PolicyDigest string
	// AuthorityDigest is the digest of the authority configuration read at the trust anchor. Empty: not compared.
	AuthorityDigest string
	// Contract is the current digest contract. Zero value: impact.V1().
	Contract impact.Contract
	// Required are the required assessments: from the trust anchor when one is configured, else from the
	// workspace.
	Required []workspace.RequiredAssessment
	// Limits are assessments.analysisLimits.
	Limits impact.Limits
}

// Evaluate decides for every assessment version and every required entry whether its approval still covers the
// current system (design D8). The steps, in order:
//
//  1. The documents are the loaded Set.
//  2. The verified approvals and revocations come from the verifier. The latest verified approval by decision time
//     decides for a version; a version without one is compared with the latest approval of an earlier version.
//  3. A verified revocation gives `revoked`; an expiry time at or before the clock gives `expired`. Both run on
//     every call, with the clock of the input, even when no file changed.
//  4. Environment, version digest, scope, assumption and evidence digests, contract version, capability profile,
//     and the dependency items (impact.Diff against the items the approval lists) are compared. A mismatch gives
//     `review-required` with the reasons and the triggers.
//  5. A comparison that cannot run (unsupported kind, unresolved reference, partial coverage, limit stop, trust
//     anchor unreadable) gives `unknown` with one closed reason.
//  6. Otherwise the result is `current`.
//
// The error is for a failure of the run (the verifier, the fact source), never for a currency result.
func Evaluate(ctx context.Context, input *EvalInput) (*Evaluation, error) {
	ev, err := newEvaluator(ctx, input)
	if err != nil {
		return nil, err
	}

	return ev.run(ctx)
}

// evaluator holds the state of one Evaluate call.
type evaluator struct {
	in       *EvalInput
	set      *Set
	now      time.Time
	registry impact.Registry
	graph    *impact.Graph
	matches  map[string]impact.Match
	subjects map[string]*subject
	choices  map[string]*choice
	cycles   map[string]impact.Cycle
	seenDiag map[string]bool
	profile  impact.CapabilityProfile
	cache    *impact.Cache
	graphSum string
	contract impact.Contract
	verified Verified
	diags    []diag.Diagnostic
}

func newEvaluator(ctx context.Context, input *EvalInput) (*evaluator, error) {
	eval := &evaluator{
		in: input, set: input.Set, contract: input.Contract, registry: input.Contracts,
		graph: impact.NewGraph(), matches: map[string]impact.Match{}, subjects: map[string]*subject{},
		choices: map[string]*choice{}, cycles: map[string]impact.Cycle{}, seenDiag: map[string]bool{},
	}

	if eval.set == nil {
		eval.set = &Set{}
	}

	nowFn := input.Now
	if nowFn == nil {
		nowFn = clock.Now
	}

	eval.now = nowFn().UTC()

	if eval.contract.Version == "" {
		eval.contract = impact.V1()
	}

	if len(eval.registry.Versions()) == 0 {
		eval.registry = impact.NewRegistry(eval.contract)
	}

	if input.Facts != nil {
		eval.profile = input.Facts.Profile()
	}

	eval.openCache()

	eval.verified = Verified{Approvals: []*Approval{}, Revocations: []*Revocation{}, Results: []Verification{}}

	if input.Verifier != nil {
		verified, err := input.Verifier.VerifySet(ctx, eval.set)
		if err != nil {
			return nil, fmt.Errorf("verify the decision records: %w", err)
		}

		eval.verified = verified
		eval.diags = append(eval.diags, verified.Diagnostics...)
	}

	return eval, nil
}

func (e *evaluator) run(ctx context.Context) (*Evaluation, error) {
	versions := slices.Clone(e.set.Assessments)
	slices.SortFunc(versions, func(a, b *Assessment) int {
		return cmp.Or(canonjson.CompareUTF16(a.ID, b.ID), cmp.Compare(a.Version, b.Version))
	})

	for _, a := range versions {
		subj, err := e.resolve(ctx, a)
		if err != nil {
			return nil, err
		}

		e.subjects[a.Key()] = subj
		e.choices[a.Key()] = e.choose(a)
	}

	for _, a := range versions {
		e.addToGraph(a)
	}

	e.noteItems(versions)

	out := &Evaluation{
		At: e.now, ContractVersion: e.contract.Version, CapabilityProfile: e.profile.ID,
		Assessments: make([]Result, 0, len(versions)), Required: []RequiredResult{},
		Verification: slices.Clone(e.verified.Results), Cycles: []impact.Cycle{},
	}

	if out.Verification == nil {
		out.Verification = []Verification{}
	}

	latest := map[string]int{}
	for _, a := range versions {
		latest[a.ID] = max(latest[a.ID], a.Version)
	}

	for _, a := range versions {
		cur := e.currency(a)
		res := Result{
			ID: a.ID, Version: a.Version, Type: a.Type, Title: a.Title, Environment: a.Environment, Outcome: a.Outcome,
			Scope: a.Scope, File: a.File, Line: a.Line, Digest: a.Digest, Subjects: e.subjectsOf(a),
			Required: []string{}, Currency: cur, Latest: latest[a.ID] == a.Version,
		}
		res.State = overlayState(e.choices[a.Key()].review.State, cur.State)

		out.Visited += cur.Analysis.Visited
		out.Assessments = append(out.Assessments, res)
	}

	e.requiredResults(out, latest)

	for _, c := range e.cycles {
		out.Cycles = append(out.Cycles, c)
	}

	slices.SortFunc(out.Cycles, func(x, y impact.Cycle) int { return impact.CompareNodeID(x.Nodes[0], y.Nodes[0]) })

	countOutcomes(out)
	e.finishCache(out)

	out.Diagnostics = diag.Sort(e.diags)

	return out, nil
}

// overlayState puts the currency over the derived review state: the states review-required, expired, and revoked
// come from currency evaluation and never from a document.
func overlayState(review State, cur CurrencyState) State {
	switch cur {
	case CurrencyCurrent, CurrencyUnknown:
		return review
	case CurrencyReviewRequired:
		if review == StateApproved || review == StateRejected {
			return StateReviewRequired
		}

		return review
	case CurrencyExpired:
		return StateExpired
	case CurrencyRevoked:
		return StateRevoked
	default:
		return review
	}
}

// note records a diagnostic once.
func (e *evaluator) note(code string, params diag.Params, opts ...diag.Option) {
	d := diag.New(code, params, opts...)

	key := diag.FormatDiagnostic(d)
	if e.seenDiag[key] {
		return
	}

	e.seenDiag[key] = true
	e.diags = append(e.diags, d)
}

// nodeOf gives the graph node of an assessment version.
func nodeOf(a *Assessment) impact.NodeID {
	return impact.NodeID{Identity: a.ID, Version: strconv.Itoa(a.Version)}
}

// subjectsOf gives the sorted CALM ids and scenario ids that an assessment covers: CurrencyFor matches on them.
func (e *evaluator) subjectsOf(a *Assessment) []string {
	set := map[string]bool{a.ID: true}

	for _, list := range [][]string{a.Scope.CalmNodes, a.Scope.CalmRelationships, a.Subject.Scenarios} {
		for _, s := range list {
			set[s] = true
		}
	}

	for _, d := range e.set.Declared(a) {
		for _, key := range []string{facetCalmNodes, facetCalmRelations} {
			for _, s := range d.Selector[key] {
				set[s] = true
			}
		}
	}

	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}
