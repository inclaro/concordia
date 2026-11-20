You are a developer on the `Hoppscotch` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Add the structure rules of Hoppscotch

Concordia is already adopted on `main`: the CALM model, the bindings, the pack `hoppscotch-adopt` and a baseline exist. The architecture team now wants rules about who may use what, and about how findings are counted.

Reference pages: `/opt/concordia/docs/reference/policies.md`, `results.md`, `exceptions.md`.

## What to add

- A new policy pack `hoppscotch-structure` (version `1.0.0`) at `policies/hoppscotch-structure.policy.yaml`. Register the file in `concordia.yaml` next to the existing pack file; keep the existing pack unchanged.
- The pack defines the same three named selectors as the adopt pack (`frontends` for all nodes of type `webclient`, `runtimes` for `hopp-js-sandbox` and `hopp-kernel`, and `clients` as an alias of `frontends`), and its rules use them by name instead of listing the nodes again, wherever the rule below says "runtimes", "frontends" or "clients".
- Every rule has severity `error` and owner `team-hoppscotch`, and only counts import evidence between nodes.
- Pre-existing violations that the new rules find in the code are recorded in the baseline (`exceptions/concordia.baseline.json`), not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`. Commit everything on `main`.

## Rules

| Rule id | Intent |
|---|---|
| `HOP-OWN-001` | The script runtimes (`runtimes`) belong to the clients: only `hopp-common`, `hopp-cli` and `hopp-selfhost-web` may import them. Any other package that imports a runtime is a violation (data ownership). |
| `HOP-BND-002` | A web client (`clients`) does not import the backend `hopp-backend` directly. Findings are counted per source symbol: several imports of the backend that one source file makes are one finding, not several. |
| `HOP-BND-003` | The backend `hopp-backend` does not import any web client (`frontends`). Findings are counted per pair of architecture nodes: however many files or imports go from the backend to the web clients, the rule reports one finding. |

## Acceptance criteria

- The new pack file is valid and registered; the adopt pack is untouched.
- A new import that breaks one of the rules is reported by the rule and blocks the check; an import that the rules allow is not reported.
- Everything is committed on `main`.
