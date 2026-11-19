You are a developer on the `airflow` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Trace the Airflow deployables through the Helm chart

Concordia is already adopted on this repository. Airflow ships its deployment as a Helm chart in `chart/`. The platform team wants the deployable components traced to their workloads, images and namespace. The chart is a template, not a plain manifest, so the team accepts that the trace cannot be completed from it: leave the chart as it is and record what Concordia can and cannot say. Do not hand-write rendered manifests for this task.

## Model changes

- Tag the nodes `af-api-server`, `af-scheduler` and `af-dag-processing` with `deployable` (keep their other tags).
- Add the service-level container node `af-namespace` (node type `system`, name `Airflow Namespace`, description "The Kubernetes namespace that hosts the Airflow control plane."), and one `deployed-in` relationship that places `af-api-server`, `af-scheduler` and `af-dag-processing` in `af-namespace`.

## Environment and bindings

- Declare the environment `chart` with the sources `chart/templates/**`. Add `chart/templates/**` to the files of the observation project.
- Bind in environment `chart`: `af-api-server` to the Deployment `airflow-api-server`, `af-scheduler` to the Deployment `airflow-scheduler`, `af-dag-processing` to the Deployment `airflow-dag-processor`, and `af-namespace` to the namespace `airflow` (the namespace resource of the environment). Keep the existing source-root bindings of the nodes.

## Policy

- Enable the built-in infrastructure pack in the workspace.
- Deployment rules are advisory in this release: set the enforcement of the families `deployment-placement` and `deployment-trace` to `advisory` in `check.enforcement`.

## Acceptance criteria

- The model and the bindings are valid and committed on `main`.
- `concordia check` exits 0 on the committed HEAD of `main`. The deployment results are reported honestly: where the chart templates give no evidence they are `unknown`, never `pass`.
- `concordia trace af-api-server --format json` runs.

Relevant documentation: `/opt/concordia/docs/reference/infrastructure-rules.md`, `workspace.md`, `bindings.md`, `results.md`, `/opt/concordia/docs/how-to/trace-a-deployment.md`.
