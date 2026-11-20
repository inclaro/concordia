You are a developer on the `fzf` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on fzf

The architecture team wants Concordia to guard the module structure of fzf. Set up the Concordia workspace from the architecture description below.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `exceptions.md`.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/fzf.calm.json`. Give every node a title-case `name` and a `description` (the role below). Put the tags in the node's `concordia.tags`.
- Observation: one project named `fzf` rooted at `.`, using codebase-memory-mcp in `moderate` index mode, that includes `main.go` and `src/**`.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source roots; a more specific source root wins over a broader one.
- Policies: the workspace loads every `policies/*.policy.yaml`. Put the rules below in the pack `fzf-adopt` (version `1.0.0`) at `policies/fzf-adopt.policy.yaml`, each with severity `error`, owner `team-fzf`, a one-sentence rationale and a remediation. Do not keep a starter policy.
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.9, fails on unknown rule verdicts and fails on errors.

## Nodes

| Node id | Type | Tags | Source roots | Role |
|---|---|---|---|---|
| `fzf-cli` | service | - | `main.go` | Entry point. |
| `fzf-core` | service | - | `src/*.go` | Option parsing and the finder loop. |
| `fzf-tui` | service | - | `src/tui/**` | Terminal rendering. |
| `fzf-algo` | service | - | `src/algo/**` | Fuzzy matching. |
| `fzf-protector` | service | - | `src/protector/**` | OS sandboxing. |
| `fzf-util` | service | `shared` | `src/util/**` | Utilities. |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second.

- `fzf-cli` uses `fzf-core`
- `fzf-cli` uses `fzf-protector`
- `fzf-core` uses `fzf-tui`
- `fzf-core` uses `fzf-algo`
- `fzf-core` uses `fzf-util`
- `fzf-tui` uses `fzf-util`
- `fzf-algo` uses `fzf-util`

## Rules

- `FZF-LYR-001` "fzf layers depend downward only": the modules form four layers, from top to bottom `fzf-cli`; `fzf-core`; `fzf-tui`, `fzf-algo` and `fzf-protector`; `fzf-util`. A module may import or call only modules of its own or a lower layer. Imports and calls both count.
- `FZF-CYC-001` "No import cycles between the fzf leaf modules": across `fzf-cli`, `fzf-protector` and `fzf-util`, the team tolerates at most zero cycles. (The finder core and the matcher already form a cycle through the test files; that one is out of scope.) When the count goes above that, Concordia reports one finding that gives the number of cycles. Imports and calls both count.

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node is bound.
- Existing violations in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
