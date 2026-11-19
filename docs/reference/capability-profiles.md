# Capability profiles

A capability profile states what evidence Concordia may rely on from one provider build. The profile is data: a YAML file in `internal/observation/profiles/` that is embedded in the binary. It is pinned to the exact provider version and adapter schema. There is no range or prefix match: a rebuilt `0.12.1` has the profile, `0.12.2` has none.

Concordia ships one profile, `cbm-0.12.1`, for codebase-memory-mcp (CBM) `0.12.1` with the adapter schema `concordia-cbm/v2`. It is grounded in the CBM 0.12.1 observations (O1 to O12) and in the committed recordings `test/fixtures/cbm-recordings/infra.json` and `commerce.json`.

## What a profile says

For each family, a profile states:

| Field | Meaning |
|---|---|
| `family` | The family name. A family is a canonical edge kind (the name is the kind), or a property family. |
| `level` | `supported`, `partial`, or `unsupported`. A family that the profile does not list is `unsupported`. |
| `maturity` | `advisory` or `stable`. A rule that depends on an `advisory` family cannot run as `mandatory`. |
| `lossModes` | Known ways in which the provider loses evidence of the family. See [Loss modes](#loss-modes). |
| `nativeEdges` | The native edge type and the discriminators that select the family: property values, properties that must be present or absent, the source file name, and the target node `kind`. |
| `requiredProperties` | Native edge properties that the family needs. |
| `fixtures` | Recordings under `test/fixtures/cbm-recordings/` that validate the family. A `stable` family names at least one, and a test fails when the file is missing. |

The profile has a content digest (`sha256:<hex>` of its canonical JSON, with sets sorted). A change of a level, a loss mode, or any other content changes the digest. `doctor`, the lock (`resolved.cbm.profile`), the run manifest, `results.json`, and the evaluation bundle record the identifier and the digest. When no profile applies, the manifest and `results.json` record `"profile": {"applies": false}`.

The profile is tied to a version number, and the lock can also pin the build itself: `resolved.cbm.sha256` is the digest of the CBM executable file. Two builds of one version can differ, and the profile digest does not tell them apart. `init` and `lock update` write the digest, `doctor`, `results.json` (`toolchain.cbm.sha256`) and the evaluation bundle record it, and a lock whose digest differs from the executable that is found gives the warning `CO-WRK-003`. See [the lock file](workspace.md#concordialock).

## How Concordia uses a profile

- **Version gate.** A build with a profile is `supported` whatever its version number. Without a profile, `0.12.x` is `supported` and supplies the code families only. A version above `0.12` and below `1.0` is `degraded` (`CO-OBS-004` warning), and every other version gives `CO-OBS-004` (exit code 5). Without a profile, every infrastructure, documentation, diagram, and test family is `unsupported`, and a run warns with `CO-OBS-017`.
- **Graph check.** After the graph is frozen, Concordia compares the profile with the edge types and edge properties of the graph schema. A family whose native edge types are all missing is *not present in this graph*; its level stays, and that is not the same as `unsupported`. A `supported` family whose required property is missing is downgraded to `partial`, and a `CO-OBS-017` warning names the property.
- **Regression.** A family that was `supported` in the profile of the lock and is not `supported` now is a capability regression. Obligations that depend on it are `unknown` with the reason `capability-regression`.

## Families of profile `cbm-0.12.1`

All families that are not code families have maturity `advisory`.

### Code families

Fixture: `commerce.json`. These families have maturity `stable`.

| Family | Level | Maturity | Native edge and discriminators | Loss modes |
|---|---|---|---|---|
| `contains` | supported | stable | `CONTAINS_FILE`<br>`CONTAINS_FOLDER` | none |
| `defines` | supported | stable | `DEFINES`<br>`DEFINES_METHOD` | none |
| `imports` | supported | stable | `IMPORTS` | none |
| `inherits` | supported | stable | `INHERITS` | none |
| `implements` | supported | stable | `IMPLEMENTS` | none |
| `calls` | supported | stable | `CALLS` | none |
| `references-callable` | supported | stable | `CALL_REFERENCE` | none |
| `reads` | supported | stable | `READS` | none |
| `writes` | supported | stable | `WRITES` | none |
| `handles-route` | supported | stable | `HANDLES` | none |
| `calls-service` | supported | stable | `HTTP_CALLS`<br>`CROSS_HTTP_CALLS` | none |
| `dispatches-async` | supported | stable | `ASYNC_CALLS`<br>`CROSS_ASYNC_CALLS` | none |
| `emits-event` | supported | stable | `EMITS`<br>`CROSS_CHANNEL` | none |
| `listens-event` | supported | stable | `LISTENS_ON` | none |
| `data-flows` | supported | stable | `DATA_FLOWS` | none |
| `ambiguous-usage` | supported | stable | `USAGE` (no ref) | none |

### Infrastructure, documentation, diagram, and test families

Fixture: `infra.json` where the table lists a native edge and the family is not `unsupported`.

| Family | Level | Maturity | Native edge and discriminators | Loss modes |
|---|---|---|---|---|
| `uses-type` | unsupported | advisory | none | none |
| `constructs` | unsupported | advisory | none | none |
| `calls-grpc` | unsupported | advisory | none | none |
| `calls-graphql` | unsupported | advisory | none | none |
| `calls-trpc` | unsupported | advisory | none | none |
| `deploys-artifact` | supported | advisory | `DEPLOYS` (via=kubectl/helm/chart) | `deploy-target-is-file` |
| `selects-workload` | partial | advisory | `INFRA_MAPS` (kind=selector) | `selector-join-directory-scoped`, `selector-join-one-of-competing`, `empty-selector`, `unsupported-selector-expression` |
| `routes-to` | partial | advisory | `INFRA_MAPS` (kind=ingress/route/httproute) | `route-identity-unscoped` |
| `configured-connection` | supported | advisory | `CONNECTS_TO` (via=env) | none |
| `uses-image` | supported | advisory | `USAGE` (ref=image) | `merged-occurrences` |
| `built-from` | partial | advisory | `DEPENDS_ON` (via=build) | `build-link-needs-build-command` |
| `base-image` | supported | advisory | `DEPENDS_ON` (via=from) | `build-link-needs-build-command` |
| `copies-from-image` | partial | advisory | `DEPENDS_ON` (via=copy-from) | `build-link-needs-build-command` |
| `launches` | supported | advisory | `SPAWNS` | none |
| `overrides` | partial | advisory | `OVERRIDE` (via=tfvars/patch) | `override-unsignalled` |
| `includes-infrastructure` | supported | advisory | `IMPORTS` (via=terraform)<br>`IMPORTS` (source kustomization.yaml/kustomization.yml) | none |
| `infrastructure-dependency` | supported | advisory | `DEPENDS_ON` (via=depends_on)<br>`DEPENDS_ON` (has repository, no via)<br>`USAGE` (via=terraform, ref=var/module/output/resource) | none |
| `placed-in` | supported | advisory | none (derived by Concordia from a workload node; see [Deployment families](#deployment-families)) | none |
| `configures-env-access` | supported | advisory | `CONFIGURES` (strategy=env_access) | none |
| `configures-env-set` | supported | advisory | `CONFIGURES` (strategy=env_set) | none |
| `configures-env-read` | supported | advisory | `CONFIGURES` (strategy=env_read) | none |
| `configures-helm-values` | supported | advisory | `CONFIGURES` (strategy=helm_values) | none |
| `configures-key-symbol` | unsupported | advisory | `CONFIGURES` (strategy=key_symbol) | none |
| `references-secret` | supported | advisory | `USAGE` (ref=env/envFrom/volume, target Secret) | `target-not-declared` |
| `references-configuration` | supported | advisory | `USAGE` (ref=env/envFrom/volume, target ConfigMap) | `target-not-declared` |
| `references-storage` | supported | advisory | `USAGE` (ref=volume, target PersistentVolumeClaim) | `target-not-declared` |
| `references-certificate-secret` | supported | advisory | `USAGE` (ref=tls, target Secret) | `target-not-declared` |
| `uses-runtime-identity` | supported | advisory | `USAGE` (ref=serviceAccountName, target ServiceAccount) | `target-not-declared` |
| `route-attaches-to-gateway` | supported | advisory | `USAGE` (ref=parentRef, target Gateway) | `target-not-declared` |
| `binding-refers-to-role` | supported | advisory | `USAGE` (ref=roleRef, target Role/ClusterRole) | `target-not-declared` |
| `binding-applies-to-principal` | supported | advisory | `USAGE` (ref=subject, target ServiceAccount) | `target-not-declared` |
| `policy-selects-workload` | partial | advisory | `USAGE` (ref=podSelector) | `target-not-declared`, `selector-join-directory-scoped`, `selector-join-one-of-competing`, `empty-selector`, `unsupported-selector-expression` |
| `disruption-policy-target` | partial | advisory | `USAGE` (ref=selector) | `target-not-declared`, `selector-join-directory-scoped`, `selector-join-one-of-competing`, `empty-selector`, `unsupported-selector-expression` |
| `scaling-target` | supported | advisory | `USAGE` (ref=scaleTarget) | `target-not-declared` |
| `supersedes` | supported | advisory | `SUPERSEDES` (syntax=supersedes) | none |
| `mentions` | supported | advisory | `MENTIONS` (syntax=link/code_path/path) | none |
| `references-file` | supported | advisory | `REFERENCES_FILE` (strategy=md_inline_link/md_backtick_path/md_bare_mention) | none |
| `diagram-arrow` | supported | advisory | `DIAGRAM_ARROW` | none |
| `tests` | supported | advisory | `TESTS` | none |
| `tests-file` | supported | advisory | `TESTS_FILE` | none |
| `similar-to` | partial | advisory | `SIMILAR_TO` | none |
| `semantically-related` | partial | advisory | `SEMANTICALLY_RELATED` | none |
| `file-changes-with` | partial | advisory | `FILE_CHANGES_WITH` | none |

### Property families

These families describe properties of the index and the nodes, not one edge kind.

| Family | Level | Maturity | Native edge and discriminators | Loss modes |
|---|---|---|---|---|
| `container-occurrence-versions` | partial | advisory | `USAGE` (ref=image) | `merged-occurrences` |
| `rendered-templates` | partial | advisory | none | `unrendered-template` |
| `index-result` | partial | advisory | none | `default-excluded-directory`, `raster-not-indexed`, `parse-partial` |
| `deployment-workload-set` | supported | advisory | none | `default-excluded-directory`, `parse-partial`, `unrendered-template`, `override-unsignalled` |
| `dockerfile-set` | supported | advisory | none | `default-excluded-directory`, `parse-partial` |
| `documentation-metadata` | supported | advisory | none | none |
| `diagram-identity` | supported | advisory | none | none |

### Deployment families

Three families of `cbm-0.12.1` serve the deployment trace and the rules of the `infrastructure` pack. All three have maturity `advisory`, name the fixture `infra.json`, and are `supported`: a `partial` level would keep a coverage record from ever being `complete`, so their loss modes apply through gaps, and a gap makes the record `partial`.

- **`placed-in`** is a derived family. CBM has no edge for placement, so the family has no native edge. Concordia derives one `placed-in` fact from each workload `Resource` node (Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob, Pod) whose namespace is a name and whose own name is not a template. The fact runs from the workload to its hosting target, the `Namespace` node of the same environment, or a synthesized `Namespace` entity when no manifest of the environment declares it. It has the role `parsed-source-fact`, the resolution `exact`, the confidence `syntactic`, the manifest location of the workload, and a derivation record: the rule `placement-from-namespace`, its version, and the observed ID of the workload node. A `deploys-artifact` fact is never an input, and the fact is not a code kind, so it never enters `reconciliation`. A namespace `?` or a templated name gives no fact and the `unrendered-template` gap stays.
- **`deployment-workload-set`** is recorded for each declared environment. The record is `complete` only when every manifest of the environment sources was read without `parse-partial`, no source is under a default-excluded directory or is a Helm chart or a Kustomize source, no workload name or namespace is unresolved, the index result is available, and the cross-check ran. Otherwise it is `partial` with `default-excluded-directory`, `parse-partial`, `unrendered-template` (a Helm chart, found by the file name `Chart.yaml`), or `override-unsignalled` (a Kustomize source, found by the file name `kustomization.yaml` or `kustomization.yml`), or `unknown`: when the index result is unavailable, when a Compose or Terraform file is in the sources (reason `unsupported deployment source: <path>`), and when a workload document has no provider node. The `placed-in` record of an environment has the status, loss modes, and reasons of the `deployment-workload-set` record of that environment.
- **`dockerfile-set`** is recorded for each source project. A Dockerfile is a file named `Dockerfile` or `Dockerfile.<suffix>`. The record is `complete` only when every Dockerfile is indexed, outside a default-excluded directory, and not `parse-partial`. It is `partial` with `default-excluded-directory` or `parse-partial`, and `unknown` when the index result is unavailable or a Dockerfile is not indexed. It never says whether an image is linked to a Dockerfile: that is the family `built-from` with the loss mode `build-link-needs-build-command`.

**Workloads without a provider node.** CBM returns a workload node only as the endpoint of a fetched edge, so a Deployment with a templated image and no reference can have no node. The manifest cross-check reads every workload document (kind, name, namespace, environment, and location, never a value) and lists it in `CrossCheckResult`. A document that has no provider node makes `deployment-workload-set` and `placed-in` `unknown` for its environment with the reason `workload declared in <path> has no provider node`, so a missing placement is never a definite absence. A document in a default-excluded directory or in a partly parsed file adds no such reason: its loss mode already explains the missing node.

## Profiles and the claim registry

A profile describes what the provider supplies. The claim registry is separate: it lists the claims that a rule can need and the contract that enables each one (an adapter or an evaluator). The registry is owned by Concordia, not by the provider build, so registering a contract never changes a profile digest, and a profile never enables a claim. `concordia doctor` shows the registry digest and its contracts next to the profile (see [doctor](cli.md#doctor)). The registry digest is recorded in `toolchain.assurance.claimsRegistryDigest` of a result. See [Assurance](assurance.md#claims-and-contracts) for the claims.

## Loss modes

| Loss mode | Observation | Meaning |
|---|---|---|
| `default-excluded-directory` | O1 | CBM skips `deploy/`, `deployments/`, `build/`, `dist/`, `node_modules/`, and `target/` unless a `.cbmignore` line such as `!deploy/` includes them. `index_repository` reports them in `excluded.dirs`. |
| `raster-not-indexed` | O1 | Raster images are not indexed (`not_indexed_files`, reason `ignored-suffix`). A raster diagram gives no evidence. |
| `parse-partial` | O1 | A file is parsed in part only (`parse_partial`, with line ranges), for example a Helm template. |
| `merged-occurrences` | O4 | Edges are unique by source, target, and type. Containers of one workload that use one image repository give one edge, and the graph does not show the lost occurrence. |
| `target-not-declared` | O5 | A reference edge exists only when the target resource is declared in the indexed project. The graph gives no signal for a dangling reference. |
| `selector-join-directory-scoped` | O6 | A selector joins workloads of the same directory only. |
| `selector-join-one-of-competing` | O6 | When two files declare the same object, the selector joins one of them. |
| `empty-selector` | O6 | An empty selector gives no edge, and no property records the selector. |
| `unsupported-selector-expression` | O6 | A `matchExpressions` selector gives no edge. |
| `route-identity-unscoped` | O7 | A `Route` node has no file or namespace in its qualified name, so two environments with one host and path share a node. |
| `deploy-target-is-file` | O8 | `kubectl apply -f` gives an edge to the file, not to the resources in it. |
| `build-link-needs-build-command` | O9 | An image to Dockerfile link exists only when a build command (a script with `docker build -t`, or Compose `build:`) names the image. `COPY --from=<stage>` has no edge. |
| `override-unsignalled` | O11 | A Kustomize `images:` override gives no edge. |
| `unrendered-template` | O11 | Helm templates stay literal: a templated name keeps the template text, and a templated image reference gives no edge. |

## Unsupported families

The following families are listed as `unsupported` because the 0.12.1 recordings show no native edge for them:

- `configures-key-symbol` (mapped but with no fixture),
- `uses-type` and `constructs` (CBM has no native edge type),
- `calls-grpc`, `calls-graphql`, and `calls-trpc`.

## Add or change a profile

1. Record the provider with `mise run record-cbm` and review the golden `test/golden/infra/native-discriminators.json`.
2. Write `internal/observation/profiles/<provider>-<version>.profile.yaml`. A profile file has no `digest` key: the loader computes it.
3. Run `go test ./internal/observation/...`. The tests check that every family exists, that each loss mode of the observations appears, that every named fixture exists, and that the discriminator values of the profile are those that the recording shows.
4. Review the digest change in the golden files that record the lock.
