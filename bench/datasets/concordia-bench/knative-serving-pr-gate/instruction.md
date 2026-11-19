You are a developer on the `knative-serving` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Gate a pull request that touches the edge and the API layer

Concordia is already set up on `main` (the code model with the rules `KN-LYR-001` and `KN-CYC-001`, the deployment trace with the environments `production` and `edge`, and the rule `KN-PLC-003`). A teammate opened a pull request: the local branch `feature/bench-change`, targeting `main`.

Relevant documentation: `/opt/concordia/docs/reference/exceptions.md`, `assessments.md` (authorities and the trust anchor), `workspace.md`, `results.md`, `cli.md`, `/opt/concordia/docs/how-to/manage-baselines-and-waivers.md`.

1. Gate the change: run Concordia for the changes of that branch relative to `main` only, so that the findings that already exist on `main` are not counted against the branch.
2. Write your review to `/workspace/repo/review.json` in this shape, listing every violation of a rule that the change introduces (the file in which the violating code or manifest is), before any waiver is applied. Do not commit `review.json`.

   ```json
   {"verdict": "pass", "violations": [{"ruleId": "<rule id>", "path": "<repo-relative file>"}]}
   ```

   `verdict` is `"pass"` or `"fail"`.
3. The architecture board accepted the edge placement of the activator for a pilot, until `2099-12-31`. Record that as a waiver for the rule `KN-PLC-003` that covers the subject `kn-activator` in the environment `edge` (not one fingerprint): owner `team-knative-serving`, issue `KN-PILOT-1`, a reason of your choice. The waiver must be approved by an exception authority with the id `arch-exceptions` (mechanism protected review, reviewer `board@bench.invalid`, records under `exceptions/`; create the directory `assessments/` with an empty `.gitkeep` file, which Concordia wants to find). Concordia verifies an approval against the trust anchor, which is `main`: put the authority and the waiver on `main`, then bring `main` into `feature/bench-change` (merge). Do not waive anything else.
4. Leave the teammate's code unchanged. The layering finding of the change must stay visible as an active finding, so the gate must still fail for it. Leave `feature/bench-change` checked out.
