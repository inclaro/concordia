package claims

import (
	"sync"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/evaluate/netpol"
	"github.com/inclaro/concordia/internal/assurance/evaluate/rbac"
	"github.com/inclaro/concordia/internal/deployment"
)

// Default is the registry of the contracts that ship with Concordia. It is built once and shared; its contracts are
// copies, so a caller cannot change it.
var Default = sync.OnceValue(func() *Registry {
	networkPolicy, accessControl, cloudFirewall := netpol.Contract(), rbac.Contract(), cloudfw.Contract()

	contracts := []Contract{
		renderingContract(), planContract(), provenanceContract(), verificationContract(), qualityContract(),
		evaluatorContract(&networkPolicy, "internal/assurance/evaluate/netpol", assurance.IntegrityDigest),
		evaluatorContract(&accessControl, "internal/assurance/evaluate/rbac", assurance.IntegrityDigest),
		// A plan needs no signed statement: its digest is recorded with each evaluation, and the bundle copies it.
		evaluatorContract(&cloudFirewall, "internal/assurance/evaluate/cloudfw", assurance.IntegrityNotVerified),
	}

	registry, err := New(contracts)
	if err != nil {
		// The contracts are compiled in: an error here is a defect that the tests catch.
		panic(err.Error())
	}

	return registry
})

// contractVersion is the version of the contracts of release R3. It changes with any change of the semantics of a
// contract.
const contractVersion = "1.0.0"

func requires(level string, integrity assurance.Integrity) Requirement {
	return Requirement{Level: level, Completeness: assurance.CoverageComplete, Integrity: integrity}
}

func renderingContract() Contract {
	needs := requires(deployment.LevelRenderedOrEvaluated, assurance.IntegrityDigest)

	return Contract{
		ID: "rendering", Kind: KindAdapter, Version: contractVersion, Experimental: true,
		Tools: []string{"helm 4", "kustomize 5"},
		Inputs: []string{
			"helm template of a chart directory with values files, offline, without hooks run",
			"kustomize build of an overlay directory, offline, without plugins, exec generators, or remote bases",
			"plain Kubernetes manifests: multi-document YAML",
		},
		Fixtures: []string{
			"test/fixtures/assurance/plain", "test/fixtures/assurance/helm-shop", "test/fixtures/assurance/kustomize-shop",
			"test/fixtures/assurance/helm-hostile", "test/fixtures/assurance/kustomize-hostile",
		},
		Limits: []string{
			"only the committed sources are rendered; values that exist only in the cluster are not known",
			"a remote base or chart repository, an exec generator, a KRM function, and a post-renderer are gaps, never run",
			"a render over a limit (time, output bytes, resources) is partial or failed, never cut",
			"the pinned renderer is a program that runs on this machine: confinement lowers, and does not remove, that risk",
		},
		Assumes: []string{},
		Enabled: []Enablement{
			{Claim: CompleteResourceSet, Requires: needs},
			{Claim: ContainerOccurrenceVersions, Requires: needs},
			{Claim: ResolvedSelections, Requires: needs},
		},
		NotEnabled: []string{string(DeploymentSucceeded), string(NetworkIsolation), string(ExactLineage), "runtime-behavior"},
	}
}

func planContract() Contract {
	return Contract{
		ID: "plan", Kind: KindAdapter, Version: contractVersion, Experimental: true,
		Tools:  []string{"terraform show -json", "opentofu show -json"},
		Inputs: []string{"plan JSON of `show -json` with a supported format_version, bound to a revision by an in-toto statement"},
		Fixtures: []string{
			"test/fixtures/assurance/plans",
		},
		Limits: []string{
			"a plan states what would change, not what was applied: it never supports a successful deployment",
			"an attribute that is unknown until apply is a gap",
			"a plan without a verified statement is content not verified and supports no mandatory claim",
		},
		Assumes: []string{},
		Enabled: []Enablement{
			{Claim: PlannedResources, Requires: requires(deployment.LevelRenderedOrEvaluated, assurance.IntegrityAuthenticated)},
		},
		NotEnabled: []string{string(DeploymentSucceeded), string(RuntimeDrift), "cloud-iam-evaluation", "encryption-evaluation"},
	}
}

func provenanceContract() Contract {
	authenticated := assurance.IntegrityAuthenticated

	return Contract{
		ID: "provenance", Kind: KindAdapter, Version: contractVersion, Experimental: true,
		Tools: []string{"cosign 3"},
		Inputs: []string{
			"in-toto Statement v1 in a DSSE envelope: SLSA provenance v1, deployment-record, and workload-observation predicates",
		},
		Fixtures: []string{"test/fixtures/assurance/statements"},
		Limits: []string{
			"exact lineage holds only within the declared trust contract: producers, builders, and source repositories",
			"a SLSA level in a statement is ignored",
			"a tag is not a version: an image without an immutable digest has no lineage",
		},
		Assumes: []string{},
		Enabled: []Enablement{
			{Claim: ExactLineage, Requires: requires(deployment.LevelRenderedOrEvaluated, authenticated)},
			{Claim: DeployedDigest, Requires: requires(deployment.LevelDeploymentExecution, authenticated)},
			{Claim: DeploymentSucceeded, Requires: requires(deployment.LevelDeploymentExecution, authenticated)},
			{Claim: RuntimeDrift, Requires: requires(deployment.LevelRuntimeObservation, authenticated)},
		},
		NotEnabled: []string{"slsa-level", "absence-of-vulnerabilities", "absence-of-malicious-code", "correct-runtime-behavior"},
	}
}

func verificationContract() Contract {
	return Contract{
		ID: "verification", Kind: KindAdapter, Version: contractVersion, Experimental: true,
		Tools:    []string{"cosign 3"},
		Inputs:   []string{"check-result predicate in an in-toto Statement v1: check id, outcome, covered subjects, revision, environment"},
		Fixtures: []string{"test/fixtures/assurance/statements"},
		Limits: []string{
			"a result states the outcome of a named check for one revision and environment, by a trusted producer",
			"TESTS and TESTS_FILE edges of the provider are candidates and never support a verdict",
		},
		Assumes: []string{},
		Enabled: []Enablement{
			{Claim: CheckOutcome, Requires: requires(LevelNotApplicable, assurance.IntegrityAuthenticated)},
		},
		NotEnabled: []string{"test-adequacy", "code-coverage", "absence-of-defects"},
	}
}

func qualityContract() Contract {
	return Contract{
		ID: "quality", Kind: KindAdapter, Version: contractVersion, Experimental: true,
		Tools:    []string{},
		Inputs:   []string{"QualityScenarioSet files with the modes structural, measured, and reviewed; measurement statements"},
		Fixtures: []string{"test/fixtures/assurance/quality"},
		Limits: []string{
			"a verdict holds for one scenario in its own mode: a structural pass never satisfies a measured scenario",
			"a measured scenario needs an admissible measurement statement of a trusted producer",
			"a reviewed scenario needs the assessments of the workspace",
		},
		Assumes: []string{},
		Enabled: []Enablement{
			{Claim: QualityScenarioVerdict, Requires: requires(LevelNotApplicable, assurance.IntegrityNotVerified)},
		},
		NotEnabled: []string{"architecture-meets-quality-attribute"},
	}
}

// evaluatorContract derives the claim contract of a built-in evaluator from its own contract, so the claims and the
// evaluator cannot say different things. The contract id is the workspace evaluator kind, and dir is the package
// directory whose tests are the fixtures of the evaluator. integrity is the integrity that the input must have.
func evaluatorContract(source *evaluate.Contract, dir string, integrity assurance.Integrity) Contract {
	enabled := make([]Enablement, 0, len(source.Enables))
	for _, claim := range source.Enables {
		enabled = append(enabled, Enablement{Claim: ID(claim), Requires: requires(source.RequiresEvidence, integrity)})
	}

	return Contract{
		ID: source.ID, Kind: KindEvaluator, Version: source.Version, Experimental: true,
		Tools:      []string{},
		Inputs:     source.Kinds,
		Fixtures:   []string{dir},
		Limits:     source.Limits,
		Assumes:    source.Assumes,
		Enabled:    enabled,
		NotEnabled: source.DoesNotEnable,
	}
}
