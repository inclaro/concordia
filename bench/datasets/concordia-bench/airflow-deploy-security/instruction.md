You are a developer on the `airflow` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Typed security and availability obligations for the Airflow chart

Concordia traces the Airflow deployables (environment `chart`, `builtin:infrastructure`). The Helm templates cannot be judged by Concordia, so the security team wants the production configuration rendered to plain manifests, committed, and checked with typed deployment rules.

## Rendered production manifests

- Values file `bench-values/prod.yaml`: executor `CeleryExecutor`, network policies enabled, PgBouncer enabled, and the pod disruption budgets of the scheduler and of the API server enabled.
- Render the chart `chart/` with release name `airflow` and namespace `airflow` using those values (`helm template`), and commit the result as `bench-env/prod-hydrated/airflow.yaml`. Helm leaves most resources without a namespace: set `metadata.namespace: airflow` on every namespaced resource, and the namespace of service account subjects (the helper `/opt/concordia-bench/bin/hydrate-namespace.py <in> <out> airflow`, run with `/opt/venv/bin/python`, does this).
- Declare the environment `prod-hydrated` with the sources `bench-env/prod-hydrated/**`, and add `bench-env/**` to the files of the observation project.

## Model changes

- Add the service node `af-worker` (name `Worker`, description "Celery worker pods that execute tasks.") bound, in environment `prod-hydrated`, to the StatefulSet `airflow-worker`.
- Add the data-asset node `af-metadata-secret` (name `Metadata Secret`, description "The Secret that holds the metadata database connection.") bound, in environment `prod-hydrated`, to the Secret `airflow-metadata` of namespace `airflow` (the infrastructure resource of the environment).
- Bind `af-api-server` (Deployment `airflow-api-server`), `af-scheduler` (Deployment `airflow-scheduler`) and `af-dag-processing` (Deployment `airflow-dag-processor`) to their workloads in environment `prod-hydrated` too, and `af-namespace` to the namespace `airflow` in that environment.

## Policy pack

Add the pack `airflow-deploy-security` (version `1.0.0`) at `policies/airflow-deploy-security.policy.yaml`, list it in the workspace, with severity `error` and owner `team-airflow` for each rule. Both rules apply to the environment `prod-hydrated` only.

| Rule id | Family | Intent |
|---|---|---|
| `AF-SEC-001` | `deployment-security` | The worker workload `af-worker` must not reference the secret `af-metadata-secret` (AIP-72: workers no longer need the database connection). |
| `AF-REL-001` | `deployment-availability` | The workloads of `af-scheduler` and `af-api-server` must each be the target of a pod disruption budget. A present budget is enough evidence for the rule to hold. |

Typed rules are advisory in this release: add `check.enforcement` entries that set the families `deployment-security` and `deployment-availability` to `advisory`, next to the entries that already exist.

## Acceptance criteria

- The model, the bindings and the policy are valid and committed on `main`.
- `concordia check` exits 0 on the committed HEAD of `main`. The existing worker secret reference is a real finding of the chart: it is reported, not fixed.

Relevant documentation: `/opt/concordia/docs/reference/policies.md`, `infrastructure-rules.md`, `workspace.md`, `bindings.md`, `results.md`.
