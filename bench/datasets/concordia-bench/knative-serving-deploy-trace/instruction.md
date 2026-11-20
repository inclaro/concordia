You are a developer on the `knative-serving` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Trace the Serving control plane to its deployments

Concordia is already adopted on this repository (code model, bindings and the pack `knative-serving-adopt`). Platform operations want to know that every control-plane component is deployed where the architecture says, and that the edge never hosts one. Extend the workspace so that Concordia can trace the components to their Kubernetes manifests.

Relevant documentation: `/opt/concordia/docs/reference/infrastructure-rules.md`, `workspace.md`, `bindings.md`, `architecture.md`, `policies.md`, `/opt/concordia/docs/how-to/trace-a-deployment.md`.

## Environments

- `production`: the manifests under `config/core/` (all of them).
- `edge`: the manifests under `bench-env/edge/`. The edge environment is not used by the control plane today; it holds only a settings ConfigMap.

Add `bench-env/**` and `config/core/**` to the files that the observation project includes. Create the file `bench-env/edge/gateway-settings.yaml` with a ConfigMap named `edge-gateway-settings` in the namespace `edge-system` (data key `mode`, value `gateway-only`).

## Model changes

- Tag `kn-activator`, `kn-autoscaler`, `kn-controller` and `kn-webhook` as `deployable` (keep their other tags).
- Add the system node `kn-ns` ("the knative-serving namespace that hosts the control plane") and the system node `kn-testing-ns` ("the knative-testing namespace, which hosts no control-plane component").
- Add one relationship with the id `control-plane-in-kn-ns`: the four deployable components are deployed in `kn-ns`.

## Bindings

- Bind the Deployments `activator`, `autoscaler`, `controller` and `webhook` to the node of the same name (`kn-activator`, ...) in both environments. Do not fix a namespace in these bindings.
- Bind the namespace `knative-serving` to `kn-ns` in both environments.
- Bind the namespace `knative-testing` to `kn-testing-ns` in `production`.

## Rules

- Turn on the built-in infrastructure pack.
- Add a pack `knative-serving-deploy-trace` (version `1.0.0`) at `policies/knative-serving-deploy-trace.policy.yaml` with this rule, severity `error`, owner `team-knative-serving`, family `deployment-placement`, a one-sentence rationale and a remediation: `KN-PLC-003` "The Serving control plane is never placed on edge clusters". No component that is tagged `deployable` may have a workload with a placement in the `edge` environment (the rule applies to that environment only). The reason is configuration drift.

## Acceptance criteria

- `concordia check` exits 0 on the committed HEAD of `main`.
- `concordia trace kn-activator` works for both environments.
- Everything is committed on `main`.
