You are a developer on the `argo-cd` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Structure rules for Argo CD

Concordia is already adopted on Argo CD: the model, the bindings, the pack `argo-cd-adopt` and the baseline are committed on `main`. The architecture team now wants two more structural guards. Add them as a second pack and keep the adopted workspace as it is.

Relevant documentation: `/opt/concordia/docs/reference/policies.md`, `exceptions.md`, `results.md`.

## Policy pack

Put the rules below in the pack `argo-cd-structure` (version `1.0.0`) at `policies/argo-cd-structure.policy.yaml`. Each rule has severity `error`, owner `team-argo-cd`, a one-sentence rationale and a remediation. Imports and calls both count for both rules.

- `ARGO-OWN-001` "Only the API server and the controllers read the settings and credential store": the node `argo-db` holds the settings and the repository and cluster credentials. Only `argo-server`, `argo-app-controller` and `argo-appset-controller` may use it. Any other node that imports or calls it is a finding.
- `ARGO-CYC-001` "The repo server, the ApplicationSet controller, the notifications controller and the commit server form no dependency cycle": among the nodes `argo-repo-server`, `argo-appset-controller`, `argo-notifications` and `argo-commit-server` the dependency graph must be acyclic. The component dependencies are collapsed to the architecture nodes before cycles are searched. The HTTP route entities that the code graph reports are not part of the model, so the rule cannot judge them and its verdict stays `unknown` (reason: unbound endpoint, reported as a binding gap) on the clean code. The team accepts that: an unknown verdict of this rule must warn and not fail the run, while a cycle is still reported as a finding.

## Acceptance criteria

- Existing violations of the new rules are recorded in the baseline `exceptions/concordia.baseline.json`, not fixed.
- `concordia check` exits 0 on the committed HEAD of `main`.
- A new import of the credential store from the notifications controller, and a new dependency of the repo server on the ApplicationSet controller (which adds a dependency cycle), each make `concordia check` fail.
- Everything is committed on `main`.
