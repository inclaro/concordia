package policy

import "github.com/inclaro/concordia/internal/observation"

// Raw policy shape (after schema validation).

type rawSelector = map[string]any

// rawCoverage is the `evidence.coverage` block of a rule.
type rawCoverage struct {
	Files              *float64 `json:"files"`
	Bytes              *float64 `json:"bytes"`
	SupportedLanguages *float64 `json:"supportedLanguages"`
}

type rawEvidence struct {
	MinimumCoverage *float64                          `json:"minimumCoverage"`
	Coverage        *rawCoverage                      `json:"coverage"`
	EdgeKinds       map[string]observation.Confidence `json:"edgeKinds"`
	// MinimumConfidence and OnUnknown apply to every rule.
	MinimumConfidence observation.Confidence `json:"minimumConfidence"`
	OnUnknown         UnknownDisposition     `json:"onUnknown"`
	// MinimumResolution, Completeness, PassCompleteness and Roles are the keys of a typed rule (it has an `obligation`).
	MinimumResolution observation.Resolution     `json:"minimumResolution"`
	Completeness      Completeness               `json:"completeness"`
	PassCompleteness  Completeness               `json:"passCompleteness"`
	Roles             []observation.EvidenceRole `json:"roles"`
}

// rawObligation is the `obligation` block of a rule.
type rawObligation struct {
	Applies  rawSelector `json:"applies"`
	Relation struct {
		Target    rawSelector       `json:"target"`
		Kind      string            `json:"kind"`
		Direction RelationDirection `json:"direction"`
	} `json:"relation"`
	Scope *struct {
		Environments []string `json:"environments"`
		Repositories []string `json:"repositories"`
	} `json:"scope"`
	Type         ObligationType `json:"type"`
	Multiplicity Multiplicity   `json:"multiplicity"`
}

type rawRule struct {
	Forbid *struct {
		Target    rawSelector `json:"target"`
		Traversal *string     `json:"traversal"`
		Edges     []string    `json:"edges"`
	} `json:"forbid"`
	Evidence *rawEvidence `json:"evidence"`
	Require  *struct {
		Edge *struct {
			Target rawSelector `json:"target"`
			Edges  []string    `json:"edges"`
		} `json:"edge"`
		Entity *struct {
			NamePattern string   `json:"namePattern"`
			Labels      []string `json:"labels"`
		} `json:"entity"`
		EffectiveAccess *struct {
			From      rawSelector `json:"from"`
			To        rawSelector `json:"to"`
			Dimension string      `json:"dimension"`
			Expect    string      `json:"expect"`
			Ports     []int       `json:"ports"`
		} `json:"effectiveAccess"`
		CheckResult *struct {
			Subject     rawSelector `json:"subject"`
			Check       string      `json:"check"`
			Environment string      `json:"environment"`
		} `json:"checkResult"`
		Claim *struct {
			ID          string `json:"id"`
			Environment string `json:"environment"`
		} `json:"claim"`
		Edges []string `json:"edges"`
	} `json:"require"`
	DenyPath *struct {
		Target   rawSelector `json:"target"`
		MaxDepth *int        `json:"maxDepth"`
		Edges    []string    `json:"edges"`
	} `json:"denyPath"`
	AllowOnly *struct {
		Targets rawSelector `json:"targets"`
		Edges   []string    `json:"edges"`
	} `json:"allowOnly"`
	Independent *struct {
		Edges []string `json:"edges"`
	} `json:"independent"`
	PublicAPIOnly *struct {
		Target rawSelector `json:"target"`
		API    struct {
			SourceRoots []string `json:"sourceRoots"`
		} `json:"api"`
		Edges []string `json:"edges"`
	} `json:"publicApiOnly"`
	Ownership *struct {
		Owners rawSelector `json:"owners"`
		Edges  []string    `json:"edges"`
	} `json:"ownership"`
	Budget *struct {
		Target  rawSelector `json:"target"`
		Measure string      `json:"measure"`
		Edges   []string    `json:"edges"`
		Max     int         `json:"max"`
	} `json:"budget"`
	ObservedRequired *struct{} `json:"observedRequired"`
	// ViewCurrency is the predicate of the rules of the `views` pack. `check` evaluates them from the declared
	// views; the compiler refuses a rule that has it.
	ViewCurrency *struct {
		Check string `json:"check"`
	} `json:"viewCurrency"`
	// Decisions is the predicate of the rules of the `adr-governance` pack. `check` evaluates them from the decision register.
	Decisions *struct {
		Obligation string `json:"obligation"`
	} `json:"decisions"`
	// AssessmentCurrency is the predicate of the rules of the `review-currency` pack: approval, disposition,
	// capability or governance. `check` evaluates them from the assessments of the workspace.
	AssessmentCurrency *string `json:"assessmentCurrency"`
	DeclaredOnly       *struct {
		Edges []string `json:"edges"`
	} `json:"declaredOnly"`
	LayerOrder *struct {
		Layers []rawSelector `json:"layers"`
		Edges  []string      `json:"edges"`
	} `json:"layerOrder"`
	Subject rawSelector `json:"subject"`
	Allow   *struct {
		Target *struct {
			SourceRoots []string `json:"sourceRoots"`
			CalmNodes   []string `json:"calmNodes"`
		} `json:"target"`
		When *struct {
			Any []struct {
				Edge      *string `json:"edge"`
				TargetTag *string `json:"targetTag"`
			} `json:"any"`
		} `json:"when"`
	} `json:"allow"`
	Aggregation *string        `json:"aggregation"`
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Owner       string         `json:"owner"`
	Remediation string         `json:"remediation"`
	Rationale   string         `json:"rationale"`
	Severity    RuleSeverity   `json:"severity"`
	Enforcement Enforcement    `json:"enforcement"`
	Obligation  *rawObligation `json:"obligation"`
	Family      string         `json:"family"`
}

type rawPack struct {
	Metadata struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"metadata"`
	Selectors  map[string]rawSelector `json:"selectors"`
	Precedence *Precedence            `json:"precedence"`
	Rules      []rawRule              `json:"rules"`
}

// setsCoverageTwice says whether the evidence sets the file coverage by both of its names,
// `minimumCoverage` and `coverage.files`.
func (e *rawEvidence) setsCoverageTwice() bool {
	if e == nil || e.Coverage == nil {
		return false
	}

	return e.MinimumCoverage != nil && e.Coverage.Files != nil
}
