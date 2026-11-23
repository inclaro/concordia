# Architecture reference

This page describes how Concordia reads the CALM architecture document: the supported CALM versions, the `concordia` extensions, edge-kind tokens, the URL mapping, patterns, decorators, and the default C4 level of each node type.

To validate a document, run `concordia model validate` ([CLI reference](cli.md#model-validate)). For how the architecture relates to the code, see [how Concordia works](../explanation/how-concordia-works.md).

## CALM support

Concordia supports CALM releases 1.2 and 1.3. The `$schema` of the document is one of these:

- absent: CALM 1.2;
- exactly one of the release URLs below;
- a pattern: any other value that is not a CALM release or draft URL (see below).

The release URLs:

```text
https://calm.finos.org/release/1.2/meta/calm.json
https://calm.finos.org/release/1.3/meta/calm.json
```

Any other CALM release or draft `$schema` gives `CO-INT-003` (exit 2). Another `$schema` value names a pattern (a path relative to the architecture document, or a URL): the architecture is validated against that pattern instead of the release schema, and the release is the first CALM release that the pattern refers to. The document is validated against the CALM schema of its release (`CO-INT-002`); the two releases differ only in the decorator definition, so architecture validation gives the same result for both. `unique-id` values must be unique (`CO-INT-004`), and relationship endpoints must exist (`CO-INT-005`). A document that is not valid JSON gives `CO-INT-001`. The other rules of the CALM CLI give `CO-INT-014` (error) and `CO-INT-015` (warning), for example a node that no relationship references, an empty string, a placeholder value, an unknown interface, or a flow transition with an unknown relationship. See [diagnostics](diagnostics.md) and [`model validate`](cli.md#model-validate).

The architecture file comes from `architecture.source` in the [workspace](workspace.md#architecture) or from `--architecture`.

## Concordia extensions

The extensions are the optional `concordia` property on nodes and relationships, and a control configuration. Schema: `internal/intent/schemas/concordia-extensions.schema.json`. A violation gives `CO-INT-007`. No extension property accepts extra keys, except the control configuration.

### Node: `concordia`

| Key | Type | Description |
|---|---|---|
| `c4-level` | `person`, `softwareSystem`, `container`, `component` | Overrides the [default C4 level](#c4-levels). |
| `owner` | string | Owner of the node. |
| `tags` | string[] | Free tags. |
| `bindings` | array | Embedded bindings. An item has `kind` and `value` with `kind` one of `source-root`, `package`, `qualified-name`, `build-target`, or `otel-service`, or it is a deployed-resource item (`kind` `workload`, `image`, `route`, `infra-resource`, or `compose-service`) with identity fields. Every item can set `cardinality`, `agreement`, `status`, and `authority`. See [bindings](bindings.md#calm-embedded-bindings). |
| `trust` | object | The [trust context](trust-context.md#where-trust-is-declared) of the node: `dimensions` (name to value) and `classification`. An undeclared dimension or a classification outside the workspace list gives `CO-SEC-001`. |

### Relationship: `concordia`

The relationship extension has two optional keys: `observation` (below) and `trust`, the [trust context](trust-context.md#where-trust-is-declared) of the flow. Its `classification` is the data classification of the flow.

#### `concordia.observation`

| Key | Type | Description |
|---|---|---|
| `allowed-edge-kinds` | string[] | Edge-kind tokens that count as observation of this relationship. An unknown token gives `CO-INT-007`. See [edge-kind tokens](#edge-kind-tokens). |
| `minimum-confidence` | `resolved`, `runtime`, `syntactic`, `heuristic`, `unknown` | Lowest confidence that counts as evidence. |
| `required` | boolean | The declared relationship must be observed. Default: `false`. |

Example, from the relationship `payments-to-fraud` of `docs/examples/payments/architecture/payments.calm.json`:

```json
"concordia": {
  "observation": {
    "allowed-edge-kinds": ["IMPORTS", "CALLS"],
    "minimum-confidence": "syntactic",
    "required": true
  }
}
```

### Control configuration

A CALM control requirement can carry a `config` (or a `config-url`) that links the control to Concordia rules and to checks. Concordia reads a configuration only when `engine` is `concordia`. A configuration needs at least one of `rule-ids` and `check-ids`; one with neither stops the run with `CO-INT-007` (exit code `2`).

| Key | Type | Required | Description |
|---|---|---|---|
| `$schema` | string | no | Schema reference. |
| `control-id` | string | no | The control ID. `check --control` selects by it. |
| `engine` | `concordia` | yes | Marks the configuration as Concordia's. |
| `rule-ids` | string[] (at least 1) | one of `rule-ids` or `check-ids` | Rules that the control runs. |
| `check-ids` | string[] (at least 1) | one of `rule-ids` or `check-ids` | Checks whose results the control needs, for example `unit-tests`. A check is named by the `check` of the [check results](assurance.md#verification-results) that the workspace declares. A `require.checkResult` rule may name only a check that a control lists here or that a result source defines (`CO-POL-005`). |
| `policy-ref` | string | no | Policy pack file, relative to the architecture document. |
| `unknown-disposition` | `fail`, `warn`, `ignore` | no | What an unknown verdict of these rules and checks does. With `fail`, a check with no definite result fails the run (exit code `4`). `warn` and `ignore` leave the verdict `unknown` and the run unchanged. |
| `minimum-policy-coverage` | number 0..1 | no | Required coverage for these rules. For `check-ids`, the coverage is the share of the checks that have a definite result (`pass`, `violation`, or `not-applicable`). Below the minimum, the verdict of the control is at least `unknown` with the reason `coverage-incomplete`. |

Example, from the control `ctl-ledger-ownership` on `ledger-db` in `docs/examples/payments/architecture/payments.calm.json`:

```json
"config": {
  "control-id": "ctl-ledger-ownership",
  "engine": "concordia",
  "policy-ref": "../policies/payments.policy.yaml",
  "rule-ids": ["PAY-DB-001"],
  "unknown-disposition": "fail",
  "minimum-policy-coverage": 0.9
}
```

The verdict of a control is the worst verdict of its rules and its checks. A check with no admissible result is `unknown` with a closed reason (for example `no-evidence-supplied`), and the control says which check. The result of a control lists `ruleIds`, `checkIds`, and `reasons`; see [Checks of a control](results.md#checks-of-a-control).

## Edge-kind tokens

A token in `allowed-edge-kinds` or in a policy names a set of canonical kinds. Source: `internal/edgekind/edgekind.go`. An unknown token gives `CO-POL-004` in a policy.

- A kebab-case token (`references-secret`) is one canonical kind. Every canonical kind has one, except `unsupported` and `ambiguous`: they keep native evidence without an interpretation and are never tokens.
- An alias token is the native name of a family. It is a set of kinds and has no mode or view.
- No facet token matches `ambiguous-usage`: every reference of infrastructure has its own kind.

| Alias | Kinds |
|---|---|
| `IMPORTS` | `imports` (code imports only) |
| `USES_TYPE` | `uses-type` |
| `CALLS` | `calls` |
| `CALL_REFERENCE` | `references-callable` |
| `USAGE` | `ambiguous-usage` (code usage only: a native `USAGE` edge without `ref`) |
| `INHERITS` | `inherits` |
| `IMPLEMENTS` | `implements` |
| `HTTP_CALLS` | `calls-service` |
| `ASYNC_CALLS` | `dispatches-async` |
| `EMITS` | `emits-event` |
| `LISTENS_ON` | `listens-event` |
| `DATA_FLOWS` | `data-flows` |
| `READS` | `reads` |
| `WRITES` | `writes` |
| `CONFIGURES`, `configures` | `configures-env-access`, `configures-env-set`, `configures-env-read`, `configures-key-symbol`, `configures-helm-values` |
| `DEPLOYS`, `deploys` | `deploys-artifact` |

A rule that wants one configuration strategy names its kebab-case token. See [policies](policies.md#edge-kind-tokens) for the support status of each token in CBM 0.12.1.

## Canonical edge kinds

Each normalized evidence fact has one canonical kind, one role, and one resolution. Source: `internal/observation/evidence.go` (kinds) and `internal/observation/normalize.go` (rule table, adapter schema `concordia-cbm/v2`, normalization version `2.1`). The fields `Role`, `Resolution`, and `Confidence` are separate: none is derived from another.

| Kind | Class | Direction | Role | Resolution | Native edge |
|---|---|---|---|---|---|
| `contains` | code | folder or file to its member | `parsed-source-fact` | `exact` | `CONTAINS_FILE`, `CONTAINS_FOLDER` |
| `defines` | code | file or class to the symbol it defines | `parsed-source-fact` | `exact` | `DEFINES`, `DEFINES_METHOD` |
| `imports` | code | importing code to imported module | `parsed-source-fact` | `exact` | `IMPORTS` with no `via`, source of a code language |
| `uses-type` | code | user to type | none | none | none (CBM has no edge) |
| `inherits` | code | subclass to base class | `parsed-source-fact` | `exact` | `INHERITS` |
| `implements` | code | implementation to interface | `parsed-source-fact` | `exact` | `IMPLEMENTS` |
| `calls` | code | caller to callee | `parsed-source-fact` | `exact` | `CALLS` |
| `references-callable` | code | referrer to callable | `parsed-source-fact` | `exact` | `CALL_REFERENCE` |
| `ambiguous-usage` | code | user to used symbol | `inferred-association` | `inferred` | `USAGE` with no `ref` and no `via`, source of a code language |
| `constructs` | code | constructing code to type | none | none | none (CBM has no edge) |
| `reads` | code | reader to data | `parsed-source-fact` | `exact` | `READS` |
| `writes` | code | writer to data | `parsed-source-fact` | `exact` | `WRITES` |
| `handles-route` | code | handler to route | `parsed-source-fact` | `exact` | `HANDLES` |
| `calls-service` | code | caller to called service | `inferred-association` | `inferred` | `HTTP_CALLS`, `CROSS_HTTP_CALLS` |
| `dispatches-async` | code | dispatcher to async target | `inferred-association` | `inferred` | `ASYNC_CALLS`, `CROSS_ASYNC_CALLS` |
| `emits-event` | code | emitter to event or channel | `inferred-association` | `inferred` | `EMITS`, `CROSS_CHANNEL` |
| `listens-event` | code | listener to event or channel | `inferred-association` | `inferred` | `LISTENS_ON` |
| `data-flows` | code | source to sink | `inferred-association` | `inferred` | `DATA_FLOWS` |
| `calls-grpc` | code | caller to gRPC method | `inferred-association` | `inferred` | `GRPC_CALLS`, `CROSS_GRPC_CALLS` |
| `calls-graphql` | code | caller to GraphQL operation | `inferred-association` | `inferred` | `GRAPHQL_CALLS`, `CROSS_GRAPHQL_CALLS` |
| `calls-trpc` | code | caller to tRPC procedure | `inferred-association` | `inferred` | `TRPC_CALLS`, `CROSS_TRPC_CALLS` |
| `deploys-artifact` | infrastructure | script or chart to artifact (controller or task to artifact) | `parsed-source-fact` | `exact` | `DEPLOYS` `via` in `kubectl`, `helm`, `chart` |
| `selects-workload` | infrastructure | Service to workload | `parsed-source-fact` | `exact` | `INFRA_MAPS` `kind=selector` |
| `routes-to` | infrastructure | Ingress, HTTPRoute, or route to Service | `parsed-source-fact` | `exact` | `INFRA_MAPS` `kind` in `ingress`, `route`, `httproute` |
| `configured-connection` | infrastructure | Compose service to Compose service | `parsed-source-fact` | `exact` | `CONNECTS_TO` `via=env` |
| `uses-image` | infrastructure | workload container to image | `parsed-source-fact` | `exact` | `USAGE` `ref=image` |
| `built-from` | infrastructure | image to Dockerfile | `parsed-source-fact` | `exact` | `DEPENDS_ON` `via=build` |
| `base-image` | infrastructure | Dockerfile build stage to base image | `parsed-source-fact` | `exact` | `DEPENDS_ON` `via=from` |
| `copies-from-image` | infrastructure | Dockerfile to external image | `parsed-source-fact` | `exact` | `DEPENDS_ON` `via=copy-from` |
| `launches` | infrastructure | CI job, script, or Dockerfile instruction to program | `parsed-source-fact` | `exact` | `SPAWNS` `api` in `shell`, `dockerfile` |
| `overrides` | infrastructure | variable or patch to overridden variable or base resource | `parsed-source-fact` | `exact` | `OVERRIDE` `via` in `tfvars`, `patch` |
| `includes-infrastructure` | infrastructure | Terraform module or Kustomization to included files | `parsed-source-fact` | `exact` | `IMPORTS` `via=terraform`, or no `via` in `kustomization.yaml` or `kustomization.yml` |
| `infrastructure-dependency` | infrastructure | dependent to dependency; facet `family` is `chart`, `compose`, or `terraform` | `parsed-source-fact` | `exact` | `DEPENDS_ON` `via=depends_on` (compose), `DEPENDS_ON` with `repository` and no `via` (chart), `USAGE` `via=terraform` with `ref` in `var`, `module`, `output`, `resource` |
| `placed-in` | infrastructure | workload to hosting target (namespace); derived by Concordia from a workload node, with a derivation record | `parsed-source-fact` | `exact` | none (derived, no native edge) |
| `configures-env-access` | infrastructure | code reader to project-wide environment variable | `parsed-source-fact` | `exact` | `CONFIGURES` `strategy=env_access` |
| `configures-env-set` | infrastructure | workload, Compose service, or Dockerfile to scoped environment variable | `parsed-source-fact` | `exact` | `CONFIGURES` `strategy=env_set` |
| `configures-env-read` | infrastructure | scoped environment variable to code | `parsed-source-fact` | `exact` | `CONFIGURES` `strategy=env_read` |
| `configures-key-symbol` | infrastructure | configuration key to code symbol | `inferred-association` | `inferred` | `CONFIGURES` `strategy=key_symbol` |
| `configures-helm-values` | infrastructure | `values.yaml` to Chart | `parsed-source-fact` | `exact` | `CONFIGURES` `strategy=helm_values` |
| `references-secret` | infrastructure | workload to Secret | `parsed-source-fact` | `exact` | `USAGE` `ref` in `env`, `envFrom`, target kind `Secret` |
| `references-configuration` | infrastructure | workload to ConfigMap | `parsed-source-fact` | `exact` | `USAGE` `ref` in `env`, `envFrom`, target kind `ConfigMap` |
| `references-storage` | infrastructure | workload to PersistentVolumeClaim | `parsed-source-fact` | `exact` | `USAGE` `ref=volume`, target kind `PersistentVolumeClaim` |
| `references-certificate-secret` | infrastructure | Ingress to TLS Secret | `parsed-source-fact` | `exact` | `USAGE` `ref=tls` |
| `uses-runtime-identity` | infrastructure | workload to ServiceAccount | `parsed-source-fact` | `exact` | `USAGE` `ref=serviceAccountName` |
| `route-attaches-to-gateway` | infrastructure | HTTPRoute to Gateway | `parsed-source-fact` | `exact` | `USAGE` `ref=parentRef` |
| `binding-refers-to-role` | infrastructure | RoleBinding to Role | `parsed-source-fact` | `exact` | `USAGE` `ref=roleRef` |
| `binding-applies-to-principal` | infrastructure | RoleBinding to ServiceAccount | `parsed-source-fact` | `exact` | `USAGE` `ref=subject` |
| `policy-selects-workload` | infrastructure | NetworkPolicy to workload | `parsed-source-fact` | `exact` | `USAGE` `ref=podSelector` |
| `disruption-policy-target` | infrastructure | PodDisruptionBudget to workload | `parsed-source-fact` | `exact` | `USAGE` `ref=selector` |
| `scaling-target` | infrastructure | HorizontalPodAutoscaler to workload | `parsed-source-fact` | `exact` | `USAGE` `ref=scaleTarget` |
| `supersedes` | documentation | new decision to old decision | `parsed-source-fact` | `exact` | `SUPERSEDES` `via=markdown`, `syntax=supersedes` |
| `mentions` | documentation | documentation section to file | `inferred-association` | `inferred` | `MENTIONS` `syntax` in `link`, `code_path`, `path` |
| `references-file` | documentation | file to referenced file | `inferred-association` | `inferred` | `REFERENCES_FILE` `strategy` in `md_inline_link`, `md_backtick_path`, `md_bare_mention` |
| `diagram-arrow` | diagram | arrow source element to arrow target element | `parsed-source-fact` | `exact` | `DIAGRAM_ARROW` |
| `tests` | test | test entity to tested entity | `candidate-verification-artifact` | `inferred` | `TESTS` |
| `tests-file` | test | test file to tested file | `candidate-verification-artifact` | `inferred` | `TESTS_FILE` |
| `similar-to` | advisory | one entity to a similar entity | `inferred-association` | `inferred` | `SIMILAR_TO` |
| `semantically-related` | advisory | one entity to a related entity | `inferred-association` | `inferred` | `SEMANTICALLY_RELATED` |
| `file-changes-with` | advisory | file to co-changing file | `inferred-association` | `inferred` | `FILE_CHANGES_WITH` |
| `unsupported` | evidence-only | native direction | `parsed-source-fact` | `unresolved` | any native type without a rule; the fact keeps native kind, endpoints, location, and properties, and is never complete coverage (`CO-OBS-016`) |
| `ambiguous` | evidence-only | native direction | `parsed-source-fact` | `ambiguous` | a native type with rules where discriminators are missing, unlisted, or conflicting; the fact keeps the native kind and the facet `ambiguity` with the reason |

The `Direction` column is the direction of the fact: from the source endpoint of the native edge to its target. A rule applies only when its discriminators match: native properties (`via`, `ref`, `kind`, `strategy`, `syntax`, `api`), the target node kind, and the class of the source node (code or infrastructure file). Native properties that a rule does not use stay on the fact.

### Roles

| Role | Meaning |
|---|---|
| `declared-intent` | A person declared it as intended (CALM, policy, binding). |
| `parsed-source-fact` | The provider parsed it from a committed file. |
| `inferred-association` | The provider or Concordia inferred it. |
| `runtime-observation` | Observed in a running system. |
| `measured-result` | The result of a measurement or a test run. |
| `reviewed-judgment` | A person reviewed and recorded it. |
| `candidate-verification-artifact` | An artifact that may verify something, but is not a result. |

The CBM adapter produces `parsed-source-fact`, `inferred-association`, and `candidate-verification-artifact` only.

### Resolutions

| Resolution | Meaning |
|---|---|
| `exact` | The content was parsed exactly; no correspondence with anything else is implied. |
| `explicitly-bound` | A binding names the endpoint. |
| `inferred` | The endpoint or the association was inferred. |
| `ambiguous` | Discriminators are missing, unlisted, or conflicting. |
| `unresolved` | The endpoint could not be identified. |
| `unbound` | A supporting entity that no binding refers to. |

Resolution is ordered `exact`, `explicitly-bound`, `inferred`, `ambiguous`, `unresolved`; `unbound` ranks with `unresolved`. `Confidence` (`unknown` to `resolved`) is a separate dimension: it keeps its meaning and does not set the role or the resolution.

### Limits of the evidence

- `deploys-artifact` is a deployment action. It is never placement and never gives a CALM `deployed-in` association. A script that deploys a file with `kubectl` gives a fact to the file, not to the resources in it.
- `policy-selects-workload` is selection, not isolation. No claim of effective isolation follows from it, and its absence is not a claim of non-isolation.
- `mentions`, `references-file`, `tests`, and `tests-file` are candidates only. A test association is not a passed test.
- `similar-to`, `semantically-related`, and `file-changes-with` are advisory. No mandatory rule uses them.
- `configures-key-symbol` is an inferred association; it alone does not satisfy a control obligation. No `configures-*` fact holds an environment variable value.

## URL mapping

A CALM document can reference remote resources through the keys `$ref`, `requirement-url`, and `config-url`. Concordia never fetches a remote URL. Each remote reference must have an entry in the file named by `architecture.urlMapping`:

```json
{"https://example.org/concordia/controls/dependency-boundary/1.0/requirement.json":"resources/requirement.json"}
```

- The mapping is a JSON object from URL to local path. A relative path is resolved against the directory of the mapping file.
- A URL matches an entry exactly, or by its part before `#`.
- URLs that start with `https://calm.finos.org/release/1.2/meta/` or `https://calm.finos.org/release/1.3/meta/` need no entry.
- A remote reference without an entry gives `CO-INT-012` (exit 6).
- A relative `config-url` is read from the directory of the architecture document. A `config-url` that cannot be read gives `CO-INT-014` (`control-requirement-validation`) when the requirement schema of its `requirement-url` loads, as in the CALM CLI.
- A relative `details.detailed-architecture` is read from the workspace root, as the CALM CLI reads it from the working directory.
- Each mapped file is recorded in `.concordia.lock` with its SHA-256 digest. See [the lock file](workspace.md#concordialock).

## Patterns

`architecture.patterns` (or `model validate --pattern`) lists CALM patterns. A pattern is a JSON Schema. Concordia narrows each pattern with the decision answers (`relationship-type.options`) of the architecture, validates the architecture document against it, and reports a failure, or a `oneOf` decision without an answer, as `CO-INT-006`. The CALM CLI pattern rules lint the pattern itself (`CO-INT-014`, `CO-INT-015`). A pattern that cannot be read gives `CO-INT-001`; a pattern that is not a valid JSON Schema gives `CO-INT-001` with the rule `json-schema`; a pattern that refers to an unsupported CALM release gives `CO-INT-003`. A pattern checks the shape of the architecture; it does not show that the code agrees with it.

## Decorators

`architecture.decorators` (or `model validate --decorator`, repeatable) lists CALM decorator documents. An entry is a path or an `https` URL. `model validate`, `check`, `init`, `render`, and `bind` validate them. A decorator is one JSON object.

- Concordia validates the decorator against the CALM decorator definition of the CALM release of the architecture. A 1.2 architecture rejects the `$schema` and `target-type` properties. A failure gives `CO-INT-008`.
- If the decorator has a non-empty string `$schema`, Concordia also validates it against that standard. A failure gives `CO-INT-008` with `(standard <url>)` in the detail.
  - The FINOS standards `https://calm.finos.org/standards/deployment/1.0/deployment.decorator.standard.json` and `https://calm.finos.org/standards/deployment/1.0/kubernetes.decorator.standard.json` are built in.
  - Another `https` URL resolves through `architecture.urlMapping` or the remote lock, and is recorded in `.concordia.lock`.
  - Any other value is a path relative to the decorator file.
  - A `https://calm.finos.org/release/` URL gets no standard check.
- A decorator or standard that cannot be read gives `CO-INT-001`. A `unique-id` used by two decorators gives `CO-INT-004`; decorator IDs are not compared with architecture IDs.
- A `target` item names the architecture when it is a path, relative to the decorator file, that resolves to the architecture file, or when it equals the remote `architecture.source` URL. If no item does, the warning `CO-INT-010` is given and `applies-to` is not checked.
- Each `applies-to` item must be a `unique-id` somewhere in the architecture. An unknown ID gives `CO-INT-009`.

## C4 levels

`render` places each node at a C4 level. The level is, in order of precedence, a binding `projection.c4Level` or the node `concordia.c4-level` (the two must agree, otherwise `CO-PRJ-005`), then the default for the `node-type`. Source: `internal/projection/c4map.go`.

| `node-type` | Default C4 level |
|---|---|
| `actor` | `person` |
| `system` | `softwareSystem` |
| `ecosystem` | `softwareSystem` |
| `service` | `container` |
| `webclient` | `container` |
| `database` | `container` |
| `ldap` | `container` |
| `data-asset` | `container` |
| `network` | not projected (`CO-PRJ-006`, info) |
| any other value | `container` |

Parents come from `composed-of` relationships. A container without a software-system parent goes into a synthetic system named after `metadata.name`. A component needs a container parent through `composed-of`; without one it gives `CO-PRJ-005`.

## Related

- [Bindings reference](bindings.md) to tie nodes to code.
- [Policies reference](policies.md) for the rules that the controls select.
- [Bind code to architecture](../how-to/bind-code-to-architecture.md) and [write policies](../how-to/write-policies.md) for practical steps.
