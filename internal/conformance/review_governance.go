package conformance

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/inclaro/concordia/internal/assessment"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/impact"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
	"github.com/inclaro/concordia/internal/workspace"
)

// The governance changes of the review configuration (design D7): the rules of the `review-currency` pack, the
// `check.enforcement` entries that select review rules, and the authorities are compared with the trust anchor. A
// change that Concordia cannot show to be stricter is a relaxation and needs a governance disposition.

const reviewPackUse = packs.Prefix + "review-currency"

// environmentsOfWorkspace gives the declared environments for the fact source (the cluster of a workload).
func environmentsOfWorkspace(wspace *workspace.Workspace) []deployment.Environment {
	out := make([]deployment.Environment, 0, len(wspace.Environments))
	for i := range wspace.Environments {
		out = append(out, deployment.Environment{ID: wspace.Environments[i].ID, Cluster: wspace.Environments[i].Cluster})
	}

	return out
}

// governanceChanges compares the working-tree review configuration with the one at the trust anchor. The changes are
// empty when the anchor cannot be read (the rule is `unknown` through the anchor) and when the candidate equals the
// anchor. When the workspace file at a readable anchor cannot be read again, failure says why, so that the rule is
// `unknown` and never a pass.
func (c *checker) governanceChanges(ctx context.Context, wspace *workspace.Workspace, anchor *assessment.Anchor) (changes []impact.SettingChange, failure string) {
	if !anchor.Readable() {
		return nil, ""
	}

	git := c.newGit(wspace.Root)
	rel := filepath.ToSlash(filepath.Base(c.opts.Env.WorkspacePath))

	data, found, err := git.ShowFile(ctx, anchor.Revision, rel)

	switch {
	case err != nil:
		return nil, fmt.Sprintf("the workspace file %s cannot be read at the trust anchor: %v", rel, err)
	case !found:
		return nil, fmt.Sprintf("the workspace file %s is not at the trust anchor", rel)
	}

	atAnchor, err := workspace.LoadBytes(c.opts.Env.WorkspacePath, data, c.opts.Env.Profile)
	if err != nil {
		return nil, fmt.Sprintf("the workspace file %s at the trust anchor cannot be loaded: %v", rel, err)
	}

	return impact.Changes(governanceState(atAnchor), governanceState(wspace)), ""
}

// governanceState reads the governed settings of a workspace: the review rules after the pack's overrides, the
// enforcement entries that select review rules, and the authorities. Both sides of the comparison use this one
// function, so that the comparison never depends on how a setting was written.
func governanceState(wspace *workspace.Workspace) impact.GovernanceState {
	state := impact.GovernanceState{Rules: []impact.RuleSetting{}, Enforcement: []impact.EnforcementSetting{}, Authorities: []impact.AuthoritySetting{}}

	for i := range wspace.Policies.Packs {
		if use := &wspace.Policies.Packs[i]; use.Use == reviewPackUse {
			state.Rules = append(state.Rules, reviewRuleSettings(wspace, use)...)
		}
	}

	for i := range wspace.Check.Enforcement {
		if e := &wspace.Check.Enforcement[i]; selectsReviewRules(e) {
			state.Enforcement = append(state.Enforcement, enforcementSettingOf(e))
		}
	}

	if wspace.Assessments != nil {
		for i := range wspace.Assessments.Authorities {
			state.Authorities = append(state.Authorities, authoritySettingOf(&wspace.Assessments.Authorities[i]))
		}
	}

	return state
}

func enforcementSettingOf(e *workspace.EnforcementEntry) impact.EnforcementSetting {
	return impact.EnforcementSetting{
		Family: e.Family, Pack: e.Pack, Environment: e.Environment, Rules: slices.Clone(e.Rules), Mode: e.Mode,
	}
}

func authoritySettingOf(a *workspace.Authority) impact.AuthoritySetting {
	setting := impact.AuthoritySetting{
		ID: a.ID, Role: a.Role, Mechanism: a.Mechanism, Reviewers: slices.Clone(a.Reviewers), Paths: slices.Clone(a.Paths),
		Signers: []string{},
	}

	for _, s := range a.Signers {
		setting.Signers = append(setting.Signers, s.Identity+"|"+s.Issuer+"|"+s.PublicKey)
	}

	return setting
}

// selectsReviewRules says whether a `check.enforcement` entry applies to review rules: it names the review pack,
// the assessment-currency family, or a REV rule, or it selects every rule.
func selectsReviewRules(e *workspace.EnforcementEntry) bool {
	if e.Family == "" && e.Pack == "" && len(e.Rules) == 0 {
		return true
	}

	return e.Family == string(policy.FamilyAssessmentCurrency) || e.Pack == "review-currency" ||
		slices.ContainsFunc(e.Rules, isReviewRuleID)
}

func isReviewRuleID(id string) bool {
	return slices.Contains(impact.RuleIDs(), id)
}

// reviewRuleSettings gives the settings of the rules of one use of the review pack: severity, `onUnknown`, and the
// evidence floor, after the overrides of the use. A disabled rule has no setting.
func reviewRuleSettings(wspace *workspace.Workspace, use *workspace.PolicyPackUse) []impact.RuleSetting {
	text, ok := packs.Source("review-currency")
	if !ok {
		return nil
	}

	info, _ := policy.DescribePack(policy.PolicyFile{Path: reviewPackUse, Text: string(text)})
	out := make([]impact.RuleSetting, 0, len(info.Rules))

	for _, rule := range info.Rules {
		override := use.Rules[rule.ID]
		if override.Enabled != nil && !*override.Enabled {
			continue
		}

		out = append(out, reviewRuleSetting(wspace, &rule, override))
	}

	return out
}

// reviewRuleSetting gives the setting of one rule with the overrides of the pack use applied over the workspace defaults.
func reviewRuleSetting(wspace *workspace.Workspace, rule *policy.RuleInfo, override workspace.RuleOverride) impact.RuleSetting {
	setting := impact.RuleSetting{
		RuleID: rule.ID, Severity: string(rule.Severity), OnUnknown: string(wspace.Check.Unknown),
		MinimumCoverage: wspace.Check.MinimumCoverage, Scope: []string{},
	}

	if override.Severity != "" {
		setting.Severity = override.Severity
	}

	if ev := override.Evidence; ev != nil {
		applyEvidenceOverride(&setting, ev)
	}

	return setting
}

func applyEvidenceOverride(setting *impact.RuleSetting, ev *workspace.RuleEvidenceOverride) {
	if ev.OnUnknown != "" {
		setting.OnUnknown = ev.OnUnknown
	}

	if ev.MinimumCoverage != nil {
		setting.MinimumCoverage = *ev.MinimumCoverage
	}

	setting.MinimumConfidence = ev.MinimumConfidence
}

// markGovernanceDispositions marks the relaxations that a verified record of a governance authority covers: an
// approval that the verifiers applied, whose authority has the role `governance` at the trust anchor, and whose
// subject digest is the digest of the changed configuration (design D7). A record that the candidate added and
// that is not on the anchor was not applied, so it never covers its own relaxation.
func (r *reviewRun) markGovernanceDispositions(set *assessment.Set, eval *assessment.Evaluation) {
	if !r.anchorReadable() || len(r.governance) == 0 {
		return
	}

	applied := appliedApprovalAuthorities(eval)

	for _, approval := range set.Approvals {
		authorityID, ok := applied[approval.ID]
		if ok && approval.Decision == assessment.DecisionApproved && r.isGovernanceAuthority(authorityID) {
			r.disposed[approval.Subject.Digest] = true
		}
	}
}

func (r *reviewRun) anchorReadable() bool {
	return r.anchor != nil && r.anchor.Readable()
}

// isGovernanceAuthority says whether the trust anchor has the authority with the role `governance`.
func (r *reviewRun) isGovernanceAuthority(authorityID string) bool {
	auth, found := r.anchor.Authority(authorityID)

	return found && auth.Role == workspace.RoleGovernance
}

// appliedApprovalAuthorities maps the id of each approval record that the verifiers applied to its authority id.
func appliedApprovalAuthorities(eval *assessment.Evaluation) map[string]string {
	applied := map[string]string{}

	for _, v := range eval.Verification {
		if v.Applied && v.Kind == assessment.RecordApproval {
			applied[v.RecordID] = v.Authority
		}
	}

	return applied
}
