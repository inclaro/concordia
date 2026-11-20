You are a developer on the `Strimzi` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Strimzi: protect the operator structure

The Concordia workspace of Strimzi (adopted earlier, pack `strimzi-adopt`) guards the layers and the API leaf. The architecture team now wants three more structural rules, in a new pack `strimzi-structure` (version `1.0.0`) at `policies/strimzi-structure.policy.yaml`. Register the file in `concordia.yaml`; keep the existing pack. Every rule has severity `error`, owner `team-strimzi`, a one-sentence rationale and a remediation. Only `IMPORTS` edges count in all three rules.

Relevant documentation: `/opt/concordia/docs/reference/policies.md`, `workspace.md`, `exceptions.md`, `results.md`.

## Rules

- `STZ-IND-001` "The three operators are independent": the cluster, topic and user operators (`stz-cluster-operator`, `stz-topic-operator`, `stz-user-operator`) are deployed and released separately, so none of them may import another. Concordia reports all edges between the same pair of operators as one finding for the pair, not one for each file or class.
- `STZ-CYC-001` "No import cycles between the small Strimzi modules": the module graph of `stz-topic-operator`, `stz-user-operator`, `stz-certificate-issuer` and `stz-config-model` must be acyclic. Cycles are searched between the modules (the bound CALM nodes), not between single classes.
- `STZ-BUD-001` "Topic and user operators do not grow towards the cluster operator": the topic operator and the user operator import the cluster operator in some places today (the code indexer reports 11 such imports). This is a ratchet: they may have at most 11 imports of `stz-cluster-operator`, and when the count goes above 11 Concordia reports one finding that gives the count.

## Acceptance criteria

- The new pack is valid and loaded by the workspace; the earlier pack is still loaded.
- Existing violations of the new rules are recorded in the baseline (not fixed). The ratchet and the cycle rule have no findings today.
- `concordia check` exits 0 on the committed HEAD of `main`.
- Everything is committed on `main`.
