You are a developer on the `Hoppscotch` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on Hoppscotch

The architecture team wants Concordia to guard the module structure of Hoppscotch. Set up the Concordia workspace from the architecture description below.

Reference pages: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `results.md`, `exceptions.md`.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/hoppscotch.calm.json`. Give every node a title-case `name` and a `description` (the role below). Put the tags in the node's `concordia.tags`.
- Observation: one project named `hoppscotch` rooted at `.`, using codebase-memory-mcp in `moderate` index mode, that includes the production sources of the packages and leaves out their test folders: `packages/hoppscotch-backend/src/**`, `packages/hoppscotch-common/src/**`, `packages/hoppscotch-data/src/**`, `packages/hoppscotch-kernel/src/**`, `packages/hoppscotch-selfhost-web/src/**`, `packages/hoppscotch-sh-admin/src/**`; for `hoppscotch-cli`: `src/*.ts` and the folders `commands`, `handlers`, `interfaces`, `options`, `types` and `utils` of `src`; for `hoppscotch-js-sandbox`: `src/*.ts` and the folders `bootstrap-code`, `cage-modules`, `constants`, `node`, `types`, `utils` and `web` of `src`.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source roots; a more specific source root wins over a broader one.
- Policy pack `hoppscotch-adopt` (version `1.0.0`) at `policies/hoppscotch-adopt.policy.yaml`. It holds only the rules below, each with severity `error` and owner `team-hoppscotch`. Do not keep a starter policy. The pack defines three named selectors that its rules and later packs reuse: `frontends` (all nodes of type `webclient`), `runtimes` (the nodes `hopp-js-sandbox` and `hopp-kernel`) and `clients` (an alias that refers to `frontends`).
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.85, fails on unknown rule verdicts and fails on errors.

## Nodes

| Node id | Type | Tags | Source roots | Role |
|---|---|---|---|---|
| `hopp-backend` | service | - | `packages/hoppscotch-backend/**` | NestJS GraphQL backend. |
| `hopp-cli` | service | - | `packages/hoppscotch-cli/**` | Command-line test runner. |
| `hopp-selfhost-web` | webclient | - | `packages/hoppscotch-selfhost-web/**` | Self-hosted web app. |
| `hopp-sh-admin` | webclient | - | `packages/hoppscotch-sh-admin/**` | Self-hosted admin dashboard. |
| `hopp-common` | service | `application` | `packages/hoppscotch-common/**` | Shared front-end application code. |
| `hopp-js-sandbox` | service | - | `packages/hoppscotch-js-sandbox/**` | Sandbox for pre-request and test scripts. |
| `hopp-kernel` | service | - | `packages/hoppscotch-kernel/**` | Platform kernel for I/O and relay. |
| `hopp-data` | service | `shared-kernel`, `domain` | `packages/hoppscotch-data/**` | Shared data types and schemas. |
| `hopp-db` | database | - | none (not bound) | PostgreSQL database. |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second, with the unique id `<source>-uses-<destination>` (for example `hopp-selfhost-web-uses-hopp-common`). Unless a row says otherwise, the relationship counts only import evidence as an observation of the dependency.

| From | To | Note |
|---|---|---|
| `hopp-selfhost-web` | `hopp-common` | |
| `hopp-selfhost-web` | `hopp-data` | |
| `hopp-selfhost-web` | `hopp-kernel` | The dependency is required: it must be observed in the code. Only call evidence counts, and evidence of heuristic confidence is enough. |
| `hopp-selfhost-web` | `hopp-backend` | |
| `hopp-sh-admin` | `hopp-backend` | |
| `hopp-common` | `hopp-data` | |
| `hopp-common` | `hopp-js-sandbox` | |
| `hopp-common` | `hopp-kernel` | |
| `hopp-cli` | `hopp-data` | |
| `hopp-cli` | `hopp-js-sandbox` | |
| `hopp-js-sandbox` | `hopp-data` | |
| `hopp-backend` | `hopp-db` | |
| `hopp-kernel` | `hopp-data` | Only call evidence counts as an observation of this dependency, not imports. |

## Rules

| Rule id | Intent |
|---|---|
| `HOP-DEC-001` | Every import or call between `service` and `webclient` packages must match a declared relationship, and only with the declared evidence kinds (import evidence only). An import with no relationship is an unreviewed dependency; an import that the model only allows as a call is a contradiction of the model. |
| `HOP-OBS-001` | Every relationship that the model marks as required must be observed in the code. Applies to `service` and `webclient` nodes. The rule is judged with a file coverage of at least 0.85 and a coverage of at least 0.4 for each language (the test specs of the repository are never indexed). |

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node that has source roots is bound.
- Existing violations in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
