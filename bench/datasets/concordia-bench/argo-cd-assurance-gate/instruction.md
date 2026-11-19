You are a developer on the `argo-cd` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Review the pull requests on the Argo CD install overlay

Concordia is already set up on `main`: the code model and its rules, and assurance evidence for the install (Concordia renders the Kustomize overlay `gitops/production` for the environments `non-ha` and `staging`, and evaluates the network policies and roles of what it renders). Your teammates opened these branches: `pr/open-redis-policy-in-overlay`, `pr/overlay-adds-second-base`, `pr/server-uses-repo-server-client`. Review each branch with Concordia against `main` and write `/workspace/repo/review.json` (do not commit it, and do not change, commit to or delete any branch, including `main`).

Relevant documentation: `/opt/concordia/docs/reference/cli.md`, `results.md`, `assurance.md`, `diagnostics.md`, `/opt/concordia/docs/how-to/manage-baselines-and-waivers.md`.

```json
{"branches": {"<branch>": {"verdict": "pass", "violations": [{"ruleId": "<rule id>", "path": "<repo-relative file, or \"\" when the finding has no file>"}], "errors": []}}}
```

- `verdict`: `"fail"` when `concordia check` exits non-zero on the branch, otherwise `"pass"`.
- `violations`: every finding that is `active` on the branch and whose fingerprint is not an `active` finding on `main`, except informational findings with an `unknown` result (severity `info` and result `unknown`), which you leave out. Rule id and file of each; list a rule and file once.
- `errors`: when Concordia refuses to run the check on the branch (no results; it reports error codes such as `CO-…`), the codes it reports; otherwise `[]`.
