# How to add stronger evidence

This guide shows you how to give Concordia evidence that the code graph cannot give: manifests that Concordia renders itself, signed statements from your build system, plans, deployment records, check results, and quality scenarios. Each kind is optional and independent. Start with one, check it, then add the next.

Evidence that Concordia cannot admit never turns into a pass. The obligation that depends on it is `unknown`, with a reason. See [verdicts and coverage](../explanation/verdicts-and-coverage.md) and [how Concordia works](../explanation/how-concordia-works.md#assurance-evidence).

## Prerequisites

- A workspace that passes `concordia check`. See [set up a workspace](set-up-a-workspace.md).
- Environments declared under `environments`. See [trace a deployment](trace-a-deployment.md#declare-the-environments).
- For rendering: `helm` or `kustomize` on the machine, if a source uses them.
- For signed statements: [cosign](https://docs.sigstore.dev/cosign/) 3 on the machine that runs `check` and on the machine that signs.

All evidence is declared under the [`assurance`](../reference/workspace.md#assurance) key. Without it, Concordia reads none of this evidence. Every path is relative to the workspace root.

## Render the manifests

Static YAML shows what an author wrote. A rendered Helm chart or Kustomize overlay shows the resource set that the declared sources produce for an environment: what would be applied, not what a cluster received (that is a deployment record or an observation, below).

1. Declare the sources of each environment. An environment, a tool, and a path appear once:

   ```yaml
   environments:
     - { id: production, sources: [deploy/**] }
     - { id: staging, sources: [deploy/**] }
   assurance:
     rendering:
       sources:
         - { environment: production, tool: helm, path: deploy/chart, valuesFiles: [deploy/values/prod.yaml], release: shop, namespace: shop }
         - { environment: staging, tool: kustomize, path: deploy/overlays/staging }
   ```

   The tool `plain` reads manifests without running a program.

2. Pin the renderers. A `helm` or `kustomize` source needs its renderer in `.concordia.lock`:

   ```sh
   concordia lock update
   ```

   The command writes the version and the `sha256` of each renderer that a source uses. A renderer that is missing, not pinned, or different from its pin stops with `CO-PRV-003` and exit 5. Commit the lock file.

3. Load the inputs without evaluating any rule:

   ```sh
   concordia assurance validate
   ```

## Check what you see

`assurance validate` loads every declared input as `check` does. For each input it prints the kind, the integrity level, whether the input is admissible, and the reasons. Under `rendering by environment` it prints the coverage of each environment, the number of resources and container occurrences, and each gap:

- `complete`: the whole resource set of the environment was rendered.
- `partial`: something was not rendered, for example a remote base, an exec generator, or a render over a limit. The gap lines say what.
- `failed`: nothing usable was rendered.

Concordia renders each source twice and warns when the two outputs differ. The command evaluates no rule and writes no file. Add `--json` for a tool. It exits 2 for invalid configuration and 5 for a missing renderer or sandbox.

To enable the rule, add the pack and read the result of `ASR-REN-001`:

```yaml
policies:
  packs:
    - use: builtin:assurance
```

The remediation text of the pack names the key `assurance.rendering`. An environment without a complete rendering is `unknown` with the reason `rendered-evidence-missing`. For the limits of rendering, see [rendering](../reference/assurance.md#rendering).

## Add signed statements

Other systems produce evidence about builds, deployments, checks, and measurements. Concordia reads it as an in-toto Statement v1. It accepts statements in three forms, and the form decides how far the statement can be trusted:

| Form | Signature | Integrity |
|---|---|---|
| A bare in-toto statement | none | `content not verified` |
| A DSSE envelope with no bundle | none that cosign can verify | `content not verified` |
| A Sigstore bundle | verified by cosign | `producer authenticated` |

A statement that is not verified supports no mandatory obligation. See [statements](../reference/assurance.md#statements).

### Declare the trust contract

Name the producers that you trust, and the kinds of claim that each one is trusted for. A producer is trusted for the listed kinds and no others:

```yaml
assurance:
  producers:
    - id: ci-build
      claims: [build-provenance, check-result, measurement, plan, deployment-record]
      authentication:
        method: cosign-keyless
        issuer: "https://token.actions.githubusercontent.com"
        identity: "https://github.com/acme/shop/.github/workflows/build.yml@refs/heads/main"
```

A key-based producer uses `method: cosign-key` and `key` (a public key file in the workspace, or a KMS reference) instead of `issuer` and `identity`. Concordia compares the subject digest of every statement itself, and it never uses a verdict from a producer.

### Write a statement

A statement has this shape. The subject of a check result or measurement is the revision, as a `gitCommit` digest:

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [{ "name": "shop", "digest": { "gitCommit": "<40-character commit id>" } }],
  "predicateType": "https://concordia.io/evidence/check-result/v1",
  "predicate": {
    "check": "unit-tests",
    "outcome": "passed",
    "revision": "<40-character commit id>",
    "environment": "production",
    "evaluator": { "name": "go-test", "version": "1.24" },
    "time": "2026-10-31T10:00:00Z",
    "covers": ["api-service"]
  }
}
```

The predicate types are `https://concordia.io/evidence/{deployment-record,workload-observation,check-result,measurement,plan}/v1`. A build provenance statement uses the SLSA provenance v1 predicate as it is. The fields of each type are in [statements](../reference/assurance.md#statements) and [verification results](../reference/assurance.md#verification-results). A required field that is absent is listed as `missing-field:<name>` and makes the statement inadmissible.

### Sign a statement in GitHub Actions

This job writes a check-result statement and signs it with cosign 3. The signature is keyless, so the job needs `id-token: write`:

```yaml
name: build
on:
  push:
    branches: [main]
permissions:
  contents: read
  id-token: write
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: sigstore/cosign-installer@v3
        with:
          cosign-release: v3.1.4
      - name: Run the tests
        id: tests
        run: |
          if go test ./...; then echo "outcome=passed" >> "$GITHUB_OUTPUT"; else echo "outcome=failed" >> "$GITHUB_OUTPUT"; fi
      - name: Write the statement
        run: |
          mkdir -p build/results build/statements
          cat > build/statements/unit-tests.statement.json <<EOF
          {
            "_type": "https://in-toto.io/Statement/v1",
            "subject": [{ "name": "${GITHUB_REPOSITORY}", "digest": { "gitCommit": "${GITHUB_SHA}" } }],
            "predicateType": "https://concordia.io/evidence/check-result/v1",
            "predicate": {
              "check": "unit-tests",
              "outcome": "${{ steps.tests.outputs.outcome }}",
              "revision": "${GITHUB_SHA}",
              "environment": "production",
              "evaluator": { "name": "go-test", "version": "1.24" },
              "time": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
              "covers": ["api-service"]
            }
          }
          EOF
      - name: Sign the statement
        run: |
          cosign attest-blob --statement build/statements/unit-tests.statement.json \
            --bundle build/results/unit-tests.json --yes
      - uses: actions/upload-artifact@v4
        with:
          name: check-results
          path: build/results/unit-tests.json
      - name: Fail the job when the tests failed
        if: steps.tests.outputs.outcome == 'failed'
        run: exit 1
```

The test step records the outcome instead of failing, so that a failed run is still signed and uploaded as evidence; the last step then fails the job, so CI blocks on failing tests as before.

`--statement` signs a statement that you wrote. The alternative is `--predicate <file> --type <predicate type URI>` with the blob that is the subject. The bundle is the file that Concordia reads: put it where `assurance.verification.results` points (`build/results/*.json` here), for example by committing it or by downloading the artifact before `concordia check`. The unsigned statement is written to `build/statements/`, outside the declared glob: in the result directory it would be read as an unsigned, inadmissible result.

The identity in the producer must match the workflow that signs. For the example above, the `identity` is the workflow file at the ref that ran, as in the producer block earlier.

### Verify, also offline

Cosign 3 writes a signing record to the public transparency log, so keyless signing needs network access from the job. Verification at `check` time uses the same trust material. With `--offline`, Concordia does not start cosign unless you give it a trusted root:

```yaml
assurance:
  verification:
    results: ["build/results/*.json"]
    maxAgeDays: 14
    trustedRoot: .sigstore/trusted_root.json
```

With `trustedRoot`, cosign verifies without network access. Without it, `--offline` leaves the statement below `producer authenticated` with the detail `offline-verification-unavailable`.

Run `concordia assurance validate` and look at the `signature:` line of each statement. The values are `none`, `no-producer`, `unavailable`, `failed`, and `verified`.

## Bind a plan to a revision

Concordia ingests the output of `terraform show -json` or `tofu show -json` as a file. It never runs `plan`. A plan shows what would change, not what was applied.

```yaml
assurance:
  plans:
    - { environment: production, tool: terraform, file: build/plans/prod.json, statement: build/plans/prod.intoto.json, maxAgeDays: 7 }
```

Without `statement`, the plan is `content not verified` and supports no mandatory claim. A `statement` is a `plan/v1` statement whose subject is the `sha256` of the plan file. Its predicate has `environment`, `revision`, and `time`, and may have `tool`. Concordia compares the subject digest with the file. An attribute that is unknown until apply is a gap, and a sensitive value is replaced by a redaction marker. See [plans](../reference/assurance.md#plans).

## Trace from source to the running image

Exact lineage needs a chain of admissible statements. Declare the trust contract of your builds and where the statements are:

```yaml
assurance:
  provenance:
    statements: ["build/attestations/*.json"]
    builders: ["https://github.com/acme/shop/.github/workflows/build.yml"]
    sourceRepositories: [shop]
    maxAgeDays: 30
  records:
    deployments: ["build/records/deploy-*.json"]
    observations: ["build/records/observed-*.json"]
    maxAgeDays: 7
```

- **Provenance statements** are SLSA provenance v1 statements, signed by a producer trusted for `build-provenance`. Concordia reads the subject (the image digest), the builder id, and the resolved dependencies. It ignores any SLSA level and claims none. A container with a tag and no digest has no lineage.
- **Deployment records** (`deployment-record/v1`) say that one container of one workload was rolled out to an environment, with the image digest, the outcome (`succeeded` or `failed`), the time, and the deployer. Only an admissible `succeeded` record supports `deployment-succeeded`.
- **Workload observations** (`workload-observation/v1`) say that a container was seen running with a digest, at a time. A record or an observation names a `cluster` or a `context`.

The platform team chooses the producer of deployment records and workload observations: for example the deploy job, a GitOps controller, or a collector in the cluster. Concordia does not prescribe it. Record the choice in your repository and declare the producer under `assurance.producers` with the claims `deployment-record` or `workload-observation`. In this release `ASR-PRV-001` and `ASR-PRV-002` run as `advisory` (or `shadow`): their contracts are experimental, and making them `mandatory` stops `check` with `CO-POL-018` (exit 2). Their findings and warnings still show every container that lacks lineage or drifts. Set the mode explicitly with `check.enforcement` (family `assurance`):

```yaml
policies:
  packs:
    - use: builtin:assurance
check:
  enforcement:
    - { rules: [ASR-PRV-001, ASR-PRV-002], mode: advisory }
```

`ASR-PRV-001` asks for exact lineage from the source to the deployed digest. `ASR-PRV-002` asks that the digest in the cluster equals the recorded one. The `lineage` entries and the deployment steps of `concordia trace` show the result for each container. See [provenance](../reference/assurance.md#provenance).

## Use check results

A check result is a statement that a named check had an outcome for a revision and an environment. Concordia does not read test adequacy from it, and the `TESTS` edges of the code graph never support a verdict.

1. Declare where the results are, as in the signing step above (`assurance.verification.results`).
2. Link a CALM control to the check ids. A control with `check-ids`, `rule-ids`, or both needs at least one list:

   ```json
   "controls": {
     "verification-001": {
       "description": "The unit tests pass",
       "requirements": [{
         "requirement-url": "https://calm.finos.org/release/1.2/meta/control.json",
         "config": { "engine": "concordia", "check-ids": ["unit-tests"] }
       }]
     }
   }
   ```

3. Or require a result in a rule:

   ```yaml
   rules:
     - id: CHK-001
       name: The unit tests pass for production
       severity: error
       require:
         checkResult: { check: unit-tests, environment: production, subject: { calmNodes: [api-service] } }
   ```

   A check that no control and no result defines is refused with `CO-POL-005`.

   Like the `assurance` pack rules, a `checkResult` rule runs as `advisory` or `shadow` in this release; `mandatory` gives `CO-POL-018`. To block a merge on a measured outcome today, use a quality scenario with `enforcement: mandatory` (below).

The verdict follows the admissible results: `passed` gives a pass, `failed` or `errored` gives a violation, and `skipped` is ignored beside the others. Conflicting results, or none, give `unknown` with `evidence-conflict` or `no-evidence-supplied`. A result is inadmissible when its signature is not verified, its revision is not the evaluated one, its environment is not the obligation's, it is older than `maxAgeDays`, or the tree is dirty. The details are listed with the result. See [verification results](../reference/assurance.md#verification-results).

## Declare quality scenarios

A quality scenario states a stimulus, a response, and how Concordia decides it. Write the scenarios in a `QualityScenarioSet` file and declare it:

```yaml
assurance:
  quality:
    scenarios: ["quality/*.yaml"]
    measurements: ["build/measurements/*.json"]
    requiredAreas: [availability, operability]
```

```yaml
apiVersion: concordia.io/v1alpha1
kind: QualityScenarioSet
scenarios:
  - id: QS-FAILOVER
    area: availability
    qualityAttribute: availability
    stimulusSource: operator
    stimulus: the primary database node fails
    environment: production
    expectedResponse: a replica takes over
    owner: platform-team
    scope: [orders-db, orders-service]
    mode: measured
    enforcement: mandatory
    criterion:
      metric: failover-seconds
      unit: s
      comparison: lte
      threshold: 30
      maxAgeDays: 30
```

The `scope` names CALM nodes or relationships. The areas are `availability`, `modifiability`, `security`, `performance`, `deployment-safety`, and `operability`. Each scenario has one mode, and a pass in one mode never satisfies another:

| Mode | The criterion | The verdict comes from |
|---|---|---|
| `structural` | `rules: [<rule id>, ...]` | The verdicts of those rules. |
| `measured` | `metric`, `unit`, `comparison`, `threshold`, `maxAgeDays` | An admissible `measurement/v1` statement. |
| `reviewed` | `subject` and `authority` | An assessment. It needs the `assessments` key; without it the reason is `reviewed-mode-not-available`. |

A measurement statement has the predicate fields `scenario`, `metric`, `value`, `unit`, `threshold`, `comparison`, `testConditions`, `revision`, `environment`, `evaluator` (`name` and `version`), `outcome`, and `window` (`start` and `end`). Concordia compares the value, the unit, and the threshold with the scenario and never uses a verdict from the producer. A unit or threshold that differs is inadmissible (`unit-mismatch`, `threshold-mismatch`). The measurement is also a statement, so the producer needs the claim `measurement`. An area in `requiredAreas` without a scenario is a finding.

A scenario with a criterion that cannot be measured, for example free text, is refused when the file loads. See [quality](../reference/assurance.md#quality).

To decide a `reviewed` scenario, approve a `quality` assessment that names it: see [approve a quality assessment](review-and-approve-assessments.md#approve-a-quality-assessment). To link a scenario to the decision behind its design, add `addressedBy: [ADR-7]`: see [link a quality scenario to a decision](govern-architecture-decisions.md#link-a-quality-scenario-to-a-decision).

Each scenario is a rule result, `quality:<id>`, in the area `qualityEvaluation`. A scenario that you set to `mandatory` counts in the exit code: a violation gives exit code 1 and an `unknown` result gives exit code 4. An active `check.rollback` runs it as `shadow`. See [roll out new obligations](roll-out-new-obligations.md#roll-out-review-and-quality-rules).

## Related

- [Assurance reference](../reference/assurance.md)
- [`assurance` in the workspace reference](../reference/workspace.md#assurance)
- [How Concordia works](../explanation/how-concordia-works.md#assurance-evidence)
- [Trace a deployment](trace-a-deployment.md)
- [Resolve unknown verdicts](resolve-unknown-verdicts.md)
- [Run in CI](run-in-ci.md)
