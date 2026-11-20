You are a developer on the `Hoppscotch` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Enable the core rule pack on Hoppscotch

Concordia is already adopted on `main`. The architecture team wants the generic rules of the built-in core pack on top of the Hoppscotch rules, so that layering, service boundaries and declared dependencies are checked by convention.

Reference pages: `/opt/concordia/docs/reference/core-rules.md`, `workspace.md` (policy packs and rule overrides), `policies.md`, `architecture.md` (tags).

## What to change

- Use the built-in pack `builtin:core` in `concordia.yaml` (keep the existing pack file `policies/hoppscotch-adopt.policy.yaml`).
- Tag the nodes in `architecture/hoppscotch.calm.json` (in the node's `concordia.tags`) so that the core conventions apply. Keep every existing tag:

| Node id | Tags after the change |
|---|---|
| `hopp-selfhost-web` | `presentation`, `client` |
| `hopp-sh-admin` | `presentation`, `client` |
| `hopp-backend` | `application` |
| `hopp-common` | `application` |
| `hopp-data` | `shared-kernel`, `domain`, `shared` |
| `hopp-kernel` | `infrastructure` |

- Apply these overrides to the core pack, without copying the pack:
  - `CORE-DECL-001`: severity `warning` (the Hoppscotch pack already holds the dependency rule as an error).
  - `CORE-OBS-001`: disabled (the Hoppscotch pack already holds the required-dependency rule).
  - `CORE-CYC-001`: severity `warning`. The code graph of this monorepo contains name-resolution noise, so cycles are a warning and not a gate.
  - `CORE-DB-001` and `CORE-LYR-002`: an `unknown` verdict does not fail the run (warn only). Some call evidence of the repository goes to HTTP routes that no node is bound to, so Concordia cannot judge these two rules completely. The team accepts this: the gaps are reported (as binding gaps) but do not fail the build, and you must not add bindings for the routes.
- Pre-existing violations that the pack finds are recorded in the baseline, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`. Commit everything on `main`.

## Acceptance criteria

- The pack is active with the tags and overrides above; no pack file was copied.
- A backend that imports another service's code, a lower package that imports a higher layer, a domain package that depends on infrastructure, and shared code that depends on a service are reported by the matching core rules. An import of the shared-kernel package by a service is not reported by the service-boundary rule.
- Everything is committed on `main`.
