You are a developer on the `argo-cd` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Security obligations for the Argo CD deployment

The deployment trace of Argo CD is already in place (environments `non-ha`, `ha` and `non-ha-hydrated`, the deployable nodes, the workload bindings, the pack `builtin:infrastructure` and the pack `argo-cd-deploy-trace`). The security team now wants three obligations on how the workloads use secrets, network policies and service accounts. Add them as a new pack; keep what exists.

Relevant documentation: `/opt/concordia/docs/reference/policies.md` (typed rules, `evidence`, precedence), `infrastructure-rules.md`, `bindings.md`, `results.md`, `workspace.md` (`check.enforcement`).

## Nodes and bindings

Add these nodes to `architecture/argo-cd.calm.json` (title-case name, description):

| Node id | Type | Role |
|---|---|---|
| `argo-redis-secret` | data-asset | The Redis password secret. |
| `argo-default-sa` | system | The default service account of the `argocd` namespace. |
| `argo-service-accounts` | system | The dedicated service accounts of the Argo CD workloads. |

Add these infrastructure-resource bindings (all not required: they are matched while `check` runs). The namespace part of the address is `_` in the environments `non-ha` and `ha` (the namespace of those manifests is unresolved) and `argocd` in `non-ha-hydrated`:

- `argo-redis-secret`: `core/Secret/<ns>/argocd-redis`, in each of the three environments.
- `argo-default-sa`: `core/ServiceAccount/<ns>/default`, in each of the three environments.
- `argo-service-accounts`: `core/ServiceAccount/<ns>/<name>` for the service accounts `argocd-application-controller`, `argocd-applicationset-controller`, `argocd-dex-server`, `argocd-notifications-controller`, `argocd-redis`, `argocd-repo-server` and `argocd-server` in `non-ha` and `non-ha-hydrated`, and for the same list without `argocd-redis` but with `argocd-redis-ha` and `argocd-redis-ha-haproxy` in `ha`.

## Policy pack

Put the rules in the pack `argo-cd-deploy-security` (version `1.0.0`) at `policies/argo-cd-deploy-security.policy.yaml`. Each rule has severity `error`, owner `team-argo-cd`, the family `deployment-security`, a one-sentence rationale and a remediation. Add an enforcement entry that runs the family `deployment-security` as `advisory` in `check.enforcement`.

- `ARGO-SEC-001` "Only the cache clients read the Redis password secret": the workloads of `argo-dex`, `argo-notifications` and `argo-appset-controller` must not reference `argo-redis-secret`. With no reference found the rule passes, also when the extraction of secret references is only partial.
- `ARGO-NET-101` "Every Argo CD workload is selected by a network policy": each node with the tag `deployable` needs a network policy that selects its workload. One parsed policy that selects the workload is proof enough, and an absence only counts when the evidence is complete.
- `ARGO-SEC-002` "No Argo CD workload runs as the default service account": the workloads of the nodes with the tag `deployable` must not use the service account of `argo-default-sa`. With no use found the rule passes, also when the extraction is only partial.

All three rules need evidence that resolves exactly.

## Acceptance criteria

- `concordia check` exits 0 on the committed HEAD of `main`.
- In the environment `non-ha-hydrated` the three rules pass.
- Everything is committed on `main`.
