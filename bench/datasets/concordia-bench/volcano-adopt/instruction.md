You are a developer on the `volcano` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Adopt Concordia on Volcano

The architecture team wants Concordia to guard the scheduler plugin structure of Volcano, the batch scheduler for Kubernetes. Set up the Concordia workspace from the architecture description below.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `bindings.md`, `policies.md`, `exceptions.md`.

## Workspace

- Workspace file: `concordia.yaml` at the repository root.
- CALM architecture: `architecture/volcano.calm.json`. Give every node a `name` and a `description` (the role below). Put the tags in the node's `concordia.tags`.
- Observation: one project named `volcano` rooted at `.`, using codebase-memory-mcp in `moderate` index mode. Include only the directories that the nodes below cover: `cmd/scheduler`, `cmd/controller-manager`, `cmd/webhook-manager`, `cmd/agent`, `pkg/scheduler`, `pkg/controllers`, `pkg/webhooks`, `pkg/agent`, `pkg/util`, `pkg/kube`, `pkg/features`, `pkg/config`, `pkg/signals`, `pkg/version` and `pkg/filewatcher` (each as `<dir>/**`). Leave the rest of the repository out of the project.
- Bindings: `bindings/concordia.bindings.yaml`. Bind each node to its source roots; a more specific source root wins over a broader one, so the six plugin nodes win over the catch-all plugin node, and the framework node wins over the scheduler node.
- Policies: the workspace loads every `policies/*.policy.yaml`. Put the rules below in the pack `volcano-adopt` (version `1.0.0`) at `policies/volcano-adopt.policy.yaml`, each with severity `error`, owner `team-volcano`, a one-sentence rationale and a remediation. Do not keep a starter policy.
- Exceptions: the baseline at `exceptions/concordia.baseline.json` and the waiver register at `exceptions/waivers.yaml`.
- The check requires a file coverage of at least 0.9, fails on unknown rule verdicts and fails on errors.

## Nodes

| Node id | Type | Tags | Source roots | Role |
|---|---|---|---|---|
| `vc-scheduler` | service | - | `cmd/scheduler/**`, `pkg/scheduler/*.go` and the scheduler packages `actions`, `cache`, `conf`, `util`, `metrics`, `gate`, `unschedulable`, `uthelper` under `pkg/scheduler/` | Batch scheduler: cache, actions and the scheduling loop. |
| `vc-framework` | service | `contract` | `pkg/scheduler/framework/**`, `pkg/scheduler/api/**` | Scheduler framework and API types that plugins build on. |
| `vc-plugin-gang` | service | - | `pkg/scheduler/plugins/gang/**` | The gang scheduler plugin. |
| `vc-plugin-drf` | service | - | `pkg/scheduler/plugins/drf/**` | The drf scheduler plugin. |
| `vc-plugin-proportion` | service | - | `pkg/scheduler/plugins/proportion/**` | The proportion scheduler plugin. |
| `vc-plugin-binpack` | service | - | `pkg/scheduler/plugins/binpack/**` | The binpack scheduler plugin. |
| `vc-plugin-predicates` | service | - | `pkg/scheduler/plugins/predicates/**` | The predicates scheduler plugin. |
| `vc-plugin-capacity` | service | - | `pkg/scheduler/plugins/capacity/**` | The capacity scheduler plugin. |
| `vc-plugins-other` | service | - | `pkg/scheduler/plugins/**` (every plugin directory not listed above, and the files directly in it) | All other scheduler plugins and plugin utilities. |
| `vc-controllers` | service | - | `cmd/controller-manager/**`, `pkg/controllers/**` | Controller manager for jobs, queues and pod groups. |
| `vc-admission` | service | `public` | `cmd/webhook-manager/**`, `pkg/webhooks/**` | Admission webhooks. |
| `vc-agent` | service | - | `cmd/agent/**`, `pkg/agent/**` | Node agent for colocation and QoS. |
| `vc-shared` | service | `shared` | `pkg/util/**`, `pkg/kube/**`, `pkg/features/**`, `pkg/config/**`, `pkg/signals/**`, `pkg/version/**`, `pkg/filewatcher/**` | Shared utilities, feature gates and kube helpers. |

## Relationships

Each relationship is a CALM `connects` relationship from the first node to the second.

- `vc-scheduler` uses `vc-framework`
- `vc-scheduler` uses `vc-plugins-other`
- `vc-scheduler` uses `vc-shared`
- `vc-plugins-other` uses `vc-framework`
- `vc-controllers` uses `vc-framework`
- `vc-controllers` uses `vc-shared`
- `vc-admission` uses `vc-controllers`
- `vc-admission` uses `vc-framework`
- `vc-admission` uses `vc-shared`
- `vc-agent` uses `vc-shared`
- `vc-plugin-gang` uses `vc-framework`
- `vc-plugin-gang` uses `vc-plugins-other`
- `vc-plugins-other` uses `vc-plugin-gang`
- `vc-plugin-drf` uses `vc-framework`
- `vc-plugin-drf` uses `vc-plugins-other`
- `vc-plugins-other` uses `vc-plugin-drf`
- `vc-plugin-proportion` uses `vc-framework`
- `vc-plugin-proportion` uses `vc-plugins-other`
- `vc-plugins-other` uses `vc-plugin-proportion`
- `vc-plugin-binpack` uses `vc-framework`
- `vc-plugin-binpack` uses `vc-plugins-other`
- `vc-plugins-other` uses `vc-plugin-binpack`
- `vc-plugin-predicates` uses `vc-framework`
- `vc-plugin-predicates` uses `vc-plugins-other`
- `vc-plugins-other` uses `vc-plugin-predicates`
- `vc-plugin-capacity` uses `vc-framework`
- `vc-plugin-capacity` uses `vc-plugins-other`
- `vc-plugins-other` uses `vc-plugin-capacity`

## Rules

- `VC-IND-001` "Volcano scheduler plugins are independent": the six plugin nodes (`vc-plugin-gang`, `vc-plugin-drf`, `vc-plugin-proportion`, `vc-plugin-binpack`, `vc-plugin-predicates`, `vc-plugin-capacity`) must not depend on each other. An import or a call from one of them to another is a finding. Imports and calls both count.
- `VC-ALW-001` "Volcano plugins use only the framework, shared code and plugin utilities": the same six plugin nodes may depend only on `vc-framework`, `vc-shared` and `vc-plugins-other`. A dependency from one of them on any other node is a finding. Imports and calls both count.

## Acceptance criteria

- The CALM model and the bindings are valid.
- Every node is bound.
- Existing violations in the code are recorded in the baseline at `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
