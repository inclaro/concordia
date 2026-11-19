You are a developer on the `argo-cd` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Assurance for the Argo CD install

Concordia is already adopted on Argo CD (model, bindings, pack `argo-cd-adopt` and baseline are committed on `main`). The security team wants evidence about what the install manifests really allow: which workload can reach which other one on the network, and who can read the settings secret. Concordia renders the install itself and evaluates network policies and roles. Add that evidence; keep what exists.

Relevant documentation: `/opt/concordia/docs/reference/assurance.md`, `assurance-rules.md`, `workspace.md` (`environments`, `assurance`, `.concordia.lock`), `policies.md` (effective access rules), `bindings.md`, `results.md`.

## Environment and rendering

- Declare the environment `non-ha` with the source `manifests/namespace-install.yaml`, and add that file to the observation `include` (next to the Go globs).
- Add the Kustomize overlay `gitops/production/kustomization.yaml`: it sets the namespace `argocd` and lists the Kustomize base directory `../../manifests/namespace-install` as its only resource (a Kustomize overlay brings in base directories, not loose files from outside its own directory). Do not modify the upstream manifests.
- Under `assurance`, render the overlay for `non-ha` with Kustomize, and enable the evaluators `network-policy` and `rbac` for `non-ha`, declaring both enforcement assumptions (the network plugin enforces NetworkPolicy; the RBAC authorizer is enabled). Update the lock file with `concordia lock update` and commit it.
- Declare a second environment `staging` with the same source file. Staging is a shared pre-production cluster: nobody has confirmed that its network plugin enforces NetworkPolicy or that its RBAC authorizer is enabled, so the security team does not want Concordia to assume either there. Render the same overlay for `staging` with Kustomize and enable the same two evaluators for it, without declaring any enforcement assumption.
- Enable the built-in pack `builtin:assurance`.

## Nodes and bindings

Add to the model (title-case name, description, tags in `concordia.tags`):

| Node id | Type | Tags | Role |
|---|---|---|---|
| `argo-dex` | service | `external` | Dex OpenID Connect identity provider. |
| `argo-redis` | database | `restricted` | Redis cache of Argo CD. |
| `argo-secret` | data-asset | - | The Argo CD settings secret. |

Add bindings, none of them required (they are matched while `check` runs), in both environments `non-ha` and `staging`, with the resource kind and name and no namespace for workloads:

- `argo-server`: Deployment `argocd-server`; `argo-repo-server`: Deployment `argocd-repo-server`; `argo-notifications`: Deployment `argocd-notifications-controller`; `argo-dex`: Deployment `argocd-dex-server`; `argo-redis`: Deployment `argocd-redis`.
- `argo-secret`: a workload-style binding with the resource kind `Secret` and the name `argocd-secret` (only workload and image bindings reach the rendered resources that the evaluators read).

## Policy pack

Put the rules in the pack `argo-cd-assurance` (version `1.0.0`) at `policies/argo-cd-assurance.policy.yaml`. Each rule has severity `error`, owner `team-argo-cd`, a one-sentence rationale and a remediation. Each asks the evaluators for the effective access of the environment.

- `ARGO-NET-001` "The identity provider has no network path to Redis": no traffic from `argo-dex` to `argo-redis` on port 6379.
- `ARGO-NET-002` "The API server reaches the repo server on its gRPC port": traffic from `argo-server` to `argo-repo-server` on port 8081 is allowed.
- `ARGO-NET-003` "The notifications controller has no network path to Redis": no traffic from `argo-notifications` to `argo-redis` on port 6379.
- `ARGO-AUTH-001` "The API server can read the Argo CD settings secret": `argo-server` is granted access to `argo-secret`.
- `ARGO-AUTH-002` "The identity provider cannot read the Argo CD settings secret": `argo-dex` is not granted access to `argo-secret`.

- `ARGO-CLM-001` "Every container image version in the non-HA install is resolved" (severity `warning`, because assurance claims are experimental): the assurance claim that the rendered `non-ha` environment gives a resolved version for every container occurrence must hold.

## Acceptance criteria

- `concordia assurance validate` exits 0.
- `concordia check` exits 0 on the committed HEAD of `main` (the assurance rules are advisory; report what the evaluators find, do not change the roles or policies of the install to make a rule pass). Where an environment does not declare what a result depends on, Concordia must say that it cannot judge, not pass.
- Everything is committed on `main`.
