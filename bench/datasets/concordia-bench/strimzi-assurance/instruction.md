You are a developer on the `Strimzi` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Strimzi: assure what the Helm chart grants and what the tests prove

Strimzi ships the cluster operator as a Helm chart in `packaging/helm-charts/helm3/strimzi-kafka-operator`. The security team wants Concordia to render that chart, evaluate the rendered roles, and to claim a passing test run only when there is admissible evidence. Add a policy pack `strimzi-assurance` (version `1.0.0`) at `policies/strimzi-assurance.policy.yaml`, register it in `concordia.yaml` next to `strimzi-adopt`, and commit everything on `main`. Every rule has severity `error`, owner `team-strimzi`, a one-sentence rationale and a remediation.

Relevant documentation: `/opt/concordia/docs/reference/assurance.md`, `assurance-rules.md`, `workspace.md`, `bindings.md`, `policies.md`, `architecture.md`, `capability-profiles.md`, `diagnostics.md`.

## Environment and rendering

- Declare one environment `install` whose sources are the chart directory (`packaging/helm-charts/helm3/strimzi-kafka-operator/**`).
- Render the chart with Helm for the environment `install`: release `strimzi`, namespace `strimzi`. Concordia pins the renderer in `.concordia.lock`: update the lock so that the pin matches the installed Helm.
- Declare an evaluator for the environment `install` that evaluates RBAC (kind `rbac`) and states the assumption that the RBAC authorizer is enabled.
- Declare a second environment `edge` for the edge clusters: its sources are the same chart directory, rendered the same way (release `strimzi`, namespace `strimzi`). The security team has not qualified the RBAC authorizer of the edge clusters yet, so declare no evaluator for `edge`.
- Take the check results from `build/results/*.json`.

## Model and bindings

- Add a `data-asset` node `stz-co-config` ("Cluster Operator Configuration", description "Configuration map of the cluster operator."). Bind it to the ConfigMap `strimzi-cluster-operator` in the namespace `strimzi` of each of the environments `install` and `edge` (as a deployed Kubernetes resource of kind workload whose resource kind is ConfigMap: the RBAC evaluator reaches rendered resources only through workload and image bindings). The binding does not need to match source code.
- Bind the Deployment `strimzi-cluster-operator` (namespace `strimzi`, in each of the environments `install` and `edge`) to the existing node `stz-cluster-operator`, next to its source binding.
- The node `stz-cluster-operator` carries a testing control `testing-001` (description "The cluster operator has passing unit tests.") that names the check `operator-unit-tests`, with the control requirement URL `https://strimzi.example.org/concordia/controls/testing/1.0/requirement.json` (mapped in `architecture/url-mapping.json` to `architecture/resources/requirement.json`, which contains `{"description":"testing requirement"}`). The unknown disposition of the control is `warn`.

## Rules

- `STZ-AUTH-001` "The cluster operator is granted access to its configuration map": in every environment the effective authorization access from `stz-cluster-operator` to `stz-co-config` must be granted. Family `effective-access`.
- `STZ-VER-001` "The operator unit tests have an admissible result": the check `operator-unit-tests` must have an admissible result in the environment `install`.

## Check result evidence

Commit `build/results/operator-unit-tests.json`: an in-toto statement (not signed) of the check-result predicate type for the check `operator-unit-tests`, outcome `passed`, environment `install`, evaluator `mvn-test` version `1`, covering `stz-cluster-operator`, time `2026-01-01T00:00:00Z`, with the all-zero git commit as subject digest and revision. The statement has no signature, so it is not admissible evidence.

## Acceptance criteria

- `concordia model validate`, `concordia bind validate`, `concordia assurance validate` and `concordia check` run successfully with the exit codes the docs describe; everything is committed.
- Concordia must never claim that the unit tests passed on the unsigned statement: report that honestly (`unknown`), do not baseline or waive it.
- Where the chart render does not yet prove the access, or where no evaluator is declared (the `edge` environment), Concordia reports an honest `unknown` with its reason. Do not hide it.
