You are a developer on the `Gitea` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on Gitea

The architecture team wants Concordia to guard the module structure of Gitea. Set up the Concordia workspace from the architecture description below.

Relevant reference pages: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `exceptions.md`, `results.md`.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/gitea.calm.json`. Give every node a title-case `name` and a `description` (the role below). Put the tags in the node's `concordia.tags`.
- Observation: one project named `gitea` rooted at `.`, using codebase-memory-mcp in `moderate` index mode, that includes: `main.go`, `cmd/**`, `routers/**`, `services/**`, `models/**`, `modelmigration/**`, `modules/**`.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source roots; a more specific source root wins over a broader one.
- Policy pack `gitea-adopt` (version `1.0.0`) at `policies/gitea-adopt.policy.yaml`. It holds only the rules below, each with severity `error` and owner `team-gitea`. Do not keep a starter policy.
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.9, fails on unknown rule verdicts and fails on errors.

## Nodes

| Node id | Type | Tags | Source roots | Role |
|---|---|---|---|---|
| `gitea-cmd` | service | - | `main.go`, `cmd/**` | Command-line entry points. |
| `gitea-routers` | service | - | `routers/**` | HTTP routing and handlers. |
| `gitea-services` | service | - | `services/**` | Business services. |
| `gitea-models` | service | - | `models/**` | Database models. |
| `gitea-db` | database | - | `models/db/**` | Database access layer and engine. |
| `gitea-modelmigration` | service | - | `modelmigration/**` | Database schema migrations. |
| `gitea-modules` | service | `shared`, `migration-safe` | `modules/**` | Shared modules. |
| `gitea-setting` | service | `shared`, `migration-safe` | `modules/setting/**` | Configuration facade. |
| `gitea-web` | webclient | - | none (not bound) | Browser front end (web_src). |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second.

- `gitea-web` uses `gitea-routers`
- `gitea-cmd` uses `gitea-routers`
- `gitea-cmd` uses `gitea-services`
- `gitea-routers` uses `gitea-services`
- `gitea-routers` uses `gitea-models`
- `gitea-routers` uses `gitea-modules`
- `gitea-routers` uses `gitea-setting`
- `gitea-services` uses `gitea-models`
- `gitea-services` uses `gitea-modules`
- `gitea-services` uses `gitea-setting`
- `gitea-services` uses `gitea-db`
- `gitea-models` uses `gitea-modules`
- `gitea-models` uses `gitea-setting`
- `gitea-models` uses `gitea-db`
- `gitea-modelmigration` uses `gitea-modules`
- `gitea-modelmigration` uses `gitea-setting`
- `gitea-modelmigration` uses `gitea-db`

## Rules

Both rules count import and call dependencies between the code of the nodes.

- `GITEA-LYR-001` "Gitea layers depend downward only": the layers, from top to bottom, are `gitea-cmd`, then `gitea-routers`, then `gitea-services`, then `gitea-models`, then `gitea-db`, then `gitea-modules` together with `gitea-setting`. Code in a lower layer must never depend on code in a higher layer.
- `GITEA-MIG-001` "Migrations are frozen against current code": `gitea-modelmigration` must not depend directly on any other service or database node, because migrations must keep the schema of their time. Two exceptions: it may depend on the database layer `gitea-db`, and on any node that carries the tag `migration-safe`.

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node that has source roots is bound.
- Existing violations in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
