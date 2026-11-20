You are a developer on the `Nushell` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Add the structure rules to Nushell

Concordia is already adopted on this repository (workspace `concordia.yaml`, model `architecture/nushell.calm.json`, pack `nushell-adopt`). The architecture team wants four more rules that protect the crate structure. Add them as a new policy pack and keep everything else.

## Policy pack

- Pack id `nushell-structure`, version `1.0.0`, at `policies/nushell-structure.policy.yaml`, registered in `concordia.yaml` beside the existing pack.
- Every rule has severity `error` and owner `team-nushell`. Every rule only counts `IMPORTS` edges.
- Findings that these rules report on the current code are pre-existing: record them in the baseline (`exceptions/concordia.baseline.json`) and do not fix the code.

## Rules

| Rule id | Intent |
|---|---|
| `NU-BUD-001` | The plugin wire protocol crate (`nu-plugin-protocol`) must stay free of the evaluation engine, the plugin engine and the parser (`nu-engine`, `nu-plugin-engine`, `nu-parser`). Today it has no such dependency, and the number of such dependencies must never grow above that count (a ratchet, not a per-edge prohibition). |
| `NU-EDG-001` | Plugin commands build on the protocol: `nu-cmd-plugin` must keep depending on `nu-protocol`. The rule reports the absence of that dependency. |
| `NU-COV-001` | `nu-utils` is the lowest layer and must not depend on the plugin command crate (`nu-cmd-plugin`). The rule is only trusted when the indexed code is well covered: it needs a file coverage of at least 0.95 and a language coverage of at least 0.95, and an `unknown` verdict fails the run. |
| `NU-COV-002` | `nu-config` is a leaf below `nu-protocol` and must not depend on the plugin command crate (`nu-cmd-plugin`). It has the same coverage requirements as `NU-COV-001` (0.95 file coverage, 0.95 language coverage), but an `unknown` verdict only warns. |

## Acceptance criteria

- The new pack is valid and registered; the earlier rule `NU-LYR-001` still works.
- `concordia check` exits 0 on the committed HEAD of `main` with no unknown verdict.
- Everything is committed on `main`.

Relevant documentation: `/opt/concordia/docs/reference/policies.md`, `workspace.md`, `exceptions.md`, `results.md`.
