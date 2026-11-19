package plan

import (
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/admit"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/workspace"
)

// ReasonNoEvidenceSupplied is the unknown reason of a claim whose plan is not admissible. It is the literal of the
// closed set in internal/conformance/reasons.go, which this package does not import.
const ReasonNoEvidenceSupplied = "no-evidence-supplied"

// SubjectDigest is the digest that a statement must name to bind this plan: the digest of the plan file. The
// statement verification takes it as the expected subject.
func (p *Plan) SubjectDigest() (statement.Digest, bool) {
	return statement.ParseDigest(p.Digest)
}

// BindInput is one plan and the statement that is supposed to bind it.
type BindInput struct {
	Plan *Plan
	// File is the workspace-relative path of the plan file, for diagnostics.
	File string
	// Statement is the `plan` statement of the plan, or nil when the plan has none.
	Statement *statement.Loaded
	// Verification is the outcome of the signature verification of Statement. Its Subject field is not used: Bind
	// compares the subject with the digest of the plan itself, so that no caller can leave the binding unchecked.
	Verification statement.Verification
	Context      assurance.Context
	Producers    []workspace.Producer
	// MaxAge is the configured maximum age of the plan (`maxAgeDays`); 0 checks no age.
	MaxAge time.Duration
}

// Binding is the admissibility of one plan. The plan itself may still be shown whatever the binding says; an
// inadmissible binding only means that mandatory claims that need the plan are `unknown`.
type Binding struct {
	// Statement is the content of the statement, nil when there is none or when it could not be read.
	Statement *statement.Plan
	// Diagnostics are the CO-PRV-009 warnings: a statement that cannot be read, a failed signature, and a subject
	// digest that is not the digest of the plan.
	Diagnostics []diag.Diagnostic
	Admission   assurance.Admission
}

// Admissible says whether the plan can support a mandatory claim.
func (b *Binding) Admissible() bool { return b.Admission.Admissible }

// UnknownReason is the reason of the claims that need an inadmissible plan, or "" when the plan is admissible.
func (b *Binding) UnknownReason() string {
	if b.Admission.Admissible {
		return ""
	}

	return ReasonNoEvidenceSupplied
}

// Bind decides whether the plan is bound to the evaluated revision, the environment, and the plan digest by a
// verified statement of a producer that is trusted for `plan`, and is not older than the maximum age (spec
// rendered-infrastructure-evidence, "Plan evidence is admissible only with revision binding"). It uses the same
// engine as every other statement, so every failed condition is a detail.
//
// Only the digest of the plan file is bound. The statement does not say that the plan content is right; the
// verified digest says that the producer saw these bytes.
//
// A plan without a statement has integrity `content not verified` and the detail `not-authenticated`. A statement
// that names another digest is `integrity-failure` with a warning, and its integrity is not raised by the signature.
func Bind(input *BindInput) Binding {
	if input.Statement == nil {
		return Binding{Admission: assurance.NewAdmission(assurance.IntegrityNotVerified, []assurance.Detail{assurance.DetailNotAuthenticated})}
	}

	path := input.Statement.Path

	facts, err := statement.ParsePlan(&input.Statement.Statement)
	if err != nil {
		// A statement whose predicate is not a plan binding binds nothing.
		return Binding{
			Admission:   assurance.NewAdmission(assurance.IntegrityNotVerified, []assurance.Detail{assurance.DetailNotAuthenticated}),
			Diagnostics: []diag.Diagnostic{diag.New(diag.PRV009, diag.Detail(path+": "+err.Error()), diag.WithSource(diag.Source{URI: path}))},
		}
	}

	verification := input.Verification
	verification.Subject = statement.SubjectDiffers

	if expected, ok := input.Plan.SubjectDigest(); ok && input.Statement.Statement.Names(expected) {
		verification.Subject = statement.SubjectMatches
	}

	return Binding{
		Admission: admit.Admit(&admit.Input{
			Verification: verification,
			Kind:         workspace.ProducerClaimPlan,
			Context:      input.Context,
			Producers:    input.Producers,
			Facts:        admit.PlanFacts(&facts),
			MaxAge:       input.MaxAge,
		}),
		Statement: &facts,
		// The verification of the statement already reports a failed signature; Bind reports only the subject that it
		// compared, so one file never gives the same warning twice.
		Diagnostics: statement.Verification{Subject: verification.Subject}.Diagnostics(path),
	}
}
