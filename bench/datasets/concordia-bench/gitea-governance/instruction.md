You are a developer on the `Gitea` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Govern the Gitea architecture decisions

Concordia is already adopted on `main` (pack `gitea-adopt` with the rules `GITEA-LYR-001` and `GITEA-MIG-001`). The architecture team wants the decisions behind those rules to be written down, committed, and checked: a decision must exist, be in force, be enforced by a rule that fails the build, and a change to migrations must come with a decision.

Relevant reference pages: `/opt/concordia/docs/reference/adr-governance.md`, `workspace.md`, `policies.md`, `results.md`.

## Decisions

Write three decision records in `docs/adr/`. Each is a Markdown file whose first line is `# ADR-NNNN: <title>`, followed by a line `Status: Accepted`, a `Date:` line and the sections Context, Decision and Consequences (a few sentences each).

| File | Decision id | Title | Governs | Enforced by |
|---|---|---|---|---|
| `docs/adr/0001-frozen-migrations.md` | `ADR-0001` | Frozen migrations | node `gitea-modelmigration` | rule `GITEA-MIG-001` of pack `gitea-adopt` 1.0.0 (required) |
| `docs/adr/0002-layered-architecture.md` | `ADR-0002` | Layered architecture | nodes `gitea-cmd`, `gitea-routers`, `gitea-services`, `gitea-models` | rule `GITEA-LYR-001` of pack `gitea-adopt` 1.0.0 (required) |
| `docs/adr/0003-database-ownership.md` | `ADR-0003` | Database ownership | node `gitea-db` | no rule yet (enforcement not required) |

Categories: `ADR-0001` has the category `data-model`; `ADR-0002` and `ADR-0003` have the category `system-boundaries`.

## Workspace changes

- The decision register is `docs/adr/decisions.yaml`. Register the three decisions with the facts above.
- Only decisions with the status `accepted` are in force.
- Add one trigger, `TRG-MIGRATIONS`, with the category `data-model`: a change to a file under `modelmigration/` must come with a change to a registered decision of that category.
- Turn on the decision rules of the built-in pack for governance of architecture decisions, next to the existing pack.
- Concordia only reads Markdown decisions when the code graph is indexed in `full` mode: switch the index mode to `full` and add `docs/adr/**` to the included paths of the project. Keep the other settings.
- `concordia adr validate` must pass.

## Decision link rule

Add a second policy pack `gitea-governance` (version 1.0.0) in `policies/gitea-governance.policy.yaml`, listed in the workspace policy files, with one error rule `GITEA-DEC-101`, named "Layering decisions that require enforcement stay linked to a rule", owned by `team-gitea`. Its intent: every decision in the register that requires enforcement must link a rule that enforces it. Give it a one-sentence rationale naming maintainability and a remediation.

## Acceptance criteria

- The register, the decision files and the workspace changes are committed on `main`.
- `concordia check` exits 0 on the committed HEAD of `main`. Findings that already exist and are not about decisions are in the baseline already; if new ones appear because of the index mode, add them to the baseline `exceptions/concordia.baseline.json` instead of fixing code.
