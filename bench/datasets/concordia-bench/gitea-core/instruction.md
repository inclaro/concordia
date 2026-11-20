You are a developer on the `Gitea` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Enable the Concordia core rules on Gitea

Concordia is already adopted on `main` (pack `gitea-adopt`). The architecture team also wants the generic rules that ship with Concordia, tuned to Gitea.

Relevant reference pages: `/opt/concordia/docs/reference/core-rules.md`, `policies.md`, `workspace.md`, `architecture.md`, `exceptions.md`.

## Changes

- Use the built-in `core` policy pack in `concordia.yaml`, next to the existing pack.
- Tag the nodes in `architecture/gitea.calm.json` (in each node's `concordia.tags`, keeping the tags that exist):
  - `gitea-routers`: `presentation`
  - `gitea-services`: `application`
  - `gitea-models`: `domain`
  - `gitea-modelmigration`: `infrastructure`
  - `gitea-modules` and `gitea-setting` keep `shared` and `migration-safe`.
- Tune the core rules with overrides on the pack entry, without copying the pack:
  - `CORE-LYR-003` (shared code is a leaf) gets the severity `error`, so that a shared module that depends on a service fails the build.
  - `CORE-SEC-001` (trust boundary) is turned off: Gitea has no public/restricted split in this model.
  - `CORE-DECL-001` (dependencies are declared) gets the minimum evidence confidence `syntactic`.
  - `CORE-OBS-001` (no architecture drift) cannot be fully judged on Gitea because the file coverage of the index is below what the rule needs, so it stays `unknown`. Do not hide that: leave the verdict as it is, but make the unknown result of this rule a warning that does not fail the check.
- Violations that already exist in the code are recorded in the baseline `exceptions/concordia.baseline.json` (keep the entries that are already there), not fixed.

## Acceptance criteria

- The CALM model stays valid and the new tags are in the model.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
