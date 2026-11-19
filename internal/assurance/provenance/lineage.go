package provenance

import (
	"slices"
	"strconv"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/canonjson"
)

// Claim identifiers of this package. They equal the ids of the claim registry (internal/assurance/claims), which a
// test keeps in step.
const (
	ClaimExactLineage        = "exact-lineage"
	ClaimDeployedDigest      = "deployed-digest"
	ClaimDeploymentSucceeded = "deployment-succeeded"
	ClaimRuntimeDrift        = "runtime-drift"
)

// The items that an `unknown` lineage result can list as missing.
const (
	// MissingDigest: the occurrence has no immutable digest, so it has no identity that a build could be linked to.
	MissingDigest = "digest"
	// MissingProvenance: no admissible provenance statement names the digest of the occurrence.
	MissingProvenance = "provenance"
	// MissingSource: the provenance names no source that is a declared repository.
	MissingSource = "source-repository"
)

// OccurrenceRef identifies one container occurrence in a result. The image parts stay separate: the tag is shown as
// written and is never a version.
type OccurrenceRef struct {
	Workload   render.ResourceID `json:"workload"`
	Container  string            `json:"container"`
	Kind       string            `json:"kind"`
	Image      string            `json:"image"`
	Repository string            `json:"repository"`
	Tag        string            `json:"tag,omitempty"`
	Digest     string            `json:"digest,omitempty"`
}

// RefOf identifies the occurrence.
func RefOf(occ *render.ContainerOccurrence) OccurrenceRef {
	return OccurrenceRef{
		Workload: occ.Workload, Container: occ.Container, Kind: occ.Kind,
		Image: occ.Raw, Repository: occ.Repository, Tag: occ.Tag, Digest: occ.Digest,
	}
}

// SourceRef is the declared source repository that a provenance statement matched.
type SourceRef struct {
	// ID is the declared source repository id.
	ID string `json:"id"`
	// URI is the resolved dependency of the statement that matched it.
	URI string `json:"uri"`
	// Revision is the `gitCommit` of that dependency, when the statement gives one.
	Revision string `json:"revision,omitempty"`
}

// LineageResult is the exact lineage claim for one container occurrence.
type LineageResult struct {
	Claim      string        `json:"claim"`
	Occurrence OccurrenceRef `json:"occurrence"`
	Verdict    Verdict       `json:"verdict"`
	// Reason is the unknown reason when the verdict is `unknown`.
	Reason string `json:"reason,omitempty"`
	// Missing lists what is missing for an `unknown` verdict: MissingDigest, MissingProvenance, MissingSource.
	Missing []string `json:"missing"`
	Detail  string   `json:"detail"`
	// Contract names the trust contract the result was reached under, for every verdict.
	Contract ContractRef `json:"contract"`
	// Statement is the provenance statement that decided the result: the one that passes, the one with the
	// untrusted builder, or the admissible one whose source is not declared.
	Statement *Ref `json:"statement,omitempty"`
	// Builder is the builder id of that statement. For a violation it is the observed evidence.
	Builder string     `json:"builder,omitempty"`
	Source  *SourceRef `json:"source,omitempty"`
	// Rejected are the statements that name the digest but cannot support the claim (tampered, unsigned, from a
	// producer that may not attest build provenance, stale), with their reasons.
	Rejected []Rejected `json:"rejected"`
}

// Lineage evaluates the exact lineage claim for one occurrence (spec artifact-provenance, "Exact lineage needs
// digest, verified provenance, and source match"). The claim is `pass` only if:
//
//   - the occurrence has an immutable digest (a tag alone names no content and is never linked to a build);
//   - an admissible, producer-authenticated provenance statement names that digest as a subject. Admissibility
//     includes that the verified producer is trusted for `build-provenance`;
//   - the builder of that statement is in the trust contract; and
//   - the statement records a source that is a declared repository (see findSource for the matching).
//
// A verified statement whose builder is not in the trust contract gives a `violation` with the builder as the
// observed evidence. Every other shortfall gives `unknown` with the missing items and the reason
// `no-evidence-supplied`. A statement that names a different digest is not linked at all. With several statements,
// one that passes wins; else a violation is reported (the deployed digest was built by a builder that the workspace
// does not trust); else the first unknown, in path order.
func Lineage(occ *render.ContainerOccurrence, statements []Statement, contract *TrustContract) LineageResult {
	res := LineageResult{
		Claim: ClaimExactLineage, Occurrence: RefOf(occ), Verdict: VerdictUnknown, Reason: ReasonNoEvidenceSupplied,
		Missing: []string{}, Contract: contract.Ref(), Rejected: []Rejected{},
	}

	want, ok := statement.ParseDigest(occ.Digest)
	if !ok {
		res.Missing = []string{MissingDigest, MissingProvenance}
		res.Detail = noDigestDetail(occ)

		return res
	}

	ordered := slices.Clone(statements)
	slices.SortFunc(ordered, func(a, b Statement) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	var admitted []*Statement

	for i := range ordered {
		stmt := &ordered[i]
		if !stmt.names(want) {
			continue
		}

		if stmt.Admission.Admissible && stmt.Admission.Integrity == assurance.IntegrityAuthenticated {
			admitted = append(admitted, stmt)

			continue
		}

		res.Rejected = append(res.Rejected, Rejected{Ref: stmt.Ref, Details: slices.Clone(stmt.Admission.Details)})
	}

	if len(admitted) == 0 {
		res.Missing = []string{MissingProvenance}
		res.Detail = noProvenanceDetail(len(res.Rejected))

		return res
	}

	return decide(&res, admitted, contract)
}

// decide picks the result among the admissible statements that name the digest.
func decide(res *LineageResult, admitted []*Statement, contract *TrustContract) LineageResult {
	var violation, unknown *Statement

	for _, stmt := range admitted {
		switch {
		case !contract.TrustsBuilder(stmt.SLSA.BuilderID):
			violation = firstNonNil(violation, stmt)
		default:
			if source, found := findSource(stmt.SLSA.ResolvedDependencies, contract.SourceRepositories); found {
				res.Verdict, res.Reason, res.Missing = VerdictPass, "", []string{}
				res.Statement, res.Builder = refOf(stmt), stmt.SLSA.BuilderID
				res.Source = &SourceRef{ID: source.ID, URI: source.URI, Revision: source.Revision}
				res.Detail = "verified provenance from " + stmt.Producer + " names this digest; builder and source repository " +
					source.ID + " are in the trust contract"

				return *res
			}

			unknown = firstNonNil(unknown, stmt)
		}
	}

	switch {
	case violation != nil:
		res.Verdict, res.Reason, res.Missing = VerdictViolation, "", []string{}
		res.Statement, res.Builder = refOf(violation), violation.SLSA.BuilderID
		res.Detail = "verified provenance names this digest, but the builder " + violation.SLSA.BuilderID + " is not in the trust contract"
	default:
		res.Missing = []string{MissingSource}
		res.Statement, res.Builder = refOf(unknown), unknown.SLSA.BuilderID
		res.Detail = "verified provenance names this digest, but none of its resolved dependencies is a declared source repository"
	}

	return *res
}

func firstNonNil(current, next *Statement) *Statement {
	if current != nil {
		return current
	}

	return next
}

func refOf(s *Statement) *Ref {
	ref := s.Ref

	return &ref
}

func noDigestDetail(occ *render.ContainerOccurrence) string {
	image := occ.Raw
	if image == "" {
		image = occ.Repository
	}

	return "missing digest: " + image + " has no immutable digest, so a tag is not a version and no build is linked; " +
		"missing provenance: none can be linked without a digest"
}

func noProvenanceDetail(rejected int) string {
	if rejected == 0 {
		return "no provenance for this digest"
	}

	return "no admissible provenance for this digest: " + strconv.Itoa(rejected) + " statement(s) that name it cannot support the claim"
}

// StepStatus is the state of the image-to-build step of the deployment trace.
type StepStatus string

// The states of the image-to-build step (spec artifact-provenance, "Static build links stay lineage candidates").
const (
	// StepVerified: the exact lineage claim is `pass` for the occurrence.
	StepVerified StepStatus = "verified"
	// StepCandidate: only a static link exists (a `built-from` link to a Dockerfile or a CI definition).
	StepCandidate StepStatus = "candidate"
	// StepUnknown: neither.
	StepUnknown StepStatus = "unknown"
)

// LineageStatus is the state of the image-to-build step for one occurrence: `verified` only when the exact lineage
// claim is `pass`; else `candidate` when a static build link exists; else `unknown`. A static link never satisfies
// the claim, so it never turns the step into `verified`. lineage may be nil (no lineage result for the
// occurrence).
func LineageStatus(lineage *LineageResult, hasStaticLink bool) StepStatus {
	switch {
	case lineage != nil && lineage.Verdict == VerdictPass:
		return StepVerified
	case hasStaticLink:
		return StepCandidate
	default:
		return StepUnknown
	}
}
