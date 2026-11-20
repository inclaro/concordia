You are a developer on the `argo-cd` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Roll out the Argo CD security obligations

The deployment trace and the pack `argo-cd-deploy-security` (rules `ARGO-SEC-001`, `ARGO-NET-101` and `ARGO-SEC-002`, family `deployment-security`) are committed on `main`. The security team wants to roll them out in stages: first observed silently in the HA install, advisory everywhere else. Change the enforcement; do not change the rules.

Relevant documentation: `/opt/concordia/docs/reference/workspace.md` (`check.enforcement`, `check.rollback`), `infrastructure-rules.md` (Enforcement), `cli.md` (rollout report), `results.md`, `diagnostics.md`, `policies.md`.

## Enforcement

In `concordia.yaml` set `check.enforcement`, narrowest entry first:

1. the family `deployment-security` in the environment `ha`: mode `shadow`;
2. the family `deployment-security`: mode `advisory`;
3. the families `deployment-placement` and `deployment-trace`: mode `advisory`.

Replace the current entries with these. Do not set any mode `mandatory`: these families do not support it in this release. Do not add `check.rollback`.

## Acceptance criteria

- `concordia check` exits 0 on the committed HEAD of `main`.
- The findings of the family `deployment-security` in the environment `ha` have the enforcement `shadow`; in the other environments they have `advisory`.
- `concordia check --rollout-report` writes the rollout report to `build/concordia/rollout-report.json`.
- Everything is committed on `main`.
