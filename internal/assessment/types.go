// Package assessment parses, validates, and loads the assessment documents of a workspace: Assessment,
// Assumption, AssessmentApproval, and AssessmentRevocation. Each kind has a closed JSON schema that is embedded in
// the package. The package gives the immutable version digest of an assessment, the authored and the derived review
// states, and the canonical digests of the decision records.
//
// The package reads files only. It does not verify who made a record (that is the authority verification of the
// workspace) and it does not evaluate currency.
package assessment

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/inclaro/concordia/internal/canonjson"
)

// APIVersion is the apiVersion of every assessment document.
const APIVersion = "concordia.io/v1alpha1"

// Document kinds.
const (
	KindAssessment = "Assessment"
	KindAssumption = "Assumption"
	KindApproval   = "AssessmentApproval"
	KindRevocation = "AssessmentRevocation"
)

// Type is the type of an assessment.
type Type string

// Assessment types.
const (
	TypeSecurity Type = "security"
	TypeQuality  Type = "quality"
)

// Types is the closed list of assessment types.
var Types = []Type{TypeSecurity, TypeQuality}

// Outcome is the stored result of an assessment. It is separate from the review state.
type Outcome string

// Outcomes.
const (
	OutcomeSatisfactory               Outcome = "satisfactory"
	OutcomeSatisfactoryWithConditions Outcome = "satisfactory-with-conditions"
	OutcomeUnsatisfactory             Outcome = "unsatisfactory"
	OutcomeInconclusive               Outcome = "inconclusive"
)

// Outcomes is the closed list of outcomes.
var Outcomes = []Outcome{
	OutcomeSatisfactory, OutcomeSatisfactoryWithConditions, OutcomeUnsatisfactory, OutcomeInconclusive,
}

// State is a review state of an assessment version.
type State string

// Review states. A document authors only StateDraft or StateReadyForReview.
const (
	StateDraft          State = "draft"
	StateReadyForReview State = "ready-for-review"
	StateApproved       State = "approved"
	StateRejected       State = "rejected"
	StateReviewRequired State = "review-required"
	StateExpired        State = "expired"
	StateRevoked        State = "revoked"
)

// States is the closed list of review states.
var States = []State{
	StateDraft, StateReadyForReview, StateApproved, StateRejected, StateReviewRequired, StateExpired, StateRevoked,
}

// Authorable says whether a document may author the state.
func (s State) Authorable() bool { return s == StateDraft || s == StateReadyForReview }

// DependencyKind is the kind of an assessment dependency.
type DependencyKind string

// Dependency kinds (closed list of the trust-context capability).
const (
	DepRuntimeIdentity        DependencyKind = "runtime-identity"
	DepRoleBinding            DependencyKind = "role-binding"
	DepSecretReference        DependencyKind = "secret-reference"
	DepConfigurationReference DependencyKind = "configuration-reference"
	DepStorageReference       DependencyKind = "storage-reference"
	DepPublicEntryPoint       DependencyKind = "public-entry-point"
	DepProtocol               DependencyKind = "protocol"
	DepCertificateReference   DependencyKind = "certificate-reference"
	DepExternalDestination    DependencyKind = "external-destination"
	DepTrustContext           DependencyKind = "trust-context"
	DepDataClassification     DependencyKind = "data-classification"
	DepPlacement              DependencyKind = "placement"
	DepBinding                DependencyKind = "binding"
	DepControl                DependencyKind = "control"
	DepImplementation         DependencyKind = "implementation"
	DepVerification           DependencyKind = "verification"
	DepQualityScenario        DependencyKind = "quality-scenario"
)

// DependencyKinds is the closed list of dependency kinds, in the order of the specification.
var DependencyKinds = []DependencyKind{
	DepRuntimeIdentity, DepRoleBinding, DepSecretReference, DepConfigurationReference, DepStorageReference,
	DepPublicEntryPoint, DepProtocol, DepCertificateReference, DepExternalDestination, DepTrustContext,
	DepDataClassification, DepPlacement, DepBinding, DepControl, DepImplementation, DepVerification,
	DepQualityScenario,
}

// Decision is the decision of an approval record.
type Decision string

// Decisions.
const (
	DecisionApproved Decision = "approved"
	DecisionRejected Decision = "rejected"
)

// DispositionKind is how a removed item is accounted for.
type DispositionKind string

// Disposition kinds.
const (
	DispositionRemovedIntentionally DispositionKind = "removed-intentionally"
	DispositionRetired              DispositionKind = "retired"
	DispositionMoved                DispositionKind = "moved"
)

// Location says where a loaded document is: a workspace-relative "/" path and the 1-based line of its root.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// Scope selects the architecture elements that an assessment or an assumption covers. At least one list is set.
type Scope struct {
	CalmNodes         []string `json:"calmNodes,omitempty"`
	CalmRelationships []string `json:"calmRelationships,omitempty"`
	NodeTypes         []string `json:"nodeTypes,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

// Selector holds the facets of a dependency selector. A scalar facet in the document is stored as a list of
// one. Which facets a kind understands is defined by the dependency contract of the change-impact capability.
type Selector map[string][]string

// UnmarshalJSON accepts a string or a list of strings for each facet.
func (s *Selector) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("assessment: selector: %w", err)
	}

	out := make(Selector, len(raw))

	for key, value := range raw {
		var one string
		if err := json.Unmarshal(value, &one); err == nil {
			out[key] = []string{one}

			continue
		}

		var many []string
		if err := json.Unmarshal(value, &many); err != nil {
			return fmt.Errorf("assessment: selector facet %q is neither a string nor a list of strings", key)
		}

		out[key] = many
	}

	*s = out

	return nil
}

// Dependency declares one dependency selector of a given kind.
type Dependency struct {
	Selector Selector       `json:"selector"`
	Kind     DependencyKind `json:"kind"`
}

// Evidence is one evidence reference. Digest is the digest of the evidence content when the author recorded it.
type Evidence struct {
	ID          string `json:"id"`
	Ref         string `json:"ref"`
	Digest      string `json:"digest,omitempty"`
	Description string `json:"description,omitempty"`
}

// Subject is what an assessment covers: dependency selectors, quality scenarios by id, or both.
type Subject struct {
	Summary      string       `json:"summary,omitempty"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
	Scenarios    []string     `json:"scenarios,omitempty"`
}

// Disposition accounts for a removed dependency item. It takes effect only through a verified approval of the
// version that lists it.
type Disposition struct {
	Identity    string          `json:"identity"`
	Disposition DispositionKind `json:"disposition"`
	MovedTo     string          `json:"movedTo,omitempty"`
	Reason      string          `json:"reason,omitempty"`
}

// Assessment is one immutable version of an assessment. Digest is the version digest (see VersionDigest).
type Assessment struct {
	ID          string `json:"id"`
	Environment string `json:"environment"`
	Type        Type   `json:"type"`
	Title       string `json:"title,omitempty"`
	Notes       string `json:"notes,omitempty"`
	// State is the authored state: draft when the document has none.
	State        State         `json:"state,omitempty"`
	Outcome      Outcome       `json:"outcome"`
	Digest       string        `json:"-"`
	Scope        Scope         `json:"scope"`
	Subject      Subject       `json:"subject"`
	Assumptions  []string      `json:"assumptions"`
	Evidence     []Evidence    `json:"evidence"`
	Dispositions []Disposition `json:"dispositions,omitempty"`
	Location     `json:"-"`
	Version      int `json:"version"`
	// Reviewed is the informal `reviewed` flag of the document. It never affects the state: only a verified
	// approval record does (see ReviewOf). Reports can name it as "a reviewed flag is not an approval".
	Reviewed bool `json:"reviewed,omitempty"`
}

// Key gives "<id>@v<version>", the name of the version in messages.
func (a *Assessment) Key() string { return VersionKey(a.ID, a.Version) }

// VersionKey gives "<id>@v<version>".
func VersionKey(id string, version int) string { return fmt.Sprintf("%s@v%d", id, version) }

// Assumption is a declared assumption. It has dependency selectors, evidence, or both. Digest is the digest of
// the canonical JSON of the document.
type Assumption struct {
	ID                 string       `json:"id"`
	Statement          string       `json:"statement"`
	Owner              string       `json:"owner"`
	VerificationMethod string       `json:"verificationMethod"`
	Digest             string       `json:"-"`
	Scope              Scope        `json:"scope"`
	Selectors          []Dependency `json:"selectors,omitempty"`
	Evidence           []Evidence   `json:"evidence,omitempty"`
	Location           `json:"-"`
}

// Reviewer is the identity of a reviewer and the authority under which the reviewer decided.
type Reviewer struct {
	Identity  string `json:"identity"`
	Authority string `json:"authority"`
}

// Ref binds an entity (an assumption or an evidence item) by id and digest.
type Ref struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// AssessmentRef binds an assessment version by id, version, and version digest.
type AssessmentRef struct {
	ID      string `json:"id"`
	Digest  string `json:"digest"`
	Version int    `json:"version"`
}

// Item is a dependency item with its digest under the digest contract of the record.
type Item struct {
	Kind     DependencyKind `json:"kind"`
	Identity string         `json:"identity"`
	Digest   string         `json:"digest"`
}

// ModifiedItem is a dependency item whose digest changed between the previous and the new subject.
type ModifiedItem struct {
	Kind           DependencyKind `json:"kind"`
	Identity       string         `json:"identity"`
	PreviousDigest string         `json:"previousDigest"`
	Digest         string         `json:"digest"`
}

// ApprovalSubject is the subject that an approval binds: its digest and the dependency items behind it.
type ApprovalSubject struct {
	Digest       string `json:"digest"`
	Dependencies []Item `json:"dependencies"`
}

// Expiry holds the expiry conditions of an approval. ExpiresAt is the zero time when the approval has no
// expiry time. Conditions are free-text conditions that end the approval when they occur.
type Expiry struct {
	ExpiresAt  time.Time `json:"expiresAt,omitzero"`
	Conditions []string  `json:"conditions,omitempty"`
}

// Previous names the approval that an incremental approval builds on and the subject digest it bound.
type Previous struct {
	Approval      string `json:"approval"`
	SubjectDigest string `json:"subjectDigest"`
}

// Delta is the exact difference between the previous subject and the new subject of an incremental approval.
type Delta struct {
	Added    []Item         `json:"added"`
	Removed  []Item         `json:"removed"`
	Modified []ModifiedItem `json:"modified"`
}

// ProducedUnder records the values an approval was produced under, so that a change of any of them can
// invalidate the approval: the normalization version, the version of each evaluator, the digest of the policy set
// that selected the review rule, and the digest of the authority configuration at the trust anchor.
type ProducedUnder struct {
	EvaluatorVersions    map[string]string `json:"evaluatorVersions"`
	NormalizationVersion string            `json:"normalizationVersion"`
	PolicyDigest         string            `json:"policyDigest"`
	AuthorityDigest      string            `json:"authorityDigest"`
}

// Approval is a decision record of kind AssessmentApproval. Its Decision is approved or rejected. Previous and
// Delta are set together for an incremental approval and are nil for an initial approval. Digest is the digest of
// the canonical JSON of the record (see RecordDigest).
type Approval struct {
	ID                string          `json:"id"`
	Decision          Decision        `json:"decision"`
	Assessment        AssessmentRef   `json:"assessment"`
	Subject           ApprovalSubject `json:"subject"`
	Scope             Scope           `json:"scope"`
	Environment       string          `json:"environment"`
	Assumptions       []Ref           `json:"assumptions"`
	Evidence          []Ref           `json:"evidence"`
	ContractVersion   string          `json:"contractVersion"`
	CapabilityProfile string          `json:"capabilityProfile"`
	Reviewer          Reviewer        `json:"reviewer"`
	DecidedAt         time.Time       `json:"decidedAt"`
	ProducedUnder     ProducedUnder   `json:"producedUnder"`
	Expiry            *Expiry         `json:"expiry,omitempty"`
	Previous          *Previous       `json:"previous,omitempty"`
	Delta             *Delta          `json:"delta,omitempty"`

	Location `json:"-"`
	Digest   string `json:"-"`

	canonical []byte
}

// CanonicalJSON gives the canonical JSON of the record. A signed record is signed over these bytes, not over the
// file bytes. The slice is shared: do not change it.
func (a *Approval) CanonicalJSON() []byte { return a.canonical }

// Revocation is a decision record of kind AssessmentRevocation. It names an approval by id and digest.
type Revocation struct {
	ID        string    `json:"id"`
	Approval  Ref       `json:"approval"`
	Reviewer  Reviewer  `json:"reviewer"`
	DecidedAt time.Time `json:"decidedAt"`
	Reason    string    `json:"reason"`

	Location `json:"-"`
	Digest   string `json:"-"`

	canonical []byte
}

// CanonicalJSON gives the canonical JSON of the record (see Approval.CanonicalJSON).
func (r *Revocation) CanonicalJSON() []byte { return r.canonical }

// Set is the loaded content of the assessment paths. Every list is sorted by id (and version), never nil.
type Set struct {
	Assessments []*Assessment
	Assumptions []*Assumption
	Approvals   []*Approval
	Revocations []*Revocation
	// Files lists every loaded file with the digest of its bytes, sorted by path.
	Files []File
}

// File is one loaded file.
type File struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Digest string `json:"digest"`
}

// Assessment gives the version of an assessment, or nil.
func (s *Set) Assessment(id string, version int) *Assessment {
	for _, a := range s.Assessments {
		if a.ID == id && a.Version == version {
			return a
		}
	}

	return nil
}

// Versions gives the versions of an assessment id in ascending order.
func (s *Set) Versions(id string) []*Assessment {
	var out []*Assessment

	for _, a := range s.Assessments {
		if a.ID == id {
			out = append(out, a)
		}
	}

	return out
}

// Assumption gives the assumption with the id, or nil.
func (s *Set) Assumption(id string) *Assumption {
	for _, a := range s.Assumptions {
		if a.ID == id {
			return a
		}
	}

	return nil
}

// Approval gives the approval record with the id, or nil.
func (s *Set) Approval(id string) *Approval {
	for _, a := range s.Approvals {
		if a.ID == id {
			return a
		}
	}

	return nil
}

// ApprovalsFor gives the decision records (approved and rejected) that bind one version, oldest first.
func (s *Set) ApprovalsFor(id string, version int) []*Approval {
	var out []*Approval

	for _, a := range s.Approvals {
		if a.Assessment.ID == id && a.Assessment.Version == version {
			out = append(out, a)
		}
	}

	SortApprovals(out)

	return out
}

// RevocationsFor gives the revocation records that name the approval, oldest first.
func (s *Set) RevocationsFor(approvalID string) []*Revocation {
	var out []*Revocation

	for _, r := range s.Revocations {
		if r.Approval.ID == approvalID {
			out = append(out, r)
		}
	}

	slices.SortStableFunc(out, func(a, b *Revocation) int {
		if c := a.DecidedAt.Compare(b.DecidedAt); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.ID, b.ID)
	})

	return out
}

// SortApprovals orders approvals by decision time, then id.
func SortApprovals(list []*Approval) {
	slices.SortStableFunc(list, func(a, b *Approval) int {
		if c := a.DecidedAt.Compare(b.DecidedAt); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.ID, b.ID)
	})
}

// DeclaredDependency is one dependency selector of an assessment with where it was declared.
type DeclaredDependency struct {
	Dependency
	// From is "subject", "scenario", or "assumption:<id>".
	From string `json:"from"`
}

// Declared gives every dependency selector of an assessment: its own subject selectors, each quality scenario of
// its subject as a dependency of kind quality-scenario with the selector `identity: <scenario id>`, and the
// selectors of the assumptions it lists. The order is fixed: subject, scenarios, then assumptions in the order
// the assessment lists them.
func (s *Set) Declared(target *Assessment) []DeclaredDependency {
	out := make([]DeclaredDependency, 0, len(target.Subject.Dependencies)+len(target.Subject.Scenarios))

	for _, dep := range target.Subject.Dependencies {
		out = append(out, DeclaredDependency{Dependency: dep, From: "subject"})
	}

	for _, scenario := range target.Subject.Scenarios {
		out = append(out, DeclaredDependency{
			Dependency: Dependency{Kind: DepQualityScenario, Selector: Selector{"identity": {scenario}}},
			From:       "scenario",
		})
	}

	for _, id := range target.Assumptions {
		asm := s.Assumption(id)
		if asm == nil {
			continue
		}

		for _, dep := range asm.Selectors {
			out = append(out, DeclaredDependency{Dependency: dep, From: "assumption:" + id})
		}
	}

	return out
}
