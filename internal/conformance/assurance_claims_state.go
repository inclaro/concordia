package conformance

import (
	"slices"
	"strings"
	"sync"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evidence"
	"github.com/inclaro/concordia/internal/assurance/provenance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/workspace"
)

// lineageCache holds the claim evidence of a run, computed on first use. Its zero value is ready to use. The lineage
// section of the results, the claim rules, and the deployment trace all read the same value, so they cannot disagree,
// and the section does not depend on which rules run.
type lineageCache struct {
	value *claimEvidence
	once  sync.Once
}

// occurrenceEvidence is what the provenance evaluators say about one rendered container occurrence: the exact
// lineage claim and the deployment claims. The deployment records and observations are admitted for the environment
// of the occurrence.
type occurrenceEvidence struct {
	occ *render.ContainerOccurrence
	// location is the repository file of the rendered workload, empty when unknown.
	location   string
	lineage    provenance.LineageResult
	deployment provenance.DeploymentResult
}

// claimEvidence is the evidence of the lineage and deployment claims of every rendered occurrence, sorted.
type claimEvidence struct {
	index       map[deployment.OccurrenceKey]int
	occurrences []occurrenceEvidence
}

// claimEvidence gives the claim evidence of the run. It is computed once.
func (a *assuranceRun) claimEvidence() *claimEvidence {
	a.lineage.once.Do(func() { a.lineage.value = buildClaimEvidence(a.ev, a.cfg, a.ctx) })

	return a.lineage.value
}

// buildClaimEvidence evaluates the claims that read statements for every container occurrence of the rendered
// evidence. A provenance statement names a digest and not an environment, so one admission serves every occurrence.
// A deployment record or observation names an environment, so each environment admits them for itself: a record of
// another environment is then inadmissible with `different-environment` (spec artifact-provenance).
func buildClaimEvidence(loaded *evidence.Evidence, cfg *workspace.Assurance, base assurance.Context) *claimEvidence {
	out := &claimEvidence{occurrences: []occurrenceEvidence{}, index: map[deployment.OccurrenceKey]int{}}
	if loaded == nil {
		return out
	}

	contract := provenance.NewTrustContract(cfg)
	statements := admitProvenance(loaded, cfg, base)
	byEnvironment := map[string]*provenance.Evidence{}

	for _, result := range loaded.Renders {
		if len(result.Occurrences) == 0 {
			continue
		}

		deployed := byEnvironment[result.Environment]
		if deployed == nil {
			deployed = admitDeployments(loaded, cfg, base, result.Environment)
			byEnvironment[result.Environment] = deployed
		}

		locations := make(map[render.ResourceID]string, len(result.Resources))
		for i := range result.Resources {
			locations[result.Resources[i].ID] = result.Resources[i].Location
		}

		for i := range result.Occurrences {
			occ := &result.Occurrences[i]

			out.occurrences = append(out.occurrences, occurrenceEvidence{
				occ: occ, location: locations[occ.Workload],
				lineage: provenance.Lineage(occ, statements, &contract), deployment: provenance.Deployment(occ, deployed),
			})
		}
	}

	slices.SortStableFunc(out.occurrences, func(a, b occurrenceEvidence) int {
		return canonjson.CompareUTF16(occurrenceSortKey(a.occ), occurrenceSortKey(b.occ))
	})

	for i := range out.occurrences {
		occ := out.occurrences[i].occ
		key := deployment.OccurrenceKey{
			Environment: occ.Workload.Environment, Kind: occ.Workload.Kind, Namespace: occ.Workload.Namespace,
			Name: occ.Workload.Name, Container: occ.Container,
		}

		if _, taken := out.index[key]; !taken {
			out.index[key] = i
		}
	}

	return out
}

// occurrenceSortKey orders occurrences by environment, workload, and container (init and ephemeral containers after
// the containers of the same name, as the renderer keeps them).
func occurrenceSortKey(occ *render.ContainerOccurrence) string {
	return strings.Join([]string{occ.Workload.String(), occ.Kind, occ.Container}, "\x00")
}

// admitProvenance admits every provenance statement that the loader read. The loader already left out the files
// whose predicate cannot be read, so an error here cannot happen; the statement is skipped.
func admitProvenance(loaded *evidence.Evidence, cfg *workspace.Assurance, base assurance.Context) []provenance.Statement {
	out := make([]provenance.Statement, 0, len(loaded.Provenance))

	for i := range loaded.Provenance {
		stmt, err := provenance.NewStatement(&loaded.Provenance[i].Loaded, loaded.Provenance[i].Verification, base, cfg)
		if err == nil {
			out = append(out, stmt)
		}
	}

	return out
}

// admitDeployments admits the deployment records and workload observations for the environment.
func admitDeployments(loaded *evidence.Evidence, cfg *workspace.Assurance, base assurance.Context, environment string) *provenance.Evidence {
	ctx := base
	ctx.Environment = environment

	out := &provenance.Evidence{}

	for i := range loaded.Deployments {
		if rec, err := provenance.NewRecord(&loaded.Deployments[i].Loaded, loaded.Deployments[i].Verification, ctx, cfg); err == nil {
			out.Records = append(out.Records, rec)
		}
	}

	for i := range loaded.Observations {
		if obs, err := provenance.NewObservation(&loaded.Observations[i].Loaded, loaded.Observations[i].Verification, ctx, cfg); err == nil {
			out.Observations = append(out.Observations, obs)
		}
	}

	return out
}

// lineageStatus is the deployment.LineageFunc of the run: the image-to-build step of the trace shows `verified` only
// when the exact lineage claim is `pass` for the occurrence. An occurrence that the rendered evidence does not have
// has no lineage result, so it is `candidate` with a static link and `unknown` without.
func (a *assuranceRun) lineageStatus(key *deployment.OccurrenceKey, hasStaticLink bool) deployment.BuildLineage {
	claim := a.claimEvidence()

	var result *provenance.LineageResult

	if i, found := claim.index[*key]; found {
		result = &claim.occurrences[i].lineage
	}

	status := deployment.BuildLineage{Status: deployment.Lineage(provenance.LineageStatus(result, hasStaticLink))}
	if status.Status != deployment.LineageVerified {
		return status
	}

	status.Digest, status.Builder, status.TrustContract = result.Occurrence.Digest, result.Builder, result.Contract.Digest

	if result.Source != nil {
		status.SourceRepository = result.Source.ID
	}

	if result.Statement != nil {
		status.Statement = result.Statement.Path
	}

	return status
}

// lineageEntries gives the `assurance.lineage` section: one entry for each rendered container occurrence, sorted.
// It never depends on the rules that ran.
func (a *assuranceRun) lineageEntries() []LineageEntry {
	claim := a.claimEvidence()
	out := make([]LineageEntry, 0, len(claim.occurrences))

	for i := range claim.occurrences {
		out = append(out, claim.occurrences[i].entry())
	}

	return out
}
