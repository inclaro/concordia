You are a developer on the `knative-serving` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Guard the queue-proxy sidecar

Concordia is already adopted on this repository (`concordia.yaml`, the model in `architecture/knative-serving.calm.json`, and the pack `knative-serving-adopt`). The queue-proxy (`kn-queue`) is injected into every user pod, so it must stay small. Add two rules about it in a new pack `knative-serving-structure` (version `1.0.0`) at `policies/knative-serving-structure.policy.yaml`. Give each rule severity `error`, owner `team-knative-serving`, a one-sentence rationale and a remediation. The workspace already loads every `policies/*.policy.yaml`.

Relevant documentation: `/opt/concordia/docs/reference/policies.md`, `exceptions.md`, `results.md`.

## Rules

- `KN-DNY-001` "The queue-proxy never reaches the reconcilers": no chain of imports or calls of up to four steps may lead from `kn-queue` to `kn-controller`, however many helper modules lie between them. A direct dependency counts as a chain of one step. The reason is blast radius.
- `KN-ALW-001` "The queue-proxy depends only on shared helpers and API types": the only modules that `kn-queue` may import or call are `kn-shared` and `kn-apis`. Any other module that it depends on is reported. The reason is a missing anti-corruption layer.

## Acceptance criteria

- The new pack is committed and valid.
- The code already has violations of both rules. Record them in the baseline `exceptions/concordia.baseline.json` (keep the findings that are already there); do not change the source code.
- `concordia check` exits 0 on the committed HEAD of `main`.
- A new import or call from the queue-proxy that breaks one of the rules shows up as a new, active finding.
- Everything is committed on `main`.
