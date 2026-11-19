You are a developer on the `browser-use` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Review teammate branches against the review-currency rules of browser-use

Concordia is already set up on `main`: it requires a security assessment of the LLM adapters (`bu-llm`) with a current approval by the security reviewer, on top of the project rules and the built-in review-currency pack. Your teammates opened these branches: `pr/move-llm-adapters-to-internet-zone`, `pr/drop-llm-trust-declaration`, `pr/add-security-reviewer`, `pr/agent-reuses-tool-registry`.

Review each branch with Concordia against `main` and write `/workspace/repo/review.json` (do not commit it, and do not change, commit to or delete any branch, including `main`). Relevant reference pages: `/opt/concordia/docs/reference/results.md`, `cli.md`, `assessments.md`, `trust-context.md`, `workspace.md`, `diagnostics.md`, `/opt/concordia/docs/how-to/review-and-approve-assessments.md`.

The review file has this shape:

```json
{"branches": {"<branch>": {"verdict": "pass", "violations": [{"ruleId": "<rule id>", "path": "<repo-relative file, or \"\" when the finding has no file>"}], "errors": []}}}
```

- `verdict`: `"fail"` when `concordia check` exits non-zero on the branch, otherwise `"pass"`.
- `violations`: every finding that is `active` on the branch and whose fingerprint is not an `active` finding on `main` (leave out informational findings with an `unknown` result, i.e. severity `info` and result `unknown`). Rule id and file of each; list a rule and file once.
- `errors`: when Concordia refuses to run the check on the branch (no results; it reports error codes such as `CO-…`), the codes it reports; otherwise `[]`.

Leave the repository as you found it: end with `main` checked out.
