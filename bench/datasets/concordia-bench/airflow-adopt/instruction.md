You are a developer on the `airflow` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on Airflow

The architecture team wants Concordia to guard the isolation of the Airflow task execution runtime (AIP-72): the Task SDK and the task runner on the workers must reach the core only through the Execution API, never through the core models, the metadata database or the scheduler. Set up the Concordia workspace from the architecture description below.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/airflow.calm.json`. Give every node a title-case `name` and a `description` (the role below). Put the tags in the node's `concordia.tags`.
- Observation: one project named `airflow` rooted at `.`, using codebase-memory-mcp in `moderate` index mode. Include only the files below (the code graph does not index the `public` route and service packages, so leave them out of the scope): `airflow-core/src/airflow/api_fastapi/*.py`, `airflow-core/src/airflow/api_fastapi/auth/**/*.py`, `airflow-core/src/airflow/api_fastapi/common/**/*.py`, `airflow-core/src/airflow/api_fastapi/core_api/*.py`, `airflow-core/src/airflow/api_fastapi/core_api/datamodels/**/*.py`, `airflow-core/src/airflow/api_fastapi/core_api/openapi/**/*.py`, `airflow-core/src/airflow/api_fastapi/core_api/routes/*.py`, `airflow-core/src/airflow/api_fastapi/core_api/routes/ui/**/*.py`, `airflow-core/src/airflow/api_fastapi/core_api/services/*.py`, `airflow-core/src/airflow/api_fastapi/core_api/services/ui/**/*.py`, `airflow-core/src/airflow/api_fastapi/execution_api/**/*.py`, `airflow-core/src/airflow/api_fastapi/logging/**/*.py`, `airflow-core/src/airflow/jobs/**/*.py`, `airflow-core/src/airflow/dag_processing/**/*.py`, `airflow-core/src/airflow/executors/**/*.py`, `airflow-core/src/airflow/models/**/*.py`, `airflow-core/src/airflow/utils/session.py`, `airflow-core/src/airflow/settings.py`, `task-sdk/src/airflow/sdk/**/*.py`, `providers/standard/src/**/*.py`.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source roots; a more specific source root wins over a broader one.
- Policy pack `airflow-adopt` (version `1.0.0`) at `policies/airflow-adopt.policy.yaml`. It holds only the rules below, each with severity `error` and owner `team-airflow`. Do not keep a starter policy.
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.9, fails on unknown rule verdicts and fails on errors.

## Nodes

| Node id | Type | Tags | Source roots | Role |
|---|---|---|---|---|
| `af-api-server` | service | `presentation` | `airflow-core/src/airflow/api_fastapi/**` | Public REST API server and UI backend. |
| `af-execution-api` | service | `contract` | `airflow-core/src/airflow/api_fastapi/execution_api/**` | Internal Execution API that workers call instead of the database. |
| `af-scheduler` | service | - | `airflow-core/src/airflow/jobs/**` | Scheduler, triggerer and job runners. |
| `af-dag-processing` | service | - | `airflow-core/src/airflow/dag_processing/**` | DAG file parsing and bundle management. |
| `af-executors` | service | - | `airflow-core/src/airflow/executors/**` | Executors that dispatch workloads to workers. |
| `af-models` | service | `domain` | `airflow-core/src/airflow/models/**` | ORM models of the metadata database. |
| `af-metadata-db` | database | - | `airflow-core/src/airflow/utils/session.py`, `airflow-core/src/airflow/settings.py` | Session and engine access to the metadata database. |
| `af-task-sdk` | service | `contract` | `task-sdk/src/airflow/sdk/**` | Public Task SDK for authoring DAGs and tasks. |
| `af-task-runner` | service | - | `task-sdk/src/airflow/sdk/execution_time/**` | Task execution runtime that runs inside workers. |
| `af-provider-standard` | service | - | `providers/standard/src/**` | The standard provider with core operators and sensors. |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second. The relationship from `af-provider-standard` to `af-executors` is only recognised as a call-level dependency: restrict the edge kinds that count as observation of it to `CALLS`.

- `af-api-server` uses `af-dag-processing`, `af-execution-api`, `af-metadata-db`, `af-models`, `af-scheduler`
- `af-dag-processing` uses `af-execution-api`, `af-executors`, `af-metadata-db`, `af-models`, `af-task-runner`, `af-task-sdk`
- `af-execution-api` uses `af-api-server`, `af-metadata-db`, `af-models`
- `af-executors` uses `af-execution-api`, `af-models`
- `af-models` uses `af-dag-processing`, `af-execution-api`, `af-executors`, `af-metadata-db`, `af-task-sdk`
- `af-provider-standard` uses `af-executors`, `af-metadata-db`, `af-models`, `af-task-runner`, `af-task-sdk`
- `af-scheduler` uses `af-dag-processing`, `af-execution-api`, `af-executors`, `af-metadata-db`, `af-models`, `af-task-runner`, `af-task-sdk`
- `af-task-runner` uses `af-dag-processing`, `af-task-sdk`
- `af-task-sdk` uses `af-dag-processing`, `af-provider-standard`, `af-task-runner`

## Rules

| Rule id | Intent | Subject | Target | Edges |
|---|---|---|---|---|
| `AF-ISO-001` | The Task SDK and the task runner must not depend directly on the core models, the metadata database or the scheduler. Judge direct dependencies only. | `af-task-sdk`, `af-task-runner` | `af-models`, `af-metadata-db`, `af-scheduler` | `IMPORTS`, `CALLS` |
| `AF-DEC-001` | Every dependency between two components (service or database nodes) must be declared as a relationship in the architecture, and must be of a kind that relationship allows. | all `service` and `database` nodes | - | `IMPORTS` |

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node is bound.
- Existing violations in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `exceptions.md`.
