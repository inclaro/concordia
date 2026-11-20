You are a developer on the `airflow` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Airflow structure rules

Concordia is already adopted on this repository (pack `airflow-adopt`, baseline committed). The architecture team now wants stronger structure rules around the Task SDK and the core. Add them without changing the source code, and record the violations that already exist in the code in the baseline (`exceptions/concordia.baseline.json`), not fix them.

## Model changes

- Add the service node `af-sdk-api-client` (name `Sdk Api Client`, description "Execution API client of the Task SDK.", tag `contract`). Bind it to `task-sdk/src/airflow/sdk/api/**`; this root is more specific than the one of `af-task-sdk`, so the API client files leave `af-task-sdk`.
- Declare these relationships (`connects`, first node uses the second): `af-task-sdk` uses `af-sdk-api-client`; `af-sdk-api-client` uses `af-task-sdk`; `af-task-runner` uses `af-sdk-api-client`; `af-sdk-api-client` uses `af-task-runner`; `af-scheduler` uses `af-sdk-api-client`.

## Policy pack

Add the pack `airflow-structure` (version `1.0.0`) at `policies/airflow-structure.policy.yaml`, next to the existing pack, and list it in the workspace. Each rule has severity `error` and owner `team-airflow`. Edges are the kinds listed.

| Rule id | Intent | Subject | Target | Edges |
|---|---|---|---|---|
| `AF-DNY-001` | The task runner must not reach the metadata database through any chain of dependencies, up to four hops long (transitive coupling). | `af-task-runner` | `af-metadata-db` | `IMPORTS`, `CALLS` |
| `AF-API-001` | The standard provider may use the Task SDK, the task runner and the SDK API client only through the public SDK API: the package root file `task-sdk/src/airflow/sdk/__init__.py`, and everything under `task-sdk/src/airflow/sdk/bases/` and `task-sdk/src/airflow/sdk/definitions/`. Reaching into any other file of those three components is a leaky abstraction. | `af-provider-standard` | `af-task-sdk`, `af-task-runner`, `af-sdk-api-client` | `IMPORTS` |
| `AF-LYR-001` | The core components depend downward only. Layers from top to bottom: `af-api-server`; `af-scheduler` and `af-dag-processing`; `af-executors`; `af-models`; `af-metadata-db`. | - | - | `IMPORTS` |
| `AF-EDG-001` | The task runner must keep depending on the SDK API client: workers reach the core only through the Execution API client. | `af-task-runner` | `af-sdk-api-client` | `IMPORTS` |

## Acceptance criteria

- The model, the bindings and the policies are valid; the new node is bound.
- Existing violations of the new rules are in the baseline, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.

Relevant documentation: `/opt/concordia/docs/reference/policies.md`, `bindings.md`, `architecture.md`, `exceptions.md`, `workspace.md`.
