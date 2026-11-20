You are a developer on the `Strimzi` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on Strimzi

The architecture team wants Concordia to guard the module structure of the Strimzi Kafka operators (Java, Maven multi-module). Set up the Concordia workspace from the architecture description below.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `exceptions.md`.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/strimzi.calm.json`. Give every node a title-case `name` and a `description` (the role below). Put the tags in the node's `concordia.tags`.
- Observation: one project named `strimzi` rooted at `.`, using codebase-memory-mcp in `moderate` index mode, that includes the `src/main/**` tree of each module in the table below (for example `api/src/main/**`). Test code is not included.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source root.
- Policies: the workspace loads every `policies/*.policy.yaml`. Put the rules below in the pack `strimzi-adopt` (version `1.0.0`) at `policies/strimzi-adopt.policy.yaml`, each with severity `error`, owner `team-strimzi`, a one-sentence rationale and a remediation. Do not keep a starter policy.
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.9, fails on unknown rule verdicts and fails on errors.

## Nodes

| Node id | Type | Tags | Source root | Role |
|---|---|---|---|---|
| `stz-cluster-operator` | service | - | `cluster-operator/src/main/**` | Operator that manages Kafka clusters. |
| `stz-topic-operator` | service | - | `topic-operator/src/main/**` | Operator that manages Kafka topics. |
| `stz-user-operator` | service | - | `user-operator/src/main/**` | Operator that manages Kafka users. |
| `stz-operator-common` | service | `shared` | `operator-common/src/main/**` | Code shared by the operators. |
| `stz-certificate-issuer` | service | - | `certificate-issuer/src/main/**` | Certificate generation helpers. |
| `stz-config-model` | service | - | `config-model/src/main/**` | Kafka configuration model. |
| `stz-api` | service | `contract` | `api/src/main/**` | Custom resource model of Strimzi. |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second.

- `stz-cluster-operator` uses `stz-api`, `stz-operator-common`, `stz-certificate-issuer` and `stz-config-model`
- `stz-topic-operator` uses `stz-operator-common` and `stz-api`
- `stz-user-operator` uses `stz-operator-common`, `stz-api` and `stz-certificate-issuer`
- `stz-operator-common` uses `stz-api` and `stz-certificate-issuer`

## Rules

- `STZ-LYR-001` "Strimzi layers depend downward only": the modules form five layers, from top to bottom `stz-cluster-operator`; `stz-topic-operator` and `stz-user-operator`; `stz-operator-common` and `stz-certificate-issuer`; `stz-config-model`; `stz-api`. A module may import only modules of its own or a lower layer. Only imports count.
- `STZ-API-001` "The API model stays a leaf": `stz-api` must not import any of the other six modules (the three operators, operator-common, certificate-issuer, config-model), directly. Only imports count.

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node is bound.
- Existing violations in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
