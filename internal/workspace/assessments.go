package workspace

import (
	"fmt"

	"github.com/inclaro/concordia/internal/diag"
)

// The authority roles.
const (
	RoleReviewer   = "reviewer"
	RoleException  = "exception"
	RoleGovernance = "governance"
)

// The approval mechanisms of an authority.
const (
	// MechanismProtectedReview is the default: the record must exist at the trust anchor revision.
	MechanismProtectedReview = "protected-review"
	// MechanismSigned: the record carries a cosign signature of a listed signer.
	MechanismSigned = "signed"
)

// The types of a required assessment.
const (
	AssessmentTypeSecurity = "security"
	AssessmentTypeQuality  = "quality"
)

// DefaultAssessmentPaths gives the default `assessments.paths`.
func DefaultAssessmentPaths() []string { return []string{"assessments"} }

// BuiltinTrustDimensions gives the built-in authority dimension names. `trust.dimensions` adds names to them.
func BuiltinTrustDimensions() []string {
	return []string{"organization", "account", "principal", "zone", "region"}
}

// Trust is the trust context configuration: the extra authority dimensions and the data classification list.
type Trust struct {
	// Dimensions are the dimension names that the workspace declares beyond the built-in ones.
	Dimensions []string `json:"dimensions"`
	// Classifications are the allowed data classification values.
	Classifications []string `json:"classifications"`
}

// Assessments is the assessment configuration of a workspace.
type Assessments struct {
	// TrustAnchor is nil when no anchor is configured.
	TrustAnchor *TrustAnchor `json:"trustAnchor,omitempty"`
	// Paths are the workspace-relative directories or files with the assessment documents.
	Paths          []string             `json:"paths"`
	Authorities    []Authority          `json:"authorities"`
	Required       []RequiredAssessment `json:"required"`
	AnalysisLimits AnalysisLimits       `json:"analysisLimits"`
}

// TrustAnchor names the git revision from which verification reads its inputs.
type TrustAnchor struct {
	// Ref is a git reference, for example `origin/main`.
	Ref string `json:"ref"`
}

// Authority is a party that can approve (or except, or govern) with one mechanism.
type Authority struct {
	ID string `json:"id"`
	// Role is `reviewer`, `exception`, or `governance`.
	Role string `json:"role"`
	// Mechanism is `protected-review` (the default) or `signed`.
	Mechanism string `json:"mechanism"`
	// Reviewers are the reviewer identities of a protected-review authority.
	Reviewers []string `json:"reviewers"`
	// Paths are the globs of the record paths that a protected-review authority accepts.
	Paths []string `json:"paths"`
	// Signers are the signers of a signed authority.
	Signers []Signer `json:"signers"`
}

// Signer is a cosign signer: a keyless identity and issuer, or a public key path.
type Signer struct {
	Identity  string `json:"identity,omitempty"`
	Issuer    string `json:"issuer,omitempty"`
	PublicKey string `json:"publicKey,omitempty"`
}

// RequiredAssessment is an assessment that the workspace requires for a scope and an environment.
type RequiredAssessment struct {
	ID          string          `json:"id"`
	Assessment  string          `json:"assessment"`
	Type        string          `json:"type"`
	Environment string          `json:"environment"`
	Authority   string          `json:"authority"`
	Scope       AssessmentScope `json:"scope"`
}

// AssessmentScope selects the CALM elements that a required assessment covers.
type AssessmentScope struct {
	CalmNodes []string `json:"calmNodes"`
	NodeTypes []string `json:"nodeTypes"`
	Tags      []string `json:"tags"`
}

// AnalysisLimits are the limits of the change-impact traversal. 0 means not set.
type AnalysisLimits struct {
	MaxDepth   int `json:"maxDepth,omitempty"`
	MaxVisited int `json:"maxVisited,omitempty"`
	MaxSeconds int `json:"maxSeconds,omitempty"`
}

// trustOf reads the merged `trust` object. It returns nil when there is no `trust` key.
func trustOf(merged object) *Trust {
	raw, ok := merged["trust"].(map[string]any)
	if !ok {
		return nil
	}

	return &Trust{
		Dimensions:      strList(raw, "dimensions", []string{}),
		Classifications: strList(raw, "classifications", []string{}),
	}
}

// assessmentsOf reads the merged `assessments` object with defaults. It returns nil when there is no
// `assessments` key.
func assessmentsOf(merged object) *Assessments {
	raw, ok := merged["assessments"].(map[string]any)
	if !ok {
		return nil
	}

	out := &Assessments{
		Paths:       strList(raw, "paths", DefaultAssessmentPaths()),
		Authorities: []Authority{},
		Required:    []RequiredAssessment{},
	}

	if anchor, isObj := raw["trustAnchor"].(map[string]any); isObj {
		out.TrustAnchor = &TrustAnchor{Ref: str(anchor, "ref", "")}
	}

	list, _ := raw["authorities"].([]any)
	for _, item := range list {
		entry, isObj := item.(map[string]any)
		if !isObj {
			continue
		}

		out.Authorities = append(out.Authorities, Authority{
			ID:        str(entry, "id", ""),
			Role:      str(entry, "role", ""),
			Mechanism: str(entry, "mechanism", MechanismProtectedReview),
			Reviewers: strList(entry, "reviewers", []string{}),
			Paths:     strList(entry, "paths", []string{}),
			Signers:   signersOf(entry),
		})
	}

	required, _ := raw["required"].([]any)
	for _, item := range required {
		entry, isObj := item.(map[string]any)
		if !isObj {
			continue
		}

		scope := obj(entry, "scope")
		out.Required = append(out.Required, RequiredAssessment{
			ID:          str(entry, "id", ""),
			Assessment:  str(entry, "assessment", ""),
			Type:        str(entry, "type", ""),
			Environment: str(entry, "environment", ""),
			Authority:   str(entry, "authority", ""),
			Scope: AssessmentScope{
				CalmNodes: strList(scope, "calmNodes", []string{}),
				NodeTypes: strList(scope, "nodeTypes", []string{}),
				Tags:      strList(scope, "tags", []string{}),
			},
		})
	}

	limits := obj(raw, "analysisLimits")
	out.AnalysisLimits = AnalysisLimits{
		MaxDepth:   int(num(limits, "maxDepth", 0)),
		MaxVisited: int(num(limits, "maxVisited", 0)),
		MaxSeconds: int(num(limits, "maxSeconds", 0)),
	}

	return out
}

func signersOf(authority object) []Signer {
	out := []Signer{}
	list, _ := authority["signers"].([]any)

	for _, item := range list {
		entry, isObj := item.(map[string]any)
		if !isObj {
			continue
		}

		out = append(out, Signer{
			Identity:  str(entry, "identity", ""),
			Issuer:    str(entry, "issuer", ""),
			PublicKey: str(entry, "publicKey", ""),
		})
	}

	return out
}

// checkAssessments rejects duplicate authority ids and duplicate required-assessment ids.
func checkAssessments(space *Workspace, path string) *diag.Error {
	cfg := space.Assessments
	if cfg == nil {
		return nil
	}

	authorities := make(map[string]struct{}, len(cfg.Authorities))

	for _, a := range cfg.Authorities {
		if _, dup := authorities[a.ID]; dup {
			return invalid(fmt.Sprintf("assessments.authorities: id '%s' appears two times", a.ID), path, nil, 0)
		}

		authorities[a.ID] = struct{}{}
	}

	required := make(map[string]struct{}, len(cfg.Required))

	for i := range cfg.Required {
		id := cfg.Required[i].ID
		if _, dup := required[id]; dup {
			return invalid(fmt.Sprintf("assessments.required: id '%s' appears two times", id), path, nil, 0)
		}

		required[id] = struct{}{}
	}

	return nil
}
