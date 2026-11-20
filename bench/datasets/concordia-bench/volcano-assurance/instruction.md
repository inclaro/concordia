You are a developer on the `volcano` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Render the Volcano Helm chart and evaluate RBAC

The security team wants evidence about what Volcano's Helm chart really grants, not only what the manifests in `installer/` say. Concordia can render Helm charts itself and evaluate Kubernetes RBAC on the result.

Relevant documentation: `/opt/concordia/docs/reference/assurance.md`, `assurance-rules.md`, `workspace.md`, `policies.md`, `bindings.md`, `results.md`, and `/opt/concordia/docs/how-to/add-stronger-evidence.md`.

## Environments and rendering

Declare one environment, `dev`: the manifests `installer/volcano-development.yaml` and `installer/volcano-agent-development.yaml` (the observation project must include both).

Render the Helm chart for `dev` (release `volcano`, namespace `volcano-system`, values file `bench-values/dev.yaml`, which holds an empty mapping `{}`) from the directory `bench-env/chart/volcano`. The upstream chart `installer/helm/chart/volcano` links its CRD directories with symbolic links, which Concordia refuses to render, so make `bench-env/chart/volcano` as a copy of the chart with every link resolved (for example with `cp -rL`) and commit it.

Pin the renderer in the lock file (`concordia lock update`). Declare an RBAC evaluator for the environment `dev` that assumes the RBAC authorizer is enabled.

## Architecture and bindings

Add the data-asset node `vc-scheduler-config` ("Volcano Scheduler Configuration"): the ConfigMap `volcano-scheduler-configmap` in `volcano-system`. In the new file `bindings/volcano.assurance.bindings.yaml` (listed in the workspace), bind `vc-scheduler` to the Deployment `volcano-scheduler` and `vc-admission` to the Deployment `volcano-admission` in `dev`, and bind `vc-scheduler-config` in `dev` to the ConfigMap `volcano-scheduler-configmap` in namespace `volcano-system`. The rendered resources are matched by the binding kind that names a workload and its resource kind (the evaluator reads only these bindings; the `ConfigMap` is a resource kind of such a binding too). Mark these deployed-resource bindings as not required (`required: false`): `concordia bind validate` checks the code graph only.

## Policy

Enable the built-in assurance pack. Add the pack `volcano-assurance` (version `1.0.0`) at `policies/volcano-assurance.policy.yaml` with two rules, each with severity `error`, owner `team-volcano`, a one-sentence rationale and a remediation:

- `VC-AUTH-001` "Admission can read the scheduler configuration": the service account of `vc-admission` must be granted access to `vc-scheduler-config`.
- `VC-AUTH-002` "The scheduler has no API access to its configuration": the service account of `vc-scheduler` must not be granted access to `vc-scheduler-config` (the scheduler reads its configuration from a mounted file).

## Acceptance criteria

- `concordia assurance validate` exits 0.
- `concordia check` exits 0 on the committed HEAD of `main` (assurance rules are advisory). The result for `VC-AUTH-002` is honest drift, not an error in your work: the real chart does give the scheduler access to its configmap.
- Everything is committed on `main`.
