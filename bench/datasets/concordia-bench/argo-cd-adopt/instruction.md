You are a developer on the `argo-cd` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on Argo CD

The architecture team wants Concordia to guard the component structure of Argo CD (Go). Set up the Concordia workspace from the architecture description below.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `exceptions.md`.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/argo-cd.calm.json`. Give every node a title-case `name` and a `description` (the role below). Put the tags in the node's `concordia.tags`.
- Observation: one project named `argo-cd` rooted at `.`, using codebase-memory-mcp in `moderate` index mode, that includes only the Go files below `cmd`, `server`, `controller`, `reposerver`, `applicationset`, `cmpserver`, `commitserver`, `notification_controller`, `pkg`, `util` and `common` (globs of the form `server/**/*.go`). Do not exclude files and do not add a `.cbmignore`: other file types in the scope would lower the coverage.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source roots; a more specific source root wins over a broader one (`util/db/**` over `util/**`).
- Policies: the workspace loads every `policies/*.policy.yaml`. Put the rules below in the pack `argo-cd-adopt` (version `1.0.0`) at `policies/argo-cd-adopt.policy.yaml`, each with severity `error`, owner `team-argo-cd`, a one-sentence rationale and a remediation. Do not keep a starter policy.
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.9, fails on unknown rule verdicts and fails on errors.

## Nodes

All nodes are of type `service`.

| Node id | Tags | Source roots | Role |
|---|---|---|---|
| `argo-server` | - | `server/**`, `cmd/argocd-server/**` | API server and web UI backend. |
| `argo-repo-server` | - | `reposerver/**`, `cmd/argocd-repo-server/**` | Git and manifest generation service. |
| `argo-app-controller` | - | `controller/**`, `cmd/argocd-application-controller/**` | Application reconciliation controller. |
| `argo-appset-controller` | - | `applicationset/**`, `cmd/argocd-applicationset-controller/**` | ApplicationSet controller. |
| `argo-notifications` | - | `notification_controller/**`, `cmd/argocd-notification/**` | Notifications controller. |
| `argo-cmp-server` | - | `cmpserver/**`, `cmd/argocd-cmp-server/**` | Config management plugin sidecar server. |
| `argo-commit-server` | - | `commitserver/**`, `cmd/argocd-commit-server/**` | Manifest hydrator commit service. |
| `argo-apis` | `contract` | `pkg/apis/**`, `pkg/apiclient/**` | API types and generated gRPC clients. |
| `argo-db` | `restricted` | `util/db/**` | Settings and credential store access. |
| `argo-util` | `shared` | `util/**`, `common/**`, `pkg/client/**`, `pkg/ratelimiter/**` | Shared utilities. |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second.

- `argo-server` uses `argo-repo-server`, `argo-apis`, `argo-db` and `argo-util`
- `argo-app-controller` uses `argo-repo-server`, `argo-commit-server`, `argo-apis`, `argo-db` and `argo-util`
- `argo-appset-controller` uses `argo-repo-server`, `argo-apis`, `argo-db` and `argo-util`
- `argo-notifications` uses `argo-apis` and `argo-util`
- `argo-repo-server` uses `argo-apis` and `argo-util`
- `argo-cmp-server` uses `argo-apis` and `argo-util`
- `argo-commit-server` uses `argo-apis` and `argo-util`
- `argo-db` uses `argo-apis` and `argo-util`

## Rules

- `ARGO-API-001` "Argo CD components reach the gRPC services only through their client packages": the components `argo-server`, `argo-repo-server`, `argo-app-controller`, `argo-appset-controller`, `argo-notifications`, `argo-cmp-server` and `argo-commit-server` may use the repo server, the commit server and the plugin server only through their client packages: the files under `reposerver/apiclient/`, `commitserver/apiclient/` and `cmpserver/apiclient/`. Using any other file of those three services is a finding. Imports and calls both count.
- `ARGO-IND-001` "The Argo CD controllers and the API server do not depend on each other": `argo-server`, `argo-app-controller`, `argo-appset-controller` and `argo-notifications` are peers that deploy separately. No one of them may import or call another. Imports and calls both count.

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node is bound.
- Existing violations in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
