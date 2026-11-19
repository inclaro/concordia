package policy

import (
	"slices"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
)

// ControlPlan is a CALM control attachment as Concordia sees it. The scope of the attachment is kept.
type ControlPlan struct {
	MinimumPolicyCoverage *float64            `json:"minimumPolicyCoverage,omitempty"`
	Scope                 intent.ControlScope `json:"scope"`
	ControlID             string              `json:"controlId"`
	Pointer               string              `json:"pointer"`
	Engine                string              `json:"engine,omitempty"`
	PolicyRef             string              `json:"policyRef,omitempty"`
	UnknownDisposition    UnknownDisposition  `json:"unknownDisposition,omitempty"`
	RuleIDs               []string            `json:"ruleIds"`
	// CheckIDs are the check identifiers that verify the control (`check-ids`), sorted and without duplicates. They are
	// not rule IDs: ApplyControls does not look them up in the policy packs.
	CheckIDs   []string `json:"checkIds,omitempty"`
	Applicable bool     `json:"applicable"`
}

// ControlOverride is the control-level override for one rule. Several controls
// on one rule combine: highest coverage, strictest disposition.
type ControlOverride struct {
	// MinimumCoverage is nil when no control sets it.
	MinimumCoverage *float64
	// OnUnknown is empty when no control sets it.
	OnUnknown  UnknownDisposition
	ControlIDs []string
}

// ControlOverrideMap holds the overrides by rule ID.
type ControlOverrideMap map[string]*ControlOverride

// ApplyControlsOptions are the options of ApplyControls.
type ApplyControlsOptions struct {
	// ControlID (`--control <id>`) keeps only the rules of this control. An
	// unknown ID gives a CO-WRK-010 error. Empty keeps all.
	ControlID string
	// KnownRuleIDs are all rule IDs that the policy packs declare. nil: the IDs
	// of the plans. Use CompileResult.RuleIDs when `--rule` filtered the plans.
	KnownRuleIDs []string
}

// ApplyControlsResult is the result of ApplyControls.
type ApplyControlsResult struct {
	// Plans are the plans after the `--control` filter.
	Plans []RulePlan
	// Controls are all controls of the architecture (after the `--control`
	// filter when set), sorted by control ID, scope level, scope ID, pointer.
	Controls []ControlPlan
	// Diagnostics hold a CO-POL-005 (error) for each unknown rule ID of a control.
	Diagnostics []diag.Diagnostic
}

func strictness(d UnknownDisposition) int {
	switch d {
	case UnknownFail:
		return 2
	case UnknownWarn:
		return 1
	case UnknownIgnore:
		return 0
	default:
		return 0
	}
}

func compareControls(left, right *ControlPlan) int {
	if c := canonjson.CompareUTF16(left.ControlID, right.ControlID); c != 0 {
		return c
	}

	if c := canonjson.CompareUTF16(string(left.Scope.Level), string(right.Scope.Level)); c != 0 {
		return c
	}

	if c := canonjson.CompareUTF16(left.Scope.ID, right.Scope.ID); c != 0 {
		return c
	}

	return canonjson.CompareUTF16(left.Pointer, right.Pointer)
}

// listControls makes the sorted control list of left model.
func listControls(model *intent.ArchitectureModel) []ControlPlan {
	out := make([]ControlPlan, len(model.Controls))

	for i := range model.Controls {
		att := &model.Controls[i]
		cfg := att.Config

		plan := ControlPlan{
			ControlID:  att.ControlID,
			Scope:      att.Scope,
			Pointer:    att.Pointer,
			Applicable: cfg != nil && cfg.Engine == "concordia",
			RuleIDs:    []string{},
			Engine:     att.Engine,
		}

		if plan.Applicable {
			plan.RuleIDs = uniqSorted(cfg.RuleIDs)
			plan.CheckIDs = uniqSorted(cfg.CheckIDs)
			plan.Engine = cfg.Engine
			plan.PolicyRef = cfg.PolicyRef
			plan.UnknownDisposition = UnknownDisposition(cfg.UnknownDisposition)
			plan.MinimumPolicyCoverage = cfg.MinimumPolicyCoverage
		}

		out[i] = plan
	}

	slices.SortStableFunc(out, func(a, b ControlPlan) int { return compareControls(&a, &b) })

	return out
}

// ControlOverrides gives the per-rule overrides from the control
// configurations (`unknown-disposition`, `minimum-policy-coverage`). It does
// not check that the rule IDs exist; ApplyControls does.
func ControlOverrides(model *intent.ArchitectureModel) ControlOverrideMap {
	out := ControlOverrideMap{}
	controls := listControls(model)

	for i := range controls {
		if !controls[i].Applicable {
			continue
		}

		for _, ruleID := range controls[i].RuleIDs {
			cur, ok := out[ruleID]
			if !ok {
				cur = &ControlOverride{ControlIDs: []string{}}
				out[ruleID] = cur
			}

			cur.merge(&controls[i])
		}
	}

	return out
}

// merge adds the settings of one control: highest coverage, strictest disposition.
func (o *ControlOverride) merge(control *ControlPlan) {
	if control.MinimumPolicyCoverage != nil {
		cov := *control.MinimumPolicyCoverage
		if o.MinimumCoverage != nil {
			cov = max(*o.MinimumCoverage, cov)
		}

		o.MinimumCoverage = &cov
	}

	disposition := control.UnknownDisposition
	if disposition != "" && (o.OnUnknown == "" || strictness(disposition) > strictness(o.OnUnknown)) {
		o.OnUnknown = disposition
	}

	if !slices.Contains(o.ControlIDs, control.ControlID) {
		o.ControlIDs = append(o.ControlIDs, control.ControlID)
	}
}

// ApplyControls checks the rule IDs of the controls and applies `--control`.
//
//   - An unknown rule ID gives a CO-POL-005 diagnostic (error, exit 2) with the control pointer.
//   - `--control <id>` keeps only the plans of that control. An unknown control ID gives a CO-WRK-010 error.
//   - Controls of another engine stay in Controls with Applicable false (verdict `not-applicable`).
func ApplyControls(model *intent.ArchitectureModel, plans []RulePlan, opts ApplyControlsOptions) (ApplyControlsResult, error) {
	known := opts.KnownRuleIDs
	if known == nil {
		for i := range plans {
			known = append(known, plans[i].RuleID)
		}
	}

	all := listControls(model)

	var diagnostics []diag.Diagnostic

	for i := range all {
		c := &all[i]

		for _, ruleID := range c.RuleIDs {
			if slices.Contains(known, ruleID) {
				continue
			}

			diagnostics = append(diagnostics, diag.New(diag.POL005, diag.Params{idParam: ruleID},
				diag.WithSource(diag.Source{URI: model.SourceURI, Pointer: c.Pointer}),
				diag.WithSuggestion("control "+c.ControlID+" names rule '"+ruleID+"', which no policy pack declares")))
		}
	}

	controls := all
	kept := slices.Clone(plans)

	if opts.ControlID != "" {
		controls = nil

		for i := range all {
			if all[i].ControlID == opts.ControlID {
				controls = append(controls, all[i])
			}
		}

		if len(controls) == 0 {
			return ApplyControlsResult{}, diag.Fail(diag.WRK010, diag.Detail("unknown control '"+opts.ControlID+"' for --control"))
		}

		var wanted []string
		for i := range controls {
			wanted = append(wanted, controls[i].RuleIDs...)
		}

		kept = slices.DeleteFunc(kept, func(p RulePlan) bool { return !slices.Contains(wanted, p.RuleID) })
	}

	return ApplyControlsResult{Plans: kept, Controls: nonNil(controls), Diagnostics: diagnostics}, nil
}
