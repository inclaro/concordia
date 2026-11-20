You are a developer on the `fzf` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Keep the fzf architecture diagrams current

Concordia is already adopted on `main`. The architecture team wants two diagrams of fzf that Concordia keeps honest: one that Concordia generates from the model, and one that the team draws by hand and Concordia compares with the model and with the code.

Relevant documentation: `/opt/concordia/docs/how-to/keep-diagrams-current.md`, `render-diagrams.md`, and `/opt/concordia/docs/reference/workspace.md`, `architecture.md`, `results.md`, `cli.md`. The Structurizr CLI that Concordia needs is installed on the `PATH`.

## Model

- Add a CALM node `fzf` of type `system` (name `Fzf`) that is composed of the six existing modules, through a relationship with the id `fzf-composed-of`. The system node needs no binding.

## Observation

- Diagram evidence of a hand-drawn Markdown diagram is only available in the `full` index mode. Switch the workspace to it, and let the project also include `docs/architecture/**`.

## Views

Both views belong to the owner `team-fzf`, have the node `fzf` as their scope, and are container views.

- `fzf-container`: generated. Concordia writes the Structurizr source to `docs/architecture/fzf-container.dsl`, and the two exports `docs/architecture/fzf-container.mmd` (Mermaid) and `docs/architecture/fzf-container.json` (JSON).
- `fzf-components`: authored. The diagram is the Mermaid block with the title `fzf components` in `docs/architecture/components.md`. Draw exactly the seven declared relationships between the six modules, using the short names `cli`, `core`, `tui`, `algo`, `protector` and `util` as diagram elements, and map each of them to its node.

- Both views are compared with the declared model only, not with the observed code: the code index of fzf has low-confidence call edges that the model does not declare, and they must not count as drift of the diagrams.

## Acceptance criteria

- The model and the views are valid.
- Render the generated view and commit all rendered files (source, exports and manifest) together with the diagram file.
- `concordia check` exits 0 on the committed HEAD of `main`: presence, freshness and correspondence of both views pass.
- Everything is committed on `main`.
