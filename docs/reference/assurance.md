# Assurance reference

The assurance adapters give Concordia evidence that code analysis cannot give: manifests that Concordia renders itself, plans, signed statements about builds and deployments, check results, and quality scenarios. This page lists what each adapter claims and what it does not claim, and the reasons that an obligation is `unknown` when evidence is missing. The workspace key that declares the evidence is [`assurance`](workspace.md#assurance).

Concordia reads this evidence in a trusted pipeline. It does not defend against a workspace that an attacker controls.

## Claims and contracts

A claim is a statement that a rule can need. Each claim is enabled by one contract: the contract of the adapter or evaluator that can support it. The registry is owned by Concordia and is not part of the provider [capability profile](capability-profiles.md): registering a contract never changes the profile digest. `concordia doctor` lists the contracts, and the run manifest records the digest of the registry.

A rule that needs a claim that no loaded contract enables is `unknown` with the reason `claim-not-enabled`. It never passes. Every contract is `experimental` for now: the severity of a rule that needs one of its claims is capped at `warning`, until the fixture suite of the contract is listed as passing in the registry.

| Contract | Kind | Enabled claims | Not enabled | Experimental |
|---|---|---|---|---|
| `cloud-firewall` (1.0.0) | evaluator | `cloud-network-isolation` | `network-isolation`, `effective-encryption-in-transit`, `cloud-iam`, `routing-reachability` | yes |
| `network-policy` (1.0.0) | evaluator | `network-isolation` | `effective-encryption-in-transit`, `cni-specific-policy-evaluation`, `rbac-grants` | yes |
| `plan` (1.0.0) | adapter | `planned-resources` | `deployment-succeeded`, `runtime-drift`, `cloud-iam-evaluation`, `encryption-evaluation` | yes |
| `provenance` (1.0.0) | adapter | `exact-lineage`, `deployed-digest`, `deployment-succeeded`, `runtime-drift` | `slsa-level`, `absence-of-vulnerabilities`, `absence-of-malicious-code`, `correct-runtime-behavior` | yes |
| `quality` (1.0.0) | adapter | `quality-scenario-verdict` | `architecture-meets-quality-attribute` | yes |
| `rbac` (1.0.0) | evaluator | `rbac-grants` | `effective-authorization`, `cloud-iam`, `network-isolation` | yes |
| `rendering` (1.0.0) | adapter | `complete-resource-set`, `container-occurrence-versions`, `resolved-selections` | `deployment-succeeded`, `network-isolation`, `exact-lineage`, `runtime-behavior` | yes |
| `verification` (1.0.0) | adapter | `check-outcome` | `test-adequacy`, `code-coverage`, `absence-of-defects` | yes |

Each enabled claim needs evidence of a stated level, a complete input, and a minimum integrity:

| Claim | Contract | Evidence level | Completeness | Integrity at least |
|---|---|---|---|---|
| `complete-resource-set` | `rendering` | `rendered-or-evaluated` | `complete` | content verified by digest |
| `container-occurrence-versions` | `rendering` | `rendered-or-evaluated` | `complete` | content verified by digest |
| `resolved-selections` | `rendering` | `rendered-or-evaluated` | `complete` | content verified by digest |
| `planned-resources` | `plan` | `rendered-or-evaluated` | `complete` | producer authenticated |
| `exact-lineage` | `provenance` | `rendered-or-evaluated` | `complete` | producer authenticated |
| `deployed-digest` | `provenance` | `deployment-execution` | `complete` | producer authenticated |
| `deployment-succeeded` | `provenance` | `deployment-execution` | `complete` | producer authenticated |
| `runtime-drift` | `provenance` | `runtime-observation` | `complete` | producer authenticated |
| `network-isolation` | `network-policy` | `rendered-or-evaluated` | `complete` | content verified by digest |
| `cloud-network-isolation` | `cloud-firewall` | `rendered-or-evaluated` | `complete` | content not verified |
| `rbac-grants` | `rbac` | `rendered-or-evaluated` | `complete` | content verified by digest |
| `check-outcome` | `verification` | `not-applicable` | `complete` | producer authenticated |
| `quality-scenario-verdict` | `quality` | `not-applicable` | `complete` | content not verified |

The levels are those of the [deployment trace](../how-to/trace-a-deployment.md). `not-applicable` marks a claim that is not about a level of the trace. The integrity levels are ordered: `content not verified`, `content verified by digest`, `producer authenticated`. A `quality-scenario-verdict` in the `measured` mode also needs an admissible measurement statement, which is checked as a statement and not through this table.

## Rendering

Concordia renders Helm charts and Kustomize overlays itself and reads plain manifests. The evidence is a function of the committed sources and the pinned renderer. It parses the rendered YAML itself: a rendered Deployment with two containers that use one image repository at two tags gives two container occurrences. A container, an init container, and an ephemeral container each give one occurrence, with the repository, tag, and digest of the image kept apart. A tag is shown as written and is never a version.

Concordia reads Pod, Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, and CronJob; Service, Ingress, and routes; NetworkPolicy; ServiceAccount, Role, ClusterRole, RoleBinding, and ClusterRoleBinding. It lists every other kind by identity only. Helm hooks stay in the output as resources and never run. A resource keeps its scope: a cluster-scoped kind has no namespace, and the same name in two environments gives two resources.

Rendering is declared per environment under [`assurance.rendering`](workspace.md#assurancerendering). A render is evidence of the `rendered-or-evaluated` level. It never supports `deployment-succeeded`, `network-isolation`, `exact-lineage`, or a claim about runtime behavior.

### Tools and lock pins

| Tool | Runs | Needs a pin |
|---|---|---|
| `plain` | no program. Concordia reads the `.yaml`, `.yml`, and `.json` files of the path. | no |
| `helm` | `helm template --include-crds [--namespace <ns>] [--values ./<file> ...] <release> ./<chart>`. The release is `release`, else the name of the chart directory. | yes |
| `kustomize` | `kustomize build ./<path>`. | yes |

A `helm` or `kustomize` source needs its renderer in the lock file. `concordia lock update` writes `resolved.renderers.<tool>` with the version that the tool reports and the `sha256` of its executable, and removes the pin of a tool that no source uses. The renderer is found through the `HELM` and `KUSTOMIZE` environment variables, else on `PATH`, as `lock update` and `doctor` find it. A renderer that is missing, not pinned, or different from its pin stops rendering with `CO-PRV-003` (exit 5). See [the lock file](workspace.md#concordialock).

The supported tool versions of the contract are Helm 4 and Kustomize 5.

### Confinement

A renderer is a program that runs on this machine, and a chart or an overlay is input that Concordia does not trust. Concordia confines the render in seven layers. The first six always apply. The seventh applies when the machine can provide it.

| Layer | What it does | What it does not do |
|---|---|---|
| 1. Fixed arguments | Concordia builds the command line from a template. No flag comes from the workspace. It never passes `--dependency-update`, `--enable-helm`, `--enable-exec`, `--enable-alpha-plugins`, or `--post-renderer`. A release name or a namespace that is not a plain name is refused. | It limits what Concordia asks of the renderer. It does not limit what the renderer does with that request. |
| 2. Pinned executable | The renderer is compared with the pin of the lock file: version and `sha256` of the executable. | It identifies the program. It does not say that the program is safe. |
| 3. Scrubbed environment | The renderer starts with a short, fixed environment. Proxies, `KUBECONFIG`, `HELM_*` and `XDG_*` settings of the user, cloud credentials, and tokens are not passed. `HOME` and the cache, config, data, and temporary directories point into a directory that Concordia creates and removes. | It removes ambient credentials from the environment. It does not stop a process from reading a file that the user can read. |
| 4. Confined copy | The declared paths are copied into a temporary directory (mode `0700`) and the renderer runs there. A symbolic link anywhere on the path is rejected and never read. A path that is absolute or leaves the workspace root is rejected. A Kustomize overlay brings in the local bases and components that it names, and nothing else. | The copy is the working directory. Without layer 7 it is not a wall: a process can open a file by its absolute path. |
| 5. Pre-scan | Before any program starts, Concordia reads the chart and kustomization files for remote bases, remote chart repositories, unvendored chart dependencies, exec generators, KRM functions, `helmCharts` inflation, and post-renderer requests. A source that has one is not rendered. Each one is a gap and a warning. | It recognizes the listed constructs. It is a check of known fields, not an analysis of everything that a template can do. |
| 6. Limits | A time limit, a cap on the output bytes, and a cap on the number of resources. | It bounds the cost of a render. It does not bound what the renderer reads. |
| 7. OS sandbox | On Linux with `bwrap` (bubblewrap) and user namespaces, the renderer runs in new network, PID, IPC, and UTS namespaces with no network interface. Only `/usr`, `/bin`, `/sbin`, `/lib`, `/lib64`, the renderer, and the input copy are bound, all read-only. The renderer's `HOME` is the only writable bind. `/etc`, the home directory of the user, and `/tmp` are not bound. | It is Linux only. It does not defend against a flaw in the kernel or in bubblewrap. |

Layers 1 to 6 reduce what Concordia passes to the renderer and what it can ask for. They do not deny network access to a renderer process. Only layer 7 does. Confinement lowers the risk of running a renderer and does not remove it.

Concordia records the confinement level of each render in `assurance.rendered[]`:

| Level | Meaning |
|---|---|
| `none` | The source is `plain`. No program started. |
| `process` | The renderer ran under layers 1 to 6. The OS sandbox was not available. |
| `process+os` | The renderer ran under layers 1 to 7. |

Concordia looks for `bwrap` and runs a trial with the same namespaces before it uses the sandbox. With [`assurance.rendering.requireOsSandbox`](workspace.md#assurancerendering) set to `true`, a machine without a usable sandbox stops the run with `CO-PRV-004` (exit 5), before a renderer starts. Without it, the render runs at level `process`.

### Limits and coverage

A render has the coverage `complete`, `partial`, or `failed`. A `failed` source has no resources: a partial set is never used as complete evidence.

| Case | Coverage | Gap kind | Diagnostic |
|---|---|---|---|
| Time over `limits.timeoutSeconds` | `failed` | `time-limit` | `CO-PRV-005` |
| Output over `limits.maxOutputBytes` | `failed` | `output-limit` | `CO-PRV-005` |
| More than `limits.maxResources` resources | `failed` | `resource-limit` | `CO-PRV-005` |
| Renderer exits with an error | `failed` | `render-error` | `CO-PRV-005` |
| Pre-scan finds a remote base, a remote chart repository, an unvendored dependency, an exec generator, a KRM function, `helmCharts`, or a post-renderer | `failed` | `remote-base`, `remote-chart-repository`, `unvendored-dependency`, `exec-generator`, `krm-function`, `helm-chart-inflation`, `post-renderer` | `CO-PRV-006` |
| Declared path missing, a symbolic link, a special file, or a path that leaves the root | `failed` | `missing-source`, `symlink`, `special-file`, `path-escape` | `CO-PRV-005` |
| Document that cannot be parsed, a resource that is not valid, a selector or an image that cannot be read | `partial` | `unparseable-document`, `invalid-resource`, `unsupported-selector`, `unparseable-image` | `CO-PRV-005` |
| Two documents with one identity | `partial` | `duplicate-identity` | `CO-PRV-007` |
| Interrupted run | `failed` | `interrupted` | none. The run exits 130. |

Concordia never cuts output to fit a limit. Environments are independent: one that fails leaves the others complete, and a rule that needs the failed environment is `unknown`. The rendering contract also states these limits:

- Only the committed sources are rendered. Values that exist only in the cluster are not known.
- A remote base or chart repository, an exec generator, a KRM function, and a post-renderer are gaps. Concordia never runs them.
- A render over a limit (`assurance.rendering.limits`) is `partial` or `failed`. Concordia never cuts the output to fit.
- The pinned renderer is a program that runs on this machine. Confinement lowers that risk and does not remove it.

### Determinism

The output digest covers the redacted canonical form of every rendered document. The same sources, values, and pinned renderer give the same digest, and so the same results. A Helm template that calls a random or time-dependent function, for example `randAlphaNum` or `now`, does not.

A run renders each source once. [`concordia assurance validate`](cli.md#assurance-validate) renders each source twice and compares the coverage and the output digests. When they differ it gives `CO-PRV-014`, which names the environment, the two digests, and up to five resources that differ.

### Redaction

Rendered text passes through the [redaction contract](redaction.md) before Concordia reads it. The values of a Secret `data` and `stringData`, and any other value under a key that the contract treats as sensitive, are replaced by a marker. The rendered output, the digest, `results.json`, the run manifest, and the bundle never hold the value. Concordia keeps the name of the Secret, the key names (`dataKeys`), and one redaction record for each removed value. The record names the property and the class (`secret-data`) and never holds the value.

## Plans

A Terraform or OpenTofu plan (`show -json`) is ingested as a file. Concordia does not run `plan`: planning needs providers, state, and credentials. Plans are declared under [`assurance.plans`](workspace.md#assuranceplans).

Concordia reads the resource changes of the plan by address, with the planned values after apply. It accepts `format_version` `1.0`, `1.1`, and `1.2`. Any other version, a file that is not a plan (a state file, for example), a plan that errored, and a change without an address give `CO-PRV-008`, and no part of the plan is used. A plan that Terraform marks incomplete (deferred changes) has the coverage `partial` and the gap `incomplete-plan`. A missing or unusable plan file is the gap `plan-file-missing` or `plan-unusable`.

An attribute that is unknown until apply is the gap `unknown-after-apply`, one for each attribute. Concordia never guesses a value. An attribute that the plan marks sensitive is replaced by a redaction marker and recorded as a redaction. Concordia reads no other copy of a value (`variables`, `planned_values`, `prior_state`), because those copies carry no marker for each attribute. Of the values before a change (`change.before` of a resource change), Concordia reads only the owner of a firewall rule resource that the plan deletes or replaces: `security_group_id`, `network_security_group_name`, and `resource_group_name` of `aws_security_group_rule`, `aws_vpc_security_group_ingress_rule`, `aws_vpc_security_group_egress_rule`, and `azurerm_network_security_rule`. The redaction rules apply to them too: a sensitive owner value is left out and recorded as a redaction. Of the `configuration` section, Concordia reads the `references` lists, the names of the attribute and block keys that the configuration sets for each resource (including the keys inside nested blocks), and the `provider_config_key` of each resource. The lists hold resource addresses, not values, and they tell the [`cloud-firewall`](#cloud-firewall) evaluator which resource an unknown attribute will point to. The key names tell it which unknown attributes the provider computes only because the configuration does not set them. The provider configuration tells it which resources share a subscription or a default project. Concordia never reads `constant_value`. A `dynamic` block does not appear in the configuration of a plan, so a block key that is absent may still be set. A CALM node can bind to a plan address with an [`infraResource` binding](bindings.md), and the trace then shows the `rendered-or-evaluated` level.

### Binding to a revision

A plan is a prediction made at some time for some revision. It supports a mandatory claim only when a `plan` statement binds it, with these checks, all collected as admissibility details:

- The statement subject names the `sha256` digest of the plan file. Another digest is `integrity-failure` and `CO-PRV-009`.
- The signature verifies against a producer that is trusted for the claim kind `plan`.
- The predicate's `revision` equals the evaluated revision (`different-revision`) and its `environment` equals the environment of the plan (`different-environment`).
- The `time` of the predicate is within `maxAgeDays` (`stale`). The tree is the evaluated state (`subject-not-evaluated-state`).

The statement binds the digest of the file. It does not say that the plan content is right. A plan without a statement is `content not verified` with the detail `not-authenticated`, and it supports no mandatory claim: a rule that needs it is `unknown` with the reason `no-evidence-supplied`. The plan is still shown. The predicate is described by [`evidence-plan.schema.json`](evidence-plan.schema.json).

### Limits

- A plan states what would change, not what was applied. It is evidence of the `rendered-or-evaluated` level and never of the `deployment-execution` level, so it never supports `deployment-succeeded`.
- The `plan` contract enables the claim `planned-resources` and nothing about cloud IAM or encryption.
- The plan binding checks the file digest and the statement. It does not run Terraform or check the plan against the state of the cloud.

## Statements

Evidence that other systems produce arrives as an in-toto Statement v1. Concordia verifies the signature with cosign against a producer that the workspace declares under [`assurance.producers`](workspace.md#assuranceproducers), and compares the subject digest itself. A statement that is malformed, fails verification, or does not match its subject is a warning (`CO-PRV-009`), and the claims that depend on it are `unknown`. The other files of the same glob are still used.

### Forms

Only the statement type `https://in-toto.io/Statement/v1` is read. A file has one of three forms:

| Form | Signature | Integrity |
|---|---|---|
| Bare in-toto statement | none | At most `content verified by digest`. Detail `not-authenticated`. |
| DSSE envelope | cosign cannot verify an envelope without a Sigstore bundle, so it counts as unsigned. | The same as a bare statement. |
| Sigstore bundle with a DSSE envelope | cosign verifies it against the declared producers. | `producer authenticated` when a producer verifies and the subject digest does not differ. |

The DSSE payload type is `application/vnd.in-toto+json`. A signed statement has to be a Sigstore bundle. The Concordia predicates are open: a field that Concordia does not list is ignored.

### Predicate types

Each predicate type is attested by one claim kind. A producer is trusted for the claim kinds that it lists and no others.

| Predicate type | Claim kind | Statement subject | JSON Schema |
|---|---|---|---|
| `https://slsa.dev/provenance/v1` | `build-provenance` | The image digest (`sha256`). Read as is. | none: the SLSA specification defines it |
| `https://concordia.io/evidence/deployment-record/v1` | `deployment-record` | The image digest of the record. | [`evidence-deployment-record.schema.json`](evidence-deployment-record.schema.json) |
| `https://concordia.io/evidence/workload-observation/v1` | `workload-observation` | The image digest of the observation. | [`evidence-workload-observation.schema.json`](evidence-workload-observation.schema.json) |
| `https://concordia.io/evidence/check-result/v1` | `check-result` | The revision (`gitCommit`) or an artifact. | [`evidence-check-result.schema.json`](evidence-check-result.schema.json) |
| `https://concordia.io/evidence/measurement/v1` | `measurement` | The revision (`gitCommit`) or an artifact. | [`evidence-measurement.schema.json`](evidence-measurement.schema.json) |
| `https://concordia.io/evidence/plan/v1` | `plan` | The `sha256` digest of the plan file. | [`evidence-plan.schema.json`](evidence-plan.schema.json) |

The schemas use JSON Schema draft 2020-12. The `$id` of each is its predicate type URL with `.schema.json` appended. A statement needs a non-empty `subject`, and every subject needs a digest.

For a SLSA statement Concordia keeps the subjects, `buildDefinition.buildType`, the resolved dependencies, `runDetails.builder.id`, the invocation id, and the start and finish times. It requires `buildDefinition.buildType` and `runDetails.builder.id`. It ignores any field that states a SLSA level, and no output states one. It does not keep the external, internal, and system build parameters or the byproducts, which can hold secrets.

### Producers and the trust contract

A producer has an id, the claim kinds that it may attest, and one authentication method:

| Method | Fields | cosign flags |
|---|---|---|
| `cosign-key` | `key`: a workspace-relative public key file, or a KMS or `env://` reference | `--key` |
| `cosign-keyless` | `issuer` and `identity` | `--certificate-oidc-issuer` and `--certificate-identity` |

A producer without an authentication method is `CO-PRV-001` (exit 2). Concordia tries the producers that are trusted for the claim kind of the statement first, then the others in id order. A signature that a producer verifies but that producer may not attest for this kind is `producer-not-trusted-for-claim`. The trust contract is these declarations, and the builders and source repositories of [`assurance.provenance`](workspace.md#assuranceprovenance). Concordia runs in a trusted pipeline. The contract protects against evidence files that were changed or that come from a producer you did not declare. It does not protect against a change to the workspace file itself. The model behind this limit is in [Evidence and trust](../explanation/evidence-and-trust.md).

### Verification

For a Sigstore bundle Concordia runs `cosign verify-blob-attestation --bundle <file> --type <predicate type> --check-claims=false` with the signer flags above, and `--trusted-root` when [`assurance.verification.trustedRoot`](workspace.md#assuranceverification) is set. cosign checks the signature, the signer, and the predicate type. Concordia compares the subject itself. The result has one signature state:

| Signature | Meaning | Admissibility detail |
|---|---|---|
| `none` | A bare statement or a DSSE envelope. | `not-authenticated` |
| `no-producer` | A signed bundle, and the workspace declares no producer. | `producer-not-trusted` |
| `unavailable` | Verification needs the network, and `--offline` is set with no `trustedRoot`. cosign was not started. | `offline-verification-unavailable` |
| `failed` | cosign ran and no declared producer verified the signature. A changed file and an undeclared signer look the same. | `integrity-failure`, and `CO-PRV-009` |
| `verified` | A declared producer verified the signature. | none, or `producer-not-trusted-for-claim` |

The integrity level follows from the signature and the subject check. `producer authenticated` needs a verified signature and a subject that does not differ. `content verified by digest` needs a subject that matches and no verified signature. Everything else is `content not verified`.

The subject check compares the statement with what it names. A plan statement must name the digest of the plan file. A deployment record and an observation must name the digest in the predicate. A difference is `integrity-failure` and `CO-PRV-009`. A provenance statement is linked to an occurrence by its subject digest. A check result and a measurement have no artifact to compare, and the evaluated revision decides.

cosign 3 verifies a bundle only with network access to the Sigstore trusted root, or with a local trusted root file. With `--offline`:

- When `assurance.verification.trustedRoot` is set, Concordia passes it to cosign and verification runs without a network call.
- When it is not set, cosign does not start. The statement stays below `producer authenticated`, with the detail `offline-verification-unavailable`. The claim that needs it is `unknown`. This is not an error, unlike signing under `--offline` (`CO-WRK-016`).

Without `--offline`, a missing `cosign` stops the run with `CO-WRK-014` (exit 5), only when a verification has to run.

### Admissibility

One engine decides whether a statement can support a claim. It collects every reason, not only the first, and uses the evaluation time of the run, which the manifest records. A statement with one or more details is inadmissible, and the obligation that depends on it has the reason `no-evidence-supplied`. `CO-PRV-010` (info) lists the details.

| Detail | Meaning |
|---|---|
| `producer-not-trusted` | No declared producer verified the signature, or the workspace declares none. |
| `producer-not-trusted-for-claim` | A producer verified it and is not trusted for this claim kind. |
| `integrity-failure` | The signature failed, or the subject digest differs from what the statement names. |
| `not-authenticated` | The statement has no signature that can be verified. |
| `different-revision` | The revision of the statement is not the evaluated revision. |
| `different-environment` | The environment of the statement is not the environment of the obligation. |
| `stale` | The statement is older than the configured `maxAgeDays`. |
| `failed-run` | A measurement whose outcome is not `passed`. |
| `invalid-outcome` | An outcome that is not one of the allowed values. |
| `threshold-mismatch` | A measurement states another threshold or comparison than the scenario. |
| `unit-mismatch` | A measurement states another unit than the scenario. |
| `subject-not-evaluated-state` | The working tree has uncommitted changes, so a statement bound to a commit is not about the state that is evaluated. |
| `offline-verification-unavailable` | See [Verification](#verification). |
| `missing-field:<name>` | A required field is absent, for example `missing-field:environment` or `missing-field:workload.namespace`. A field that is `null` or an empty string is missing. An empty list is not. |

`maxAgeDays` has one value for each source: [`assurance.plans[]`](workspace.md#assuranceplans), [`assurance.provenance`](workspace.md#assuranceprovenance), [`assurance.records`](workspace.md#assurancerecords), and [`assurance.verification`](workspace.md#assuranceverification). A measurement uses the `maxAgeDays` of its scenario. Without it, age is not checked. When it is set, the statement needs a time (`missing-field:time` otherwise).

### Persisted form

Concordia persists and bundles only the fields that the contract of each predicate lists, and the statement digest. Everything else is removed, and each removal is recorded. A token in the external parameters of a provenance statement never reaches the bundle. The bundle copy is `assurance/statements/<sha256>.json`. See [redaction](redaction.md).

### assurance validate

[`concordia assurance validate`](cli.md#assurance-validate) loads every declared input as `check` does and prints, for each one, its kind (`rendering`, `plan`, `plan-statement`, `build-provenance`, `deployment-record`, `workload-observation`, `check-result`, `measurement`, or `quality-scenarios`), its integrity level, whether it is admissible, and the reasons. It prints the coverage of each rendered environment, renders each source twice, evaluates no rule, and writes no file. It exits 2 for an invalid declaration, 5 for a missing renderer or a required sandbox that is not available, and 0 otherwise: a stale statement is printed as inadmissible with `stale` and exits 0.

## Provenance

The provenance contract enables exact lineage within the trust contract that the workspace declares under [`assurance.provenance`](workspace.md#assuranceprovenance) and [`assurance.producers`](workspace.md#assuranceproducers). It enables four claims and does not enable the others:

| Enabled | Not enabled |
|---|---|
| `exact-lineage`, `deployed-digest`, `deployment-succeeded`, `runtime-drift` | `slsa-level`, `absence-of-vulnerabilities`, `absence-of-malicious-code`, `correct-runtime-behavior` |

A rule that needs a claim that is not enabled is `unknown` with the reason `claim-not-enabled`. It never passes.

### Exact lineage

The claim is evaluated for each container occurrence. It is `pass` only when all of these hold, and the result names the trust contract by id and digest:

- The occurrence has an immutable digest. A tag is not a version: an image with a tag only has no lineage, and Concordia does not link it to a build.
- An admissible provenance statement with integrity `producer authenticated` names that digest as a subject. Its producer is trusted for `build-provenance`.
- The builder of the statement (`runDetails.builder.id`) is in `builders`. A declared builder matches the id exactly, and also the id with a Git reference appended (`<builder>@refs/heads/main`), unless the declared builder has a reference itself.
- One resolved dependency of the statement is a Git source that matches an entry of `sourceRepositories`. Concordia reduces the URI to `host/owner/repo`, in lower case, without the scheme, user, reference, and `.git`. An entry matches when its segments equal the last segments of that path, so `shop`, `acme/shop`, and `github.com/acme/shop` all match `github.com/acme/shop`. Package URLs (`pkg:oci/...`) are never sources.

| Verdict | When | Reason | Missing |
|---|---|---|---|
| `pass` | All of the above. | none | none |
| `violation` | An authenticated statement names the digest and its builder is not in the contract. The builder is the observed evidence. | none | none |
| `unknown` | The digest is missing. | `no-evidence-supplied` | `digest`, `provenance` |
| `unknown` | No admissible statement names the digest. A statement that names another digest is not linked at all. | `no-evidence-supplied` | `provenance` |
| `unknown` | An authenticated statement names the digest, its builder is trusted, and no resolved dependency is a declared source. | `no-evidence-supplied` | `source-repository` |
| `unknown` | The environment has no rendered source, or its render is not complete, so its occurrences are not all known. | `rendered-evidence-missing` | none |

With several statements, one that passes wins, then a violation, then the first unknown in path order. A statement that names the digest and cannot support the claim (tampered, unsigned, from a producer that may not attest build provenance, stale) is listed as rejected, with its details. A static link from an image to a Dockerfile or a CI definition is a lineage candidate. In the deployment trace the image-to-build step is `verified` only when the lineage is `pass`, `candidate` when only a static link exists, and `unknown` otherwise.

### Deployment records and observations

A deployment record (evidence level `deployment-execution`) and a workload observation (level `runtime-observation`) are about an occurrence when they name the same namespace, workload kind, workload name, and container, and the same cluster when both state one. Only admissible statements of the environment of the occurrence count. Records and observations stay separate facts: the rendered digest, the record, and the observation.

| Claim | Verdict |
|---|---|
| `deployment-succeeded` | `pass` only from an admissible record whose outcome is `succeeded`. `violation` from an admissible record whose outcome is `failed`. Otherwise `unknown` with `no-evidence-supplied`. |
| `deployed-digest` | `pass` when the newest admissible `succeeded` record binds a digest and the rendered image does not name another one. Another digest, or records of the same time that name different digests, is `unknown` with `evidence-conflict`. A tag in the manifest and a record that names its digest link the occurrence to that digest. |
| `runtime-drift` | `pass` when the newest admissible observation names the same digest as every source it is compared with: the rendered digest and the `succeeded` records. `violation` when it differs from one: each finding names both digests and both sources (`rendered`, `deployment-record`, `workload-observation`). `unknown` without an admissible observation, or with nothing to compare it with. |

Of the admissible records, the newest decides, and an older record never overrides it. The same holds for observations. An observation older than `assurance.records.maxAgeDays` is `stale` and gives no drift. A drift finding never chooses between its two sources.

Concordia reports no SLSA level and makes no claim about vulnerabilities, malicious code, or runtime behavior. The predicates are described by [`evidence-deployment-record.schema.json`](evidence-deployment-record.schema.json) and [`evidence-workload-observation.schema.json`](evidence-workload-observation.schema.json). The results are in [`assurance.lineage`](results.md#lineage). The pack rules `ASR-PRV-001` and `ASR-PRV-002` are in [assurance rules](assurance-rules.md).

## Evaluators

The built-in evaluators compute an effective result from evidence that Concordia reads itself: `network-policy` for `networking.k8s.io/v1` NetworkPolicy and `rbac` for roles and bindings read the rendered manifests, and `cloud-firewall` reads the firewall rules of a Terraform or OpenTofu [plan](#plans). Selection edges and role references from the code graph are never evidence of isolation or access: the code graph has no edge for an empty `podSelector` or a `matchExpressions` selector. An evaluator runs for the environments that you list under [`assurance.evaluators`](workspace.md#assuranceevaluators). A rule asks for a result with `require.effectiveAccess`, which [policies](policies.md) describes. Each evaluator has a version, which results and digests record, and a contract that `concordia doctor` lists.

### network-policy

The evaluator answers whether traffic from one workload to another, on one port and protocol, is `allowed`, `denied`, or `unknown`:

- A workload that no policy selects for a direction (ingress or egress) is open for that direction. Selection uses `podSelector` with `matchLabels` and `matchExpressions`. An empty `podSelector` selects the namespace. `policyTypes` decides which directions a policy governs.
- The policies that select a workload form a union: a rule of any of them allows the traffic. Adding an allow rule never turns `allowed` into `denied`.
- A peer is a pod selector, a namespace selector, both, or an `ipBlock`. An `ipBlock` is evaluated only for addresses outside the pod network. Concordia does not know the pod and service ranges of the cluster, so an `ipBlock` that a workload-to-workload query depends on is cluster-dependent and the result is `unknown`.
- Ports use the protocol (TCP by default), a number or a name, and `endPort`. A named port resolves against the container ports of the destination.
- Traffic needs every direction to allow it. One denied direction denies it, even when the other is `unknown`. `unknown` beats `allowed`.

### rbac

The evaluator answers whether a subject is `granted` a verb on a resource, with the bindings and rules that grant it:

- It reads Role, ClusterRole (with aggregation rules), RoleBinding, and ClusterRoleBinding, with subjects of kind ServiceAccount, User, and Group.
- It supports wildcard verbs, resources, and API groups, subresources, `resourceNames`, and `nonResourceURLs`.
- A binding that refers to a role that is not in the input is a missing role target and makes the result `unknown`, unless another binding grants the permission.

For `require.effectiveAccess` with the dimension `authorization`, the subject is the ServiceAccount of the `from` element and the object is each rendered resource that the `to` element is bound to. Access exists when any of `get`, `list`, `watch`, `create`, `update`, `patch`, and `delete` is granted.

### cloud-firewall

The evaluator answers whether TCP or UDP traffic from one planned cloud resource to another, on one port, is `allowed`, `denied`, or `unknown`. It reads the firewall rules of the plans of the environment, declared under [`assurance.plans`](workspace.md#assuranceplans). It does not run Terraform, and it does not read the `.tf` files. It supports AWS security groups, Azure network security groups (NSGs), and GCP VPC firewall rules. The prefix of the resource type selects the provider: `aws_`, `azurerm_`, or `google_`.

You choose the evaluator for each pair of a rule with your bindings:

| The two CALM elements bind to | Evaluator |
|---|---|
| planned resources of the environment ([`infraResource`](bindings.md)), both | `cloud-firewall` |
| rendered workloads, both | `network-policy` |
| a planned resource and a rendered workload | none: `unknown`, `binding-gap`, "the pair mixes planned cloud resources and Kubernetes workloads" |

When only one element binds to a planned resource and the other binds to nothing in the environment, Concordia uses `cloud-firewall`, and the result is a `binding-gap` for the unbound element. An element can bind to a planned resource and to a rendered workload at once. Then the first row that fits decides: both elements planned gives `cloud-firewall`, else both elements workloads gives `network-policy`, because the path stays inside the cluster, else the pair is mixed. A pair of `cloud-firewall` does not need rendered manifests. The `infraResource` binding decides the evaluator, not the plan. A missing or unreadable plan gives `rendered-evidence-missing`, and a bound address that the plan does not hold gives `rendered-evidence-missing` with the gap `workload-not-in-input`, which names the address. A binding to the configuration address of a resource with several instances reads only the instances that exist after apply; an instance that the plan destroys is not queried.

#### Endpoints

An endpoint is the plan address of a resource. The evaluator finds the rules that apply to it and its address set: the IPv4 prefixes that hold every address it can have, or unknown.

| Provider | Endpoint type | Rules come from | Address set |
|---|---|---|---|
| AWS | `aws_instance`, `aws_network_interface` | the security groups in `vpc_security_group_ids` or `security_groups`, plus the groups that other resources attach (see below). An instance whose configuration has `network_interface` blocks and does not set `vpc_security_group_ids` gets the groups of the interfaces of those blocks. Exactly one block must have a `device_index` known to be 0; otherwise the gap is `unresolved-reference`, `<instance> has no network_interface block whose device_index is known to be 0, so its primary interface is not known`. | the private IPs of the endpoint and of every interface attached to it, when the plan gives all of them: `private_ips` of an interface when the plan knows the whole list; else `private_ip` as a `/32` when the configuration sets none of `private_ips`, `private_ips_count`, and `private_ip_list` (an instance adds its `secondary_private_ips`, which are none when the configuration does not set them). Otherwise the `cidr_block` of the subnet. One interface whose addresses are unknown makes the set unknown, and so does an attachment that does not resolve and may attach another interface to the instance. |
| AWS | `aws_lb`, `aws_ecs_service` (`network_configuration`), `aws_lambda_function` (`vpc_config`) | the security groups of the resource | the `cidr_block` of every subnet |
| AWS | `aws_db_instance`, `aws_rds_cluster`, `aws_elasticache_cluster`, `aws_elasticache_replication_group` | the security groups of the resource | unknown |
| AWS | `aws_security_group`, `aws_default_security_group` | the group itself | unknown |
| Azure | `azurerm_network_interface` | the NSG of the NIC and the NSG of its subnet | for every `ip_configuration` of the NIC, its `private_ip_address` as a `/32`, else every entry of `address_prefixes` of its subnet. An `ip_configuration` list that is unknown until apply makes the set unknown. |
| Azure | `azurerm_linux_virtual_machine`, `azurerm_windows_virtual_machine` | as for its NIC. A VM with more than one NIC is not supported. | as for its NIC |
| Azure | `azurerm_subnet` | the NSG of the subnet | every entry of `address_prefixes` |
| Azure | `azurerm_private_endpoint` | the NSG of its subnet, inbound only when the subnet enables network policies for private endpoints (see below) | as for the subnet |
| GCP | `google_compute_instance`, `google_compute_instance_template` | the firewall rules of its network that target it. An instance with more than one network interface is not supported. | `network_interface[0].network_ip` as a `/32`, else the `ip_cidr_range` of the subnetwork, together with every `ip_cidr_range` of `network_interface[0].alias_ip_range`. An alias range that the plan does not give, such as a size (`/24`) that the provider fills in, makes the set unknown. |

Any other endpoint type gives the gap `unsupported-resource-type`. An AWS endpoint that names no security group, or a network interface attached to it that names none, gives `unresolved-reference` with `<address> names no security group; the default group of the VPC is not in the plan`. An instance whose groups come from its `network_interface` blocks is not such an endpoint: its own `vpc_security_group_ids` is computed from its primary interface, and the plan shows it unknown or empty.

#### Rule sources

| Provider | Resources that the evaluator reads |
|---|---|
| AWS | `aws_security_group` and `aws_default_security_group` (inline `ingress` and `egress`), `aws_security_group_rule`, `aws_vpc_security_group_ingress_rule`, `aws_vpc_security_group_egress_rule`, `aws_network_interface_sg_attachment`, `aws_network_interface_attachment`, `aws_network_interface` resources whose `attachment` block names an instance, and the `network_interface` blocks of `aws_instance` |
| Azure | `azurerm_network_security_group` (inline `security_rule`), `azurerm_network_security_rule`, `azurerm_subnet_network_security_group_association`, `azurerm_network_interface_security_group_association`, `azurerm_network_interface_application_security_group_association` |
| GCP | `google_compute_firewall` |

On AWS, the groups of an instance or network interface also include the groups that `aws_network_interface_sg_attachment` attaches, and the groups of the network interfaces that are attached to an instance. An instance's own `network_interface[*].network_interface_id` blocks count too. Each such block has its own reference list in the configuration, so it resolves as described in "References". An attachment concerns the endpoint for certain only when its known value is an ID of the endpoint or of one of its interfaces, or when its configuration references resolve, in full, to that one resource. An attachment that may concern the endpoint but does not resolve gives `unresolved-reference`, with the detail `<resource>.<path> does not resolve, so it may attach a security group to <instance>`. It makes a side that no rule allows `unknown`. These cases are attachments that do not resolve:

- a `network_interface` block of the instance whose interface does not resolve, or a `network_interface` list that is unknown as a whole or in part;
- an `attachment` block of a network interface whose `instance` is unknown and no configuration reference names it, or an `attachment` list that is unknown as a whole. Every instance endpoint then gets `unknown-after-apply` on `<nic>.attachment[*].instance` and `unresolved-reference` with `<nic>.attachment does not resolve, so it may attach a security group to <instance>`. When the plan knows the `instance` of every `attachment` block, the known values decide, and the configuration references of the blocks do not;
- an attachment whose configuration references name the endpoint and another resource, for example through a conditional expression. It gives `unresolved-reference` with `<resource>.<path> names <n> resources` too.

These cases are not exceptions in a real plan that creates resources. The provider computes `aws_instance.network_interface` and `aws_network_interface.attachment` when the configuration has no such block, so the plan shows them unknown until apply. Concordia does not read them as empty, because a `dynamic` block can set them and the plan's configuration does not show `dynamic` blocks. So in a create plan, an instance or a network interface that no rule allows is `unknown`, not `denied` (see "Known limits of this release").

An `aws_network_interface_sg_attachment` and the groups of its interface can conflict. When the configuration sets the groups of the interface (`security_groups` of an `aws_network_interface`, or `vpc_security_group_ids` of an instance for its primary interface) and their planned value, known in full, does not list the attached group, an apply that sets the interface's groups removes the attached group, and a later apply of the attachment may add it again. Concordia does not count the group then. The side is `unknown` with `unresolved-reference`: `<attachment> attaches <group> to <interface>, whose planned <attribute> does not list it: the two resources conflict, and an apply may remove the group`.

An Azure rule resource belongs to an NSG through the NSG name and resource group of the rule, when the plan knows both. Concordia compares names without case. When the name is unknown until apply, the configuration reference of `network_security_group_name` decides, when it names one NSG and the rule's known `resource_group_name` is the NSG's. A reference that names several NSGs, or an NSG of another or unknown resource group, makes the owner `unknown` for every NSG, with `unresolved-reference`: `<rule>.network_security_group_name: the configuration names <n> network security groups`, or `... the rule is in resource group <group> and <nsg> is in <group>`. A reference list of which a part does not resolve, such as a variable beside an NSG, also makes the owner `unknown`. When two plans of the environment hold an NSG with that name and resource group, a rule that may belong to either is `unknown` with `unresolved-reference`: `network security group <group>/<name> names <n> resources of the plans`. A name and resource group identify an NSG only within one subscription, so the owner is certain only when the rule and the NSG are in one plan and have one provider configuration (`provider_config_key`). Otherwise the owner is `unknown` with `unresolved-reference`: `<rule> names <nsg>, but the plan does not show that the two are in one subscription (one plan and one provider configuration)`.

#### References

An attribute that is unknown until apply, such as the ID of a security group that the plan creates, resolves through the `references` of the plan's `configuration`. These rules apply:

- A reference to a resource with one instance resolves to that instance.
- A reference to a resource with more instances, to a data source, or to nothing gives the gap `unresolved-reference`.
- An unknown attribute that no reference resolves gives the gap `unknown-after-apply`.
- A reference must name a resource of the expected kind: a security group, subnet, NSG, NIC, application security group, network, subnetwork, or service account. A reference to another type gives `unresolved-reference` with the detail `<address>.<path> names <other>, a <type>, not one of <types>`. For example, `aws_vpc.main.default_security_group_id` names an `aws_vpc`, not a security group.
- A reference through `var.`, `local.`, `each.key`, or `each.value` never resolves, because the plan does not say which resource the value names. It gives `unresolved-reference` with the detail `<entry> is a variable or local value; the resource it names is not known from the plan`, and the result is `unknown`. This holds even when the plan records the value of the variable, for example `concat([aws_security_group.app.id], var.extra)` with `var.extra = []`: Concordia does not evaluate expressions.
- A reference that reads an attribute that does not identify its resource, such as a tag (`aws_security_group.a.tags["peer"]`), never resolves either, because the value may be the ID of any resource. It gives `unresolved-reference` with the detail `<resource>.<attribute> is an attribute that does not identify its resource; the resource it names is not known from the plan`. The attributes that identify a resource are `id`, `arn`, `name`, `self_link`, `email`, and `primary_network_interface_id`.
- A value that the plan marks sensitive is redacted when Concordia reads the plan. The evaluator treats a redacted value like a value that is unknown until apply: it gives `unknown-after-apply` on the attribute, and a redacted rule list, or a redacted element of one, makes the list unknown.

The inline blocks AWS `ingress` and `egress`, and Azure `security_rule`, are attributes written as blocks. In the configuration of a real plan they have one reference list for all their blocks together, not one for each block, and the blocks of a `dynamic` block are not in it at all. So an attribute of an inline block that is unknown until apply, such as the ID of a group that the plan creates, never resolves. It gives `unresolved-reference`: `<address>.<list>[*].<field>: the configuration has one reference list for all blocks of <list> (<addresses>), which does not say which block names which resource, and a dynamic block may name others`. When the configuration has no reference for the list at all, the gap is `unknown-after-apply` on the attribute. A known ID resolves as usual. A list that is unknown as a whole, or that has an element that is unknown as a whole, gives `unknown-after-apply` on `<group>.ingress`, `<group>.egress`, or `<nsg>.security_rule`.

Nested blocks that are not attributes, such as the `network_interface` blocks of an instance, have a reference list for each block. The plan lists their elements in another order than the configuration, so a reference in one block resolves across all blocks of the list, and only when every block with references names the same resources and as many blocks have references as have the attribute unknown. Otherwise the result is `unresolved-reference`. A `dynamic` block adds a block without references, so it never resolves this way.

In a plan against existing state, the inline list of a group or NSG is a copy of all its rules, including rule resources. When the plan deletes, replaces, or updates a rule resource (`aws_security_group_rule`, `aws_vpc_security_group_ingress_rule`, `aws_vpc_security_group_egress_rule`, or `azurerm_network_security_rule`), the inline list may still hold the old rule. So a match on the inline list of a group or NSG that may own such a resource is `unknown`. The gap is `unknown-after-apply`, with the detail `<group or NSG>.<ingress|egress|security_rule> may repeat a rule that the plan changes`. Concordia finds the owner like this:

| Change | The owner is |
|---|---|
| update | the group or NSG of the planned values. When the planned `security_group_id` and the `id` of the group are both known, they decide. |
| delete | the group or NSG before the change: the group whose known `id` is the old `security_group_id`, or the NSG with the old `network_security_group_name` and `resource_group_name`. A group whose `id` is unknown until apply may be the owner. |
| replace | both of the above. A new `type` replaces an `aws_security_group_rule`, and the plan does not give the old `type`, so the old owner counts for both directions. |
| delete, and the plan does not give the old owner (it is sensitive, or a hand-written plan has no `before` values) | every group (for the direction of the rule) and every NSG of the plans |

An `aws_security_group_rule` whose `type` is unknown affects both directions: as a stale owner above, and as a rule of each direction that applies maybe, with `unknown-after-apply` on `<rule>.type`. An inline rule whose `self` is unknown until apply may select its own group: it matches maybe, with `unknown-after-apply`. On Azure, an inline `security_rule` with the same name as a rule resource that surely belongs to the NSG is left out, and the planned values of the resource decide. A plan with only `create` and `no-op` actions is not affected.

#### Semantics

Traffic is `allowed` when the source side allows it outbound and the destination side allows it inbound. One side that says no makes it `denied`. Otherwise one side that cannot say makes it `unknown`.

| Provider | How one side decides |
|---|---|
| AWS | Rules only allow. The union of the rules of all groups of the endpoint applies. A group with no rule for the direction denies it. Concordia does not add the AWS default egress rule, because Terraform removes it from the groups that it manages. An egress rule of a group of the source must match the destination, and an ingress rule of a group of the destination must match the source. A rule matches the peer through a referenced group, `self`, or a CIDR. An inline list that is unknown until apply makes the side `unknown`, unless another rule allows the traffic. In a create plan, a group without inline blocks has an unknown list, because the provider computes it from the rule resources (see "Known limits of this release"). Traffic leaves through one network interface and arrives at one network interface, so when an endpoint has more than one interface, `allowed` also needs one pair of a source interface and a destination interface whose own groups allow the traffic. A group that an attachment adds without naming one interface counts for no interface in that check. Without such a pair, the result is `unknown` with `unsupported-resource-type`: `<source> to <destination>: the groups of several network interfaces together allow the traffic, but no one pair of network interfaces is shown to allow it`. |
| Azure | Every NSG on the path must allow the traffic: the NIC NSG and the subnet NSG of the source (outbound), then the subnet NSG and the NIC NSG of the destination (inbound). An absent NSG allows. In each NSG, the rules of the direction run by ascending priority, and the first rule that matches decides. Concordia adds the Azure default rules at 65000, 65001, and 65500. So `AllowVnetInBound` allows traffic inside one virtual network unless a rule with a lower number denies it. An unknown `priority`, `direction`, or `access` makes the rule uncertain. An NSG whose `security_rule` list is unknown is `unknown` at that check point. |
| GCP | Egress is checked at the source against the destination address. Ingress is checked at the destination against the source address, network tags, and service account. A rule applies to an instance when it has no targets, or a target tag or target service account of the instance. An ingress rule also matches its `destination_ranges` against the destination, and an egress rule its `source_ranges` against the source. No ranges means every address. The provider computes `source_ranges` and `destination_ranges` when the configuration does not set them, and the plan shows them unknown; Concordia then reads them as no ranges, on both sides of a rule. Rules run by ascending priority, deny before allow at equal priority, and the first rule that matches decides. Concordia adds the implied rules at 65535: deny all ingress, allow all egress. The results name them with the address `implied`. Disabled rules are skipped. A rule whose `priority`, `direction`, `disabled`, `allow` or `deny` list, or a `protocol` or `ports` inside a block, is unknown until apply is uncertain: it comes first, and if it may match, the side is `unknown` with `unknown-after-apply`. A network is a project and a name: a self link or an ID names its project, and a bare name is in the `project` of the resource that names it. A project is its known value, or, when the configuration does not set `project`, the default project of the provider configuration; a `project` that the configuration sets to a value unknown until apply can be any project. Two networks of one name are one network when their projects are equal, or when both projects are the default of one provider configuration of one plan; otherwise the query is `unknown` with `peering-not-evaluated`, and a firewall rule whose network may differ is uncertain with `unresolved-reference`. A subnetwork that a self link or an ID names is the subnetwork with that `self_link` or `id`; one that a bare name names is the one subnetwork of the plans with that name in the region of the instance and in the `subnetwork_project` of the interface, or, when the configuration does not set it, the default project of the instance's provider configuration. |

A rule that may match, but that the evaluator cannot decide, before a rule that decides, makes the side `unknown`.

An Azure private endpoint gets the inbound check of its subnet NSG only when the subnet enables network policies for private endpoints. When the subnet disables them, Concordia does not look for the NSG at all, so an association that does not resolve does not matter. Concordia reads `private_endpoint_network_policies` first, then the older `private_endpoint_network_policies_enabled` and `enforce_private_link_endpoint_network_policies`:

| Subnet policy | Subnet NSG, inbound to the private endpoint |
|---|---|
| `Enabled`, `NetworkSecurityGroupEnabled` | applies |
| `Disabled`, `RouteTableEnabled` | does not apply |
| unknown until apply | `unknown`, `unknown-after-apply` |
| not in the plan | `unknown`, `unsupported-policy-kind` |

Rule prefixes match the endpoint address set like this:

| Rule prefix | Match |
|---|---|
| an IPv4 prefix that contains every prefix of the endpoint set | yes |
| an IPv4 prefix that overlaps no prefix of the endpoint set | no |
| any other IPv4 prefix against a known endpoint set | `unknown`, `address-dependent-range`, detail `<prefix> covers only part of <set>` |
| `0.0.0.0/0`, endpoint set unknown | yes |
| any other IPv4 prefix, endpoint set unknown | `unknown`, `address-dependent-range` |
| an IPv6 prefix, including `::/0` and an IPv4-mapped prefix such as `::ffff:10.0.0.0/104` | `unknown`, `address-dependent-range`, detail `<prefix>: IPv6 ranges are not evaluated` |
| does not parse | `unknown`, `invalid-address-range` |

An IPv6 prefix never decides a result on its own: it is `unknown` unless an IPv4 rule decides first.

On Azure and GCP, an IPv4 deny cannot stop IPv6 traffic, which falls through to a later rule. So a deny that decides through an address prefix gives `denied` only when the plan proves that at least one endpoint has IPv4 addresses only. IPv6 traffic between two endpoints needs an IPv6 address at both ends, so one IPv4-only end is enough. Otherwise the result is `unknown`, and the gap is `address-dependent-range`:

| Provider | The deny counts when | An endpoint is IPv4-only when | Gap detail |
|---|---|---|---|
| Azure | its source or destination names a CIDR or an IP. `*`, service tags, `VirtualNetwork`, application security groups, and default rules never count. | every entry of `address_prefixes` of its subnet is a known IPv4 prefix, and, for an endpoint with a NIC, every `ip_configuration` of the NIC has the `private_ip_address_version` `IPv4`. An absent version is `IPv4`, the provider default. A subnet or private endpoint without a NIC needs only the subnet part. | `<address> <rule>: an IPv4 deny decided, and the plan does not show that either endpoint is IPv4-only` |
| GCP | it is an egress deny, or an ingress deny that does not select the peer by source tags or service accounts only | `network_interface.0.stack_type` of the instance is `IPV4_ONLY`, or the instance does not state it and the `stack_type` of its subnetwork in the plan is `IPV4_ONLY`. A stack type is `IPV4_ONLY` when the plan knows it as such, or when it is unknown until apply only because the configuration does not set it and no IPv6 setting is present: the provider then uses `IPV4_ONLY`. For the interface, every `network_interface` block of the instance must be written statically (the plan lists as many blocks as the configuration shows), because a `dynamic` block may set the stack type, and the interface must have no `ipv6_access_config` block and no `ipv6_address`, `internal_ipv6_prefix_length`, or `ipv6_access_type` in the configuration or with a value in the plan. For the subnetwork, the same holds for `ipv6_access_type`, `external_ipv6_prefix`, `internal_ipv6_prefix`, and `ipv6_cidr_range`. A stack type that the configuration sets to a value unknown until apply proves nothing. | `<address> <rule>: an IPv4 deny decided, and the stack type of neither endpoint is known to be IPV4_ONLY` |

So on GCP, an IPv4 deny gives `denied` when the configuration sets `stack_type` to `IPV4_ONLY`, or leaves it unset and sets no IPv6 attribute. AWS needs no such rule, because its rules only allow.

Protocols `-1`, `all`, and `*` match every protocol. ICMP and other protocols never match a TCP or UDP query. A port range outside 0-65535 does not parse and gives `unknown` with `unsupported-policy-kind`, detail `port range "<range>"`. Azure source ports other than `*` (`source_port_range` or `source_port_ranges`) give `unknown` with `unsupported-policy-kind`, detail `source port range`, because a query has no source port. Azure service tags: `*` matches, `VirtualNetwork` matches when both endpoints are in one virtual network, `Internet` and `AzureLoadBalancer` do not match, and any other tag gives `unsupported-policy-kind`. Concordia compares virtual networks by the plan and the provider configuration (`provider_config_key`) of each subnet, which fix its subscription, and by the network's resource group and name, else the configuration reference. A subnet finds its network by name in its own resource group, so a configuration reference identifies the network only when the plan shows the subnet and the network in one resource group. Otherwise the gap is `unresolved-reference`: `<subnet>.virtual_network_name names <network>, but the plan does not show that the two are in one resource group`. A subnet whose provider configuration the plan does not give is not in a known virtual network. A reference to a network with more than one instance gives `unresolved-reference`, and the result is `unknown`. An application security group in a rule never matches an endpoint whose NIC the plan shows in no application security group, even when the rule's groups do not resolve.

The `RuleRef` of a result names each rule that decided it, as `<address> <rule>`: for example `aws_security_group.web ingress[0]`, `aws_vpc_security_group_ingress_rule.db rule`, `azurerm_network_security_group.app AllowHttps (priority 100)`, or `google_compute_firewall.db priority 1000 allow`.

#### Assumption, integrity, and digest

A `denied` result rests on the assumption `firewall-rules-complete-in-plan`: no firewall rule for the resources of the query exists outside the evaluated plans, for example in another Terraform state or in a change made in the console. Declare it under `assurance.evaluators[].assumes`. An `allowed` result does not need the declaration. On AWS, a rule outside the plans can only allow more, so an `allowed` result holds without it. On Azure and GCP, a deny rule outside the plans with a higher priority can stop traffic that the plans allow, so an `allowed` result there also rests on the absence of such rules. Read an Azure or GCP `allowed` result with that in mind.

The plan does not need a signed statement. The claim `cloud-network-isolation` needs the integrity `content not verified`. Each evaluation in [`assurance.evaluations`](results.md#evaluations) records the plan digest in `inputDigest` and the integrity of the plan binding in `integrity`: `content not verified` for a plan without a statement, `producer authenticated` for a plan that a verified statement binds. With more plans in the environment, `inputDigest` is the digest of their sorted digests, and `integrity` is the lowest. `concordia export bundle` copies the plans, so a reader can check the bytes by digest.

With `ports`, Concordia queries TCP on each port, because the rule form has no protocol. Without `ports`, Concordia takes the ports from the firewall rules of the environment's plans, separately for each protocol. A rule for UDP, or for every protocol, is also queried over UDP, and the query in `assurance.evaluations` names the protocol, for example `udp/53 from …`. A plan with Azure resources is always queried over UDP too, because the Azure default rules, which are not in the plan, allow every protocol inside a virtual network, and an endpoint without an NSG allows every protocol. For each port and range that a rule names, Concordia queries both ends, the ports next to them, and one port inside the range. It also queries, for each protocol in use, one port that no rule of that protocol names, so a rule that opens every port (`*`, a GCP block without ports, AWS `-1`) still gets a query. A rule matches ports only through these ranges, so every group of ports that the rules treat alike gets at least one query: the answer holds for every port of the protocol.

Concordia keeps the plans of an environment apart. A configuration reference resolves only inside the plan of the resource that holds it, because the configuration of one root module says nothing about the resources of another. A known ID, or a known Azure name and resource group, resolves across all plans of the environment. When two plans hold the same address, only a query that names that address as an endpoint is `unknown`: `coverage-incomplete`, with the gap `unresolved-reference` and the detail `<address> is in <n> plans of environment <env>`. A bound address names no plan, so Concordia cannot tell which of the two resources you mean. A known ID that resources of two plans have gives `unresolved-reference` too: `<resource>.<attribute>: <id> is the id of <n> resources of the plans`. Other pairs of the environment are not affected.

#### Gaps and limits

| Gap | Meaning |
|---|---|
| `incomplete-plan` | A plan of the environment is not `complete`: it has deferred changes, or one of several plans could not be read. Every query is `unknown`. When no plan can be read, the result is `rendered-evidence-missing`. |
| `workload-not-in-input` | An endpoint address, for example a bound address with a typo, is not a resource of the plans after apply. The result is `rendered-evidence-missing`. |
| `mixed-providers` | The two endpoints belong to different providers. |
| `unsupported-resource-type` | The endpoint type is not in the table above, an Azure VM has more than one NIC, a GCP instance has more than one network interface, or the groups of several AWS network interfaces allow traffic that no one pair of interfaces allows. |
| `unresolved-reference` | A reference does not resolve to one resource of the expected kind: it names several instances, a data source, nothing, or another resource type, it goes through a variable or local value, or it reads an attribute that does not identify its resource; inline blocks name different resources; an AWS attachment or `network_interface` block does not resolve, or its references name several resources; an Azure subnet or NIC has more than one associated NSG; an Azure rule and its NSG, or a subnet and its virtual network, are not shown in one subscription or one resource group; an endpoint address is in more than one plan of the environment; or a known ID, or an Azure NSG name, belongs to resources of more than one plan. |
| `unknown-after-apply` | An attribute that the evaluator needs is unknown until apply or redacted, and no reference resolves it. This includes an inline rule list that is unknown, an inline list that may repeat a rule that the plan deletes, replaces, or updates, an unknown `instance` in an attachment block, an unknown Azure rule `priority`, `direction`, or `access`, an unknown GCP rule field, and an unknown private-endpoint policy. |
| `address-dependent-range`, `invalid-address-range` | A rule prefix overlaps the endpoint partly, needs an address that the plan does not know, is an IPv6 prefix, or does not parse; or an IPv4 deny decided on Azure or GCP and the plan does not prove that an endpoint is IPv4-only. |
| `peering-not-evaluated` | Azure `VirtualNetwork` across two virtual networks, or two GCP instances in different networks. |
| `unsupported-policy-kind` | An AWS prefix list; an Azure service tag, a source port range, an application security group against an endpoint without a NIC, or a private endpoint whose subnet does not state its network policies; or a GCP firewall policy. Any `google_compute_network_firewall_policy*`, `google_compute_region_network_firewall_policy*`, or `google_compute_firewall_policy*` resource in the plan makes every GCP query `unknown`. |

The contract states these limits on every result:

- Network ACLs, route tables, and firewall appliances (AWS Network Firewall, Azure Firewall) are not evaluated.
- VPC and VNet peering, transit gateways, and private links are not evaluated.
- GCP hierarchical and network firewall policies are not evaluated.
- Rules managed outside this plan are assumed absent, not observed.
- A plan states what would be applied, not what is applied.
- IPv6 ranges are not evaluated. A rule that names one gives `unknown`, unless an IPv4 rule decides first.

#### Known limits of this release

These limits are known and recorded. They make results `unknown` more often than needed, and never give a wrong `allowed` or `denied`. The last entry is a condition of the provider defaults that Concordia reads, not such a limit. An earlier release could give a wrong `allowed` when an `aws_network_interface_sg_attachment` conflicted with the groups of its interface; this release reads that conflict as `unknown` (see "Rule sources"), even where one apply alone would give `denied`.

- **Inline rules that name a resource that the plan creates.** An inline `ingress`, `egress`, or `security_rule` block whose group or application security group is unknown until apply never resolves, because the configuration has one reference list for all blocks of the list and does not show `dynamic` blocks. Use rule resources (`aws_vpc_security_group_ingress_rule`, `azurerm_network_security_rule`) when you want such rules to decide.
- **Computed block lists in a create plan.** A new security group without inline blocks, a new instance without a `network_interface` block, and a new network interface without an `attachment` block have that list unknown until apply. A plan cannot show that no `dynamic` block sets it, so Concordia does not read it as empty: a side that no rule allows is `unknown` until the resources exist. A plan against existing state knows these lists.
- **References through a variable with a known value.** A reference through `var.`, `local.`, `each.key`, or `each.value` never resolves, even when the plan records the value of the variable.
- **Azure rule owner through a variable, with an unknown name.** When the `network_security_group_name` of an `azurerm_network_security_rule` is unknown until apply and comes through a variable or local value, Concordia cannot tell which NSG the rule belongs to. It treats the rule as possibly belonging to every NSG, so a match on it is `unknown` wherever it may apply. When the plan knows the name, the name decides.
- **Deleted rule resources without a known old owner.** When the plan does not give the owner of a deleted rule resource before the change (the owner is sensitive, or the plan has no `before` values), every inline rule that matches is `unknown`, on every security group (for the direction of the rule) and on every NSG of the plans.
- **Instances that set `primary_network_interface` are not evaluated.** Concordia does not read the `primary_network_interface` block of `aws_instance` (aws 6.x). Such an instance has `vpc_security_group_ids` unknown until apply, so it stays `unknown`.
- **Provider defaults belong to the recorded provider versions.** The defaults that Concordia reads for unset attributes (GCP `stack_type`, and the own ranges of a GCP firewall rule) hold for `hashicorp/google` 8.12.0, the version of the recorded plans below. They are checked again when a provider major version changes.

The evaluator is tested against plans that Terraform 1.17.0 recorded for `hashicorp/aws` 6.74.0, `hashicorp/google` 8.12.0, and `hashicorp/azurerm` 5.15.0, without edits. The configurations, the plans, and how they were recorded are in `test/fixtures/assurance/plans/real` (see its `README.md`).

The enabled claim is `cloud-network-isolation`. The evaluator does not enable `network-isolation`, `effective-encryption-in-transit`, `cloud-iam`, or `routing-reachability`.

### Assumptions

A result that is `denied` or `not-granted` rests on an enforcement assumption: that the cluster's network plugin enforces NetworkPolicy (`network-policy-enforced`), that the RBAC authorizer is enabled (`rbac-authorizer-enabled`), or that the plans hold every firewall rule of the resources (`firewall-rules-complete-in-plan`). Concordia cannot see any of them, so the workspace declares them in `assurance.evaluators[].assumes`. Every result lists its assumptions and limits. An `allowed` or `granted` result needs no declared assumption, because an unenforced policy only allows more. An Azure or GCP `allowed` result from `cloud-firewall` still reads only the rules in the plans (see "Assumption, integrity, and digest").

### The gates of a result

A `require.effectiveAccess` rule passes through three gates, in this order:

| Gate | A failure gives |
|---|---|
| The workspace enables an evaluator of the kind for the environment. | `unknown`, `no-evaluator-available` |
| The environment has the evidence of the evaluator: rendered resources for `network-policy` and `rbac`, a usable plan for `cloud-firewall`. The namespaces that the result involves have `complete` coverage. A CALM element that is not bound to a rendered resource or a planned resource is a `binding-gap`. | `unknown`, `rendered-evidence-missing`, `coverage-incomplete`, or `binding-gap` |
| A `denied` or `not-granted` result has its assumption declared. | `unknown`, `enforcement-not-declared` |

A rule that expects `denied` is violated by one open port, and is met when every port is closed. Without `ports`, `network-policy` queries the ports that the destination declares, every port that a policy rule names, and one port that no rule names. `cloud-firewall` collects the ports of the firewall rules separately for each protocol, so that every group of ports that the rules treat alike gets a query. The [`cloud-firewall`](#cloud-firewall) section describes this. Each query appears in [`assurance.evaluations`](results.md#evaluations), with the evaluator, its version, the digest of its input, the policies or rules that decided it, the assumptions, the limits, and the gaps.

### Limits and gaps

An evaluator that cannot evaluate a construct gives `unknown` with a gap, for the affected workloads only. Other namespaces are not blocked. The gap is listed in the `gaps` of the evaluation in [`assurance.evaluations`](results.md#evaluations).

| Gap | Meaning |
|---|---|
| `unsupported-policy-kind` | A CNI-specific kind, such as CiliumNetworkPolicy, CalicoNetworkPolicy, or AdminNetworkPolicy, selects the workload. It is not evaluated. |
| `unresolved-named-port` | A named port that no container port defines. |
| `cluster-dependent-range` | An `ipBlock` that may contain pod addresses. |
| `invalid-selector`, `invalid-address-range`, `invalid-peer` | The document does not parse. |
| `namespace-coverage-incomplete`, `cluster-coverage-incomplete` | The render of the namespace, or of the cluster-scoped resources, is not `complete`. |
| `namespace-not-in-input`, `workload-not-in-input` | The result needs a namespace or a workload that is not in the rendered input. |
| `missing-role-target`, `invalid-role-reference`, `aggregation-cycle` | A role that a binding names is missing or not valid, or aggregation refers to itself. |
| `declared-evidence-only` | The input is declared-level evidence, which has no selectors and rules. |

The contracts also state what the evaluators do not do. `network-policy` does not model host-network pods, node traffic, service address translation, ingress controllers, a service mesh, or application-layer authorization. `rbac` does not evaluate cloud IAM, admission control, or webhook authorizers, does not know the group membership of users, and reports the ClusterRoles that the cluster provides (`cluster-admin`, `admin`, `edit`, `view`) as missing targets when they are not rendered. The enabled claims are `network-isolation` and `rbac-grants`. Neither enables `effective-encryption-in-transit`, `cni-specific-policy-evaluation`, `effective-authorization`, or `cloud-iam`.

## Verification results

A check result is a statement that a named check had an outcome for a revision and an environment, from a trusted producer. The contract enables `check-outcome` and nothing about test adequacy, code coverage, or the absence of defects. Results are declared under [`assurance.verification`](workspace.md#assuranceverification). The predicate is described by [`evidence-check-result.schema.json`](evidence-check-result.schema.json).

| Field | Type | Meaning |
|---|---|---|
| `check` | string | The check identifier, for example `unit-tests`. |
| `outcome` | `passed`, `failed`, `errored`, `skipped` | Any other value is `invalid-outcome`. |
| `revision` | string | The source revision that the check ran against. |
| `environment` | string | The environment of the check. |
| `evaluator.name`, `evaluator.version` | string | The program that made the result. |
| `evidence` | list | Optional. Each item has a `name`, a `uri`, and a `digest` map. |
| `time`, or `window.start` and `window.end` | RFC 3339 | When the check ran. The age counts from `time`, else from `window.end`. |
| `covers` | string list | The control, node, and scenario identifiers that the result covers. |

A missing field and an empty field stay different. A missing `covers` is `missing-field:covers`, and the result is inadmissible. An empty list (`"covers": []`) is a statement that the result covers nothing. The result is admissible, it is shown with that fact, and a rule with a `subject` never counts it for a node.

### Admissibility and verdicts

A result is admissible for an obligation only when its producer is trusted for `check-result` and its integrity is `producer authenticated`; its revision is the evaluated revision; its environment is the environment of the obligation; its time is within `maxAgeDays`; and the tree is the evaluated state. Each failed condition is one detail: `producer-not-trusted`, `producer-not-trusted-for-claim`, `not-authenticated`, `integrity-failure`, `offline-verification-unavailable`, `different-revision`, `different-environment`, `stale`, `subject-not-evaluated-state`, `invalid-outcome`, and `missing-field:<name>`. The [list of details](#admissibility) says what each one means. A result with outcome `failed` or `errored` is admissible.

| Admissible results of the check | Verdict | Reason |
|---|---|---|
| A `passed` result | `pass`, with the digest of the result statement as evidence | none |
| A `failed` or `errored` result | `violation` | none |
| A `passed` result and a `failed` or `errored` result | `unknown`, and `CO-PRV-012` names both | `evidence-conflict` |
| Only `skipped` results | `unknown`, detail `the check was skipped` | `no-evidence-supplied` |
| No result, or only inadmissible results | `unknown`, detail `no admissible result`, and one line for each inadmissible result with its details | `no-evidence-supplied` |

A `skipped` result next to a `passed` or `failed` one is ignored. No result wins by order or by time. An inadmissible result never gives `pass` or `violation`. A glob in `results` that matches no file is `CO-PRV-011`, and the dependent obligations are `unknown` with `no-evidence-supplied`. Missing files are never failed checks.

The `TESTS` and `TESTS_FILE` edges of the code graph are candidates only. They carry no framework, test name, or confidence. They never support a verdict. [`assurance validate`](cli.md#assurance-validate) and `explain` list them next to a check that has no result, with the CALM nodes that the tested node is bound to as a suggested mapping for the `covers` list of a result.

### Rules and controls

A rule names a check with `require.checkResult` (`check`, and optionally `environment` and `subject`), which [policies](policies.md) describes. A check that no result and no control defines is a compile error (exit 2). Without an `environment`, the rule has one outcome for each environment that a result of the check names. With a `subject`, a result counts for a node only when its `covers` lists it.

A control lists checks with `check-ids`, beside or instead of `rule-ids` (see [control configuration](architecture.md#control-configuration)). The verdict of the control is the worst verdict of its rules and its checks. The verdict of a check is the worst over the environments that its results name. A check with no admissible result is `unknown` with `no-evidence-supplied` and names the check, so a link to a check never fulfils a control. The `minimum-policy-coverage` of the control counts the checks with a definite result, and `unknown-disposition: fail` fails the run for a check that is `unknown`. The results list the check ids of each control, and the results of each check are in [`assurance.checkResults`](results.md#checkresults).

## Quality

A quality scenario file (`QualityScenarioSet`) declares scenarios in one of three modes, and findings that concern them. Scenarios are declared under [`assurance.quality`](workspace.md#assurancequality). The schema is `internal/assurance/quality/schemas/quality.schema.json`. A verdict holds for one scenario in its own mode: a `structural` pass never satisfies a `measured` scenario. The contract enables a verdict for each scenario and does not enable a statement that an architecture meets a quality attribute in general.

### File shape

The file has `apiVersion: concordia.io/v1alpha1`, `kind: QualityScenarioSet`, `scenarios` (at least one), and `findings`. Unknown keys are not allowed.

| Scenario field | Type | Meaning |
|---|---|---|
| `id` | string | Unique in the workspace. Letters, digits, `.`, `_`, `-`. |
| `area` | enum | `availability`, `modifiability`, `security`, `performance`, `deployment-safety`, or `operability`. |
| `qualityAttribute`, `stimulusSource`, `stimulus`, `environment`, `expectedResponse` | string | The scenario, in the usual six parts. `environment` is the environment in which a measurement is taken. |
| `owner` | string | Who owns the scenario. |
| `scope` | string list | CALM element ids. An id that the model does not contain is `CO-QUA-002`. |
| `mode` | enum | `structural`, `measured`, or `reviewed`. |
| `criterion` | object | Depends on the mode, see below. |
| `enforcement` | `mandatory`, `advisory` | Default `advisory`. |
| `addressedBy` | string list | ADR ids of the form `ADR-<number>`. |

| Mode | `criterion` |
|---|---|
| `structural` | `rules`: rule ids, at least one. Each must be defined by a loaded policy. |
| `measured` | `metric`, `unit`, `comparison` (`lt`, `lte`, `gt`, `gte`, `eq`), `threshold` (number), and optional `maxAgeDays`. |
| `reviewed` | `subject` and `authority`. |

| Finding field | Type | Meaning |
|---|---|---|
| `id` | string | Unique. |
| `type` | enum | `sensitivity-point`, `risk`, or `tradeoff`. |
| `statement`, `owner` | string | What the finding says, and who owns it. |
| `status` | enum | `open`, `accepted`, or `resolved`. |
| `scenarios` | string list | The scenarios that it concerns. Each must be declared. |
| `areas` | enum list | Optional. Else the areas of its scenarios. |
| `addressedBy` | string list | ADR ids. |
| `assumptions` | string list | Optional. |

```yaml
apiVersion: concordia.io/v1alpha1
kind: QualityScenarioSet
scenarios:
  - id: checkout-latency
    area: performance
    qualityAttribute: latency
    stimulusSource: customers
    stimulus: 500 checkouts per second
    environment: production
    expectedResponse: p95 latency stays below 300 ms
    owner: team-checkout
    scope: [shop]
    mode: measured
    enforcement: mandatory
    criterion: { metric: checkout-p95, unit: ms, comparison: lt, threshold: 300, maxAgeDays: 14 }
    addressedBy: [ADR-7]
findings:
  - id: cache-tradeoff
    type: tradeoff
    statement: The cache lowers latency and raises the cost of a schema change.
    owner: team-checkout
    status: open
    scenarios: [checkout-latency]
    areas: [performance, modifiability]
```

A file that is not valid, a field that is missing, and a duplicate id are `CO-QUA-001` (exit 2). A scope, a rule, a finding scenario, or an ADR that cannot be resolved is `CO-QUA-002` (exit 2). Each error names the file and the line.

### Modes

| Mode | Reads | Verdict |
|---|---|---|
| `structural` | The verdicts of the rules in `criterion.rules`. | The worst of `error`, `violation`, `unknown`, `pass`. A rule that is `unknown` keeps its reason, for example `coverage-incomplete`. A rule that was not evaluated or is `not-applicable` counts as `unknown` with `no-evidence-supplied`. |
| `measured` | Measurement statements. | `pass` or `violation` from an admissible measurement. |
| `reviewed` | The assessments of the workspace (`type: quality` that name the scenario id). | See below. |

Concordia evaluates scenarios after the rules, the ADR decision hook, and the review evaluation of the same run.

**Reviewed outcome mapping.** Only the assessments of type `quality` count, and a security assessment does not review a scenario. Without an `assessments` key the scenario is `unknown` with `reviewed-mode-not-available` and the detail `the workspace has no assessments key`. It is never a pass.

| Assessment state | Verdict |
|---|---|
| `current` and approved, outcome `satisfactory` | `pass` |
| `current` and approved, outcome `satisfactory-with-conditions` | `pass`, with the detail `satisfactory with conditions` |
| `current` and approved, outcome `unsatisfactory` | `violation` |
| `current` and approved, outcome `inconclusive` | `unknown`, `no-evidence-supplied` |
| A rejected approval | `violation` |
| `review-required`, `expired`, `revoked`, `unknown`, not approved, or no assessment | `unknown`, with the state as the detail. The reason is `no-evidence-supplied`, or the reason that the currency reports when its state is `unknown`. |

With several assessments the worst wins: a rejection, then `unsatisfactory`, then `inconclusive`, then a pass. An assessment that is not current and approved never lowers a pass.

### Measurements

A measurement is a statement of the predicate type `https://concordia.io/evidence/measurement/v1`, described by [`evidence-measurement.schema.json`](evidence-measurement.schema.json). Its fields are `scenario`, `metric`, `value`, `unit`, `threshold`, `comparison`, `testConditions`, `revision`, `environment`, `evaluator` (`name`, `version`), `outcome`, and `window` (`start`, `end`). Concordia compares `value` with the criterion of the scenario itself and never uses a verdict of the producer. A measurement is admissible when:

- its producer is trusted for `measurement` and the integrity is `producer authenticated`;
- its `environment` is the `environment` of the scenario, and its `revision` is the evaluated revision;
- its `threshold`, `comparison`, and `unit` equal those of the scenario, with no unit converted (`threshold-mismatch`, `unit-mismatch`), and its `metric` is the metric of the scenario;
- its `outcome` is `passed` (`failed-run` otherwise);
- the end of its `window` is within `criterion.maxAgeDays` (`stale`); and
- no field is missing, and `testConditions` is stated.

An inadmissible measurement never gives `pass` or `violation`. It is `CO-QUA-003` with every reason, and the scenario is `unknown` with `no-evidence-supplied`. Of the admissible measurements, the newest window decides, and an older window never does. Measurements of that window that disagree make the scenario `unknown` with `evidence-conflict` and `CO-PRV-012`. A measurement with no `scenario` cannot be assigned to one and is `CO-QUA-003`.

### Areas, findings, and ADR links

The report has one row for each of the six areas. The verdict of an area with scenarios is the worst verdict of those scenarios, so one `pass` and one `unknown` give `unknown`. An area with no scenario is `no scenario` and never `pass`. An area that [`assurance.quality.requiredAreas`](workspace.md#assurancequality) lists and that no scenario covers is a `violation`, which an empty scenario set cannot remove. A name in `requiredAreas` that is not an area is `CO-QUA-002`.

A finding is listed next to its areas whatever its status, and never changes an area verdict: a passing `performance` scenario does not close an open tradeoff that lowers `modifiability`.

An `addressedBy` entry links a scenario or a finding to a decision of [ADR governance](adr-governance.md). Ids compare by canonical form: `ADR-0007` equals `ADR-7`.

| State of the decision | Link |
|---|---|
| `active` | active |
| `superseded` or `inactive` | not active. The owner is flagged for a review of its rationale. |
| `ambiguous` or `unknown` | unknown |
| Not evaluated (no decision register, or the governance pack is not enabled) | unknown, with the detail `adr-governance-not-available`. The file still loads. |

When ADR governance is evaluated, an id that no register entry has is `CO-QUA-002` (exit 2). An id that is not of the form `ADR-<number>` is `CO-QUA-001`.

### Enforcement

Each scenario becomes one rule result, `quality:<id>`, so the exit code, `check.failOn`, `check.unknown`, and waivers treat it as any other obligation.

| `enforcement` | Severity | On `unknown` |
|---|---|---|
| `mandatory` | `error` | `fail`: the run exits 4 |
| `advisory` (default) | `warning` | `warn` |

A required area with no scenario is the mandatory rule `quality:area:<area>`. These rules report in the area `qualityEvaluation` of [`areas`](results.md#areas), not in `conformance`, so a green conformance does not hide an `unknown` mandatory scenario. The report is [`assurance.quality`](results.md#quality) and [`assurance.qualityAreas`](results.md#qualityareas).

## require.claim

A rule requires a registered claim with `require.claim`. The rules of the [`assurance` pack](assurance-rules.md) use it, and a rule of any policy pack can use it:

```yaml
rules:
  - id: SHOP-REN-001
    name: Production renders completely
    severity: warning
    family: assurance
    require:
      claim:
        id: complete-resource-set
        environment: production
```

| Key | Type | Meaning |
|---|---|---|
| `id` | string | A claim id in kebab-case. |
| `environment` | string | Optional. Without it the rule applies to every environment of the run. |

The rule has the family `assurance-claim`, with the maturity `advisory`, and it is not reducible for `check --changed`: it always evaluates the full scope. A rule-level `subject` is refused (`CO-POL-001`). Concordia looks the claim up in the registry when it evaluates the rule, not when it compiles it. A claim that is not registered, or whose contract is not loaded, is `unknown` with `claim-not-enabled`, not a compile error. A claim of an experimental contract caps the severity of the rule at `warning`.

Concordia evaluates these claims for a rule: `complete-resource-set`, `container-occurrence-versions`, and `resolved-selections` for each environment, and `exact-lineage`, `deployed-digest`, `deployment-succeeded`, and `runtime-drift` for each container occurrence. A claim that has its own rule family or evaluator (`planned-resources`, `network-isolation`, `cloud-network-isolation`, `rbac-grants`, `check-outcome`, `quality-scenario-verdict`) is `unknown` with `no-evidence-supplied` through `require.claim`. These claims are evaluated through `require.effectiveAccess`, `require.checkResult`, or a quality scenario. The pack rules are:

| Rule | Claim | Severity |
|---|---|---|
| `ASR-REN-001` | `complete-resource-set` | `warning` |
| `ASR-PRV-001` | `exact-lineage` | `warning` |
| `ASR-PRV-002` | `runtime-drift` | `warning` |

The pack is opt-in: `use: builtin:assurance` under [`policies.packs`](workspace.md#policiespacks) enables it.

## Reasons

An `unknown` obligation has exactly one reason from a closed set. The assurance adapters add six. Each keeps one meaning, and no other reason stands for it.

| Reason | Meaning |
|---|---|
| `claim-not-enabled` | A rule needs a claim that no loaded contract enables: the claim is not in the registry, or its contract is not loaded. |
| `no-evidence-supplied` | No statement or result was supplied for the check, or every one that was supplied is inadmissible. |
| `no-evaluator-available` | An effective-policy claim has no evaluator for the environment. |
| `rendered-evidence-missing` | The scope has no complete rendered-level evidence. |
| `enforcement-not-declared` | An evaluator result needs an enforcement assumption that the workspace does not declare. |
| `reviewed-mode-not-available` | A `reviewed` quality scenario is evaluated in a workspace without an `assessments` key. |

The other reasons of the closed set are in [verdicts and coverage](../explanation/verdicts-and-coverage.md#the-reasons-for-unknown).

## Diagnostics

The codes `CO-PRV-001` to `CO-PRV-014` and `CO-QUA-001` to `CO-QUA-003` are in [diagnostics](diagnostics.md). A declaration that is not valid, an environment that is not declared, and a path outside the workspace root are errors with exit code 2. A renderer that is missing or different from its pin, and an OS sandbox that is required and unavailable, are errors with exit code 5. A problem in the content of an evidence source is a warning that makes the dependent claim `unknown`.

## Related

- [Assurance rules](assurance-rules.md): the rules of the built-in pack `assurance`.
- [Workspace](workspace.md#assurance): the `assurance` key that declares the evidence.
- [Results](results.md#assurance): the `assurance` object of `results.json`.
- [Add stronger evidence](../how-to/add-stronger-evidence.md): the steps to declare and supply the evidence.
- [Evidence and trust](../explanation/evidence-and-trust.md): why the evidence has levels and integrity, and the trust model.
