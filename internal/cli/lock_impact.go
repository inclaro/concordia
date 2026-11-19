package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/conformance"
	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

// digestImpactPath is the workspace-relative path of the digest-impact report that `lock update` writes.
const digestImpactPath = "build/concordia/digest-impact.json"

// impactSchemaVersion is the `schemaVersion` of the digest-impact report.
const impactSchemaVersion = "1.0"

// listPreview is the number of names that the text summary prints for a list before it says "and N more".
const listPreview = 8

// Components of the toolchain that a digest-impact report names.
const (
	componentCBM           = "cbm-build"
	componentExecutable    = "cbm-executable"
	componentAdapter       = "adapter-schema"
	componentProfile       = "capability-profile"
	componentCalm          = "calm-schema"
	componentNormalization = "normalization-version"
)

// unrecorded stands for a value that the lock does not record.
const unrecorded = "unrecorded"

// noProfile stands for a build to which no capability profile applies.
const noProfile = "none"

// Family changes between two capability profiles.
const (
	changeAdded     = "added"
	changeImproved  = "improved"
	changeReduced   = "reduced"
	changeRemoved   = "removed"
	changeMaturity  = "maturity-changed"
	changeLossModes = "loss-modes-changed"
)

// Ranks of the support levels.
const (
	levelRankNone = iota
	levelRankPartial
	levelRankFull
)

// upgradeImpact is the digest-impact report of `lock update` (MIG-05).
type upgradeImpact struct {
	// Latest is what the latest recorded run shows; nil when no run is recorded.
	Latest        *latestRunImpact  `json:"latestRun"`
	SchemaVersion string            `json:"schemaVersion"`
	Changes       []toolchainChange `json:"changes"`
	Capabilities  *capabilityImpact `json:"capabilities,omitempty"`
	Equivalence   equivalence       `json:"equivalence"`
}

// toolchainChange is one component of the toolchain whose pinned value changed.
type toolchainChange struct {
	Component string `json:"component"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// capabilityImpact is the capability diff between the locked profile and the new one.
type capabilityImpact struct {
	Locked  *observation.ProfileRef `json:"locked"`
	Current *observation.ProfileRef `json:"current"`
	// Note says why the diff is not available.
	Note string `json:"note,omitempty"`
	// Families are the families whose support differs.
	Families []familyDelta `json:"families"`
	// Regressions are the families that the locked profile supported and the new one does not.
	Regressions []observation.CapabilityRegression `json:"regressions"`
	// Fixtures are the recordings that validate the changed families of the new profile.
	Fixtures []string `json:"fixtures"`
	// ProfileFixtures are all recordings that the new profile names.
	ProfileFixtures []string `json:"profileFixtures"`
	// Comparable is false when a profile definition is not available in this build.
	Comparable bool `json:"comparable"`
}

// familyDelta is the change of support of one family.
type familyDelta struct {
	Family       string   `json:"family"`
	Change       string   `json:"change"`
	WasLevel     string   `json:"wasLevel"`
	NowLevel     string   `json:"nowLevel"`
	WasMaturity  string   `json:"wasMaturity"`
	NowMaturity  string   `json:"nowMaturity"`
	Fixtures     []string `json:"fixtures"`
	WasLossModes []string `json:"wasLossModes,omitempty"`
	NowLossModes []string `json:"nowLossModes,omitempty"`
}

// digestChange is one digest of the latest run before and after the upgrade.
type digestChange struct {
	Name string `json:"name"`
	// Before is empty when the latest run does not record the digest.
	Before string `json:"before"`
	After  string `json:"after"`
	// Recorded is false when the latest run has no value to compare with.
	Recorded bool `json:"recorded"`
	Changed  bool `json:"changed"`
}

// findingRef names a finding of a run.
type findingRef struct {
	RuleID      string `json:"ruleId"`
	Fingerprint string `json:"fingerprint"`
}

// verdictChange is a rule whose verdict differs between the latest run and the re-evaluation.
type verdictChange struct {
	RuleID string `json:"ruleId"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// latestRunImpact compares the latest recorded run with the same run evaluated with the new toolchain.
type latestRunImpact struct {
	Path  string `json:"path"`
	RunID string `json:"runId"`
	// Reason says why the new toolchain could not evaluate the workspace (Reevaluated is false).
	Reason string `json:"reason,omitempty"`
	// Digests are the digests that change for the latest run: normalizedGraph, findings, architecture, policy.
	Digests []digestChange `json:"digests"`
	// FindingsRemoved are the findings of the latest run whose fingerprint the new toolchain does not produce.
	FindingsRemoved []findingRef `json:"findingsRemoved"`
	// FindingsAdded are the fingerprints that only the new toolchain produces.
	FindingsAdded  []findingRef    `json:"findingsAdded"`
	VerdictChanges []verdictChange `json:"verdictChanges"`
	// Reevaluated is false when the new toolchain could not evaluate the workspace.
	Reevaluated bool `json:"reevaluated"`
}

// equivalence says whether the upgrade is shown not to change the results of the latest run.
type equivalence struct {
	Statement string `json:"statement"`
	// Reasons say what stops the report from showing equivalence.
	Reasons []string `json:"reasons"`
	Shown   bool     `json:"shown"`
}

const (
	statementShown    = "equivalence shown for the latest run"
	statementNotShown = "equivalence not shown"
)

// runSnapshot is what the report compares between two evaluations of one workspace.
type runSnapshot struct {
	Verdicts     map[string]string
	RunID        string
	Architecture string
	Graph        string
	Findings     []findingRef
	Policies     []conformance.InputRef
}

// toolchainChanges lists the pinned components that differ between two locks.
func toolchainChanges(before, after *workspace.LockResolved) []toolchainChange {
	profileText := func(p *workspace.LockProfile) string {
		if p == nil {
			return noProfile
		}

		return p.ID + " (" + p.Digest + ")"
	}

	orUnrecorded := func(v string) string {
		if v == "" {
			return unrecorded
		}

		return v
	}

	pairs := []toolchainChange{
		{componentCBM, before.CBM.ExecutableVersion, after.CBM.ExecutableVersion},
		{componentExecutable, orUnrecorded(before.CBM.SHA256), orUnrecorded(after.CBM.SHA256)},
		{componentAdapter, before.CBM.AdapterSchema, after.CBM.AdapterSchema},
		{componentProfile, profileText(before.CBM.Profile), profileText(after.CBM.Profile)},
		{componentCalm, before.Calm.SchemaVersion, after.Calm.SchemaVersion},
		{componentNormalization, orUnrecorded(before.CBM.NormalizationVersion), orUnrecorded(after.CBM.NormalizationVersion)},
	}

	var out []toolchainChange

	for _, p := range pairs {
		if p.From != p.To {
			out = append(out, p)
		}
	}

	return out
}

func levelRank(level observation.SupportLevel) int {
	if level == observation.SupportSupported {
		return levelRankFull
	}

	if level == observation.SupportPartial {
		return levelRankPartial
	}

	return levelRankNone
}

// classifyFamily names how the support of a family differs, or "" when it does not.
func classifyFamily(was, now *observation.FamilyState) string {
	wasRank, nowRank := levelRank(was.Level), levelRank(now.Level)

	if wasRank == nowRank {
		return classifySameLevel(was, now)
	}

	switch {
	case wasRank == levelRankNone:
		return changeAdded
	case nowRank == levelRankNone:
		return changeRemoved
	case nowRank > wasRank:
		return changeImproved
	default:
		return changeReduced
	}
}

// classifySameLevel names how a family differs when its support level does not: its maturity or its loss modes.
func classifySameLevel(was, now *observation.FamilyState) string {
	switch {
	case was.Maturity != now.Maturity:
		return changeMaturity
	case !slices.Equal(sortedStrings(was.LossModes), sortedStrings(now.LossModes)):
		return changeLossModes
	default:
		return ""
	}
}

func sortedStrings(in []string) []string {
	out := slices.Clone(in)
	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// familiesOf gives the family states of a profile (nil: a build that has none).
func familiesOf(profile *observation.CapabilityProfile) map[string]observation.FamilyState {
	if profile == nil {
		return observation.NoProfileFamilies()
	}

	return observation.DeclaredFamilies(profile)
}

// capabilityImpactBetween compares two capability profiles; nil stands for a build that has none.
func capabilityImpactBetween(locked, current *observation.CapabilityProfile) *capabilityImpact {
	out := &capabilityImpact{
		Locked: locked.Ref(), Current: current.Ref(), Comparable: true,
		Families: []familyDelta{}, Regressions: []observation.CapabilityRegression{}, Fixtures: []string{}, ProfileFixtures: []string{},
	}

	newFamilies := familiesOf(current)
	out.Families, out.Fixtures = familyDeltasBetween(familiesOf(locked), newFamilies, current)

	if current != nil {
		out.ProfileFixtures = profileFixtureNames(current)
	}

	if regressions := observation.CapabilityRegressions(locked, current.Ref(), newFamilies); regressions != nil {
		out.Regressions = regressions
	}

	return out
}

// familyStateOrMissing gives the state of a family, or the state of a family that the profile does not declare.
func familyStateOrMissing(families map[string]observation.FamilyState, name string) observation.FamilyState {
	if state, ok := families[name]; ok {
		return state
	}

	return observation.FamilyState{Level: observation.SupportUnsupported, Maturity: observation.MaturityAdvisory}
}

// familyDeltasBetween gives the families whose support differs, and the sorted fixtures that they name.
func familyDeltasBetween(oldFamilies, newFamilies map[string]observation.FamilyState, current *observation.CapabilityProfile) (deltas []familyDelta, fixtures []string) {
	deltas = []familyDelta{}
	named := map[string]bool{}

	for _, name := range sortedKeyUnion(oldFamilies, newFamilies) {
		was, now := familyStateOrMissing(oldFamilies, name), familyStateOrMissing(newFamilies, name)

		delta, changed := familyDeltaOf(name, &was, &now, current)
		if !changed {
			continue
		}

		for _, f := range delta.Fixtures {
			named[f] = true
		}

		deltas = append(deltas, delta)
	}

	return deltas, sortedKeys(named)
}

// profileFixtureNames gives the sorted fixtures that any family of the profile names.
func profileFixtureNames(profile *observation.CapabilityProfile) []string {
	all := map[string]bool{}

	for i := range profile.Families {
		for _, f := range profile.Families[i].Fixtures {
			all[f] = true
		}
	}

	return sortedKeys(all)
}

// sortedKeyUnion gives the keys of both maps, sorted and without repeats.
func sortedKeyUnion[V any](first, second map[string]V) []string {
	names := make([]string, 0, len(first)+len(second))
	for name := range first {
		names = append(names, name)
	}

	for name := range second {
		if _, ok := first[name]; !ok {
			names = append(names, name)
		}
	}

	slices.SortFunc(names, canonjson.CompareUTF16)

	return names
}

// familyDeltaOf gives the change of one family. The bool is false when the support does not differ.
func familyDeltaOf(name string, was, now *observation.FamilyState, current *observation.CapabilityProfile) (familyDelta, bool) {
	change := classifyFamily(was, now)
	if change == "" {
		return familyDelta{}, false
	}

	delta := familyDelta{
		Family: name, Change: change,
		WasLevel: string(was.Level), NowLevel: string(now.Level),
		WasMaturity: string(was.Maturity), NowMaturity: string(now.Maturity),
		WasLossModes: sortedStrings(was.LossModes), NowLossModes: sortedStrings(now.LossModes),
		Fixtures: []string{},
	}

	if current != nil && change != changeRemoved {
		if support, ok := current.Family(name); ok {
			delta.Fixtures = sortedStrings(support.Fixtures)
		}
	}

	return delta, true
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// capabilityImpactOf resolves the profiles of two locks and compares them. A profile that this build does not ship
// cannot be compared; the report then says so and does not show equivalence.
func capabilityImpactOf(before, after *workspace.LockResolved) *capabilityImpact {
	locked, lockedOK := shippedProfile(before.CBM.Profile)
	current, currentOK := shippedProfile(after.CBM.Profile)

	if lockedOK && currentOK {
		return capabilityImpactBetween(locked, current)
	}

	missing := before.CBM.Profile
	if lockedOK {
		missing = after.CBM.Profile
	}

	return &capabilityImpact{
		Locked: profileRefOf(before.CBM.Profile), Current: profileRefOf(after.CBM.Profile),
		Note: fmt.Sprintf("the capability profile %s (%s) is not shipped with this build: the capability diff is not available",
			missing.ID, missing.Digest),
		Families: []familyDelta{}, Regressions: []observation.CapabilityRegression{}, Fixtures: []string{}, ProfileFixtures: []string{},
	}
}

func profileRefOf(p *workspace.LockProfile) *observation.ProfileRef {
	if p == nil {
		return nil
	}

	return &observation.ProfileRef{ID: p.ID, Digest: p.Digest}
}

// shippedProfile gives the profile definition that a lock profile names. A nil lock profile is "no profile" and is available.
func shippedProfile(p *workspace.LockProfile) (*observation.CapabilityProfile, bool) {
	if p == nil {
		return nil, true
	}

	return observation.LockedProfile(profileRefOf(p))
}

// fingerprintsDigest is the digest of the sorted fingerprints of a run.
func fingerprintsDigest(findings []findingRef) (string, error) {
	prints := make([]string, len(findings))
	for i, f := range findings {
		prints[i] = f.Fingerprint
	}

	slices.SortFunc(prints, canonjson.CompareUTF16)

	data, err := canonjson.Marshal(prints)
	if err != nil {
		return "", fmt.Errorf("digest findings: %w", err)
	}

	return digest.OfBytes(data), nil
}

// policiesDigest is the digest of the policy inputs of a run, sorted by path.
func policiesDigest(policies []conformance.InputRef) (string, error) {
	sorted := slices.Clone(policies)
	slices.SortFunc(sorted, func(a, b conformance.InputRef) int { return canonjson.CompareUTF16(a.Path, b.Path) })

	data, err := canonjson.Marshal(sorted)
	if err != nil {
		return "", fmt.Errorf("digest policies: %w", err)
	}

	return digest.OfBytes(data), nil
}

// compareRuns compares the latest recorded run with its re-evaluation.
func compareRuns(recorded, now *runSnapshot) (*latestRunImpact, error) {
	out := &latestRunImpact{
		RunID: recorded.RunID, Reevaluated: true, Digests: []digestChange{},
		FindingsRemoved: []findingRef{}, FindingsAdded: []findingRef{}, VerdictChanges: []verdictChange{},
	}

	digests, err := digestChanges(recorded, now)
	if err != nil {
		return nil, err
	}

	out.Digests = digests
	out.FindingsRemoved = findingsMissingFrom(recorded.Findings, now.Findings)
	out.FindingsAdded = findingsMissingFrom(now.Findings, recorded.Findings)
	out.VerdictChanges = verdictChanges(recorded.Verdicts, now.Verdicts)

	return out, nil
}

// Names of the digests of the latest run.
const (
	digestGraph        = "normalizedGraph"
	digestFindings     = "findings"
	digestArchitecture = "architecture"
	digestPolicy       = "policy"
)

// digestChanges gives the four digests of the latest run before and after.
func digestChanges(recorded, now *runSnapshot) ([]digestChange, error) {
	beforeFindings, err := fingerprintsDigest(recorded.Findings)
	if err != nil {
		return nil, err
	}

	afterFindings, err := fingerprintsDigest(now.Findings)
	if err != nil {
		return nil, err
	}

	beforePolicy, err := policiesDigest(recorded.Policies)
	if err != nil {
		return nil, err
	}

	afterPolicy, err := policiesDigest(now.Policies)
	if err != nil {
		return nil, err
	}

	digestOf := func(name, before, after string) digestChange {
		return digestChange{Name: name, Before: before, After: after, Recorded: before != "", Changed: before != "" && before != after}
	}

	return []digestChange{
		digestOf(digestGraph, recorded.Graph, now.Graph),
		digestOf(digestFindings, beforeFindings, afterFindings),
		digestOf(digestArchitecture, recorded.Architecture, now.Architecture),
		digestOf(digestPolicy, beforePolicy, afterPolicy),
	}, nil
}

// findingsMissingFrom gives the findings of have whose fingerprint is not in other, sorted by rule and fingerprint.
func findingsMissingFrom(have, other []findingRef) []findingRef {
	known := make(map[string]bool, len(other))
	for _, f := range other {
		known[f.Fingerprint] = true
	}

	out := []findingRef{}

	for _, f := range have {
		if !known[f.Fingerprint] {
			out = append(out, f)
		}
	}

	slices.SortFunc(out, func(a, b findingRef) int {
		if c := canonjson.CompareUTF16(a.RuleID, b.RuleID); c != 0 {
			return c
		}

		return canonjson.CompareUTF16(a.Fingerprint, b.Fingerprint)
	})

	return out
}

// verdictChanges gives the rules whose verdict differs, sorted by rule.
func verdictChanges(recorded, now map[string]string) []verdictChange {
	out := []verdictChange{}

	for _, id := range sortedKeyUnion(recorded, now) {
		if was, after := recorded[id], now[id]; was != after {
			out = append(out, verdictChange{RuleID: id, Before: was, After: after})
		}
	}

	return out
}

// judgeEquivalence says whether the upgrade is shown not to change the results of the latest run.
func judgeEquivalence(capabilities *capabilityImpact, latest *latestRunImpact) equivalence {
	reasons := capabilityReasons(capabilities)

	switch {
	case latest == nil:
		reasons = append(reasons, "no run is recorded to compare with")
	case !latest.Reevaluated:
		reasons = append(reasons, "the latest run could not be evaluated with the new toolchain: "+latest.Reason)
	default:
		reasons = append(reasons, latestReasons(latest)...)
	}

	if len(reasons) == 0 {
		return equivalence{Shown: true, Statement: statementShown, Reasons: []string{}}
	}

	return equivalence{Statement: statementNotShown, Reasons: reasons}
}

// capabilityReasons says what in the capability diff stops the report from showing equivalence.
func capabilityReasons(capabilities *capabilityImpact) []string {
	if capabilities == nil {
		return nil
	}

	var reasons []string

	if !capabilities.Comparable {
		reasons = append(reasons, "the capability profiles cannot be compared")
	}

	if len(capabilities.Regressions) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d families that the locked profile supported are not supported now", len(capabilities.Regressions)))
	}

	return reasons
}

// latestReasons says what in the evaluation of the latest run stops the report from showing equivalence.
func latestReasons(latest *latestRunImpact) []string {
	var reasons []string

	findingsChanged := false

	for i := range latest.Digests {
		switch d := &latest.Digests[i]; {
		case !d.Changed:
		case d.Name == digestFindings:
			findingsChanged = true
		default:
			reasons = append(reasons, "the "+d.Name+" digest changed")
		}
	}

	if findingsChanged {
		reasons = append(reasons, fmt.Sprintf("%d findings were removed and %d added", len(latest.FindingsRemoved), len(latest.FindingsAdded)))
	}

	if len(latest.VerdictChanges) > 0 {
		reasons = append(reasons, fmt.Sprintf("%d rule verdicts changed", len(latest.VerdictChanges)))
	}

	return reasons
}

// latestRunReportPath gives the file of the JSON output of the workspace.
func latestRunReportPath(wsp *workspace.Workspace) string {
	for _, o := range wsp.Report.Outputs {
		if o.Format == workspace.ReportJSON {
			return o.Path
		}
	}

	return workspace.DefaultReportPath
}

// readLatestRun reads the results of the latest run, which are the JSON output of the workspace.
func readLatestRun(wsp *workspace.Workspace) (path string, snap *runSnapshot, err error) {
	path = latestRunReportPath(wsp)

	data, err := os.ReadFile(wsp.ResolveInRoot(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, nil, nil
		}

		return path, nil, fmt.Errorf("read %s: %w", path, err)
	}

	snap, err = parseRunSnapshot(path, data)

	return path, snap, err
}

// parseRunSnapshot takes the comparable parts of the results of a run from their JSON.
func parseRunSnapshot(path string, data []byte) (*runSnapshot, error) {
	var doc struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
		Inputs struct {
			Architecture conformance.InputRef   `json:"architecture"`
			Policies     []conformance.InputRef `json:"policies"`
		} `json:"inputs"`
		Rules []struct {
			RuleID  string `json:"ruleId"`
			Verdict string `json:"verdict"`
		} `json:"rules"`
		Findings []findingRef `json:"findings"`
	}

	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	snap := &runSnapshot{
		RunID: doc.Run.ID, Architecture: doc.Inputs.Architecture.Digest, Policies: doc.Inputs.Policies,
		Findings: doc.Findings, Verdicts: make(map[string]string, len(doc.Rules)),
	}

	for _, r := range doc.Rules {
		snap.Verdicts[r.RuleID] = r.Verdict
	}

	return snap, nil
}

// snapshotOf takes the comparable parts of a run in memory.
func snapshotOf(run *conformance.Run) (*runSnapshot, error) {
	snap := &runSnapshot{
		RunID: run.Run.ID, Architecture: run.Inputs.Architecture.Digest, Policies: run.Inputs.Policies,
		Verdicts: make(map[string]string, len(run.Rules)),
	}

	for i := range run.Findings {
		snap.Findings = append(snap.Findings, findingRef{RuleID: run.Findings[i].RuleID, Fingerprint: run.Findings[i].Fingerprint})
	}

	for i := range run.Rules {
		snap.Verdicts[run.Rules[i].RuleID] = string(run.Rules[i].Verdict)
	}

	if run.Evidence != nil {
		data, err := canonjson.Marshal(run.Evidence)
		if err != nil {
			return nil, fmt.Errorf("digest the normalized graph: %w", err)
		}

		snap.Graph = digest.OfBytes(data)
	}

	return snap, nil
}

// notEvaluated is the entry of a latest run that the new toolchain could not evaluate.
func notEvaluated(path, runID, reason string) *latestRunImpact {
	return &latestRunImpact{
		Path: path, RunID: runID, Reason: reason, Digests: []digestChange{},
		FindingsRemoved: []findingRef{}, FindingsAdded: []findingRef{}, VerdictChanges: []verdictChange{},
	}
}

// latestRunImpactOf evaluates the latest recorded run again with the lock that was just written.
// found is false when no run is recorded. A failed evaluation is recorded in the report, not returned:
// the lock is already updated and the report says that equivalence is not shown.
func latestRunImpactOf(ctx context.Context, app *Context, wsp *workspace.Workspace) (latest *latestRunImpact, found bool, err error) {
	path, recorded, readErr := readLatestRun(wsp)
	if readErr != nil {
		return notEvaluated(path, "", readErr.Error()), true, nil
	}

	if recorded == nil {
		return nil, false, nil
	}

	run, runErr := conformance.RunCheck(ctx, &conformance.CheckOptions{
		Env: checkEnv(app), Deps: checkDeps(app), SkipReports: true, KeepEvidence: true,
	})
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, false, fmt.Errorf("lock update: %w", ctxErr)
		}

		return notEvaluated(path, recorded.RunID, message(runErr)), true, nil
	}

	now, err := snapshotOf(run)
	if err != nil {
		return nil, false, err
	}

	out, err := compareRuns(recorded, now)
	if err != nil {
		return nil, false, err
	}

	out.Path = path

	return out, true, nil
}

// writeUpgradeImpact builds the digest-impact report for the toolchain change that `lock update` just wrote,
// saves it as canonical JSON in the workspace, and prints the text summary.
func writeUpgradeImpact(ctx context.Context, app *Context, wsp *workspace.Workspace, before, after *workspace.LockResolved) error {
	changes := toolchainChanges(before, after)
	if len(changes) == 0 {
		return nil
	}

	impact := &upgradeImpact{SchemaVersion: impactSchemaVersion, Changes: changes}

	if changed := slices.ContainsFunc(changes, func(c toolchainChange) bool { return c.Component == componentProfile }); changed {
		impact.Capabilities = capabilityImpactOf(before, after)
	}

	latest, found, err := latestRunImpactOf(ctx, app, wsp)
	if err != nil {
		return err
	}

	if found {
		impact.Latest = latest
	}

	impact.Equivalence = judgeEquivalence(impact.Capabilities, impact.Latest)

	text, err := canonjson.Marshal(impact)
	if err != nil {
		return fmt.Errorf("encode the digest-impact report: %w", err)
	}

	if err := fsutil.WriteFileAtomic(wsp.ResolveInRoot(digestImpactPath), text); err != nil {
		return diag.Fail(diag.REP001, diag.Params{keyPath: digestImpactPath, keyDetail: err.Error()})
	}

	app.Out(impact.summary())

	return nil
}

// summary is the text summary of the report.
func (u *upgradeImpact) summary() string {
	var out strings.Builder

	out.WriteString("digest impact (" + digestImpactPath + ")\n")

	for _, change := range u.Changes {
		fmt.Fprintf(&out, "  %s: %s -> %s\n", change.Component, change.From, change.To)
	}

	if u.Capabilities != nil {
		u.writeCapabilities(&out, u.Capabilities)
	}

	if u.Latest != nil {
		writeLatest(&out, u.Latest)
	} else {
		out.WriteString("  latest run: none recorded\n")
	}

	out.WriteString("  " + u.Equivalence.Statement + "\n")

	for _, reason := range u.Equivalence.Reasons {
		out.WriteString("    - " + reason + "\n")
	}

	return out.String()
}

func (u *upgradeImpact) writeCapabilities(out *strings.Builder, caps *capabilityImpact) {
	if !caps.Comparable {
		out.WriteString("  capabilities: " + caps.Note + "\n")

		return
	}

	writeFamilyChanges(out, caps.Families)

	for i := range caps.Regressions {
		fmt.Fprintf(out, "  capability regression: %s %s -> %s\n", caps.Regressions[i].Family, caps.Regressions[i].Was, caps.Regressions[i].Now)
	}

	if len(caps.Fixtures) > 0 {
		fmt.Fprintf(out, "  fixtures that validate the changed families: %s\n", preview(caps.Fixtures))
	}

	if len(caps.ProfileFixtures) > 0 {
		fmt.Fprintf(out, "  fixtures of the new profile: %s\n", preview(caps.ProfileFixtures))
	}
}

// writeFamilyChanges lists the changed families by kind of change.
func writeFamilyChanges(out *strings.Builder, families []familyDelta) {
	if len(families) == 0 {
		out.WriteString("  capabilities: no family changes\n")
	}

	byChange := map[string][]string{}
	for i := range families {
		byChange[families[i].Change] = append(byChange[families[i].Change], families[i].Family)
	}

	for _, change := range []string{changeAdded, changeImproved, changeReduced, changeRemoved, changeMaturity, changeLossModes} {
		if names := byChange[change]; len(names) > 0 {
			fmt.Fprintf(out, "  capabilities %s: %s\n", change, preview(names))
		}
	}
}

func writeLatest(out *strings.Builder, latest *latestRunImpact) {
	if !latest.Reevaluated {
		fmt.Fprintf(out, "  latest run %s: not evaluated with the new toolchain: %s\n", latest.RunID, latest.Reason)

		return
	}

	fmt.Fprintf(out, "  latest run %s:\n", latest.RunID)

	for i := range latest.Digests {
		switch d := &latest.Digests[i]; {
		case !d.Recorded:
			fmt.Fprintf(out, "    %s digest: not recorded by the latest run; now %s\n", d.Name, d.After)
		case d.Changed:
			fmt.Fprintf(out, "    %s digest: %s -> %s\n", d.Name, d.Before, d.After)
		default:
			fmt.Fprintf(out, "    %s digest: unchanged\n", d.Name)
		}
	}

	for i := range latest.FindingsRemoved {
		fmt.Fprintf(out, "    fingerprint removed: %s %s\n", latest.FindingsRemoved[i].RuleID, latest.FindingsRemoved[i].Fingerprint)
	}

	for i := range latest.FindingsAdded {
		fmt.Fprintf(out, "    fingerprint added: %s %s\n", latest.FindingsAdded[i].RuleID, latest.FindingsAdded[i].Fingerprint)
	}

	for i := range latest.VerdictChanges {
		v := &latest.VerdictChanges[i]
		fmt.Fprintf(out, "    verdict of %s: %s -> %s\n", v.RuleID, v.Before, v.After)
	}
}

// preview joins the first names of a list.
func preview(names []string) string {
	if len(names) <= listPreview {
		return strings.Join(names, ", ")
	}

	return fmt.Sprintf("%s and %d more", strings.Join(names[:listPreview], ", "), len(names)-listPreview)
}
