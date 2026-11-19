You are a developer on the `argo-cd` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Trace the deployment of Argo CD

Concordia is already adopted on Argo CD (model, bindings, pack `argo-cd-adopt` and baseline are committed on `main`). The platform team wants Concordia to trace how the Argo CD components are deployed, from the install manifests that the repository ships. Extend the workspace; keep what exists.

Relevant documentation: `/opt/concordia/docs/reference/infrastructure-rules.md`, `workspace.md`, `bindings.md`, `architecture.md`, `policies.md`, `results.md`, `diagnostics.md`.

## Environments

Declare three environments in `concordia.yaml` (also add the files to the project `include` of the observation, next to the Go globs):

| Environment id | Sources | Meaning |
|---|---|---|
| `non-ha` | `manifests/namespace-install.yaml` | The single-instance install. |
| `ha` | `manifests/ha/namespace-install.yaml` | The high-availability install. |
| `non-ha-hydrated` | `bench-env/non-ha-hydrated/**` | A copy of the single-instance install with explicit namespaces (see below). |

The generated install manifests leave the namespace of their resources to `kubectl apply -n argocd`: no workload has a `metadata.namespace`. Concordia derives a placement only from an explicit namespace, so for these two environments the trace stays honest and says `unknown`. Do not hide that: do not add a namespace to the upstream files and do not weaken a rule to make it pass.

Create `bench-env/non-ha-hydrated/install.yaml` from `manifests/namespace-install.yaml`: every namespaced resource without a namespace gets `metadata.namespace: argocd`, and every ServiceAccount subject of a RoleBinding without a namespace gets `namespace: argocd`. Existing namespaces are kept. Commit the file.

## Nodes

Add these nodes to `architecture/argo-cd.calm.json` (title-case name, description, tags in `concordia.tags`):

| Node id | Type | Tags | Role |
|---|---|---|---|
| `argo-dex` | service | `external`, `deployable` | Dex OpenID Connect identity provider. |
| `argo-redis` | database | `restricted` | Redis cache of Argo CD. |
| `argo-ns` | system | - | The `argocd` namespace. |
| `argo-default-ns` | system | - | The `default` namespace. |

Add the tag `deployable` to `argo-server`, `argo-repo-server`, `argo-app-controller`, `argo-appset-controller` and `argo-notifications`, and the tag `public` to `argo-server`. Add one `deployed-in` relationship with the container `argo-ns` and the nodes `argo-server`, `argo-repo-server`, `argo-app-controller`, `argo-appset-controller`, `argo-notifications`, `argo-dex` and `argo-redis`.

## Bindings

Add to `bindings/concordia.bindings.yaml`. Workload bindings name the environment, the resource kind and the name, and no namespace. The deployed-resource bindings are only matched while `check` runs, so mark all bindings below as not required.

| Node | Workloads (in `non-ha` and `non-ha-hydrated`) | Workloads (in `ha`) |
|---|---|---|
| `argo-server` | Deployment `argocd-server` | the same |
| `argo-repo-server` | Deployment `argocd-repo-server` | the same |
| `argo-app-controller` | StatefulSet `argocd-application-controller` | the same |
| `argo-appset-controller` | Deployment `argocd-applicationset-controller` | the same |
| `argo-notifications` | Deployment `argocd-notifications-controller` | the same |
| `argo-dex` | Deployment `argocd-dex-server` | the same |
| `argo-redis` | Deployment `argocd-redis` | StatefulSet `argocd-redis-ha-server` and Deployment `argocd-redis-ha-haproxy` |

- `argo-ns` is the infrastructure resource `core/Namespace/_/argocd` and `argo-default-ns` is `core/Namespace/_/default`, in each of the three environments.
- `argo-dex` is also bound to the image repository `ghcr.io/dexidp/dex`, and `argo-redis` to the image repository `public.ecr.aws/docker/library/redis`.

## Policies

- Enable the built-in pack `builtin:infrastructure`.
- Set the enforcement of the families `deployment-placement` and `deployment-trace` to `advisory` in `check.enforcement`.
- Add the pack `argo-cd-deploy-trace` (version `1.0.0`) at `policies/argo-cd-deploy-trace.policy.yaml` with the rule `ARGO-TRC-101` "The HA install runs a Redis workload from a resolved image" (severity `error`, owner `team-argo-cd`, family `deployment-trace`, a one-sentence rationale and a remediation): in the environment `ha` only, the node `argo-redis` needs at least one workload that uses an image. One workload is enough. A fact must resolve exactly, an absence only counts when the evidence is complete, and a present fact passes even when the coverage is partial.

## Acceptance criteria

- `concordia check` exits 0 on the committed HEAD of `main`: the typed rules are advisory.
- `concordia check --environment non-ha-hydrated` places every deployable node in `argo-ns`; `--environment non-ha` and `--environment ha` give `unknown` for the placement and the workload rules, never `pass`.
- `concordia trace argo-server --format json` shows the trace of each environment.
- Everything is committed on `main`.
