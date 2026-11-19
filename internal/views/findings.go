package views

import (
	"slices"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/policy"
	"github.com/inclaro/concordia/internal/policy/packs"
)

// The rules of the built-in pack `views` (design D3). The IDs are never reused or renumbered (INT-05).
const (
	RulePresence       = "VIEW-PRES-001"
	RuleFreshness      = "VIEW-FRESH-001"
	RuleCorrespondence = "VIEW-CORR-001"
)

// PackID is the ID of the built-in pack of the view rules (`builtin:views`).
const PackID = "views"

// The finding kinds of the view rules: a view that is absent, stale, or that drifts from the architecture.
const (
	KindViewAbsent conformance.FindingKind = "view-absent"
	KindViewStale  conformance.FindingKind = "view-stale"
	KindViewDrift  conformance.FindingKind = "view-drift"
)

// The checks of a view, as they appear in the fingerprint (`view:<check>:<difference>`) and in the finding messages.
const (
	checkPresence       = "presence"
	checkFreshness      = "freshness"
	checkCorrespondence = "correspondence"
)

// The differences of a presence and a freshness finding, as they appear in the fingerprint.
const (
	differenceAbsent      = "absent"
	differenceMismatch    = "mismatch"
	differenceInvalid     = "manifest-invalid"
	differenceUnknown     = "unknown"
	differenceContract    = "contract"
	differenceInputs      = "inputs"
	differenceManifest    = "manifest"
	differenceArtifact    = "artifact"
	differenceUnavailable = "unavailable"
	differenceStale       = "stale"

	// relatedView is the kind of the related reference of a finding to its view.
	relatedView = "view"

	// subjectRevision is the subject of a presence finding that is about the tracked revision as a whole.
	subjectRevision = "revision"
)

// rule describes one rule of the views pack.
type rule struct {
	id    string
	check string
	kind  conformance.FindingKind
	// name and severity come from the pack text.
	name     string
	severity policy.RuleSeverity
}

var (
	packOnce sync.Once
	packInfo policy.PackInfo
)

// viewPack describes the embedded pack `views`, so that the names, severities and version of the rule results are
// the ones of the pack and cannot drift from it.
func viewPack() *policy.PackInfo {
	packOnce.Do(func() {
		text, ok := packs.Source(PackID)
		if !ok {
			panic("views: the embedded pack views is missing")
		}

		info, problems := policy.DescribePack(policy.PolicyFile{Path: packs.Prefix + PackID, Text: string(text)})
		if len(problems) > 0 {
			panic("views: the embedded pack views is not valid: " + problems[0].Message)
		}

		packInfo = info
	})

	return &packInfo
}

// rules gives the three rules in the order of the checks.
func rules() []rule {
	pack := viewPack()
	table := []rule{
		{id: RulePresence, check: checkPresence, kind: KindViewAbsent},
		{id: RuleFreshness, check: checkFreshness, kind: KindViewStale},
		{id: RuleCorrespondence, check: checkCorrespondence, kind: KindViewDrift},
	}

	for i := range table {
		for _, info := range pack.Rules {
			if info.ID == table[i].id {
				table[i].name, table[i].severity = info.Name, info.Severity
			}
		}
	}

	return table
}

// slug turns a reason phrase into a word that is stable in a fingerprint.
func slug(reason string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(reason)), " ", "-")
}

// ruleTally is what the checks of one rule over all the views gave.
type ruleTally struct {
	verdicts []conformance.RuleVerdict
	subjects []string
	reasons  []conformance.Reason
	findings []conformance.Finding
}

// rulesAndFindings makes the rule results of the three rules and the findings of the non-pass checks.
func rulesAndFindings(outcomes []outcome, onUnknown policy.UnknownDisposition) (rs []conformance.RuleResult, found []conformance.Finding) {
	for _, spec := range rules() {
		tally := tallyRule(&spec, outcomes)

		rs = append(rs, ruleResult(&spec, &tally, onUnknown))
		found = append(found, tally.findings...)
	}

	return rs, found
}

// tallyRule runs the check of a rule over every view.
func tallyRule(spec *rule, outcomes []outcome) ruleTally {
	var tally ruleTally

	for i := range outcomes {
		res := &outcomes[i]
		status, phrases := checkOf(spec.check, res)

		tally.verdicts = append(tally.verdicts, verdictOf(status))

		if status == StatusNotApplicable {
			continue
		}

		tally.subjects = append(tally.subjects, res.View.ID)

		if status != StatusPass {
			tally.reasons = append(tally.reasons, reasonsOfPhrases(res.View.ID, phrases)...)
		}

		tally.findings = append(tally.findings, findingsOf(spec, res)...)
	}

	tally.findings = dedupe(tally.findings)

	return tally
}

// reasonsOfPhrases makes the reasons of a view from the phrases of one of its checks.
func reasonsOfPhrases(viewID string, phrases []string) []conformance.Reason {
	reasons := make([]conformance.Reason, 0, len(phrases))
	for _, phrase := range phrases {
		reasons = append(reasons, conformance.Reason{Code: slug(phrase), Message: viewID + ": " + phrase})
	}

	return reasons
}

// checkOf gives the status and the reason phrases of one check of one view.
func checkOf(check string, o *outcome) (status Status, reasons []string) {
	switch check {
	case checkPresence:
		return o.Presence.Status, o.Presence.Reasons
	case checkFreshness:
		return o.Freshness.Status, o.Freshness.Reasons
	}

	return o.Correspondence.Status, o.Correspondence.Reasons
}

// ruleResult makes the rule result of one rule from what its checks of all the views gave.
func ruleResult(spec *rule, tally *ruleTally, onUnknown policy.UnknownDisposition) conformance.RuleResult {
	pack := viewPack()

	slices.Sort(tally.subjects)

	plan := policy.RulePlan{
		RuleID: spec.id, Name: spec.name, Severity: spec.severity, Pack: policy.PackRef{ID: pack.ID, Version: pack.Version},
		Predicate: policy.Predicate{Family: policy.FamilyView}, Aggregation: policy.AggregationEdge, Family: string(policy.FamilyView),
		EvidenceRequirements: policy.EvidenceRequirements{OnUnknown: onUnknown},
		Subjects:             nonNil(slices.Clone(tally.subjects)), Targets: []string{}, UnsupportedEdgeKinds: []observation.CanonicalEdgeKind{},
		Traversal: policy.Traversal{Direction: "outbound", EdgeKinds: []observation.CanonicalEdgeKind{}},
	}

	return conformance.RuleResult{
		RuleID: spec.id, Name: spec.name, Severity: spec.severity, Policy: plan.Pack, Family: policy.FamilyView,
		Verdict: conformance.WorstVerdict(tally.verdicts), Reasons: nonNil(tally.reasons), Subjects: plan.Subjects,
		Targets: []string{}, Plan: plan, FindingCount: len(tally.findings),
	}
}

// dedupe drops the findings with a fingerprint that an earlier finding has (the same subject and difference
// reached twice). The pipeline sorts the findings of the run.
func dedupe(list []conformance.Finding) []conformance.Finding {
	seen := map[string]bool{}
	out := list[:0]

	for i := range list {
		if seen[list[i].Fingerprint] {
			continue
		}

		seen[list[i].Fingerprint] = true

		out = append(out, list[i])
	}

	return out
}

// primaryPath is the file that a finding of a view points to: the declared source of an authored view, else the
// output of a generated view.
func primaryPath(view *Definition) string {
	switch {
	case view.Source != nil:
		return view.Source.Path
	case view.Output != nil:
		return view.Output.Path
	}

	return ""
}

// findingBuilder holds what the findings of one check of one view share.
type findingBuilder struct {
	rule *rule
	view *Definition
}

// viewFinding is what a finding of a view says: its result, its subject and difference (they make the
// fingerprint with the path), the next action, and what was expected and observed.
type viewFinding struct {
	result     conformance.RuleVerdict
	subject    string
	difference string
	path       string
	action     string
	expected   string
	observed   string
}

// about gives a copy of the finding with the subject, the difference and the observed text set.
func (v *viewFinding) about(subject, difference, observed string) *viewFinding {
	out := *v
	out.subject, out.difference, out.observed = subject, difference, observed

	return &out
}

// finding makes one finding. The fingerprint (design D3) is the fingerprint function with the rule, the view ID
// as the source, the subject as the target, the kind `view:<check>:<difference>`, and the path. It has no provider
// ordinal, no timestamp and no digest, so a regenerated artifact keeps the identity of its findings.
func (b *findingBuilder) finding(spec *viewFinding) conformance.Finding {
	owner := b.view.Owner

	return conformance.Finding{
		Owner:            &owner,
		Fingerprint:      b.fingerprint(spec),
		RuleID:           b.rule.id,
		Severity:         policy.RuleSeverity(b.view.EffectiveSeverity()),
		Kind:             b.rule.kind,
		Message:          "view " + b.view.ID + ": " + spec.observed,
		Source:           conformance.Endpoint{ArchitectureID: b.view.ID},
		Target:           conformance.Endpoint{ArchitectureID: spec.subject},
		EvidencePath:     []conformance.EvidenceStep{},
		RelatedLocations: []conformance.Location{},
		Location:         &conformance.Location{Path: spec.path},
		NextAction:       spec.action,
		Result:           spec.result,
		Expected:         spec.expected,
		Observed:         spec.observed,
		Area:             conformance.AreaDiagramCurrency,
		Related:          []conformance.RelatedRef{{Kind: relatedView, ID: b.view.ID}},
	}
}

// fingerprint is the identity of a finding.
func (b *findingBuilder) fingerprint(spec *viewFinding) string {
	return conformance.Fingerprint(conformance.Parts{
		RuleID: b.rule.id, SourceArchitectureID: b.view.ID, TargetArchitectureID: spec.subject,
		CanonicalEdgeKind: "view:" + b.rule.check + ":" + spec.difference, NormalizedRepositoryPath: spec.path,
	})
}

// findingsOf gives the findings of one check of one view. A pass and a not-applicable check have none; an `error`
// check has none either: it is the `error` verdict of its rule, with the reason in the rule result.
func findingsOf(r *rule, o *outcome) []conformance.Finding {
	b := &findingBuilder{rule: r, view: o.View}

	switch r.check {
	case checkPresence:
		return b.presence(o)
	case checkFreshness:
		return b.freshness(o)
	}

	return b.correspondence(o)
}

// resultOfStatus gives the result of a finding for a check status: `violation`, or `unknown`.
func resultOfStatus(status Status) conformance.RuleVerdict {
	if status == StatusUnknown {
		return conformance.VerdictUnknown
	}

	return conformance.VerdictViolation
}

func presenceAction(view *Definition, p *PresenceResult) string {
	switch p.Status {
	case StatusViolation:
		if view.Origin == OriginGenerated {
			return NextActionRegenerateView
		}

		return NextActionSupplyEvidence
	case StatusUnknown, StatusError:
		return NextActionFixAnalysis
	case StatusPass, StatusNotApplicable:
	}

	return ""
}

func freshnessAction(f *FreshnessResult) string {
	switch f.Status {
	case StatusViolation:
		return NextActionRegenerateView
	case StatusUnknown:
		if slices.Contains(f.Reasons, ReasonCommittedUnavailable) {
			return NextActionRegenerateView
		}

		return NextActionFixAnalysis
	case StatusError:
		return NextActionFixAnalysis
	case StatusPass, StatusNotApplicable:
	}

	return ""
}

// presenceDifference classes the reason of one file into the difference of the fingerprint.
func presenceDifference(reason string) string {
	switch reason {
	case ReasonViewIDMismatch, ReasonScopeMismatch:
		return differenceMismatch
	case ReasonManifestInvalid:
		return differenceInvalid
	}

	return differenceAbsent
}

func (b *findingBuilder) presence(res *outcome) []conformance.Finding {
	pres := &res.Presence
	if !reportsFinding(pres.Status) {
		return nil
	}

	base := &viewFinding{result: resultOfStatus(pres.Status), action: presenceAction(res.View, pres), path: primaryPath(res.View)}

	if pres.Status == StatusUnknown {
		base.expected = "the declared files of the view are listed in the tracked Git revision"

		return []conformance.Finding{b.finding(base.about(subjectRevision, differenceUnknown, "presence is unknown: "+strings.Join(pres.Reasons, "; ")))}
	}

	out := b.presenceFileFindings(pres, base)
	if len(out) == 0 {
		base.expected = "the declared files of the view are in the tracked revision"
		out = append(out, b.finding(base.about(subjectRevision, differenceAbsent, "presence: "+strings.Join(pres.Reasons, "; "))))
	}

	return out
}

// presenceFileFindings gives the finding of each declared file that has a reason of absence.
func (b *findingBuilder) presenceFileFindings(pres *PresenceResult, base *viewFinding) []conformance.Finding {
	var out []conformance.Finding

	for i := range pres.Files {
		file := &pres.Files[i]
		if file.Reason == "" {
			continue
		}

		observed := file.Role + " " + file.Path + ": " + file.Reason
		if file.Detail != "" {
			observed += " (" + file.Detail + ")"
		}

		spec := base.about(file.Role, presenceDifference(file.Reason), observed)
		spec.path = file.Path
		spec.expected = "the " + file.Role + " " + file.Path + " is in revision " + pres.Revision + " and names the view"

		out = append(out, b.finding(spec))
	}

	return out
}

// reportsFinding says whether a check status can have findings: only a violation and an unknown do.
func reportsFinding(status Status) bool {
	return status == StatusViolation || status == StatusUnknown
}

func (b *findingBuilder) freshness(res *outcome) []conformance.Finding {
	fresh := &res.Freshness
	if !reportsFinding(fresh.Status) {
		return nil
	}

	base := &viewFinding{
		result:   resultOfStatus(fresh.Status),
		path:     res.View.ManifestPath(),
		action:   freshnessAction(fresh),
		expected: "the committed artifacts equal what Concordia regenerates from the inputs of this run",
	}

	var out []conformance.Finding

	if fresh.Status == StatusViolation {
		out = b.freshnessViolations(fresh, base)
	}

	out = append(out, b.artifactFindings(fresh, base)...)

	if len(out) == 0 {
		difference := differenceStale
		if fresh.Status == StatusUnknown {
			difference = differenceUnavailable
		}

		spec := base.about(res.View.ID, difference, "freshness: "+strings.Join(fresh.Reasons, "; "))
		spec.path = primaryPath(res.View)

		out = append(out, b.finding(spec))
	}

	return out
}

// freshnessViolations gives the findings of the manifest of a stale view: the digest contract, the inputs and
// the manifest itself.
func (b *findingBuilder) freshnessViolations(fresh *FreshnessResult, base *viewFinding) []conformance.Finding {
	var out []conformance.Finding

	if fresh.ContractChanged {
		out = append(out, b.finding(base.about("digest-contract", differenceContract, ReasonContractChanged)))
	}

	if len(fresh.DifferingDigests) > 0 {
		observed := ReasonInputsChanged + ": " + strings.Join(fresh.DifferingDigests, ", ")
		out = append(out, b.finding(base.about("inputs", differenceInputs, observed)))
	}

	if len(fresh.ManifestDifferences) > 0 {
		observed := ReasonManifestDiffers + ": " + strings.Join(fresh.ManifestDifferences, ", ")
		out = append(out, b.finding(base.about("manifest", differenceManifest, observed)))
	}

	return out
}

// artifactFindings gives the finding of each artifact that has the status of the whole check.
func (b *findingBuilder) artifactFindings(fresh *FreshnessResult, base *viewFinding) []conformance.Finding {
	var out []conformance.Finding

	for i := range fresh.Artifacts {
		art := &fresh.Artifacts[i]
		if art.Status != fresh.Status {
			continue
		}

		difference := differenceArtifact
		if art.Status == StatusUnknown {
			difference = differenceUnavailable
		}

		spec := base.about(art.Path, difference, artifactObserved(art))
		spec.path = art.Path

		out = append(out, b.finding(spec))
	}

	return out
}

func artifactObserved(a *ArtifactFreshness) string {
	text := "artifact " + a.Path + ": " + strings.Join(a.Reasons, "; ")
	if a.Detail != "" {
		text += " (" + a.Detail + ")"
	}

	return text
}

func (b *findingBuilder) correspondence(res *outcome) []conformance.Finding {
	corr := &res.Correspondence
	if !reportsFinding(corr.Status) {
		return nil
	}

	source := corr.Source.Path
	if source == "" {
		source = primaryPath(res.View)
	}

	base := &viewFinding{
		path:     source,
		expected: "the diagram shows the elements and relationships of the " + strings.Join(corr.Against, " and ") + " architecture",
	}

	return append(b.differenceFindings(corr, base), b.unknownFindings(res.View, corr, base)...)
}

// differenceFindings gives the finding of each difference between the diagram and the architecture.
func (b *findingBuilder) differenceFindings(corr *CorrespondenceResult, base *viewFinding) []conformance.Finding {
	out := make([]conformance.Finding, 0, len(corr.Differences))

	for i := range corr.Differences {
		diff := &corr.Differences[i]
		observed := diff.Kind + " " + diff.Subject + " " + diff.Key()

		if diff.Detail != "" {
			observed += " (" + diff.Detail + ")"
		}

		spec := *base
		spec.result, spec.action = conformance.VerdictViolation, NextActionCorrectIntent
		spec.subject, spec.difference, spec.observed = diff.Key(), diff.Kind, observed

		finding := b.finding(&spec)
		placeAt(&finding, diff.Locations)

		out = append(out, finding)
	}

	return out
}

// unknownFindings gives the finding of each subject that the correspondence could not compare.
func (b *findingBuilder) unknownFindings(view *Definition, corr *CorrespondenceResult, base *viewFinding) []conformance.Finding {
	out := make([]conformance.Finding, 0, len(corr.Unknown))

	for i := range corr.Unknown {
		gap := &corr.Unknown[i]

		subject := gap.Subject
		if subject == "" {
			subject = view.ID
		}

		spec := *base
		spec.result, spec.action = conformance.VerdictUnknown, unknownReasonAction(gap.Reason)
		spec.subject, spec.difference = subject, differenceUnknown+":"+slug(gap.Reason)
		spec.observed = "correspondence is unknown: " + gap.Reason + " (" + subject + ")" + unknownHint(gap.Reason)

		finding := b.finding(&spec)
		placeAt(&finding, gap.Locations)

		out = append(out, finding)
	}

	return out
}

// placeAt puts the first place of the diagram that shows the subject on the finding, and the others in its
// related locations.
func placeAt(finding *conformance.Finding, places []Location) {
	if len(places) == 0 {
		return
	}

	finding.Location = &conformance.Location{Path: places[0].Path, Line: places[0].Line}

	for _, p := range places[1:] {
		finding.RelatedLocations = append(finding.RelatedLocations, conformance.Location{Path: p.Path, Line: p.Line})
	}
}

// unknownHint adds what to do about a reason that has a known remedy in the configuration.
func unknownHint(reason string) string {
	if reason == ReasonStructureNotAvailable {
		return ": the provider keeps diagram evidence only in index mode `full` (observation.indexMode)"
	}

	return ""
}
