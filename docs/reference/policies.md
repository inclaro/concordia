# Policies reference

A policy pack is a YAML file that holds rules. Concordia compiles each rule against the architecture and evaluates it on the graph. This page describes the `PolicyPack` format, the five predicates, selectors and their dimensions, typed rules (the `obligation` block), the `evidence` block, precedence, edge-kind tokens, and confidence levels.

For a task-oriented guide, see [Write policies](../how-to/write-policies.md). For the meaning of the verdicts, see [Results reference](results.md).

## PolicyPack format

Policy pack files are listed in the workspace key `policies.files`. A pack can also come from the binary or from a pinned vendored file: see [Packs and overrides](#packs-and-overrides). Both keys are described in the [Workspace reference](workspace.md). Each pack is validated against `internal/policy/schemas/policy.schema.json`. Unknown keys are rejected.

| Field | Type | Description |
|---|---|---|
| `apiVersion` | const, required | `concordia.io/v1alpha1`. |
| `kind` | const, required | `PolicyPack`. |
| `metadata.id` | string, required | Pack ID. |
| `metadata.version` | string, required | Pack version. |
| `selectors` | map of name to selector | Named selectors that rules reference with `ref`. |
| `precedence` | object | A versioned list of rule IDs that resolves conflicts between rules. See [Precedence and conflicts](#precedence-and-conflicts). |
| `rules` | array, required | The rules. |

### Rule fields

| Field | Type | Description |
|---|---|---|
| `id` | string, required | Matches `^[A-Z][A-Z0-9]*(-[A-Z0-9]+)+$`, for example `CO-BND-001`. A duplicate ID gives `CO-POL-003`. |
| `name` | string, required | Human-readable name. |
| `severity` | enum, required | `error`, `warning`, or `info`. |
| `rationale` | string | Why the rule exists. |
| `remediation` | string | How to fix a finding. |
| `owner` | string | Owner of the rule. A finding takes the owner of its source node when the node has one, otherwise the rule owner. |
| `family` | string | The rule family that enforcement is set for. Lowercase letters, digits, and `-`. |
| `enforcement` | `mandatory`, `advisory`, `shadow` | What a result of the rule does to the run. Only `mandatory` results block it. A typed rule without the key is `advisory`; the other rules gate as `mandatory`. A matching entry of `check.enforcement` in the workspace overrides this value (see [workspace](workspace.md#checkenforcement)). |
| `aggregation` | string | `edge` (default), `file`, `symbol` or `architecture-pair`. It says which edge findings become one finding: the fingerprint then leaves out the edge kind and the qualified names (`file`), the edge kind, target name and source file (`symbol`), or all of these (`architecture-pair`). Only rules with edge findings (`forbid`, `layerOrder`, `declaredOnly`, `denyPath`, `allowOnly`, `independent`, `publicApiOnly`, `ownership`) accept a value other than `edge`. Other values give `CO-POL-002`. |
| `evidence` | object | See [The `evidence` block](#the-evidence-block). |
| `subject` | selector | The nodes the rule applies to. Required for every predicate except `layerOrder`, `viewCurrency`, `decisions`, `assessmentCurrency`, `require.effectiveAccess`, `require.checkResult`, and `require.claim`, where it is not allowed (these name their own selectors or subjects). |
| one predicate | object | Exactly one of `forbid`, `layerOrder`, `declaredOnly`, `observedRequired`, `require`, `denyPath`, `allowOnly`, `independent`, `publicApiOnly`, `ownership`, `budget`, `decisions`, `assessmentCurrency`, or `viewCurrency`. A rule with an `obligation` block has no other predicate. `viewCurrency` (`check`: `presence`, `freshness`, or `correspondence`) belongs to the built-in `views` pack: `check` evaluates it from the declared views, and the compiler refuses it in a workspace policy. |
| `obligation` | object | A typed rule. See [Typed rules](#typed-rules-the-obligation-block). |
| `allow` | object | Allowed only together with `forbid`. |

## Selectors

A selector chooses CALM nodes. The result is the union of all its fields.

| Field | Type | Selects |
|---|---|---|
| `calmNodes` | list of strings | Nodes with these IDs. An ID that is not in the architecture gives `CO-POL-005`. |
| `nodeTypes` | list of strings | Nodes whose `node-type` is in the list. |
| `tags` | list of strings | Nodes that carry at least one of the tags. |
| `ref` | string | The named selector in `selectors`. Chains are followed; a cycle or unknown name gives `CO-POL-002`. |

Any other key gives `CO-POL-002`.

### Selecting the declared deployment topology: `deployedIn`

`deployedIn` is a boolean key that reads the `deployed-in` relationships of the architecture. It is valid only in an `obligation` block, as the whole `subject` or the whole `obligation.relation.target`. It cannot be combined with another key. In `obligation.applies`, in an operator, in a named selector, and in a rule without an `obligation` block it gives `CO-POL-002`.

| Place | Value | Selects |
|---|---|---|
| `subject` | `true` | The nodes that a `deployed-in` relationship lists as deployed nodes, and not the containers. `false` gives `CO-POL-002`. |
| `obligation.relation.target` | `true` | For each obligation, the containers of the `deployed-in` relationships that list the subject. |
| `obligation.relation.target` | `false` | For each obligation, every hosting target that is bound to a CALM element other than those containers. |

The rule names no ID, so a built-in pack can ship placement rules for a model that it does not know. `concordia policy verify` shows the key in the declaration line as `deployedIn=true` or `deployedIn=false`.

```yaml
- id: DEP-PLC-001
  name: A deployed service is placed in its declared container
  severity: error
  family: deployment-placement
  subject: { deployedIn: true }
  obligation:
    type: required
    relation: { kind: placed-in, direction: from-workload, target: { deployedIn: true } }
  evidence:
    minimumResolution: explicitly-bound
    completeness: complete
    passCompleteness: any
- id: DEP-PLC-002
  name: A deployed service is not placed anywhere else
  severity: error
  family: deployment-placement
  subject: { deployedIn: true }
  obligation:
    type: forbidden
    relation: { kind: placed-in, direction: from-workload, target: { deployedIn: false } }
  evidence:
    minimumResolution: explicitly-bound
```

The second rule is `forbidden` and the first is `required`, but they do not conflict: the targets of the two never overlap for one subject.

### Anchoring on images: `from-image`

The direction `from-image` anchors an obligation on the images that an `image` binding maps to the subject. The evidence fact leaves the image. It suits a relation that starts at the image, such as the build definition of the image (`built-from`):

```yaml
- id: DEP-IMG-002
  name: Each image of a deployable service has a build definition
  severity: error
  family: deployment-trace
  subject: { tags: [deployable] }
  obligation:
    type: required
    relation: { kind: built-from, direction: from-image }
  evidence:
    minimumResolution: exact
    completeness: complete
    passCompleteness: any
```

A build link exists only when a build command names the image, so the coverage of `built-from` is partial and an absence is `unknown`. A link that is present is a candidate and passes (`passCompleteness: any`).

### Selector dimensions

A selector in `obligation.applies` can also test properties of the evidence. A selector that uses a dimension has exactly one dimension, or exactly one of the operators `allOf`, `anyOf`, and `not`. The keys `calmNodes`, `nodeTypes`, `tags`, and `ref` keep their union meaning, and they stay the only keys of `subject` and of `obligation.relation.target`. A key from this list next to a dimension, two dimensions at one level, and any other key give `CO-POL-002`.

| Dimension | Value | Property of |
|---|---|---|
| `architecture` | `id`, `nodeType` (each one value or a list) | The CALM node of the obligation subject. |
| `observedKind` | value or list | The evidence kind of the fact. |
| `environment` | value or list | The environment of the fact. |
| `repository` | value or list | The repository (project) of the fact. |
| `workload` | `cluster`, `namespace`, `kind`, `name` | The Kubernetes workload of the fact. |
| `container` | `name`, `image` | The container occurrence of the fact. |
| `protocol`, `operation`, `interface` | value or list | The connection that the fact describes. |
| `evidenceRole` | value or list | The evidence role of the fact. |
| `trustContext`, `dataClassification`, `governanceScope` | value or list | Properties with no data source in this release. They are always unknown. |

A structured dimension needs all its properties to match; each property accepts one value or a list.

Selectors are evaluated with three values: true, false, and unknown. A missing property gives unknown, never false:

| Operator | Result |
|---|---|
| `not` | `not` of unknown is unknown. |
| `allOf` | False if one operand is false, else unknown if one is unknown, else true. |
| `anyOf` | True if one operand is true, else unknown if one is unknown, else false. |

A selector that is false for a fact means that the rule does not apply to that fact. A selector that is unknown makes the obligation `unknown` with the reason `selector-property-unknown`. It never makes the rule `not-applicable`.

```yaml
- id: DEP-CONN-002
  name: Production connections use HTTPS
  severity: error
  subject: { calmNodes: [billing-service] }
  obligation:
    type: forbidden
    relation: { kind: calls-service, direction: from-subject }
    applies:
      allOf:
        - environment: production
        - not: { protocol: https }
```

## Typed rules: the `obligation` block

A typed rule says what a subject needs, forbids, or allows in terms of one kind of evidence. Concordia builds one obligation for each subject and each environment, from the architecture, the rule scope, and the declared environments, whatever the graph holds. The obligation ID is the first 16 hexadecimal characters of the SHA-256 of the rule ID, the subject ID, the environment, and the relation. The same inputs give the same IDs in the same order.

```yaml
- id: DEP-IMG-001
  name: Every billing workload uses an image
  severity: error
  family: deployment
  enforcement: advisory
  subject: { calmNodes: [billing-service] }
  obligation:
    type: required
    relation: { kind: uses-image, direction: from-workload }
    scope: { environments: [production] }
    multiplicity: forAll
  evidence:
    roles: [parsed-source-fact, runtime-observation]
    minimumResolution: explicitly-bound
    completeness: complete
```

| Field | Type | Description |
|---|---|---|
| `obligation.type` | `required`, `forbidden`, `allowed`, required | `required`: the relation must exist. `forbidden`: each observed occurrence is a violation. `allowed`: each observed occurrence is a pass. |
| `obligation.applies` | selector | Which facts the rule applies to. See [Selector dimensions](#selector-dimensions). Default: all. |
| `obligation.relation.kind` | edge token | The evidence kind of the relation. The compiler checks it (`CO-POL-004`). |
| `obligation.relation.direction` | `from-subject`, `to-subject`, `from-workload`, `to-workload`, `from-image` | `from-*`: the fact leaves the entity. `to-*`: the fact enters it. `*-subject`: the entity is any observed entity that a binding maps to the subject. `*-workload`: the entity is a workload (Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob, Pod) that a binding maps to the subject. `from-image`: the entity is an `Image` entity that an active `image` binding maps to the subject. |
| `obligation.relation.target` | selector | The CALM nodes at the other end of the relation. Without it, any other end matches. The other end must have a binding; one that has none is a binding gap. `{deployedIn: true}` stands for the containers of the subject, `{deployedIn: false}` for every other bound hosting target. See [`deployedIn`](#selecting-the-declared-deployment-topology-deployedin). |
| `obligation.scope.environments` | list of environment IDs | The environments of the obligations. Default: every declared environment. An ID that the workspace does not declare keeps its obligation as `unknown` (`environment-unresolved`). `check --environment` leaves out the others. |
| `obligation.scope.repositories` | list of strings | The repositories (projects) that the entities may come from. Default: all. |
| `obligation.multiplicity` | `forAll`, `exists` | Default `forAll`. See below. |

### How an obligation is evaluated

Each fact of the relation kind that matches the direction, the target, and the environment gives one result for the obligation. The checks run in this order: environment, target binding, `applies`, evidence role, and minimum resolution. A fact that fails the role check or the resolution check can neither satisfy an obligation nor give a violation. The result is `unknown`.

- **`required` and `forAll`.** Each entity of the subject needs its own satisfying fact. One replica with the fact does not satisfy the others: they are `violation` or `unknown`, and the obligation is not `pass`.
- **`required` and `exists`.** One satisfying entity gives `pass`. The same holds for `forAll` when the bindings of the subject say `agreement: any`.
- **Absence.** A necessary fact that is absent is a `violation` only if the provider supports the family, the analysis succeeded, and the coverage of the predicate in the scope is `complete` (or the level that `completeness` asks for). Otherwise it is `unknown` with the reason of what is missing. A `forbidden` obligation with no occurrence is an absence too.
- **`forbidden`.** An admissible occurrence is a `violation`. With no occurrence, the result is `pass` only when the coverage allows that claim.
- **`allowed`.** Policy that allows one relation does not allow the others between the same entities. An observed connection of another kind between the subject and the target, for example `configured-connection` next to an allowed `calls-service`, that no rule decides is a `violation`. The allowed relation keeps its `pass`.
- **Direction.** A relation in the reverse direction never satisfies a `required` obligation, and it is evaluated separately. If the model declares the subject `deployed-in` a container that the rule names as target, the CALM direction applies: each listed node is deployed in the container, and a relation that places the container in the subject is an unexpected placement and a `violation`.
- **Placement target (`deployedIn`).** With `target: {deployedIn: true}`, only a fact whose other end is bound to a container of the subject counts: a placement in the container of another node does not. With `target: {deployedIn: false}`, a fact counts when its other end is bound to a CALM element other than those containers: an end that is bound to a container is not evaluated, an end with no binding gives `unknown` with the reason `binding-gap`, and an end with ambiguous bindings gives `ambiguous-binding`. A `forbidden` obligation with `deployedIn: false` also reads the hosting side of the fact, because a `placed-in` fact ends at the hosting target: a fact whose workload is bound to a container of the subject and whose hosting target is bound to the subject, in the environment of the obligation, is a `violation` of class `wrong-direction` (unexpected placement) with the location of the fact. It never satisfies a `required` obligation. A subject that no `deployed-in` relationship lists has an obligation that is `not-applicable`, because the model proves that the rule does not apply to it.
- **Image anchors (`from-image`).** The entities of the obligation are the `Image` entities that an active `image` binding maps to the subject. The environment of the obligation does not exclude them, and the source files and modules that other bindings map to the subject are not anchors. A subject that no active `image` binding maps to an image has an obligation that is `not-applicable`: a `proposed` binding takes no part in resolution.
- **Environments.** A fact in an environment that the rule does not cover never satisfies the obligation. It is shown as a separate result, `not-applicable`. A fact with no environment, in a kind that an environment owns, is `unknown`. The build lineage kinds `built-from`, `base-image`, and `copies-from-image` are the exception: they connect an image, which has no environment, and a Dockerfile that no environment source contains, so they are evidence in every environment whatever environment they have, including `unresolved`. The environment still limits the anchors, and every other kind keeps the rule above.
- **Conflicting facts.** Facts of the same role that disagree about a property give `unknown` with the reason `evidence-conflict`. No fact wins by order. Two manifests of one environment that declare one resource (same group, kind, namespace, and name) are competing declarations, not two instances: `CO-OBS-021` names both files, and the obligations of the workload give `unknown` with the same reason.
- **Possibly lossy facts.** A fact that the provider may have merged with others never gives a `pass` when the pass needs `complete` coverage, that is, under `completeness: complete` with no `passCompleteness`, or with `passCompleteness: complete`. Under `passCompleteness: partial` or `any` the fact can give a `pass`.
- **Capability.** A family that the lock profile supported and the current profile does not makes the related claims `unknown` with the reason `capability-regression`.

The `unknown` reasons form a closed set. See [the reasons for `unknown`](../explanation/verdicts-and-coverage.md#the-reasons-for-unknown). The gate counts the result of each occurrence, never the summary of a pair of nodes. `reconciliation` stays a separate report and does not contain infrastructure facts.

#### Pass and absence: `completeness` and `passCompleteness`

`completeness` is the coverage that an absence needs, and `passCompleteness` is the coverage that a pass needs. Without `passCompleteness`, a pass needs what `completeness` says. A rule whose presence is evidence on its own while its absence is not sets `completeness: complete` and `passCompleteness: any`: a present fact passes under partial coverage, and a missing one stays `unknown` until the coverage is `complete`. A claim that is blocked for another reason (a limit that the run went over, a capability regression, a failed scope, or an unsupported family) stays blocked for every value.

A rule about the whole set keeps the default. A rule that a single parsed fact proves sets `passCompleteness` lower:

```yaml
- id: DEP-TRC-001
  name: Each deployable service has a workload in each environment
  severity: error
  family: deployment-trace
  subject: { tags: [deployable] }
  obligation:
    type: required
    multiplicity: exists
    relation: { kind: placed-in, direction: from-workload }
  evidence:
    minimumResolution: exact
    completeness: complete
    passCompleteness: any
```

Under partial coverage this rule passes for a service with a placement fact and stays `unknown` (`coverage-incomplete`) for a service with none. With `passCompleteness: partial` a possibly lossy fact can pass as well; the default value of `complete` keeps the block.

`concordia policy verify` shows the contract of each typed rule on one line: `deployedIn=true` or `deployedIn=false` follows the relation kind when the target comes from the model, and `passCompleteness=<value>` follows `completeness` when the rule sets it. The direction `from-image` shows as the direction.

```text
  DEP-PLC-001 family=deployment-placement: required forAll from-workload placed-in deployedIn=true scope=any roles=parsed-source-fact,runtime-observation minimumResolution=explicitly-bound completeness=complete passCompleteness=any
```

## Predicates

### `forbid`

Reports an edge from a subject to a target when the edge kind is in `edges`.

| Field | Type | Description |
|---|---|---|
| `forbid.target` | selector, required | The forbidden targets. |
| `forbid.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |
| `forbid.traversal` | string | Only `direct` is accepted (`CO-POL-002` otherwise). Use `denyPath` for transitive paths. |
| `allow.target.sourceRoots` | list of globs | Edges whose target file matches a glob are not reported. |
| `allow.target.calmNodes` | list of node IDs | Edges to these nodes are not reported. |
| `allow.when.any[]` | items with one key: `edge` (token) or `targetTag` (string) | Edges of that kind, or to a target node with that tag, are not reported. |

Finding kind: `forbidden-dependency`.

### `layerOrder`

| Field | Type | Description |
|---|---|---|
| `layerOrder.layers` | list of selectors, at least 2 | Layers in order. The first selector is the top layer. |
| `layerOrder.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |

An edge from a node in layer `i` to a node in an earlier layer `j < i` is reported. A node that matches several layers takes the first one. Finding kind: `layer-violation`.

### `declaredOnly`

| Field | Type | Description |
|---|---|---|
| `declaredOnly.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |

An observed edge between two subjects is reported when the architecture declares no relationship between them (`undeclared`), or when the declared relationship does not allow the edge kind (`contradiction`). Evidence below the relationship's confidence threshold is counted as sub-threshold, not reported.

### `observedRequired`

`observedRequired` is an empty object (`observedRequired: {}`). The rule has no `edges`. It reports each declared `connects` or `interacts` relationship that has `concordia.observation.required: true` and no observed edge of an allowed kind between its source and target (finding kind `unobserved`). The allowed kinds and the minimum confidence come from the relationship's `concordia.observation` extension (see [Architecture reference](architecture.md)); without them, every supported code kind except the structural kinds `contains` and `defines`, and the rule's confidence apply. Infrastructure, documentation, test, and advisory kinds are never default kinds.

### `require` (acyclic)

| Field | Value | Description |
|---|---|---|
| `require.graphProperty` | `acyclic` | The only property. |
| `require.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |
| `require.collapseBy` | `architectureBinding` | Code entities are collapsed to their bound CALM nodes before cycles are searched. |

### `denyPath`

| Field | Type | Description |
|---|---|---|
| `denyPath.target` | selector, required | The nodes that no path from a subject may reach. |
| `denyPath.edges` | list of edge tokens, required, at least 1 | Edge kinds that a path may use. |
| `denyPath.maxDepth` | integer 1 to 10 | Longest path in edges. Default 4. |

Intermediate code nodes do not need a binding. Each (start node, end node, subject, target) gives one finding with the whole path as evidence. Finding kind: `forbidden-path`.

### `allowOnly`

| Field | Type | Description |
|---|---|---|
| `allowOnly.targets` | selector, required | The only nodes that a subject may use. |
| `allowOnly.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |

An edge from a subject to a node outside `targets` is reported. Finding kind: `disallowed-dependency`.

### `independent`

| Field | Type | Description |
|---|---|---|
| `independent.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |

An edge between two subjects is reported. The rule needs 2 or more subjects (`CO-CON-022` otherwise). Finding kind: `peer-dependency`.

### `publicApiOnly`

| Field | Type | Description |
|---|---|---|
| `publicApiOnly.target` | selector | The nodes whose API is public. Default: all CALM nodes. A selector that selects nothing gives `CO-CON-025`. |
| `publicApiOnly.api.sourceRoots` | list of globs, at least 1 | The files that form the public API. |
| `publicApiOnly.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |

An edge from a subject to a file of a target that matches no glob of `api.sourceRoots` is reported. Finding kind: `non-public-api`.

### `ownership`

| Field | Type | Description |
|---|---|---|
| `ownership.owners` | selector, required | The nodes that may access the subjects. |
| `ownership.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |

An edge into a subject from a node that is neither an owner nor a subject is reported. Finding kind: `ownership-violation`.

### `budget`

| Field | Type | Description |
|---|---|---|
| `budget.measure` | `forbidden-edges` or `cycles` | What is counted. |
| `budget.max` | integer, 0 or more | The most findings that the measure may have. |
| `budget.target` | selector | Required for `forbidden-edges` (the forbidden targets). Not allowed for `cycles`. |
| `budget.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |

When the count is above `max`, one finding reports the count and lists the other places as related locations. Finding kind: `budget-exceeded`.

### `require.edge` and `require.entity`

| Field | Type | Description |
|---|---|---|
| `require.edge.target` | selector, required | Each subject must have an edge to one of these nodes. |
| `require.edge.edges` | list of edge tokens, required, at least 1 | Edge kinds that count. |
| `require.entity.labels` | list of strings, at least 1 | Each subject must have a bound observed node with one of these labels (for example `Route`, `Class`). |
| `require.entity.namePattern` | RE2 pattern | The qualified name of the node must match. A pattern that does not compile gives `CO-POL-001`. |

Both give a finding only when the coverage requirements are met. Finding kinds: `missing-edge`, `missing-entity`.

The edge predicates `denyPath`, `allowOnly`, `independent`, `publicApiOnly` and `ownership` skip a pair of nodes when one is an ancestor of the other in `composed-of` relationships.

## Assurance rules: `require.effectiveAccess`, `require.checkResult`, and `require.claim`

Three `require` forms read the evidence of the [assurance adapters](assurance.md): the rendered and evaluated manifests, the check results, and the claim registry. They need the workspace key [`assurance`](workspace.md#assurance). Without it, or without the input that a rule needs, the result is `unknown` with a reason that names the missing input (see [Reasons](assurance.md#reasons)), never a pass. Exactly one of the forms is the predicate of a rule.

The three forms have the same properties:

- **No rule-level `subject`.** The rule names its own subjects (`from` and `to`, the optional `subject` of `checkResult`, or the claim). A rule that declares `subject` with one of them is refused with `CO-POL-001` and the message `'subject' is not permitted with require.<form>`.
- **Advisory maturity.** The family contract is not yet stable, so these rules run as `advisory` or `shadow`: making one `mandatory` stops `check` with `CO-POL-018` (exit 2). Their results and findings are reported, and a claim of an experimental contract caps the severity of its findings at `warning`.
- **Not edge predicates.** The findings are not edges, so `check --changed` never reduces them. The rules report in the area `conformance`; the rules that quality scenarios make report in `qualityEvaluation`, see [Areas](results.md#areas).
- **Enforcement.** A rule without `enforcement` is `advisory`. Set a mode per rule, family, or environment with [`check.enforcement`](workspace.md#checkenforcement). The families are `effective-access`, `check-result`, and `assurance-claim` unless the rule sets `family`.

### `require.effectiveAccess`

```yaml
- id: ORG-NET-001
  name: The public gateway has no network path to the database
  severity: error
  require:
    effectiveAccess:
      dimension: network
      from: { calmNodes: [public-gateway] }
      to: { calmNodes: [orders-db] }
      expect: denied
      ports: [5432]
```

The rule asks an evaluator what an environment allows between two sets of workloads. The evaluators read the rendered documents of the environment, never the code graph: a missing selection edge is not isolation.

| Field | Type | Description |
|---|---|---|
| `require.effectiveAccess.dimension` | `network` or `authorization`, required | `network` asks the `network-policy` evaluator, `authorization` asks the `rbac` evaluator. Any other value gives `CO-POL-002` with the supported values. |
| `require.effectiveAccess.from` | selector, required | The subjects: workloads that connect (`network`) or that act (`authorization`). |
| `require.effectiveAccess.to` | selector, required | The targets. A pair of one element with itself says nothing and is left out. |
| `require.effectiveAccess.expect` | string, required | `denied` or `allowed` for `network`; `not-granted` or `granted` for `authorization`. Another value, or a value of the other dimension, gives `CO-POL-002`. |
| `require.effectiveAccess.ports` | list of integers 1 to 65535, at least 1, unique | The destination ports to query. Only for `network`: with `authorization` the compiler gives `CO-POL-002`. Without ports, Concordia queries the ports that the destination declares, every port that a policy rule names, and one port that no rule names, so a policy that opens only unnamed ports is not missed. |

The rule is evaluated for each environment of the run, each pair of subject and target, and each port. `expect: denied` is violated by one open port; `expect: allowed` is met by one open port. A closed answer (`denied` or `not-granted`) holds only when the enforcement that it assumes is declared under `assurance.evaluators[].assumes`; an answer of `allowed` or `granted` needs no assumption. Reasons of an `unknown`: `no-evaluator-available` (no evaluator of the kind is enabled for the environment), `rendered-evidence-missing`, `coverage-incomplete` (a namespace whose render is not complete), `enforcement-not-declared`, and `claim-not-enabled`. The report lists each query in [`assurance.evaluations`](results.md#evaluations).

### `require.checkResult`

```yaml
- id: ORG-VER-001
  name: The unit tests pass in production
  severity: error
  require:
    checkResult:
      check: unit-tests
      environment: production
```

| Field | Type | Description |
|---|---|---|
| `require.checkResult.check` | string, required | The check ID. The compiler refuses an ID that no control lists in `check-ids` and that no result source of the workspace defines: `CO-POL-005` (exit code `2`), so that a typo does not pass as `unknown`. |
| `require.checkResult.environment` | string | The environment of the result. Without it, the rule gives one result for each environment that a result of the check names, and the enforcement mode of each environment applies. |
| `require.checkResult.subject` | selector | The elements that the result must cover. With it, a result counts for a subject only when its `covers` list names it. The `subject` here is the one of the clause; the rule itself has none. |

The verdict follows the admissible results of the check: `passed` gives `pass`, `failed` or `errored` gives `violation`. Results that disagree give `unknown` with `evidence-conflict`. A `skipped` result, only inadmissible results, or no result give `unknown` with `no-evidence-supplied`. A `TESTS` edge of the code graph is shown beside the missing result as a candidate and never gives a verdict. See [Verification results](assurance.md#verification-results).

### `require.claim`

```yaml
- id: ORG-ASR-001
  name: Each environment has complete rendered evidence
  severity: warning
  family: assurance
  require:
    claim:
      id: complete-resource-set
```

| Field | Type | Description |
|---|---|---|
| `require.claim.id` | string, required | A claim of the registry, in lowercase words joined by `-` (for example `exact-lineage`). The compiler does not check the ID against the registry: the registry is read when the rule is evaluated. |
| `require.claim.environment` | string | The environment to evaluate. Without it, the rule runs for the environments of the run. |

The claim names its own subjects: an environment for the rendering claims, and a container occurrence for the lineage and deployment claims. A claim that no loaded contract enables, including an unknown ID, is `unknown` with `claim-not-enabled`. Seven claims have an evaluation in `require.claim`: `complete-resource-set`, `container-occurrence-versions`, `resolved-selections`, `exact-lineage`, `deployed-digest`, `deployment-succeeded`, and `runtime-drift`. The other registered claims are evaluated by `require.effectiveAccess`, `require.checkResult`, and quality scenarios, and a `require.claim` rule that names one of them is `unknown`, never a pass. The claims, their contracts, and their minimum evidence are in [Assurance](assurance.md#claims-and-contracts). The rules of the built-in pack `builtin:assurance` use this form: see [Assurance rules](assurance-rules.md).

## Decision governance rules: the `decisions` key

A rule with a `decisions` key is a decision-governance rule. It has no `subject` and no edge predicate: `check` evaluates it from the [decision register](adr-governance.md) and the committed ADR documents, and it reports in the area `adrGovernance`. The family is `decision-governance`; it is not an edge family, so `--changed` never reduces it.

```yaml
- id: GOV-ADR-004
  name: A decision that requires enforcement links a mandatory rule
  severity: error
  decisions: { obligation: enforcement }
```

| Field | Type | Description |
|---|---|---|
| `decisions.obligation` | string, required | `presence`, `applicability`, `justification`, `enforcement`, `history`, or `scope`. An unknown name gives `CO-POL-002`. |

The built-in pack `builtin:adr-governance` (version `1.0.0`) has one rule for each obligation. Its rules declare no enforcement mode, so they are `mandatory` by default; to roll the pack out gradually, set `advisory` or `shadow` with `check.enforcement` (family `decision-governance`).

| Rule | Obligation | Severity | Checks |
|---|---|---|---|
| `GOV-ADR-001` | `presence` | error | A registered decision is committed in the evaluated revision. |
| `GOV-ADR-002` | `applicability` | error | A registered decision is active for what it governs. |
| `GOV-ADR-003` | `justification` | error | A mandatory rule is justified by an active decision. |
| `GOV-ADR-004` | `enforcement` | error | A decision with `enforcement: required` links a mandatory rule. |
| `GOV-ADR-005` | `history` | error | The supersession history is consistent. |
| `GOV-ADR-006` | `scope` | warning | An architecturally significant change has a decision. |

Enable the pack with `policies.packs: [{use: builtin:adr-governance}]`. The pack without an `adr` register gives `not-applicable` rules, and a register without the pack gives `CO-ADR-007` only. The rules are not part of `core`; see [core rules](core-rules.md#decision-governance).

## Review currency rules: the `assessmentCurrency` key

A rule with an `assessmentCurrency` key is a review rule. It has no `subject` and no edge predicate: `check` evaluates it from the [assessments](assessments.md) of the workspace and their verified approvals, and it reports in the area `assessmentCurrency`. The family is `assessment-currency`; it is not an edge family, so `--changed` never reduces it. The currency of an assessment does not depend on which files changed.

```yaml
- id: REV-APR-001
  name: A required assessment has a current verified approval
  severity: error
  family: assessment-currency
  assessmentCurrency: approval
```

| Field | Type | Description |
|---|---|---|
| `assessmentCurrency` | string, required | `approval`, `disposition`, `capability`, or `governance`. Any other value is a policy error (`CO-POL-001`). |

The built-in pack `builtin:review-currency` (version `1.0.0`) has one rule for each check. Its rules declare no enforcement mode, and the rules of this family are `advisory` by default and cannot be `mandatory` while the maturity of the family is advisory (`CO-POL-018`): set `mandatory`, `advisory`, or `shadow` with `check.enforcement` (family `assessment-currency`, or `pack: review-currency`). A verdict does not depend on the mode.

| Rule | Check | Severity | Checks |
|---|---|---|---|
| `REV-APR-001` | `approval` | error | A required assessment has a current verified approval. `current` passes; `review-required`, `expired`, `revoked`, and no approval are violations; `unknown` is `unknown` with one reason (`target-not-declared`, `trust-anchor-unreadable`, `verification-unavailable`, `reference-unresolved`, `unsupported-family`, `coverage-incomplete`, or `limit-exceeded`). |
| `REV-DSP-001` | `disposition` | error | A dependency that an approval covered and that is now removed or no longer selected has a verified disposition (`removed-intentionally`, `retired`, or `moved`). One violation for each removed item without one. |
| `REV-CAP-001` | `capability` | error | When the capability profile no longer supports a dependency kind or selector facet that an approved assessment uses, the loss is shown and the dependency is `unknown`. |
| `REV-GOV-001` | `governance` | error | A candidate change to the review rules, the `check.enforcement` entries for review rules, or an authority that Concordia cannot show to be stricter than the trust anchor has a governance disposition. One violation for each relaxation; `unknown` when the trust anchor cannot be read. |

Enable the pack with `policies.packs: [{use: builtin:review-currency}]`. The pack without required assessments gives four `not-applicable` rules and `areas.assessmentCurrency` is `not-configured`. The rules are not part of `core`; see [core rules](core-rules.md#review-currency). A `mandatory` mode for these rules needs one authority and a trust anchor (`CO-ASM-004`, exit 2) and the guarantees of the enforcement check (`CO-POL-018`). An active `check.rollback` runs them as `shadow`.

## The `evidence` block

| Field | Type | Description |
|---|---|---|
| `minimumConfidence` | `unknown`, `heuristic`, `syntactic`, `runtime`, `resolved` | Edges below this confidence never produce a finding. |
| `edgeKinds` | map of edge token to confidence | A minimum confidence for single edge kinds. It replaces `minimumConfidence` for these kinds. A kind that the rule does not traverse gives `CO-POL-001`. |
| `minimumCoverage` | number 0 to 1 | Minimum share of in-scope files that the graph covers. |
| `coverage.files` | number 0 to 1 | Alias of `minimumCoverage`. Setting both gives `CO-POL-001`. |
| `coverage.bytes` | number 0 to 1 | Minimum share of the bytes of the in-scope files that the graph covers. Not met: `CO-CON-028`. No default: without it, bytes are reported but not required. |
| `coverage.supportedLanguages` | number 0 to 1 | Minimum share of the files of each language. Default: `minimumCoverage`. Not met: `CO-CON-026`. |
| `onUnknown` | `fail`, `warn`, `ignore` | What an `unknown` verdict does to the run. |
| `roles` | list of evidence roles | Only in a rule with an `obligation` block. The roles that the rule accepts: `declared-intent`, `parsed-source-fact`, `inferred-association`, `runtime-observation`, `measured-result`, `reviewed-judgment`, `candidate-verification-artifact`. Default: `parsed-source-fact` and `runtime-observation`. A fact of another role is never evidence for the rule. |
| `minimumResolution` | `exact`, `explicitly-bound`, `inferred`, `ambiguous`, `unresolved` | Only in a typed rule. The weakest resolution of a fact that counts. Default `explicitly-bound`. |
| `completeness` | `any`, `partial`, `complete` | Only in a typed rule. The coverage of the predicate that an absence needs, and that a pass needs when `passCompleteness` is not set. Default `complete`. |
| `passCompleteness` | `any`, `partial`, `complete` | Only in a typed rule. The coverage of the predicate that a pass needs, while `completeness` stays what an absence needs. Default: the value of `completeness`. See [pass and absence](#pass-and-absence-completeness-and-passcompleteness). |

Effective values come from the first source that sets them. Defaults: `minimumConfidence: syntactic`, `minimumCoverage: 0.95`, `onUnknown: fail`. A workspace [rule override](workspace.md) can set `edgeKinds`, `coverage.bytes` and `coverage.supportedLanguages`; its value replaces the value of the rule.

| Setting | Order (first wins) |
|---|---|
| `minimumConfidence` | Rule `evidence`, default. No CLI option or workspace key sets it. |
| `minimumCoverage` | CLI `--minimum-coverage`, CALM control, rule `evidence`, workspace `check.minimumCoverage`, default. |
| `onUnknown` | CLI `--unknown`, CALM control, rule `evidence`, workspace `check.unknown`, default. |

A control carries only coverage and unknown handling; it does not set confidence. When several controls attach to one rule, the highest coverage and the strictest disposition apply.

## Edge-kind tokens

Edge lists accept an alias token or a canonical kind (kebab-case). A token names a set of canonical kinds. An unknown token gives `CO-POL-004`. `unsupported` and `ambiguous` are not tokens. For the kinds, directions, roles, and resolutions see [Canonical edge kinds](architecture.md#canonical-edge-kinds).

| Token | Kinds | Native CBM edge | Support in CBM 0.12.1 |
|---|---|---|---|
| `contains` | `contains` | `CONTAINS_FILE`, `CONTAINS_FOLDER` | supported, stable |
| `defines` | `defines` | `DEFINES`, `DEFINES_METHOD` | supported, stable |
| `IMPORTS`, `imports` | `imports` | `IMPORTS` (code) | supported, stable |
| `USES_TYPE`, `uses-type` | `uses-type` | none | unsupported, advisory |
| `inherits`, `INHERITS` | `inherits` | `INHERITS` | supported, stable |
| `implements`, `IMPLEMENTS` | `implements` | `IMPLEMENTS` | supported, stable |
| `CALLS`, `calls` | `calls` | `CALLS` | supported, stable |
| `CALL_REFERENCE`, `references-callable` | `references-callable` | `CALL_REFERENCE` | supported, stable |
| `USAGE`, `ambiguous-usage` | `ambiguous-usage` | `USAGE` without `ref` | supported, stable |
| `constructs` | `constructs` | none | unsupported, advisory |
| `READS`, `reads` | `reads` | `READS` | supported, stable |
| `WRITES`, `writes` | `writes` | `WRITES` | supported, stable |
| `handles-route` | `handles-route` | `HANDLES` | supported, stable |
| `HTTP_CALLS`, `calls-service` | `calls-service` | `HTTP_CALLS`, `CROSS_HTTP_CALLS` | supported, stable |
| `ASYNC_CALLS`, `dispatches-async` | `dispatches-async` | `ASYNC_CALLS`, `CROSS_ASYNC_CALLS` | supported, stable |
| `EMITS`, `emits-event` | `emits-event` | `EMITS`, `CROSS_CHANNEL` | supported, stable |
| `LISTENS_ON`, `listens-event` | `listens-event` | `LISTENS_ON` | supported, stable |
| `DATA_FLOWS`, `data-flows` | `data-flows` | `DATA_FLOWS` | supported, stable |
| `calls-grpc` | `calls-grpc` | `GRPC_CALLS`, `CROSS_GRPC_CALLS` | unsupported, advisory |
| `calls-graphql` | `calls-graphql` | `GRAPHQL_CALLS`, `CROSS_GRAPHQL_CALLS` | unsupported, advisory |
| `calls-trpc` | `calls-trpc` | `TRPC_CALLS`, `CROSS_TRPC_CALLS` | unsupported, advisory |
| `DEPLOYS`, `deploys`, `deploys-artifact` | `deploys-artifact` | `DEPLOYS` | supported, advisory |
| `selects-workload` | `selects-workload` | `INFRA_MAPS` `kind=selector` | partial, advisory |
| `routes-to` | `routes-to` | `INFRA_MAPS` `kind` in `ingress`, `route`, `httproute` | partial, advisory |
| `configured-connection` | `configured-connection` | `CONNECTS_TO` `via=env` | supported, advisory |
| `uses-image` | `uses-image` | `USAGE` `ref=image` | supported, advisory |
| `built-from` | `built-from` | `DEPENDS_ON` `via=build` | partial, advisory |
| `base-image` | `base-image` | `DEPENDS_ON` `via=from` | supported, advisory |
| `copies-from-image` | `copies-from-image` | `DEPENDS_ON` `via=copy-from` | partial, advisory |
| `launches` | `launches` | `SPAWNS` | supported, advisory |
| `overrides` | `overrides` | `OVERRIDE` | partial, advisory |
| `includes-infrastructure` | `includes-infrastructure` | `IMPORTS` `via=terraform` or Kustomization | supported, advisory |
| `infrastructure-dependency` | `infrastructure-dependency` | `DEPENDS_ON`, `USAGE` `via=terraform` | supported, advisory |
| `placed-in` | `placed-in` | none (derived by Concordia from a workload node) | supported, advisory |
| `CONFIGURES`, `configures` | `configures-env-access`, `configures-env-set`, `configures-env-read`, `configures-key-symbol`, `configures-helm-values` | `CONFIGURES` | each kind below |
| `configures-env-access` | `configures-env-access` | `CONFIGURES` `strategy=env_access` | supported, advisory |
| `configures-env-set` | `configures-env-set` | `CONFIGURES` `strategy=env_set` | supported, advisory |
| `configures-env-read` | `configures-env-read` | `CONFIGURES` `strategy=env_read` | supported, advisory |
| `configures-key-symbol` | `configures-key-symbol` | `CONFIGURES` `strategy=key_symbol` | unsupported, advisory |
| `configures-helm-values` | `configures-helm-values` | `CONFIGURES` `strategy=helm_values` | supported, advisory |
| `references-secret` | `references-secret` | `USAGE` `ref` in `env`, `envFrom` | supported, advisory |
| `references-configuration` | `references-configuration` | `USAGE` `ref` in `env`, `envFrom` | supported, advisory |
| `references-storage` | `references-storage` | `USAGE` `ref=volume` | supported, advisory |
| `references-certificate-secret` | `references-certificate-secret` | `USAGE` `ref=tls` | supported, advisory |
| `uses-runtime-identity` | `uses-runtime-identity` | `USAGE` `ref=serviceAccountName` | supported, advisory |
| `route-attaches-to-gateway` | `route-attaches-to-gateway` | `USAGE` `ref=parentRef` | supported, advisory |
| `binding-refers-to-role` | `binding-refers-to-role` | `USAGE` `ref=roleRef` | supported, advisory |
| `binding-applies-to-principal` | `binding-applies-to-principal` | `USAGE` `ref=subject` | supported, advisory |
| `policy-selects-workload` | `policy-selects-workload` | `USAGE` `ref=podSelector` | partial, advisory |
| `disruption-policy-target` | `disruption-policy-target` | `USAGE` `ref=selector` | partial, advisory |
| `scaling-target` | `scaling-target` | `USAGE` `ref=scaleTarget` | supported, advisory |
| `supersedes` | `supersedes` | `SUPERSEDES` | supported, advisory |
| `mentions` | `mentions` | `MENTIONS` | supported, advisory |
| `references-file` | `references-file` | `REFERENCES_FILE` | supported, advisory |
| `diagram-arrow` | `diagram-arrow` | `DIAGRAM_ARROW` | supported, advisory |
| `tests` | `tests` | `TESTS` | supported, advisory |
| `tests-file` | `tests-file` | `TESTS_FILE` | supported, advisory |
| `similar-to` | `similar-to` | `SIMILAR_TO` | partial, advisory |
| `semantically-related` | `semantically-related` | `SEMANTICALLY_RELATED` | partial, advisory |
| `file-changes-with` | `file-changes-with` | `FILE_CHANGES_WITH` | partial, advisory |

### Token meanings

A token has one meaning. It is the same set of kinds in every workspace and run, and no setting changes it.

- `IMPORTS` (or `imports`) matches code imports only. A native `IMPORTS` edge of Terraform (`via=terraform`) or of a `kustomization.yaml` is `includes-infrastructure`. A native `IMPORTS` edge with no `via` whose source file is neither code nor a Kustomize file is `ambiguous` (`CO-OBS-019`). No rule that names `IMPORTS` counts either of them.
- `USAGE` (or `ambiguous-usage`) matches code usage only: a native `USAGE` edge with no `ref`. A `USAGE` edge with a `ref` is selected by its own token (`uses-image`, `references-secret`, `references-configuration`, and the others in the table), and a Terraform reference by `infrastructure-dependency`. No facet token matches `ambiguous-usage`.
- `CONFIGURES` (or `configures`) matches all five strategy kinds: `configures-env-access`, `configures-env-set`, `configures-env-read`, `configures-key-symbol`, `configures-helm-values`. Name one strategy token to narrow the rule to that strategy.
- `deploys` (and `DEPLOYS`, `deploys-artifact`) matches `deploys-artifact`. The evidence is the native `DEPLOYS` edges with `via` of `kubectl`, `helm`, or `chart`. A `kubectl apply -f` edge ends at the applied file, not at the resources inside it. A deployment action is never placement.

The pair reconciliation (`reconciliation` in `results.json`) considers the code kinds only. A fact of an infrastructure, configuration, reference, documentation, diagram, protocol, or test kind never creates or changes a reconciliation entry; a rule that names such a kind gates on its own findings. The `core` rules that read `IMPORTS` can give other results than before: see [Core rules](core-rules.md#results-that-can-change).

A rule that lists an unsupported kind (`uses-type`, `constructs`, `calls-grpc`, `calls-graphql`, `calls-trpc`, `configures-key-symbol`) compiles with the warning `CO-POL-009`. The rule then has no evidence for that kind. A kind that the profile supports (for example `deploys-artifact`) never gives `CO-POL-009`; the rule ends as `pass` or `violation`, or as `unknown` for coverage reasons only.

## Confidence levels

Ranks from weakest to strongest: `unknown` (0), `heuristic` (1), `syntactic` (2), `runtime` (3), `resolved` (4). The CBM adapter (`concordia-cbm/v2`) assigns:

| Native edge | Confidence |
|---|---|
| `CALL_REFERENCE`, `INHERITS`, `IMPLEMENTS`, `DEFINES`, `DEFINES_METHOD`, `CONTAINS_FILE`, `CONTAINS_FOLDER` | `resolved` |
| `IMPORTS`, `READS`, `WRITES`, `DEPLOYS`, `DEPENDS_ON`, `INFRA_MAPS`, `CONNECTS_TO`, `SPAWNS`, `OVERRIDE`, `SUPERSEDES`, `DIAGRAM_ARROW` | `syntactic` |
| `USAGE` with `ref` (or `via=terraform`) | `syntactic` |
| `CONFIGURES` with `strategy` `env_access`, `env_set`, `env_read`, `helm_values` | `syntactic` |
| `USAGE` without `ref` (code usage), `HTTP_CALLS`, `ASYNC_CALLS`, `EMITS`, `LISTENS_ON`, `DATA_FLOWS`, `HANDLES`, `CROSS_HTTP_CALLS`, `CROSS_ASYNC_CALLS`, `CROSS_CHANNEL`, `GRPC_CALLS`, `GRAPHQL_CALLS`, `TRPC_CALLS`, `CROSS_GRPC_CALLS`, `CROSS_GRAPHQL_CALLS`, `CROSS_TRPC_CALLS` | `heuristic` |
| `CONFIGURES` with `strategy=key_symbol` | `heuristic` |
| `MENTIONS`, `REFERENCES_FILE`, `TESTS`, `TESTS_FILE`, `SIMILAR_TO`, `SEMANTICALLY_RELATED`, `FILE_CHANGES_WITH` | `heuristic` |
| `CALLS` with a `strategy` that starts with `lsp_` | `resolved` |
| `CALLS` with a numeric `confidence` property of 0.5 or more | `syntactic` |
| any other `CALLS` | `heuristic` |

The adapter does not produce `runtime`. Confidence is separate from the evidence role and resolution (see [Canonical edge kinds](architecture.md#canonical-edge-kinds)).

## Packs and overrides

The workspace key `policies.packs` adds packs to the rules of `policies.files`. A pack has the same format as any policy file. Each entry has one source in `use`:

| `use` | Meaning | Pin |
|---|---|---|
| `builtin:<name>` | A pack that ships inside the binary. `concordia policy list` shows the names: `core`, `infrastructure`, `adr-governance`, `review-currency`, `assurance`, and `views`. The built-in pack `core` has the rules of [core rules](core-rules.md). The pack `infrastructure` has `INFRA-PLC-001`, `INFRA-PLC-002`, and `INFRA-TRC-001` to `INFRA-TRC-004`; see [infrastructure rules](infrastructure-rules.md). The pack `assurance` has `ASR-REN-001`, `ASR-PRV-001`, and `ASR-PRV-002`; see [assurance rules](assurance-rules.md). The pack `views` has `VIEW-PRES-001`, `VIEW-FRESH-001`, and `VIEW-CORR-001`; `check` evaluates them from the `views` of the workspace, so do not list it in `policies.packs` (the compiler refuses it with `CO-POL-001`). See [results](results.md#views). The pack `adr-governance` has `GOV-ADR-001` to `GOV-ADR-006`; see [Decision governance rules](#decision-governance-rules-the-decisions-key). The pack `review-currency` has `REV-APR-001`, `REV-DSP-001`, `REV-CAP-001`, and `REV-GOV-001`; see [Review currency rules](#review-currency-rules-the-assessmentcurrency-key). | The binary pins it. A `sha256` is an error (`CO-POL-013`). |
| a workspace-relative path | A vendored file, usually in `policies/vendor/`. | `sha256` is required. Concordia checks it each time it reads the file (`CO-POL-010` when it differs). |
| a URL | Not allowed (`CO-POL-014`). Run `concordia policy add <url>` to vendor the file. | |

`check` never uses the network. Only `concordia policy add` can fetch a URL.

A rule ID must be unique over all packs and files. A second declaration gives `CO-POL-003`. Rule IDs of the built-in `core` pack start with `CORE-`.

### Overrides

An entry can change single rules of its pack, without a copy of the pack. `rules` maps a rule ID to an override:

```yaml
policies:
  packs:
    - use: builtin:core
      rules:
        CORE-DECL-001: { severity: warning }
        CORE-SEC-001: { enabled: false }
        CORE-CYC-001: { evidence: { minimumCoverage: 0.8 }, owner: team-platform }
```

| Override | Effect |
|---|---|
| `severity` | The rule has this severity. Findings, baselines, and reports use it. |
| `enabled: false` | The rule is dropped from the run. Its ID stays known, so `--rule <id>` and CALM controls that name it do not fail. |
| `evidence` | The keys that you set replace the keys of the `evidence` block of the rule. The other keys stay. |
| `owner` | The rule has this owner. |

An override for a rule ID that the pack does not declare gives `CO-POL-015`. This guards against typos. The `evidence` override has the same precedence as the `evidence` block of the rule (see [The `evidence` block](#the-evidence-block)): a CLI option or a CALM control still wins.

### Pins and the lock file

`.concordia.lock` records each pack of `policies.packs` with its `id`, `version`, `source`, and `sha256`. A pack whose digest differs from the lock gives `CO-INT-012` (exit 6), as for a resource. See [Workspace reference](workspace.md#concordialock).

### Rule IDs and baselines

Baselines, waivers, and fingerprints use the rule ID. A pack author must never reuse or renumber a rule ID. See [use policy packs](../how-to/use-policy-packs.md#write-and-publish-your-own-pack).

## Precedence and conflicts

Two rules contradict each other when they apply to the same obligation or the same evidence occurrence and one requires or allows the relation while the other forbids it. Concordia finds this from the obligations before it reads the graph. It compares the subject, the environment, and the relation (kind, direction, and overlapping targets). Rules whose `applies` selectors cannot match the same fact do not conflict: a rule that allows `protocol: https` and a rule that forbids `protocol: administrative` apply to different occurrences. A target that a rule takes from the model with [`deployedIn`](#selecting-the-declared-deployment-topology-deployedin) is compared for each subject: the containers of a subject and the other hosting targets of the same subject never overlap, so a `required` rule on `deployedIn: true` and a `forbidden` rule on `deployedIn: false` do not conflict.

No rule has implicit priority, and the first match never wins. A conflict stops `check` with `CO-POL-017` (exit code 2) and names both rules and the subject. A pack can resolve the conflict with an explicit, versioned list. A rule listed earlier wins over a rule listed later:

```yaml
apiVersion: concordia.io/v1alpha1
kind: PolicyPack
metadata: { id: deployment-pack, version: 1.0.0 }
precedence:
  version: 1
  order: [DEP-ROUTE-002, DEP-ROUTE-001]
rules: []
```

The list resolves only a pair in which both rules appear. For any other pair the conflict stays. The rule that loses stays in the results: its `supersededBy` field names the winner, the pack, and the precedence version, and its obligation results stay visible. They do not decide the verdict or the gate.

## Related diagnostics

`CO-POL-001` to `CO-POL-005`, `CO-POL-009`, `CO-POL-010` to `CO-POL-018`, and `CO-CON-021` to `CO-CON-024`: see [Diagnostics reference](diagnostics.md).

## Next steps

- [Write policies](../how-to/write-policies.md)
- [Resolve unknown verdicts](../how-to/resolve-unknown-verdicts.md)
- [Bindings reference](bindings.md)
