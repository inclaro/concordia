You are a developer on the `Gitea` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Review a pull request with Concordia

Concordia is already adopted on `main` (rules `GITEA-LYR-001` and `GITEA-MIG-001`). A teammate opened a pull request: the local branch `feature/bench-change`, targeting `main`.

Relevant reference pages: `/opt/concordia/docs/reference/exceptions.md`, `results.md`, `cli.md`.

1. Run Concordia as the architecture gate for the changes of that branch only.
2. Write your review to `/workspace/repo/review.json` in this shape, listing every violation of severity `error` that the change introduces as it stands before any waiver (not the baselined ones, and not the informational findings with an `unknown` result). The waiver of step 3 does not remove a finding from the review. Do not commit `review.json`.

   ```json
   {"verdict": "pass", "violations": [{"ruleId": "<rule id>", "path": "<repo-relative file>"}]}
   ```

   `verdict` is `"pass"` or `"fail"`.
3. The architecture board accepted the migration finding only: the migration that uses a model is being rewritten. Add a finding waiver for the `GITEA-MIG-001` finding of the change, with owner `team-gitea`, issue `GITEA-BENCH-1`, expiry `2099-12-31` and a reason of your choice. Do not waive any other finding. Commit the waiver file on `feature/bench-change` and leave that branch checked out.
4. Leave the teammate's code unchanged. The layering finding must stay visible as an active finding, so the gate must still fail for it.
