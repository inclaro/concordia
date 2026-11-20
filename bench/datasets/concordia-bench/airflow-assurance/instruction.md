You are a developer on the `airflow` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Rendered-chart assurance for Airflow

Concordia is already adopted on this repository. The security team wants evidence about what the production chart really allows on the network, taken from the rendered chart and not from the code. Concordia renders Helm charts itself and evaluates network policies.

## Rendering and evaluators

- Values file `bench-values/prod.yaml`: executor `CeleryExecutor`, network policies enabled, PgBouncer enabled, and the pod disruption budgets of the scheduler and of the API server enabled.
- Declare the environment `prod` with the sources `chart/templates/**`, and add `chart/templates/**` to the files of the observation project.
- Declare assurance rendering for `prod`: the Helm chart `chart`, release `airflow`, namespace `airflow`, with the values file above. Refresh the lock file so that the Helm renderer is pinned.
- Run the network-policy evaluator and the RBAC evaluator for `prod`, and declare that the cluster enforces network policies and that the RBAC authorizer is enabled.
- Enable the built-in `assurance` pack.
- Add a second environment `staging` that is rendered the same way (same chart, release, namespace and sources) with the values file `bench-values/staging.yaml`: the same values as the production file, plus the PgBouncer port `6544` (`ports.pgbouncer`). The staging cluster is not known to enforce network policies, and nobody has confirmed its RBAC authorizer: run both evaluators for `staging` too, but do not declare any enforcement assumptions there.

## Model changes

Bind, in environment `prod`: `af-api-server` (Deployment `airflow-api-server`), `af-scheduler` (Deployment `airflow-scheduler`) and `af-dag-processing` (Deployment `airflow-dag-processor`) as workloads, and add three nodes:

| Node id | Type | Name | Description | Binding in `prod` |
|---|---|---|---|---|
| `af-namespace` | system | Airflow Namespace | The Kubernetes namespace that hosts the Airflow control plane. | the namespace `airflow` |
| `af-worker` | service | Worker | Celery worker pods that execute tasks. | StatefulSet `airflow-worker` |
| `af-pgbouncer` | service | Pgbouncer | The PgBouncer connection pool in front of the metadata database. | Deployment `airflow-pgbouncer` |

Bind `af-scheduler` (Deployment `airflow-scheduler`), `af-worker`, `af-pgbouncer` and `af-namespace` in environment `staging` the same way as in `prod`.

## Policy pack

Add the pack `airflow-assurance` (version `1.0.0`) at `policies/airflow-assurance.policy.yaml` and list it in the workspace. Each rule has severity `error` and owner `team-airflow`:

| Rule id | Intent |
|---|---|
| `AF-NET-001` | The effective network policy must deny traffic from `af-worker` to `af-pgbouncer` on port 6543 (AIP-72: workers do not reach the database pool). |
| `AF-NET-002` | The effective network policy must allow traffic from `af-scheduler` to `af-pgbouncer` on port 6543. |

The assurance rules are advisory in this release. Report what Concordia finds; do not change the chart.

## Acceptance criteria

- The assurance declarations validate (`concordia assurance validate` exits 0), and the model, bindings and policies are valid and committed on `main`.
- `concordia check` exits 0 on the committed HEAD of `main`.

Relevant documentation: `/opt/concordia/docs/reference/assurance.md`, `assurance-rules.md`, `workspace.md`, `policies.md`, `bindings.md`, `results.md`.
