You are a developer on the `browser-use` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Require a reviewed security assessment for the LLM adapters of browser-use

The security group wants the egress of the LLM adapters (`bu-llm`) to stay under review: a security assessment must exist for them and carry a current approval by the security reviewer, and Concordia must flag it as soon as what was reviewed changes. Documentation: `/opt/concordia/docs/reference/assessments.md`, `workspace.md`, `trust-context.md`, `policies.md`, `/opt/concordia/docs/how-to/review-and-approve-assessments.md`.

## Workspace

- Declare one environment `library` whose sources are `pyproject.toml` (assessments are bound to an environment).
- The workspace allows the data classifications `public`, `internal` and `secret`. On the architecture model, `bu-llm` is in the trust zone `egress` and has the data classification `internal` (`concordia.trust` in `architecture/browser-use.calm.json`). Do not change anything else in the model or the bindings.
- Enable the built-in review-currency pack in `concordia.yaml`, next to the existing pack `browser-use-adopt` (keep it). Leave the rules at their default enforcement.
- Add a repository rule `BU-ASM-001` "The LLM egress assessment has a current verified approval" in a new policy pack `browser-use-review` (version 1.0.0, file `policies/browser-use-review.policy.yaml`, listed in the workspace policy files): it fails when the required assessment has no current verified approval. Give it severity error and owner `team-browser-use`.
- Assessments: the directory `assessments`. The trust anchor is the git ref `main`.
- Authority `security-review`: role reviewer, mechanism protected review, the only accepted reviewer is `secops@bench.invalid`, and it accepts records under `assessments/approvals/**` and `assessments/revocations/**`.
- Required assessment `llm-egress`: assessment id `ASM-LLM-EGRESS`, type security, scope the node `bu-llm`, environment `library`, authority `security-review`.
- Write the assessment `ASM-LLM-EGRESS` version 1 (file `assessments/ASM-LLM-EGRESS/v1.yaml`): type security, title "LLM adapter egress", scope `bu-llm`, environment `library`, outcome satisfactory, authored state ready for review, no assumptions and no evidence. Its subject depends on three things for `bu-llm`: how the node is bound to code, its trust context, and its data classification.
- Commit all of this on `main`, then act as the reviewer `secops@bench.invalid`: approve `ASM-LLM-EGRESS` (look at the subject first, then confirm the digest you examined) and commit the approval record on `main`.

## Acceptance criteria

- `concordia assessment status --json` shows `ASM-LLM-EGRESS` with the currency `current`, and `concordia check` reports no violation of the review rules.
- Everything, including the approval record, is committed on `main`.
