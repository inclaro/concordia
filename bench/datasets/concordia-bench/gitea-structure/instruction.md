You are a developer on the `Gitea` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Add the Gitea structure rules

Concordia is already adopted on `main` (workspace `concordia.yaml`, model `architecture/gitea.calm.json`, pack `gitea-adopt`). The architecture team wants three more rules that go beyond layering. Add them as a new policy pack and keep the existing pack.

Relevant reference pages: `/opt/concordia/docs/reference/policies.md`, `workspace.md`, `results.md`, `exceptions.md`.

## Policy pack

- Pack id `gitea-structure`, version `1.0.0`, file `policies/gitea-structure.policy.yaml`. Register it in `concordia.yaml` next to the existing pack.
- Each rule has severity `error` and owner `team-gitea`, and a one-sentence rationale and a remediation.
- All three rules count import and call dependencies unless the text says otherwise.

## Rules

- `GITEA-DNY-001` "Shared modules cannot reach services or routers": code of the nodes `gitea-modules` and `gitea-setting` must not reach `gitea-services` or `gitea-routers` over any chain of up to 3 dependencies, even through intermediate packages.
- `GITEA-API-001` "Configuration is read through the setting facade": code of `gitea-routers` and `gitea-services` may call the node `gitea-setting` only through the functions that are declared in `modules/setting/setting.go` and `modules/setting/config_provider.go`. Only calls count for this rule. Report such violations once for each source file, not once for each call.
- `GITEA-OWN-001` "Only models, services and migrations own the database layer": only `gitea-models`, `gitea-services` and `gitea-modelmigration` may depend on `gitea-db`. Any other node that depends on it is a violation.

## Acceptance criteria

- The three rules are declared in the new pack, with the ids above.
- Violations that already exist in the code are recorded in the baseline `exceptions/concordia.baseline.json` (keep the entries that are already there), not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
