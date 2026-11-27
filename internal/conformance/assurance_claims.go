package conformance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/claims"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/provenance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/policy"
)

// LineageEntry is one container occurrence of `assurance.lineage` (spec artifact-provenance, "Provenance results are
// reported and bundled"). It is the result of the exact lineage claim for the occurrence, with the deployment evidence
// next to it, kept as separate facts: the rendered digest, the deployment record, and the workload observation.
type LineageEntry struct {
	// Environment is the declared environment of the rendered occurrence.
	Environment string `json:"environment"`
	// Occurrence names the workload, the container, and the image parts. A tag is shown as written and is never a
	// version.
	Occurrence provenance.OccurrenceRef `json:"occurrence"`
	// Verdict is the result of the exact lineage claim: pass, violation, or unknown.
	Verdict RuleVerdict `json:"verdict"`
	// Reason is the closed reason of an unknown result.
	Reason UnknownReason `json:"reason,omitempty"`
	// Missing lists what an unknown result lacks (`digest`, `provenance`, `source-repository`).
	Missing []string `json:"missing"`
	Detail  string   `json:"detail"`
	// Builder and Source are the builder and the declared source repository of the statement that decided the result.
	// For a violation the builder is the observed evidence.
	Builder string                `json:"builder,omitempty"`
	Source  *provenance.SourceRef `json:"source,omitempty"`
	// Statement is the provenance statement that decided the result.
	Statement *provenance.Ref `json:"statement,omitempty"`
	// TrustContract names the trust contract that the result was reached under, for every verdict.
	TrustContract provenance.ContractRef `json:"trustContract"`
	// Rejected are the statements that name the digest and cannot support the claim, with their reasons.
	Rejected []provenance.Rejected `json:"rejected"`
	// Deployment is what the admissible deployment records and workload observations say about the container.
	Deployment LineageDeployment `json:"deployment"`
}

// LineageDeployment is the deployment evidence of one occurrence.
type LineageDeployment struct {
	DeployedDigest      provenance.DeployedDigest      `json:"deployedDigest"`
	DeploymentSucceeded provenance.DeploymentSucceeded `json:"deploymentSucceeded"`
	RuntimeDrift        provenance.RuntimeDrift        `json:"runtimeDrift"`
	// Rejected are the records and observations of this container that cannot support a claim, with their reasons.
	Rejected []provenance.Rejected `json:"rejected"`
}

// entry is the lineage section line of the occurrence.
func (o *occurrenceEvidence) entry() LineageEntry {
	l := &o.lineage

	return LineageEntry{
		Environment: o.occ.Workload.Environment, Occurrence: l.Occurrence, Verdict: RuleVerdict(l.Verdict), Reason: UnknownReason(l.Reason),
		Missing: slices.Clone(l.Missing), Detail: l.Detail, Builder: l.Builder, Source: l.Source, Statement: l.Statement,
		TrustContract: l.Contract, Rejected: slices.Clone(l.Rejected),
		Deployment: LineageDeployment{
			DeployedDigest: o.deployment.DeployedDigest, DeploymentSucceeded: o.deployment.DeploymentSucceeded,
			RuntimeDrift: o.deployment.RuntimeDrift, Rejected: slices.Clone(o.deployment.Rejected),
		},
	}
}

// claimOutcomes evaluates a rule of the family `assurance-claim` (design D29). The outcomes are per subject of the
// claim: an environment for the rendered claims, a container occurrence for the claims that read lineage and
// deployment statements.
//
// Claims that are evaluated here:
//
//   - `complete-resource-set`, `container-occurrence-versions`, `resolved-selections`: from the rendered evidence of
//     each environment (complete coverage; `resolved-selections` also needs every selector to be of supported syntax).
//   - `exact-lineage`, `deployed-digest`, `deployment-succeeded`, `runtime-drift`: from the provenance evaluators.
//
// The other registered claims (`planned-resources`, `network-isolation`, `cloud-network-isolation`, `rbac-grants`,
// `check-outcome`, `quality-scenario-verdict`) have their own evaluators and rule families, with their own outcome
// per subject; a `require.claim` rule that names one of them has no evaluation here and returns false, so the family
// default applies: `unknown`, never a pass.
func (c *checker) claimOutcomes(plan *policy.RulePlan) ([]ExternalOutcome, bool) {
	claim := plan.Predicate.AssuranceClaim
	if !c.hasAssuranceEvidence() || claim == nil {
		return nil, false
	}

	id, environments := claims.ID(claim.ID), c.claimEnvironments(claim)

	if slices.Contains(renderedClaimIDs, id) {
		return renderedOutcomes(c.assurance.ev, id, environments), true
	}

	if outcome, ok := occurrenceClaims[id]; ok {
		return c.occurrenceOutcomes(environments, outcome), true
	}

	return nil, false
}

// hasAssuranceEvidence says whether the run loaded assurance evidence.
func (c *checker) hasAssuranceEvidence() bool {
	return c.assurance != nil && c.assurance.ev != nil
}

// renderedClaimIDs are the claims that the rendered documents give.
var renderedClaimIDs = []claims.ID{claims.CompleteResourceSet, claims.ContainerOccurrenceVersions, claims.ResolvedSelections}

// occurrenceClaims are the claims that the provenance evaluators give for each container occurrence, with the outcome
// of one occurrence.
var occurrenceClaims = map[claims.ID]func(*occurrenceEvidence) ExternalOutcome{
	claims.ExactLineage:        lineageOutcome,
	claims.DeployedDigest:      deployedDigestOutcome,
	claims.DeploymentSucceeded: deploymentSucceededOutcome,
	claims.RuntimeDrift:        runtimeDriftOutcome,
}

// claimEnvironments gives the environments that a claim rule is evaluated for: the environment that the rule names, or
// else the environments of the run. A run with no declared environment evaluates the environments that the rendered
// sources name; with none of those it has one unnamed environment, which the rendered claims report as missing.
func (c *checker) claimEnvironments(claim *policy.ClaimRequirement) []string {
	switch {
	case claim.Environment != "":
		return c.namedClaimEnvironment(claim)
	case len(c.environments.Selected) > 0:
		return slices.Clone(c.environments.Selected)
	}

	return c.renderedEnvironments()
}

// namedClaimEnvironment gives the environment that the claim names, none when the run excludes it.
func (c *checker) namedClaimEnvironment(claim *policy.ClaimRequirement) []string {
	if slices.Contains(c.environments.Excluded, claim.Environment) {
		return nil
	}

	return []string{claim.Environment}
}

// renderedEnvironments gives the environments that the rendered sources name, sorted, or one unnamed environment.
func (c *checker) renderedEnvironments() []string {
	var out []string

	for _, result := range c.assurance.ev.Renders {
		if !slices.Contains(out, result.Environment) {
			out = append(out, result.Environment)
		}
	}

	if len(out) == 0 {
		return []string{""}
	}

	slices.SortFunc(out, canonjson.CompareUTF16)

	return out
}

// sourcesOf gives the rendered sources of an environment.
func sourcesOf(ev *evidence.Evidence, environment string) []*render.Result {
	var out []*render.Result

	for _, result := range ev.Renders {
		if result.Environment == environment {
			out = append(out, result)
		}
	}

	return out
}

// environmentName names an environment for a detail text.
func environmentName(environment string) string {
	if environment == "" {
		return "the run"
	}

	return "environment " + environment
}

// incompleteSources lists the sources whose coverage is not complete, with their gaps. The detail of an unknown
// outcome shows them, so a user can see which source and which gap keeps the claim from passing.
func incompleteSources(sources []*render.Result) (detail, path string) {
	var parts []string

	for _, source := range sources {
		if source.Coverage == assurance.CoverageComplete {
			continue
		}

		if path == "" {
			path = source.Path
		}

		text := fmt.Sprintf("%s has %s coverage", source.Path, source.Coverage)

		if gaps := gapTexts(source.Gaps); len(gaps) > 0 {
			text += " (" + strings.Join(gaps, "; ") + ")"
		}

		parts = append(parts, text)
	}

	return strings.Join(parts, "; "), path
}

func gapTexts(gaps []assurance.Gap) []string {
	out := make([]string, 0, len(gaps))

	for i := range gaps {
		text := gaps[i].Kind
		if gaps[i].Detail != "" {
			text += ": " + gaps[i].Detail
		}

		out = append(out, text)
	}

	return out
}

// renderedEnvironment gives the outcome of a rendered claim for one environment. Without a rendered source the claim
// is `unknown` with `rendered-evidence-missing`; a partial or failed source is the same, with its gaps as detail: a
// partial resource set never counts as complete evidence.
func renderedEnvironment(claim claims.ID, environment string, sources []*render.Result) ExternalOutcome {
	out := ExternalOutcome{Environment: environment}

	if len(sources) == 0 {
		out.Verdict, out.Reason = VerdictUnknown, ReasonRenderedEvidenceMissing
		out.Detail = "no rendering source is declared for " + environmentName(environment)

		return out
	}

	if detail, path := incompleteSources(sources); detail != "" {
		out.Verdict, out.Reason, out.Detail = VerdictUnknown, ReasonRenderedEvidenceMissing, detail
		out.Location = &Location{Path: path}

		return out
	}

	if claim == claims.ResolvedSelections {
		if selector, found := unsupportedSelector(sources); found {
			out.Verdict, out.Reason = VerdictUnknown, ReasonRenderedEvidenceMissing
			out.Detail = "the selector " + selector.String() + " uses syntax that the renderer does not resolve"

			return out
		}
	}

	resources := 0
	for _, source := range sources {
		resources += source.ResourceCount()
	}

	out.Verdict = VerdictPass
	out.Detail = fmt.Sprintf("%d rendered source(s) of %s are complete (%d resources)", len(sources), environmentName(environment), resources)

	return out
}

func unsupportedSelector(sources []*render.Result) (render.ResourceID, bool) {
	for _, source := range sources {
		for i := range source.Selections {
			if source.Selections[i].Unsupported {
				return source.Selections[i].Selector, true
			}
		}
	}

	return render.ResourceID{}, false
}

// renderedOutcomes gives one outcome for each environment.
func renderedOutcomes(ev *evidence.Evidence, id claims.ID, environments []string) []ExternalOutcome {
	out := make([]ExternalOutcome, 0, len(environments))

	for _, environment := range environments {
		out = append(out, renderedEnvironment(id, environment, sourcesOf(ev, environment)))
	}

	return out
}

// occurrenceOutcomes gives one outcome for each rendered container occurrence of the environments, made by outcome.
// An environment with no rendered source, or with a source that is not complete, adds an `unknown` outcome of its own
// with `rendered-evidence-missing`: its occurrences cannot all be known, so the claim never passes for the
// environment as a whole.
func (c *checker) occurrenceOutcomes(environments []string, outcome func(*occurrenceEvidence) ExternalOutcome) []ExternalOutcome {
	return append(c.occurrenceGaps(environments), c.occurrenceResults(environments, outcome)...)
}

// occurrenceGaps gives the `unknown` outcome of each environment whose occurrences are not all known.
func (c *checker) occurrenceGaps(environments []string) []ExternalOutcome {
	var out []ExternalOutcome

	for _, environment := range environments {
		if gap, found := c.occurrenceGap(environment); found {
			out = append(out, gap)
		}
	}

	return out
}

// occurrenceResults gives the outcome of each rendered container occurrence of the environments.
func (c *checker) occurrenceResults(environments []string, outcome func(*occurrenceEvidence) ExternalOutcome) []ExternalOutcome {
	var out []ExternalOutcome

	claim := c.assurance.claimEvidence()

	for i := range claim.occurrences {
		if occ := &claim.occurrences[i]; slices.Contains(environments, occ.occ.Workload.Environment) {
			out = append(out, occurrenceResult(occ, outcome))
		}
	}

	return out
}

// occurrenceGap gives the `unknown` outcome of an environment whose occurrences are not all known: it has no rendered
// source, or a source that is not complete.
func (c *checker) occurrenceGap(environment string) (ExternalOutcome, bool) {
	sources := sourcesOf(c.assurance.ev, environment)
	if len(sources) == 0 {
		return renderedEnvironment(claims.CompleteResourceSet, environment, sources), true
	}

	detail, path := incompleteSources(sources)
	if detail == "" {
		return ExternalOutcome{}, false
	}

	return ExternalOutcome{
		Environment: environment, Verdict: VerdictUnknown, Reason: ReasonRenderedEvidenceMissing, Location: &Location{Path: path},
		Detail: "the occurrences of " + environmentName(environment) + " are not all known: " + detail,
	}, true
}

// occurrenceResult gives the outcome of one occurrence, named by its workload and container.
func occurrenceResult(occ *occurrenceEvidence, outcome func(*occurrenceEvidence) ExternalOutcome) ExternalOutcome {
	result := outcome(occ)
	result.Environment = occ.occ.Workload.Environment
	result.Subject, result.Occurrence = workloadSubject(&occ.occ.Workload), occurrenceName(occ.occ)

	if occ.location != "" {
		result.Location = &Location{Path: occ.location}
	}

	return result
}

// workloadSubject names the workload of an occurrence: its kind, namespace, and name.
func workloadSubject(id *render.ResourceID) string {
	if id.Namespace == "" {
		return id.Kind + "/" + id.Name
	}

	return id.Kind + "/" + id.Namespace + "/" + id.Name
}

// occurrenceName is the container name; an init or ephemeral container carries its kind, because it may share the
// name of a container.
func occurrenceName(occ *render.ContainerOccurrence) string {
	if occ.Kind == render.KindContainer || occ.Kind == "" {
		return occ.Container
	}

	return occ.Kind + ":" + occ.Container
}

// outcomeOf maps a provenance verdict onto a rule verdict. The reasons of the provenance package are members of the
// closed set; a reason outside it makes ExternalOutcome.normalize report an error, so none is invented here.
func outcomeOf(verdict provenance.Verdict, reason, detail string) ExternalOutcome {
	return ExternalOutcome{Verdict: RuleVerdict(verdict), Reason: UnknownReason(reason), Detail: detail}
}

func lineageOutcome(o *occurrenceEvidence) ExternalOutcome {
	l := &o.lineage

	return outcomeOf(l.Verdict, l.Reason, l.Detail)
}

func deployedDigestOutcome(o *occurrenceEvidence) ExternalOutcome {
	d := &o.deployment.DeployedDigest

	return outcomeOf(d.Verdict, d.Reason, d.Detail)
}

func deploymentSucceededOutcome(o *occurrenceEvidence) ExternalOutcome {
	d := &o.deployment.DeploymentSucceeded

	return outcomeOf(d.Verdict, d.Reason, d.Detail)
}

// runtimeDriftOutcome names every difference with both digests and both sources: Concordia chooses none of them.
func runtimeDriftOutcome(o *occurrenceEvidence) ExternalOutcome {
	d := &o.deployment.RuntimeDrift
	out := outcomeOf(d.Verdict, d.Reason, d.Detail)

	if len(d.Findings) > 0 {
		parts := make([]string, len(d.Findings))
		for i := range d.Findings {
			parts[i] = describeDigest(&d.Findings[i].Observed) + " names " + d.Findings[i].Observed.Digest + " but " +
				describeDigest(&d.Findings[i].Expected) + " names " + d.Findings[i].Expected.Digest
		}

		out.Detail = "drift: " + strings.Join(parts, "; ")
	}

	return out
}

func describeDigest(d *provenance.DigestSource) string {
	if d.Statement == nil {
		return d.Source
	}

	return d.Source + " " + d.Statement.Path
}
