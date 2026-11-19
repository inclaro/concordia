package views_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/views"
	"github.com/inclaro/concordia/internal/workspace"
)

// evaluate runs views.Evaluate on the inputs against the given tracked revision.
func evaluate(t *testing.T, in *views.Inputs, git workspace.GitPort, onUnknown policy.UnknownDisposition) *conformance.ViewEvaluation {
	t.Helper()

	out, err := views.Evaluate(t.Context(), &conformance.ViewEvalInput{
		State: &conformance.ViewInput{
			Workspace: in.Workspace, Reconcile: in.Observed.Reconcile, Evidence: in.Observed.Evidence, Bindings: in.Bindings,
			Policies: in.Policies, Coverage: in.Observed.Coverage, Reconciliation: in.Reconciliation,
		},
		Git: git, ToolVersion: "test", OnUnknown: onUnknown,
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func ruleOf(t *testing.T, out *conformance.ViewEvaluation, id string) *conformance.RuleResult {
	t.Helper()

	i := slices.IndexFunc(out.Rules, func(r conformance.RuleResult) bool { return r.RuleID == id })
	if i < 0 {
		t.Fatalf("no rule %s in %+v", id, out.Rules)
	}

	return &out.Rules[i]
}

func findingsOf(out *conformance.ViewEvaluation, id string) []conformance.Finding {
	var found []conformance.Finding

	for i := range out.Findings {
		if out.Findings[i].RuleID == id {
			found = append(found, out.Findings[i])
		}
	}

	return found
}

// committedRevision is the tracked revision of a generated view that was rendered and committed.
func committedRevision(t *testing.T, in *views.Inputs, view *views.Definition) *workspace.FakeGit {
	t.Helper()

	return gitAt(committedFiles(t, in, view))
}

// fndRuleIDs gives the IDs of the rules in order.
func fndRuleIDs(rules []conformance.RuleResult) []string {
	ids := make([]string, 0, len(rules))
	for i := range rules {
		ids = append(ids, rules[i].RuleID)
	}

	return ids
}

// fndIsViewRule says whether a rule belongs to the pack `views` in version 1.0.0, in the view family, with a name and
// the view-currency plan.
func fndIsViewRule(rule *conformance.RuleResult) bool {
	inPack := rule.Policy.ID == views.PackID && rule.Policy.Version == "1.0.0" && rule.Family == policy.FamilyView

	return inPack && strings.TrimSpace(rule.Name) != "" && rule.Plan.Family == "view-currency"
}

// The built-in pack `views` has the three rules, in version 1.0.0, and the results name them (design D3).
func TestViewRulesAreRegistered(t *testing.T) {
	t.Parallel()

	in := testInputs(t, "HTTPS", containerView("payments-container"))
	out := evaluate(t, in, committedRevision(t, in, &in.Workspace.Views[0]), policy.UnknownFail)

	// The results come in the order of the checks; the pipeline sorts the rules of the run by ID.
	if !slices.Equal(fndRuleIDs(out.Rules), []string{views.RulePresence, views.RuleFreshness, views.RuleCorrespondence}) {
		t.Fatalf("rules = %+v", out.Rules)
	}

	for i := range out.Rules {
		rule := &out.Rules[i]
		if !fndIsViewRule(rule) {
			t.Errorf("rule %+v", rule)
		}

		if rule.Plan.EvidenceRequirements.OnUnknown != policy.UnknownFail {
			t.Errorf("%s: onUnknown %s, want the effective check.unknown", rule.RuleID, rule.Plan.EvidenceRequirements.OnUnknown)
		}
	}

	// A view of a workspace with no violation: the rules pass, the freshness rule has the generated view as subject.
	if got := ruleOf(t, out, views.RuleFreshness); got.Verdict != conformance.VerdictPass || !slices.Equal(got.Subjects, []string{"payments-container"}) {
		t.Errorf("freshness rule = %+v", got)
	}
}

// fingerprintCase is the standard container view with its committed files, for the fingerprint checks.
type fingerprintCase struct {
	in    *views.Inputs
	def   *views.Definition
	files map[string]string
}

func newFingerprintCase(t *testing.T) *fingerprintCase {
	t.Helper()

	in := testInputs(t, "HTTPS", containerView("payments-container"))
	def := &in.Workspace.Views[0]

	return &fingerprintCase{in: in, def: def, files: committedFiles(t, in, def)}
}

// evaluateFiles evaluates the view against a revision that has the files.
func (c *fingerprintCase) evaluateFiles(t *testing.T, files map[string]string) *conformance.ViewEvaluation {
	t.Helper()

	return evaluate(t, c.in, gitAt(files), policy.UnknownFail)
}

// editedFiles gives the committed files with the text appended to the generated artifact.
func (c *fingerprintCase) editedFiles(edit string) map[string]string {
	edited := maps.Clone(c.files)
	edited[c.def.Output.Path] += edit

	return edited
}

// isPresenceFinding says whether a finding is the absent-manifest finding with the fingerprint.
func isPresenceFinding(f *conformance.Finding, fingerprint, manifestPath string) bool {
	return f.Fingerprint == fingerprint && f.Kind == views.KindViewAbsent && f.Result == conformance.VerdictViolation &&
		f.Location != nil && f.Location.Path == manifestPath && f.Source.ArchitectureID == "payments-container"
}

// requirePresenceFingerprint checks the finding of an uncommitted manifest.
func (c *fingerprintCase) requirePresenceFingerprint(t *testing.T, out *conformance.ViewEvaluation) {
	t.Helper()

	manifestPath := c.def.ManifestPath()

	found := findingsOf(out, views.RulePresence)
	if len(found) != 1 {
		t.Fatalf("presence findings = %+v", found)
	}

	wantPresence := conformance.Fingerprint(conformance.Parts{
		RuleID: views.RulePresence, SourceArchitectureID: "payments-container", TargetArchitectureID: "manifest",
		CanonicalEdgeKind: "view:presence:absent", NormalizedRepositoryPath: manifestPath,
	})
	if got := &found[0]; !isPresenceFinding(got, wantPresence, manifestPath) {
		t.Errorf("presence finding = %+v, want fingerprint %s", *got, wantPresence)
	}
}

// freshnessFindings gives the freshness findings of the artifact after the edit.
func (c *fingerprintCase) freshnessFindings(t *testing.T, edit string) []conformance.Finding {
	t.Helper()

	return findingsOf(c.evaluateFiles(t, c.editedFiles(edit)), views.RuleFreshness)
}

func fndFingerprints(found []conformance.Finding) []string {
	out := make([]string, 0, len(found))
	for i := range found {
		out = append(out, found[i].Fingerprint)
	}

	return out
}

// requireFreshnessFingerprint checks that two different edits of the artifact give the same finding identity, and
// that the artifact finding has the fingerprint of design D3. No digest is in the fingerprint.
func (c *fingerprintCase) requireFreshnessFingerprint(t *testing.T) {
	t.Helper()

	fingerprints := make([]string, 0, 2)

	for _, edit := range []string{"// layout edit one\n", "// a different layout edit, longer\n"} {
		fresh := c.freshnessFindings(t, edit)
		if len(fresh) == 0 {
			t.Fatal("an edited artifact has no freshness finding")
		}

		for i := range fresh {
			if fresh[i].Kind != views.KindViewStale {
				t.Errorf("kind = %s", fresh[i].Kind)
			}
		}

		fingerprints = append(fingerprints, fresh[0].Fingerprint)
	}

	if fingerprints[0] != fingerprints[1] {
		t.Errorf("the fingerprint changed with the artifact bytes: %v", fingerprints)
	}

	wantFreshness := conformance.Fingerprint(conformance.Parts{
		RuleID: views.RuleFreshness, SourceArchitectureID: "payments-container", TargetArchitectureID: c.def.Output.Path,
		CanonicalEdgeKind: "view:freshness:artifact", NormalizedRepositoryPath: c.def.Output.Path,
	})
	if slices.Contains(fingerprints, wantFreshness) {
		return
	}

	// The first finding of the check can be another difference; the artifact finding must be among them.
	if all := fndFingerprints(c.freshnessFindings(t, "// x\n")); !slices.Contains(all, wantFreshness) {
		t.Errorf("no finding has the fingerprint %s: %v", wantFreshness, all)
	}
}

// fndSubjectKey gives the subject key of a difference: the element ID, or `from>to`.
func fndSubjectKey(d *conformance.ViewDifference) string {
	if d.To == "" {
		return d.From
	}

	return d.From + ">" + d.To
}

// hasDriftFinding says whether a finding with the fingerprint is a drift finding.
func hasDriftFinding(found []conformance.Finding, fingerprint string) bool {
	return slices.ContainsFunc(found, func(f conformance.Finding) bool {
		return f.Fingerprint == fingerprint && f.Kind == views.KindViewDrift
	})
}

// requireCorrespondenceFingerprints checks that each difference has the fingerprint of its kind and its subject key. A
// generated view is compared with the observed projection, which differs from the model of the fixture.
func (c *fingerprintCase) requireCorrespondenceFingerprints(t *testing.T, out *conformance.ViewEvaluation) {
	t.Helper()

	correspondence := findingsOf(out, views.RuleCorrespondence)
	if len(out.Views) != 1 {
		t.Fatalf("views = %+v, want one view", out.Views)
	}

	if len(out.Views[0].Correspondence.Differences) == 0 {
		t.Fatalf("the standard code graph gives no correspondence difference: %+v", out.Views[0].Correspondence)
	}

	for i := range out.Views[0].Correspondence.Differences {
		d := &out.Views[0].Correspondence.Differences[i]
		key := fndSubjectKey(d)

		want := conformance.Fingerprint(conformance.Parts{
			RuleID: views.RuleCorrespondence, SourceArchitectureID: "payments-container", TargetArchitectureID: key,
			CanonicalEdgeKind: "view:correspondence:" + d.Kind, NormalizedRepositoryPath: c.def.Output.Path,
		})

		if !hasDriftFinding(correspondence, want) {
			t.Errorf("no finding for the %s difference %s", d.Kind, key)
		}
	}
}

// requireNoDigestOrOrdinal checks that a finding never carries a digest or an ordinal in the text that identifies it.
func requireNoDigestOrOrdinal(t *testing.T, out *conformance.ViewEvaluation) {
	t.Helper()

	for i := range out.Findings {
		target := out.Findings[i].Target.ArchitectureID
		if strings.Contains(target, "sha256:") || strings.Contains(target, "#diagram-") {
			t.Errorf("target %s", target)
		}
	}
}

// The fingerprint has the form of design D3: the rule, the view ID, the subject, the kind `view:<check>:<difference>`
// and the path. It has no provider ordinal and no digest, so a regenerated artifact keeps the identity of its finding.
func TestFindingFingerprintForm(t *testing.T) {
	t.Parallel()

	c := newFingerprintCase(t)

	// Presence: the manifest is not committed.
	absent := maps.Clone(c.files)
	delete(absent, c.def.ManifestPath())

	out := c.evaluateFiles(t, absent)

	c.requirePresenceFingerprint(t, out)
	// Freshness: the committed DSL is edited. Two different edits give two different artifact digests and the
	// same finding identity: no digest is in the fingerprint.
	c.requireFreshnessFingerprint(t)
	c.requireCorrespondenceFingerprints(t, out)
	requireNoDigestOrOrdinal(t, out)
}

// fndPresenceSeveritiesAndActions gives the severity and the next action of each presence finding by view ID.
func fndPresenceSeveritiesAndActions(out *conformance.ViewEvaluation) (severities map[string]policy.RuleSeverity, actions map[string]string) {
	severities = map[string]policy.RuleSeverity{}
	actions = map[string]string{}

	found := findingsOf(out, views.RulePresence)
	for i := range found {
		severities[found[i].Source.ArchitectureID] = found[i].Severity
		actions[found[i].Source.ArchitectureID] = found[i].NextAction
	}

	return severities, actions
}

// requireAuthoredViewAction checks that the authored view needs evidence.
func requireAuthoredViewAction(t *testing.T, out *conformance.ViewEvaluation) {
	t.Helper()

	for i := range out.Views {
		if out.Views[i].ID == "payments-authored" && out.Views[i].NextAction != views.NextActionSupplyEvidence {
			t.Errorf("view next action = %q", out.Views[i].NextAction)
		}
	}
}

// requireDifferenceNeedsIntent checks that a difference between the diagram and the implementation needs corrected
// intent.
func requireDifferenceNeedsIntent(t *testing.T, out *conformance.ViewEvaluation) {
	t.Helper()

	found := findingsOf(out, views.RuleCorrespondence)
	for i := range found {
		if found[i].Result == conformance.VerdictViolation && found[i].NextAction != views.NextActionCorrectIntent {
			t.Errorf("difference finding action = %q", found[i].NextAction)
		}
	}
}

// The severity of a finding comes from `comparison.severity` (default error); the next-action category follows
// design D10.
func TestFindingSeverityAndNextAction(t *testing.T) {
	t.Parallel()

	generated := containerView("payments-container")
	warning := containerView("payments-warning")
	warning.Comparison = &views.Comparison{Severity: "warning"}
	authored := authoredAt("payments-authored", "docs/arch/payments.md")
	in := testInputs(t, "HTTPS", generated, warning, authored)

	// Nothing is committed: the generated views are absent and need `regenerate view`, the authored one needs evidence.
	out := evaluate(t, in, gitAt(map[string]string{"README.md": "x"}), policy.UnknownFail)

	severities, actions := fndPresenceSeveritiesAndActions(out)

	wantSeverities := map[string]policy.RuleSeverity{"payments-container": "error", "payments-warning": "warning", "payments-authored": "error"}
	if !maps.Equal(severities, wantSeverities) {
		t.Errorf("severities = %v, want %v", severities, wantSeverities)
	}

	wantActions := map[string]string{"payments-container": views.NextActionRegenerateView, "payments-warning": views.NextActionRegenerateView, "payments-authored": views.NextActionSupplyEvidence}
	if !maps.Equal(actions, wantActions) {
		t.Errorf("next actions = %v, want %v", actions, wantActions)
	}

	requireAuthoredViewAction(t, out)
	requireDifferenceNeedsIntent(t, out)
}

// isUnknownAnalysisFix says whether a finding is an `unknown` result whose fix is the analysis configuration.
func isUnknownAnalysisFix(f *conformance.Finding) bool {
	return f.Result == conformance.VerdictUnknown && f.NextAction == views.NextActionFixAnalysis
}

// With no repository evidence the presence is unknown: the finding is an `unknown` result, the rule is unknown, and
// the fix is the analysis configuration, not the diagram.
func TestPresenceUnknownWithoutARevision(t *testing.T) {
	t.Parallel()

	in := testInputs(t, "HTTPS", containerView("payments-container"))
	out := evaluate(t, in, &workspace.FakeGit{}, policy.UnknownWarn)

	rule := ruleOf(t, out, views.RulePresence)
	if rule.Verdict != conformance.VerdictUnknown || rule.Plan.EvidenceRequirements.OnUnknown != policy.UnknownWarn {
		t.Errorf("rule = %+v", rule)
	}

	found := findingsOf(out, views.RulePresence)
	if len(found) != 1 || !isUnknownAnalysisFix(&found[0]) {
		t.Errorf("findings = %+v", found)
	}

	if len(rule.Reasons) == 0 || !strings.HasPrefix(rule.Reasons[0].Message, "payments-container: ") {
		t.Errorf("reasons = %+v", rule.Reasons)
	}
}

// hasNotEvaluatedDigest says whether a view has no assessment and a semantic digest.
func hasNotEvaluatedDigest(v *conformance.ViewResult) bool {
	return v.Assessment.State == conformance.AssessmentCurrencyNotEvaluated && v.SemanticDigest != nil && strings.HasPrefix(*v.SemanticDigest, "sha256:")
}

// hasCurrentContracts says whether a view names the digest and projection contracts and did not change them.
func hasCurrentContracts(v *conformance.ViewResult) bool {
	return v.DigestContract == views.DigestContract && v.ProjectionContract == views.ProjectionContract && !v.ContractChanged
}

// requireViewInterface checks the interface values that every view carries.
func requireViewInterface(t *testing.T, v *conformance.ViewResult) {
	t.Helper()

	if !hasNotEvaluatedDigest(v) || !hasCurrentContracts(v) {
		t.Errorf("view %s = %+v", v.ID, v)
	}

	if v.Depicts.Elements == nil || v.Depicts.Relationships == nil || v.Scope == nil {
		t.Errorf("view %s has a nil list: %+v", v.ID, v)
	}
}

// isLinkedTo says whether the link names the artifact and manifest of the definition and the semantic digest.
func isLinkedTo(link *conformance.ViewGeneratedFrom, def *views.Definition, semanticDigest string) bool {
	if link == nil {
		return false
	}

	return link.ArtifactPath == def.Output.Path && link.ManifestPath == def.ManifestPath() &&
		strings.HasPrefix(link.ArtifactDigest, "sha256:") && link.SemanticDigest == semanticDigest
}

// isRegeneratedAndPassing says whether a view passes presence and freshness, and freshness was verified by
// regeneration.
func isRegeneratedAndPassing(v *conformance.ViewResult) bool {
	return v.Presence.Status == conformance.VerdictPass && v.Freshness.Status == conformance.VerdictPass &&
		v.Freshness.Verification == views.VerificationRegenerated
}

// Each view has its four results and the assessment-currency interface of design D15.
func TestViewResultCarriesTheAssessmentInterface(t *testing.T) {
	t.Parallel()

	in := testInputs(t, "HTTPS", containerView("payments-container"), authoredAt("payments-prose", "docs/arch/prose.md"))
	def := &in.Workspace.Views[0]
	files := committedFiles(t, in, def)
	files["docs/arch/prose.md"] = "# Payments\n"

	out := evaluate(t, in, gitAt(files), policy.UnknownFail)

	if len(out.Views) != 2 || !slices.Contains([]string{"payments-prose", "payments-container"}, out.Views[0].ID) {
		t.Fatalf("views = %+v", out.Views)
	}

	var generated, authored *conformance.ViewResult

	for i := range out.Views {
		requireViewInterface(t, &out.Views[i])

		switch out.Views[i].ID {
		case "payments-container":
			generated = &out.Views[i]
		case "payments-prose":
			authored = &out.Views[i]
		}
	}

	// The generated artifact is linked to its view snapshot (spec "Artifact is linked to its view snapshot").
	if !isLinkedTo(generated.GeneratedFrom, def, *generated.SemanticDigest) {
		t.Errorf("generatedFrom = %+v", generated.GeneratedFrom)
	}

	if authored.GeneratedFrom != nil || authored.Freshness.Status != conformance.VerdictNotApplicable {
		t.Errorf("authored view = %+v", authored)
	}

	if !isRegeneratedAndPassing(generated) {
		t.Errorf("generated view = %+v", generated)
	}
}
