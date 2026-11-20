You are a developer on the `Strimzi` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Strimzi: trace the operator deployment

Strimzi ships its operators as plain Kubernetes install manifests in `install/` (the cluster, topic, user and access operators). The platform team wants Concordia to trace each operator from the architecture to its workload, namespace and image. The model, bindings and the pack `strimzi-adopt` from the adoption are in place; keep them.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md`, `bindings.md`, `architecture.md`, `infrastructure-rules.md`, `capability-profiles.md` and the how-to `/opt/concordia/docs/how-to/trace-a-deployment.md`.

## What to add

1. **Two environments** in `concordia.yaml`:
   - `install`: the manifests under `install/cluster-operator/`, `install/topic-operator/`, `install/user-operator/` and `install/access-operator/`.
   - `install-hydrated`: a committed copy of the same manifests with explicit namespaces (see below), found under `deploy/hydrated/`.
   The observation project must also index `install/**` and `deploy/hydrated/**`.
2. **Hydrated copy.** The upstream operator manifests leave `metadata.namespace` unset, so a manifest alone says nothing about where a workload runs. Create `deploy/hydrated/strimzi.yaml`: all documents of the four operator install directories (leave out the CustomResourceDefinition files), written as one multi-document YAML file, where every namespaced resource without a namespace gets the namespace `strimzi`, and every ServiceAccount subject of a RoleBinding or ClusterRoleBinding that has no namespace gets `strimzi` too. Resources that already name a namespace keep it. The directory `deploy/` is skipped by the code indexer by default: add a `.cbmignore` at the repository root that re-includes it.
3. **Model.** Extend `architecture/strimzi.calm.json`:
   - tag `stz-cluster-operator`, `stz-topic-operator` and `stz-user-operator` as `deployable`;
   - a new service `stz-access-operator` (name "Access Operator", description "Operator that manages Kafka access (deployed from its own repository image).", tag `deployable`);
   - two new `system` nodes: `stz-strimzi-ns` ("Strimzi Namespace", the namespace that hosts the Kafka operators) and `stz-access-ns` ("Access Operator Namespace", the namespace that hosts the access operator);
   - a `deployed-in` relationship `stz-operators-in-strimzi-ns` that deploys the cluster, topic and user operators in `stz-strimzi-ns`, and a `deployed-in` relationship `stz-access-in-access-ns` that deploys `stz-access-operator` in `stz-access-ns`.
4. **Bindings.** Keep the code bindings of each node and add to the same binding (for the nodes that have source roots) or in a new binding (`stz-access-operator`, the namespaces):
   - the Deployment of each operator, in both environments: `strimzi-cluster-operator`, `strimzi-topic-operator`, `strimzi-user-operator` and `strimzi-access-operator`. Do not set a namespace in the workload binding;
   - the image repository `quay.io/strimzi/operator` for `stz-cluster-operator` only (the topic and user operators use the same image repository, but the image belongs to one node), and the image repository `quay.io/strimzi/access-operator` for `stz-access-operator`;
   - the namespace `strimzi` for `stz-strimzi-ns` in `install-hydrated` and the namespace `strimzi-access-operator` for `stz-access-ns` in both environments (as Concordia resources of kind Namespace). Bindings that only name deployed resources must not be required to match source code.
5. **Policies.** Turn on the built-in infrastructure pack and set the enforcement of the families `deployment-placement` and `deployment-trace` to `advisory` in `check.enforcement`.

## Acceptance criteria

- `concordia model validate`, `concordia bind validate` and `concordia check` succeed (exit 0); the workspace is committed on `main`.
- `concordia trace stz-access-operator` and `concordia trace stz-cluster-operator` work for both environments.
- Nothing is baselined for the new rules; typed rules are advisory and do not change the exit code. Where the manifests cannot prove a placement (workloads without a namespace) Concordia must report an honest `unknown`, never a pass.
