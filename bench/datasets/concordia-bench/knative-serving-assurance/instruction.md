You are a developer on the `knative-serving` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Evaluate effective access from rendered manifests

Concordia is already adopted on this repository (code model, bindings and the pack `knative-serving-adopt`). Security wants to know what the Serving manifests really grant, not only which resources are named in them. Add Concordia's assurance evidence for the control-plane manifests and three rules that ask the evaluators.

Relevant documentation: `/opt/concordia/docs/reference/assurance.md`, `workspace.md` (`environments`, `assurance`), `policies.md` (assurance rules), `bindings.md`, `assurance-rules.md`, `/opt/concordia/docs/how-to/add-stronger-evidence.md`.

## Environments and sources

- Add `config/core/**` and `bench-env/**` to the files that the observation project includes.
- `production`: the manifests under `config/core/` (all of them).
- `production-hydrated`: the manifests under `bench-env/production-hydrated/`. The `ko://` image references of the upstream Deployments are not valid image references, so the render of `production` is partial and Concordia says so. This second environment holds copies that can be rendered completely. Create it: copy these files from `config/core/` into `bench-env/production-hydrated/` with the same relative paths: `200-serviceaccount.yaml`, `200-roles/activator.yaml`, `200-roles/clusterrole.yaml`, `200-roles/clusterrole-namespaced.yaml`, `200-roles/addressable-resolvers-clusterrole.yaml`, `200-roles/podspecable-bindings-clusterrole.yaml`, `deployments/activator.yaml`, `deployments/autoscaler.yaml`, `deployments/controller.yaml`, `deployments/webhook.yaml`, `webhooks/secret.yaml`. In the four Deployment copies, replace the image `ko://knative.dev/serving/cmd/<name>` with `gcr.io/knative-releases/knative.dev/serving/cmd/<name>:v1.24.0`. The activator team plans to narrow its Role, and the hydrated copy shows that planned state: in `bench-env/production-hydrated/200-roles/activator.yaml`, remove `secrets` from the first rule of the Role `knative-serving-activator` (its second rule, which names two certificate secrets, stays). Change nothing else.
- `staging-hydrated`: a staging cluster runs the same manifests (the sources are the same directory, `bench-env/production-hydrated/`), but nobody can vouch that its RBAC authorizer is enabled.

## Assurance

- Render all three environments from plain manifests (nothing runs). For `production`, render `config/core/deployments`, `config/core/200-roles`, `config/core/200-serviceaccount.yaml` and `config/core/webhooks/secret.yaml` (each its own source, none contains symbolic links); for `production-hydrated` and `staging-hydrated`, render `bench-env/production-hydrated`.
- Run the RBAC evaluator in all three environments (for `staging-hydrated` render the same directory). Declare that the authorizer is enabled in `production` and `production-hydrated`; in `staging-hydrated` do not declare it. Declare no network evaluator.
- Turn on the built-in assurance pack.

## Model changes

- Add the data-asset node `kn-webhook-certs` ("the serving certificates secret of the webhook"). Bind the Secret `webhook-certs` of the namespace `knative-serving` to it in all three environments; the binding names no source file, so mark it as not required.
- Bind the Deployments `activator`, `autoscaler`, `controller` and `webhook` to the node of the same name (`kn-activator`, ...) in all three environments, in addition to their source roots. Do not fix a namespace in these bindings.

## Rules

Add a pack `knative-serving-assurance` (version `1.0.0`) at `policies/knative-serving-assurance.policy.yaml`. Every rule has severity `error`, owner `team-knative-serving`, family `effective-access`, a one-sentence rationale and a remediation.

- `KN-AUTH-001` "The activator cannot read the webhook certificates": the service account of `kn-activator` must not be granted any access to the Secret that `kn-webhook-certs` is bound to. The reason is least privilege.
- `KN-AUTH-002` "The controller can manage the webhook certificates": the service account of `kn-controller` must be granted access to that Secret. The reason is operability.
- `KN-NET-001` "The activator has no network path to the autoscaler on the metrics port": traffic from `kn-activator` to `kn-autoscaler` on port 8080 must be denied. The reason is zero-trust networking. The workspace has no network evaluator, and the team accepts that this rule reports `unknown` for that reason until one is added.

The team expects `KN-AUTH-001` to hold in `production-hydrated`, and to report `unknown` in `staging-hydrated`, because the authorizer is not declared there: a claim that nothing is granted cannot be made without it.

## Acceptance criteria

- `concordia assurance validate` exits 0.
- `concordia check` exits 0 on the committed HEAD of `main` (the assurance rules are advisory: their findings never change the exit code).
- Everything is committed on `main`.
