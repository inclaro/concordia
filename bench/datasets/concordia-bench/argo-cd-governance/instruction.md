You are a developer on the `argo-cd` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Govern the architecture decisions of Argo CD

Concordia is already adopted on Argo CD (model, bindings, pack `argo-cd-adopt` and baseline are committed on `main`). The architecture board wants the decisions behind the rules to be committed, registered and enforced. Add ADR governance; keep what exists.

Relevant documentation: `/opt/concordia/docs/reference/adr-governance.md`, `workspace.md` (`adr`, `observation`), `policies.md`, `core-rules.md`, `results.md`, `diagnostics.md`.

## Decision documents

The code graph provider reads Markdown, and so the decisions, only in the index mode `full`: set `observation.indexMode` to `full` and add `docs/adr/**` to the observation `include` (next to the Go globs). Write three decision documents. Each has the title line `# ADR-<number>: <title>` (four-digit number), a line `Status: <status>`, a date, the deciders (`Team Argo CD`) and the sections Context, Decision and Consequences (one or two sentences each). The context of the first two names the Argo CD proposals that motivate them (`docs/proposals/config-management-plugin-v2.md` and `docs/proposals/manifest-hydrator.md`; `docs/proposals/003-applications-outside-argocd-namespace.md`).

| File | Title | Status |
|---|---|---|
| `docs/adr/0001-grpc-client-packages.md` | Components reach the gRPC services through their client packages | `Approved` |
| `docs/adr/0002-independent-components.md` | The API server and the controllers are independent | `Approved` |
| `docs/adr/0003-install-variants.md` | Argo CD ships namespace-scoped and HA install variants | `Accepted` |

## Register and lifecycle

- The decision register is `docs/adr/decisions.yaml`. Register the three decisions as `ADR-0001`, `ADR-0002` and `ADR-0003`, each pinned to its file, expected `active`.
  - `ADR-0001` (category `system-boundaries`) governs the nodes `argo-repo-server`, `argo-commit-server` and `argo-cmp-server`; it requires enforcement by the rule `ARGO-API-001` of the pack `argo-cd-adopt` (version `1.0.0`).
  - `ADR-0002` (category `system-boundaries`) governs `argo-server`, `argo-app-controller`, `argo-appset-controller` and `argo-notifications`; it requires enforcement by `ARGO-IND-001` of the pack `argo-cd-adopt`.
  - `ADR-0003` (category `deployment-strategy`) governs `argo-server`, `argo-repo-server`, `argo-app-controller`, `argo-appset-controller` and `argo-notifications`; it requires no enforcement.
- Only the status `accepted` makes a decision active, and the status text `Approved` means `accepted`.
- Add the trigger `TRG-INSTALL` (category `deployment-strategy`) that fires on every change below `manifests/**`.
- Enable the built-in pack `builtin:adr-governance`.

## Acceptance criteria

- `concordia adr validate` exits 0.
- `concordia check` exits 0 on the committed HEAD of `main`, and every decision rule that can be evaluated without a change set passes.
- Everything is committed on `main`.
