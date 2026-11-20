You are a developer on the `fzf` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Review teammates' branches against the fzf architecture

Concordia is already set up on `main`: the fzf architecture model with its layering and cycle rules, and two architecture views (a generated container view with its exports, and a hand-drawn one in `docs/architecture/components.md`) that Concordia keeps current. Your teammates opened these branches: `pr/draw-protector-using-utilities`, `pr/renderer-uses-matcher`, `pr/tweak-generated-mermaid`, `pr/tweak-generated-json` and `pr/tui-measures-text`. Review each branch with Concordia against `main` and write `/workspace/repo/review.json` (do not commit it, and do not change, commit to or delete any branch, including `main`).

Relevant documentation: `/opt/concordia/docs/reference/results.md`, `cli.md`, `/opt/concordia/docs/how-to/keep-diagrams-current.md`.

The format of `review.json`:

```json
{"branches": {"<branch>": {"verdict": "pass", "violations": [{"ruleId": "<rule id>", "path": "<repo-relative file, or \"\" when the finding has no file>"}], "errors": []}}}
```

- `verdict`: `"fail"` when `concordia check` exits non-zero on the branch, otherwise `"pass"`.
- `violations`: every finding that is `active` on the branch and whose fingerprint is not an `active` finding on `main`, except the informational findings (severity `info`) with an `unknown` result. Rule id and file of each; list a rule and file once.
- `errors`: when Concordia refuses to run the check on the branch (no results; it reports error codes such as `CO-…`), the codes it reports; otherwise `[]`.
