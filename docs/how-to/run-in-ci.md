# How to run Concordia in CI

This guide shows you how to run `concordia check` in a CI pipeline, publish the results, and handle the exit codes.

## Prerequisites

- A workspace that passes `concordia check` on your machine. See [set up a workspace](set-up-a-workspace.md).
- A committed baseline, if you adopt Concordia on existing code. See [manage baselines and waivers](manage-baselines-and-waivers.md).
- A CI runner with git, and either Go (to run `go install`) or a Concordia release archive. Install the pinned CBM build with `concordia cbm install`, or use the [installer](install.md#method-1-installer), which does both. A CI runner has no agent daemon, so it has no [conflict with another CBM build](../explanation/cbm-and-agents.md). See [install](install.md).
- If the workspace declares [views](keep-diagrams-current.md) with exports, the Structurizr CLI `2025.11.09` on the runner. See [install Structurizr through Docker](render-diagrams.md#install-the-structurizr-cli-through-docker).

## Generate a CI template

`concordia init --ci github|gitlab|generic` writes a CI file. The GitHub template is `.github/workflows/concordia.yml`:

```yaml
name: concordia
on:
  pull_request:
  push:
    branches: [main]
jobs:
  concordia:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      security-events: write
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: "1.27"
      - run: go install github.com/inclaro/concordia/cmd/concordia@latest
      # Install the pinned codebase-memory-mcp build (the default directory ~/.local/bin is not on the PATH of a runner).
      - run: concordia cbm install --dir "$HOME/.local/bin" && echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - run: concordia index
      - run: concordia check --format sarif --output build/concordia/results.sarif
      - uses: github/codeql-action/upload-sarif@v3
        if: always()
        with:
          sarif_file: build/concordia/results.sarif
      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: concordia-results
          path: build/concordia/results.json
```

The `concordia cbm install` step downloads the pinned CBM build, checks its digests, and adds it to the `PATH`. It never runs the upstream CBM installer.

The GitLab template is `.gitlab/concordia.gitlab-ci.yml`. It uses the image `golang:1`, runs the same `go install` line, then `concordia cbm install --dir "$HOME/.local/bin"` and `export PATH="$HOME/.local/bin:$PATH"`, then `concordia index` and `concordia check --format junit --output build/concordia/results.xml`. It publishes the JUnit file as a report and `build/concordia/results.json` as an artifact.

The generic template is a shell script, `ci/concordia.sh`. It runs `concordia cbm install`, adds `$HOME/.local/bin` to the `PATH`, then runs `concordia index` and `concordia check --format json --output build/concordia/results.json`. It expects `concordia` to be on the `PATH` already.

## Always index after checkout

If the indexed graph is stale or not stable, `check` fails with `CO-OBS-008` and exit code 3. Run `concordia index` in every job, after the checkout and before `check`. Fetch full history (`fetch-depth: 0` on GitHub) if your workspace needs it.

## Choose report formats

`check --format` accepts `pretty`, `json`, `junit`, and `sarif`. With `--output <path>`, the command writes the primary output to a file.

To write several formats on every run, list them in `concordia.yaml`:

```yaml
report:
  outputs:
    - format: junit
      path: build/concordia/results.xml
    - format: sarif
      path: build/concordia/results.sarif
```

`check` always writes `build/concordia/results.json` as well. Upload the files you need as artifacts, or upload the SARIF file to your code-scanning service. For the mappings, see [results](../reference/results.md).

## Use offline mode and commit the lock

`--offline` forbids network access and requires every remote CALM reference to be mapped and locked:

```bash
concordia --offline check
```

The file `.concordia.lock` records the toolchain versions and a digest for each mapped resource. Commit it. A digest mismatch, or an unlocked resource in offline mode, fails with `CO-INT-012` and exit code 6. See [workspace](../reference/workspace.md#concordialock) for the format, and [architecture](../reference/architecture.md) for URL mapping.

## Handle exit codes

| Exit code | Meaning | Usual action |
|---|---|---|
| 0 | Ok | None. |
| 1 | A finding at or above `check.failOn` is `active` or has an expired waiver | Fix the code, or add a [waiver](manage-baselines-and-waivers.md). |
| 2 | Invalid input | Fix the file named in the diagnostic. |
| 3 | Adapter failure, including a stale graph (`CO-OBS-008`) | Run `concordia index`. |
| 4 | Unknown verdict or coverage shortfall | See [resolve unknown verdicts](resolve-unknown-verdicts.md). |
| 5 | Toolchain missing or incompatible | Install or fix the tool that the diagnostic names, for example CBM (`concordia cbm install`; `CO-OBS-003`, `CO-OBS-004`, `CO-OBS-005`) or git (`CO-WRK-012`). |
| 6 | Network or lock problem | Update and commit `.concordia.lock`. |
| 7 | Internal error | Report it, with the run manifest in `.concordia/runs/`. |
| 130 | Interrupted | Run again. |

The full list is in the [CLI reference](../reference/cli.md#exit-codes). CI never accepts new baseline entries: `concordia baseline update --accept` fails when the `CI` variable is set.

## Gate by environment

Exit codes 1, 4, and 7 count only results with enforcement `mandatory`. You set the mode for each rule family and environment in `check.enforcement`, so the same violation can block in one environment and not in another:

```yaml
check:
  enforcement:
    - {family: deployment-security, environment: staging, mode: mandatory}
    - {family: deployment-security, environment: production, mode: advisory}
```

With this setting a violation in `staging` gives exit code 1. The same violation in `production` is shown with enforcement `advisory` and the exit code stays 0. An `unknown` obligation takes the mode of its own environment too: it gives exit code 4 only in an environment where the family is `mandatory` and the rule has `onUnknown: fail`.

| Mode | In the reports | In `results.json` | Exit code |
|---|---|---|---|
| `mandatory` | Shown | Yes | Counts |
| `advisory` | Shown (SARIF level `note`) | Yes | Does not count |
| `shadow` | Not shown in pretty, SARIF, or JUnit | Yes | Does not count |

Typed rules (rules with an `obligation` block) are `advisory` until you set another mode. A rule that you set to `mandatory` fails the job with `CO-POL-018` (exit code 2) when a guarantee that it needs is absent in an environment: the environment is not declared, the evidence family is not `stable` in the provider profile, or the run has no coverage predicate for the family. The diagnostic names the rule, the environment, and the guarantee; no gate result is produced. To switch new enforcement off for a time, declare `check.rollback`: the typed rules, the decision-governance rules, and the review rules run as `shadow`, the summary says that new enforcement was disabled, and the check fails with `CO-WRK-018` (exit code 2) after the `expires` day. See [roll out new obligations](roll-out-new-obligations.md).

## Read the areas and compare runs

`results.json` shows six areas separately: `conformance`, `deploymentTrace`, `adrGovernance`, `diagramCurrency`, `assessmentCurrency`, and `qualityEvaluation`. An area that the workspace does not configure says `not-configured`, never `pass`. Read the area that your job cares about instead of one overall status:

```sh
jq '.areas | to_entries[] | {area: .key, verdict: .value.verdict}' build/concordia/results.json
```

`summary.verdict` is the worst verdict of the rules and of the configured areas, so a passing `conformance` area never hides an area that is `unknown`. The exit code does not depend on the areas: it comes from the mandatory rules and findings (see above). The terminal output names every area that is `unknown` before the summary line.

To check that two runs agree, compare `summary.resultDigest`. It is the same for the same recording or graph, policies, bindings, and workspace, and it ignores the run id, the timestamps, and the host. The counters in `observability` show how much evidence a run read (`ingestedFacts`), how much it could not use (`unresolvedFacts`, `unsupportedFacts`, `coverageGaps`), and whether a project failed (`stageFailures`). A rising `unsupportedFacts` after a CBM upgrade is worth a look before you trust a green result.

## Verify views from a protected pipeline

A workspace that declares `views` has three more checks in `concordia check`: presence of each view in the Git revision, freshness of the generated files, and correspondence of the diagram with the architecture. The rules are `VIEW-PRES-001`, `VIEW-FRESH-001`, and `VIEW-CORR-001`. You do not enable them in `policies.packs`. They follow `check.failOn`, `check.unknown`, baselines, waivers, and `check.enforcement` (family `view-currency`), so the [exit codes](#handle-exit-codes) are the ones above: 1 for a violation at or above `failOn`, and 4 for an `unknown` result when `check.unknown` is `fail`.

### What `check` trusts

`check` is the verifier. It never trusts a digest that is stored in a view manifest:

1. It reads the manifest and the artifacts of the view from the committed revision through Git, not from the working tree.
2. It rebuilds the projections of every view from the current inputs: the CALM model, the bindings, the observed evidence, and the policies that select a depicted element.
3. It regenerates the Structurizr DSL in memory and every export with the Structurizr CLI, and compares the bytes and the digests with the committed files and the manifest.

A manifest that someone edited to match a stale diagram does not give a pass. `check` never writes a view file. `concordia render --views` is the explicit command that writes them: people run it and commit the files.

A result that `check` reports as current is trustworthy only when the verifier is. The branch under test can change the manifest, the diagram, the workspace file, and the pipeline file. So:

- Run `check` from a protected pipeline: a pipeline definition, a Concordia version, and a Structurizr version that the branch under test cannot change. Take them from the protected branch or from a central repository, and pin the versions instead of using `@latest`.
- Treat the view manifest in the working tree as untrusted input. The trusted CI recomputes everything that the manifest claims.
- Check out the commit under test and run `concordia index` after the checkout, so that the index and the revision belong together. Presence and freshness read the committed revision; a file that you create in the job is not committed.

### Structurizr pin and confinement

Exports are regenerated with the Structurizr CLI `2025.11.09`. A CLI with another version fails with `CO-DGM-004` (exit code 5) when you render, and `check` reports the export as `unknown` with the reason `generator cannot run`; it never passes it. `concordia doctor` warns when the version differs from the pin.

Concordia runs the CLI in a scratch directory with a scrubbed environment: only `PATH` and `JAVA_HOME`, `HOME` in the scratch directory, no proxy variable. The manifest records `confinement: scrubbed-env`. This is not a sandbox. Concordia cannot deny the network to a process that it starts, so on a runner that needs this guarantee, point `STRUCTURIZR_CLI` to the Docker wrapper that runs the container with `--network none`. The wrapper is in [install Structurizr through Docker](render-diagrams.md#install-the-structurizr-cli-through-docker). The [limits of the confinement](render-diagrams.md#the-pinned-version-and-confined-rendering) are stated there.

### Pipeline steps

Use the order of the generated templates. Between the checkout and `index`, install Concordia at a pinned version, then the pinned CBM build (`concordia cbm install`), and the Structurizr wrapper:

```yaml
steps:
  - uses: actions/checkout@v4
    with:
      fetch-depth: 0
  # Install Concordia at a pinned version, then the pinned CBM build (concordia cbm install), and the Structurizr wrapper (docker run --network none).
  - run: concordia index
  - run: concordia check --format sarif --output build/concordia/results.sarif
  - uses: actions/upload-artifact@v4
    if: always()
    with:
      name: concordia-results
      path: build/concordia/results.json
```

An authored Mermaid view needs `observation.indexMode: full` in the workspace file, because CBM emits diagram evidence only in that mode. Without it, the correspondence of the view is `unknown` with the reason `structure not available`.

### Upgrades

A new Structurizr pin or a new digest contract in a Concordia release makes every generated view stale, with the freshness reason `digest contract changed` (`contractChanged: true` in `results.json`), until someone runs `concordia render --views` and commits the files. Do the upgrade in a branch that renders and commits the views, and merge the pipeline change together with the new files. During the move, `comparison.severity: warning` on the views keeps the job green while you commit. See [adopt views in shadow mode](keep-diagrams-current.md#adopt-views-in-shadow-mode).

## Gate on ADR governance

A workspace with an [`adr`](../reference/workspace.md#adr) register and the pack `builtin:adr-governance` in `policies.packs` has six more rules, `GOV-ADR-001` to `GOV-ADR-006`. Their findings report in `areas.adrGovernance`. They follow `check.failOn`, `check.unknown`, baselines, waivers, and `check.enforcement` (family `decision-governance`), so the [exit codes](#handle-exit-codes) are the ones above:

| Situation | Exit code |
|---|---|
| An active decision that requires enforcement links no mandatory rule (`GOV-ADR-004`), or a mandatory rule rests only on a superseded decision (`GOV-ADR-003`) | 1 |
| The same finding with a valid waiver or a baseline entry | 0. The rule and the area stay `violation`; the finding is `waived` or `baselined`. |
| The same finding with an expired waiver | 1 |
| The register, a link to a rule, the lifecycle profile, or a trigger is not valid (`CO-ADR-001` to `CO-ADR-005`) | 2. No result is produced. |
| Decision evidence is `unknown` (for example, the provider has no ADR evidence) and `check.unknown` is `fail` | 4 |
| The same with `--unknown warn` | 0 |

To adopt the pack without blocking the job, set the family to `advisory` or `shadow` under `check.enforcement` first. A run with `--changed` still evaluates the governance rules on the full scope, and `CO-CON-027` names them. Governance only reads: it never writes a register or an ADR file.

## Gate on review currency

A workspace with an [`assessments`](../reference/workspace.md) key, required assessments, and the pack `builtin:review-currency` in `policies.packs` has four more rules, `REV-APR-001`, `REV-DSP-001`, `REV-CAP-001`, and `REV-GOV-001`. Their findings report in `areas.assessmentCurrency`. They are `advisory` until you set another mode with `check.enforcement` (`pack: review-currency`). They follow `check.failOn`, `check.unknown`, waivers, and `check.rollback`; a baseline does not accept a `shadow` finding.

| Situation | Exit code |
|---|---|
| A required assessment has no verified approval, or its approval is `review-required`, `expired`, or `revoked`, and the rules are `mandatory` | 1 |
| The same with the rules `advisory` | 0. The verdict and the finding stay. |
| The currency is `unknown` (for example, the trust anchor cannot be read) and `check.unknown` is `fail`, with `mandatory` rules | 4 |
| `mandatory` review rules without an authority or a trust anchor (`CO-ASM-004`), or without a guarantee of the enforcement check (`CO-POL-018`) | 2. No result is produced. |
| An assessment document is not valid (`CO-ASM-001` to `CO-ASM-003`) | 2 |

An approval counts only when Concordia verifies it from local git data at the **trust anchor** (`assessments.trustAnchor.ref`, for example `origin/main`): the record must exist at that revision and name a reviewer that the authority at that revision lists. A shallow clone must therefore contain the anchor and its history up to HEAD. Fetch it before `check` with `git fetch --depth=50 origin main:refs/remotes/origin/main` (a shallow clone tracks only its own branch, so a plain `git fetch origin main` does not create `origin/main`), then run `git fetch --deepen=50` so that the anchor is an ancestor of HEAD, and deepen more when `CO-ASM-007` still says that it is not. Without the anchor the approvals are `unknown` with the reason `trust-anchor-unreadable`, and a `mandatory` gate exits 4. `check` never writes an approval: `concordia assessment approve` refuses to run when `CI` is true. A run with `--changed` evaluates the review rules on the full scope, and `CO-CON-027` names them.

A `signed` approval also needs `cosign` on the runner, and `cosign` 3 needs network access to verify unless you pass a Sigstore trusted root. Under `--offline`, a signed approval is `unknown` with the reason `verification-unavailable`, and a `mandatory` gate exits 4. Use `protected-review` approvals in a job that runs offline. See [review and approve assessments](review-and-approve-assessments.md#choose-a-mechanism).

### Reuse the review evaluation between runs

`concordia check --incremental` reuses the comparison of each assessment from an earlier run. It reads and writes the derived directory `.concordia/cache/impact/`, which `init` already ignores, and the result equals the result of a full `check`. A job that restores that directory from its CI cache, for example keyed by the base branch, evaluates only the assessments that depend on an item that changed since the cached run.

- The cache file is named by a key of the digest contract, the normalization version, the capability profile, the policy digest, the authority digest, the evaluator versions, and the revision of the trust anchor. When any of them changed, the restored file is not used. Concordia evaluates in full and reports the note `CO-ASM-010`; the exit code does not change.
- Expiry, revocation, and the verification of the records run on every job, so an approval that expired or was revoked since the cached run is never reported as current.
- A damaged or incomplete restore is the same case: `CO-ASM-010`, a full evaluation, and a new file. Do not edit the cache. A `check` without `--incremental` neither reads nor writes the directory, so a job that must not touch the workspace leaves the flag out.
- `results.json` and the run manifest report `analysis.mode`, `analysis.cacheHits`, and `analysis.cacheMisses`, so a pipeline can see how much of the evaluation it reused.

The limits of the traversal (`assessments.analysisLimits`: `maxDepth` 64, `maxVisited` 100000, `maxSeconds` 120 by default) apply to both modes. The [benchmark](../explanation/review-currency.md#benchmark) shows what the cache saves.

## Gate on assurance evidence

A workspace with an [`assurance`](../reference/workspace.md#assurance) key adds evidence that the job must be able to read. Run `concordia assurance validate` before `check` to see each input, its integrity level, and why it is inadmissible. The command evaluates no rule and writes no file. It exits 2 for an invalid declaration and 5 for a missing renderer or an unavailable required sandbox.

```sh
concordia assurance validate
concordia check --format sarif --output build/concordia/results.sarif
```

Set up the runner for what the workspace declares:

- **Renderers.** A `helm` or `kustomize` source needs the tool on the `PATH` (or in `HELM` or `KUSTOMIZE`) at the version and `sha256` pinned in `.concordia.lock` under `resolved.renderers`. A missing or different renderer fails with `CO-PRV-003` and exit code 5. Run `concordia lock update` and commit the lock when you change the renderer. With `assurance.rendering.requireOsSandbox: true`, the runner needs `bwrap` and user namespaces, or the job fails with `CO-PRV-004` (exit code 5).
- **cosign.** Signed statements are verified with `cosign` 3 on the `PATH` (or in `COSIGN`). Without it the statement stays below `producer authenticated` and the claim is `unknown`.
- **Network and `--offline`.** `cosign` 3 verifies against the Sigstore trusted root, so it needs network access. To verify under `--offline`, set `assurance.verification.trustedRoot` to a trusted root file in the workspace. Without it, a statement under `--offline` is `unknown` with the detail `offline-verification-unavailable`. Signing under `--offline` is an error (`CO-WRK-016`, exit code 6).
- **Quality scenarios.** A scenario with `enforcement: mandatory`, and a name in `assurance.quality.requiredAreas` with no scenario, count in the exit code: exit code 1 for a violation, and exit code 4 for an `unknown` result. Their findings report in `areas.qualityEvaluation`.
- **Assurance rules.** The rules of the `assurance` pack and the `require.*` rules cannot be `mandatory` yet (`CO-POL-018`). Keep them `advisory` or `shadow`, see [roll out new obligations](roll-out-new-obligations.md#know-what-mandatory-needs).

For the evidence itself, see [add stronger evidence](add-stronger-evidence.md).

## Verify a bundle

A job that receives a conformance bundle checks it before it relies on it:

```sh
concordia export verify build/concordia/conformance.tar.gz
```

Exit code 0 prints `content verified by digest`: every file matches the digest in the manifest, and no file is missing or extra. A digest check shows that the content did not change after export; it does not show who made the bundle. A changed, missing, or extra file gives `CO-REP-004` (exit code 2) and names the file.

To authenticate the producer, sign with `concordia export bundle --sign` and verify with `--verify-signature` (for example `--certificate-identity` and `--certificate-oidc-issuer` for keyless signatures). A verified signature prints `producer authenticated`. A missing `cosign` gives exit code 5.

`explain`, `diff`, and `export bundle` refuse a run that did not finish (for example after an interrupt with exit code 130): `CO-REP-004`, exit code 2. Run `check` again.

## Share bundles safely

A bundle leaves the CI job: other people download it, and the job log keeps its output. Before you share one:

1. Export with `--include-source-snippets violations` only when the snippets are needed. The default `none` stores no source text.
2. Read the count that `export bundle` and `explain --source` print (`CO-REP-005`, for example `redaction applied: 2 value(s) removed from snippets/7c4d....txt`). Secret values in Kubernetes Secrets, Dockerfile `ENV`, Compose environments, `.tfvars`, shell scripts, and known token and key shapes are replaced by `[REDACTED:<class>]`. The `redactions` records in `manifest.json` list the file, line, and class of each one and never the value.
3. Treat a secret in an unusual place as not covered. Redaction knows the infrastructure formats and the common token shapes; see the [limits](../reference/redaction.md#limits).
4. Check the paths: a `CO-REP-006` diagnostic means that Concordia refused a path that left the workspace or project root. The bundle has no content of that path.
5. Verify the bundle before you rely on it (see above), and sign it when the receiver has to know who made it.

The rules, markers, and records are in the [Redaction reference](../reference/redaction.md).

## Confirm the result

Push a branch. The job runs `index` and `check`, and the exit code of the job matches the table above. With SARIF upload, findings appear in the code-scanning view.

## Next steps

- [Manage baselines and waivers](manage-baselines-and-waivers.md)
- [Render diagrams](render-diagrams.md) to publish an architecture view as a CI artifact.
- [Redaction reference](../reference/redaction.md)
- [Diagnostics reference](../reference/diagnostics.md)
