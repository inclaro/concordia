package conformance

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/exceptions"
	"github.com/inclaro/concordia/internal/intent"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
)

// Reason code of a rule that is not applicable because another rule supersedes it for every obligation.
const reasonSuperseded = "superseded"

// plannedObligations builds the obligations of all typed plans, sorted by rule ID. The plans must stay in place.
func plannedObligations(plans []policy.RulePlan, model *intent.ArchitectureModel, ctx *TypedContext) []PlannedObligation {
	var out []PlannedObligation

	for i := range plans {
		plan := &plans[i]
		if plan.Predicate.Family != policy.FamilyTyped {
			continue
		}

		built := BuildObligations(plan, model, ctx.Workspace, ctx.Bindings, ctx.Environments)
		for i := range built {
			out = append(out, PlannedObligation{Obligation: built[i], Plan: plan})
		}
	}

	slices.SortStableFunc(out, func(a, b PlannedObligation) int {
		return cmp.Or(
			canonjson.CompareUTF16(a.RuleID, b.RuleID), canonjson.CompareUTF16(a.Subject, b.Subject),
			canonjson.CompareUTF16(a.Environment, b.Environment),
		)
	})

	return out
}

// capabilityRegressions gives the families that the capability profile of the lockfile supported and this run does
// not (observation.CapabilityRegressions). A lock that records no profile, or a profile that this binary does not
// ship, promised nothing that can be compared.
func capabilityRegressions(lock *observation.ProfileRef, caps *observation.Capabilities) []observation.CapabilityRegression {
	locked, ok := observation.LockedProfile(lock)
	if !ok {
		return nil
	}

	return observation.CapabilityRegressions(locked, caps.Profile, caps.Families)
}

// precedencesOf gives the `precedence` lists of the packs, in pack order, with the ID of the pack that declares each.
func precedencesOf(packs []policy.PolicyPackRef) []policy.NamedPrecedence {
	var out []policy.NamedPrecedence

	for i := range packs {
		if p := packs[i].Precedence; p != nil {
			out = append(out, policy.NamedPrecedence{ID: packs[i].ID, Precedence: *p})
		}
	}

	return out
}

func conflictRefs(planned []PlannedObligation) []policy.ObligationRef {
	refs := make([]policy.ObligationRef, len(planned))
	for i := range planned {
		refs[i] = planned[i].Ref(planned[i].Plan)
	}

	return refs
}

// CheckPolicyConflicts builds the obligations of the typed plans from intent and scope alone (no graph) and detects
// the rules that contradict each other (policy.DetectConflicts). A conflict that no precedence resolves is CO-POL-017
// (error, exit code 2): `check` stops before it evaluates anything. The report also lists the obligations
// that a precedence entry supersedes. Plans that are not typed are ignored.
func CheckPolicyConflicts(plans []policy.RulePlan, model *intent.ArchitectureModel, ctx *TypedContext) (policy.ConflictReport, error) {
	report := policy.DetectConflicts(conflictRefs(plannedObligations(plans, model, ctx)), ctx.Precedences)
	if len(report.Conflicts) == 0 {
		return report, nil
	}

	return report, diag.Fail(diag.POL017, diag.Detail(conflictText(report.Conflicts)))
}

// conflictText names each conflict: both rules and the subject (and environment).
func conflictText(conflicts []policy.Conflict) string {
	parts := make([]string, len(conflicts))

	for i := range conflicts {
		c := &conflicts[i]
		place := "subject " + c.Subject

		if c.Environment != "" {
			place += " in environment " + c.Environment
		}

		parts[i] = fmt.Sprintf("rule %s (requires or allows) and rule %s (forbids) contradict for %s on the relation %s; no precedence ranks both",
			c.Affirmative, c.Negative, place, c.Relation)
	}

	return strings.Join(parts, "; ")
}

// typedEvaluation holds the obligation results of all typed rules of a run.
type typedEvaluation struct {
	byRule      map[string][]ObligationResult
	diagnostics []diag.Diagnostic
}

// evaluateTyped builds and evaluates the obligations of the typed plans together, resolves the conflicts that a
// precedence list decides, and keeps the others visible as errors: no rule wins by order. (`check` stops on them
// before it evaluates; this is the guard for other callers of Evaluate.)
func evaluateTyped(input *EvaluateInput, plans []policy.RulePlan) *typedEvaluation {
	evaluation := &typedEvaluation{byRule: map[string][]ObligationResult{}}
	planned := plannedObligations(plans, input.Model, &input.Typed)

	if len(planned) == 0 {
		return evaluation
	}

	results := NewObligationEvaluator(input).EvaluateAll(planned)
	applyPrecedenceReport(results, policy.DetectConflicts(conflictRefs(planned), input.Typed.Precedences))

	families := map[string]struct{}{}

	for i := range results {
		res := &results[i]
		evaluation.byRule[res.RuleID] = append(evaluation.byRule[res.RuleID], *res)

		if res.Reason == ReasonUnsupportedFamily {
			if family, ok := observation.FamilyForKind(res.Relation.Kind); ok {
				families[family] = struct{}{}
			}
		}
	}

	evaluation.diagnostics = unsupportedFamilyDiagnostics(families)

	return evaluation
}

// applyPrecedenceReport marks the obligations that a precedence entry supersedes, and turns the obligations of
// a conflict that no precedence ranks into errors.
func applyPrecedenceReport(results []ObligationResult, report policy.ConflictReport) {
	index := make(map[string]int, len(results))
	for i := range results {
		index[results[i].ID] = i
	}

	for _, loss := range report.Losses {
		if i, ok := index[loss.ObligationID]; ok {
			results[i].SupersededBy = append(results[i].SupersededBy, loss.By)
		}
	}

	for i := range report.Conflicts {
		c := &report.Conflicts[i]
		text := conflictText([]policy.Conflict{*c})

		for _, id := range []string{c.AffirmativeObligation, c.NegativeObligation} {
			if j, ok := index[id]; ok && results[j].Verdict != VerdictError {
				results[j].Verdict, results[j].Reason, results[j].Detail = VerdictError, "", diag.New(diag.POL017, diag.Detail(text)).Message
			}
		}
	}
}

// unsupportedFamilyDiagnostics gives one CON031 diagnostic for each family, sorted by name.
func unsupportedFamilyDiagnostics(families map[string]struct{}) []diag.Diagnostic {
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}

	slices.SortFunc(names, canonjson.CompareUTF16)

	if len(names) == 0 {
		return nil
	}

	out := make([]diag.Diagnostic, 0, len(names))

	for _, name := range names {
		out = append(out, diag.New(diag.CON031, diag.Params{"family": name}))
	}

	return out
}

// ruleResult gives the result of a typed rule and its findings. The verdict of the rule is the worst verdict of its
// obligations that no precedence entry supersedes (error, violation, unknown, pass); it is `not-applicable` when
// every obligation is not applicable, there is none, or all are superseded. Every obligation result stays in the result.
func (t *typedEvaluation) ruleResult(plan *policy.RulePlan, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration) (RuleResult, []Finding) {
	obligations := t.byRule[plan.RuleID]
	base := RuleResult{
		RuleID: plan.RuleID, Name: plan.Name, Severity: plan.Severity, Policy: plan.Pack, Family: plan.Predicate.Family,
		Subjects: nonNilStrings(plan.Subjects), Targets: nonNilStrings(plan.Targets), Plan: *plan,
		Reasons: []Reason{}, Obligations: obligations,
	}

	// The mode of an obligation is the mode of its rule in the environment of the obligation. The mode of the rule
	// is the strongest of the obligations that count; the gates read the obligation modes.
	cfg := &input.Enforcement
	base.Enforcement = cfg.Resolve(plan, "").Mode

	for i := range obligations {
		obligations[i].Enforcement = cfg.Resolve(plan, obligations[i].Environment).Mode
	}

	if code, skip := notApplicable(plan); skip || len(obligations) == 0 {
		if !skip {
			code = diag.CON022
		}

		base.Verdict = VerdictNotApplicable
		base.Reasons = []Reason{{Code: code, Message: diag.New(code, nil).Message}}

		return base, nil
	}

	findings := t.countObligations(&base, plan, input, nodesByID)
	findings = dedupeFindings(findings)
	base.FindingCount = len(findings)

	return base, findings
}

// countObligations folds the obligations that no precedence entry supersedes into the verdict, the reasons, and
// the enforcement mode of the rule result, and gives their findings.
func (t *typedEvaluation) countObligations(base *RuleResult, plan *policy.RulePlan, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration) []Finding {
	var (
		verdicts []RuleVerdict
		findings []Finding
	)

	counted := policy.EnforcementShadow

	for i := range base.Obligations {
		res := &base.Obligations[i]

		if res.Superseded() {
			if base.SupersededBy == nil {
				by := res.SupersededBy[0]
				base.SupersededBy = &by
			}

			continue
		}

		counted = Stronger(counted, res.Enforcement)

		verdicts = append(verdicts, res.Verdict)
		base.Reasons = appendReason(base.Reasons, res)
		findings = append(findings, t.findings(plan, res, input, nodesByID)...)
	}

	base.Verdict = WorstVerdict(verdicts)

	if len(verdicts) > 0 {
		base.Enforcement = counted
		return findings
	}

	by := base.SupersededBy
	base.Reasons = []Reason{{Code: reasonSuperseded, Message: fmt.Sprintf("superseded by rule %s (precedence %s version %d)", by.RuleID, by.PrecedenceID, by.PrecedenceVersion)}}

	return findings
}

// appendReason adds the reason of an unknown obligation (the closed code and the detail), or the error detail.
func appendReason(reasons []Reason, res *ObligationResult) []Reason {
	var reason Reason

	switch res.Verdict {
	case VerdictUnknown:
		reason = Reason{Code: string(res.Reason), Message: res.Detail}
	case VerdictError:
		reason = Reason{Code: diag.CON099, Message: res.Detail}
	case VerdictPass, VerdictViolation, VerdictNotApplicable:
		return reasons
	}

	if slices.Contains(reasons, reason) {
		return reasons
	}

	return append(reasons, reason)
}

// findings gives the findings of one obligation: a finding for each decisive violation, and a binding gap
// finding (kind binding-gap, severity info, next action "resolve binding") for each fact whose endpoint has no
// binding. An `unknown` obligation that has no binding gap finding gets one finding of kind obligation-unknown
// that shows the reason, the missing evidence or coverage, and the next action (unless its rule ignores unknown
// results). The fact stays in the evidence in every case.
func (t *typedEvaluation) findings(plan *policy.RulePlan, res *ObligationResult, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration) []Finding {
	var out []Finding

	gap := false

	for i := range res.Occurrences {
		occ := &res.Occurrences[i]

		switch {
		case isDecisiveViolation(occ):
			out = append(out, typedFinding(plan, res, occ, input, nodesByID))
		case isBoundGap(occ):
			out = append(out, typedFinding(plan, res, occ, input, nodesByID))
			gap = true
		}
	}

	if res.Verdict == VerdictUnknown && !gap && plan.EvidenceRequirements.OnUnknown != policy.UnknownIgnore {
		out = append(out, unknownFinding(plan, res, nodesByID))
	}

	return out
}

// isDecisiveViolation says whether the occurrence result is a violation that decides the verdict.
func isDecisiveViolation(occ *OccurrenceResult) bool {
	return occ.Result == VerdictViolation && occ.Decisive
}

// isBoundGap says whether the occurrence result is a binding gap of a fact that has a bound edge.
func isBoundGap(occ *OccurrenceResult) bool {
	return occ.Class == OccurrenceBindingGap && occ.Reason == ReasonBindingGap && occ.bound != nil
}

// unknownFinding makes the finding of an obligation with no definite result. Its severity is `info` and its
// result is `unknown`: it explains the gap and never gates by itself (the verdict of the rule gates).
func unknownFinding(plan *policy.RulePlan, res *ObligationResult, nodesByID map[string]*intent.NodeDeclaration) Finding {
	target := firstOrEmpty(res.Relation.Target)

	finding := Finding{
		RuleID: plan.RuleID, Severity: policy.SeverityInfo, Kind: KindObligationUnknown, Disposition: exceptions.DispositionActive,
		Message:      fmt.Sprintf("obligation %s %s (%s) is unknown (%s): %s", res.Type, res.Subject, environmentText(res.Environment), res.Reason, res.Detail),
		NextAction:   nextActionFor(res.Reason),
		ObligationID: res.ID, Subject: res.Subject, Environment: res.Environment, Result: VerdictUnknown, Reason: res.Reason,
		Expected: obligationExpected(res), Observed: fmt.Sprintf("unknown (%s): %s", res.Reason, res.Detail),
		Enforcement: res.Enforcement, EvidencePath: []EvidenceStep{}, RelatedLocations: []Location{},
		Owner:  ownerOf(plan, res.Subject, nodesByID),
		Source: Endpoint{ArchitectureID: res.Subject}, Target: Endpoint{ArchitectureID: target},
	}

	for i := range res.Occurrences {
		if occ := &res.Occurrences[i]; occ.Result == VerdictUnknown && occ.Location != nil {
			finding.Location = occ.Location

			break
		}
	}

	finding.Fingerprint = TypedFingerprint(&TypedParts{
		Parts: Parts{
			RuleID: plan.RuleID, SourceArchitectureID: res.Subject, TargetArchitectureID: target,
			CanonicalEdgeKind: "unknown:" + string(res.Reason) + ":" + string(res.Relation.Kind),
		},
		Environment: res.Environment,
	})

	return finding
}

// obligationExpected says what the obligation requires.
func obligationExpected(res *ObligationResult) string {
	target := "any target"
	if len(res.Relation.Target) > 0 {
		target = strings.Join(res.Relation.Target, ", ")
	}

	return fmt.Sprintf("%s relation %s %s %s of %s in %s", res.Type, res.Relation.Kind, res.Relation.Direction, target, res.Subject, environmentText(res.Environment))
}

// typedFinding makes the finding of one occurrence result.
func typedFinding(plan *policy.RulePlan, res *ObligationResult, occ *OccurrenceResult, input *EvaluateInput, nodesByID map[string]*intent.NodeDeclaration) Finding {
	finding := Finding{
		RuleID: plan.RuleID, Severity: plan.Severity, Kind: KindObligationViolation, Disposition: exceptions.DispositionActive,
		Message:      fmt.Sprintf("obligation %s %s (%s): %s", res.Type, res.Subject, environmentText(res.Environment), occ.Detail),
		NextAction:   occ.NextAction,
		ObligationID: res.ID, Subject: res.Subject, Environment: res.Environment, Result: occ.Result, Occurrence: occ.Occurrence,
		Enforcement: res.Enforcement, EvidencePath: []EvidenceStep{}, RelatedLocations: []Location{},
		Owner:  ownerOf(plan, res.Subject, nodesByID),
		Source: Endpoint{ArchitectureID: res.Subject}, Target: Endpoint{ArchitectureID: firstOrEmpty(res.Relation.Target)},
		Location: occ.Location,
	}

	parts := Parts{
		RuleID: plan.RuleID, SourceArchitectureID: finding.Source.ArchitectureID, TargetArchitectureID: finding.Target.ArchitectureID,
		CanonicalEdgeKind: string(occ.Kind),
	}

	classifyOccurrence(&finding, &parts, occ)
	attachOccurrenceEvidence(&finding, &parts, occ, input)

	if occ.Location != nil {
		parts.NormalizedRepositoryPath = occ.Location.Path
	}

	finding.Fingerprint = TypedFingerprint(&TypedParts{Parts: parts, Environment: res.Environment, Occurrence: occ.Occurrence})
	finding.Expected = obligationExpected(res)
	finding.Observed = occurrenceObserved(occ)

	if occ.Result == VerdictUnknown {
		finding.Reason = occ.Reason
	}

	return finding
}

// classifyOccurrence sets the kind and severity of the finding, and the canonical edge kind of its
// fingerprint, from the class of the occurrence result.
func classifyOccurrence(finding *Finding, parts *Parts, occ *OccurrenceResult) {
	switch occ.Class {
	case OccurrenceMatching:
	case OccurrenceBindingGap:
		finding.Kind, finding.Severity = KindBindingGap, policy.SeverityInfo
		parts.CanonicalEdgeKind = "binding-gap:" + string(occ.Kind)
	case OccurrenceAbsent:
		finding.Kind = KindObligationAbsent
		parts.CanonicalEdgeKind = "absent:" + string(occ.Kind)
	case OccurrenceWrongDirection, OccurrenceNotPermitted, OccurrenceWrongEnvironment, OccurrenceRoleMismatch, OccurrenceConflict:
		parts.CanonicalEdgeKind = string(occ.Class) + ":" + string(occ.Kind)
	}
}

// attachOccurrenceEvidence sets the endpoints, the edge, and the evidence path of the finding from the bound
// edge of the occurrence, or else the observed node that anchors it.
func attachOccurrenceEvidence(finding *Finding, parts *Parts, occ *OccurrenceResult, input *EvaluateInput) {
	if bound := occ.bound; bound != nil {
		finding.Source = Endpoint{ArchitectureID: cmp.Or(firstOrEmpty(bound.FromIDs), finding.Source.ArchitectureID), Observed: observedRef(&bound.From)}
		finding.Target = Endpoint{ArchitectureID: cmp.Or(firstOrEmpty(bound.ToIDs), finding.Target.ArchitectureID), Observed: observedRef(&bound.To)}
		finding.Edge = edgeRef(bound)
		finding.EvidencePath = edgeSteps(bound)
		parts.SourceArchitectureID, parts.TargetArchitectureID = finding.Source.ArchitectureID, finding.Target.ArchitectureID
		parts.SourceQualifiedName, parts.TargetQualifiedName = bound.From.QualifiedName, bound.To.QualifiedName

		return
	}

	if node, ok := input.Evidence.Nodes[occ.Anchor]; ok {
		finding.Source.Observed = observedRef(&node)
		parts.SourceQualifiedName = node.QualifiedName
	}
}

// occurrenceObserved says what the occurrence result observed: the fact (its kind, its endpoints, and the
// place) and what the evaluation says about it, or the absence and the reason for it.
func occurrenceObserved(occ *OccurrenceResult) string {
	if !hasObservedFact(occ) {
		return fmt.Sprintf("no %s fact observed: %s", occ.Kind, occ.Detail)
	}

	text := fmt.Sprintf("%s fact from %s to %s", occ.Kind, occ.Source.QualifiedName, occ.Target.QualifiedName)

	if occ.Location != nil {
		text += " at " + locationText(occ.Location)
	}

	if occ.Result == VerdictUnknown && occ.Reason != "" {
		text += fmt.Sprintf(" (unknown, %s)", occ.Reason)
	}

	return text + ": " + occ.Detail
}

// hasObservedFact says whether the occurrence result is a fact with both endpoints.
func hasObservedFact(occ *OccurrenceResult) bool {
	return occ.Class != OccurrenceAbsent && occ.Source != nil && occ.Target != nil
}

func environmentText(env string) string {
	if env == "" {
		return "no environment"
	}

	return "environment " + env
}

// dedupeFindings keeps one finding for each fingerprint: the facts that give one fingerprint are one finding, and
// the places of the others are related locations.
func dedupeFindings(findings []Finding) []Finding {
	first := map[string]int{}
	out := make([]Finding, 0, len(findings))

	for i := range findings {
		finding := &findings[i]

		kept, seen := first[finding.Fingerprint]
		if !seen {
			first[finding.Fingerprint] = len(out)
			out = append(out, *finding)

			continue
		}

		if isNewRelatedLocation(&out[kept], finding.Location) {
			out[kept].RelatedLocations = append(out[kept].RelatedLocations, *finding.Location)
			slices.SortFunc(out[kept].RelatedLocations, compareLocations)
		}
	}

	return out
}

// isNewRelatedLocation says whether loc is a place that the kept finding does not show yet, as its own place or as
// a related location.
func isNewRelatedLocation(kept *Finding, loc *Location) bool {
	if loc == nil {
		return false
	}

	if kept.Location != nil && compareLocations(*kept.Location, *loc) == 0 {
		return false
	}

	return !slices.Contains(kept.RelatedLocations, *loc)
}
