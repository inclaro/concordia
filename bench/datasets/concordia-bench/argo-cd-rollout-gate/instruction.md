You are a developer on the `argo-cd` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Review the pull requests on the Argo CD enforcement roll-out

Concordia is already set up on `main`: the code model and its rules, the deployment trace, and the security obligations of the pack `argo-cd-deploy-security`, rolled out in stages through `check.enforcement` in `concordia.yaml` (silently observed in the HA install, advisory everywhere else). Your teammates opened these branches: `pr/make-security-rules-blocking`, `pr/pause-enforcement-until-2099`, `pr/pause-enforcement-until-2020`, `pr/server-uses-repo-server-client`. Review each branch with Concordia against `main` and write `/workspace/repo/review.json` (do not commit it, and do not change, commit to or delete any branch, including `main`).

Relevant documentation: `/opt/concordia/docs/reference/cli.md`, `results.md`, `workspace.md` (`check.enforcement`, `check.rollback`), `infrastructure-rules.md`, `diagnostics.md`, `/opt/concordia/docs/how-to/roll-out-new-obligations.md`, `/opt/concordia/docs/how-to/manage-baselines-and-waivers.md`.

```json
{"branches": {"<branch>": {"verdict": "pass", "violations": [{"ruleId": "<rule id>", "path": "<repo-relative file, or \"\" when the finding has no file>"}], "errors": []}}}
```

- `verdict`: `"fail"` when `concordia check` exits non-zero on the branch, otherwise `"pass"`.
- `violations`: every finding that is `active` on the branch and whose fingerprint is not an `active` finding on `main`, except informational findings with an `unknown` result (severity `info` and result `unknown`), which you leave out. Rule id and file of each; list a rule and file once.
- `errors`: when Concordia refuses to run the check on the branch (no results; it reports error codes such as `CO-…`), the codes it reports; otherwise `[]`.
