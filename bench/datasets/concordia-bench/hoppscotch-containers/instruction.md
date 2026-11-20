You are a developer on the `Hoppscotch` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Bind Hoppscotch to its container deployment and add container rules

Concordia is already adopted on `main`. Hoppscotch ships a Compose file and a multi-stage Dockerfile at the repository root. The architecture team wants the deployment described in the model and three rules about it.

Reference pages: `/opt/concordia/docs/reference/infrastructure-rules.md`, `bindings.md`, `policies.md` (typed rules: the `obligation` block), `workspace.md` (environments), `capability-profiles.md`.

## What to add

- Observation: add `docker-compose.yml`, `prod.Dockerfile` and `bench-env/**` to the files the project includes.
- Environments in `concordia.yaml`:
  - `selfhost`: the files `docker-compose.yml` and `prod.Dockerfile`.
  - `selfhost-hydrated`: the files under `bench-env/selfhost-hydrated/` (a small, plain Compose file with a Dockerfile that you create, see below).
- Create `bench-env/selfhost-hydrated/docker-compose.yml` with one service `backend` whose image is `ghcr.io/hoppscotch/bench-backend:1.0.0` and that is built from a Dockerfile `Dockerfile` in the same directory (build target `backend`). Create that `Dockerfile` with one stage named `backend` on the base image `node:20-alpine` that runs `node server.js` in `/app`.
- Bindings in a new file `bindings/concordia.containers.bindings.yaml`, registered in `concordia.yaml` next to the existing bindings file (id of every binding as given):

| Binding id | Node | What it binds |
|---|---|---|
| `bind-hopp-backend-compose` | `hopp-backend` | Compose service `hoppscotch-backend` of `docker-compose.yml` |
| `bind-hopp-selfhost-web-compose` | `hopp-selfhost-web` | Compose service `hoppscotch-app` of `docker-compose.yml` |
| `bind-hopp-sh-admin-compose` | `hopp-sh-admin` | Compose service `hoppscotch-sh-admin` of `docker-compose.yml` |
| `bind-hopp-db-compose` | `hopp-db` | Compose service `hoppscotch-db` of `docker-compose.yml` |
| `bind-hopp-db-image` | `hopp-db` | The image repository `postgres` (the binding does not have to match an entity) |
| `bind-hopp-backend-compose-hydrated` | `hopp-backend` | Service `backend` of `bench-env/selfhost-hydrated/docker-compose.yml` |
| `bind-hopp-backend-dockerfile-hydrated` | `hopp-backend` | The source file `bench-env/selfhost-hydrated/Dockerfile` (the binding does not have to match an entity) |
| `bind-hopp-backend-image` | `hopp-backend` | The image repository `ghcr.io/hoppscotch/bench-backend` (the binding does not have to match an entity) |

- A policy pack `hoppscotch-containers` (version `1.0.0`) at `policies/hoppscotch-containers.policy.yaml`, registered in `concordia.yaml`. It defines the same three named selectors as the adopt pack (`frontends`, `runtimes`, `clients`). Every rule has severity `error` and owner `team-hoppscotch`, and applies in each declared environment unless the rule says otherwise.

| Rule id | Family | Intent |
|---|---|---|
| `HOP-IMG-001` | `deployment-trace` | The image of `hopp-backend` must have a build definition: a Dockerfile it is built from. One build link is enough to pass; a missing link is only unknown, because build links are only partly visible. |
| `HOP-IMG-002` | `deployment-trace` | The Dockerfile that is bound to `hopp-backend` must declare a base image. Judge it on the build files that belong to the node, not on its images: one declared base image anywhere in the node's files is enough to pass, and an absence is unknown unless coverage is complete. |
| `HOP-CON-001` | `deployment-security` | `hopp-selfhost-web` must not be configured with a connection to `hopp-db` (for example a database URL in its environment). Only the backend may hold the database connection. The rule applies to the environment `selfhost` only. |

- Record pre-existing findings in the baseline if the check blocks. The typed rules are advisory in this release, so their results never change the exit code; do not try to make them mandatory.
- `concordia check` exits 0 on the committed HEAD of `main`. Commit everything on `main`.

## Acceptance criteria

- The environments, bindings and pack are valid and registered.
- A service of the self-hosted web app that gains a database connection is reported by `HOP-CON-001` in `selfhost`. When the build definition of the backend image disappears, `HOP-IMG-001` says unknown, never violation. The Dockerfiles bound to the backend pass `HOP-IMG-002`.
- Everything is committed on `main`.
