You are a developer on the `knative-serving` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Keep the Serving architecture diagrams current

Concordia is already adopted on this repository (code model, bindings and the pack `knative-serving-adopt`). Architecture wants two diagrams of Serving that are generated from the model and that fail the build when they go stale.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md` (`views`, `environments`), `architecture.md`, `results.md`, `/opt/concordia/docs/how-to/keep-diagrams-current.md`, `/opt/concordia/docs/how-to/render-diagrams.md`.

## Model changes

- Add `config/core/**` to the files that the observation project includes, and declare the environment `production` with the sources `config/core/**`.
- Add the system node `kn-ns` ("the knative-serving namespace that hosts the control plane") and the system node `kn-serving` ("Knative Serving").
- Add a relationship with the id `control-plane-in-kn-ns`: `kn-activator`, `kn-autoscaler`, `kn-controller` and `kn-webhook` are deployed in `kn-ns`.
- Add a relationship with the id `kn-serving-composed-of`: `kn-serving` is composed of all eight code nodes (`kn-activator`, `kn-autoscaler`, `kn-controller`, `kn-webhook`, `kn-queue`, `kn-apis`, `kn-client`, `kn-shared`).
- Bind the Deployments `activator`, `autoscaler`, `controller` and `webhook` to the node of the same name in `production`, in addition to their source roots; do not fix a namespace. Bind the namespace `knative-serving` to `kn-ns` in `production`; this binding names no source file, so mark it as not required.

## Views

Both views are generated, owned by `team-knative-serving`, and are compared with the declared model only (the observed code graph has dependencies that the model deliberately does not declare).

- `kn-deployment`: a deployment view of the environment `production` that shows the four deployable components and `kn-ns`. The Structurizr DSL is committed at `docs/architecture/kn-deployment.dsl`; also export it as PlantUML to `docs/architecture/kn-deployment.puml` and as Graphviz DOT to `docs/architecture/kn-deployment.dot`.
- `kn-container`: a container view of `kn-serving`. The Structurizr DSL is committed at `docs/architecture/kn-container.dsl`; also export it as JSON to `docs/architecture/kn-container.json`.

Render the views with the Structurizr CLI that is installed in the image, and commit the DSL files, the exports and the manifest files that the render writes beside them.

## Acceptance criteria

- `concordia render --views` exits 0, and `concordia check` exits 0 on the committed HEAD of `main`, with the view rules passing.
- Everything is committed on `main`.
