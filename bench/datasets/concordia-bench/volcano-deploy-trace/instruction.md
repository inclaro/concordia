You are a developer on the `volcano` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Trace the Volcano deployment

Volcano ships plain Kubernetes manifests in `installer/`. The platform team wants Concordia to follow each Volcano component from its CALM node to its workload, container image and namespace, in two environments, and to say honestly where the chain cannot be followed.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md`, `bindings.md`, `infrastructure-rules.md`, `architecture.md`, `policies.md`, and `/opt/concordia/docs/how-to/trace-a-deployment.md`.

## Environments

Declare two environments in `concordia.yaml`:

- `dev`: the manifests `installer/volcano-development.yaml` and `installer/volcano-agent-development.yaml`, and everything under `bench-env/dev/` (the directory does not exist yet; the platform team will drop controlled manifests there).
- `monitoring`: the manifest `installer/volcano-monitoring.yaml`.

The observation project must also include these manifest files and `bench-env/dev/**`, next to the code directories it already includes.

## Architecture changes

Edit `architecture/volcano.calm.json` (keep every existing node and relationship):

- Tag `vc-scheduler`, `vc-controllers`, `vc-admission` and `vc-agent` with `deployable`.
- Add the system nodes `vc-system-ns` ("Volcano System Namespace", the Kubernetes namespace `volcano-system`) and `vc-monitoring-ns` ("Volcano Monitoring Namespace", the namespace `volcano-monitoring`).
- Add the service `vc-monitoring` ("Volcano Monitoring", the Prometheus server), tagged `deployable`.
- Add a `deployed-in` relationship `vc-control-plane-in-system-ns`: `vc-scheduler`, `vc-controllers`, `vc-admission` and `vc-agent` are deployed in `vc-system-ns`.
- Add a `deployed-in` relationship `vc-monitoring-in-monitoring-ns`: `vc-monitoring` is deployed in `vc-monitoring-ns`.

## Bindings

Add the file `bindings/volcano.deploy.bindings.yaml` (list it in the workspace next to the existing bindings file). Do not bind a namespace on the workload bindings. Bind:

| Node | Workloads | Image repository |
|---|---|---|
| `vc-scheduler` | Deployment `volcano-scheduler` in `dev` | `docker.io/volcanosh/vc-scheduler` |
| `vc-controllers` | Deployment `volcano-controllers` in `dev` | `docker.io/volcanosh/vc-controller-manager` |
| `vc-admission` | Deployment `volcano-admission` and Job `volcano-admission-init`, both in `dev` | `docker.io/volcanosh/vc-webhook-manager` |
| `vc-agent` | DaemonSet `volcano-agent` in `dev` | `docker.io/volcanosh/vc-agent` |
| `vc-monitoring` | Deployment `prometheus-deployment` in `monitoring` | `prom/prometheus` |

Mark every binding of a deployed resource (workload, image, namespace and other infrastructure resources) as not required (`required: false`): `concordia bind validate` checks the code graph only and would report them as matching nothing.

Bind the Kubernetes namespace objects: `vc-system-ns` is the namespace `volcano-system` and `vc-monitoring-ns` is the namespace `volcano-monitoring`, each bound in both environments.

## Policy

Enable the built-in infrastructure pack. Set the enforcement of the two families `deployment-placement` and `deployment-trace` explicitly to `advisory` for every environment.

## Acceptance criteria

- The model and the bindings are valid and every node is bound.
- The infrastructure rules (`INFRA-PLC-001`, `INFRA-PLC-002`, `INFRA-TRC-001` to `INFRA-TRC-004`) are active.
- `concordia trace vc-monitoring --format json` works and shows what Concordia can and cannot follow.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
