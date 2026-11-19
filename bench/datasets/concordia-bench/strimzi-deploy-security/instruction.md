You are a developer on the `Strimzi` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Strimzi: security and availability obligations for the operator deployment

The deployment trace of Strimzi (environments `install` and `install-hydrated`, pack `builtin:infrastructure`) is in place. The security team now wants three obligations about the operator manifests, in a new pack `strimzi-deploy-security` (version `1.0.0`) at `policies/strimzi-deploy-security.policy.yaml`. Register the file in `concordia.yaml` next to `strimzi-adopt` and set the enforcement of the family `deployment-security` to `advisory` in `check.enforcement` (keep the entries for the two infrastructure families). Every rule has severity `error`, owner `team-strimzi`, family `deployment-security`, a one-sentence rationale and a remediation.

Relevant documentation: `/opt/concordia/docs/reference/policies.md` (typed rules), `infrastructure-rules.md`, `bindings.md`, `capability-profiles.md`.

## New model elements and bindings

Add these nodes to `architecture/strimzi.calm.json` (keep everything else):

| Node id | Type | Name | Description | Bound to |
|---|---|---|---|---|
| `stz-co-config` | data-asset | Cluster Operator Configuration | Configuration map of the cluster operator. | the ConfigMap `strimzi-cluster-operator` in namespace `strimzi`, environment `install-hydrated` |
| `stz-cluster-admin` | system | Cluster Admin Role | The built-in cluster-admin cluster role. | the ClusterRole `cluster-admin`, both environments |
| `stz-cluster-bindings` | system | Operator Cluster Bindings | The cluster role bindings of the operators. | the ClusterRoleBindings `strimzi-cluster-operator`, `strimzi-cluster-operator-kafka-broker-delegation`, `strimzi-cluster-operator-kafka-client-delegation` and `strimzi-access-operator`, both environments |
| `stz-operator-roles` | system | Operator Cluster Roles | The cluster roles that the Strimzi install manifests define. | the ClusterRoles `strimzi-cluster-operator-namespaced`, `strimzi-cluster-operator-global`, `strimzi-cluster-operator-leader-election`, `strimzi-cluster-operator-watched`, `strimzi-kafka-broker`, `strimzi-entity-operator`, `strimzi-kafka-client` and `strimzi-access-operator`, both environments |

All of these are deployed resources of kind infrastructure resource (cluster-scoped kinds use `_` as namespace in their address; namespaced ones use the namespace). Bindings that only name deployed resources must not be required to match source code.

## Rules

- `STZ-SEC-001` "The cluster operator mounts its configuration map": the workload of `stz-cluster-operator` must reference the configuration `stz-co-config`, in the environment `install-hydrated` only (the namespace of the configuration map is only explicit there).
- `STZ-SEC-002` "No operator binding grants cluster-admin": no cluster role binding of `stz-cluster-bindings` may refer to the role `stz-cluster-admin`, in every environment.
- `STZ-SEC-003` "Every operator runs under its own service account": the workloads of `stz-cluster-operator`, `stz-topic-operator`, `stz-user-operator` and `stz-access-operator` must each use a runtime identity (a service account), in every environment.

## Acceptance criteria

- `concordia model validate`, `concordia bind validate` and `concordia check` succeed (exit 0); everything is committed on `main`.
- Where the manifests of an environment cannot prove the answer (the workloads without a namespace in `install`) Concordia reports `unknown`, never a pass. Do not baseline or waive these.
