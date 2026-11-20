You are a developer on the `Gitea` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Review pull requests against the Gitea architecture decisions

Concordia is already set up on `main`: the architecture model with the rules `GITEA-LYR-001` and `GITEA-MIG-001`, the register of architecture decisions in `docs/adr/` with the built-in decision governance rules, the rule `GITEA-DEC-101`, and a trigger that wants a decision change next to every migration change. Your teammates opened these branches:

- `pr/deprecate-layering-decision`
- `pr/retire-migration-decision`
- `pr/decisions-replace-each-other`
- `pr/unlink-migration-decision-rule`
- `pr/unlink-layering-decision-rule`
- `pr/downgrade-migration-rule`
- `pr/migration-note-and-comment-fixes`

Review each branch with Concordia against `main`: check only the changes of the branch (`concordia check` can limit a run to the changes since a base ref), so that the findings that already exist on `main` are not counted against it. Write `/workspace/repo/review.json` (do not commit it, and do not change, commit to or delete any branch, including `main`).

Relevant documentation: `/opt/concordia/docs/reference/cli.md`, `results.md`, `adr-governance.md`, `policies.md`.

```json
{"branches": {"<branch>": {"verdict": "pass", "violations": [{"ruleId": "<rule id>", "path": "<repo-relative file, or \"\" when the finding has no file>"}], "errors": []}}}
```

- `verdict`: `"fail"` when `concordia check` exits non-zero on the branch, otherwise `"pass"`.
- `violations`: every finding that is `active` on the branch and whose fingerprint is not an `active` finding on `main`, except informational findings with an `unknown` result (severity `info` and result `unknown`), which are left out. Rule id and file of each; list a rule and file once.
- `errors`: when Concordia refuses to run the check on the branch (no results; it reports error codes such as `CO-…`), the codes it reports; otherwise `[]`.

Index each branch before you check it. Leave `main` checked out when you are done.
