You are a developer on the `knative-serving` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Check the security and availability intent of the Serving deployment

Concordia already traces the Serving control plane to its Kubernetes manifests on `main` (environments `production` and `edge`, the infrastructure pack and the pack `knative-serving-deploy-trace`). The security and operations teams now want four deployment rules enforced from the manifests, and a safe place to prove that they work.

Relevant documentation: `/opt/concordia/docs/reference/policies.md` (typed rules), `infrastructure-rules.md`, `bindings.md`, `workspace.md`, `results.md`, `/opt/concordia/docs/how-to/resolve-unknown-verdicts.md`.

## Why a hydrated copy

The manifests under `config/core/` include symbolic links and unrelated manifests elsewhere in the repository, so Concordia cannot always give a definite answer for the `production` environment: it answers `unknown` and says why. Add a third environment `production-hydrated` that holds plain copies of the manifests that the rules need, so that each rule can be proven on controlled input:

- Directory: `bench-env/production-hydrated/`. Copy these files from `config/core/` into it with the same relative paths, resolving symbolic links (`cp -L`): `100-namespace.yaml`, `200-serviceaccount.yaml`, `200-roles/activator.yaml`, `200-roles/clusterrole.yaml`, `200-roles/clusterrole-namespaced.yaml`, `200-roles/addressable-resolvers-clusterrole.yaml`, `200-roles/podspecable-bindings-clusterrole.yaml`, `deployments/activator.yaml`, `deployments/activator-hpa.yaml`, `deployments/autoscaler.yaml`, `deployments/controller.yaml`, `deployments/webhook.yaml`, `deployments/webhook-hpa.yaml`, `webhooks/secret.yaml`.
- Environment `production-hydrated`: its sources are everything under that directory. In this environment the four Deployments and the namespace `knative-serving` bind exactly like in `production` (bind the Deployments without a namespace).

## Model changes

- Add the system node `kn-sa-controller` ("the shared controller service account of the Serving control plane"). Bind the ServiceAccount `controller` of the namespace `knative-serving` to it in `production` and `production-hydrated`.
- Add the data-asset node `kn-webhook-certs` ("the serving certificates secret of the webhook"). Bind the Secret `webhook-certs` of the namespace `knative-serving` to it in both environments.
- These bindings only name deployed resources, so no source file matches them: mark them as not required.

## Rules

Add a pack `knative-serving-deploy-security` (version `1.0.0`) at `policies/knative-serving-deploy-security.policy.yaml`. Every rule has severity `error`, owner `team-knative-serving`, a one-sentence rationale and a remediation. Each rule applies only to the environments `production` and `production-hydrated`.

Family `deployment-security`:

- `KN-SEC-001` "The autoscaler and the webhook do not share the controller identity": the workloads of `kn-autoscaler` and `kn-webhook` must not run as `kn-sa-controller`. The reason is excessive privilege.
- `KN-SEC-002` "Only the webhook mounts or reads its serving certificates": the workloads of `kn-activator`, `kn-autoscaler` and `kn-controller` must not reference `kn-webhook-certs`. The reason is secret sprawl.

Family `deployment-reliability`:

- `KN-REL-001` "Every control-plane Deployment has a disruption budget": each workload of `kn-activator`, `kn-autoscaler`, `kn-controller` and `kn-webhook` must be the target of a disruption policy. The reason is single points of failure.
- `KN-REL-002` "The activator and the webhook scale horizontally": it is enough that one workload of each of `kn-activator` and `kn-webhook` is the target of an autoscaler. The reason is reliability.

Leave the rules at Concordia's default evidence requirements; do not relax how much evidence a definite answer needs. The team expects this for `KN-REL-001` on `production-hydrated`: a workload that has a disruption policy passes (no finding), and a workload without one is reported as `unknown` under those defaults, not as a violation.

## Acceptance criteria

- `concordia check` exits 0 on the committed HEAD of `main` (these rule families are advisory: their findings never change the exit code).
- Everything is committed on `main`.
