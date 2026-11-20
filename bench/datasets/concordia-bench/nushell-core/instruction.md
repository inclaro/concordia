You are a developer on the `Nushell` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Enable the Concordia core rules on Nushell

Concordia is already adopted on this repository (workspace `concordia.yaml`, model `architecture/nushell.calm.json`, pack `nushell-adopt`). The architecture team wants the built-in core rule pack on top of it, with the crate roles expressed as tags.

## Work

- Enable the built-in `core` pack in `concordia.yaml`. Keep the existing `nushell-adopt` pack.
- Tag these nodes in the CALM model (`concordia.tags`), in addition to the tags they already have:

| Node | Tag |
|---|---|
| `nu-cli` | `presentation` |
| `nu-command` | `application` |
| `nu-engine` | `domain` |
| `nu-plugin-engine` | `infrastructure` |
| `nu-protocol` | `contract` |
| `nu-utils` | `shared` |

- Override two core rules in the workspace, not by copying the pack:
  - `CORE-CYC-001` needs a file coverage of only 0.9 (instead of its default).
  - `CORE-LYR-003` has severity `error` (instead of its default warning).
- Findings that the core rules report on the current code are pre-existing: record them in the baseline (`exceptions/concordia.baseline.json`) and do not fix the code.

## Acceptance criteria

- The model, the workspace and the lock file are valid; everything is committed on `main`.
- `concordia check` exits 0 on the committed HEAD of `main` with no unknown verdict.
- The two overrides are visible in the check results: a violation of `CORE-LYR-003` is reported with severity `error`.

Relevant documentation: `/opt/concordia/docs/reference/core-rules.md`, `policies.md`, `workspace.md`, `architecture.md`.
