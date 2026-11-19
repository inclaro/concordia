package conformance

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	checkverdict "github.com/inclaro/concordia/internal/assurance/checkresult/verdict"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/quality"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/digest"
	"github.com/inclaro/concordia/internal/impact"
)

// The review dependency kinds `verification` and `quality-scenario` come from the assurance evidence (design D27).
// Without an `assurance` key the capability profile does not list them, and a selector of either kind is
// `unsupported-family` as before.

// facetCheck is the check facet of a verification item.
const facetCheck = "check"

// facetIdentity is the identity facet of a quality scenario item. The assessment evaluation turns it into the
// identities of the selector, so it only needs to agree with the form that assessment.Set.Declared writes.
const facetIdentity = "identity"

// facetScenario is the scenario facet of a quality scenario item.
const facetScenario = "scenario"

// Identity prefixes of the items of assurance evidence.
const (
	verificationIdentityPrefix = "check:"
	scenarioIdentityPrefix     = "scenario:"
)

// ReviewAssurance is the assurance evidence that the review facts read: the admitted check results and the loaded
// quality scenarios of the run. nil in ReviewFactsInput means that the workspace has no `assurance` key.
type ReviewAssurance struct {
	// Scenarios are the loaded quality scenarios. nil when the workspace declares no `assurance.quality`.
	Scenarios *quality.Set
	// ChecksUnreadable says why the check result source is not read completely (no source is declared, a glob matched
	// no file, a file could not be read); "" when it is. An empty match of an unreadable source says nothing.
	ChecksUnreadable string
	// ScenariosUnreadable says why there is no scenario source ("" when scenarios were loaded).
	ScenariosUnreadable string
	// CheckResults are the check results that were read with their admissibility. Only the admissible ones are items.
	CheckResults []checkverdict.Admitted
}

// reviewAssurance gives the assurance evidence that the review facts read, nil without an `assurance` key.
func (c *checker) reviewAssurance() *ReviewAssurance {
	state := c.assurance
	if state == nil || state.ev == nil {
		return nil
	}

	out := &ReviewAssurance{Scenarios: state.ev.Scenarios, CheckResults: state.ev.CheckResults}

	if state.ev.Scenarios == nil {
		out.ScenariosUnreadable = "the workspace declares no assurance.quality scenario source"
	}

	out.ChecksUnreadable = checksUnreadable(state.ev, state.cfg != nil && state.cfg.Verification != nil)

	return out
}

// checksUnreadable says why the check result source is not completely read, "" when it is: no source declared, a
// declared glob that matched no file, or a matched file that gave no check result (it was malformed or of another
// predicate type).
func checksUnreadable(evid *evidence.Evidence, declared bool) string {
	if !declared {
		return "the workspace declares no assurance.verification result source"
	}

	var matched []string

	for i := range evid.Globs {
		if evid.Globs[i].Kind == evidence.KindCheckResult {
			matched = append(matched, evid.Globs[i].Paths...)
		}
	}

	if len(matched) == 0 {
		return "no check result file matches assurance.verification.results"
	}

	read := make(map[string]struct{}, len(evid.CheckResults))
	for i := range evid.CheckResults {
		read[evid.CheckResults[i].Path] = struct{}{}
	}

	var unread []string

	for _, path := range matched {
		if _, ok := read[path]; !ok {
			unread = append(unread, path)
		}
	}

	if len(unread) > 0 {
		slices.SortFunc(unread, canonjson.CompareUTF16)

		return "check result file could not be read: " + strings.Join(slices.Compact(unread), ", ")
	}

	return ""
}

// assuranceUnreadable gives why the evidence source of an assurance kind is not completely read, "" when it is (or
// the kind is not an assurance kind).
func (f *ReviewFacts) assuranceUnreadable(kind impact.Kind) string {
	if f.assurance == nil {
		return ""
	}

	if kind == impact.KindVerification {
		return f.assurance.ChecksUnreadable
	}

	if kind == impact.KindQualityScenario {
		return f.assurance.ScenariosUnreadable
	}

	return ""
}

// buildAssurance makes the items of the two assurance kinds.
func (f *ReviewFacts) buildAssurance() {
	if f.assurance == nil {
		return
	}

	f.put(impact.KindVerification, f.verificationItems())
	f.put(impact.KindQualityScenario, f.scenarioItems())
}

// checkKey names a check in an environment.
type checkKey struct{ check, environment string }

// verificationItems makes one item for each check and environment that has an admissible result. An inadmissible
// result is not an item: it is not evidence, so it cannot stand behind an approval. The item holds the covered
// identifiers (`subject`) and, for each admissible result, its outcome and the digest of its statement (`result`), so
// a changed outcome or a new statement changes the digest of the item.
func (f *ReviewFacts) verificationItems() []reviewCandidate {
	type group struct {
		subjects []string
		results  []string
	}

	groups := map[checkKey]*group{}

	for i := range f.assurance.CheckResults {
		admitted := &f.assurance.CheckResults[i]
		if admitted.Result == nil || !admitted.Admission.Admissible {
			continue
		}

		key := checkKey{check: admitted.Result.Check, environment: admitted.Result.Environment}

		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}

		g.subjects = append(g.subjects, admitted.Result.Covers...)
		g.results = append(g.results, admitted.Result.Outcome+" "+admitted.Digest)
	}

	out := make([]reviewCandidate, 0, len(groups))

	for key, g := range groups {
		facets := impact.Facets{facetCheck: {key.check}, facetSubject: g.subjects, "result": g.results}

		out = append(out, reviewCandidate{
			item: f.contract.NewItem(impact.KindVerification, verificationIdentityPrefix+key.check+"@"+key.environment, facets, nil, nil),
			env:  key.environment, calm: slices.Clone(g.subjects),
		})
	}

	return out
}

// scenarioItems makes one item for each loaded quality scenario. `scenario` is the digest of the definition without
// its place in the file, so moving a scenario inside its file changes nothing, and `responseMeasure` is the
// criterion in words, so a trigger names what changed.
func (f *ReviewFacts) scenarioItems() []reviewCandidate {
	if f.assurance.Scenarios == nil {
		return nil
	}

	out := make([]reviewCandidate, 0, len(f.assurance.Scenarios.Scenarios))

	for _, scenario := range f.assurance.Scenarios.Scenarios {
		facets := impact.Facets{
			facetScenario: {scenarioDigest(scenario)}, "responseMeasure": {responseMeasure(scenario)}, facetIdentity: {scenario.ID},
		}

		out = append(out, reviewCandidate{
			item: f.contract.NewItem(impact.KindQualityScenario, scenarioIdentityPrefix+scenario.ID, facets, nil, nil),
			env:  scenario.Environment, calm: slices.Clone(scenario.Scope),
		})
	}

	return out
}

// scenarioDigest is the digest of the canonical JSON of the scenario definition. The location is not part of the
// definition: it changes when the file is edited above the scenario.
func scenarioDigest(scenario *quality.Scenario) string {
	def := *scenario
	def.Location = quality.Location{}
	def.AddressedBy = make([]quality.ADRRef, len(scenario.AddressedBy))

	for i, ref := range scenario.AddressedBy {
		def.AddressedBy[i] = quality.ADRRef{ID: ref.ID}
	}

	data, err := canonjson.Marshal(def)
	if err != nil {
		// A scenario holds strings, numbers, and slices only; encoding cannot fail.
		panic(fmt.Errorf("scenario digest: %w", err))
	}

	return digest.OfBytes(data)
}

// responseMeasure says in words what the scenario measures: the metric and its threshold, the rules, or the review.
func responseMeasure(scenario *quality.Scenario) string {
	crit := &scenario.Criterion

	switch scenario.Mode {
	case quality.ModeMeasured:
		text := crit.Metric + " " + string(crit.Comparison) + " " + strconv.FormatFloat(crit.Threshold, 'g', -1, 64) + " " + crit.Unit
		if crit.MaxAgeDays > 0 {
			text += " (max age " + strconv.Itoa(crit.MaxAgeDays) + " days)"
		}

		return text
	case quality.ModeStructural:
		rules := slices.Clone(crit.Rules)
		slices.SortFunc(rules, canonjson.CompareUTF16)

		return "rules " + strings.Join(rules, ", ")
	case quality.ModeReviewed:
		return "reviewed: subject " + crit.Subject + ", authority " + crit.Authority
	}

	return string(scenario.Mode)
}

// scenarioIdentities gives the identities of a quality scenario selector as the items write them. The assessment
// evaluation selects a scenario with `identity: <scenario id>`, and an item identity is `scenario:<id>`.
func scenarioIdentities(identities []string) []string {
	out := make([]string, len(identities))

	for i, id := range identities {
		out[i] = id
		if !strings.HasPrefix(id, scenarioIdentityPrefix) {
			out[i] = scenarioIdentityPrefix + id
		}
	}

	return out
}
