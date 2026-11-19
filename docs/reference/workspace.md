# Workspace reference

This page describes every key of the workspace file (`concordia.yaml` by default) and the `.concordia.lock` file. The schema is `internal/workspace/schemas/workspace.schema.json` (`https://concordia.io/schemas/v1alpha1/workspace.schema.json`). Unknown keys are rejected, except under `metadata`.

To create a workspace, see [set up a workspace](../how-to/set-up-a-workspace.md). To select another file, use the `--workspace` option in the [CLI reference](cli.md). Paths in the workspace are relative to the directory that contains the workspace file.

## Top-level keys

| Key | Type | Required | Default | Description |
|---|---|---|---|---|
| `apiVersion` | `concordia.io/v1alpha1` | no | | Schema version. |
| `kind` | `Workspace` | no | | Document kind. |
| `metadata` | object | no | | Free-form metadata. |
| `architecture` | object | no | | The CALM source. |
| `observation` | object | yes | | The code graph source and the project to observe. |
| `environments` | list | no | | Deployment environments. See [environments](#environments). |
| `limits` | object | no | | Resource limits of a run. See [limits](#limits). |
| `bindings` | object | no | | Binding files. |
| `policies` | object | no | | Policy files and policy packs. |
| `exceptions` | object | no | | Baseline and waiver files. |
| `adr` | object | no | | Decision register, lifecycle profile, and triggers. See [adr](#adr). |
| `trust` | object | no | | Extra authority dimensions and the data classification list. See [trust](#trust). |
| `assessments` | object | no | | Assessment paths, trust anchor, authorities, required assessments, and analysis limits. See [assessments](#assessments). |
| `assurance` | object | no | | Rendered sources, plans, producers, provenance, records, evaluators, verification results, and quality scenarios. See [assurance](#assurance). |
| `projection` | object | no | | Diagram output. |
| `views` | list | no | | Architecture views that must exist. See [views](#views). |
| `check` | object | no | | Check thresholds. |
| `report` | object | no | | Report outputs. |
| `profiles` | map | no | | Named partial workspaces. |

## metadata

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | none | Workspace name. Names the synthetic system in rendered views. Other keys are allowed. |

## architecture

| Key | Type | Default | Description |
|---|---|---|---|
| `source` | string | none | Path to the CALM architecture document. `--architecture` overrides it. |
| `patterns` | string[] | `[]` | Paths to CALM patterns (JSON Schemas) that the architecture must satisfy. |
| `decorators` | string[] | `[]` | Paths or `https` URLs of CALM decorator documents. See [architecture](architecture.md#decorators). |
| `urlMapping` | string | none | Path to a JSON file `{url: relativePath}`, the same format as the CALM CLI `-u` option. See [architecture](architecture.md#url-mapping). |

## observation

| Key | Type | Default | Description |
|---|---|---|---|
| `provider` | `codebase-memory-mcp` | `codebase-memory-mcp` | Code graph provider. |
| `executable` | string | `codebase-memory-mcp` | CBM executable. |
| `timeoutSeconds` | number > 0 | `300` | Timeout of a CBM call. |
| `indexTimeoutSeconds` | number > 0 | `1800` | Timeout of an index call. |
| `retries` | integer 0..3 | `1` | Retries of a failed CBM call. |
| `indexMode` | `fast`, `moderate`, `full` | `moderate` | CBM index mode. `--mode` overrides it. |
| `projects` | array of projects | required | One or more projects with different names. The first project is the primary project: its paths have no prefix. Paths of other projects have the prefix `@<name>/` (for example `@payments/src/app.ts`) in findings, bindings, coverage and changed files. `root` is relative to the workspace root and can point outside it (`../other-repo`). |
| `runtime` | object | none | Runtime-trace evidence. See [observation.runtime](#observationruntime). |

Concordia runs CBM as one long-lived process for the whole command, not one process for each call. It starts `<executable>` with no arguments (an MCP server on stdio) on the first call, sends every call to it, one at a time, and ends it when the command ends. The process runs in an empty directory in the user cache (`concordia/cbm-session`), never in your repository, because CBM indexes the working directory of a session when it starts and its `auto_index` setting is on by default. The first session creates one empty CBM project for that directory; it is not used.

`timeoutSeconds` and `indexTimeoutSeconds` limit one call, including the start of the process when the call needs it. A call that times out, or a process that exits or answers in a way that Concordia cannot read, gives `CO-OBS-001` with the end of the stderr of CBM. Concordia then kills that process and starts a new one for the next call, and `retries` repeats a read-only call (`index_status`, `get_graph_schema`, `query_graph`, `list_projects`) with a new process. A CBM that refuses to run next to a CBM daemon of another build gives `CO-OBS-001` that is not retried: its detail says that the executable is a different build than the running daemon, and that every CBM client must use one binary. With `--trace`, each call writes the event `tools/call <tool>`.

### observation.projects[]

| Key | Type | Default | Description |
|---|---|---|---|
| `name` | string | required | CBM project name. |
| `root` | string | `.` | Repository root, relative to the workspace. |
| `include` | glob[] | `[]` | Files that count toward coverage. An empty list means all files. |
| `exclude` | glob[] | `[]` | Files that are left out of the check. They stay in the coverage total as excluded files. |

### observation.runtime

Runtime-trace evidence: OTLP/JSON trace files, one export request per line. The traces add `calls-service` edges to the observed graph.

| Key | Type | Default | Description |
|---|---|---|---|
| `traces` | string[] | required, at least one | Globs of trace files, relative to the workspace root. A list that matches no file gives `CO-OBS-012`. |
| `environment` | string | none | Declared environment that the traces describe. Spans of another environment are skipped. It must match an `environments` entry (`CO-WRK-002`). |
| `maxAgeDays` | integer >= 1 | none | Spans that ended more than this many days ago are skipped (`CO-OBS-013`). Without it, there is no age limit. |

## environments

A list of deployment environments. The schema is closed: an unknown key is `CO-WRK-002` (exit 2).

| Key | Type | Default | Description |
|---|---|---|---|
| `id` | string | required | Environment id. Pattern `^[a-z][a-z0-9-]*$`. Unique in the list. |
| `sources` | glob[] | required | Files that belong to the environment, relative to the workspace root. At least one glob. |
| `cluster` | string | none | Cluster name of the environment. |
| `description` | string | none | Free text. |

With declared environments, `check` also reads the container occurrence and selector facts (`uses-image`, `selects-workload`, `policy-selects-workload`, `disruption-policy-target`) even when no rule uses them, because the manifest cross-check compares the manifests of the environment sources with them (see [coverage](results.md#coverage)). A file that matches the `sources` of several environments (a shared base) is attributed to each of them: one fact for each environment. A file that matches none is not attributed to an environment. Duplicate ids and an `observation.runtime.environment` that no `environments` entry declares (also when `environments` is missing) give `CO-WRK-002`.

```yaml
environments:
  - id: prod
    sources: [k8s/base/**, k8s/prod/**]
    cluster: prod-eu
  - id: dev
    sources: [k8s/base/**, k8s/dev/**]
```

## limits

Resource limits of a run. The schema is closed: an unknown key, or a value below 1, is `CO-WRK-002` (exit 2). A key that is not set has its default, which is far above the size of common repositories.

| Key | Type | Default | Description |
|---|---|---|---|
| `graph.maxNodes` | integer | `5000000` | The most nodes in the observed graph of the run. |
| `graph.maxEdges` | integer | `20000000` | The most edges that the provider gave (mapped facts and unsupported facts). |
| `traversal.maxVisited` | integer | `10000000` | The most nodes that one cycle or path traversal visits before it stops. |
| `evidence.maxFacts` | integer | `20000000` | The most facts (evidence edges and synthesized entities) of the run. |

A run that goes above a limit reports the limit, the configured value, and the observed value (`CO-CON-030`, and `coverage.limits.breaches` in `results.json`). Concordia never drops nodes, edges, facts, or findings to stay under a limit:

- A graph or evidence limit applies to every rule. A rule without a finding is `unknown` with the reason `CO-CON-030` and is never `pass`. A rule with a finding keeps its `violation`.
- A traversal that reaches `traversal.maxVisited` stops and is marked incomplete (`coverage.traversalIncomplete` of the rule). Findings that it already found stay. Without a finding the rule is `unknown`, never `pass`. The observed value of a traversal is the number of nodes visited when it stopped, so it is a lower bound.

```yaml
limits:
  graph: {maxNodes: 2000000}
  traversal: {maxVisited: 1000000}
```

## bindings

| Key | Type | Default | Description |
|---|---|---|---|
| `files` | glob[] | `["bindings/concordia.bindings.yaml"]` | Binding files. See [bindings](bindings.md). |

## policies

| Key | Type | Default | Description |
|---|---|---|---|
| `files` | glob[] | `["policies/**/*.policy.yaml"]` | Policy pack files. See [policies](policies.md). |
| `packs` | array of pack entries | `[]` | Policy packs to use: packs built into the binary and vendored files. A profile replaces the whole list. |

`check --policy` replaces both `files` and `packs`: a run with `--policy` reads only the files that the option names, and it locks no pack.

A file that an entry of `packs` names is read once, even when a `files` glob also matches it. The default glob matches `policies/vendor/`, where `concordia policy add` puts vendored packs.

### `policies.packs[]`

| Key | Type | Default | Description |
|---|---|---|---|
| `use` | string | required | `builtin:<name>` for a pack in the binary, or a workspace-relative path of a pack file. A URL is not allowed (`CO-POL-014`): vendor it with `concordia policy add`. |
| `sha256` | string `sha256:<64 hex digits>` | none | Digest of the pack file. Required for a file (`CO-POL-013` otherwise). Not allowed for a built-in pack, because the binary pins it. A file whose digest differs gives `CO-POL-010`. |
| `rules` | map of rule ID to override | none | Changes of single rules, without a copy of the pack. An ID that the pack does not declare gives `CO-POL-015`. |

### `policies.packs[].rules.<rule-id>`

| Key | Type | Description |
|---|---|---|
| `severity` | `error`, `warning`, `info` | Replaces the severity of the rule. |
| `enabled` | boolean | `false` drops the rule from the run. |
| `evidence` | object | Partial `evidence` block: `minimumConfidence`, `minimumCoverage` (or its alias `coverage.files`), `coverage.bytes`, `coverage.supportedLanguages`, `edgeKinds` (a map of edge kind to minimum confidence, replaced by key), and `onUnknown`. A key that you set replaces the key of the rule. See [policies](policies.md#the-evidence-block). |
| `owner` | string | Replaces the owner of the rule. |

```yaml
policies:
  files: [policies/**/*.policy.yaml]
  packs:
    - use: builtin:core
      rules:
        CORE-DECL-001: { severity: warning }
        CORE-SEC-001: { enabled: false }
        CORE-CYC-001: { evidence: { minimumCoverage: 0.8 }, owner: team-platform }
    - use: policies/vendor/acme-arch@1.2.0.policy.yaml
      sha256: sha256:<64 hex digits>
```

For a guide, see [use policy packs](../how-to/use-policy-packs.md).

## exceptions

| Key | Type | Default | Description |
|---|---|---|---|
| `baseline` | string | `exceptions/concordia.baseline.json` | Baseline file. |
| `waivers` | string | `exceptions/waivers.yaml` | Waiver register. See [exceptions](exceptions.md). |
| `maxExpiryDays` | integer ≥ 1 | none | The longest duration of a scoped waiver in days. A scoped waiver above it is invalid (`CO-EXC-002`, exit 2). It does not bound a finding waiver. |

## trust

Declares the trust context that CALM nodes, relationships, and binding entries can use. Without this key, only the built-in dimensions exist and no classification is declared. A value that a node or binding uses must come from these lists (`CO-SEC-001`, exit 2).

| Key | Type | Default | Description |
|---|---|---|---|
| `dimensions` | string[] | `[]` | Authority dimension names that you add to the built-in names `organization`, `account`, `principal`, `zone`, and `region`. Each name starts with a letter and has only letters, digits, `-`, and `_`. A name appears once. |
| `classifications` | string[] | `[]` | The allowed data classification values, for example `public`, `internal`, `restricted`. A missing classification stays `unknown`: Concordia never infers one. A value appears once. |

## assessments

Configures security and quality assessments, the authorities that approve them, and the assessments that the workspace requires. When Concordia verifies an approval, it reads `trustAnchor`, `authorities`, and `required` from the git revision of the trust anchor, not from the working tree. See [diagnostics](diagnostics.md#assessment-currency-asm) for the codes.

| Key | Type | Default | Description |
|---|---|---|---|
| `paths` | string[] | `[assessments]` | Workspace-relative directories or files with the assessment, assumption, approval, and revocation documents. |
| `trustAnchor` | object | none | The git revision that verification reads from. See [assessments.trustAnchor](#assessmentstrustanchor). |
| `authorities` | list | `[]` | The parties that can approve. See [assessments.authorities[]](#assessmentsauthorities). |
| `required` | list | `[]` | Assessments that the workspace requires. See [assessments.required[]](#assessmentsrequired). |
| `analysisLimits` | object | none | Limits of the change-impact traversal. See [assessments.analysisLimits](#assessmentsanalysislimits). The top-level [`limits`](#limits) key is a different key and is not reused. |

There is no `assessments.enforcement` key and no `assessments.limits` key. The enforcement mode of the review rules comes from [`check.enforcement`](#checkenforcement), and the default is `advisory`.

### assessments.trustAnchor

| Key | Type | Default | Description |
|---|---|---|---|
| `ref` | string | required | A git reference, for example `origin/main`. Concordia resolves it with git, without network access. A reference that is missing, shallow, or not an ancestor of `HEAD` gives `CO-ASM-007`, and the dependent currency is `unknown` with the reason `trust-anchor-unreadable`. |

### assessments.authorities[]

| Key | Type | Default | Description |
|---|---|---|---|
| `id` | string | required | Authority id. Pattern `^[a-z][a-z0-9-]*$`. Unique in the list. |
| `role` | `reviewer`, `exception`, `governance` | required | What the authority approves: assessments, scoped waivers, or changes of the review configuration. Any other value is invalid (exit 2). |
| `mechanism` | `protected-review`, `signed` | `protected-review` | How an approval is verified. `protected-review`: the record exists at the trust anchor revision. `signed`: the record has a cosign signature of a listed signer. |
| `reviewers` | string[] | `[]` | Reviewer identities that a `protected-review` authority accepts. |
| `paths` | glob[] | `[]` | Workspace-relative paths where a `protected-review` authority accepts records. |
| `signers` | list | `[]` | Signers that a `signed` authority accepts. Each entry is `{identity, issuer}` (keyless) or `{publicKey}` (a key file), not both. |

### assessments.required[]

| Key | Type | Default | Description |
|---|---|---|---|
| `id` | string | required | Id of the requirement. Unique in the list. |
| `assessment` | string | required | Id of the assessment that must have a current approval. |
| `type` | `security`, `quality` | required | The assessment type. |
| `scope` | object | none | Selects the covered CALM elements: `calmNodes`, `nodeTypes`, and `tags`, each a string list. |
| `environment` | string | required | The environment that the approval must bind. |
| `authority` | string | required | The `id` of the authority that must approve. |

### assessments.analysisLimits

| Key | Type | Default | Description |
|---|---|---|---|
| `maxDepth` | integer ≥ 1 | `64` | The deepest path of the dependency traversal. |
| `maxVisited` | integer ≥ 1 | `100000` | The most nodes that one traversal visits. |
| `maxSeconds` | integer ≥ 1 | `120` | The longest time of one traversal in seconds. |

A key that is not set takes its default. The defaults come from the [benchmark](../explanation/review-currency.md#default-limits): the largest graph that it measures has a longest path of three nodes and a largest traversal of 12 nodes, so the defaults leave a wide margin. A traversal that reaches a limit stops and reports an incomplete analysis (`CO-ASM-008`); the dependent currency is `unknown`. A result of a stopped traversal is never stored in the [incremental cache](../explanation/review-currency.md#incremental-evaluation) that `check --incremental` writes to `.concordia/cache/impact/`. The limits are not part of the cache key: a changed limit changes the fingerprint of each stored comparison, so each is evaluated again.

```yaml
trust:
  dimensions: [tenant]
  classifications: [public, internal, restricted]
exceptions:
  maxExpiryDays: 30
assessments:
  trustAnchor: { ref: origin/main }
  authorities:
    - id: security-review
      role: reviewer
      reviewers: [alice@example.com]
      paths: [assessments/approvals/security/**]
    - id: platform-signers
      role: reviewer
      mechanism: signed
      signers:
        - { identity: release-bot@example.com, issuer: "https://accounts.example.com" }
  required:
    - { id: edge-exposure, assessment: ASM-EDGE, type: security, scope: { calmNodes: [api-gateway] }, environment: production, authority: security-review }
  analysisLimits: { maxDepth: 64, maxVisited: 100000, maxSeconds: 120 }
```

## adr

Turns on ADR governance. Without this key, no decision rule has anything to evaluate. See [ADR governance](adr-governance.md).

| Key | Type | Default | Description |
|---|---|---|---|
| `files` | glob[] | `[]` | Decision register files (`kind: DecisionRegister`). A path without glob characters must exist. A profile replaces the whole list. |
| `lifecycle` | object | `{active: [accepted]}` | Lifecycle profile with the keys `active` (string[], the statuses that count as active, default `[accepted]`) and `statusMap` (map of status text to provider status, default `{}`). See [lifecycle](adr-governance.md#lifecycle-profile). |
| `triggers` | list | `[]` | Scope triggers: changes that a decision should justify. Each has `id` (required), `category`, `paths` (glob[]), `architecture` (`node-added`, `node-removed`, `relationship-added`, `relationship-removed`), `nodeTypes`, and `tags`. See [triggers](adr-governance.md#triggers). |

## assurance

Declares the evidence that the assurance adapters read: manifests that Concordia renders, Terraform or OpenTofu plans, signed statements, deployment records, check results, and quality scenarios. Without this key, Concordia reads none of them and `results.json` has no `assurance` object. For what the adapters can claim, see [assurance](assurance.md).

The schema is closed. An unknown key, a missing required key, or a producer without `authentication` is `CO-PRV-001` (exit 2). Every `environment` must be an `id` of [`environments`](#environments) (`CO-WRK-017`, exit 2). Every path or glob is relative to the workspace root and uses `/`: an absolute path, a Windows drive, or a `..` element gives `CO-PRV-002` (exit 2). A profile can set `assurance` as it can set `assessments`. A glob that matches no file is not an error: it gives the warning `CO-PRV-011` and the dependent check is `unknown`.

| Key | Type | Default | Description |
|---|---|---|---|
| `rendering` | object | none | The sources that Concordia renders, and the confinement settings. See [assurance.rendering](#assurancerendering). |
| `plans` | list | `[]` | Plan files for Terraform or OpenTofu. See [assurance.plans[]](#assuranceplans). |
| `producers` | list | `[]` | The producers that Concordia trusts to sign statements. See [assurance.producers[]](#assuranceproducers). |
| `provenance` | object | none | Build provenance statements and the trust contract of builds. See [assurance.provenance](#assuranceprovenance). |
| `records` | object | none | Deployment records and workload observations. See [assurance.records](#assurancerecords). |
| `evaluators` | list | `[]` | The effective-policy evaluators that run for an environment. See [assurance.evaluators[]](#assuranceevaluators). |
| `verification` | object | none | Check results and the trusted root for signature verification. See [assurance.verification](#assuranceverification). |
| `quality` | object | none | Quality scenario files, measurements, and the areas that need a scenario. See [assurance.quality](#assurancequality). |

### assurance.rendering

| Key | Type | Default | Description |
|---|---|---|---|
| `sources` | list | required, at least one | The sources to render, one entry for each environment and tool. See [assurance.rendering.sources[]](#assurancerenderingsources). |
| `requireOsSandbox` | boolean | `false` | `true` makes the OS sandbox (Linux `bwrap`) mandatory. Without it the run stops with `CO-PRV-004` (exit 5). |
| `limits.timeoutSeconds` | integer ≥ 1 | `120` | The longest time of one render. |
| `limits.maxOutputBytes` | integer ≥ 1 | `67108864` (64 MiB) | The most bytes of rendered output. |
| `limits.maxResources` | integer ≥ 1 | `20000` | The most resources of one render. |

A render over a limit gives the coverage `partial` or `failed` and the warning `CO-PRV-005`. Concordia never cuts the output to fit.

### assurance.rendering.sources[]

| Key | Type | Default | Description |
|---|---|---|---|
| `environment` | string | required | An environment id. |
| `tool` | `helm`, `kustomize`, `plain` | required | `helm` runs `helm template`, `kustomize` runs `kustomize build`, and `plain` reads manifests without running a program. |
| `path` | string | required | The chart directory, the overlay directory, or the directory or file of plain manifests. |
| `valuesFiles` | string[] | `[]` | Helm values files, in order. Only for `helm`. |
| `release` | string | none | The Helm release name. Only for `helm`. |
| `namespace` | string | none | The Helm release namespace. Only for `helm`. |

`valuesFiles`, `release`, and `namespace` on a `kustomize` or `plain` source are invalid. An environment, tool, and path appear once. A `helm` or `kustomize` source needs the renderer in the [lock file](#concordialock): see `resolved.renderers` below.

### assurance.plans[]

| Key | Type | Default | Description |
|---|---|---|---|
| `environment` | string | required | An environment id. |
| `tool` | `terraform`, `opentofu` | required | The tool that made the plan. |
| `file` | string | required | The output of `show -json`. |
| `statement` | string | none | The in-toto statement that binds the plan to a revision. Without it the plan is `content not verified`. |
| `maxAgeDays` | integer ≥ 1 | none | The oldest age of the statement that is still admissible. |

### assurance.producers[]

| Key | Type | Default | Description |
|---|---|---|---|
| `id` | string | required | Producer id. Pattern `^[a-z][a-z0-9-]*$`. Unique in the list. |
| `claims` | string[] | required, at least one | What the producer is trusted to state: `build-provenance`, `check-result`, `measurement`, `plan`, `deployment-record`, `workload-observation`. A producer is trusted for these kinds and no others. |
| `authentication.method` | `cosign-key`, `cosign-keyless` | required | How the producer signs. |
| `authentication.key` | string | none | For `cosign-key`: a workspace-relative public key file, or a KMS or `env://` reference. A reference with a scheme is not a path. |
| `authentication.issuer`, `authentication.identity` | string | none | For `cosign-keyless`: the OIDC issuer and the certificate identity of the signer. Both are required. |

A `cosign-key` producer with `issuer` or `identity`, or a `cosign-keyless` producer with `key`, is invalid.

### assurance.provenance

| Key | Type | Default | Description |
|---|---|---|---|
| `statements` | glob[] | required | Build provenance statements. |
| `builders` | string[] | `[]` | The builder ids that the trust contract accepts. |
| `sourceRepositories` | string[] | `[]` | The ids of the declared source repositories that a build may come from. |
| `maxAgeDays` | integer ≥ 1 | none | The oldest age of a statement that is still admissible. |

### assurance.records

| Key | Type | Default | Description |
|---|---|---|---|
| `deployments` | glob[] | `[]` | Deployment record statements. |
| `observations` | glob[] | `[]` | Workload observation statements. |
| `maxAgeDays` | integer ≥ 1 | none | The oldest age of a record that is still admissible. |

### assurance.evaluators[]

| Key | Type | Default | Description |
|---|---|---|---|
| `environment` | string | required | An environment id. One entry for each environment. |
| `kinds` | `network-policy`, `rbac` | required, at least one | The evaluators that run for the environment. |
| `assumes` | `network-policy-enforced`, `rbac-authorizer-enabled` | `[]` | The enforcement assumptions that you declare. An evaluator result that needs an assumption that is not declared is `unknown` with the reason `enforcement-not-declared`. |

### assurance.verification

| Key | Type | Default | Description |
|---|---|---|---|
| `results` | glob[] | required | Check result statements. |
| `maxAgeDays` | integer ≥ 1 | none | The oldest age of a result that is still admissible. |
| `trustedRoot` | string | none | A workspace-relative Sigstore `trusted_root.json`. With it, every statement verification passes it to cosign, which then verifies without network access. Under `--offline` it is the only way to verify a statement. A path outside the workspace root is `CO-PRV-002` (exit 2). |

### assurance.quality

| Key | Type | Default | Description |
|---|---|---|---|
| `scenarios` | glob[] | required | `QualityScenarioSet` files. |
| `measurements` | glob[] | `[]` | Measurement statements. |
| `requiredAreas` | string[] | `[]` | Quality areas that must have at least one scenario. An area without a scenario is a finding. |

```yaml
environments:
  - { id: production, sources: [deploy/**] }
  - { id: staging, sources: [deploy/**] }
assurance:
  rendering:
    limits: { timeoutSeconds: 120 }
    sources:
      - { environment: production, tool: helm, path: deploy/chart, valuesFiles: [deploy/values/prod.yaml], release: shop, namespace: shop }
      - { environment: staging, tool: kustomize, path: deploy/overlays/staging }
  plans:
    - { environment: production, tool: terraform, file: build/plans/prod.json, statement: build/plans/prod.intoto.json, maxAgeDays: 7 }
  producers:
    - id: ci-build
      claims: [build-provenance, check-result, measurement, plan, deployment-record]
      authentication:
        method: cosign-keyless
        issuer: "https://token.actions.githubusercontent.com"
        identity: "https://github.com/acme/shop/.github/workflows/build.yml@refs/heads/main"
  provenance:
    statements: ["build/attestations/*.json"]
    builders: ["https://github.com/acme/shop/.github/workflows/build.yml"]
    sourceRepositories: [shop]
    maxAgeDays: 30
  records: { deployments: ["build/records/deploy-*.json"], observations: ["build/records/observed-*.json"], maxAgeDays: 7 }
  evaluators:
    - { environment: production, kinds: [network-policy, rbac], assumes: [network-policy-enforced, rbac-authorizer-enabled] }
  verification: { results: ["build/results/*.json"], maxAgeDays: 14, trustedRoot: .sigstore/trusted_root.json }
  quality: { scenarios: ["quality/*.yaml"], measurements: ["build/measurements/*.json"], requiredAreas: [operability] }
```

## projection

| Key | Type | Default | Description |
|---|---|---|---|
| `provider` | `structurizr` | `structurizr` | Diagram provider. |
| `output` | string | `build/concordia/structurizr` | Output directory of `render`. |
| `allowIncludes` | list of paths | none | Workspace-relative repository paths that the Structurizr DSL of a rendering may include. Any other `!include`, and every URL, is refused (`CO-DGM-003`). |

## views

A list of the architecture views that the workspace requires. `views` is a top-level key: a profile cannot set or overlay it. Each entry declares one view; the schema is closed, so an unknown key, a missing required key, or a `selector` with another key than `title` or `anchor` is rejected with exit code 2 (`CO-DGM-001`, which names the view and the key).

| Key | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Durable identity of the view: `[a-z][a-z0-9-]*`, unique in the workspace. Never derived from a position or a provider ordinal. |
| `kind` | enum | yes | `system-context`, `container`, `component`, `deployment`, `trust`, or `data-flow`. |
| `origin` | enum | yes | `generated` (Concordia writes the diagram) or `authored` (the team writes it). `trust` and `data-flow` views are `authored` only. |
| `owner` | string | yes | The team that answers for the view. |
| `scope` | list of CALM node IDs | yes | The nodes that the view is about. At least one. |
| `abstraction` | enum | yes | `system`, `container`, `component`, or `deployment`. The kinds `system-context`, `container`, `component`, and `deployment` fix it to `system`, `container`, `component`, and `deployment`; `trust` and `data-flow` declare it. |
| `environment` | string | for `deployment` | An environment that [`environments`](#environments) declares. Another kind may name one to limit its observed projection to that environment. |
| `coverage.elements`, `coverage.relationships` | lists of CALM IDs | no | What the view must show. Default: every element of the scope at the declared abstraction, and every declared relationship between them. |
| `source.path`, `source.format` | string, enum | for `authored` | The diagram file and its format: `mermaid`, `plantuml`, `dsl`, `png`, `jpeg`, or `gif`. |
| `source.selector` | object | no | `title` or `anchor` (exactly one) of the diagram block in the file. An index or a provider ordinal is not accepted. |
| `output.path` | string | for `generated` | Workspace-relative path of the committed view. It must be in canonical form (no `.` or `..` segments, no repeated or backslash separators), inside the workspace, not under `projection.output`, not written by another view, and not matched by a Git ignore rule. |
| `output.exports[]` | list of `{format, path}` | no | Further committed forms of a generated view. `format` is `json`, `mermaid`, `plantuml`, or `dot`. `static` is rejected: it has no canonicalization rule. The same path rules apply. |
| `projection` | object | no | `merge` (`binding`), `edgeKinds` (edge kinds from the vocabulary of `check`), `selfEdges` (`omit`, `keep`; default `omit`), `parallelEdges` (`merge` by kind and protocol, or `separate`; default `merge`), and `semantic` (the display texts that count for the semantic digest: `name`, `description`, `tags`). |
| `mapping.elements` | map | for `authored`, except raster | Diagram element identifier (for Mermaid, the node ID) to CALM node ID. |
| `mapping.depicts` | object | no | Raster sources only: `digest` (`sha256:<hex>` of the image), `elements`, and `relationships` that the image claims to show. |
| `comparison` | object | no | `against` (`declared`, `observed`; default both), `minimumConfidence`, and `severity` (`error`, `warning`, or `info`; default `error`). |

`concordia model validate`, `check`, and `render --views` check the definitions against the other inputs: the `environment` names a declared environment, the IDs in `scope`, `coverage`, and `mapping.elements` are in the CALM model, `projection.edgeKinds` are known edge kinds, and the paths of a generated view (the output, the exports, and the manifest `<output.path>.manifest.json`) are not ignored by Git. A tracked path that an ignore rule also matches is accepted. Each problem is `CO-DGM-001` or `CO-DGM-002` with exit code 2.

The same commands print an information diagnostic (`CO-DGM-005`, no exit code) for each default view that the workspace does not declare: a `system-context` view for each software system, a `container` view for each system that contains a `service`, and a `deployment` view for each production environment (the environments with the ID `prod` or `production`). The suggestions are advice, not standards. Declaring every one of them silences the output. A workspace that declares neither `views` nor `environments` gets no suggestion.

```yaml
environments:
  - id: staging
    sources: [deploy/staging/**]
views:
  - id: payments-container
    kind: container
    origin: generated
    owner: team-payments
    scope: [payments-system]
    abstraction: container
    environment: staging
    output:
      path: docs/architecture/views/payments-container.dsl
      exports:
        - format: mermaid
          path: docs/architecture/views/payments-container.mmd
    comparison: {against: [declared, observed], minimumConfidence: syntactic, severity: warning}
  - id: payments-context
    kind: system-context
    origin: authored
    owner: team-payments
    scope: [payments-system]
    abstraction: system
    source: {path: docs/arch/payments.md, format: mermaid, selector: {title: Payments context}}
    mapping:
      elements: {customer: customer, payments: payments-system}
projection:
  allowIncludes: []
```

The workspace `test/fixtures/views/concordia.yaml` in the repository holds this example with its CALM 1.2 architecture; `concordia model validate` accepts it.

## check

| Key | Type | Default | Description |
|---|---|---|---|
| `minimumCoverage` | number 0..1 | `0.95` | Required file coverage. `--minimum-coverage` overrides it. |
| `unknown` | `fail`, `warn`, `ignore` | `fail` | What an unknown rule verdict does. `--unknown` overrides it. |
| `failOn` | `error`, `warning`, `info` | `error` | Minimum active severity that yields failure. `--fail-on` overrides it. |
| `enforcement` | list | none | The enforcement mode by rule family, pack, or rule, and environment. See [check.enforcement](#checkenforcement). |
| `rollback` | object | none | Runs the typed, decision-governance, and review rules as `shadow` until a date. See [check.rollback](#checkrollback). |

### check.enforcement

An ordered list that sets the enforcement mode of rules by family, pack, or rule ID, and optionally by environment. The schema is closed.

| Key | Type | Default | Description |
|---|---|---|---|
| `family` | string | none | The `family` that the rule declares (see [policies](policies.md)). Pattern `^[a-z][a-z0-9-]*$`. |
| `pack` | string | none | The ID of the policy pack of the rule. |
| `rules` | string[] | none | Rule IDs. At least one. |
| `environment` | string | all | A declared environment ID. The entry then applies to the obligations of that environment only. |
| `mode` | `mandatory`, `advisory`, `shadow` | required | The enforcement mode. |

An entry needs at least one of `family`, `pack`, and `rules`. When an entry gives several selectors, all of them must match.

The mode of a rule in an environment is the first of these that applies:

1. The first `check.enforcement` entry that matches the rule and the environment. The order of the list decides: write the narrow entries first.
2. The `enforcement` that the rule declares.
3. The default: `mandatory` for rules that use only the code evidence families, `advisory` for typed rules (rules with an `obligation` block).

| Mode | Effect |
|---|---|
| `mandatory` | Findings and `unknown` verdicts count in the exit code (`1`, `4`, `7`). |
| `advisory` | The findings are shown with enforcement `advisory` (SARIF level `note`) and the verdicts stay. They do not change the exit code. |
| `shadow` | The findings are written to `results.json` only. Pretty, SARIF, and JUnit do not show them, the exit code does not count them, and `baseline create` does not accept them. |

A rule that runs as `mandatory` by a workspace entry or by its own `enforcement` needs guarantees: an environment that `environments` declares, the maturity `stable` of its evidence family in the provider profile (a rule of the assurance families `effective-access`, `check-result`, and `assurance-claim` has the maturity `advisory`, so it can never run as `mandatory` in this release), and a coverage predicate for the family in the run. If one is absent, `check` stops with `CO-POL-018` (exit 2), names the rule, the environment, and the absent guarantee, and gives no result. Concordia does not lower the mode: set `advisory` or `shadow` here to run the rule. See [roll out new obligations](../how-to/roll-out-new-obligations.md).

```yaml
check:
  enforcement:
    - {family: deployment-security, environment: staging, mode: mandatory}
    - {family: deployment-security, environment: production, mode: advisory}
    - {pack: infra, mode: shadow}
```

### check.rollback

Switches new enforcement off for a time. While the rollback is active, every rule that uses typed obligations, and every rule of the `decision-governance` family (the [ADR governance](adr-governance.md) pack), and every review rule (the `assessment-currency` family: `REV-APR-001`, `REV-DSP-001`, `REV-CAP-001`, and `REV-GOV-001` of the `review-currency` pack; see [assessments](assessments.md)), runs as `shadow`, whatever `check.enforcement` and the rule say. This includes a review rule that `check.enforcement` sets to `mandatory`. Findings and bundles are still recorded, and the verdicts do not change. The code families and `view-currency` keep their mode.

| Key | Type | Default | Description |
|---|---|---|---|
| `reason` | string | required | Why enforcement is disabled. |
| `authorizedBy` | string | required | Who authorized it. Recorded as written; Concordia does not check it. |
| `expires` | date `YYYY-MM-DD` | required | The last day of the rollback (UTC). |

The run manifest and `summary.rollback` in `results.json` record the reason, the authority, and the expiry, and the `pretty` report says that new enforcement was disabled: the output is not a successful typed, governance, or review check. After the `expires` day, `check` fails with `CO-WRK-018` (exit 2).

```yaml
check:
  rollback:
    reason: "false positives on the new ingress rules (INC-1234)"
    authorizedBy: jane.doe@example.com
    expires: "2026-11-15"
```

## report.outputs

A list of reports that every `check` writes.

| Key | Type | Default | Description |
|---|---|---|---|
| `format` | `json`, `junit`, `sarif`, `pretty` | required | Report format. |
| `path` | string | required | Output path. |

When `report.outputs` is absent, the default is one entry: `json` at `build/concordia/results.json`. See [results](results.md).

## profiles

A map from a profile name to a partial workspace. The profile accepts the keys `metadata`, `architecture`, `observation`, `bindings`, `policies`, `exceptions`, `projection`, `adr`, `trust`, `assessments`, `assurance`, `check`, and `report`; it does not accept `apiVersion`, `kind`, `environments`, `limits`, or `views`. In `observation`, no key is required. `--profile <name>` merges the profile into the root values: objects merge deeply and arrays are replaced.

```yaml
profiles:
  ci:
    check:
      minimumCoverage: 0.98
```

## Defaults table

| Value | Default |
|---|---|
| `check.unknown` | `fail` |
| `check.minimumCoverage` | `0.95` |
| `check.failOn` | `error` |
| `observation.indexMode` | `moderate` |
| Results file | `build/concordia/results.json` |
| Coverage file | `build/concordia/coverage.json` |
| DSL file | `build/concordia/structurizr/workspace.dsl` |
| Run manifest | `.concordia/runs/<runId>/manifest.json` |
| Lock file | `.concordia.lock` |
| Index result file | `build/concordia/index-result.json` |
| `limits.graph.maxNodes` | `5000000` |
| `limits.graph.maxEdges` | `20000000` |
| `limits.traversal.maxVisited` | `10000000` |
| `limits.evidence.maxFacts` | `20000000` |

## .concordia.lock

The lock file is YAML in the workspace root. `check`, `model validate`, `init`, and `policy add` read it, and write it atomically. It records the toolchain, the digests of remote resources that `architecture.urlMapping` resolves, and the policy packs of `policies.packs`. `resolved.calm.schemaVersion` is the CALM release (`"1.2"` or `"1.3"`) of the architecture; a lock without it is 1.2. `init` writes `"1.3"`, or the release of the file given with `--calm`. `concordia lock update` re-pins it.

```yaml
lockVersion: 1
resolved:
  calm:
    schemaVersion: "1.2"
  cbm:
    executableVersion: <CBM version>
    adapterSchema: concordia-cbm/v2
    sha256: sha256:<digest of the CBM executable>   # optional: the build that the lock was written for
    normalizationVersion: "2.1"   # optional: the evidence normalization table of the lock
    profile:             # optional: the capability profile of the CBM build
      id: cbm-0.12.1
      digest: sha256:<digest of the profile>
  structurizr:           # optional
    cliVersion: <version>
  renderers:             # only when a declared source uses helm or kustomize
    helm:                # only when a source uses helm
      version: <version that `helm version --short` reports>
      sha256: sha256:<digest of the helm executable>
    kustomize:           # only when a source uses kustomize
      version: <version that `kustomize version` reports>
      sha256: sha256:<digest of the kustomize executable>
resources:
  - uri: <remote URL>
    path: <POSIX path relative to the workspace>
    sha256: <digest of the mapped file>
packs:                   # only when the workspace uses a pack
  - id: <metadata.id of the pack>
    version: <metadata.version of the pack>
    source: <the use value>
    sha256: <digest of the pack text>
```

Rules for the lock:

- A CBM version that differs from `resolved.cbm.executableVersion` gives the warning `CO-WRK-003`.
- A capability profile that differs from `resolved.cbm.profile` (another identifier or digest, or no profile) gives the warning `CO-WRK-003`. `init` writes the profile of the found build. A lock without `profile` is not compared.
- A CBM executable whose file digest differs from `resolved.cbm.sha256` gives the warning `CO-WRK-003`. It names the resolved path of the executable, the digest that was found, and the digest in the lock, and tells you to run `concordia lock update`. `init` and `lock update` write the digest of the executable that Concordia finds: the `observation.executable` of the workspace (default `codebase-memory-mcp`), found on the `PATH` as a shell does, with symbolic links resolved. A lock without `sha256` is not compared: it promised nothing, so it gives no warning. A value that is not `sha256:` and 64 lower-case hex digits gives `CO-WRK-002`. The warning never stops a run: the CBM version rule applies here, not the stricter [renderer](#concordialock) rule.
- A mapped resource whose digest differs from the lock, or whose file cannot be read, gives `CO-INT-012` (exit 6).
- A mapped resource that is not in the lock is added and the lock is written. With `--offline`, it gives `CO-INT-012` (exit 6).
- Credentials in a resource URL are removed before the URL is written.
- A pack in `policies.packs` is matched to its lock entry by `source`. The digest of a built-in pack is the digest of the text that is embedded in the binary.
- A pack whose digest differs from the lock gives `CO-INT-012` (exit 6), as for a resource. To accept the new pack, review it and remove its entry from the lock file, or run `concordia policy add <file> --force` for a vendored pack. A new binary can change a built-in pack: the same steps apply.
- A pack that is not in the lock is added and the lock is written, also with `--offline`: a pack is a local file, so no network is used.
- The `packs` key is written only when at least one pack is in use. A lock file without it is valid.
- `resolved.cbm.normalizationVersion` is the version of the evidence normalization table. `init` writes it and `concordia lock update` re-pins it. A lock without it is valid; `lock update` then lists the normalization version as `unrecorded` in its [digest-impact report](cli.md#lock-update).
- `resolved.renderers` pins the renderers that a source of [`assurance.rendering.sources`](#assurancerenderingsources) uses: only `helm` for a `helm` source, only `kustomize` for a `kustomize` source. A `plain` source needs none. `concordia lock update` writes the pins from the executable that it finds (the `HELM` or `KUSTOMIZE` environment variable, else the one on `PATH`) and removes the pin of a tool that no source uses. A lock without `renderers` is valid when no source needs a renderer.
- A renderer that a source uses must match its pin: the version and the `sha256` of the executable. A renderer that is missing, not pinned, or different from its pin gives `CO-PRV-003` (exit 5). There is no pin on first use: an unpinned render would not be reproducible.
- A lock file with another shape gives `CO-WRK-002`.

## Example

The complete workspace of the commerce example (`docs/examples/commerce/concordia.yaml`):

```yaml
apiVersion: concordia.io/v1alpha1
kind: Workspace
metadata:
  name: commerce-platform
architecture:
  source: architecture/commerce.calm.json
  urlMapping: architecture/url-mapping.json
observation:
  provider: codebase-memory-mcp
  projects:
    - name: commerce-example
      root: .
      include: [services/**, packages/**]
      exclude: [services/legacy/**]
  indexMode: fast
bindings:
  files: [bindings/concordia.bindings.yaml]
policies:
  files: [policies/**/*.policy.yaml]
exceptions:
  baseline: exceptions/concordia.baseline.json
  waivers: exceptions/waivers.yaml
check:
  minimumCoverage: 0.9
  unknown: fail
  failOn: error
```

## Related

- [CLI reference](cli.md) for the options that override these keys.
- [Architecture](architecture.md), [bindings](bindings.md), [policies](policies.md), and [exceptions](exceptions.md) for the files that the workspace points to.
- [Set up a workspace](../how-to/set-up-a-workspace.md) to create one.
