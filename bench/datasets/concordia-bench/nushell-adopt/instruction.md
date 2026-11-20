You are a developer on the `Nushell` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on Nushell

The architecture team wants Concordia to guard the crate structure of the Nushell workspace. Set up the Concordia workspace from the architecture description below.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/nushell.calm.json`. Give every node a title-case `name` and a `description` (the role below). Put the tags in the node's `concordia.tags`.
- Observation: one project named `nushell` rooted at `.`, using codebase-memory-mcp in `moderate` index mode, that includes only the crate sources: `crates/*/src/**`.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source root.
- Policy pack `nushell-adopt` (version `1.0.0`) at `policies/nushell-adopt.policy.yaml`. It holds only the rule below, with severity `error` and owner `team-nushell`. Do not keep a starter policy.
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.9, fails on unknown rule verdicts and fails on errors.

## Nodes

One node for each workspace crate below. Every node has type `service`.

| Node id | Type | Tags | Source roots | Role |
|---|---|---|---|---|
| `nu-cli` | service | - | `crates/nu-cli/**` | Line editor, REPL and prompt. |
| `nu-command` | service | - | `crates/nu-command/**` | Built-in command implementations. |
| `nu-cmd-lang` | service | - | `crates/nu-cmd-lang/**` | Language keywords and core commands. |
| `nu-cmd-base` | service | - | `crates/nu-cmd-base/**` | Shared helpers for command crates. |
| `nu-cmd-plugin` | service | - | `crates/nu-cmd-plugin/**` | Commands that manage plugins. |
| `nu-parser` | service | - | `crates/nu-parser/**` | Parser. |
| `nu-plugin-engine` | service | - | `crates/nu-plugin-engine/**` | Engine side of the plugin protocol: spawns and talks to plugins. |
| `nu-plugin` | service | - | `crates/nu-plugin/**` | Library that plugin authors build on. |
| `nu-engine` | service | - | `crates/nu-engine/**` | Evaluation engine. |
| `nu-plugin-core` | service | - | `crates/nu-plugin-core/**` | Shared plugin communication code. |
| `nu-plugin-protocol` | service | - | `crates/nu-plugin-protocol/**` | Plugin wire protocol types. |
| `nu-protocol` | service | - | `crates/nu-protocol/**` | Core types: values, signatures, errors. |
| `nu-config` | service | - | `crates/nu-config/**` | Configuration types. |
| `nu-utils` | service | `shared` | `crates/nu-utils/**` | Foundation utilities. |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second, with the unique id `<source>-uses-<destination>`.

- `nu-cli` uses `nu-command`
- `nu-cli` uses `nu-cmd-base`
- `nu-cli` uses `nu-engine`
- `nu-cli` uses `nu-parser`
- `nu-cli` uses `nu-plugin-engine`
- `nu-cli` uses `nu-protocol`
- `nu-cli` uses `nu-config`
- `nu-cli` uses `nu-utils`
- `nu-command` uses `nu-cmd-base`
- `nu-command` uses `nu-engine`
- `nu-command` uses `nu-parser`
- `nu-command` uses `nu-protocol`
- `nu-command` uses `nu-config`
- `nu-command` uses `nu-utils`
- `nu-cmd-lang` uses `nu-cmd-base`
- `nu-cmd-lang` uses `nu-engine`
- `nu-cmd-lang` uses `nu-parser`
- `nu-cmd-lang` uses `nu-protocol`
- `nu-cmd-lang` uses `nu-utils`
- `nu-cmd-base` uses `nu-engine`
- `nu-cmd-base` uses `nu-parser`
- `nu-cmd-base` uses `nu-protocol`
- `nu-cmd-plugin` uses `nu-engine`
- `nu-cmd-plugin` uses `nu-protocol`
- `nu-cmd-plugin` uses `nu-plugin-engine`
- `nu-parser` uses `nu-engine`
- `nu-parser` uses `nu-plugin-engine`
- `nu-parser` uses `nu-protocol`
- `nu-parser` uses `nu-utils`
- `nu-plugin-engine` uses `nu-engine`
- `nu-plugin-engine` uses `nu-protocol`
- `nu-plugin-engine` uses `nu-plugin-core`
- `nu-plugin-engine` uses `nu-plugin-protocol`
- `nu-plugin-engine` uses `nu-utils`
- `nu-plugin` uses `nu-engine`
- `nu-plugin` uses `nu-protocol`
- `nu-plugin` uses `nu-plugin-core`
- `nu-plugin` uses `nu-plugin-protocol`
- `nu-plugin` uses `nu-utils`
- `nu-engine` uses `nu-protocol`
- `nu-engine` uses `nu-utils`
- `nu-plugin-core` uses `nu-protocol`
- `nu-plugin-core` uses `nu-plugin-protocol`
- `nu-plugin-core` uses `nu-utils`
- `nu-plugin-protocol` uses `nu-protocol`
- `nu-plugin-protocol` uses `nu-utils`
- `nu-protocol` uses `nu-config`
- `nu-protocol` uses `nu-utils`

## Rules

| Rule id | Intent | Layers (top to bottom) | Edges |
|---|---|---|---|
| `NU-LYR-001` | Crates depend downward only: a crate must never use a crate in a layer above it. Crates in the same layer may use each other. | [nu-cli] > [nu-command, nu-cmd-lang, nu-cmd-plugin] > [nu-cmd-base] > [nu-parser] > [nu-plugin-engine, nu-plugin] > [nu-engine] > [nu-plugin-core] > [nu-plugin-protocol] > [nu-protocol] > [nu-config] > [nu-utils] | `IMPORTS` |

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node is bound.
- Existing violations in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `exceptions.md`.
