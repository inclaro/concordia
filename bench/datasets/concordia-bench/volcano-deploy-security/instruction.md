You are a developer on the `volcano` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Add security and supply-chain obligations to the Volcano deployment

The deployment of Volcano is already traced in two environments (`dev` and `monitoring`, with the infrastructure pack on). The security team now wants three typed deployment obligations, and a controlled copy of the dev manifests to check them against.

Relevant documentation: `/opt/concordia/docs/reference/policies.md` (typed rules), `infrastructure-rules.md`, `bindings.md`, `workspace.md`, `results.md`, and `/opt/concordia/docs/how-to/trace-a-deployment.md`.

## A controlled copy of the dev manifests

Add a third environment `dev-hydrated` whose manifests are everything under `bench-env/dev-hydrated/` (the environment source is the glob `bench-env/dev-hydrated/**`; the reference copy is the file `bench-env/dev-hydrated/volcano.yaml`, and the platform team adds further manifest files to that folder). Build that file from the documents of `installer/volcano-development.yaml`, leaving out every `CustomResourceDefinition` document (do not include the agent manifest) and keeping every other workload, role and webhook configuration as the installer declares it (do not include the agent manifest). The admission deployment mounts the Secret `volcano-admission-secret`, which no installer manifest declares because the admission init job creates it at runtime: add an empty `Opaque` Secret manifest named `volcano-admission-secret` in the namespace `volcano-system` to the copy, so that every reference in the copy has a declared target. The observation project must include `bench-env/dev-hydrated/**` (and `installer/volcano-monitoring.yaml`, which stays in `monitoring`). Bind the deployable control plane components and the two namespaces in `dev-hydrated` exactly as in `dev`: the same workloads for each node, and `vc-system-ns` and `vc-monitoring-ns` as the namespaces `volcano-system` and `volcano-monitoring`.

## New architecture nodes

Add these nodes (all other nodes and relationships stay):

- `vc-cluster-rolebindings`, a system node "Volcano Cluster Role Bindings": the cluster role bindings of the Volcano components. Bind it, in `dev`, to `volcano-admission-role`, `volcano-controllers-role`, `volcano-scheduler-role` and `volcano-agent-role`, in `dev-hydrated` to `volcano-admission-role`, `volcano-controllers-role`, `volcano-scheduler-role` and to `volcano-bench-role` (a binding that the platform team reserves and that does not exist yet), and in `monitoring` to `prometheus-volcano` and `kube-state-metrics`.
- The service accounts that those bindings apply to also need nodes of their own: bind the service accounts `volcano-admission`, `volcano-controllers`, `volcano-scheduler` and `volcano-agent` of `volcano-system` (in `dev`; in `dev-hydrated` all but `volcano-agent`) to the nodes `vc-admission`, `vc-controllers`, `vc-scheduler` and `vc-agent`, and the service account `kube-state-metrics` of `volcano-monitoring` (in `monitoring`) to `vc-monitoring`.
- `vc-default-sa`, a system node "Default Service Account": the default service account of a namespace. Bind it to the service account `default` of `volcano-monitoring` in `monitoring` and to the service account `default` of `volcano-system` in `dev-hydrated`.
- `vc-scheduler-config`, a data-asset node "Volcano Scheduler Configuration": the ConfigMap `volcano-scheduler-configmap` in `volcano-system`. Bind it in `dev` and `dev-hydrated`.

Mark every binding of a deployed resource (workload, image, namespace and other infrastructure resources) as not required (`required: false`): `concordia bind validate` checks the code graph only and would report them as matching nothing.

Put the new bindings in `bindings/volcano.security.bindings.yaml` and list that file in the workspace.

## Policy

Add the pack `volcano-deploy-security` (version `1.0.0`) at `policies/volcano-deploy-security.policy.yaml`. Each rule has severity `error`, owner `team-volcano`, family `deployment-security`, a one-sentence rationale and a remediation. Also set the enforcement of the family `deployment-security` to `advisory`.

Leave every rule's evidence requirements at their defaults (set no `evidence` keys on the rules). The installer manifests of `dev` and `monitoring` are only partial evidence (the build link of the images is not known, and the monitoring manifest is a template), so with the defaults Concordia must report these obligations as unknown in `dev` and `monitoring` wherever a pass or a violation cannot be established, and never as a pass there; the controlled copy `dev-hydrated` is the complete evidence, where the rules give a definite answer.

- `VC-SEC-001` "No cluster role binding grants the default service account": no cluster role binding of `vc-cluster-rolebindings` may apply to the default service account `vc-default-sa`. Check it in all three environments.
- `VC-SEC-002` "The scheduler workload mounts its configuration": every workload of `vc-scheduler` must reference the configuration map `vc-scheduler-config`. Check it in `dev` and `dev-hydrated`.
- `VC-IMG-001` "Deployable components run first-party images only": no workload of a component tagged `deployable` may use a container image other than `docker.io/volcanosh/vc-scheduler:v1.16.0`, `docker.io/volcanosh/vc-controller-manager:v1.16.0`, `docker.io/volcanosh/vc-webhook-manager:v1.16.0` or `docker.io/volcanosh/vc-agent:v1.16.0` (compare the image reference with its tag). Check it in all three environments.

## Conflicts with the infrastructure pack

`VC-IMG-001` forbids a relation that the infrastructure rules `INFRA-TRC-002` and `INFRA-TRC-004` require, and Concordia refuses to run contradicting rules without an explicit ranking. Resolve it in the pack `volcano-deploy-security` with a precedence list of version 1 in which `VC-IMG-001` outranks `INFRA-TRC-002`, which outranks `INFRA-TRC-004`.

## Acceptance criteria

- The model, the bindings and the policies are valid; every node is bound.
- `concordia policy verify` lists the three new rules.
- `concordia check` exits 0 on the committed HEAD of `main` (these rules are advisory).
- Everything is committed on `main`.
