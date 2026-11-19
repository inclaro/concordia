You are a developer on the `volcano` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Keep the security review of Volcano admission current

The admission webhooks decide which jobs and queues enter a Volcano cluster, so the security team wants their security assessment to stay valid: when the identity, the role bindings or the code flows of admission change, the approval must stop counting until somebody reviews the change again.

Relevant documentation: `/opt/concordia/docs/reference/assessments.md`, `workspace.md`, `policies.md`, `bindings.md`, `results.md`, `/opt/concordia/docs/explanation/review-currency.md` and `/opt/concordia/docs/how-to/review-and-approve-assessments.md`.

## Workspace

- Declare one environment `dev` whose manifests are `installer/volcano-development.yaml` and everything under `bench-env/dev/`. The observation project must include these two paths next to the code directories it already includes. The platform team keeps a reviewed extra role binding for the admission service account in `bench-env/dev/volcano-admission-extra.yaml` (a role `volcano-admission-leases` that allows leases and a role binding of the same name in `volcano-system`); create that file as described.
- Mark every binding of a deployed resource (workload, image, namespace and other infrastructure resources) as not required (`required: false`): `concordia bind validate` checks the code graph only and would report them as matching nothing.

Bind the node `vc-admission` to the workloads Deployment `volcano-admission` and Job `volcano-admission-init` in `dev`, in the new file `bindings/volcano.review.bindings.yaml` (list it in the workspace).
- Enable the built-in review currency pack.
- Assessments live under `assessments/`. The trust anchor is the git reference `main`. Declare one authority `security-review` that approves assessments by protected review, accepts the reviewer `secops@bench.invalid` and the records under `assessments/approvals/` and `assessments/revocations/`.
- Require the assessment `ASM-ADMISSION` (requirement id `admission-security`, type security, authority `security-review`) for the node `vc-admission` in the environment `dev`.

## The assessment

Write `assessments/ASM-ADMISSION/v1.yaml`: a security assessment titled "Volcano admission security", scope `vc-admission`, environment `dev`, outcome `satisfactory`, state `ready-for-review`, no assumptions and no evidence entries. Its subject depends on three things of `vc-admission`: its runtime identity, its role bindings, and its flows to other components (the `binding` dependency kind).

## Approval

Commit the workspace on `main`, then approve the assessment as the reviewer `secops@bench.invalid` with `concordia assessment approve` (confirm the subject digest it prints), and commit the approval record on `main`. Run `concordia assessment approve` outside of CI (the `CI` variable must not be set).

## Acceptance criteria

- `concordia assessment validate` passes and `concordia assessment status --json` reports the currency of `ASM-ADMISSION` as `current`.
- `concordia check` exits 0 on the committed HEAD of `main` and the review rule that requires an approval passes.
- Everything is committed on `main`.
