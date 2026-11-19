# Assurance reference

The assurance adapters give Concordia evidence that code analysis cannot give: manifests that Concordia renders itself, plans, signed statements about builds and deployments, check results, and quality scenarios. This page lists what each adapter claims and what it does not claim, and the reasons that an obligation is `unknown` when evidence is missing. The workspace key that declares the evidence is [`assurance`](workspace.md#assurance).

Concordia reads this evidence in a trusted pipeline. It does not defend against a workspace that an attacker controls.

## Claims and contracts

A claim is a statement that a rule can need. Each claim is enabled by one contract: the contract of the adapter or evaluator that can support it. The registry is owned by Concordia and is not part of the provider [capability profile](capability-profiles.md): registering a contract never changes the profile digest. `concordia doctor` lists the contracts, and the run manifest records the digest of the registry.

A rule that needs a claim that no loaded contract enables is `unknown` with the reason `claim-not-enabled`. It never passes. Every contract is `experimental` for now: the severity of a rule that needs one of its claims is capped at `warning`, until the fixture suite of the contract is listed as passing in the registry.

| Contract | Kind | Enabled claims | Not enabled | Experimental |
|---|---|---|---|---|
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

An attribute that is unknown until apply is the gap `unknown-after-apply`, one for each attribute. Concordia never guesses a value. An attribute that the plan marks sensitive is replaced by a redaction marker and recorded as a redaction. Concordia reads no other copy of a value (`variables`, `planned_values`, `prior_state`, `configuration`), because those copies carry no marker for each attribute. A CALM node can bind to a plan address with an [`infraResource` binding](bindings.md), and the trace then shows the `rendered-or-evaluated` level.

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

The built-in evaluators compute an effective result from the rendered manifests: `network-policy` for `networking.k8s.io/v1` NetworkPolicy, and `rbac` for roles and bindings. Selection edges and role references from the code graph are never evidence of isolation or access: the code graph has no edge for an empty `podSelector` or a `matchExpressions` selector. An evaluator runs for the environments that you list under [`assurance.evaluators`](workspace.md#assuranceevaluators). A rule asks for a result with `require.effectiveAccess`, which [policies](policies.md) describes. Each evaluator has a version, which results and digests record, and a contract that `concordia doctor` lists.

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

### Assumptions

A result that is `denied` or `not-granted` rests on an enforcement assumption: that the cluster's network plugin enforces NetworkPolicy (`network-policy-enforced`), or that the RBAC authorizer is enabled (`rbac-authorizer-enabled`). Concordia cannot see either, so the workspace declares them in `assurance.evaluators[].assumes`. Every result lists its assumptions and limits. An `allowed` or `granted` result needs no assumption, because an unenforced policy only allows more.

### The gates of a result

A `require.effectiveAccess` rule passes through three gates, in this order:

| Gate | A failure gives |
|---|---|
| The workspace enables an evaluator of the kind for the environment. | `unknown`, `no-evaluator-available` |
| The environment has rendered resources, and the namespaces that the result involves have `complete` coverage. A CALM element that is not bound to a rendered resource is a `binding-gap`. | `unknown`, `rendered-evidence-missing`, `coverage-incomplete`, or `binding-gap` |
| A `denied` or `not-granted` result has its assumption declared. | `unknown`, `enforcement-not-declared` |

A rule that expects `denied` is violated by one open port, and is met when every port is closed. Without `ports`, Concordia queries the ports that the destination declares, every port that a policy rule names, and one port that no rule names. Each query appears in [`assurance.evaluations`](results.md#evaluations), with the evaluator, its version, the digest of the rendered input, the policies that decided it, the assumptions, the limits, and the gaps.

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

Concordia evaluates these claims for a rule: `complete-resource-set`, `container-occurrence-versions`, and `resolved-selections` for each environment, and `exact-lineage`, `deployed-digest`, `deployment-succeeded`, and `runtime-drift` for each container occurrence. A claim that has its own rule family or evaluator (`planned-resources`, `network-isolation`, `rbac-grants`, `check-outcome`, `quality-scenario-verdict`) is `unknown` with `no-evidence-supplied` through `require.claim`. These claims are evaluated through `require.effectiveAccess`, `require.checkResult`, or a quality scenario. The pack rules are:

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
