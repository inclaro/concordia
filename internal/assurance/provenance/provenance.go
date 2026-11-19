// Package provenance evaluates the build and deployment evidence of container occurrences: exact lineage from a
// deployed image back to a declared source repository (claim `exact-lineage`), the digest that a deployment record
// binds to a workload (`deployed-digest`), successful deployment (`deployment-succeeded`), and drift between the
// record, the rendered digest, and what a workload observation saw (`runtime-drift`).
//
// The package is pure. It does not read files, start cosign, or read the clock: the caller loads the statements,
// verifies them with statement.Verifier, and gives the verification results and the run context to the New*
// functions, which run the one admissibility engine (internal/assurance/admit). The evaluators below then see only
// admitted statements, so a statement that failed verification can never support a claim: it is listed as
// considered-and-rejected and the claim stays `unknown`.
package provenance

import (
	"slices"
	"strings"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/admit"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/workspace"
)

// ContractID is the id of the provenance contract of the claim registry.
const ContractID = "provenance"

// Verdict is the verdict of one claim. The integrator maps it onto the verdicts of internal/conformance.
type Verdict string

// The verdicts of the claims of this package.
const (
	VerdictPass      Verdict = "pass"
	VerdictViolation Verdict = "violation"
	VerdictUnknown   Verdict = "unknown"
)

// The unknown reasons that this package sets. They are members of the closed set of internal/conformance/reasons.go,
// which this package cannot import; the integrator converts them.
const (
	// ReasonNoEvidenceSupplied: no statement, or no admissible statement, supports the claim.
	ReasonNoEvidenceSupplied = "no-evidence-supplied"
	// ReasonEvidenceConflict: admissible statements with the same subject and role disagree and none wins.
	ReasonEvidenceConflict = "evidence-conflict"
)

// TrustContract is what exact lineage holds within (spec artifact-provenance, "Exact lineage needs digest, verified
// provenance, and source match"): the producers trusted for `build-provenance`, the trusted builders, and the
// declared source repositories. Its digest goes into every lineage result, so a result names the contract it was
// reached under, and a changed contract changes the digest.
type TrustContract struct {
	Producers          []workspace.Producer `json:"producers"`
	Builders           []string             `json:"builders"`
	SourceRepositories []string             `json:"sourceRepositories"`
}

// NewTrustContract takes the trust contract out of the workspace configuration. cfg may be nil: the contract is then
// empty and no lineage can pass. Every list is sorted, so the digest does not depend on the order of the file.
func NewTrustContract(cfg *workspace.Assurance) TrustContract {
	out := TrustContract{Producers: []workspace.Producer{}, Builders: []string{}, SourceRepositories: []string{}}
	if cfg == nil {
		return out
	}

	for i := range cfg.Producers {
		if slices.Contains(cfg.Producers[i].Claims, workspace.ProducerClaimBuildProvenance) {
			producer := cfg.Producers[i]
			producer.Claims = slices.Sorted(slices.Values(producer.Claims))
			out.Producers = append(out.Producers, producer)
		}
	}

	slices.SortFunc(out.Producers, func(a, b workspace.Producer) int { return canonjson.CompareUTF16(a.ID, b.ID) })

	if cfg.Provenance != nil {
		out.Builders = slices.Sorted(slices.Values(cfg.Provenance.Builders))
		out.SourceRepositories = slices.Sorted(slices.Values(cfg.Provenance.SourceRepositories))
	}

	return out
}

// Digest is `sha256:<hex>` of the canonical JSON of the contract.
func (c *TrustContract) Digest() string {
	data, err := canonjson.Marshal(c)
	if err != nil {
		// The contract is plain strings and structs of strings: encoding cannot fail.
		panic("provenance: trust contract digest: " + err.Error())
	}

	return digest.OfBytes(data)
}

// ContractRef names the trust contract that a result was reached under.
type ContractRef struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Ref names the contract: the provenance contract id and the digest of the trust contract.
func (c *TrustContract) Ref() ContractRef { return ContractRef{ID: ContractID, Digest: c.Digest()} }

// TrustsBuilder says whether a builder id from a provenance statement is in the contract. A declared builder
// matches the id exactly, and also the id with a Git reference appended (`<builder>@refs/heads/main`): SLSA builder
// ids of GitHub workflows carry the reference, and the workspace declares the workflow without it. A declared builder
// that has a reference itself matches exactly only.
func (c *TrustContract) TrustsBuilder(id string) bool {
	if id == "" {
		return false
	}

	for _, builder := range c.Builders {
		if id == builder || (!strings.Contains(builder, "@") && strings.HasPrefix(id, builder+"@")) {
			return true
		}
	}

	return false
}

// Ref identifies one admitted or rejected statement file in a result.
type Ref struct {
	// Path is the workspace-relative path of the statement file.
	Path string `json:"path"`
	// Digest is the digest of the file bytes.
	Digest string `json:"digest"`
	// Producer is the id of the declared producer whose signature verified. Empty when none did.
	Producer  string              `json:"producer,omitempty"`
	Integrity assurance.Integrity `json:"integrity"`
}

// Rejected is a statement that concerns the claim but cannot support it, with every reason.
type Rejected struct {
	Ref
	Details []assurance.Detail `json:"details"`
}

// Statement is a SLSA provenance statement together with the outcome of verifying and admitting it.
type Statement struct {
	Ref
	SLSA      statement.SLSA      `json:"-"`
	Admission assurance.Admission `json:"admission"`
}

// NewStatement reads a loaded SLSA provenance statement and admits it for the claim kind `build-provenance`. The
// verification is the result of statement.Verifier.Verify for the file. cfg gives the declared producers and the
// maximum age; it may be nil.
func NewStatement(loaded *statement.Loaded, verification statement.Verification, ctx assurance.Context, cfg *workspace.Assurance) (Statement, error) {
	slsa, err := statement.ParseSLSA(&loaded.Statement, verification)
	if err != nil {
		return Statement{}, err //nolint:wrapcheck // the reader names the predicate type that is wrong.
	}

	var age time.Duration

	if cfg != nil && cfg.Provenance != nil {
		age = days(cfg.Provenance.MaxAgeDays)
	}

	admission := admit.Admit(&admit.Input{
		Verification: verification, Kind: workspace.ProducerClaimBuildProvenance, Context: ctx,
		Producers: producersOf(cfg), Facts: admit.SLSAFacts(&slsa), MaxAge: age,
	})

	return Statement{
		Ref:       Ref{Path: loaded.Path, Digest: loaded.Digest, Producer: verification.ProducerID, Integrity: admission.Integrity},
		SLSA:      slsa,
		Admission: admission,
	}, nil
}

// names says whether one subject of the statement has the digest. Hex digests compare without case.
func (s *Statement) names(want statement.Digest) bool {
	for i := range s.SLSA.Subjects {
		if value, ok := s.SLSA.Subjects[i].Digest[want.Algorithm]; ok && strings.EqualFold(value, want.Value) {
			return true
		}
	}

	return false
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func producersOf(cfg *workspace.Assurance) []workspace.Producer {
	if cfg == nil {
		return nil
	}

	return cfg.Producers
}
