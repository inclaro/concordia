# Assurance adapters release notes

This release adds adapters and evaluators that supply stronger evidence than a static reference: rendered manifests, evaluated plans, verified statements, check results, and quality scenarios. This page lists what a user can see change. Everything is opt-in and advisory by default.

## The `assurance` workspace key

A new key `assurance` declares the evidence. Without it Concordia reads none of it, and `results.json` has no `assurance` object. The schema is closed, and an invalid declaration is `CO-PRV-001` (exit 2). A path outside the workspace root is `CO-PRV-002`. Its parts are `rendering`, `plans`, `producers`, `provenance`, `records`, `evaluators`, `verification` (including `trustedRoot`), and `quality`. A profile can set `assurance`. See [`assurance`](../reference/workspace.md#assurance).

## Rendered manifests and plans

- Concordia renders plain manifests, Helm charts, and Kustomize overlays for a declared environment and parses the output itself. It records container occurrences with the image repository, tag, and digest kept separate.
- `helm` and `kustomize` run under pinned, confined execution: fixed arguments, a pinned executable, a scrubbed environment, a confined copy, a pre-scan for remote and exec features, limits, and an optional `bwrap` sandbox. A render that is partial or failed is a coverage gap and is never used as complete evidence. `assurance.rendering.requireOsSandbox: true` stops the run with `CO-PRV-004` (exit 5) on a machine without a usable sandbox. See [Confinement](../reference/assurance.md#confinement).
- `concordia lock update` writes `resolved.renderers.<tool>` with the version and the `sha256` of the renderer. A missing or different renderer is `CO-PRV-003` (exit 5). `doctor` probes `helm` and `kustomize` after `cosign`, and exits 5 when a declared source needs a missing tool.
- Terraform and OpenTofu plan JSON is read as evaluated-level evidence. Unknown and sensitive values stay gaps.
- With complete rendered evidence, `INFRA-TRC-004` of the [infrastructure pack](deployment-traceability.md) can pass for an environment. The profile entry for `container-occurrence-versions` and its digest do not change.

## Producers and provenance

- Statements are in-toto v1 and are verified with cosign against the producers that you declare. `assurance.verification.trustedRoot` lets cosign verify without network access, and under `--offline` it is the only way to verify a statement.
- The integrity of a statement is one of `content-not-verified`, `content-verified-by-digest`, and `producer-authenticated`. A claim needs a minimum integrity, and an inadmissible statement never supports a pass.
- Concordia reports an exact-lineage claim only inside the declared trust contract, together with deployment records and workload observations (provenance, deployed digest, drift). It never reports a SLSA level.
- Concordia claims no more than the contracts list. See [Assurance](../reference/assurance.md#claims-and-contracts) and [Redaction](../reference/redaction.md).

## Effective-policy evaluators

Evaluators for Kubernetes NetworkPolicy and RBAC run over rendered evidence, for the environments in `assurance.evaluators[]`. A selection edge alone never shows isolation, and no evaluator means no claim. A result that needs an enforcement assumption (`network-policy-enforced` or `rbac-authorizer-enabled`) that you did not declare is `unknown` with `enforcement-not-declared`.

## Check results and quality scenarios

- `TESTS` and `TESTS_FILE` stay candidates. Only an admissible check result for the evaluated revision can pass an obligation. Controls gain `VERIFIED_BY` check results through `check-ids` in the control configuration.
- Quality scenario files have three modes, kept separate: `structural` (verdicts of rules), `measured` (an admissible measurement), and `reviewed` (the assessment currency of the same run). A reviewed scenario in a workspace without an `assessments` key is `unknown` with `reviewed-mode-not-available`. An `ADDRESSES` link to an ADR is `unknown` with an explicit reason when the run does not evaluate ADR governance. See [Quality](../reference/assurance.md#quality).
- Results gain the `qualityEvaluation` area and the `assurance` object (`rendered`, `plans`, `statements`, `lineage`, `evaluations`, `checkResults`, `candidates`, `quality`, `qualityAreas`) and `toolchain.assurance`. `schemaVersion` stays `1.1` and the fields are additive. See [Assurance in results](../reference/results.md#assurance).
- The review dependency kinds `verification` and `quality-scenario` are now supplied when the workspace has `assurance`. An assessment that depends on a check result or a scenario is re-reviewed when it changes. See the [review currency release notes](review-currency.md).

## The `assurance` pack

Built-in pack `assurance`, version `1.0.0`. Add `- use: builtin:assurance` to `policies.packs`. The rules use the new `require.claim` predicate. The policy schema also gains `require.effectiveAccess` and `require.checkResult`. See [Assurance rules](../reference/assurance-rules.md).

| Rule | What it checks |
|---|---|
| `ASR-REN-001` | Each environment has complete rendered evidence. |
| `ASR-PRV-001` | Each container occurrence has exact lineage from source to deployed image. |
| `ASR-PRV-002` | The deployed digest of each container occurrence matches the observed digest. |

Every rule is advisory by default with severity `warning`, so the pack does not change the exit code. Every contract is experimental in this release, and the families have the maturity `advisory`: a mandatory rule on them is refused with `CO-POL-018`. Set `shadow` or `advisory` with `check.enforcement` (family `assurance`).

## Unknown reasons

Six reasons join the closed set: `claim-not-enabled`, `no-evidence-supplied`, `no-evaluator-available`, `rendered-evidence-missing`, `enforcement-not-declared`, and `reviewed-mode-not-available`.

## Commands and diagnostics

- `concordia assurance validate [--json]` loads the declared inputs as `check` does and shows how far each is admissible. It renders each source twice, evaluates no rule, and writes no file. See [assurance validate](../reference/cli.md#assurance-validate).
- `concordia doctor` lists the claim contracts and the registry digest, and `--json` has a `claims` object.
- New diagnostics `CO-PRV-001` to `CO-PRV-014` and `CO-QUA-001` to `CO-QUA-003`. A missing `cosign` is `CO-WRK-014`. Signing under `--offline` stays `CO-WRK-016`. No exit code is new.
- The evaluation bundle holds adapter contracts, evaluator identities, and redacted statements.

## What needs your action

- Nothing changes without an `assurance` key. To use the adapters, install `helm` or `kustomize` if you render them, run `concordia lock update`, declare the evidence, and run `concordia assurance validate`. See [Add stronger evidence](../how-to/add-stronger-evidence.md).
- Enable `builtin:assurance` only when you have evidence to supply: a rule without it is `unknown`.
