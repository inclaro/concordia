You are a developer on the `Nushell` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Review three branches with Concordia

Concordia is already set up on `main`: the Nushell crate rules, the quality scenarios and required quality areas, and the verification rule for the engine's unit tests. Your teammates opened these branches: `pr/require-security-quality-area`, `pr/rename-unit-tests-check`, `pr/use-protocol-in-commands`. Review each branch with Concordia against `main` and write `/workspace/repo/review.json` (do not commit it, and do not change, commit to or delete any branch, including `main`).

```json
{"branches": {"<branch>": {"verdict": "pass", "violations": [{"ruleId": "<rule id>", "path": "<repo-relative file, or \"\" when the finding has no file>"}], "errors": []}}}
```

- `verdict`: `"fail"` when `concordia check` exits non-zero on the branch, otherwise `"pass"`.
- `violations`: every finding that is `active` on the branch and whose fingerprint is not an `active` finding on `main` (informational findings with an `unknown` result, severity `info`, are left out). Rule id and file of each; list a rule and file once.
- `errors`: when Concordia refuses to run the check on the branch (no results; it reports error codes such as `CO-…`), the codes it reports; otherwise `[]`.

Relevant documentation: `/opt/concordia/docs/reference/results.md`, `cli.md`, `assurance.md`, `exceptions.md`, `diagnostics.md`.
