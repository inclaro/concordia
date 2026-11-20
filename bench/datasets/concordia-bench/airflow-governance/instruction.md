You are a developer on the `airflow` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# ADR governance for Airflow

Concordia is already adopted on this repository. Airflow keeps real architecture decision records under `airflow-core/adr/lang-sdk/` and `dev/breeze/doc/adr/`. The architecture team wants the decisions that govern the isolation of the task runtime registered, kept in force, and linked to the rules that enforce them.

## Workspace changes

- Index in `full` mode (the code graph reads Markdown decision records only in that mode) and add these decision files to the files of the observation project: `airflow-core/adr/lang-sdk/0002-workload-execution.md`, `airflow-core/adr/lang-sdk/0004-dag-parsing.md`, `dev/breeze/doc/adr/0001-record-architecture-decisions.md`.
- The repository holds many other decision records whose numbers collide with these three (every ADR directory starts at `0001`), and a decision number that appears twice makes the history ambiguous. Keep the code graph from indexing every decision record except these three: use the ignore file `.cbmignore` at the repository root to exclude the other ADR directories (`airflow-core/adr/lang-sdk/`, `dev/breeze/doc/adr/`, `go-sdk/adr/`, `java-sdk/adr/`, `ts-sdk/adr/`, `providers/common/sql/src/airflow/providers/common/sql/doc/adr/`) while keeping the three files above.
- Enable the built-in `adr-governance` pack.
- Decision register at `adr/decisions.yaml`, registered under the `adr` key. Only the status `accepted` is active. Register these decisions, each pinned to its file path and expected active:

| Decision id | File | Categories | Governs | Enforcement |
|---|---|---|---|---|
| `ADR-2` | `airflow-core/adr/lang-sdk/0002-workload-execution.md` | `system-boundaries` | node `af-task-runner` | required, enforced by rule `AF-ISO-001` of pack `airflow-adopt` version `1.0.0` |
| `ADR-4` | `airflow-core/adr/lang-sdk/0004-dag-parsing.md` | `dag-processing` | node `af-dag-processing` | required, enforced by rule `AF-DEC-001` of pack `airflow-adopt` version `1.0.0` |
| `ADR-1` | `dev/breeze/doc/adr/0001-record-architecture-decisions.md` | `quality-tradeoffs` | node `af-provider-standard` | none |

- Scope trigger `TRG-EXECUTION-API` (category `system-boundaries`): a change to any file under `airflow-core/src/airflow/api_fastapi/execution_api/` should be justified by a decision of that category.

## Acceptance criteria

- `concordia adr validate` exits 0, and everything is committed on `main`.
- `concordia check` exits 0 on the committed HEAD of `main`: every registered decision is committed and active, the enforcing rules are mandatory, and the supersession history is consistent.
- Do not modify the decision records.

Relevant documentation: `/opt/concordia/docs/reference/adr-governance.md`, `workspace.md`, `policies.md`, `/opt/concordia/docs/how-to/govern-architecture-decisions.md`.
