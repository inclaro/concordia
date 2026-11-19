# Infra fixture

A small payments repository with Kubernetes, Docker, Helm, Kustomize, Terraform,
shell, CI, ADR and diagram files. Concordia indexes it with `codebase-memory-mcp`
0.12.1 (`mise run record-cbm`) and replays the recording in
test/integration/infra_replay_test.go. It reproduces the CBM 0.12.1 observations O1 to O12
that test/integration/infra_replay_test.go asserts.

Some values are fake secrets for the redaction tests. They are listed in test/fixtures/infra-seeds.json, outside
this directory. Nothing here is a real credential.

Nothing here is built, deployed or rendered. The files exist to reproduce CBM observations, so some are
incomplete on purpose: the Dockerfile has no `go.mod` in its build context and the Helm template needs a value
that `values.yaml` does not set (O9, O11). Fixing them would change the recorded line numbers and edges.

## Observations

Each line names one observation that a subtest of `TestInfraObservations` asserts.

- O1. Default exclusions: they are reported as coverage loss.
- O2. Kubernetes resource identity (`Resource` nodes).
- O3. Image identity (`Image` nodes): the identity is the repository.
- O4. Occurrence merging: two containers with one image repository give one `USAGE` edge.
- O5. Reference `USAGE` edges and undeclared targets.
- O6. Selector joins: they are scoped to the directory and silent about gaps.
- O7. Routes.
- O8. Deployment actions (`DEPLOYS`).
- O9. Build and image lineage (`DEPENDS_ON`).
- O10. Configuration (`CONFIGURES`).
- O11. Other infrastructure families.
- O12. Documentation, decisions and diagrams.

## Observation map

| Observation | What it shows | Files |
| --- | --- | --- |
| O1 default exclusions | deploy/ is skipped by default and reported in `excluded.dirs`; a `.cbmignore` line !deploy/ includes it again (second recording); a PNG is `not_indexed_files` with the reason `ignored-suffix`; a Helm template is `parse_partial` | deploy/edge.yaml, docs/context.png, charts/api/templates/deployment.yaml |
| O2 resource identity | `obj_key` is `<group>/<Kind>/<namespace>/<name>`; cluster-scoped kinds use `_`; a namespaced kind without a namespace uses `?`; one `obj_key` in two files gives two nodes; Kustomize documents have no `obj_key` | k8s/prod/namespace.yaml, k8s/prod/reports.yaml, k8s/overrides/reports.yaml, kustomize/base/deployment.yaml, kustomize/base/kustomization.yaml |
| O3 image identity | One `Image` node for each repository; the tag or digest is only in the edge property `image`; a short Docker Hub name has `registry: unknown`; a digest keeps the form `repo@sha256:...` | k8s/staging/worker.yaml, compose.yaml, k8s/prod/deployment.yaml |
| O4 occurrence merging | Two containers with one image repository and two tags give one `USAGE` edge (`container: sidecar`) | k8s/prod/deployment.yaml |
| O5 reference edges | Each `ref` value of a reference `USAGE` edge, with declared targets; undeclared Secret, ConfigMap, PVC, ServiceAccount, Role subject, Gateway and TLS Secret give no edge; Terraform `USAGE` has `via: terraform` | k8s/prod/deployment.yaml, k8s/prod/ingress.yaml, k8s/prod/httproute.yaml, k8s/prod/rbac.yaml, k8s/prod/pdb.yaml, k8s/prod/hpa.yaml, k8s/prod/networkpolicy.yaml, k8s/staging/deployment.yaml, terraform/main.tf, app/main.go |
| O6 selector joins | A Service, NetworkPolicy and PDB join a workload in another file of the same directory; a workload in k8s/staging/ is not joined; a competing declaration of the same `obj_key` in another directory (k8s/overrides/) is not joined, so the Service joins one of the two; an empty `podSelector` and `matchExpressions` give no edge | k8s/prod/service.yaml, k8s/prod/networkpolicy.yaml, k8s/prod/pdb.yaml, k8s/staging/worker.yaml, k8s/prod/reports.yaml, k8s/overrides/reports.yaml |
| O7 routes | `INFRA_MAPS` kinds `ingress`, `route`, and `Route` nodes without namespace; two environments with one host and path share one `Route` node; an HTTPRoute without hostname has host `*` | k8s/prod/ingress.yaml, k8s/staging/ingress.yaml, k8s/prod/httproute.yaml |
| O8 deployment actions | `DEPLOYS` `via: kubectl` targets a `File`; `via: helm` targets a `Chart`; `via: chart` targets the templated `Resource` | scripts/deploy.sh, charts/api/Chart.yaml, charts/api/templates/deployment.yaml |
| O9 build and image lineage | `DEPENDS_ON` with `via` `from`, `copy-from`, `build`, `depends_on`; `COPY --from=<stage>` gives no edge; a chart dependency has no `via`; only a build command links an `Image` to a Dockerfile | docker/Dockerfile, compose.yaml, scripts/build.sh, charts/api/Chart.yaml |
| O10 configuration | `CONFIGURES` strategies `env_set`, `env_access`, `env_read`, `helm_values`; `EnvVar` nodes carry no value | k8s/prod/deployment.yaml, docker/Dockerfile, compose.yaml, app/main.go, charts/api/values.yaml |
| O11 other families | `CONNECTS_TO` `via: env`; `SPAWNS` from Dockerfile, Makefile and GitHub Actions; `OVERRIDE` `via` `tfvars` and `patch`; a Kustomize `images:` `newTag` gives no edge; `IMPORTS` from kustomization.yaml has no `via`; a templated image gives no `USAGE` edge | compose.yaml, docker/Dockerfile, Makefile, .github/workflows/ci.yaml, terraform/prod.tfvars, kustomize/overlays/prod/kustomization.yaml, kustomize/overlays/prod/patch-replicas.yaml, terraform/main.tf, charts/api/templates/deployment.yaml |
| O12 documentation | `ADR` nodes with `superseded_by`; `SUPERSEDES`; `MENTIONS` and `REFERENCES_FILE`; a Mermaid `Diagram` with `DiagramElement` and `DIAGRAM_ARROW`; `TESTS`, `TESTS_FILE`, and `CALLS` with `lsp_direct` | docs/adr/0001-use-self-hosted-postgres.md, docs/adr/0002-use-managed-postgres.md, docs/architecture.md, app/main.go, app/main_test.go |

## Seeded secrets

| File | Class |
| --- | --- |
| k8s/prod/secret.yaml | Kubernetes Secret `stringData` and base64 `data` |
| k8s/prod/deployment.yaml | container environment value |
| docker/Dockerfile | `ENV` instruction |
| terraform/prod.tfvars | tfvars assignment |
| compose.yaml | Compose service environment |
| scripts/deploy.sh | token in an `export` line |
| certs/dev-key.pem | fake PEM private key |
