You are a developer on the `knative-serving` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on knative-serving

The platform team wants Concordia to guard the module structure of the Knative Serving control plane and data plane. Set up the Concordia workspace from the architecture description below.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `exceptions.md`.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/knative-serving.calm.json`. Give every node a title-case `name` and a `description` (the role below). Put the tags in the node's `concordia.tags`.
- Observation: one project named `knative-serving` rooted at `.`, using codebase-memory-mcp in `moderate` index mode, that includes `cmd/**` and `pkg/**`.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source roots.
- Policies: the workspace loads every `policies/*.policy.yaml`. Put the rules below in the pack `knative-serving-adopt` (version `1.0.0`) at `policies/knative-serving-adopt.policy.yaml`, each with severity `error`, owner `team-knative-serving`, a one-sentence rationale and a remediation. Do not keep a starter policy.
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.9, fails on unknown rule verdicts and fails on errors.

## Nodes

| Node id | Type | Tags | Source roots | Role |
|---|---|---|---|---|
| `kn-activator` | service | `public` | `cmd/activator/**`, `pkg/activator/**` | Request buffer and proxy that wakes revisions scaled to zero. |
| `kn-autoscaler` | service | - | `cmd/autoscaler/**`, `pkg/autoscaler/**` | Autoscaler that decides how many pods each revision needs. |
| `kn-controller` | service | - | `cmd/controller/**`, `pkg/reconciler/**` | Reconcilers for services, routes, configurations and revisions. |
| `kn-webhook` | service | - | `cmd/webhook/**`, `pkg/webhook/**` | Admission webhook that defaults and validates Serving resources. |
| `kn-queue` | service | - | `cmd/queue/**`, `pkg/queue/**` | Queue-proxy sidecar that runs in every user pod. |
| `kn-apis` | service | `contract` | `pkg/apis/**` | API types of the Serving resources. |
| `kn-client` | service | `contract` | `pkg/client/**` | Generated clients, informers and listers. |
| `kn-shared` | service | `shared` | `pkg/http/**`, `pkg/logging/**`, `pkg/metrics/**`, `pkg/networking/**`, `pkg/observability/**`, `pkg/resources/**`, `pkg/deployment/**`, `pkg/gc/**`, `pkg/leaderelection/**`, `pkg/cleanup/**` | Shared HTTP, logging, metrics, networking and resource helpers. |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second. The relationship id is `<source>-uses-<destination>`.

- `kn-activator` uses `kn-apis`
- `kn-activator` uses `kn-autoscaler`
- `kn-activator` uses `kn-client`
- `kn-activator` uses `kn-controller`
- `kn-activator` uses `kn-queue`
- `kn-activator` uses `kn-shared`
- `kn-autoscaler` uses `kn-apis`
- `kn-autoscaler` uses `kn-controller`
- `kn-autoscaler` uses `kn-shared`
- `kn-client` uses `kn-apis`
- `kn-controller` uses `kn-activator`
- `kn-controller` uses `kn-apis`
- `kn-controller` uses `kn-autoscaler`
- `kn-controller` uses `kn-client`
- `kn-controller` uses `kn-queue`
- `kn-controller` uses `kn-shared`
- `kn-queue` uses `kn-activator`
- `kn-queue` uses `kn-autoscaler`
- `kn-queue` uses `kn-shared`
- `kn-shared` uses `kn-apis`
- `kn-webhook` uses `kn-apis`
- `kn-webhook` uses `kn-autoscaler`
- `kn-webhook` uses `kn-controller`
- `kn-webhook` uses `kn-shared`

## Rules

- `KN-LYR-001` "Serving components depend downward only": the modules form three layers, from top to bottom: `kn-activator`, `kn-autoscaler`, `kn-controller`, `kn-webhook` and `kn-queue` together; `kn-shared`; `kn-apis` and `kn-client` together. A module may import or call only modules of its own or a lower layer. Imports and calls both count.
- `KN-CYC-001` "No dependency cycles between Serving modules": across all eight modules, no dependency cycle is allowed. Imports and calls both count, and code is judged at the level of the modules that it is bound to.

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node is bound.
- Existing violations in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
