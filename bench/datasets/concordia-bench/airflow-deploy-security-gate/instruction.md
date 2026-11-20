You are a developer on the `airflow` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Review pull requests against the typed deployment rules

Concordia is already set up on `main` (the code model, and typed security and availability rules over the rendered production manifests in `bench-env/prod-hydrated/`). Your teammates opened these branches: `pr/disable-scheduler-disruption-budget`, `pr/sdk-reuses-own-definitions`, `pr/sdk-uses-core-models`. Review each branch with Concordia against `main` and write the review file below.

Relevant documentation: `/opt/concordia/docs/reference/infrastructure-rules.md`, `results.md`, `cli.md`.

Write `/workspace/repo/review.json` (do not commit it, and do not change, commit to or delete any branch, including `main`) in this shape:

```json
{"branches": {"<branch>": {"verdict": "pass", "violations": [{"ruleId": "<rule id>", "path": "<repo-relative file, or \"\" when the finding has no file>"}], "errors": []}}}
```

- `verdict`: `"fail"` when `concordia check` exits non-zero on the branch, otherwise `"pass"`.
- `violations`: every finding that is `active` on the branch and whose fingerprint is not an `active` finding on `main` except informational findings with an `unknown` result (severity `info` and result `unknown`), which are left out. Rule id and file of each; list a rule and file once.
- `errors`: when Concordia refuses to run the check on the branch (no results; it reports error codes such as `CO-…`), the codes it reports; otherwise `[]`.
