You are a developer on the `Strimzi` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Strimzi: roll out the deployment obligations gradually

The deployment trace (environments `install` and `install-hydrated`, packs `builtin:infrastructure` and `strimzi-deploy-security`) is in place and the three security obligations `STZ-SEC-001`, `STZ-SEC-002` and `STZ-SEC-003` (family `deployment-security`) run as advisory today. The platform lead wants a staged roll-out, and a time-boxed switch that turns the new enforcement off while the team fixes the findings. Change only `concordia.yaml`, and commit on `main`.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md` (the enforcement and rollback keys of the check section), `cli.md` (the rollout report), `diagnostics.md`, `infrastructure-rules.md` and the how-to `/opt/concordia/docs/how-to/roll-out-new-obligations.md`.

## Staged enforcement

Set the enforcement of the rule families so that:

1. the family `deployment-security` runs in shadow mode in the environment `install` (its findings go to `results.json` only);
2. the family `deployment-security` is advisory in every other environment;
3. the families `deployment-placement` and `deployment-trace` stay advisory in every environment.

The narrow entry must win over the general one.

## Rollback

Declare a rollback: reason "roll out the Strimzi deployment obligations gradually", authorized by `platform-lead@bench.invalid`, last day `2099-12-31`. While it is active Concordia runs every typed rule as shadow.

## Rollout report

Run `concordia check --rollout-report` once, to see which rows are ready for the next step. Do not commit the report (it is a build output). The report changes no mode and no exit code.

## Acceptance criteria

- `concordia check` succeeds (exit 0) and the workspace is committed on `main`.
- Nothing is baselined or waived. An unknown stays an honest unknown.
- Concordia must refuse a rule of an advisory-maturity family that is made mandatory (exit 2): do not make anything mandatory.
